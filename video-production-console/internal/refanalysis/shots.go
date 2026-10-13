package refanalysis

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"math"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type VideoInfo struct {
	DurationS float64 `json:"duration_s"`
	Width     int     `json:"width"`
	Height    int     `json:"height"`
	FPS       float64 `json:"fps"`
}

type Shot struct {
	Index  int     `json:"index"`
	StartS float64 `json:"start_s"`
	EndS   float64 `json:"end_s"`
}

// Shots 先读取实际视频参数，再检测切镜；输出镜头始终覆盖完整时长。
func Shots(ctx context.Context, ffmpeg, ffprobe, video string) (VideoInfo, []Shot, error) {
	if ffmpeg == "" || ffprobe == "" {
		return VideoInfo{}, nil, errors.New("请先配置 FFmpeg 和 FFprobe")
	}
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	raw, err := exec.CommandContext(probeCtx, ffprobe, "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=width,height,avg_frame_rate,r_frame_rate,duration:format=duration", "-of", "json", video).Output()
	cancel()
	if err != nil {
		return VideoInfo{}, nil, errors.New("无法读取原视频信息")
	}
	info, err := parseProbe(raw)
	if err != nil {
		return VideoInfo{}, nil, err
	}
	sceneCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(sceneCtx, ffmpeg, "-hide_banner", "-nostdin", "-i", video, "-map", "0:v:0", "-vf", "select='gt(scene,0.32)',showinfo", "-an", "-fps_mode", "vfr", "-f", "null", "-")
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return info, nil, errors.New("无法启动切镜识别")
	}
	if err := cmd.Start(); err != nil {
		return info, nil, errors.New("无法启动切镜识别")
	}
	// 逐行解析，不累积 ffmpeg 的完整日志，也不把其中的路径返回给调用方。
	var cuts []float64
	scanner := bufio.NewScanner(stderr)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		cuts = append(cuts, parseSceneTimes(scanner.Text())...)
	}
	if scanner.Err() != nil {
		cancel()
	}
	if err := cmd.Wait(); err != nil || scanner.Err() != nil {
		return info, nil, errors.New("视频切镜识别失败或超时")
	}
	return info, shotsFromCuts(info.DurationS, cuts), nil
}

func parseProbe(raw []byte) (VideoInfo, error) {
	var probe struct {
		Streams []struct {
			Width       int    `json:"width"`
			Height      int    `json:"height"`
			Duration    string `json:"duration"`
			AverageRate string `json:"avg_frame_rate"`
			Rate        string `json:"r_frame_rate"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil || len(probe.Streams) == 0 {
		return VideoInfo{}, errors.New("原视频信息无效")
	}
	stream := probe.Streams[0]
	duration, _ := strconv.ParseFloat(probe.Format.Duration, 64)
	if !positiveFinite(duration) {
		duration, _ = strconv.ParseFloat(stream.Duration, 64)
	}
	if !positiveFinite(duration) || stream.Width <= 0 || stream.Height <= 0 {
		return VideoInfo{}, errors.New("原视频时长或分辨率无效")
	}
	fps := frameRate(stream.AverageRate)
	if fps == 0 {
		fps = frameRate(stream.Rate)
	}
	return VideoInfo{DurationS: duration, Width: stream.Width, Height: stream.Height, FPS: fps}, nil
}

func frameRate(value string) float64 {
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

func positiveFinite(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

var ptsTimePattern = regexp.MustCompile(`pts_time:\s*([+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?)`)

func parseSceneTimes(stderr string) []float64 {
	var times []float64
	for _, match := range ptsTimePattern.FindAllStringSubmatch(stderr, -1) {
		value, err := strconv.ParseFloat(match[1], 64)
		if err == nil && positiveFinite(value) {
			times = append(times, value)
		}
	}
	return times
}

func shotsFromCuts(duration float64, cuts []float64) []Shot {
	if !positiveFinite(duration) {
		return nil
	}
	ordered := append([]float64(nil), cuts...)
	sort.Float64s(ordered)
	bounds := []float64{0}
	previous := math.Inf(-1)
	for _, cut := range ordered {
		if !positiveFinite(cut) || cut >= duration {
			continue
		}
		// 同一组连续近邻只保留首次切点，避免闪切把一个转场拆成多段。
		if cut-previous > 0.4+1e-9 {
			bounds = append(bounds, cut)
		}
		previous = cut
	}
	if len(bounds) == 1 {
		count := math.Min(60, math.Ceil(duration/3))
		for i := 1; i < int(count); i++ {
			bounds = append(bounds, duration*float64(i)/count)
		}
	}
	bounds = append(bounds, duration)
	for len(bounds)-1 > 60 {
		remove := 1
		for i := 2; i < len(bounds)-1; i++ {
			if bounds[i+1]-bounds[i-1] < bounds[remove+1]-bounds[remove-1] {
				remove = i
			}
		}
		bounds = append(bounds[:remove], bounds[remove+1:]...)
	}
	shots := make([]Shot, len(bounds)-1)
	for i := range shots {
		shots[i] = Shot{Index: i + 1, StartS: bounds[i], EndS: bounds[i+1]}
	}
	return shots
}
