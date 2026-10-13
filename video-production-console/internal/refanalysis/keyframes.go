package refanalysis

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"
)

const maxFrames = 24

type Frame struct {
	Index int     `json:"index"`
	TimeS float64 `json:"time_s"`
	Shot  int     `json:"shot"`
	URL   string  `json:"url,omitempty"`
	JPEG  []byte  `json:"-"`
}

func frameFilename(index int) string { return fmt.Sprintf("frame_%02d.jpg", index) }

// Keyframes 的输出目录由服务按 reference/analysis ID 生成并校验。
func Keyframes(ctx context.Context, ffmpeg, video string, shots []Shot, directory string) ([]Frame, error) {
	frames := sampleFrames(shots)
	if ffmpeg == "" || len(frames) == 0 {
		return nil, errors.New("没有可提取的关键帧或未配置 FFmpeg")
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return nil, errors.New("关键帧目录创建失败")
	}
	for i := range frames {
		path := filepath.Join(directory, frameFilename(frames[i].Index))
		frameCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := exec.CommandContext(frameCtx, ffmpeg, "-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-ss", fmt.Sprintf("%.6f", frames[i].TimeS), "-i", video, "-map", "0:v:0", "-frames:v", "1", "-vf", "scale='min(768,iw)':'min(768,ih)':force_original_aspect_ratio=decrease", "-q:v", "4", path).Run()
		cancel()
		if err != nil {
			return nil, fmt.Errorf("第 %d 帧提取失败或超时", i+1)
		}
		frames[i].JPEG, err = os.ReadFile(path)
		if err != nil || len(frames[i].JPEG) == 0 {
			return nil, fmt.Errorf("第 %d 帧读取失败", i+1)
		}
	}
	return frames, nil
}

func sampleFrames(shots []Shot) []Frame {
	if len(shots) == 0 {
		return nil
	}
	duration := shots[len(shots)-1].EndS
	if !positiveFinite(duration) {
		return nil
	}
	hookTime := 0.3
	if duration <= hookTime {
		hookTime = duration / 2
	}
	hook := Frame{TimeS: hookTime, Shot: shots[0].Index}
	var candidates []Frame
	for _, shot := range shots {
		if shot.StartS <= hookTime && hookTime < shot.EndS {
			hook.Shot = shot.Index
		}
		mid := (shot.StartS + shot.EndS) / 2
		if shot.EndS > shot.StartS && math.Abs(mid-hookTime) > 1e-9 {
			candidates = append(candidates, Frame{TimeS: mid, Shot: shot.Index})
		}
	}
	candidates = append(candidates, hook)
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].TimeS < candidates[j].TimeS })
	if len(candidates) > maxFrames {
		var early, later []Frame
		for _, frame := range candidates {
			if frame.TimeS <= 6 {
				early = append(early, frame)
			} else {
				later = append(later, frame)
			}
		}
		if len(early) >= maxFrames {
			withoutHook := make([]Frame, 0, len(early)-1)
			for _, frame := range early {
				if frame.TimeS != hookTime {
					withoutHook = append(withoutHook, frame)
				}
			}
			candidates = append(evenFrames(withoutHook, maxFrames-1), hook)
		} else {
			candidates = append(early, evenFrames(later, maxFrames-len(early))...)
		}
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].TimeS < candidates[j].TimeS })
	}
	for i := range candidates {
		candidates[i].Index = i + 1
	}
	return candidates
}

func evenFrames(frames []Frame, count int) []Frame {
	if count <= 0 {
		return nil
	}
	if len(frames) <= count {
		return frames
	}
	if count == 1 {
		return []Frame{frames[len(frames)/2]}
	}
	out := make([]Frame, count)
	for i := range out {
		index := int(math.Round(float64(i) * float64(len(frames)-1) / float64(count-1)))
		out[i] = frames[index]
	}
	return out
}
