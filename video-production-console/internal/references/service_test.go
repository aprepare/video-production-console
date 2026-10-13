package references

import (
	"context"
	"io"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"video-production-console/internal/platformapi"
	consoleSettings "video-production-console/internal/settings"
	"video-production-console/internal/store"
)

type fakeRuntime struct{ root string }

func (f fakeRuntime) Runtime(context.Context) (consoleSettings.Runtime, error) {
	runtime := consoleSettings.Runtime{PlatformAPIToken: "t"}
	runtime.PlatformAPIBaseURL = "http://gateway"
	runtime.DataRoot = f.root
	return runtime, nil
}

type fakePlatform struct {
	mu       sync.Mutex
	polls    int
	started  []string
	resolved platformapi.Post
	download func(context.Context, platformapi.Platform, string, io.Writer, int64) (int64, error)
}

func (f *fakePlatform) Status(context.Context) ([]platformapi.AppStatus, error) { return nil, nil }
func (f *fakePlatform) Search(context.Context, platformapi.Platform, string, string, string, string) (platformapi.SearchPage, error) {
	return platformapi.SearchPage{}, nil
}
func (f *fakePlatform) Resolve(context.Context, string) (platformapi.Post, error) {
	return f.resolved, nil
}
func (f *fakePlatform) Comments(context.Context, platformapi.Platform, string, int) ([]platformapi.Comment, error) {
	return nil, nil
}
func (f *fakePlatform) Download(ctx context.Context, platform platformapi.Platform, rawURL string, w io.Writer, limit int64) (int64, error) {
	if f.download != nil {
		return f.download(ctx, platform, rawURL, w, limit)
	}
	n, err := io.WriteString(w, "video")
	return int64(n), err
}
func (f *fakePlatform) StartTranscription(_ context.Context, videoURL string) (platformapi.Transcript, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = append(f.started, videoURL)
	return platformapi.Transcript{TaskID: "T1", Status: "ONGOING"}, nil
}
func (f *fakePlatform) Transcription(context.Context, string) (platformapi.Transcript, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.polls++
	if f.polls < 2 {
		return platformapi.Transcript{TaskID: "T1", Status: "ONGOING"}, nil
	}
	return platformapi.Transcript{TaskID: "T1", Status: "COMPLETED", Text: "俺点了三个菜。", Sentences: []platformapi.Sentence{{StartMS: 0, EndMS: 800, Text: "俺点了三个菜。"}}}, nil
}

func newService(t *testing.T, platform *fakePlatform) (*Service, *store.ReferenceRepository) {
	t.Helper()
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := store.NewReferenceRepository(db)
	svc := New(repo, fakeRuntime{root: root}).WithPlatform(func(string, string) (Platform, error) { return platform, nil }, 10*time.Millisecond)
	t.Cleanup(func() {
		svc.mu.Lock()
		jobs := make([]*mediaDownload, 0, len(svc.downloads))
		for _, job := range svc.downloads {
			job.cancel()
			jobs = append(jobs, job)
		}
		svc.mu.Unlock()
		for _, job := range jobs {
			select {
			case <-job.done:
			case <-time.After(3 * time.Second):
				t.Error("background download did not stop")
			}
		}
	})
	return svc, repo
}

func TestCollectSubmitsTranscriptionAndPollerCompletesIt(t *testing.T) {
	platform := &fakePlatform{resolved: platformapi.Post{
		Platform: platformapi.Douyin, ExternalID: "7694", Title: "岚山海鲜", Text: "#地道海鲜",
		VideoURL: "https://cdn/v.mp4", Likes: 10,
	}}
	svc, repo := newService(t, platform)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	post, err := svc.Collect(ctx, "https://v.douyin.com/x/", nil, "岚山海鲜", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if post.TranscriptStatus != "running" || len(platform.started) != 1 || platform.started[0] != "https://cdn/v.mp4" {
		t.Fatalf("post=%+v started=%v", post, platform.started)
	}
	if post.Caption != "#地道海鲜" || post.Transcript != "" {
		t.Fatalf("caption must not become transcript: %+v", post)
	}
	done := make(chan struct{})
	go func() { defer close(done); svc.Run(ctx) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got, _ := repo.Get(ctx, post.ID)
		if got.TranscriptStatus == "completed" {
			if got.Transcript != "俺点了三个菜。" || got.TranscriptJSON == "" {
				t.Fatalf("completed=%+v", got)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("poller did not complete the transcription")
}

func TestRecollectKeepsTranscriptAndDoesNotResubmit(t *testing.T) {
	platform := &fakePlatform{resolved: platformapi.Post{Platform: platformapi.Douyin, ExternalID: "1", VideoURL: "https://cdn/1.mp4", Likes: 1}}
	svc, repo := newService(t, platform)
	ctx := context.Background()
	first, err := svc.Collect(ctx, "https://v.douyin.com/a/", nil, "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CompleteTranscript(ctx, first.ID, "原口播", "[]"); err != nil {
		t.Fatal(err)
	}
	platform.resolved.Likes = 99
	again, err := svc.Collect(ctx, "https://v.douyin.com/a/", nil, "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != first.ID || again.Likes != 99 || again.Transcript != "原口播" || len(platform.started) != 0 {
		t.Fatalf("again=%+v started=%v", again, platform.started)
	}
}

func TestTranscribeWithoutVideoIsRejected(t *testing.T) {
	platform := &fakePlatform{resolved: platformapi.Post{Platform: platformapi.XHS, ExternalID: "abc"}}
	svc, _ := newService(t, platform)
	post, err := svc.Collect(context.Background(), "abc", nil, "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Transcribe(context.Background(), post.ID); err != ErrNoTranscribableVideo {
		t.Fatalf("err=%v", err)
	}
}
