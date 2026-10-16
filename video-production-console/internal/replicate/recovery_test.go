package replicate

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"video-production-console/internal/grokvideo"
	"video-production-console/internal/refanalysis"
	"video-production-console/internal/runninghub"
	"video-production-console/internal/settings"
	"video-production-console/internal/store"
)

func waitTake(t *testing.T, repo *store.ReplicateRepository, id, status string) store.ReplicateTake {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		v, e := repo.GetTake(t.Context(), id)
		if e != nil {
			t.Fatal(e)
		}
		if v.Status == status {
			return v
		}
		if v.Status == "failed" && status != "failed" {
			t.Fatalf("take failed: %s", v.Error)
		}
		time.Sleep(time.Millisecond)
	}
	v, _ := repo.GetTake(t.Context(), id)
	t.Fatalf("expected %s got %+v", status, v)
	return v
}

type resumeGrok struct {
	submits, polls atomic.Int32
	gate           chan struct{}
}

func (g *resumeGrok) Submit(context.Context, grokvideo.SubmitRequest) (grokvideo.Request, error) {
	g.submits.Add(1)
	return grokvideo.Request{ID: "paid-task"}, nil
}
func (g *resumeGrok) Poll(ctx context.Context, id string) (grokvideo.Status, error) {
	g.polls.Add(1)
	select {
	case <-g.gate:
		return grokvideo.Status{State: grokvideo.StateDone, VideoURL: "https://example.com/video"}, nil
	case <-ctx.Done():
		return grokvideo.Status{}, ctx.Err()
	}
}
func (g *resumeGrok) Download(ctx context.Context, url string, w io.Writer) error {
	_, e := w.Write([]byte("fake-video"))
	return e
}
func TestRestartResumesWithoutResubmission(t *testing.T) {
	g := &resumeGrok{gate: make(chan struct{})}
	s, repo := fixture(t, g)
	p, e := s.Create(t.Context(), CreateRequest{Blank: true, VideoProvider: "grok"})
	if e != nil {
		t.Fatal(e)
	}
	p = addReady(t, s, p.ID)
	out, e := s.Generate(t.Context(), p.ID, GenerateRequest{})
	if e != nil {
		t.Fatal(e)
	}
	id := out.Project.Shots[0].Takes[0].ID
	waitTake(t, repo, id, "running")
	s.Close()
	v, _ := repo.GetTake(t.Context(), id)
	if v.Status != "running" || v.ProviderTaskID != "paid-task" {
		t.Fatalf("lost resumable task: %+v", v)
	}
	for _, status := range []string{"queued", "submitting"} {
		v := store.ReplicateTake{ID: uuid.NewString(), ShotID: p.Shots[0].ID, Provider: "grok", Prompt: "motion", Status: status, KeySlot: -1}
		if e = repo.CreateTakes(t.Context(), []store.ReplicateTake{v}); e != nil {
			t.Fatal(e)
		}
	}
	resumed := &resumeGrok{gate: make(chan struct{})}
	close(resumed.gate)
	opt := s.opt
	opt.Grok = func(settings.Runtime) (grokvideo.Client, error) { return resumed, nil }
	s2, e := New(repo, nil, s.runtime, opt)
	if e != nil {
		t.Fatal(e)
	}
	defer s2.Close()
	if e = s2.Resume(t.Context()); e != nil {
		t.Fatal(e)
	}
	waitTake(t, repo, id, "succeeded")
	p, e = s2.Get(t.Context(), p.ID)
	if e != nil {
		t.Fatal(e)
	}
	if resumed.submits.Load() != 0 || resumed.polls.Load() == 0 || p.Progress.TakesFailed != 2 {
		t.Fatalf("duplicate submit or interrupted states: %+v submissions=%d polls=%d", p, resumed.submits.Load(), resumed.polls.Load())
	}
	for _, v := range p.Shots[0].Takes {
		if v.Status == "failed" && v.Error != "控制台重启中断，请重新生成" {
			t.Fatal(v.Error)
		}
	}
}

type hotRuntime struct {
	mu sync.Mutex
	rt settings.Runtime
}

func (r *hotRuntime) Runtime(context.Context) (settings.Runtime, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rt, nil
}

type fakeHub struct {
	mu                     sync.Mutex
	active, max, submitted int
	keys                   []string
	gate                   chan struct{}
	reject                 string
	byTask                 map[string]string
}

func (h *fakeHub) Upload(ctx context.Context, key, name string, data []byte) (string, error) {
	return "uploaded.png", nil
}
func (h *fakeHub) RunApp(ctx context.Context, key, app string, nodes []runninghub.Node, instance string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.keys = append(h.keys, key)
	if key == h.reject {
		return "", &runninghub.ProviderError{Code: "INSUFFICIENT_BALANCE", DisableKey: true}
	}
	h.active++
	h.submitted++
	if h.active > h.max {
		h.max = h.active
	}
	id := fmt.Sprint(h.submitted)
	if h.byTask == nil {
		h.byTask = map[string]string{}
	}
	h.byTask[id] = key
	return id, nil
}
func (h *fakeHub) Query(ctx context.Context, key, id string) (runninghub.Status, error) {
	select {
	case <-ctx.Done():
		return runninghub.Status{}, ctx.Err()
	case <-h.gate:
		h.mu.Lock()
		defer h.mu.Unlock()
		h.active--
		return runninghub.Status{State: "SUCCESS", Results: []runninghub.Result{{OutputType: "mp4", URL: "https://example.com/video"}}}, nil
	}
}
func (h *fakeHub) Download(ctx context.Context, url string, w io.Writer, limit int64) error {
	_, e := w.Write([]byte("video"))
	return e
}
func (h *fakeHub) count() int { h.mu.Lock(); defer h.mu.Unlock(); return h.submitted }
func TestRunningHubParallelPoolHotSettingsAndKeyFallback(t *testing.T) {
	h := &fakeHub{gate: make(chan struct{}), reject: "bad"}
	s, repo := fixture(t, nil)
	rt := s.runtime.(testRuntime).rt
	rt.RunningHubAPIKeys = "bad\ngood"
	rt.RunningHubPerKeyConcurrency = 1
	hot := &hotRuntime{rt: rt}
	s.runtime = hot
	s.opt.Hub = h
	p, e := s.Create(t.Context(), CreateRequest{Blank: true})
	if e != nil {
		t.Fatal(e)
	}
	p = addReady(t, s, p.ID)
	out, e := s.Generate(t.Context(), p.ID, GenerateRequest{Variants: 3})
	if e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(4 * time.Second)
	for h.count() < 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if h.count() != 1 {
		t.Fatal("no successful fallback")
	}
	hot.mu.Lock()
	hot.rt.RunningHubPerKeyConcurrency = 3
	hot.mu.Unlock()
	for h.count() < 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if h.count() != 3 {
		t.Fatalf("hot concurrency did not release queue: %d", h.count())
	}
	close(h.gate)
	for _, v := range out.Project.Shots[0].Takes {
		waitTake(t, repo, v.ID, "succeeded")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	bad := 0
	for _, k := range h.keys {
		if k == "bad" {
			bad++
		}
	}
	if bad != 1 || h.max != 3 {
		t.Fatalf("disabled key reuse or serial execution: bad=%d max=%d", bad, h.max)
	}
}
func TestPromptSuffixTruncationAndTimeout(t *testing.T) {
	p := store.ReplicateProject{StyleLock: string(make([]rune, 4100))}
	prompt, w, e := buildPrompt(p, store.ReplicateShot{MotionPrompt: "完整动作"})
	if e != nil || len([]rune(prompt)) != 4000 || w == "" {
		t.Fatalf("prompt %d %q %v", len([]rune(prompt)), w, e)
	}
	g := &resumeGrok{gate: make(chan struct{})}
	s, repo := fixture(t, g)
	s.opt.TakeTimeout = 30 * time.Millisecond
	pr, e := s.Create(t.Context(), CreateRequest{Blank: true, VideoProvider: "grok"})
	if e != nil {
		t.Fatal(e)
	}
	pr = addReady(t, s, pr.ID)
	out, e := s.Generate(t.Context(), pr.ID, GenerateRequest{})
	if e != nil {
		t.Fatal(e)
	}
	v := waitTake(t, repo, out.Project.Shots[0].Takes[0].ID, "failed")
	if v.Error != "排队或生成超过15分钟，请重新生成" {
		t.Fatal(v.Error)
	}
	if !errors.Is(translateProvider(context.Canceled), context.Canceled) {
		t.Fatal("cancellation lost")
	}
}

var _ = refanalysis.VideoInfo{}
