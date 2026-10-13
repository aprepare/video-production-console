package refanalysis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/settings"
	"video-production-console/internal/store"
)

type RuntimeProvider interface {
	Runtime(context.Context) (settings.Runtime, error)
}

type Options struct {
	MediaLocator  MediaLocator
	DetectShots   func(context.Context, string, string, string) (VideoInfo, []Shot, error)
	ExtractFrames func(context.Context, string, string, []Shot, string) ([]Frame, error)
	HTTPClient    HTTPClient
}

type runningJob struct {
	cancel context.CancelFunc
	done   chan struct{}
}

type Service struct {
	repo       *store.ReferenceAnalysisRepository
	references *store.ReferenceRepository
	runtime    RuntimeProvider
	options    Options
	client     *Client
	mu         sync.Mutex
	jobs       map[string]*runningJob
	closed     bool
}

type Analysis struct {
	store.ReferenceAnalysis
	Report *Report `json:"report,omitempty"`
	Frames []Frame `json:"frames,omitempty"`
}

func New(repo *store.ReferenceAnalysisRepository, references *store.ReferenceRepository, runtime RuntimeProvider, optionValues ...Options) *Service {
	options := Options{}
	if len(optionValues) > 0 {
		options = optionValues[0]
	}
	if options.MediaLocator == nil {
		options.MediaLocator = FileMediaLocator{Runtime: runtime}
	}
	if options.DetectShots == nil {
		options.DetectShots = Shots
	}
	if options.ExtractFrames == nil {
		options.ExtractFrames = Keyframes
	}
	return &Service{repo: repo, references: references, runtime: runtime, options: options, client: NewClient(options.HTTPClient), jobs: map[string]*runningJob{}}
}

// Start 返回创建时的 queued 快照；请求结束或断开不取消后台任务。
func (s *Service) Start(ctx context.Context, referenceID, model, focus string) (store.ReferenceAnalysis, error) {
	referenceID, model = strings.TrimSpace(referenceID), strings.TrimSpace(model)
	if !validID(referenceID) || len(model) > 256 {
		return store.ReferenceAnalysis{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return store.ReferenceAnalysis{}, errors.New("分析服务已关闭")
	}
	existing, err := s.repo.Active(ctx, referenceID)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, store.ErrReferenceAnalysisNotFound) {
		return store.ReferenceAnalysis{}, err
	}
	post, err := s.references.Get(ctx, referenceID)
	if err != nil {
		return store.ReferenceAnalysis{}, err
	}
	rt, err := s.settings(ctx)
	if err != nil {
		return store.ReferenceAnalysis{}, err
	}
	video, err := s.options.MediaLocator.LocalVideo(ctx, referenceID)
	if err != nil {
		if errors.Is(err, ErrUnsafePath) || errors.Is(err, ErrInvalidInput) {
			return store.ReferenceAnalysis{}, err
		}
		return store.ReferenceAnalysis{}, errors.New("原视频缓存读取失败")
	}
	if video == "" {
		return store.ReferenceAnalysis{}, ErrMediaNotReady
	}
	cfg, err := ResolveConfig(rt, model)
	if err != nil {
		return store.ReferenceAnalysis{}, err
	}
	a := store.ReferenceAnalysis{ID: uuid.NewString(), ReferenceID: referenceID, Model: cfg.Model, Focus: truncateFocus(focus)}
	if _, err := analysisPath(rt.DataRoot, a, ""); err != nil {
		return store.ReferenceAnalysis{}, err
	}
	a, created, err := s.repo.CreateQueued(ctx, a)
	if err != nil || !created {
		return a, err
	}
	jobCtx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	job := &runningJob{cancel: cancel, done: make(chan struct{})}
	s.jobs[a.ID] = job
	go func() {
		defer func() {
			cancel()
			s.mu.Lock()
			delete(s.jobs, a.ID)
			s.mu.Unlock()
			close(job.done)
		}()
		if err := s.run(jobCtx, a, post, rt, cfg, video); err != nil {
			s.fail(a.ID, err, rt)
		}
	}()
	return a, nil
}

func (s *Service) settings(ctx context.Context) (settings.Runtime, error) {
	if s.runtime == nil {
		return settings.Runtime{}, ErrNotConfigured
	}
	rt, err := s.runtime.Runtime(ctx)
	if err != nil {
		return settings.Runtime{}, errors.New("拆解设置读取失败")
	}
	return rt, nil
}

func (s *Service) run(ctx context.Context, a store.ReferenceAnalysis, post store.ReferencePost, rt settings.Runtime, cfg ModelConfig, video string) error {
	if err := s.repo.MarkRunning(ctx, a.ID); err != nil {
		return err
	}
	info, shots, err := s.options.DetectShots(ctx, rt.FFmpegPath, rt.FFprobePath, video)
	if err != nil {
		return fmt.Errorf("镜头识别失败：%w", err)
	}
	if !positiveFinite(info.DurationS) || len(shots) == 0 {
		return errors.New("视频时长或镜头列表无效")
	}
	directory, err := analysisPath(rt.DataRoot, a, "")
	if err != nil {
		return err
	}
	framesReady := false
	defer func() {
		if !framesReady {
			if err := removeAnalysisDirectory(rt.DataRoot, a); err != nil {
				slog.Warn("清理未完成的分析帧失败", "analysis_id", a.ID)
			}
		}
	}()
	frames, err := s.options.ExtractFrames(ctx, rt.FFmpegPath, video, shots, directory)
	if err != nil {
		return fmt.Errorf("关键帧提取失败：%w", err)
	}
	if !validFrames(frames, len(frames)) {
		return errors.New("关键帧列表无效")
	}
	for i := range frames {
		if frames[i].TimeS >= info.DurationS || frames[i].Shot > len(shots) {
			return errors.New("关键帧时间或镜头编号无效")
		}
		path, err := analysisPath(rt.DataRoot, a, frameFilename(frames[i].Index))
		if err != nil {
			return err
		}
		frames[i].JPEG, err = os.ReadFile(path)
		if err != nil || len(frames[i].JPEG) == 0 {
			return errors.New("关键帧读取失败")
		}
		frames[i].URL = ""
	}
	manifestPath, err := analysisPath(rt.DataRoot, a, "frames.json")
	if err != nil {
		return err
	}
	manifest, err := json.Marshal(frames)
	if err != nil {
		return errors.New("关键帧清单生成失败")
	}
	if err := os.WriteFile(manifestPath, manifest, 0o600); err != nil {
		return errors.New("关键帧清单保存失败")
	}
	if err := s.repo.SetFrames(ctx, a.ID, len(frames)); err != nil {
		return err
	}
	framesReady = true
	report, err := s.client.Analyze(ctx, cfg, post, info, shots, frames, a.Focus)
	if err != nil {
		return err
	}
	normalized, err := json.Marshal(report)
	if err != nil {
		return errors.New("拆解报告保存格式无效")
	}
	return s.repo.Complete(ctx, a.ID, string(normalized))
}

var errorURLPattern = regexp.MustCompile(`(?i)[a-z][a-z0-9+.-]*://[^\s"'<>]+`)

func safeError(err error, rt settings.Runtime) string {
	message := err.Error()
	for _, key := range []string{rt.AnalysisAPIKey, rt.VisionAPIKey, rt.GrokAPIKey, rt.RemixAPIKey} {
		if key != "" {
			message = strings.ReplaceAll(message, key, "[密钥已隐藏]")
		}
	}
	message = errorURLPattern.ReplaceAllString(message, "[地址已隐藏]")
	runes := []rune(message)
	if len(runes) > 500 {
		message = string(runes[:500])
	}
	return message
}

func (s *Service) fail(id string, cause error, rt settings.Runtime) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.repo.Fail(ctx, id, safeError(cause, rt)); err != nil && !errors.Is(err, store.ErrReferenceAnalysisNotFound) {
		slog.Error("保存拆解失败状态失败", "analysis_id", id)
	}
}

func (s *Service) Get(ctx context.Context, id string) (Analysis, error) {
	if !validID(id) {
		return Analysis{}, ErrInvalidInput
	}
	a, err := s.repo.Get(ctx, id)
	if err != nil {
		return Analysis{}, err
	}
	out := Analysis{ReferenceAnalysis: a}
	if a.ReportJSON != "" {
		report, err := ParseReport(a.ReportJSON)
		if err != nil {
			return Analysis{}, err
		}
		out.Report = &report
	}
	if a.FrameCount > 0 {
		rt, err := s.settings(ctx)
		if err != nil {
			return Analysis{}, err
		}
		out.Frames, err = loadFrames(rt.DataRoot, a)
		if err != nil {
			return Analysis{}, err
		}
	}
	return out, nil
}

func (s *Service) ListByReference(ctx context.Context, referenceID string) ([]store.ReferenceAnalysis, error) {
	if !validID(referenceID) {
		return nil, ErrInvalidInput
	}
	if _, err := s.references.Get(ctx, referenceID); err != nil {
		return nil, err
	}
	return s.repo.ListByReference(ctx, referenceID)
}

// Resume 仅在启动挂载路由前执行，遗留任务不能继续持有 running 状态。
func (s *Service) Resume(ctx context.Context) error { return s.repo.FailInterrupted(ctx) }

func (s *Service) OpenFrame(ctx context.Context, id string, index int) (*os.File, error) {
	if !validID(id) {
		return nil, ErrInvalidInput
	}
	a, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if index < 1 || index > a.FrameCount || index > maxFrames {
		return nil, ErrFrameNotFound
	}
	rt, err := s.settings(ctx)
	if err != nil {
		return nil, err
	}
	path, err := analysisPath(rt.DataRoot, a, frameFilename(index))
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, ErrFrameNotFound
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, ErrFrameNotFound
	}
	return f, nil
}

func (s *Service) Delete(ctx context.Context, id string) error {
	if !validID(id) {
		return ErrInvalidInput
	}
	s.mu.Lock()
	job := s.jobs[id]
	if job != nil {
		job.cancel()
	}
	s.mu.Unlock()
	if job != nil {
		select {
		case <-job.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	rt, err := s.settings(ctx)
	if err != nil {
		return err
	}
	if err := removeAnalysisDirectory(rt.DataRoot, a); err != nil {
		return err
	}
	return s.repo.Delete(ctx, id)
}

// Close 由应用关闭时调用；先取消，再等待文件写入和最终状态落盘结束。
func (s *Service) Close() {
	s.mu.Lock()
	s.closed = true
	jobs := make([]*runningJob, 0, len(s.jobs))
	for _, job := range s.jobs {
		job.cancel()
		jobs = append(jobs, job)
	}
	s.mu.Unlock()
	for _, job := range jobs {
		<-job.done
	}
}
