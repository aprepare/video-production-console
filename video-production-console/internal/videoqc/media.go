package videoqc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"video-production-console/internal/refanalysis"
)

type boundedBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (b *boundedBuffer) Len() int      { return b.buffer.Len() }
func (b *boundedBuffer) Bytes() []byte { return b.buffer.Bytes() }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		return 0, errors.New("command output too large")
	}
	return b.buffer.Write(p)
}

func inspectMedia(ctx context.Context, ffmpeg, ffprobe, video string) (refanalysis.VideoInfo, []float64, error) {
	if ffmpeg == "" || ffprobe == "" {
		return refanalysis.VideoInfo{}, nil, errors.New("请先配置 FFmpeg 和 FFprobe")
	}
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	cmd := exec.CommandContext(probeCtx, ffprobe, "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=width,height,avg_frame_rate,r_frame_rate,duration:format=duration", "-of", "json", video)
	probe := &boundedBuffer{limit: 64 << 10}
	cmd.Stdout = probe
	err := cmd.Run()
	cancel()
	if err != nil {
		return refanalysis.VideoInfo{}, nil, errors.New("无法读取视频信息，请确认文件是可播放的视频")
	}
	info, err := parseProbe(probe.Bytes())
	if err != nil {
		return info, nil, err
	}
	sceneCtx, cancelScene := context.WithTimeout(ctx, 5*time.Minute)
	defer cancelScene()
	cmd = exec.CommandContext(sceneCtx, ffmpeg, "-hide_banner", "-nostdin", "-i", video, "-map", "0:v:0", "-vf", "scale=320:-1,select='gt(scene,0.32)',showinfo", "-an", "-fps_mode", "vfr", "-f", "null", "-")
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return info, nil, errors.New("无法启动视频切镜识别")
	}
	if err = cmd.Start(); err != nil {
		return info, nil, errors.New("无法启动视频切镜识别")
	}
	cuts := []float64{}
	scanner := bufio.NewScanner(stderr)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		for _, match := range ptsTimePattern.FindAllStringSubmatch(scanner.Text(), -1) {
			value, err := strconv.ParseFloat(match[1], 64)
			if err == nil && positiveFinite(value) && value < info.DurationS {
				cuts = append(cuts, value)
				// Excessively long media should fail within the bounded task,
				// rather than silently dropping transitions or growing forever.
				if len(cuts) > 100000 {
					cancelScene()
				}
			}
		}
	}
	if scanner.Err() != nil {
		cancelScene()
	}
	if err = cmd.Wait(); err != nil || scanner.Err() != nil {
		return info, nil, errors.New("视频切镜识别失败或超时")
	}
	return info, cuts, nil
}

var ptsTimePattern = regexp.MustCompile(`pts_time:\s*([+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?)`)

func parseProbe(raw []byte) (refanalysis.VideoInfo, error) {
	var probe struct {
		Streams []struct {
			Width    int    `json:"width"`
			Height   int    `json:"height"`
			Duration string `json:"duration"`
			Average  string `json:"avg_frame_rate"`
			Rate     string `json:"r_frame_rate"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if json.Unmarshal(raw, &probe) != nil || len(probe.Streams) == 0 {
		return refanalysis.VideoInfo{}, errors.New("视频信息无效")
	}
	s := probe.Streams[0]
	duration, _ := strconv.ParseFloat(s.Duration, 64)
	if !positiveFinite(duration) {
		duration, _ = strconv.ParseFloat(probe.Format.Duration, 64)
	}
	if !positiveFinite(duration) || s.Width <= 0 || s.Height <= 0 {
		return refanalysis.VideoInfo{}, errors.New("视频时长或分辨率无效")
	}
	rate := func(value string) float64 {
		num, den, fraction := strings.Cut(value, "/")
		n, _ := strconv.ParseFloat(num, 64)
		if fraction {
			d, _ := strconv.ParseFloat(den, 64)
			if !positiveFinite(d) {
				return 0
			}
			n /= d
		}
		if !positiveFinite(n) {
			return 0
		}
		return n
	}
	fps := rate(s.Average)
	if fps == 0 {
		fps = rate(s.Rate)
	}
	return refanalysis.VideoInfo{DurationS: duration, Width: s.Width, Height: s.Height, FPS: fps}, nil
}

func extractFrame(ctx context.Context, ffmpeg, video string, timestamp float64) ([]byte, error) {
	frameCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(frameCtx, ffmpeg, "-hide_banner", "-loglevel", "error", "-nostdin", "-ss", fmt.Sprintf("%.6f", timestamp), "-i", video, "-map", "0:v:0", "-frames:v", "1", "-vf", "scale='min(768,iw)':'min(768,ih)':force_original_aspect_ratio=decrease", "-q:v", "4", "-f", "image2pipe", "-vcodec", "mjpeg", "pipe:1")
	output := &boundedBuffer{limit: maxJPEGBytes}
	cmd.Stdout = output
	if err := cmd.Run(); err != nil {
		return nil, errors.New("视频抽帧失败或超时")
	}
	if !validJPEG(output.Bytes()) {
		return nil, errors.New("视频抽帧结果无效")
	}
	return output.Bytes(), nil
}
