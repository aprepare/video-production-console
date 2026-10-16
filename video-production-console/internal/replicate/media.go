package replicate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/refanalysis"
	"video-production-console/internal/store"
)

type boundedOutput struct {
	b     bytes.Buffer
	limit int
}

func (w *boundedOutput) Write(p []byte) (int, error) {
	if w.b.Len()+len(p) > w.limit {
		return 0, errors.New("command output exceeds limit")
	}
	return w.b.Write(p)
}
func command(ctx context.Context, exe string, args ...string) ([]byte, error) {
	out := &boundedOutput{limit: 128 << 10}
	stderr := &boundedOutput{limit: 128 << 10}
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Stdout = out
	cmd.Stderr = stderr
	if e := cmd.Run(); e != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, invalid("媒体处理失败，请检查FFmpeg配置和视频文件")
	}
	return out.b.Bytes(), nil
}
func probeVideo(ctx context.Context, exe, path string) (refanalysis.VideoInfo, error) {
	if exe == "" {
		exe = "ffprobe"
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	b, e := command(ctx, exe, "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=width,height,avg_frame_rate:format=duration", "-of", "json", path)
	if e != nil {
		return refanalysis.VideoInfo{}, e
	}
	var v struct {
		Streams []struct {
			Width  int    `json:"width"`
			Height int    `json:"height"`
			Rate   string `json:"avg_frame_rate"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if json.Unmarshal(b, &v) != nil || len(v.Streams) != 1 {
		return refanalysis.VideoInfo{}, invalid("视频信息无效")
	}
	d, e := strconv.ParseFloat(v.Format.Duration, 64)
	if e != nil || d <= 0 || math.IsNaN(d) || math.IsInf(d, 0) || v.Streams[0].Width <= 0 || v.Streams[0].Height <= 0 {
		return refanalysis.VideoInfo{}, invalid("视频时长或分辨率无效")
	}
	rate := strings.Split(v.Streams[0].Rate, "/")
	fps := 0.0
	if len(rate) == 2 {
		n, _ := strconv.ParseFloat(rate[0], 64)
		den, _ := strconv.ParseFloat(rate[1], 64)
		if den > 0 {
			fps = n / den
		}
	}
	return refanalysis.VideoInfo{DurationS: d, Width: v.Streams[0].Width, Height: v.Streams[0].Height, FPS: fps}, nil
}

type ShotSpan struct {
	Position int     `json:"position"`
	StartS   float64 `json:"start_s"`
	EndS     float64 `json:"end_s"`
	TakeID   string  `json:"take_id"`
}
type stitchPart struct {
	shot store.ReplicateShot
	take store.ReplicateTake
}

func (s *Service) Stitch(ctx context.Context, id, audio string) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, e := s.project(ctx, id)
	if e != nil {
		return Project{}, e
	}
	if audio != "" && audio != "none" {
		return Project{}, invalid("本期仅支持无声成片 audio=none")
	}
	parts := []stitchPart{}
	missing := []int{}
	for _, sh := range p.Shots {
		found := false
		for _, t := range sh.Takes {
			if t.ID == sh.SelectedTakeID && t.Status == "succeeded" {
				f, e := s.open(t.Path)
				if e != nil {
					continue
				}
				f.Close()
				parts = append(parts, stitchPart{sh, t})
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, sh.Position)
		}
	}
	if len(missing) > 0 {
		return Project{}, missingTakes(missing)
	}
	if len(parts) == 0 {
		return Project{}, missingTakes([]int{})
	}
	p.Status = "stitching"
	p.Error = ""
	p.QCID = ""
	p.FinalPath = ""
	p.FinalDurationS = 0
	if e = s.repo.SaveProject(ctx, p); e != nil {
		return Project{}, e
	}
	s.launch("stitch:"+id, id, "", "stitch", func(ctx context.Context) error { return s.runStitch(ctx, p, parts) })
	return s.get(ctx, id)
}
func (s *Service) runStitch(ctx context.Context, p store.ReplicateProject, parts []stitchPart) error {
	rt, e := s.runtime.Runtime(ctx)
	if e != nil {
		return e
	}
	exe := rt.FFmpegPath
	if exe == "" {
		exe = "ffmpeg"
	}
	sizes := map[string][2]int{"9:16": {720, 1280}, "16:9": {1280, 720}, "1:1": {960, 960}, "3:4": {720, 960}, "4:3": {960, 720}}
	size := sizes[p.Ratio]
	dir := filepath.Join(p.ID, "stitch-"+uuid.NewString())
	s.mu.Lock()
	e = s.root.MkdirAll(dir, 0700)
	s.mu.Unlock()
	if e != nil {
		return e
	}
	defer func() { s.mu.Lock(); _ = s.root.RemoveAll(dir); s.mu.Unlock() }()
	spans := []ShotSpan{}
	var listing strings.Builder
	at := 0.0
	for i, part := range parts {
		source := filepath.Join(dir, fmt.Sprintf("source-%d.mp4", i))
		target := filepath.Join(dir, fmt.Sprintf("part-%d.mp4", i))
		s.mu.Lock()
		src, e := s.open(part.take.Path)
		var dst *os.File
		if e == nil {
			dst, e = s.root.OpenFile(source, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		}
		s.mu.Unlock()
		if e != nil {
			if src != nil {
				src.Close()
			}
			return e
		}
		_, e = io.Copy(dst, io.LimitReader(src, maxVideoBytes+1))
		src.Close()
		ce := dst.Close()
		if e != nil {
			return e
		}
		if ce != nil {
			return ce
		}
		filter := fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setsar=1,fps=30", size[0], size[1], size[0], size[1])
		_, e = command(ctx, exe, "-v", "error", "-nostdin", "-y", "-i", filepath.Join(s.root.Name(), source), "-map", "0:v:0", "-an", "-vf", filter, "-c:v", "libx264", "-preset", "fast", "-pix_fmt", "yuv420p", "-movflags", "+faststart", filepath.Join(s.root.Name(), target))
		if e != nil {
			return e
		}
		info, e := s.opt.Probe(ctx, rt.FFprobePath, filepath.Join(s.root.Name(), target))
		if e != nil {
			return e
		}
		spans = append(spans, ShotSpan{Position: part.shot.Position, StartS: at, EndS: at + info.DurationS, TakeID: part.take.ID})
		at += info.DurationS
		fmt.Fprintf(&listing, "file 'part-%d.mp4'\n", i)
	}
	s.mu.Lock()
	e = s.write(filepath.Join(dir, "concat.txt"), []byte(listing.String()))
	s.mu.Unlock()
	if e != nil {
		return e
	}
	finalTmp := filepath.Join(dir, "final.mp4")
	_, e = command(ctx, exe, "-v", "error", "-nostdin", "-y", "-f", "concat", "-safe", "1", "-i", filepath.Join(s.root.Name(), dir, "concat.txt"), "-c", "copy", "-an", "-movflags", "+faststart", filepath.Join(s.root.Name(), finalTmp))
	if e != nil {
		return e
	}
	info, e := s.opt.Probe(ctx, rt.FFprobePath, filepath.Join(s.root.Name(), finalTmp))
	if e != nil {
		return e
	}
	manifest, e := json.Marshal(spans)
	if e != nil {
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e = ctx.Err(); e != nil {
		return e
	}
	p, e = s.repo.Get(ctx, p.ID)
	if e != nil {
		return e
	}
	if p.Status != "stitching" {
		return context.Canceled
	}
	if e = s.write(filepath.Join(p.ID, "timeline.json"), manifest); e != nil {
		return e
	}
	path := filepath.Join(p.ID, "final.mp4")
	if e = s.root.Rename(finalTmp, path); e != nil {
		return e
	}
	p.Status = "stitched"
	p.FinalPath = path
	p.FinalDurationS = info.DurationS
	p.Error = ""
	return s.repo.SaveProject(ctx, p)
}
