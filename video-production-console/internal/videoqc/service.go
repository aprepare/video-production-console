package videoqc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"video-production-console/internal/refanalysis"
	"video-production-console/internal/settings"
	"video-production-console/internal/store"
)

var (
	ErrInvalidInput      = errors.New("视频审核参数不正确")
	ErrNotConfigured     = errors.New("还没配置看图模型，请到设置里配置")
	ErrUnsupportedFormat = errors.New("仅支持 MP4、MOV、WebM、MKV 视频")
	ErrTooLarge          = errors.New("视频上传不能超过 300MB")
	ErrUnsafePath        = errors.New("视频审核文件路径不安全")
	ErrMediaNotReady     = errors.New("原视频还没下载好，请先完成下载")
	ErrUnsupportedSource = errors.New("图生视频任务暂不支持直接审核，请先导出视频后上传")
	ErrMediaNotFound     = errors.New("审核视频或帧不存在")
)

type Input struct {
	Source   string `json:"source"`
	SourceID string `json:"source_id"`
	Product  string `json:"product"`
	Model    string `json:"model"`
}

type Options struct {
	ReplicateSource func(context.Context, string, string) (ReplicateMedia, error)
	MediaLocator    refanalysis.MediaLocator
	Inspect         func(context.Context, string, string, string) (refanalysis.VideoInfo, []float64, error)
	ExtractFrame    func(context.Context, string, string, float64) ([]byte, error)
	HTTPClient      refanalysis.HTTPClient
}

type runningJob struct {
	cancel context.CancelFunc
	done   chan struct{}
}
type Service struct {
	repo        *store.VideoQCRepository
	references  *store.ReferenceRepository
	runtime     refanalysis.RuntimeProvider
	options     Options
	client      *Client
	mu          sync.Mutex
	jobs        map[string]*runningJob
	closed      bool
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	slots       chan struct{}
	taskTimeout time.Duration
}

func New(repo *store.VideoQCRepository, references *store.ReferenceRepository, runtime refanalysis.RuntimeProvider, values ...Options) *Service {
	opt := Options{}
	if len(values) > 0 {
		opt = values[0]
	}
	if opt.MediaLocator == nil {
		opt.MediaLocator = refanalysis.FileMediaLocator{Runtime: runtime}
	}
	if opt.Inspect == nil {
		opt.Inspect = inspectMedia
	}
	if opt.ExtractFrame == nil {
		opt.ExtractFrame = extractFrame
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{repo: repo, references: references, runtime: runtime, options: opt, client: NewClient(opt.HTTPClient), jobs: map[string]*runningJob{}, ctx: ctx, cancel: cancel, slots: make(chan struct{}, 2), taskTimeout: 8 * time.Minute}
}

func validateInput(in Input) error {
	if !utf8.ValidString(in.Product) || utf8.RuneCountInString(in.Product) > 300 || !utf8.ValidString(in.Model) || len(in.Model) > 256 {
		return ErrInvalidInput
	}
	for _, r := range in.Model {
		if unicode.IsControl(r) {
			return ErrInvalidInput
		}
	}
	return nil
}

// Register an intake before Close starts waiting. Shutdown also cancels uploads
// and preflight reads; accepted jobs have their own detached context.
func (s *Service) begin(ctx context.Context) (context.Context, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, nil, errors.New("视频审核服务已关闭")
	}
	s.wg.Add(1)
	opCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.ctx, cancel)
	return opCtx, func() { stop(); cancel(); s.wg.Done() }, nil
}

func (s *Service) settings(ctx context.Context) (settings.Runtime, error) {
	if s.runtime == nil {
		return settings.Runtime{}, ErrNotConfigured
	}
	rt, err := s.runtime.Runtime(ctx)
	if err != nil {
		return rt, errors.New("视频审核设置读取失败")
	}
	return rt, nil
}

func (s *Service) prepare(ctx context.Context, in Input) (settings.Runtime, refanalysis.ModelConfig, error) {
	rt, err := s.settings(ctx)
	if err != nil {
		return rt, refanalysis.ModelConfig{}, err
	}
	cfg, err := refanalysis.ResolveConfig(rt, in.Model)
	if err != nil {
		return rt, cfg, ErrNotConfigured
	}
	return rt, cfg, nil
}

func (s *Service) Start(ctx context.Context, in Input) (Check, error) {
	if err := validateInput(in); err != nil {
		return Check{}, err
	}
	if in.Source == "replicate" || in.Source == "replicate_take" {
		return s.startReplicate(ctx, in)
	}
	if in.Source == "image_video_job" {
		return Check{}, ErrUnsupportedSource
	}
	if in.Source != "reference" || !validID(in.SourceID) {
		return Check{}, ErrInvalidInput
	}
	ctx, done, err := s.begin(ctx)
	if err != nil {
		return Check{}, err
	}
	defer done()
	rt, cfg, err := s.prepare(ctx, in)
	if err != nil {
		return Check{}, err
	}
	post, err := s.references.Get(ctx, in.SourceID)
	if err != nil {
		return Check{}, err
	}
	video, err := s.options.MediaLocator.LocalVideo(ctx, in.SourceID)
	if err != nil {
		return Check{}, ErrMediaNotReady
	}
	if video == "" {
		return Check{}, ErrMediaNotReady
	}
	info, err := os.Stat(video)
	if err != nil || !info.Mode().IsRegular() {
		return Check{}, ErrMediaNotReady
	}
	root, err := openFiles(rt.DataRoot, true)
	if err != nil {
		return Check{}, err
	}
	defer root.Close()
	id := uuid.NewString()
	if err = root.Mkdir(id, 0700); err != nil {
		return Check{}, ErrUnsafePath
	}
	a, err := s.enqueue(ctx, store.VideoQCCheck{ID: id, Source: in.Source, SourceID: in.SourceID, Title: sanitizeTitle(post.Title), Product: in.Product, Model: cfg.Model}, rt, cfg, video)
	if err != nil {
		_ = root.RemoveAll(id)
	}
	return a, err
}

func (s *Service) StartUpload(ctx context.Context, in Input, filename string, reader io.Reader) (Check, error) {
	if err := validateInput(in); err != nil {
		return Check{}, err
	}
	if in.Source != "" && in.Source != "upload" || in.SourceID != "" || !utf8.ValidString(filename) {
		return Check{}, ErrInvalidInput
	}
	ctx, done, err := s.begin(ctx)
	if err != nil {
		return Check{}, err
	}
	defer done()
	rt, cfg, err := s.prepare(ctx, in)
	if err != nil {
		return Check{}, err
	}
	root, err := openFiles(rt.DataRoot, true)
	if err != nil {
		return Check{}, err
	}
	defer root.Close()
	id := uuid.NewString()
	video, err := saveUpload(ctx, root, id, filename, reader)
	if err != nil {
		return Check{}, err
	}
	a, err := s.enqueue(ctx, store.VideoQCCheck{ID: id, Source: "upload", Title: sanitizeTitle(filename), Product: in.Product, Model: cfg.Model}, rt, cfg, video)
	if err != nil {
		_ = root.RemoveAll(id)
	}
	return a, err
}

func (s *Service) enqueue(ctx context.Context, a store.VideoQCCheck, rt settings.Runtime, cfg refanalysis.ModelConfig, video string, ranges ...[]ShotRange) (Check, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Check{}, errors.New("视频审核服务已关闭")
	}
	a, err := s.repo.CreateQueued(ctx, a)
	if err != nil {
		return Check{}, err
	}
	jobCtx, cancel := context.WithCancel(s.ctx)
	job := &runningJob{cancel: cancel, done: make(chan struct{})}
	s.jobs[a.ID] = job
	s.wg.Add(1)
	go func() {
		defer func() {
			cancel()
			s.mu.Lock()
			delete(s.jobs, a.ID)
			s.mu.Unlock()
			close(job.done)
			s.wg.Done()
		}()
		var err error
		select {
		case s.slots <- struct{}{}:
			defer func() { <-s.slots }()
			// Queue time does not consume the running task's eight minute budget.
			runCtx, cancelRun := context.WithTimeout(jobCtx, s.taskTimeout)
			err = s.run(runCtx, a, rt, cfg, video, ranges...)
			cancelRun()
		case <-jobCtx.Done():
			err = jobCtx.Err()
		}
		if err != nil {
			s.fail(a.ID, err, rt)
		}
	}()
	return Check{VideoQCCheck: a, Frames: []Frame{}}, nil
}

func (s *Service) run(ctx context.Context, a store.VideoQCCheck, rt settings.Runtime, cfg refanalysis.ModelConfig, video string, ranges ...[]ShotRange) error {
	if err := s.repo.MarkRunning(ctx, a.ID); err != nil {
		return err
	}
	info, cuts, err := s.options.Inspect(ctx, rt.FFmpegPath, rt.FFprobePath, video)
	if err != nil {
		return err
	}
	frames := sampleFrames(info.DurationS, info.FPS, cuts)
	if !validFrames(frames, info.DurationS) || info.Width <= 0 || info.Height <= 0 {
		return errors.New("视频时长或分辨率无效")
	}
	root, err := openFiles(rt.DataRoot, false)
	if err != nil {
		return err
	}
	defer root.Close()
	dir, err := openDirectory(root, a.ID)
	if err != nil {
		return err
	}
	defer dir.Close()
	for i := range frames {
		if err := ctx.Err(); err != nil {
			return err
		}
		frames[i].JPEG, err = s.options.ExtractFrame(ctx, rt.FFmpegPath, video, frames[i].TimeS)
		if err != nil {
			return fmt.Errorf("第 %d 帧提取失败：%w", i+1, err)
		}
		if !validJPEG(frames[i].JPEG) {
			return errors.New("抽帧结果不是有效的 768 像素以内 JPEG")
		}
		if err := writeExclusive(dir, frameFilename(frames[i].Index), frames[i].JPEG); err != nil {
			return err
		}
	}
	manifest, err := json.Marshal(frames)
	if err != nil {
		return errors.New("审核帧清单生成失败")
	}
	if err := writeExclusive(dir, "frames.json", manifest); err != nil {
		return err
	}
	result, err := s.client.Check(ctx, cfg, info, frames, a.Product)
	if err != nil {
		return err
	}
	for i := range result.Report.Issues {
		result.Report.Issues[i].ShotPosition = 0
		if a.Source == "replicate" && len(ranges) > 0 {
			result.Report.Issues[i].ShotPosition = shotAt(ranges[0], result.Report.Issues[i].TimeS)
		}
	}
	report, err := json.Marshal(result.Report)
	if err != nil {
		return errors.New("审核报告保存失败")
	}
	a.DurationS, a.Width, a.Height, a.FrameCount = info.DurationS, info.Width, info.Height, len(frames)
	a.Verdict, a.Score, a.ReportJSON = result.Verdict, result.Score, string(report)
	return s.repo.Complete(ctx, a.ID, a)
}

var errorURLPattern = regexp.MustCompile(`(?i)[a-z][a-z0-9+.-]*://[^\s"'<>]+`)

func (s *Service) fail(id string, cause error, rt settings.Runtime) {
	message := cause.Error()
	if errors.Is(cause, context.DeadlineExceeded) {
		message = "视频审核超过 8 分钟，已停止"
	} else if errors.Is(cause, context.Canceled) {
		message = "视频审核已取消"
	}
	for _, key := range []string{rt.AnalysisAPIKey, rt.VisionAPIKey, rt.GrokAPIKey, rt.RemixAPIKey} {
		if key != "" {
			message = strings.ReplaceAll(message, key, "[密钥已隐藏]")
		}
	}
	message = errorURLPattern.ReplaceAllString(message, "[地址已隐藏]")
	if chars := []rune(message); len(chars) > 500 {
		message = string(chars[:500])
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.repo.Fail(ctx, id, message); err != nil && !errors.Is(err, store.ErrVideoQCNotFound) {
		slog.Error("保存视频审核失败状态失败", "check_id", id)
	}
}

func (s *Service) List(ctx context.Context, limit int) ([]store.VideoQCCheck, error) {
	return s.repo.List(ctx, limit)
}

func (s *Service) Get(ctx context.Context, id string) (Check, error) {
	if !validID(id) {
		return Check{}, ErrInvalidInput
	}
	a, err := s.repo.Get(ctx, id)
	if err != nil {
		return Check{}, err
	}
	out := Check{VideoQCCheck: a, Frames: []Frame{}}
	if a.Status != "completed" {
		return out, nil
	}
	var report Report
	if json.Unmarshal([]byte(a.ReportJSON), &report) != nil {
		return Check{}, errors.New("审核报告读取失败")
	}
	out.Report = &report
	dir, err := s.directory(ctx, id)
	if err != nil {
		return Check{}, err
	}
	defer dir.Close()
	out.Frames, err = loadFrames(dir, a)
	return out, err
}

func (s *Service) directory(ctx context.Context, id string) (*os.Root, error) {
	rt, err := s.settings(ctx)
	if err != nil {
		return nil, err
	}
	root, err := openFiles(rt.DataRoot, false)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return openDirectory(root, id)
}

func (s *Service) OpenFrame(ctx context.Context, id string, index int) (*os.File, error) {
	if !validID(id) {
		return nil, ErrInvalidInput
	}
	a, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if a.Status != "completed" || index < 1 || index > a.FrameCount || index > 40 {
		return nil, ErrMediaNotFound
	}
	dir, err := s.directory(ctx, id)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	return openRegular(dir, frameFilename(index))
}

func (s *Service) OpenVideo(ctx context.Context, id string) (*os.File, error) {
	if !validID(id) {
		return nil, ErrInvalidInput
	}
	a, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if a.Source != "upload" && a.Source != "replicate" && a.Source != "replicate_take" {
		return nil, ErrMediaNotFound
	}
	dir, err := s.directory(ctx, id)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	for _, ext := range []string{".mp4", ".mov", ".webm", ".mkv"} {
		f, err := openRegular(dir, "source"+ext)
		if err == nil {
			return f, nil
		}
		if errors.Is(err, ErrUnsafePath) {
			return nil, err
		}
	}
	return nil, ErrMediaNotFound
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
	if _, err := s.repo.Get(ctx, id); err != nil {
		return err
	}
	rt, err := s.settings(ctx)
	if err != nil {
		return err
	}
	root, err := openFiles(rt.DataRoot, false)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if root != nil {
		defer root.Close()
		if info, err := root.Lstat(id); err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return ErrUnsafePath
			}
			if err := root.RemoveAll(id); err != nil {
				return errors.New("审核文件删除失败")
			}
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return s.repo.Delete(ctx, id)
}

func (s *Service) Resume(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.jobs) != 0 {
		return errors.New("审核任务执行中，无法恢复遗留状态")
	}
	return s.repo.FailInterrupted(ctx)
}

func (s *Service) Close() {
	s.mu.Lock()
	s.closed = true
	s.cancel()
	s.mu.Unlock()
	s.wg.Wait()
}
