package videoqc

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"video-production-console/internal/domain"
	"video-production-console/internal/refanalysis"
	"video-production-console/internal/settings"
	"video-production-console/internal/store"
)

type runtimeStub struct{ rt settings.Runtime }

func (r runtimeStub) Runtime(context.Context) (settings.Runtime, error) { return r.rt, nil }

type doer func(*http.Request) (*http.Response, error)

func (d doer) Do(r *http.Request) (*http.Response, error) { return d(r) }

func jpegFixture(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, 16, 10)), nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func serviceFixture(t *testing.T, customize func(*Options)) (*Service, *store.VideoQCRepository, *sql.DB, string) {
	t.Helper()
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	refs := store.NewReferenceRepository(db)
	if _, err = refs.Upsert(t.Context(), store.ReferencePost{ID: "ref", Platform: "douyin", ExternalID: "ref", Title: "海鲜"}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "references", "ref")
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "source.mp4"), []byte("cached"), 0600); err != nil {
		t.Fatal(err)
	}
	jpegData := jpegFixture(t)
	opt := Options{
		Inspect: func(ctx context.Context, ffmpeg, ffprobe, video string) (refanalysis.VideoInfo, []float64, error) {
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) > 8*time.Minute {
				t.Error("missing 8m task timeout")
			}
			return refanalysis.VideoInfo{DurationS: 3, Width: 320, Height: 240, FPS: 25}, []float64{1.5}, nil
		},
		ExtractFrame: func(context.Context, string, string, float64) ([]byte, error) { return jpegData, nil },
		HTTPClient: doer(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(qcEnvelope(t, goodReport)))}, nil
		}),
	}
	if customize != nil {
		customize(&opt)
	}
	rt := runtimeStub{settings.Runtime{PublicSettings: domain.PublicSettings{DataRoot: root, AnalysisBaseURL: "http://model.invalid/v1", AnalysisModel: "fake", FFmpegPath: "ffmpeg", FFprobePath: "ffprobe"}, AnalysisAPIKey: "test-secret"}}
	repo := store.NewVideoQCRepository(db)
	s := New(repo, refs, rt, opt)
	t.Cleanup(s.Close)
	return s, repo, db, root
}

func waitCheck(t *testing.T, repo *store.VideoQCRepository, id, status string) store.VideoQCCheck {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		a, err := repo.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if a.Status == status {
			return a
		}
		if a.Status == "failed" && status != "failed" {
			t.Fatalf("failed: %s", a.Error)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("check %s did not reach %s", id, status)
	return store.VideoQCCheck{}
}

func TestServiceReferenceLifecycleAndDetachedRequest(t *testing.T) {
	s, repo, _, root := serviceFixture(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	a, err := s.Start(ctx, Input{Source: "reference", SourceID: "ref", Product: "自有海鲜"})
	cancel()
	if err != nil || a.Status != "queued" || a.Report != nil || a.Frames == nil || len(a.Frames) != 0 {
		t.Fatalf("queued %+v %v", a, err)
	}
	waitCheck(t, repo, a.ID, "completed")
	detail, err := s.Get(t.Context(), a.ID)
	if err != nil || detail.Report == nil || len(detail.Frames) != 5 || detail.FrameCount != 5 || detail.Title != "海鲜" || detail.Product != "自有海鲜" || detail.Verdict != "pass" {
		t.Fatalf("detail %+v %v", detail, err)
	}
	f, err := s.OpenFrame(t.Context(), a.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err = s.OpenFrame(t.Context(), a.ID, 6); !errors.Is(err, ErrMediaNotFound) {
		t.Fatalf("frame bounds %v", err)
	}
	if _, err = s.OpenVideo(t.Context(), a.ID); !errors.Is(err, ErrMediaNotFound) {
		t.Fatalf("reference playback %v", err)
	}
	if err = s.Delete(t.Context(), a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(root, "video-qc", a.ID)); !os.IsNotExist(err) {
		t.Fatalf("directory remains %v", err)
	}
	if _, err = repo.Get(t.Context(), a.ID); !errors.Is(err, store.ErrVideoQCNotFound) {
		t.Fatal(err)
	}
}

func TestServiceConcurrencyCloseAndDelete(t *testing.T) {
	entered := make(chan string, 8)
	var active, maxActive atomic.Int32
	s, repo, _, root := serviceFixture(t, func(opt *Options) {
		opt.Inspect = func(ctx context.Context, _, _, path string) (refanalysis.VideoInfo, []float64, error) {
			n := active.Add(1)
			defer active.Add(-1)
			for old := maxActive.Load(); n > old; old = maxActive.Load() {
				if maxActive.CompareAndSwap(old, n) {
					break
				}
			}
			entered <- path
			<-ctx.Done()
			return refanalysis.VideoInfo{}, nil, ctx.Err()
		}
	})
	ids := []string{}
	for i := 0; i < 5; i++ {
		a, err := s.Start(t.Context(), Input{Source: "reference", SourceID: "ref"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, a.ID)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("worker never started")
		}
	}
	select {
	case <-entered:
		t.Fatal("more than two workers")
	case <-time.After(50 * time.Millisecond):
	}
	queuedID := ""
	for _, id := range ids {
		a, _ := repo.Get(t.Context(), id)
		if a.Status == "queued" {
			queuedID = id
			break
		}
	}
	if queuedID == "" {
		t.Fatal("no queued work")
	}
	if err := s.Delete(t.Context(), queuedID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "video-qc", queuedID)); !os.IsNotExist(err) {
		t.Fatal("deleted queued files remain")
	}
	s.Close()
	if active.Load() != 0 || maxActive.Load() != 2 {
		t.Fatalf("workers active=%d max=%d", active.Load(), maxActive.Load())
	}
	for _, id := range ids {
		if id != queuedID {
			waitCheck(t, repo, id, "failed")
		}
	}
	if _, err := s.Start(t.Context(), Input{Source: "reference", SourceID: "ref"}); err == nil {
		t.Fatal("start after close")
	}
}

func TestServiceSourcesValidationAndRecovery(t *testing.T) {
	s, repo, _, root := serviceFixture(t, nil)
	for _, in := range []Input{{Source: "upload"}, {Source: "reference", SourceID: "../ref"}, {Source: "reference", SourceID: "ref", Product: strings.Repeat("中", 301)}, {Source: "reference", SourceID: "ref", Product: string([]byte{0xff})}} {
		if _, err := s.Start(t.Context(), in); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("input %+v: %v", in, err)
		}
	}
	if _, err := s.Start(t.Context(), Input{Source: "image_video_job", SourceID: "job"}); !errors.Is(err, ErrUnsupportedSource) {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "references", "ref", "source.mp4")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Start(t.Context(), Input{Source: "reference", SourceID: "ref"}); !errors.Is(err, ErrMediaNotReady) {
		t.Fatal(err)
	}
	for _, id := range []string{"stale-queued", "stale-running"} {
		if _, err := repo.CreateQueued(t.Context(), store.VideoQCCheck{ID: id, Source: "upload"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.MarkRunning(t.Context(), "stale-running"); err != nil {
		t.Fatal(err)
	}
	if err := s.Resume(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"stale-queued", "stale-running"} {
		a, _ := repo.Get(t.Context(), id)
		if a.Status != "failed" || a.Error != "控制台重启中断" {
			t.Fatalf("%+v", a)
		}
	}
}

func TestServiceRejectsEscapingPaths(t *testing.T) {
	s, repo, _, root := serviceFixture(t, nil)
	for _, id := range []string{"../escape", "/absolute", `..\escape`, "C:escape", "a/b"} {
		if _, err := s.Get(t.Context(), id); err == nil {
			t.Fatalf("unsafe get %q", id)
		}
		if err := s.Delete(t.Context(), id); err == nil {
			t.Fatalf("unsafe delete %q", id)
		}
	}
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "frame_01.jpg")
	if err := os.WriteFile(sentinel, []byte("protected"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "video-qc"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "video-qc", "escape")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := repo.CreateQueued(t.Context(), store.VideoQCCheck{ID: "escape", Source: "upload"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkRunning(t.Context(), "escape"); err != nil {
		t.Fatal(err)
	}
	if err := repo.Complete(t.Context(), "escape", store.VideoQCCheck{FrameCount: 1}); err != nil {
		t.Fatal(err)
	}
	if f, err := s.OpenFrame(t.Context(), "escape", 1); err == nil {
		f.Close()
		t.Fatal("escaped frame root")
	}
	_ = s.Delete(t.Context(), "escape")
	data, err := os.ReadFile(sentinel)
	if err != nil || string(data) != "protected" {
		t.Fatalf("outside file changed %v", err)
	}
}
