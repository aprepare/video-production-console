// Package replicate owns persistent, resumable reference-video replication jobs.
package replicate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"video-production-console/internal/grokvideo"
	"video-production-console/internal/imageproject"
	"video-production-console/internal/refanalysis"
	"video-production-console/internal/runninghub"
	"video-production-console/internal/settings"
	"video-production-console/internal/store"
	"video-production-console/internal/videoqc"
)

type ImageGenerator interface {
	GenerateMany(context.Context, imageproject.GenerateRequest) ([]imageproject.GenerateResult, error)
}
type HubClient interface {
	Upload(context.Context, string, string, []byte) (string, error)
	RunApp(context.Context, string, string, []runninghub.Node, string) (string, error)
	Query(context.Context, string, string) (runninghub.Status, error)
	Download(context.Context, string, io.Writer, int64) error
}
type Options struct {
	Hub            HubClient
	Grok           func(settings.Runtime) (grokvideo.Client, error)
	Images         ImageGenerator
	ReferenceFrame func(context.Context, string, int) (*os.File, error)
	QC             *videoqc.Service
	Probe          func(context.Context, string, string) (refanalysis.VideoInfo, error)
	PollInterval   time.Duration
	TakeTimeout    time.Duration
}
type job struct {
	cancel                  context.CancelFunc
	done                    chan struct{}
	projectID, shotID, kind string
}
type Service struct {
	repo        *store.ReplicateRepository
	analyses    *store.ReferenceAnalysisRepository
	runtime     refanalysis.RuntimeProvider
	opt         Options
	root        *os.Root
	mu          sync.Mutex
	jobs        map[string]*job
	deleting    map[string]bool
	closed      bool
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	pool        *runninghub.KeyPool
	grokSlots   chan struct{}
	imageActive int
	changed     chan struct{}
}

func New(repo *store.ReplicateRepository, analyses *store.ReferenceAnalysisRepository, runtime refanalysis.RuntimeProvider, opt Options) (*Service, error) {
	rt, err := runtime.Runtime(context.Background())
	if err != nil {
		return nil, err
	}
	if rt.DataRoot == "" {
		return nil, invalid("未配置存储目录")
	}
	if err = os.MkdirAll(rt.DataRoot, 0700); err != nil {
		return nil, err
	}
	data, err := os.OpenRoot(rt.DataRoot)
	if err != nil {
		return nil, err
	}
	defer data.Close()
	if err = data.MkdirAll("replicate", 0700); err != nil {
		return nil, err
	}
	root, err := data.OpenRoot("replicate")
	if err != nil {
		return nil, err
	}
	if opt.Hub == nil {
		opt.Hub = &runninghub.Client{}
	}
	if opt.Grok == nil {
		opt.Grok = func(rt settings.Runtime) (grokvideo.Client, error) { return grokvideo.NewFromRuntime(rt, nil, nil) }
	}
	if opt.Images == nil {
		opt.Images = imageproject.NewClient(nil)
	}
	if opt.Probe == nil {
		opt.Probe = probeVideo
	}
	if opt.PollInterval <= 0 {
		opt.PollInterval = 22 * time.Second
	}
	if opt.TakeTimeout <= 0 {
		opt.TakeTimeout = 15 * time.Minute
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{repo: repo, analyses: analyses, runtime: runtime, opt: opt, root: root, ctx: ctx, cancel: cancel, jobs: map[string]*job{}, deleting: map[string]bool{}, pool: &runninghub.KeyPool{}, grokSlots: make(chan struct{}, 4), changed: make(chan struct{})}, nil
}
func (s *Service) SetQC(qc *videoqc.Service) { s.mu.Lock(); s.opt.QC = qc; s.mu.Unlock() }
func notFound(err error) error {
	if errors.Is(err, store.ErrReplicateNotFound) || errors.Is(err, store.ErrReferenceAnalysisNotFound) {
		return &Error{"replicate_not_found", "复刻项目、镜头或版本不存在"}
	}
	if errors.Is(err, store.ErrInvalidReplicate) {
		return invalid("复刻参数不正确")
	}
	return err
}
func validID(id string) bool { _, e := uuid.Parse(id); return e == nil }
func (s *Service) available(id string) error {
	if s.closed {
		return invalid("复刻服务已关闭")
	}
	if s.deleting[id] {
		return invalid("项目正在删除")
	}
	return nil
}
func checkText(values ...string) error {
	for _, v := range values {
		if !utf8.ValidString(v) || utf8.RuneCountInString(v) > 30000 || strings.ContainsRune(v, 0) {
			return invalid("文本内容无效或过长")
		}
	}
	return nil
}
func validProvider(p string) bool { return p == "runninghub_h3" || p == "grok" }
func validRatio(r string) bool {
	switch r {
	case "9:16", "16:9", "1:1", "3:4", "4:3":
		return true
	}
	return false
}
func durationFor(v float64, provider string) int {
	n := int(math.Round(v))
	if provider != "grok" {
		return max(1, min(10, n))
	}
	best := 6
	for _, d := range []int{10, 15} {
		if abs(n-d) < abs(n-best) {
			best = d
		}
	}
	return best
}
func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
func validDuration(n int, p string) bool {
	if p == "grok" {
		return n == 6 || n == 10 || n == 15
	}
	return n >= 1 && n <= 10
}
func validateProject(p store.ReplicateProject) error {
	if !validProvider(p.VideoProvider) || !validRatio(p.Ratio) || p.Variants < 1 || p.Variants > 4 {
		return invalid("比例、视频模型或版本数不正确")
	}
	if utf8.RuneCountInString(p.Product) > 300 {
		return invalid("产品描述不能超过300字")
	}
	return checkText(p.Title, p.Product, p.StyleLock, p.ImageModel)
}
func (s *Service) Create(ctx context.Context, in CreateRequest) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.available(""); e != nil {
		return Project{}, e
	}
	rt, e := s.runtime.Runtime(ctx)
	if e != nil {
		return Project{}, e
	}
	if in.Ratio == "" {
		in.Ratio = "9:16"
	}
	if in.VideoProvider == "" {
		in.VideoProvider = rt.ReplicateDefaultProvider
		if in.VideoProvider == "" {
			in.VideoProvider = "runninghub_h3"
		}
	}
	if in.Variants == 0 {
		in.Variants = rt.ReplicateDefaultVariants
		if in.Variants == 0 {
			in.Variants = 1
		}
	}
	p := store.ReplicateProject{ID: uuid.NewString(), Title: strings.TrimSpace(in.Title), ReferenceID: in.ReferenceID, AnalysisID: in.AnalysisID, Product: in.Product, Ratio: in.Ratio, VideoProvider: in.VideoProvider, ImageModel: in.ImageModel, Variants: in.Variants, Status: "draft"}
	shots := []store.ReplicateShot{}
	if in.Blank {
		if in.ReferenceID != "" || in.AnalysisID != "" {
			return Project{}, invalid("空白项目不能指定拆解")
		}
	} else {
		a, err := s.analyses.Get(ctx, in.AnalysisID)
		if err != nil {
			return Project{}, notFound(err)
		}
		if a.Status != "completed" || a.ReferenceID != in.ReferenceID {
			return Project{}, invalid("请选择此对标已完成的拆解")
		}
		report, err := refanalysis.ParseReport(a.ReportJSON)
		if err != nil {
			return Project{}, invalid("拆解报告无法读取")
		}
		if len(report.Replicate.AIPrompts) == 0 {
			return Project{}, &Error{"analysis_has_no_prompts", "这份拆解没有复刻提示词，请重新拆解一次"}
		}
		if len(report.Replicate.AIPrompts) > 200 {
			return Project{}, invalid("一个项目最多200个镜头")
		}
		p.StyleLock = report.Replicate.StyleLock
		for i, a := range report.Replicate.AIPrompts {
			if math.IsNaN(a.EndS) || math.IsInf(a.EndS, 0) || a.EndS < a.StartS || a.StartS < 0 {
				return Project{}, invalid("拆解镜头时间无效")
			}
			shots = append(shots, store.ReplicateShot{ID: uuid.NewString(), ProjectID: p.ID, Position: i + 1, Purpose: a.Purpose, SourceStartS: a.StartS, SourceEndS: a.EndS, DurationS: durationFor(a.EndS-a.StartS, p.VideoProvider), FirstFramePrompt: a.FirstFramePrompt, MotionPrompt: a.MotionPrompt, Negative: a.Negative, Keep: a.Keep, FirstFrameStatus: "none"})
		}
	}
	if p.Title == "" {
		p.Title = "未命名复刻项目"
	}
	if e = validateProject(p); e != nil {
		return Project{}, e
	}
	if e = s.repo.Create(ctx, p, shots); e != nil {
		return Project{}, notFound(e)
	}
	return s.get(ctx, p.ID)
}
func view(p store.ReplicateProject) Project {
	out := Project{ID: p.ID, Title: p.Title, ReferenceID: p.ReferenceID, AnalysisID: p.AnalysisID, Product: p.Product, StyleLock: p.StyleLock, Ratio: p.Ratio, VideoProvider: p.VideoProvider, ImageModel: p.ImageModel, Variants: p.Variants, Status: p.Status, FinalDurationS: p.FinalDurationS, QCID: p.QCID, Error: p.Error, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt, Shots: []Shot{}}
	if p.Status == "stitched" && p.FinalPath != "" {
		out.FinalURL = "/api/replicate/" + p.ID + "/final?v=" + p.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	for _, sh := range p.Shots {
		shot := Shot{ID: sh.ID, Position: sh.Position, Purpose: sh.Purpose, SourceStartS: sh.SourceStartS, SourceEndS: sh.SourceEndS, DurationS: sh.DurationS, FirstFramePrompt: sh.FirstFramePrompt, MotionPrompt: sh.MotionPrompt, Negative: sh.Negative, Keep: sh.Keep, FirstFrameSource: sh.FirstFrameSource, FirstFrameStatus: sh.FirstFrameStatus, FirstFrameError: sh.FirstFrameError, SelectedTakeID: sh.SelectedTakeID, Takes: []Take{}}
		if sh.FirstFramePath != "" {
			shot.FirstFrameURL = "/api/replicate/shots/" + sh.ID + "/first-frame?v=" + sh.UpdatedAt.UTC().Format(time.RFC3339Nano)
		}
		for _, t := range sh.Takes {
			take := Take{ID: t.ID, Provider: t.Provider, Status: t.Status, Error: t.Error, DurationS: t.DurationS, Width: t.Width, Height: t.Height, CostNote: t.CostNote, CreatedAt: t.CreatedAt}
			out.Progress.TakesTotal++
			switch t.Status {
			case "succeeded":
				out.Progress.TakesDone++
				take.URL = "/api/replicate/takes/" + t.ID + "/video"
			case "failed", "canceled":
				out.Progress.TakesFailed++
			default:
				out.Progress.TakesRunning++
			}
			shot.Takes = append(shot.Takes, take)
		}
		out.Shots = append(out.Shots, shot)
	}
	return out
}
func (s *Service) get(ctx context.Context, id string) (Project, error) {
	p, e := s.repo.Get(ctx, id)
	return view(p), notFound(e)
}
func (s *Service) Get(ctx context.Context, id string) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.get(ctx, id)
}
func (s *Service) List(ctx context.Context, limit int) ([]Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ps, e := s.repo.List(ctx, limit)
	if e != nil {
		return nil, e
	}
	out := []Project{}
	for _, p := range ps {
		v, e := s.get(ctx, p.ID)
		if e != nil {
			return nil, e
		}
		v.Shots = nil
		out = append(out, v)
	}
	return out, nil
}
func (s *Service) project(ctx context.Context, id string) (store.ReplicateProject, error) {
	if e := s.available(id); e != nil {
		return store.ReplicateProject{}, e
	}
	p, e := s.repo.Get(ctx, id)
	if e != nil {
		return p, notFound(e)
	}
	if p.Status == "stitching" {
		return p, invalid("正在拼接，请完成后再修改")
	}
	return p, nil
}
func invalidate(p *store.ReplicateProject) {
	p.FinalPath = ""
	p.FinalDurationS = 0
	p.QCID = ""
	p.Error = ""
	p.Status = "draft"
}
func (s *Service) Patch(ctx context.Context, id string, in ProjectPatch) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, e := s.project(ctx, id)
	if e != nil {
		return Project{}, e
	}
	if in.Title != nil {
		p.Title = *in.Title
	}
	if in.Product != nil {
		p.Product = *in.Product
	}
	if in.StyleLock != nil {
		p.StyleLock = *in.StyleLock
	}
	if in.Ratio != nil {
		p.Ratio = *in.Ratio
	}
	if in.VideoProvider != nil {
		p.VideoProvider = *in.VideoProvider
	}
	if in.ImageModel != nil {
		p.ImageModel = *in.ImageModel
	}
	if in.Variants != nil {
		p.Variants = *in.Variants
	}
	if e = validateProject(p); e != nil {
		return Project{}, e
	}
	invalidate(&p)
	if e = s.repo.SaveProject(ctx, p); e != nil {
		return Project{}, notFound(e)
	}
	return s.get(ctx, id)
}
func applyShot(sh *store.ReplicateShot, in ShotPatch, provider string) error {
	if in.Purpose != nil {
		sh.Purpose = *in.Purpose
	}
	if in.DurationS != nil {
		sh.DurationS = *in.DurationS
	}
	if in.FirstFramePrompt != nil {
		sh.FirstFramePrompt = *in.FirstFramePrompt
	}
	if in.MotionPrompt != nil {
		sh.MotionPrompt = *in.MotionPrompt
	}
	if in.Negative != nil {
		sh.Negative = *in.Negative
	}
	if in.Keep != nil {
		sh.Keep = *in.Keep
	}
	if !validDuration(sh.DurationS, provider) {
		return invalid("该视频模型不支持此时长")
	}
	return checkText(sh.Purpose, sh.FirstFramePrompt, sh.MotionPrompt, sh.Negative, sh.Keep)
}
func (s *Service) AddShot(ctx context.Context, id string, in ShotPatch) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, e := s.project(ctx, id)
	if e != nil {
		return Project{}, e
	}
	if len(p.Shots) >= 200 {
		return Project{}, invalid("一个项目最多200个镜头")
	}
	sh := store.ReplicateShot{ID: uuid.NewString(), ProjectID: id, DurationS: 6, FirstFrameStatus: "none"}
	if e = applyShot(&sh, in, p.VideoProvider); e != nil {
		return Project{}, e
	}
	after := len(p.Shots)
	if in.AfterPosition != nil {
		after = *in.AfterPosition
	}
	if after < 0 || after > len(p.Shots) {
		return Project{}, invalid("插入位置无效")
	}
	if e = s.repo.AddShot(ctx, sh, after); e != nil {
		return Project{}, notFound(e)
	}
	invalidate(&p)
	if e = s.repo.SaveProject(ctx, p); e != nil {
		return Project{}, e
	}
	return s.get(ctx, id)
}
func (s *Service) PatchShot(ctx context.Context, id string, in ShotPatch) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sh, e := s.repo.GetShot(ctx, id)
	if e != nil {
		return Project{}, notFound(e)
	}
	p, e := s.project(ctx, sh.ProjectID)
	if e != nil {
		return Project{}, e
	}
	if in.AfterPosition != nil {
		return Project{}, invalid("请使用镜头排序接口")
	}
	if e = applyShot(&sh, in, p.VideoProvider); e != nil {
		return Project{}, e
	}
	if e = s.repo.SaveShot(ctx, sh); e != nil {
		return Project{}, notFound(e)
	}
	return s.get(ctx, p.ID)
}
func (s *Service) Reorder(ctx context.Context, id string, ids []string) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, e := s.project(ctx, id)
	if e != nil {
		return Project{}, e
	}
	if e = s.repo.ReorderShots(ctx, id, ids); e != nil {
		return Project{}, notFound(e)
	}
	invalidate(&p)
	if e = s.repo.SaveProject(ctx, p); e != nil {
		return Project{}, e
	}
	return s.get(ctx, id)
}
func (s *Service) Providers(ctx context.Context) (ProvidersResponse, error) {
	rt, e := s.runtime.Runtime(ctx)
	if e != nil {
		return ProvidersResponse{}, e
	}
	keys := parseKeys(rt.RunningHubAPIKeys)
	def := rt.ReplicateDefaultProvider
	if !validProvider(def) {
		def = "runninghub_h3"
	}
	return ProvidersResponse{DefaultProvider: def, Providers: []Provider{{ID: "runninghub_h3", Label: "MiniMax H3（RunningHub）", Note: "质量好 · 每条约 3 分钟 · 约 35 币", Available: len(keys) > 0, Keys: len(keys), Durations: []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, DefaultDuration: 6}, {ID: "grok", Label: "Grok Video", Note: "快 · 便宜 · 480p", Available: strings.TrimSpace(rt.GrokAPIKey) != "", Durations: []int{6, 10, 15}, DefaultDuration: 6}}}, nil
}
func parseKeys(raw string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, line := range strings.Split(raw, "\n") {
		k := strings.TrimSpace(line)
		if k != "" && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}
func (s *Service) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		s.wg.Wait()
		return
	}
	s.closed = true
	s.cancel()
	s.mu.Unlock()
	s.wg.Wait()
	s.mu.Lock()
	s.root.Close()
	s.mu.Unlock()
}
func (s *Service) launch(id, pid, sid, kind string, fn func(context.Context) error) {
	ctx, cancel := context.WithTimeout(s.ctx, s.opt.TakeTimeout)
	j := &job{cancel: cancel, done: make(chan struct{}), projectID: pid, shotID: sid, kind: kind}
	s.jobs[id] = j
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer cancel()
		err := fn(ctx)
		s.mu.Lock()
		defer s.mu.Unlock()
		if err != nil && !s.closed {
			s.finishError(id, j, err)
		}
		delete(s.jobs, id)
		close(j.done)
		close(s.changed)
		s.changed = make(chan struct{})
	}()
}
func (s *Service) finishError(id string, j *job, cause error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	msg := "生成失败，请重试"
	var e *Error
	if errors.As(cause, &e) {
		msg = e.Message
	}
	if errors.Is(cause, context.DeadlineExceeded) {
		msg = "排队或生成超过15分钟，请重新生成"
	}
	if errors.Is(cause, context.Canceled) {
		msg = "任务已取消"
	}
	switch j.kind {
	case "take":
		t, err := s.repo.GetTake(ctx, id)
		if err == nil && t.Status != "canceled" && t.Status != "succeeded" {
			t.Status = "failed"
			t.Error = msg
			_ = s.repo.SaveTake(ctx, t)
		}
	case "image":
		sh, err := s.repo.GetShot(ctx, j.shotID)
		if err == nil {
			sh.FirstFrameStatus = "failed"
			sh.FirstFrameError = msg
			_ = s.repo.SaveShot(ctx, sh)
		}
	case "stitch":
		p, err := s.repo.Get(ctx, j.projectID)
		if err == nil {
			p.Status = "failed"
			p.Error = msg
			_ = s.repo.SaveProject(ctx, p)
		}
	}
	s.refresh(ctx, j.projectID)
}
func (s *Service) refresh(ctx context.Context, id string) {
	p, e := s.repo.Get(ctx, id)
	if e != nil || p.Status == "stitching" || p.Status == "stitched" {
		return
	}
	active, success, failed := 0, 0, 0
	for _, sh := range p.Shots {
		for _, t := range sh.Takes {
			switch t.Status {
			case "queued", "submitting", "running":
				active++
			case "succeeded":
				success++
			case "failed", "canceled":
				failed++
			}
		}
	}
	switch {
	case active > 0:
		p.Status = "generating"
	case success > 0:
		p.Status = "ready"
	case failed > 0:
		p.Status = "failed"
	default:
		p.Status = "draft"
	}
	_ = s.repo.SaveProject(ctx, p)
}
func (s *Service) selectedShots(p store.ReplicateProject, ids []string) ([]store.ReplicateShot, error) {
	if len(ids) == 0 {
		return p.Shots, nil
	}
	wanted := map[string]bool{}
	for _, id := range ids {
		if wanted[id] {
			return nil, invalid("镜头ID不能重复")
		}
		wanted[id] = true
	}
	out := []store.ReplicateShot{}
	for _, sh := range p.Shots {
		if wanted[sh.ID] {
			out = append(out, sh)
			delete(wanted, sh.ID)
		}
	}
	if len(wanted) > 0 {
		return nil, invalid("镜头不属于此项目")
	}
	return out, nil
}

// Keep persisted snapshots free of provider secrets, URLs and unknown payloads.
func safeUsage(raw any) string {
	b, e := json.Marshal(raw)
	if e != nil || string(b) == "null" {
		return ""
	}
	var v any
	if json.Unmarshal(b, &v) != nil {
		return ""
	}
	numbers := map[string]float64{}
	if obj, ok := v.(map[string]any); ok {
		for _, k := range []string{"coins", "cost", "credits", "amount", "consumeCoins", "consumeMoney", "duration"} {
			if n, ok := obj[k].(float64); ok {
				numbers[k] = n
			}
		}
	}
	if len(numbers) == 0 {
		return ""
	}
	b, _ = json.Marshal(numbers)
	return string(b)
}
func missingTakes(positions []int) error {
	return &Error{"stitch_missing_takes", fmt.Sprintf("以下镜头没有可用版本：%v", positions)}
}
