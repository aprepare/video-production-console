package references

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"video-production-console/internal/platformapi"
	"video-production-console/internal/store"
)

func seedMediaPost(t *testing.T, repo *store.ReferenceRepository) store.ReferencePost {
	t.Helper()
	post, err := repo.Upsert(context.Background(), store.ReferencePost{
		ID: "media-one", Platform: "douyin", ExternalID: "1",
		VideoURL: "https://cdn/source.mp4?signature=private", CoverURL: "/api/img?u=cover",
	})
	if err != nil {
		t.Fatal(err)
	}
	return post
}

func TestCacheMediaPublishesVideoAndCover(t *testing.T) {
	var svc *Service
	var calls int
	platform := &fakePlatform{download: func(ctx context.Context, platform platformapi.Platform, rawURL string, w io.Writer, limit int64) (int64, error) {
		calls++
		if platform != platformapi.Douyin {
			t.Errorf("platform=%s", platform)
		}
		post, err := svc.repo.Get(ctx, "media-one")
		wantStatus := "ready"
		if strings.Contains(rawURL, "source.mp4") {
			wantStatus = "downloading"
		}
		if err != nil || post.MediaStatus != wantStatus {
			t.Errorf("status while downloading=%s err=%v", post.MediaStatus, err)
		}
		body := "cover-bytes"
		if strings.Contains(rawURL, "source.mp4") {
			body = "source-bytes"
			if limit != 1<<30 {
				t.Errorf("video limit=%d", limit)
			}
			runtime, _ := svc.runtime.Runtime(ctx)
			if _, err := os.Stat(filepath.Join(runtime.DataRoot, "references", "media-one", "source.mp4.part")); err != nil {
				t.Errorf("missing part file: %v", err)
			}
			if _, err := os.Stat(filepath.Join(runtime.DataRoot, "references", "media-one", "source.mp4")); !os.IsNotExist(err) {
				t.Error("source published before download completed")
			}
		} else if rawURL != "/api/img?u=cover" {
			t.Errorf("cover URL=%q", rawURL)
		}
		n, err := io.WriteString(w, body)
		return int64(n), err
	}}
	svc, _ = newService(t, platform)
	post := seedMediaPost(t, svc.repo)
	got, err := svc.CacheMedia(context.Background(), post.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.MediaStatus != "ready" || got.MediaSize != 12 || got.MediaError != "" || calls != 2 {
		t.Fatalf("cached=%+v calls=%d", got, calls)
	}
	for path, want := range map[string]string{got.MediaPath: "source-bytes", got.CoverPath: "cover-bytes"} {
		if !filepath.IsAbs(path) {
			t.Errorf("path must be absolute: %s", path)
		}
		body, err := os.ReadFile(path)
		if err != nil || string(body) != want {
			t.Errorf("cached body=%q err=%v", body, err)
		}
		if _, err := os.Stat(path + ".part"); !os.IsNotExist(err) {
			t.Errorf("part file remains: %v", err)
		}
	}
}

func TestCacheMediaFailureIsSafeAndRemovesPartialFile(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"network", errors.New("https://cdn/source?signature=private token=secret"), "原视频下载失败"},
		{"forbidden", &platformapi.DownloadHTTPError{StatusCode: 403}, "视频地址已过期，请重新采集"},
		{"missing", &platformapi.DownloadHTTPError{StatusCode: 404}, "视频地址已过期，请重新采集"},
		{"gone", &platformapi.DownloadHTTPError{StatusCode: 410}, "视频地址已过期，请重新采集"},
		{"upstream", &platformapi.DownloadHTTPError{StatusCode: 500}, "HTTP 500"},
		{"large", platformapi.ErrMediaTooLarge, "1 GiB"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			platform := &fakePlatform{download: func(_ context.Context, _ platformapi.Platform, _ string, w io.Writer, _ int64) (int64, error) {
				_, _ = io.WriteString(w, "partial")
				return 7, tc.err
			}}
			svc, repo := newService(t, platform)
			post := seedMediaPost(t, repo)
			_, err := svc.CacheMedia(context.Background(), post.ID)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v want %q", err, tc.want)
			}
			got, err := repo.Get(context.Background(), post.ID)
			if err != nil || got.MediaStatus != "failed" || !strings.Contains(got.MediaError, tc.want) || got.MediaPath != "" || got.MediaSize != 0 {
				t.Fatalf("failed=%+v err=%v", got, err)
			}
			if strings.Contains(got.MediaError, "private") || strings.Contains(got.MediaError, "secret") || strings.Contains(got.MediaError, "https:") {
				t.Fatalf("unsafe error=%q", got.MediaError)
			}
			runtime, _ := svc.runtime.Runtime(context.Background())
			for _, name := range []string{"source.mp4", "source.mp4.part"} {
				if _, err := os.Stat(filepath.Join(runtime.DataRoot, "references", post.ID, name)); !os.IsNotExist(err) {
					t.Errorf("failed download left %s: %v", name, err)
				}
			}
		})
	}
}

func TestCacheMediaCoverFailureDoesNotFailVideo(t *testing.T) {
	platform := &fakePlatform{download: func(_ context.Context, _ platformapi.Platform, rawURL string, w io.Writer, _ int64) (int64, error) {
		if strings.HasPrefix(rawURL, "/api/img") {
			return 0, errors.New("cover unavailable")
		}
		n, err := io.WriteString(w, "video")
		return int64(n), err
	}}
	svc, repo := newService(t, platform)
	post := seedMediaPost(t, repo)
	got, err := svc.CacheMedia(context.Background(), post.ID)
	if err != nil || got.MediaStatus != "ready" || got.CoverPath != "" {
		t.Fatalf("cached=%+v err=%v", got, err)
	}
}

func TestCacheMediaCoverCancellationKeepsReadyVideo(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	platform := &fakePlatform{download: func(_ context.Context, _ platformapi.Platform, rawURL string, w io.Writer, _ int64) (int64, error) {
		if strings.HasPrefix(rawURL, "/api/img") {
			cancel()
			return 0, ctx.Err()
		}
		n, err := io.WriteString(w, "video")
		return int64(n), err
	}}
	svc, repo := newService(t, platform)
	post := seedMediaPost(t, repo)
	got, err := svc.CacheMedia(ctx, post.ID)
	if err != nil || got.MediaStatus != "ready" || got.MediaSize != 5 {
		t.Fatalf("cover cancellation failed the video: status=%s size=%d err=%v", got.MediaStatus, got.MediaSize, err)
	}
}

func TestCacheMediaRejectsReentryAndRecordsCancellation(t *testing.T) {
	started := make(chan struct{})
	var calls atomic.Int32
	platform := &fakePlatform{download: func(ctx context.Context, _ platformapi.Platform, _ string, _ io.Writer, _ int64) (int64, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-ctx.Done()
		return 0, ctx.Err()
	}}
	svc, repo := newService(t, platform)
	post := seedMediaPost(t, repo)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := svc.CacheMedia(ctx, post.ID); done <- err }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("download did not start")
	}
	got, err := svc.CacheMedia(context.Background(), post.ID)
	if err != nil || got.MediaStatus != "downloading" || calls.Load() != 1 {
		t.Fatalf("reentry=%+v err=%v calls=%d", got, err, calls.Load())
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancellation succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("download did not stop")
	}
	got, err = repo.Get(context.Background(), post.ID)
	if err != nil || got.MediaStatus != "failed" {
		t.Fatalf("canceled status=%s err=%v", got.MediaStatus, err)
	}
}

func TestCollectCachesWithIndependentTenMinuteContext(t *testing.T) {
	started, release := make(chan context.Context, 1), make(chan struct{})
	platform := &fakePlatform{resolved: platformapi.Post{Platform: platformapi.Douyin, ExternalID: "auto", VideoURL: "https://cdn/video"}, download: func(ctx context.Context, _ platformapi.Platform, _ string, w io.Writer, _ int64) (int64, error) {
		started <- ctx
		<-release
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		n, err := io.WriteString(w, "video")
		return int64(n), err
	}}
	svc, repo := newService(t, platform)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	post, err := svc.Collect(ctx, "link", nil, "", "", false)
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	cancel()
	select {
	case workCtx := <-started:
		deadline, ok := workCtx.Deadline()
		if workCtx.Err() != nil || !ok || time.Until(deadline) < 9*time.Minute || time.Until(deadline) > 10*time.Minute {
			t.Errorf("background context err=%v deadline=%v", workCtx.Err(), deadline)
		}
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("automatic download did not start")
	}
	close(release)
	waitMediaStatus(t, repo, post.ID, "ready")
}

func TestRunFailsInterruptedMediaAndKeepsReady(t *testing.T) {
	svc, repo := newService(t, &fakePlatform{})
	for i, status := range []string{"downloading", "ready", "none"} {
		_, err := repo.Upsert(context.Background(), store.ReferencePost{ID: status, Platform: "douyin", ExternalID: fmt.Sprint(i), MediaStatus: status})
		if err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); svc.Run(ctx) }()
	defer func() { cancel(); <-done }()
	post := waitMediaStatus(t, repo, "downloading", "failed")
	if post.MediaError != "控制台重启，下载中断，请点重新下载" {
		t.Fatalf("error=%q", post.MediaError)
	}
	for _, status := range []string{"ready", "none"} {
		post, err := repo.Get(context.Background(), status)
		if err != nil || post.MediaStatus != status {
			t.Fatalf("status=%s err=%v", post.MediaStatus, err)
		}
	}
}

func TestCacheMediaNoVideoAndCachedTranscription(t *testing.T) {
	platform := &fakePlatform{}
	svc, repo := newService(t, platform)
	post, err := repo.Upsert(context.Background(), store.ReferencePost{ID: "xhs", Platform: "xhs", ExternalID: "xhs"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CacheMedia(context.Background(), post.ID); !errors.Is(err, ErrNoReferenceVideo) {
		t.Fatalf("no video error=%v", err)
	}
	post = seedMediaPost(t, repo)
	if _, err := svc.CacheMedia(context.Background(), post.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Transcribe(context.Background(), post.ID); err != nil {
		t.Fatal(err)
	}
	if len(platform.started) != 1 || platform.started[0] != post.VideoURL {
		t.Fatalf("transcription URLs=%v", platform.started)
	}
}

func TestCacheMediaRetriesFailureAndRecollectKeepsReadyFile(t *testing.T) {
	var calls atomic.Int32
	platform := &fakePlatform{resolved: platformapi.Post{Platform: platformapi.Douyin, ExternalID: "1", VideoURL: "https://cdn/new"}, download: func(_ context.Context, _ platformapi.Platform, rawURL string, w io.Writer, _ int64) (int64, error) {
		if calls.Add(1) == 1 {
			return 0, errors.New("temporary failure")
		}
		n, err := io.WriteString(w, "video")
		return int64(n), err
	}}
	svc, repo := newService(t, platform)
	post := seedMediaPost(t, repo)
	if _, err := svc.CacheMedia(context.Background(), post.ID); err == nil {
		t.Fatal("first download succeeded")
	}
	got, err := svc.CacheMedia(context.Background(), post.ID)
	if err != nil || got.MediaStatus != "ready" || got.MediaError != "" {
		t.Fatalf("retry=%+v err=%v", got, err)
	}
	count := calls.Load()
	again, err := svc.Collect(context.Background(), "link", nil, "", "", false)
	if err != nil || again.MediaPath != got.MediaPath || again.MediaStatus != "ready" || calls.Load() != count {
		t.Fatalf("recollect=%+v err=%v calls=%d", again, err, calls.Load())
	}
}

func TestCacheMediaReplacesReadySource(t *testing.T) {
	var calls int
	platform := &fakePlatform{download: func(_ context.Context, _ platformapi.Platform, _ string, w io.Writer, _ int64) (int64, error) {
		calls++
		body := "old-video"
		if calls == 2 {
			body = "new-video"
		}
		n, err := io.WriteString(w, body)
		return int64(n), err
	}}
	svc, repo := newService(t, platform)
	post, err := repo.Upsert(context.Background(), store.ReferencePost{ID: "refresh", Platform: "channels", ExternalID: "1", VideoURL: "https://cdn/video"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		post, err = svc.CacheMedia(context.Background(), post.ID)
		if err != nil || post.MediaStatus != "ready" {
			t.Fatalf("refresh status=%s err=%v", post.MediaStatus, err)
		}
	}
	body, err := os.ReadFile(post.MediaPath)
	if err != nil || string(body) != "new-video" || calls != 2 {
		t.Fatalf("body=%q calls=%d err=%v", body, calls, err)
	}
}

func TestRunDoesNotFailCurrentDownload(t *testing.T) {
	started := make(chan struct{})
	platform := &fakePlatform{download: func(ctx context.Context, _ platformapi.Platform, _ string, _ io.Writer, _ int64) (int64, error) {
		close(started)
		<-ctx.Done()
		return 0, ctx.Err()
	}}
	svc, repo := newService(t, platform)
	post := seedMediaPost(t, repo)
	if _, err := svc.StartMediaCache(context.Background(), post.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("download did not start")
	}
	_, err := repo.Upsert(context.Background(), store.ReferencePost{ID: "stale", Platform: "douyin", ExternalID: "stale", MediaStatus: "downloading"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); svc.Run(ctx) }()
	defer func() { cancel(); <-done }()
	waitMediaStatus(t, repo, "stale", "failed")
	got, err := repo.Get(context.Background(), post.ID)
	if err != nil || got.MediaStatus != "downloading" {
		t.Fatalf("current download status=%s err=%v", got.MediaStatus, err)
	}
}

func waitMediaStatus(t *testing.T, repo *store.ReferenceRepository, id, status string) store.ReferencePost {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		post, err := repo.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if post.MediaStatus == status {
			return post
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("media %s did not become %s", id, status)
	return store.ReferencePost{}
}

// 视频号等平台没有可直接下载的封面时，从缓存的原片截一帧当封面。
func TestCacheMediaFallsBackToSourceFrameForCover(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	sample := filepath.Join(t.TempDir(), "sample.mp4")
	if out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=size=320x240:rate=10:duration=1", "-pix_fmt", "yuv420p", sample).CombinedOutput(); err != nil {
		t.Fatalf("make sample: %v %s", err, out)
	}
	video, err := os.ReadFile(sample)
	if err != nil {
		t.Fatal(err)
	}
	platform := &fakePlatform{download: func(_ context.Context, _ platformapi.Platform, _ string, w io.Writer, _ int64) (int64, error) {
		n, err := w.Write(video)
		return int64(n), err
	}}
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := store.NewReferenceRepository(db)
	svc := New(repo, fakeRuntime{root: root, ffmpeg: ffmpeg}).WithPlatform(func(string, string) (Platform, error) { return platform, nil }, time.Hour)
	post, err := repo.Upsert(context.Background(), store.ReferencePost{ID: "no-cover", Platform: "channels", ExternalID: "c1", VideoURL: "https://finder/video"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.CacheMedia(context.Background(), post.ID)
	if err != nil || got.MediaStatus != "ready" || got.CoverPath == "" {
		t.Fatalf("cached=%+v err=%v", got, err)
	}
	info, err := os.Stat(got.CoverPath)
	if err != nil || info.Size() == 0 {
		t.Fatalf("cover frame missing: %v", err)
	}
}
