package aigc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/runninghub"
	consoleSettings "video-production-console/internal/settings"
	"video-production-console/internal/store"
)

type shot struct {
	Name     string  `json:"name"`
	Image    string  `json:"image"`
	Audio    *string `json:"audio"`
	Duration int     `json:"duration"`
	Prompt   string  `json:"prompt"`
}
type videoParams struct {
	JobOptions
	Prompt         string            `json:"prompt"`
	DurationS      int               `json:"duration_s"`
	Aspect         string            `json:"aspect"`
	ShotsPath      string            `json:"shots_path"`
	OutputDir      string            `json:"output_dir"`
	EstimatedShots int               `json:"estimated_shots"`
	ShotMaterials  map[string]string `json:"shot_materials"`
}

// PrepareImageToVideo copies images and writes a manifest without submitting a paid task.
func (s *Service) PrepareImageToVideo(ctx context.Context, merchantID string, materialIDs []string, prompt string, durationS int, aspect string, options ...JobOptions) (store.AIGCJob, error) {
	if durationS < 3 || durationS > 10 || (aspect != "9:16" && aspect != "16:9" && aspect != "1:1") || strings.TrimSpace(prompt) == "" {
		return store.AIGCJob{}, ErrInvalidInput
	}
	inputs, err := s.inputs(ctx, merchantID, materialIDs, "image", 0)
	if err != nil {
		return store.AIGCJob{}, err
	}
	opt, err := s.options(ctx, merchantID, options)
	if err != nil {
		return store.AIGCJob{}, err
	}
	jobID := uuid.NewString()
	root := filepath.Join(s.dataRoot, "aigc")
	dir, err := safePath(root, filepath.Join(root, jobID))
	if err != nil {
		return store.AIGCJob{}, err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return store.AIGCJob{}, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(dir)
		}
	}()
	p := videoParams{JobOptions: opt, Prompt: prompt, DurationS: durationS, Aspect: aspect, ShotsPath: filepath.Join(dir, "shots.json"), OutputDir: filepath.Join(dir, "generated"), EstimatedShots: len(inputs), ShotMaterials: map[string]string{}}
	shots := make([]shot, 0, len(inputs))
	for i, m := range inputs {
		name := fmt.Sprintf("shot-%02d", i+1)
		ext := strings.ToLower(filepath.Ext(m.Path))
		switch ext {
		case ".png", ".jpg", ".jpeg", ".webp":
		default:
			ext = ".png"
		}
		imageName := name + ext
		if err = copyFile(m.Path, filepath.Join(dir, imageName)); err != nil {
			return store.AIGCJob{}, err
		}
		shots = append(shots, shot{Name: name, Image: imageName, Duration: durationS, Prompt: prompt})
		p.ShotMaterials[name] = m.ID
	}
	raw, err := json.MarshalIndent(shots, "", "  ")
	if err != nil {
		return store.AIGCJob{}, err
	}
	if err = os.WriteFile(p.ShotsPath, raw, 0600); err != nil {
		return store.AIGCJob{}, err
	}
	if err = os.MkdirAll(p.OutputDir, 0700); err != nil {
		return store.AIGCJob{}, err
	}
	job, err := s.createJob(ctx, jobID, merchantID, "image_to_video", "awaiting_confirm", materialIDs, p, opt.OrderID)
	if err != nil {
		return job, err
	}
	keep = true
	return job, nil
}

func copyFile(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return ErrUnsafePath
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		_ = os.Remove(target)
		return err
	}
	return nil
}

// ConfirmImageToVideo atomically claims an awaiting job before launching its command.
func (s *Service) ConfirmImageToVideo(ctx context.Context, jobID string) (store.AIGCJob, error) {
	if !validID(jobID) {
		return store.AIGCJob{}, ErrInvalidInput
	}
	job, err := s.jobs.GetJob(ctx, jobID)
	if err != nil {
		return job, err
	}
	if job.Status != "awaiting_confirm" || job.Kind != "image_to_video" {
		return job, store.ErrAIGCJobConflict
	}
	var p videoParams
	if err = json.Unmarshal([]byte(job.ParamsJSON), &p); err != nil {
		return job, ErrInvalidInput
	}
	root := filepath.Join(s.dataRoot, "aigc")
	dir, err := safePath(root, filepath.Join(root, job.ID))
	if err != nil {
		return job, err
	}
	for _, path := range []string{p.ShotsPath, p.OutputDir} {
		if _, err = safePath(dir, path); err != nil {
			return job, err
		}
	}
	if p.ShotsPath != filepath.Join(dir, "shots.json") || p.OutputDir != filepath.Join(dir, "generated") {
		return job, ErrUnsafePath
	}
	// Validate the persisted manifest too; never allow a changed manifest to upload arbitrary files.
	raw, err := os.ReadFile(p.ShotsPath)
	if err != nil {
		return job, err
	}
	var shots []shot
	if err = json.Unmarshal(raw, &shots); err != nil || len(shots) == 0 || len(shots) != p.EstimatedShots || len(shots) != len(job.InputMaterialIDs) {
		return job, ErrInvalidInput
	}
	allowed := map[string]bool{}
	for _, id := range job.InputMaterialIDs {
		allowed[id] = true
	}
	parents := map[string]store.MerchantMaterial{}
	for i, item := range shots {
		name := fmt.Sprintf("shot-%02d", i+1)
		materialID := p.ShotMaterials[name]
		if item.Name != name || !allowed[materialID] || item.Audio != nil || item.Duration != p.DurationS || item.Prompt != p.Prompt || filepath.IsAbs(item.Image) {
			return job, ErrInvalidInput
		}
		path, err := safePath(dir, filepath.Join(dir, item.Image))
		if err != nil {
			return job, err
		}
		info, err := os.Stat(path)
		if err != nil {
			return job, err
		}
		if !info.Mode().IsRegular() {
			return job, ErrUnsafePath
		}
		parent, err := s.merchants.GetMaterial(ctx, materialID)
		if err != nil {
			return job, err
		}
		if parent.MerchantID != job.MerchantID || parent.Kind != "image" {
			return job, ErrInvalidInput
		}
		parents[name] = parent
	}
	rt, err := s.settings(ctx)
	if err != nil {
		return job, err
	}
	keys := consoleSettings.ParseRunningHubAPIKeys(rt.RunningHubAPIKeys)
	if len(keys) == 0 {
		return job, errors.New("还没配置 RunningHub Key，请到设置填写")
	}
	if err = s.jobs.ClaimJob(ctx, job.ID); err != nil {
		return job, err
	}
	job.Status = "running"
	go s.runVideo(job, p, shots, parents, keys)
	return job, nil
}

func (s *Service) runVideo(job store.AIGCJob, p videoParams, shots []shot, parents map[string]store.MerchantMaterial, keys []string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	failures := []string{}
	httpClient, ok := s.HTTPClient.(*http.Client)
	if !ok {
		resultCtx, resultCancel := context.WithTimeout(context.Background(), time.Minute)
		defer resultCancel()
		_ = s.finish(resultCtx, job.ID, nil, []string{"RunningHub HTTP 客户端不可用"})
		return
	}
	client := runninghub.Client{HTTP: httpClient}
	for _, item := range shots {
		imagePath := filepath.Join(filepath.Dir(p.ShotsPath), item.Image)
		image, err := os.ReadFile(imagePath)
		if err != nil {
			failures = append(failures, item.Name+"：读取首帧失败")
			continue
		}
		if err := s.generateRunningHubVideo(ctx, client, keys, item, p.Aspect, image, filepath.Join(p.OutputDir, item.Name+".mp4")); err != nil {
			failures = append(failures, item.Name+"："+redact(err.Error(), keys...))
		}
	}
	resultCtx, resultCancel := context.WithTimeout(context.Background(), time.Minute)
	defer resultCancel()
	ids := []string{}
	files, err := filepath.Glob(filepath.Join(p.OutputDir, "*.mp4"))
	if err != nil {
		failures = append(failures, "扫描生成视频失败")
	}
	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
		parent, ok := parents[name]
		// Temporary *.download.mp4 and unrelated files are not completed shots.
		if !ok {
			continue
		}
		path, err := safePath(p.OutputDir, file)
		if err != nil {
			failures = append(failures, name+"："+err.Error())
			continue
		}
		id := uuid.NewString()
		filename := sourceStem(parent) + "_video_01.mp4"
		target, err := s.materialPath(job.MerchantID, id, filename)
		if err == nil {
			err = copyFile(path, target)
		}
		if err != nil {
			failures = append(failures, name+"：导入视频失败："+err.Error())
			continue
		}
		m, err := s.recordMaterial(resultCtx, parent, id, filename, target, "video", "image_to_video", p, "RunningHub H3")
		if err != nil {
			_ = os.Remove(target)
			failures = append(failures, name+"：记录视频失败："+err.Error())
			continue
		}
		ids = append(ids, m.ID)
	}
	if len(ids) < p.EstimatedShots {
		failures = append(failures, fmt.Sprintf("已生成 %d/%d 个视频", len(ids), p.EstimatedShots))
	}
	for i := range failures {
		failures[i] = redact(failures[i], keys...)
	}
	if err := s.finish(resultCtx, job.ID, ids, failures); err != nil {
		_ = s.jobs.UpdateJobStatus(resultCtx, job.ID, "failed", "保存视频作业结果失败")
	}
}

func (s *Service) generateRunningHubVideo(ctx context.Context, client runninghub.Client, keys []string, item shot, aspect string, image []byte, output string) error {
	attempts := len(keys)
	if attempts > 2 {
		attempts = 2
	}
	var lastErr error
	for index := 0; index < attempts; index++ {
		key := keys[index]
		uploaded, err := client.Upload(ctx, key, filepath.Base(item.Image), image)
		if err != nil {
			lastErr = err
			if runninghub.ShouldDisable(err) {
				continue
			}
			break
		}
		nodes, err := runninghub.BuildH3Nodes(runninghub.H3Request{Prompt: item.Prompt, DurationS: item.Duration, Ratio: aspect, PromptOptimize: true}, uploaded)
		if err != nil {
			return err
		}
		taskID, err := client.RunApp(ctx, key, runninghub.H3WebappID, nodes, "default")
		if err != nil {
			lastErr = err
			if runninghub.ShouldDisable(err) {
				continue
			}
			break
		}
		// Once a task ID exists, never submit another paid task. Query and
		// download must continue with the same key that created the task.
		return pollAndDownloadRunningHub(ctx, client, key, taskID, output)
	}
	if lastErr == nil {
		lastErr = errors.New("RunningHub generation failed")
	}
	return lastErr
}

func pollAndDownloadRunningHub(ctx context.Context, client runninghub.Client, key, taskID, output string) error {
	for {
		status, err := client.Query(ctx, key, taskID)
		if err != nil {
			return err
		}
		switch strings.ToUpper(status.State) {
		case "SUCCESS":
			videoURL := status.VideoURL()
			if videoURL == "" {
				return errors.New("RunningHub 成功响应没有 MP4")
			}
			temporary := output + ".download"
			file, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
			if err != nil {
				return err
			}
			err = client.Download(ctx, videoURL, file, 2<<30)
			closeErr := file.Close()
			if err = errors.Join(err, closeErr); err != nil {
				_ = os.Remove(temporary)
				return err
			}
			if err := os.Rename(temporary, output); err != nil {
				_ = os.Remove(temporary)
				return err
			}
			return nil
		case "FAILED", "FAILURE", "CANCELED":
			return fmt.Errorf("RunningHub 任务%s", strings.ToLower(status.State))
		}
		timer := time.NewTimer(20 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
