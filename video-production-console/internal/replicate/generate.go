package replicate

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"video-production-console/internal/grokvideo"
	"video-production-console/internal/runninghub"
	"video-production-console/internal/settings"
	"video-production-console/internal/store"
)

func buildPrompt(p store.ReplicateProject, sh store.ReplicateShot) (string, string, error) {
	motion := strings.TrimSpace(sh.MotionPrompt)
	if motion == "" {
		return "", "", invalid("请先填写动作提示词")
	}
	const limit = 4000
	if utf8.RuneCountInString(motion) > limit {
		return "", "", invalid("动作提示词不能超过4000字")
	}
	suffix := "。画面风格：" + p.StyleLock
	if sh.Negative != "" {
		suffix += "。避免：" + sh.Negative
	}
	if sh.Keep != "" {
		suffix += "。商品外观保持：" + sh.Keep
	}
	remaining := limit - utf8.RuneCountInString(motion)
	r := []rune(suffix)
	warning := ""
	if len(r) > remaining {
		r = r[:remaining]
		warning = "附加风格提示词过长，已截断；动作提示词完整保留"
	}
	return motion + string(r), warning, nil
}
func providerConfigured(rt settings.Runtime, p string) error {
	if p == "runninghub_h3" && len(parseKeys(rt.RunningHubAPIKeys)) == 0 {
		return &Error{"video_provider_not_configured", "MiniMax 视频接口暂不可用，请联系管理员"}
	}
	if p == "grok" && strings.TrimSpace(rt.GrokAPIKey) == "" {
		return &Error{"video_provider_not_configured", "还没配置 Grok Key，请到设置填写"}
	}
	return nil
}

type takeInput struct {
	p     store.ReplicateProject
	sh    store.ReplicateShot
	take  store.ReplicateTake
	image []byte
	mime  string
}

func (s *Service) Generate(ctx context.Context, id string, in GenerateRequest) (GenerateResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, e := s.project(ctx, id)
	if e != nil {
		return GenerateResponse{}, e
	}
	shots, e := s.selectedShots(p, in.ShotIDs)
	if e != nil {
		return GenerateResponse{}, e
	}
	provider := in.Provider
	if provider == "" {
		provider = p.VideoProvider
	}
	if !validProvider(provider) {
		return GenerateResponse{}, invalid("视频模型无效")
	}
	variants := in.Variants
	if variants == 0 {
		variants = p.Variants
	}
	if variants < 1 || variants > 4 {
		return GenerateResponse{}, invalid("每个镜头版本数必须为1到4")
	}
	rt, e := s.runtime.Runtime(ctx)
	if e != nil {
		return GenerateResponse{}, e
	}
	if e = providerConfigured(rt, provider); e != nil {
		return GenerateResponse{}, e
	}
	s.pool.Configure(parseKeys(rt.RunningHubAPIKeys), rt.RunningHubPerKeyConcurrency)
	out := GenerateResponse{Skipped: []Skipped{}}
	inputs := []takeInput{}
	takes := []store.ReplicateTake{}
	for _, sh := range shots {
		if sh.FirstFramePath == "" {
			out.Skipped = append(out.Skipped, Skipped{sh.ID, "先出首帧图或上传图片"})
			continue
		}
		image, mime, e := s.readImage(sh)
		if e != nil {
			out.Skipped = append(out.Skipped, Skipped{sh.ID, "先出首帧图或上传图片"})
			continue
		}
		if !validDuration(sh.DurationS, provider) {
			return out, invalid("部分镜头时长不适用于选中的视频模型，请先修改时长")
		}
		prompt, warning, e := buildPrompt(p, sh)
		if e != nil {
			return out, e
		}
		for range variants {
			t := store.ReplicateTake{ID: uuid.NewString(), ShotID: sh.ID, Provider: provider, Prompt: prompt, Status: "queued", KeySlot: -1, Error: warning, CreatedAt: time.Now().UTC()}
			takes = append(takes, t)
			inputs = append(inputs, takeInput{p: p, sh: sh, take: t, image: image, mime: mime})
		}
	}
	if len(takes) > 0 {
		if e = s.repo.CreateTakes(ctx, takes); e != nil {
			return out, e
		}
		p.Status = "generating"
		p.Error = ""
		if e = s.repo.SaveProject(ctx, p); e != nil {
			return out, e
		}
		for _, input := range inputs {
			input := input
			s.launch(input.take.ID, p.ID, input.sh.ID, "take", func(ctx context.Context) error { return s.runTake(ctx, input, false) })
		}
	}
	out.Project, e = s.get(ctx, id)
	return out, e
}
func (s *Service) saveTake(ctx context.Context, t store.ReplicateTake, allowed ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return e
	}
	ok, e := s.repo.SaveTakeIfStatus(ctx, t, allowed...)
	if e != nil {
		return e
	}
	if !ok {
		return context.Canceled
	}
	return nil
}
func (s *Service) runTake(ctx context.Context, in takeInput, resume bool) error {
	t := in.take
	if !t.CreatedAt.IsZero() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, t.CreatedAt.Add(s.opt.TakeTimeout))
		defer cancel()
	}
	rt, e := s.runtime.Runtime(ctx)
	if e != nil {
		return e
	}
	if e = providerConfigured(rt, t.Provider); e != nil {
		return e
	}
	var download func(context.Context, string, io.Writer) error
	var videoURL, cost string
	if t.Provider == "grok" {
		select {
		case s.grokSlots <- struct{}{}:
			defer func() { <-s.grokSlots }()
		case <-ctx.Done():
			return ctx.Err()
		}
		client, e := s.opt.Grok(rt)
		if e != nil {
			return translateProvider(e)
		}
		download = client.Download
		if !resume {
			t.Status = "submitting"
			if e = s.saveTake(ctx, t, "queued"); e != nil {
				return e
			}
			req, e := client.Submit(ctx, grokvideo.SubmitRequest{Prompt: t.Prompt, Seconds: in.sh.DurationS, Image: in.image, ImageMIMEType: in.mime})
			if e != nil {
				return translateProvider(e)
			}
			t.ProviderTaskID = req.ID
			t.Status = "running"
			if e = s.persistSubmitted(t); e != nil {
				return e
			}
		}
		for {
			status, e := client.Poll(ctx, t.ProviderTaskID)
			if e != nil {
				return translateProvider(e)
			}
			if status.State == grokvideo.StateDone {
				videoURL = status.VideoURL
				break
			}
			if status.State == grokvideo.StateFailed {
				return translateProvider(&grokvideo.ProviderError{Code: status.ErrorCode, Message: status.ErrorMessage})
			}
			if e = waitPoll(ctx, s.opt.PollInterval); e != nil {
				return e
			}
		}
	} else {
		s.pool.Configure(parseKeys(rt.RunningHubAPIKeys), rt.RunningHubPerKeyConcurrency)
		var lease *runninghub.Lease
		if resume {
			lease, e = s.acquireHub(ctx, t.KeySlot)
		} else {
			lease, e = s.acquireHub(ctx, -1)
		}
		if e != nil {
			return translateProvider(e)
		}
		defer func() { lease.Release() }()
		if !resume {
			for attempt := 0; attempt < 2; attempt++ {
				t.KeySlot = lease.Slot()
				t.Status = "submitting"
				if e = s.saveTake(ctx, t, "queued", "submitting"); e != nil {
					return e
				}
				var name string
				name, e = s.opt.Hub.Upload(ctx, lease.Key(), filepath.Base(in.sh.FirstFramePath), in.image)
				if e == nil {
					var nodes []runninghub.Node
					nodes, e = runninghub.BuildH3Nodes(runninghub.H3Request{Image: in.image, ImageName: filepath.Base(in.sh.FirstFramePath), Prompt: t.Prompt, DurationS: in.sh.DurationS, Ratio: in.p.Ratio, PromptOptimize: true}, name)
					if e == nil {
						t.ProviderTaskID, e = s.opt.Hub.RunApp(ctx, lease.Key(), runninghub.H3WebappID, nodes, "default")
					}
				}
				if e == nil {
					break
				}
				if !runninghub.ShouldDisable(e) {
					return translateProvider(e)
				}
				lease.Disable()
				if attempt == 1 {
					return translateProvider(e)
				}
				lease.Release()
				lease, e = s.acquireHub(ctx, -1)
				if e != nil {
					return translateProvider(e)
				}
			}
			t.Status = "running"
			if e = s.persistSubmitted(t); e != nil {
				return e
			}
		}
		download = func(ctx context.Context, url string, w io.Writer) error {
			return s.opt.Hub.Download(ctx, url, w, maxVideoBytes)
		}
		for {
			status, e := s.opt.Hub.Query(ctx, lease.Key(), t.ProviderTaskID)
			if e != nil {
				if runninghub.ShouldDisable(e) {
					lease.Disable()
				}
				return translateProvider(e)
			}
			switch strings.ToUpper(status.State) {
			case "SUCCESS":
				videoURL = status.VideoURL()
				cost = safeUsage(status.Usage)
			case "FAILED", "FAILURE", "CANCELED", "CANCELLED":
				return translateProvider(&runninghub.ProviderError{Code: status.ErrorCode})
			}
			if videoURL != "" {
				break
			}
			if strings.EqualFold(status.State, "SUCCESS") {
				return invalid("上游已完成但没有返回视频")
			}
			if e = waitPoll(ctx, s.opt.PollInterval); e != nil {
				return e
			}
		}
	}
	if videoURL == "" {
		return invalid("上游没有返回视频")
	}
	path := filepath.Join(in.p.ID, "takes", t.ID+".mp4")
	tmp := path + ".tmp"
	s.mu.Lock()
	e = ctx.Err()
	if e == nil {
		e = s.root.MkdirAll(filepath.Dir(path), 0700)
	}
	var f *os.File
	if e == nil {
		f, e = s.root.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	}
	s.mu.Unlock()
	if e != nil {
		return e
	}
	defer func() { s.mu.Lock(); _ = s.root.Remove(tmp); s.mu.Unlock() }()
	limited := &limitedWriter{writer: f, remaining: maxVideoBytes}
	e = download(ctx, videoURL, limited)
	ce := f.Close()
	if e != nil {
		return translateProvider(e)
	}
	if ce != nil {
		return ce
	}
	info, e := s.opt.Probe(ctx, rt.FFprobePath, filepath.Join(s.root.Name(), tmp))
	if e != nil {
		return invalid("生成的视频无法读取，请重新生成")
	}
	if info.DurationS <= 0 || info.Width <= 0 || info.Height <= 0 {
		return invalid("生成的视频无有效画面")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e = ctx.Err(); e != nil {
		return e
	}
	current, e := s.repo.GetTake(ctx, t.ID)
	if e != nil {
		return e
	}
	if current.Status != "running" {
		return context.Canceled
	}
	if e = s.root.Rename(tmp, path); e != nil {
		return e
	}
	t.Status = "succeeded"
	t.Path = path
	t.DurationS = info.DurationS
	t.Width = info.Width
	t.Height = info.Height
	t.CostNote = cost
	ok, e := s.repo.SaveTakeIfStatus(ctx, t, "running")
	if e != nil || !ok {
		_ = s.root.Remove(path)
		return e
	}
	s.refresh(ctx, in.p.ID)
	return nil
}

// A successful submission must survive local shutdown. Never use the canceled
// polling context to save the upstream ID; cancellation still wins via CAS.
func (s *Service) persistSubmitted(t store.ReplicateTake) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ok, e := s.repo.SaveTakeIfStatus(ctx, t, "submitting")
	if e != nil {
		return e
	}
	if !ok {
		return context.Canceled
	}
	return nil
}
func waitPoll(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Refresh settings while queued, so changing keys or capacity does not require
// another Generate request to wake existing work. The task deadline is retained.
func (s *Service) acquireHub(ctx context.Context, slot int) (*runninghub.Lease, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rt, err := s.runtime.Runtime(ctx)
		if err != nil {
			return nil, err
		}
		s.pool.Configure(parseKeys(rt.RunningHubAPIKeys), rt.RunningHubPerKeyConcurrency)
		wait, cancel := context.WithTimeout(ctx, time.Second)
		var lease *runninghub.Lease
		if slot < 0 {
			lease, err = s.pool.Acquire(wait)
		} else {
			lease, err = s.pool.AcquireSlot(wait, slot)
		}
		cancel()
		if err == nil {
			return lease, nil
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
	}
}

type limitedWriter struct {
	writer    io.Writer
	remaining int64
}

func (w *limitedWriter) Write(b []byte) (int, error) {
	if int64(len(b)) > w.remaining {
		return 0, errors.New("视频超过下载上限")
	}
	n, e := w.writer.Write(b)
	w.remaining -= int64(n)
	return n, e
}
func translateProvider(e error) error {
	if errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
		return e
	}
	code := ""
	var h *runninghub.ProviderError
	var g *grokvideo.ProviderError
	status := 0
	if errors.As(e, &h) {
		code = h.Code
		status = h.StatusCode
	}
	if errors.As(e, &g) {
		code = g.Code
		status = g.StatusCode
	}
	code = strings.ToLower(code)
	switch {
	case strings.Contains(code, "balance") || strings.Contains(code, "credit") || strings.Contains(code, "money") || status == 402:
		return invalid("余额不足，请充值或更换Key")
	case strings.Contains(code, "auth") || strings.Contains(code, "key") || status == 401 || status == 403:
		return invalid("Key无效或无权限，请检查设置")
	case strings.Contains(code, "safety") || strings.Contains(code, "content") || strings.Contains(code, "moderation"):
		return invalid("内容未通过模型审核，请调整提示词或图片")
	case strings.Contains(code, "prompt") || strings.Contains(code, "length"):
		return invalid("提示词过长或格式不正确，请缩短后重试")
	case strings.Contains(code, "timeout") || status == 408 || status == 504:
		return invalid("上游排队超时，请稍后重试")
	}
	return invalid("视频服务请求失败，请检查配置或稍后重试")
}
func (s *Service) Resume(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.available(""); e != nil {
		return e
	}
	if len(s.jobs) > 0 {
		return invalid("正在执行任务，不能重复恢复")
	}
	rt, e := s.runtime.Runtime(ctx)
	if e != nil {
		return e
	}
	s.pool.Configure(parseKeys(rt.RunningHubAPIKeys), rt.RunningHubPerKeyConcurrency)
	takes, e := s.repo.ActiveTakes(ctx)
	if e != nil {
		return e
	}
	for _, t := range takes {
		sh, e := s.repo.GetShot(ctx, t.ShotID)
		if e != nil {
			return e
		}
		p, e := s.repo.Get(ctx, sh.ProjectID)
		if e != nil {
			return e
		}
		if t.ProviderTaskID == "" {
			t.Status = "failed"
			t.Error = "控制台重启中断，请重新生成"
			if e = s.repo.SaveTake(ctx, t); e != nil {
				return e
			}
			s.refresh(ctx, p.ID)
			continue
		}
		t.Status = "running"
		if e = s.repo.SaveTake(ctx, t); e != nil {
			return e
		}
		in := takeInput{p: p, sh: sh, take: t}
		s.launch(t.ID, p.ID, sh.ID, "take", func(ctx context.Context) error { return s.runTake(ctx, in, true) })
	}
	// Interrupted image and stitch jobs are local operations, never resubmitted.
	ps, e := s.repo.InterruptedProjects(ctx)
	if e != nil {
		return e
	}
	for _, p := range ps {
		p, e = s.repo.Get(ctx, p.ID)
		if e != nil {
			return e
		}
		if p.Status == "stitching" {
			p.Status = "failed"
			p.Error = "控制台重启中断，请重新拼接"
			if e = s.repo.SaveProject(ctx, p); e != nil {
				return e
			}
		}
		for _, sh := range p.Shots {
			if sh.FirstFrameStatus == "generating" {
				sh.FirstFrameStatus = "failed"
				sh.FirstFrameError = "控制台重启中断，请重新生成"
				if e = s.repo.SaveShot(ctx, sh); e != nil {
					return e
				}
			}
		}
	}
	return nil
}
