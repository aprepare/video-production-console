// Package imagestudio owns single-prompt image generation and its persistent history.
package imagestudio

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"video-production-console/internal/domain"
	"video-production-console/internal/imageproject"
	"video-production-console/internal/settings"
	"video-production-console/internal/store"
)

var ErrNotConfigured = errors.New("还没配置生图服务，请到「设置 → 图片」填写")
var ErrClosed = errors.New("出图服务已关闭，请稍后重试")
var ErrOutputNotReady = errors.New("图片尚未生成，请稍后重试")

type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

type RuntimeProvider interface {
	Runtime(context.Context) (settings.Runtime, error)
}
type Generator interface {
	imageproject.Generator
	GenerateMany(context.Context, imageproject.GenerateRequest) ([]imageproject.GenerateResult, error)
}
type CreateRequest struct {
	Prompt      string `json:"prompt"`
	FinalPrompt string `json:"final_prompt"`
	Enhance     bool   `json:"enhance"`
	Style       string `json:"style"`
	CustomStyle string `json:"custom_style"`
	Model       string `json:"model"`
	Ratio       string `json:"ratio"`
	Count       int    `json:"count"`
	TextModel   string `json:"text_model"`
}
type EnhanceRequest struct {
	Prompt      string `json:"prompt"`
	Style       string `json:"style"`
	CustomStyle string `json:"custom_style"`
	Ratio       string `json:"ratio"`
	TextModel   string `json:"text_model"`
}
type EnhanceResult struct {
	FinalPrompt string `json:"final_prompt"`
	Model       string `json:"model"`
}
type Enhancer func(context.Context, EnhanceRequest) (EnhanceResult, error)

type job struct {
	cancel context.CancelFunc
	done   chan struct{}
}
type Service struct {
	repo      *store.ImageStudioRepository
	runtime   RuntimeProvider
	generator Generator
	enhancer  Enhancer
	root      *os.Root
	ctx       context.Context
	cancel    context.CancelFunc
	// opMu serializes create/retry/delete/close, never held by workers.
	opMu   sync.Mutex
	closed bool
	mu     sync.Mutex
	jobs   map[string]*job
	slots  chan struct{}
	wg     sync.WaitGroup
}

func NewService(repo *store.ImageStudioRepository, runtime RuntimeProvider, generator Generator, enhancer Enhancer) (*Service, error) {
	rt, err := runtime.Runtime(context.Background())
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(rt.DataRoot) == "" {
		return nil, errors.New("图片存储目录未配置")
	}
	if err = os.MkdirAll(rt.DataRoot, 0o700); err != nil {
		return nil, err
	}
	dataRoot, err := os.OpenRoot(rt.DataRoot)
	if err != nil {
		return nil, err
	}
	defer dataRoot.Close()
	if err = dataRoot.MkdirAll("image-studio", 0o700); err != nil {
		return nil, err
	}
	root, err := dataRoot.OpenRoot("image-studio")
	if err != nil {
		return nil, err
	}
	if err = repo.RecoverInterrupted(context.Background()); err != nil {
		root.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{repo: repo, runtime: runtime, generator: generator, enhancer: enhancer, root: root, ctx: ctx, cancel: cancel, jobs: make(map[string]*job), slots: make(chan struct{}, 3)}, nil
}

func Validate(req CreateRequest) error {
	for _, s := range []string{req.Prompt, req.FinalPrompt, req.Style, req.CustomStyle, req.Model, req.Ratio, req.TextModel} {
		if !utf8.ValidString(s) {
			return &ValidationError{"请输入合法的 UTF-8 文本"}
		}
	}
	if strings.TrimSpace(req.Prompt) == "" || utf8.RuneCountInString(req.Prompt) > 4000 {
		return &ValidationError{"提示词需要 1–4000 字"}
	}
	if req.FinalPrompt != "" && (strings.TrimSpace(req.FinalPrompt) == "" || utf8.RuneCountInString(req.FinalPrompt) > 4000) {
		return &ValidationError{"最终提示词需要 1–4000 字"}
	}
	if req.Count < 1 || req.Count > 4 {
		return &ValidationError{"每次可生成 1–4 张图片"}
	}
	switch req.Ratio {
	case "3:4", "4:3", "9:16", "1:1":
	default:
		return &ValidationError{"画幅请选择 3:4、4:3、9:16 或 1:1"}
	}
	if utf8.RuneCountInString(req.CustomStyle) > 500 {
		return &ValidationError{"自定义风格不能超过 500 字"}
	}
	if req.Style == "custom" {
		if strings.TrimSpace(req.CustomStyle) == "" {
			return &ValidationError{"请填写自定义风格"}
		}
	} else if req.Style != "" {
		if _, ok := imageproject.StyleInstruction(req.Style); !ok || strings.TrimSpace(req.Style) != req.Style {
			return &ValidationError{"所选风格不存在，请重新选择"}
		}
	}
	return nil
}
func ComposePrompt(prompt, style, custom string) string {
	if style == "" {
		return prompt
	}
	description := custom
	if style != "custom" {
		description, _ = imageproject.StyleInstruction(style)
	}
	return prompt + "\n" + description
}
func (s *Service) Enhance(ctx context.Context, req EnhanceRequest) (EnhanceResult, error) {
	if err := Validate(CreateRequest{Prompt: req.Prompt, Style: req.Style, CustomStyle: req.CustomStyle, Ratio: req.Ratio, TextModel: req.TextModel, Count: 1}); err != nil {
		return EnhanceResult{}, err
	}
	if s.enhancer == nil {
		return EnhanceResult{}, errors.New("尚未配置图文文本模型，请到图文制作页检查连接")
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	result, err := s.enhancer(ctx, req)
	if err != nil {
		return EnhanceResult{}, err
	}
	result.FinalPrompt = ComposePrompt(strings.TrimSpace(result.FinalPrompt), req.Style, req.CustomStyle)
	if err := Validate(CreateRequest{Prompt: req.Prompt, FinalPrompt: result.FinalPrompt, Ratio: req.Ratio, Count: 1}); err != nil || strings.TrimSpace(result.FinalPrompt) == "" {
		return EnhanceResult{}, errors.New("文本模型未返回有效的扩写，请重试")
	}
	return result, nil
}
func (s *Service) configured(ctx context.Context) (settings.Runtime, error) {
	rt, err := s.runtime.Runtime(ctx)
	if err != nil {
		return rt, err
	}
	if strings.TrimSpace(rt.ImageBaseURL) == "" || strings.TrimSpace(rt.ImageAPIKey) == "" || s.generator == nil {
		return rt, ErrNotConfigured
	}
	return rt, nil
}
func (s *Service) Create(ctx context.Context, req CreateRequest) (domain.ImageGeneration, error) {
	if err := Validate(req); err != nil {
		return domain.ImageGeneration{}, err
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if s.closed {
		return domain.ImageGeneration{}, ErrClosed
	}
	rt, err := s.configured(ctx)
	if err != nil {
		return domain.ImageGeneration{}, err
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = rt.ImageModel
	}
	if model == "" {
		model = "grok-imagine-image-2.0"
	}
	now := time.Now().UTC()
	final := req.FinalPrompt
	if final == "" {
		final = ComposePrompt(req.Prompt, req.Style, req.CustomStyle)
	}
	g := domain.ImageGeneration{ID: uuid.NewString(), Prompt: req.Prompt, FinalPrompt: final, Enhance: req.Enhance, Style: req.Style, CustomStyle: req.CustomStyle, Model: model, Ratio: req.Ratio, Count: req.Count, Status: "queued", CreatedAt: now, UpdatedAt: now, Outputs: make([]domain.ImageGenerationOutput, req.Count)}
	for i := range g.Outputs {
		g.Outputs[i] = domain.ImageGenerationOutput{ID: uuid.NewString(), GenerationID: g.ID, Position: i, Status: "pending", CreatedAt: now, UpdatedAt: now}
	}
	if err = s.repo.Create(ctx, g); err != nil {
		return g, err
	}
	s.launch(g, rt, req.FinalPrompt == "" && req.Enhance, req.TextModel, false)
	return g, nil
}
func (s *Service) Get(ctx context.Context, id string) (domain.ImageGeneration, error) {
	return s.repo.Get(ctx, id)
}
func (s *Service) List(ctx context.Context, opt store.ImageGenerationListOptions) ([]domain.ImageGeneration, bool, error) {
	return s.repo.List(ctx, opt)
}
func (s *Service) Favorite(ctx context.Context, id string, value bool) (domain.ImageGeneration, error) {
	if err := s.repo.Favorite(ctx, id, value); err != nil {
		return domain.ImageGeneration{}, err
	}
	return s.Get(ctx, id)
}

func (s *Service) launch(g domain.ImageGeneration, rt settings.Runtime, enhance bool, textModel string, retry bool) {
	ctx, cancel := context.WithCancel(s.ctx)
	j := &job{cancel: cancel, done: make(chan struct{})}
	s.mu.Lock()
	s.jobs[g.ID] = j
	s.mu.Unlock()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() { cancel(); s.mu.Lock(); delete(s.jobs, g.ID); s.mu.Unlock(); close(j.done) }()
		select {
		case s.slots <- struct{}{}:
			defer func() { <-s.slots }()
		case <-ctx.Done():
			s.finish(g.ID, "出图已取消，可以重试")
			return
		}
		ctx, budgetCancel := context.WithTimeout(ctx, 6*time.Minute)
		defer budgetCancel()
		err := s.run(ctx, g, rt, enhance, textModel, retry)
		message := ""
		if err != nil {
			message = imageproject.ImageErrorMessage(err, g.Model)
			var expansion *enhanceFailure
			if errors.As(err, &expansion) {
				message = "提示词扩写失败，请检查图文文本模型后重试"
			}
			if errors.Is(err, context.Canceled) {
				message = "出图已取消，可以重试"
			}
		}
		s.finish(g.ID, message)
	}()
}

type enhanceFailure struct{ err error }

func (e *enhanceFailure) Error() string { return "prompt enhancement failed" }
func (e *enhanceFailure) Unwrap() error { return e.err }

func (s *Service) run(ctx context.Context, g domain.ImageGeneration, rt settings.Runtime, enhance bool, textModel string, retry bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.repo.Running(ctx, g.ID); err != nil {
		return err
	}
	if enhance {
		result, err := s.Enhance(ctx, EnhanceRequest{Prompt: g.Prompt, Style: g.Style, CustomStyle: g.CustomStyle, Ratio: g.Ratio, TextModel: textModel})
		if err != nil {
			return &enhanceFailure{err}
		}
		g.FinalPrompt = result.FinalPrompt
		if err = s.repo.FinalPrompt(ctx, g.ID, g.FinalPrompt); err != nil {
			return err
		}
	}
	pending := []domain.ImageGenerationOutput{}
	for _, o := range g.Outputs {
		if o.Status == "pending" {
			pending = append(pending, o)
		}
	}
	if len(pending) == 0 {
		return nil
	}
	req := imageproject.GenerateRequest{BaseURL: rt.ImageBaseURL, APIKey: rt.ImageAPIKey, Model: g.Model, Prompt: g.FinalPrompt, Ratio: g.Ratio, Count: len(pending), Stream: rt.ImageStream}
	if !retry {
		results, err := s.generator.GenerateMany(ctx, req)
		// Keep every successfully decoded result even if a later result failed.
		for i, result := range results {
			if i >= len(pending) {
				break
			}
			if e := s.save(ctx, pending[i], result); e != nil {
				return e
			}
		}
		if isFatal(err) {
			return err
		}
		n := min(len(results), len(pending))
		pending = pending[n:]
	}
	if len(pending) == 0 {
		return nil
	}
	concurrency := max(1, min(g.Count, rt.MaxImageConcurrency))
	attempts := rt.ImageGenerationAttempts
	if attempts < 1 {
		attempts = 2
	}
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan domain.ImageGenerationOutput)
	var wg sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex
	for range min(concurrency, len(pending)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for o := range jobs {
				one := req
				one.Count = 1
				result, err := imageproject.GenerateWithRetry(workCtx, s.generator, one, attempts)
				if err == nil {
					err = s.save(workCtx, o, result)
				}
				if err != nil {
					message := imageproject.ImageErrorMessage(err, g.Model)
					finalCtx, c := context.WithTimeout(context.Background(), 10*time.Second)
					o.Status = "failed"
					o.Error = message
					_ = s.repo.SaveOutput(finalCtx, o)
					c()
					errMu.Lock()
					if firstErr == nil || isFatal(err) {
						firstErr = err
					}
					errMu.Unlock()
					if isFatal(err) {
						cancel()
					}
				}
			}
		}()
	}
dispatch:
	for _, o := range pending {
		select {
		case jobs <- o:
		case <-workCtx.Done():
			break dispatch
		}
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	return ctx.Err()
}
func isFatal(err error) bool {
	var e *imageproject.HTTPStatusError
	return errors.As(err, &e) && (e.StatusCode == 400 || e.StatusCode == 401 || e.StatusCode == 403)
}

func (s *Service) save(ctx context.Context, o domain.ImageGenerationOutput, result imageproject.GenerateResult) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if result.Error != nil {
		return result.Error
	}
	if len(result.Bytes) == 0 || result.Width < 1 || result.Height < 1 {
		return errors.New("图片数据无效")
	}
	if _, err := uuid.Parse(o.GenerationID); err != nil {
		return errors.New("图片目录无效")
	}
	if err := s.root.MkdirAll(o.GenerationID, 0o700); err != nil {
		return err
	}
	path := filepath.Join(o.GenerationID, fmt.Sprintf("%d_%s%s", o.Position, uuid.NewString(), Extension(result.MIMEType)))
	f, err := s.root.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(result.Bytes)
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = s.root.Remove(path)
		return err
	}
	o.Status = "ready"
	o.Path = path
	o.MIMEType = result.MIMEType
	o.Width = result.Width
	o.Height = result.Height
	o.Bytes = int64(len(result.Bytes))
	o.Error = ""
	if err = s.repo.SaveOutput(ctx, o); err != nil {
		_ = s.root.Remove(path)
		return err
	}
	return nil
}
func (s *Service) finish(id, message string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	explicitReason := message != ""
	if !explicitReason {
		message = "生图服务没有返回完整图片，可以重试"
	}
	if err := s.repo.FailPending(ctx, id, message); err != nil {
		return
	}
	g, err := s.repo.Get(ctx, id)
	if err != nil {
		return
	}
	ready := 0
	failure := ""
	for _, o := range g.Outputs {
		if o.Status == "ready" {
			ready++
		} else if failure == "" && o.Error != "" {
			failure = o.Error
		}
	}
	status := "failed"
	if failure != "" && !explicitReason {
		message = failure
	}
	if ready == g.Count {
		status = "completed"
		message = ""
	} else if ready > 0 {
		status = "partial"
		message = fmt.Sprintf("只出了 %d 张（共 %d 张），%s", ready, g.Count, message)
	}
	_ = s.repo.Finish(ctx, id, status, message)
}
func (s *Service) Retry(ctx context.Context, id string) (domain.ImageGeneration, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if s.closed {
		return domain.ImageGeneration{}, ErrClosed
	}
	g, err := s.Get(ctx, id)
	if err != nil {
		return g, err
	}
	if g.Status == "queued" || g.Status == "running" {
		return g, store.ErrImageGenerationRunning
	}
	// The terminal state is committed just before the old worker exits. Wait
	// for that cleanup so its deferred map removal cannot erase the new retry.
	s.mu.Lock()
	previous := s.jobs[id]
	s.mu.Unlock()
	if previous != nil {
		select {
		case <-previous.done:
		case <-ctx.Done():
			return g, ctx.Err()
		}
	}
	failed := 0
	for _, o := range g.Outputs {
		if o.Status == "failed" {
			failed++
		}
	}
	if failed == 0 {
		return g, nil
	}
	rt, err := s.configured(ctx)
	if err != nil {
		return g, err
	}
	if err = s.repo.Retry(ctx, id); err != nil {
		return g, err
	}
	g, err = s.Get(ctx, id)
	if err != nil {
		return g, err
	}
	s.launch(g, rt, false, "", true)
	return g, nil
}
func (s *Service) Delete(ctx context.Context, id string) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if _, err := s.Get(ctx, id); err != nil {
		return err
	}
	if _, err := uuid.Parse(id); err != nil {
		return errors.New("图片目录无效")
	}
	s.mu.Lock()
	j := s.jobs[id]
	s.mu.Unlock()
	if j != nil {
		j.cancel()
		<-j.done
	}
	if err := s.root.RemoveAll(id); err != nil {
		return err
	}
	return s.repo.Delete(ctx, id)
}
func (s *Service) OpenOutput(ctx context.Context, id string) (*os.File, domain.ImageGenerationOutput, error) {
	o, err := s.repo.Output(ctx, id)
	if err != nil {
		return nil, o, err
	}
	if o.Status != "ready" {
		return nil, o, ErrOutputNotReady
	}
	if _, err = uuid.Parse(o.GenerationID); err != nil {
		return nil, o, errors.New("图片路径无效")
	}
	if filepath.Dir(o.Path) != o.GenerationID || filepath.Base(o.Path) == "." {
		return nil, o, errors.New("图片路径无效")
	}
	f, err := s.root.Open(o.Path)
	if err != nil {
		return nil, o, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, o, errors.New("图片文件无效")
	}
	return f, o, nil
}
func Extension(mime string) string {
	switch mime {
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	default:
		return ".png"
	}
}

func (s *Service) Close() {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	s.cancel()
	s.wg.Wait()
	_ = s.root.Close()
}
