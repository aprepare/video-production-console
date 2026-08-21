package imagevideo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"video-production-console/internal/domain"
)

type ProjectReader interface {
	Get(context.Context, string) (domain.ImageProject, []domain.ImageProjectItem, error)
}

type JobCreator interface {
	CreateAndLock(context.Context, CreateJob) (Job, bool, error)
	GetByIdempotencyKey(context.Context, string) (Job, error)
	SaveNarration(context.Context, string, NarrationArtifact) error
	FailPreparation(context.Context, string, string) error
}

type RuntimeProvider interface {
	Runtime(context.Context) (RuntimeSnapshot, error)
}

type NarrationProducer interface {
	Produce(context.Context, domain.ImageProject) (NarrationArtifact, error)
}

type StartRequest struct {
	ProjectID, AccountID, IdempotencyKey string
	OutputMode                           OutputMode
}

type StartResult struct {
	Job     Job
	Created bool
}

type Service struct {
	projects          ProjectReader
	jobs              JobCreator
	runtime           RuntimeProvider
	narration         func(RuntimeSnapshot) (NarrationProducer, error)
	kick              func(string)
	retryRegistration func(context.Context, string) error
}

func (s *Service) SetRegistrationRetry(retry func(context.Context, string) error) {
	s.retryRegistration = retry
}

func (s *Service) RetryRegistration(ctx context.Context, jobID string) error {
	if s == nil || s.retryRegistration == nil {
		return fmt.Errorf("image video registration runtime is unavailable")
	}
	return s.retryRegistration(ctx, strings.TrimSpace(jobID))
}

func NewService(projects ProjectReader, jobs JobCreator, runtime RuntimeProvider, kick func(string)) *Service {
	return &Service{
		projects: projects, jobs: jobs, runtime: runtime, kick: kick,
		narration: func(value RuntimeSnapshot) (NarrationProducer, error) {
			return NewNarrationAdapterFromRuntime(value, "")
		},
	}
}

func (s *Service) Start(ctx context.Context, request StartRequest) (StartResult, error) {
	if s == nil || s.runtime == nil || s.projects == nil || s.jobs == nil {
		return StartResult{}, fmt.Errorf("image video service is not configured")
	}
	request.ProjectID = strings.TrimSpace(request.ProjectID)
	request.AccountID = strings.TrimSpace(request.AccountID)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	if request.ProjectID == "" || request.AccountID == "" || request.IdempotencyKey == "" || (request.OutputMode != ModeSlideshow && request.OutputMode != ModeImageToVideo) {
		return StartResult{}, fmt.Errorf("%w: output mode, account, and idempotency key are required", ErrConflict)
	}
	if existing, err := s.jobs.GetByIdempotencyKey(ctx, request.IdempotencyKey); err == nil {
		if existing.ProjectID != request.ProjectID || existing.AccountID != request.AccountID || existing.OutputMode != request.OutputMode {
			return StartResult{}, fmt.Errorf("%w: idempotency key belongs to another image video job", ErrConflict)
		}
		if !(existing.Status == JobFailed && existing.Phase == "preparing" && existing.NarrationRelativePath == "") {
			s.kickJob(existing.ID)
			return StartResult{Job: existing}, nil
		}
	} else if !errors.Is(err, ErrNotFound) {
		return StartResult{}, err
	}
	runtime, err := s.runtime.Runtime(ctx)
	if err != nil {
		return StartResult{}, err
	}
	if strings.TrimSpace(runtime.DataRoot) == "" || strings.TrimSpace(runtime.MachineProfilePath) == "" || strings.TrimSpace(runtime.JianyingRoot) == "" {
		return StartResult{}, fmt.Errorf("image video draft runtime is not configured")
	}
	if request.OutputMode == ModeImageToVideo && (strings.TrimSpace(runtime.GrokBaseURL) == "" || strings.TrimSpace(runtime.GrokAPIKey) == "" || strings.TrimSpace(runtime.FFmpegPath) == "" || strings.TrimSpace(runtime.FFprobePath) == "") {
		return StartResult{}, fmt.Errorf("image-to-video provider or media runtime is not configured")
	}
	project, sourceItems, err := s.projects.Get(ctx, request.ProjectID)
	if err != nil {
		return StartResult{}, err
	}
	if project.OutputModeLockedAt != nil && OutputMode(project.OutputMode) != request.OutputMode {
		return StartResult{}, fmt.Errorf("%w: requested output mode differs from the locked project", ErrConflict)
	}
	if len(sourceItems) == 0 {
		return StartResult{}, fmt.Errorf("%w: image project has no scenes", ErrConflict)
	}
	for _, item := range sourceItems {
		if item.Status != "ready" || item.ImagePath == nil || strings.TrimSpace(*item.ImagePath) == "" {
			return StartResult{}, fmt.Errorf("%w: every project image must be ready", ErrConflict)
		}
	}
	producer, err := s.narration(runtime)
	if err != nil {
		return StartResult{}, err
	}
	artifact, err := producer.Produce(ctx, project)
	if err != nil {
		return StartResult{}, err
	}
	scenes, err := BuildTimedScenes(request.OutputMode, sourceItems, artifact.TimingDocument)
	if err != nil {
		return StartResult{}, err
	}
	byID := make(map[string]domain.ImageProjectItem, len(sourceItems))
	for _, item := range sourceItems {
		byID[item.ID] = item
	}
	createItems := make([]CreateItem, 0, len(scenes))
	for _, scene := range scenes {
		source, ok := byID[scene.ImageProjectItemID]
		if !ok {
			return StartResult{}, fmt.Errorf("%w: planned image source is unavailable", ErrConflict)
		}
		relative, digest, err := snapshotImage(runtime.DataRoot, project.ID, source)
		if err != nil {
			return StartResult{}, err
		}
		if !strings.EqualFold(digest, scene.InputImageSHA256) {
			return StartResult{}, fmt.Errorf("%w: image changed while the job was being prepared", ErrConflict)
		}
		createItems = append(createItems, CreateItem{ImageProjectItemID: source.ID, Ordinal: scene.Ordinal, TimelineDurationUS: scene.TimelineDurationUS, RequestedDurationSeconds: scene.RequestedDurationSeconds, InputImageRelativePath: relative, InputImageSHA256: digest, MaxAttempts: MaxAttemptsPerRound})
	}
	_, templateFingerprint, err := BuiltInTemplate()
	if err != nil {
		return StartResult{}, err
	}
	job, existed, err := s.jobs.CreateAndLock(ctx, CreateJob{ProjectID: project.ID, AccountID: request.AccountID, OutputMode: request.OutputMode, TemplateVersion: BuiltInTemplateVersion, TemplateFingerprint: templateFingerprint, IdempotencyKey: request.IdempotencyKey, Concurrency: MaxVideoConcurrency, Items: createItems})
	if err != nil {
		return StartResult{}, err
	}
	if !existed {
		if err := s.jobs.SaveNarration(ctx, job.ID, artifact); err != nil {
			_ = s.jobs.FailPreparation(context.Background(), job.ID, "narration persistence failed")
			return StartResult{}, err
		}
	} else if job.Status == JobFailed && job.Phase == "preparing" {
		if err := s.jobs.SaveNarration(ctx, job.ID, artifact); err != nil {
			_ = s.jobs.FailPreparation(context.Background(), job.ID, "narration persistence failed")
			return StartResult{}, err
		}
		refreshed, refreshErr := s.jobs.GetByIdempotencyKey(ctx, request.IdempotencyKey)
		if refreshErr != nil {
			return StartResult{}, refreshErr
		}
		job = refreshed
	}
	s.kickJob(job.ID)
	return StartResult{Job: job, Created: !existed}, nil
}

func (s *Service) kickJob(jobID string) {
	if s.kick != nil {
		s.kick(jobID)
	}
}

func snapshotImage(dataRoot, projectID string, item domain.ImageProjectItem) (string, string, error) {
	if item.ImagePath == nil {
		return "", "", fmt.Errorf("image source is unavailable")
	}
	root := filepath.Join(dataRoot, "image-projects", projectID)
	absolute, err := filepath.Abs(*item.ImagePath)
	if err != nil {
		return "", "", err
	}
	relative, err := filepath.Rel(root, absolute)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", "", fmt.Errorf("image source is outside its managed project")
	}
	relative, absolute, err = ResolveProjectPath(dataRoot, projectID, filepath.ToSlash(relative))
	if err != nil {
		return "", "", err
	}
	file, err := os.Open(absolute)
	if err != nil {
		return "", "", err
	}
	defer file.Close()
	hash := sha256.New()
	read, err := io.Copy(hash, io.LimitReader(file, maxImageInputBytes+1))
	if err != nil || read == 0 || read > maxImageInputBytes {
		return "", "", fmt.Errorf("image source is empty or too large")
	}
	return relative, hex.EncodeToString(hash.Sum(nil)), nil
}
