package imagevideo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type LifecycleRepository interface {
	ImageVideoJobRepository
	GetDetail(context.Context, string) (JobDetail, error)
	BeginDraft(context.Context, string, time.Time) (JobDetail, error)
	SaveDraft(context.Context, string, string, string, string, string, time.Time) error
	FailDraftOrRegistration(context.Context, string, string, string, string, time.Time) error
	CompleteRegistration(context.Context, string, string, string, time.Time) error
}

type LifecycleConfig struct {
	Repository                                  LifecycleRepository
	Worker                                      *Worker
	DataRoot                                    string
	PythonBinary                                string
	DraftScript                                 string
	MachineProfilePath                          string
	SkillRoot, RegistrationScript, JianyingRoot string
	SkillSnapshotID                             string
	Registrar                                   Registrar
}

type Lifecycle struct {
	repository LifecycleRepository
	worker     *Worker
	config     LifecycleConfig
	ctx        context.Context
	cancel     context.CancelFunc
	wake       chan struct{}
	finalize   chan string
	wg         sync.WaitGroup
}

func NewLifecycle(config LifecycleConfig) (*Lifecycle, error) {
	if config.Repository == nil || config.Worker == nil || strings.TrimSpace(config.DataRoot) == "" {
		return nil, fmt.Errorf("image video lifecycle dependencies are incomplete")
	}
	ctx, cancel := context.WithCancel(context.Background())
	lifecycle := &Lifecycle{repository: config.Repository, worker: config.Worker, config: config, ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1), finalize: make(chan string, 32)}
	lifecycle.wg.Add(1)
	go lifecycle.run()
	_ = lifecycle.Kick(ctx, "startup")
	return lifecycle, nil
}

func (l *Lifecycle) Close() {
	if l == nil {
		return
	}
	l.cancel()
	l.wg.Wait()
}

func (l *Lifecycle) Kick(_ context.Context, _ string) error {
	if l == nil || l.ctx == nil || l.wake == nil {
		return fmt.Errorf("image video lifecycle is unavailable")
	}
	select {
	case l.wake <- struct{}{}:
	default:
	}
	return nil
}

func (l *Lifecycle) DraftReady(_ context.Context, jobID string) error {
	if l == nil || l.ctx == nil || l.finalize == nil || strings.TrimSpace(jobID) == "" {
		return fmt.Errorf("image video lifecycle is unavailable")
	}
	select {
	case l.finalize <- jobID:
	default:
		go func() {
			select {
			case l.finalize <- jobID:
			case <-l.ctx.Done():
			}
		}()
	}
	return nil
}

func (l *Lifecycle) RetryRegistration(_ context.Context, jobID string) error {
	if l == nil || l.ctx == nil || l.repository == nil || strings.TrimSpace(jobID) == "" {
		return fmt.Errorf("image video lifecycle is unavailable")
	}
	detail, err := l.repository.GetDetail(l.ctx, jobID)
	if err != nil {
		return err
	}
	if detail.Job.Phase != "registration" || detail.Job.DraftStatus != "succeeded" || detail.Job.RegistrationStatus != "failed" {
		return fmt.Errorf("%w: registration is not retryable", ErrConflict)
	}
	return l.DraftReady(l.ctx, jobID)
}

func (l *Lifecycle) run() {
	defer l.wg.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-l.ctx.Done():
			return
		case jobID := <-l.finalize:
			l.finalizeJob(jobID)
		case <-l.wake:
			l.drainWorker()
		case <-ticker.C:
			l.drainWorker()
		}
	}
}

func (l *Lifecycle) drainWorker() {
	for l.ctx.Err() == nil {
		didWork, err := l.worker.RunOnce(l.ctx)
		if err != nil || !didWork {
			return
		}
	}
}

func (l *Lifecycle) finalizeJob(jobID string) {
	detail, err := l.repository.BeginDraft(l.ctx, jobID, time.Now().UTC())
	if err != nil {
		return
	}
	phase := "draft"
	if detail.Job.DraftStatus == "succeeded" && detail.Job.DraftRelativePath != "" {
		phase = "registration"
	}
	if phase == "draft" {
		if err := l.buildDraft(detail); err != nil {
			_ = l.repository.FailDraftOrRegistration(context.Background(), jobID, "draft", "image_video_draft_failed", err.Error(), time.Now().UTC())
			return
		}
		detail, err = l.repository.GetDetail(l.ctx, jobID)
		if err != nil {
			return
		}
	}
	if err := l.registerDraft(detail); err != nil {
		_ = l.repository.FailDraftOrRegistration(context.Background(), jobID, "registration", "image_video_registration_failed", err.Error(), time.Now().UTC())
	}
}

func (l *Lifecycle) buildDraft(detail JobDetail) error {
	job := detail.Job
	if job.NarrationRelativePath == "" || len(detail.Items) == 0 {
		return fmt.Errorf("narration or scenes are unavailable")
	}
	_, narrationPath, err := ResolveProjectPath(l.config.DataRoot, job.ProjectID, job.NarrationRelativePath)
	if err != nil {
		return err
	}
	_, outputRoot, err := ResolveManagedPath(l.config.DataRoot, job.ProjectID, filepath.ToSlash(filepath.Join(job.ID, "output")))
	if err != nil {
		return err
	}
	scenes := make([]DraftSceneInput, 0, len(detail.Items))
	var start, total int64
	for _, item := range detail.Items {
		if item.Status != string(JobSucceeded) {
			return fmt.Errorf("image video items are incomplete")
		}
		var mediaPath string
		if job.OutputMode == ModeSlideshow {
			_, mediaPath, err = ResolveProjectPath(l.config.DataRoot, job.ProjectID, item.InputImageRelativePath)
		} else {
			_, mediaPath, err = ResolveManagedPath(l.config.DataRoot, job.ProjectID, item.OutputVideoRelativePath)
		}
		if err != nil {
			return err
		}
		motion := "none"
		if job.OutputMode == ModeSlideshow {
			motion = sceneMotionCycle[(item.Ordinal-1)%len(sceneMotionCycle)]
		}
		scenes = append(scenes, DraftSceneInput{StartUS: start, DurationUS: item.TimelineDurationUS, MediaPath: mediaPath, Motion: motion})
		start += item.TimelineDurationUS
		total += item.TimelineDurationUS
	}
	displayName := "图文视频-" + job.ID
	artifact, err := BuildDraft(l.ctx, DraftBuildRequest{JobID: job.ID, DisplayName: displayName, OutputRoot: outputRoot, PythonBinary: l.config.PythonBinary, ScriptPath: l.config.DraftScript, NarrationPath: narrationPath, OutputMode: job.OutputMode, DurationUS: total, Scenes: scenes})
	if err != nil {
		return err
	}
	manifestPath := filepath.Join(outputRoot, "task_manifest.json")
	productionPlanPath := filepath.Join(outputRoot, "production_plan.json")
	productionPlan := map[string]any{"version": "image-video-v1", "job_id": job.ID, "output_mode": job.OutputMode, "template_version": job.TemplateVersion, "template_fingerprint": job.TemplateFingerprint, "duration_us": total, "scenes": len(scenes), "spoken_captions": false, "editable_titles": false}
	productionPlanBytes, err := json.MarshalIndent(productionPlan, "", "  ")
	if err != nil {
		return err
	}
	productionPlanBytes = append(productionPlanBytes, '\n')
	if err := os.WriteFile(productionPlanPath, productionPlanBytes, 0o600); err != nil {
		return err
	}
	manifest := map[string]any{
		"schema_version": "2.0", "task_id": job.ID, "job_id": job.ID,
		"skill": "jianying-image-video", "action": "imagevideo.register",
		"inputs": []any{}, "engineering_inputs": []any{}, "output_dir": outputRoot,
		"expected_outputs": []map[string]any{
			{"type": "production_plan", "required": true, "description": "job-bound image video production plan"},
			{"type": "plaintext_workspace", "required": true, "description": "editable image video draft"},
		},
		"approval_mode": "auto_after_valid_plan", "skill_snapshot_id": BuiltInManifestSkillID(),
		"non_secret_settings": map[string]any{"machine_profile_path": l.config.MachineProfilePath, "draft_display_name": displayName, "revision_notes": "image-video drafts do not use scenic SRT or account_background inputs"},
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(manifestPath, encoded, 0o600); err != nil {
		return err
	}
	manifestHash := sha256.Sum256(encoded)
	draftRelative := filepath.ToSlash(filepath.Join(job.ID, "output", "workspace", job.ID))
	manifestRelative := filepath.ToSlash(filepath.Join(job.ID, "output", "task_manifest.json"))
	return l.repository.SaveDraft(l.ctx, job.ID, draftRelative, artifact.Fingerprint, manifestRelative, hex.EncodeToString(manifestHash[:]), time.Now().UTC())
}

func (l *Lifecycle) registerDraft(detail JobDetail) error {
	job := detail.Job
	if l.config.Registrar == nil {
		return fmt.Errorf("trusted image video registrar is not configured")
	}
	_, workspace, err := ResolveManagedPath(l.config.DataRoot, job.ProjectID, job.DraftRelativePath)
	if err != nil {
		return err
	}
	_, manifest, err := ResolveManagedPath(l.config.DataRoot, job.ProjectID, job.ManifestRelativePath)
	if err != nil {
		return err
	}
	_, outputRoot, err := ResolveManagedPath(l.config.DataRoot, job.ProjectID, filepath.ToSlash(filepath.Join(job.ID, "output")))
	if err != nil {
		return err
	}
	displayName := "图文视频-" + job.ID
	coverPath := ""
	if len(detail.Items) > 0 {
		if job.OutputMode == ModeSlideshow {
			if _, path, coverErr := ResolveProjectPath(l.config.DataRoot, job.ProjectID, detail.Items[0].InputImageRelativePath); coverErr == nil {
				coverPath = path
			}
		} else if _, path, coverErr := ResolveManagedPath(l.config.DataRoot, job.ProjectID, detail.Items[0].OutputVideoRelativePath); coverErr == nil {
			coverPath = path
		}
	}
	request := RegistrationRequest{
		JobID: job.ID, ProjectID: job.ProjectID, DisplayName: displayName,
		ManifestPath: manifest, WorkspacePath: workspace, OutputDir: outputRoot,
		TemplateVersion: job.TemplateVersion, TemplateFingerprint: job.TemplateFingerprint,
		DraftFingerprint: job.DraftFingerprint, JianyingRoot: l.config.JianyingRoot,
		MachineProfilePath: l.config.MachineProfilePath, CoverPath: coverPath,
	}
	if _, err := ValidateRegistrationBinding(l.config.DataRoot, job, request); err != nil {
		return err
	}
	result, err := l.config.Registrar.Register(l.ctx, request)
	if err != nil {
		return err
	}
	jobsRoot := filepath.Join(l.config.DataRoot, "image-projects", job.ProjectID, "jobs")
	receiptRelative, err := filepath.Rel(jobsRoot, result.ReceiptPath)
	if err != nil || receiptRelative == ".." || strings.HasPrefix(receiptRelative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("registration receipt is outside the job root")
	}
	return l.repository.CompleteRegistration(l.ctx, job.ID, filepath.ToSlash(receiptRelative), result.DirectorySHA256, time.Now().UTC())
}
