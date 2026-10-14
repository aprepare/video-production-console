package remixlab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	_ "golang.org/x/image/webp"
)

// AI 出图走 RunningHub（rh-studio 的 rh.py）。平台排队可能长达半小时，所以在后台跑，状态落在 run 目录。
const (
	aiCoverModel   = "qwen_unlimited"
	aiCoverJobName = "cover"
	aiCoverTimeout = 90 * time.Minute
)

var ErrAICoverUnavailable = errors.New("ai cover generator is not configured")

type AICoverJob struct {
	Status    string    `json:"status"` // running / done / failed
	Titles    [2]string `json:"titles"`
	Model     string    `json:"model"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at,omitzero"`
	Error     string    `json:"error,omitempty"`
	Cover     CoverView `json:"cover,omitzero"`
}

// AICoverRunner 运行 rh.py，阻塞到它退出；测试替换成假实现。
type AICoverRunner func(ctx context.Context, jobsPath, outDir string) error

func (s *Service) SetAICoverRunner(runner AICoverRunner) { s.aiCover = runner }

// RHScriptRunner 用本机 Python 调 rh-studio 的 rh.py。
func RHScriptRunner(python, script string) AICoverRunner {
	return func(ctx context.Context, jobsPath, outDir string) error {
		cmd := exec.CommandContext(ctx, python, script, "run", "--jobs", jobsPath, "--out", outDir, "--concurrency", "1")
		cmd.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8")
		output, err := cmd.CombinedOutput()
		if err != nil {
			text := strings.TrimSpace(string(output))
			if runes := []rune(text); len(runes) > 300 {
				text = string(runes[len(runes)-300:])
			}
			return fmt.Errorf("rh.py: %w: %s", err, text)
		}
		return nil
	}
}

func aiCoverDir(outputDir string) string { return filepath.Join(outputDir, "ai-cover") }

func readAICoverJob(dir string) (AICoverJob, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "job.json"))
	if err != nil {
		return AICoverJob{}, err
	}
	var job AICoverJob
	return job, json.Unmarshal(raw, &job)
}

func writeAICoverJob(dir string, job AICoverJob) error {
	raw, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		return err
	}
	return writeCoverPackage(filepath.Join(dir, "job.json"), raw)
}

// StartAICover 提交一张 AI 封面；同一 run 已在出图时直接返回那一单，避免重复扣费。
func (s *Service) StartAICover(ctx context.Context, runID, line1, line2 string) (AICoverJob, error) {
	if s.aiCover == nil {
		return AICoverJob{}, ErrAICoverUnavailable
	}
	titles, err := normalizeCoverTitles(line1, line2)
	if err != nil {
		return AICoverJob{}, err
	}
	s.coverMu.Lock()
	defer s.coverMu.Unlock()
	target, err := s.coverTarget(ctx, runID)
	if err != nil {
		return AICoverJob{}, err
	}
	dir := aiCoverDir(target.run.OutputDir)
	if job, err := readAICoverJob(dir); err == nil && job.Status == "running" {
		s.launchAICover(runID, dir)
		return job, nil
	}
	// 上一单的输出与任务记录挪走，rh.py 才会按新标题重新提交。
	if _, err := os.Stat(dir); err == nil {
		if err := os.Rename(dir, dir+"-"+s.now().Format("20060102-150405")); err != nil {
			return AICoverJob{}, err
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return AICoverJob{}, err
	}
	jobs := []map[string]any{{
		"name":   aiCoverJobName,
		"model":  aiCoverModel,
		"image":  target.style.ReferencePath,
		"prompt": target.style.FillPrompt(titles),
		"params": map[string]any{"original_ratio": true, "prompt_enhance": false},
	}}
	raw, err := json.MarshalIndent(jobs, "", "  ")
	if err != nil {
		return AICoverJob{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, "jobs.json"), raw, 0o644); err != nil {
		return AICoverJob{}, err
	}
	job := AICoverJob{Status: "running", Titles: titles, Model: aiCoverModel, StartedAt: s.now()}
	if err := writeAICoverJob(dir, job); err != nil {
		return AICoverJob{}, err
	}
	s.launchAICover(runID, dir)
	return job, nil
}

// AICoverStatus 读出图进度；控制台重启后对仍在出图的单子重新接上（rh.py 只查询已提交的任务，不会重提）。
func (s *Service) AICoverStatus(ctx context.Context, runID string) (AICoverJob, error) {
	run, err := s.repo.GetRun(ctx, runID)
	if err != nil {
		return AICoverJob{}, err
	}
	if strings.TrimSpace(run.OutputDir) == "" {
		return AICoverJob{}, os.ErrNotExist
	}
	dir := aiCoverDir(run.OutputDir)
	job, err := readAICoverJob(dir)
	if err != nil {
		return AICoverJob{}, err
	}
	if job.Status == "running" && s.aiCover != nil {
		s.launchAICover(runID, dir)
	}
	return job, nil
}

func (s *Service) launchAICover(runID, dir string) {
	s.aiCoverMu.Lock()
	if s.aiCoverActive == nil {
		s.aiCoverActive = map[string]bool{}
	}
	if s.aiCoverActive[runID] {
		s.aiCoverMu.Unlock()
		return
	}
	s.aiCoverActive[runID] = true
	s.aiCoverMu.Unlock()
	go func() {
		defer func() {
			s.aiCoverMu.Lock()
			delete(s.aiCoverActive, runID)
			s.aiCoverMu.Unlock()
		}()
		s.finishAICover(runID, dir)
	}()
}

func (s *Service) finishAICover(runID, dir string) {
	ctx, cancel := context.WithTimeout(context.Background(), aiCoverTimeout)
	defer cancel()
	runErr := s.aiCover(ctx, filepath.Join(dir, "jobs.json"), filepath.Join(dir, "out"))
	s.coverMu.Lock()
	defer s.coverMu.Unlock()
	job, err := readAICoverJob(dir)
	if err != nil || job.Status != "running" {
		return
	}
	view, err := s.adoptAICover(ctx, runID, dir, job.Titles)
	if err != nil {
		job.Status, job.Error = "failed", aiCoverFailure(dir, runErr, err)
	} else {
		job.Status, job.Cover = "done", view
	}
	job.EndedAt = s.now()
	_ = writeAICoverJob(dir, job)
}

// adoptAICover 把平台出的图转成 run 目录的 cover.png，再走与本地出图相同的存库和发布包回写。
func (s *Service) adoptAICover(ctx context.Context, runID, dir string, titles [2]string) (CoverView, error) {
	matches, _ := filepath.Glob(filepath.Join(dir, "out", aiCoverJobName+".*"))
	source := ""
	for _, match := range matches {
		switch strings.ToLower(filepath.Ext(match)) {
		case ".png", ".jpg", ".jpeg", ".webp":
			source = match
		}
		if source != "" {
			break
		}
	}
	if source == "" {
		return CoverView{}, errors.New("平台没有返回图片")
	}
	target, err := s.coverTarget(ctx, runID)
	if err != nil {
		return CoverView{}, err
	}
	file, err := os.Open(source)
	if err != nil {
		return CoverView{}, err
	}
	img, _, err := image.Decode(file)
	file.Close()
	if err != nil {
		return CoverView{}, fmt.Errorf("读取平台图片: %w", err)
	}
	output := filepath.Join(target.run.OutputDir, "cover.png")
	tmp, err := os.CreateTemp(target.run.OutputDir, ".cover-*.png")
	if err != nil {
		return CoverView{}, err
	}
	defer os.Remove(tmp.Name())
	if err := png.Encode(tmp, img); err != nil {
		tmp.Close()
		return CoverView{}, err
	}
	if err := tmp.Close(); err != nil {
		return CoverView{}, err
	}
	if err := os.Rename(tmp.Name(), output); err != nil {
		return CoverView{}, err
	}
	bounds := img.Bounds()
	return s.publishCover(ctx, target, output, titles, bounds.Dx(), bounds.Dy())
}

// aiCoverFailure 优先用 rh.py 记在任务文件里的平台原因，读不到才用进程错误。
func aiCoverFailure(dir string, runErr, adoptErr error) string {
	raw, err := os.ReadFile(filepath.Join(dir, "out", aiCoverJobName+".task.json"))
	if err == nil {
		var task struct {
			Failed     string  `json:"failed"`
			Rejected   string  `json:"rejected"`
			TaskID     string  `json:"taskId"`
			Submitting float64 `json:"submitting"`
		}
		if json.Unmarshal(raw, &task) == nil {
			if task.TaskID == "" && task.Submitting > 0 {
				return "提交回执不明，平台可能已经扣费；请先到 RunningHub 确认，别急着重新生成"
			}
			if reason := strings.TrimSpace(task.Failed + " " + task.Rejected); reason != "" {
				return "RunningHub 出图失败：" + reason
			}
		}
	}
	if runErr != nil {
		return "出图进程失败：" + runErr.Error()
	}
	return adoptErr.Error()
}
