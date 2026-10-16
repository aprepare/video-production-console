package replicate

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"io"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
	"video-production-console/internal/domain"
	"video-production-console/internal/grokvideo"
	"video-production-console/internal/refanalysis"
	"video-production-console/internal/settings"
	"video-production-console/internal/store"
)

type testRuntime struct{ rt settings.Runtime }

func (r testRuntime) Runtime(context.Context) (settings.Runtime, error) { return r.rt, nil }

type blockedGrok struct {
	active, maximum, submitted atomic.Int32
	release                    chan struct{}
}

func (g *blockedGrok) Submit(ctx context.Context, in grokvideo.SubmitRequest) (grokvideo.Request, error) {
	n := g.active.Add(1)
	g.submitted.Add(1)
	for old := g.maximum.Load(); n > old; old = g.maximum.Load() {
		if g.maximum.CompareAndSwap(old, n) {
			break
		}
	}
	return grokvideo.Request{ID: "task"}, nil
}
func (g *blockedGrok) Poll(ctx context.Context, id string) (grokvideo.Status, error) {
	select {
	case <-g.release:
		g.active.Add(-1)
		return grokvideo.Status{State: grokvideo.StateDone, VideoURL: "https://example.com/video"}, nil
	case <-ctx.Done():
		return grokvideo.Status{}, ctx.Err()
	}
}
func (g *blockedGrok) Download(ctx context.Context, u string, w io.Writer) error {
	_, e := w.Write([]byte("video"))
	return e
}
func fixture(t *testing.T, g grokvideo.Client) (*Service, *store.ReplicateRepository) {
	t.Helper()
	root := t.TempDir()
	db, e := store.Open(filepath.Join(root, "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	repo := store.NewReplicateRepository(db)
	rt := testRuntime{settings.Runtime{PublicSettings: domain.PublicSettings{DataRoot: root}, GrokAPIKey: "fake"}}
	s, e := New(repo, store.NewReferenceAnalysisRepository(db), rt, Options{Grok: func(settings.Runtime) (grokvideo.Client, error) { return g, nil }, Probe: func(context.Context, string, string) (refanalysis.VideoInfo, error) {
		return refanalysis.VideoInfo{DurationS: 6, Width: 320, Height: 240}, nil
	}, PollInterval: time.Millisecond})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.Close)
	return s, repo
}
func ptr[T any](v T) *T { return &v }
func addReady(t *testing.T, s *Service, id string) Project {
	t.Helper()
	p, e := s.AddShot(t.Context(), id, ShotPatch{MotionPrompt: ptr("镜头缓慢推进"), FirstFramePrompt: ptr("茶杯"), DurationS: ptr(6)})
	if e != nil {
		t.Fatal(e)
	}
	var b bytes.Buffer
	png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 8, 8)))
	p, e = s.UploadFirstFrame(t.Context(), p.Shots[len(p.Shots)-1].ID, "first.png", &b)
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func TestParallelVariantsAndSkipped(t *testing.T) {
	g := &blockedGrok{release: make(chan struct{})}
	s, _ := fixture(t, g)
	p, e := s.Create(t.Context(), CreateRequest{Blank: true, Title: "并行", VideoProvider: "grok"})
	if e != nil {
		t.Fatal(e)
	}
	p = addReady(t, s, p.ID)
	p = addReady(t, s, p.ID)
	p, e = s.AddShot(t.Context(), p.ID, ShotPatch{})
	if e != nil {
		t.Fatal(e)
	}
	out, e := s.Generate(t.Context(), p.ID, GenerateRequest{Variants: 2})
	if e != nil {
		t.Fatal(e)
	}
	if len(out.Skipped) != 1 || out.Project.Progress.TakesTotal != 4 {
		t.Fatalf("%+v", out)
	}
	deadline := time.Now().Add(3 * time.Second)
	for g.maximum.Load() < 4 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if g.maximum.Load() != 4 {
		t.Fatalf("not parallel: %d", g.maximum.Load())
	}
	close(g.release)
	for time.Now().Before(deadline) {
		p, e = s.Get(t.Context(), p.ID)
		if e != nil {
			t.Fatal(e)
		}
		if p.Progress.TakesDone == 4 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if p.Progress.TakesDone != 4 || p.Shots[0].SelectedTakeID == "" {
		t.Fatalf("%+v", p)
	}
	out, e = s.Generate(t.Context(), p.ID, GenerateRequest{ShotIDs: []string{p.Shots[0].ID}, Variants: 1})
	if e != nil || out.Project.Progress.TakesTotal != 5 {
		t.Fatalf("regen: %+v %v", out, e)
	}
}
