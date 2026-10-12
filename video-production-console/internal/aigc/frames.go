package aigc

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/store"
)

// ExtractFrames synchronously samples a video and rolls back all outputs on failure.
func (s *Service) ExtractFrames(ctx context.Context, merchantID, materialID string, count int) (store.AIGCJob, []store.MerchantMaterial, error) {
	if count < 1 || count > 12 {
		return store.AIGCJob{}, nil, ErrInvalidInput
	}
	inputs, err := s.inputs(ctx, merchantID, []string{materialID}, "video", 1)
	if err != nil {
		return store.AIGCJob{}, nil, err
	}
	rt, err := s.settings(ctx)
	if err != nil {
		return store.AIGCJob{}, nil, err
	}
	if rt.FFmpegPath == "" {
		return store.AIGCJob{}, nil, errors.New("未配置 ffmpeg")
	}
	params := map[string]any{"count": count}
	job, err := s.createJob(ctx, "", merchantID, "extract_frames", "running", []string{materialID}, params, "")
	if err != nil {
		return job, nil, err
	}
	outputs := []store.MerchantMaterial{}
	paths := []string{}
	fail := func(cause error) (store.AIGCJob, []store.MerchantMaterial, error) {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, m := range outputs {
			if e := s.merchants.DeleteMaterial(cleanupCtx, m.ID); e != nil {
				cause = errors.Join(cause, e)
			}
		}
		for _, path := range paths {
			if e := os.Remove(path); e != nil && !os.IsNotExist(e) {
				cause = errors.Join(cause, e)
			}
		}
		message := redact(cause.Error(), rt.ImageAPIKey)
		if e := s.jobs.SetJobOutputs(cleanupCtx, job.ID, []string{}); e != nil {
			cause = errors.Join(cause, e)
		}
		if e := s.jobs.UpdateJobStatus(cleanupCtx, job.ID, "failed", message); e != nil {
			cause = errors.Join(cause, e)
		}
		job.Status, job.Error, job.OutputMaterialIDs = "failed", message, []string{}
		return job, nil, errors.New(redact(cause.Error(), rt.ImageAPIKey))
	}
	duration := 10.0
	if rt.FFprobePath != "" {
		probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		stdout, _, probeErr := s.Execute(probeCtx, rt.FFprobePath, "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", inputs[0].Path)
		cancel()
		if value, e := strconv.ParseFloat(strings.TrimSpace(string(stdout)), 64); probeErr == nil && e == nil && value > 0 && !math.IsInf(value, 0) && !math.IsNaN(value) {
			duration = value
		}
	}
	for i := 0; i < count; i++ {
		position := 0.5
		if count > 1 {
			position = 0.05 + 0.90*float64(i)/float64(count-1)
		}
		id := uuid.NewString()
		filename := fmt.Sprintf("%s_frame_%02d.jpg", sourceStem(inputs[0]), i+1)
		path, e := s.materialPath(merchantID, id, filename)
		if e != nil {
			return fail(e)
		}
		paths = append(paths, path)
		frameCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_, stderr, e := s.Execute(frameCtx, rt.FFmpegPath, "-y", "-ss", fmt.Sprintf("%.3f", position*duration), "-i", inputs[0].Path, "-frames:v", "1", "-vf", "scale=1280:1280:force_original_aspect_ratio=decrease", "-q:v", "2", path)
		cancel()
		if e != nil {
			return fail(fmt.Errorf("第 %d 帧提取失败：%v %s", i+1, e, stderr))
		}
		m, e := s.recordMaterial(ctx, inputs[0], id, filename, path, "image", "extract_frames", map[string]any{"count": count, "time_s": position * duration}, "ffmpeg")
		if e != nil {
			return fail(e)
		}
		outputs = append(outputs, m)
	}
	ids := make([]string, len(outputs))
	for i, m := range outputs {
		ids[i] = m.ID
	}
	if err := s.finish(ctx, job.ID, ids, nil); err != nil {
		return fail(err)
	}
	job, err = s.jobs.GetJob(ctx, job.ID)
	return job, outputs, err
}
