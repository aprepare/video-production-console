package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"video-production-console/internal/platformapi"
	"video-production-console/internal/references"
	consoleSettings "video-production-console/internal/settings"
	"video-production-console/internal/store"
)

type referenceRuntimeStub struct{ root string }

func (r referenceRuntimeStub) Runtime(context.Context) (consoleSettings.Runtime, error) {
	runtime := consoleSettings.Runtime{}
	runtime.DataRoot = r.root
	return runtime, nil
}

type referencePlatformStub struct {
	references.Platform
	download func(context.Context, io.Writer) (int64, error)
}

func (p referencePlatformStub) Download(ctx context.Context, _ platformapi.Platform, _ string, w io.Writer, _ int64) (int64, error) {
	return p.download(ctx, w)
}

func newReferenceHandler(t *testing.T, platform references.Platform) (http.Handler, *store.ReferenceRepository, string) {
	t.Helper()
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := store.NewReferenceRepository(db)
	svc := references.New(repo, referenceRuntimeStub{root})
	if platform != nil {
		svc.WithPlatform(func(string, string) (references.Platform, error) { return platform, nil }, time.Second)
	}
	return NewReferencesHandler(svc, repo), repo, root
}

func putReferenceMedia(t *testing.T, repo *store.ReferenceRepository, root, title string) store.ReferencePost {
	t.Helper()
	dir := filepath.Join(root, "references", "media-one")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	video, cover := filepath.Join(dir, "source.mp4"), filepath.Join(dir, "cover.jpg")
	if err := os.WriteFile(video, []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cover, []byte{0xff, 0xd8, 0xff, 0xe0, 0x01}, 0o644); err != nil {
		t.Fatal(err)
	}
	post, err := repo.Upsert(context.Background(), store.ReferencePost{
		ID: "media-one", Platform: "douyin", ExternalID: "123", Title: title,
		MediaStatus: "ready", MediaPath: video, MediaSize: 10, CoverPath: cover,
	})
	if err != nil {
		t.Fatal(err)
	}
	return post
}

func TestReferenceMediaServesContentRangesAndCover(t *testing.T) {
	h, repo, root := newReferenceHandler(t, nil)
	post := putReferenceMedia(t, repo, root, "中文标题")
	for _, tc := range []struct {
		name, suffix, method, byteRange string
		status                          int
		body, contentType               string
	}{
		{"video", "/media", "GET", "", 200, "0123456789", "video/mp4"},
		{"range", "/media", "GET", "bytes=2-5", 206, "2345", "video/mp4"},
		{"suffix", "/media", "GET", "bytes=-3", 206, "789", "video/mp4"},
		{"head", "/media", "HEAD", "", 200, "", "video/mp4"},
		{"cover", "/cover", "GET", "", 200, string([]byte{0xff, 0xd8, 0xff, 0xe0, 0x01}), "image/jpeg"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "/api/references/"+post.ID+tc.suffix, nil)
			if tc.byteRange != "" {
				r.Header.Set("Range", tc.byteRange)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status || w.Body.String() != tc.body || w.Header().Get("Content-Type") != tc.contentType {
				t.Fatalf("status=%d body=%q headers=%v", w.Code, w.Body.String(), w.Header())
			}
			if tc.name == "range" && w.Header().Get("Content-Range") != "bytes 2-5/10" {
				t.Fatalf("range=%q", w.Header().Get("Content-Range"))
			}
		})
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/references/"+post.ID, nil))
	if !strings.Contains(w.Body.String(), `"has_video":true`) || !strings.Contains(w.Body.String(), `"has_cover":true`) || strings.Contains(w.Body.String(), "media_path") || strings.Contains(w.Body.String(), "cover_path") {
		t.Fatalf("JSON=%s", w.Body.String())
	}
}

func TestReferenceMediaDownloadFilename(t *testing.T) {
	for _, tc := range []struct{ name, title, want string }{
		{"Chinese", "中文 空格/\\:*?\"<>|\r\n标题", "中文 空格标题.mp4"},
		{"punctuation", "标题=分集+内容@作者&结束", "标题=分集+内容@作者&结束.mp4"},
		{"truncate", strings.Repeat("长", 35), strings.Repeat("长", 30) + ".mp4"},
		{"empty", "", "douyin-123.mp4"},
		{"illegal", "/\\:*?\"<>|", "douyin-123.mp4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, repo, root := newReferenceHandler(t, nil)
			post := putReferenceMedia(t, repo, root, tc.title)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", "/api/references/"+post.ID+"/media?download=1", nil))
			header := w.Header().Get("Content-Disposition")
			kind, params, err := mime.ParseMediaType(header)
			if w.Code != 200 || err != nil || kind != "attachment" || params["filename"] != tc.want || !strings.Contains(header, "filename*=UTF-8''") {
				t.Fatalf("status=%d disposition=%q params=%v err=%v", w.Code, header, params, err)
			}
		})
	}
}

func TestReferenceMediaNotReadyAndMissingCover(t *testing.T) {
	h, repo, _ := newReferenceHandler(t, nil)
	post, err := repo.Upsert(context.Background(), store.ReferencePost{ID: "empty", Platform: "xhs", ExternalID: "1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"/media", "/cover"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/api/references/"+post.ID+suffix, nil))
		if w.Code != 404 {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		if suffix == "/media" && (!strings.Contains(w.Body.String(), `"code":"reference_media_not_ready"`) || !strings.Contains(w.Body.String(), `"message":"原视频还没下载好。"`)) {
			t.Fatalf("body=%s", w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/api/references/"+post.ID+"/media", nil))
	if w.Code != 409 || !strings.Contains(w.Body.String(), `"code":"no_reference_video"`) || !strings.Contains(w.Body.String(), `"message":"这条内容没有视频。"`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestReferenceMediaRejectsPathsOutsideCache(t *testing.T) {
	for _, kind := range []string{"absolute", "parent", "prefix", "relative", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			h, repo, root := newReferenceHandler(t, nil)
			outside := filepath.Join(root, "outside.mp4")
			if err := os.WriteFile(outside, []byte("private"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(root, "references"), 0o755); err != nil {
				t.Fatal(err)
			}
			path := outside
			switch kind {
			case "parent":
				path = filepath.Join(root, "references") + string(filepath.Separator) + ".." + string(filepath.Separator) + "outside.mp4"
			case "relative":
				path = "../outside.mp4"
			case "prefix":
				path = filepath.Join(root, "references-other", "outside.mp4")
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("private"), 0o644); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				path = filepath.Join(root, "references", "linked.mp4")
				if err := os.Symlink(outside, path); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			}
			post, err := repo.Upsert(context.Background(), store.ReferencePost{ID: "unsafe", Platform: "douyin", ExternalID: "1", MediaStatus: "ready", MediaPath: path, CoverPath: path})
			if err != nil {
				t.Fatal(err)
			}
			for _, suffix := range []string{"/media", "/cover"} {
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest("GET", "/api/references/"+post.ID+suffix, nil))
				if w.Code != 404 || strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), root) {
					t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
				}
			}
		})
	}
}

func TestReferenceDeleteRemovesOnlyItsCache(t *testing.T) {
	for _, files := range []bool{true, false} {
		name := "missing"
		if files {
			name = "cached"
		}
		t.Run(name, func(t *testing.T) {
			h, repo, root := newReferenceHandler(t, nil)
			post := putReferenceMedia(t, repo, root, "delete")
			dir := filepath.Join(root, "references", post.ID)
			if !files {
				if err := os.RemoveAll(dir); err != nil {
					t.Fatal(err)
				}
			}
			other := filepath.Join(root, "references", "keep.mp4")
			if err := os.WriteFile(other, []byte("keep"), 0o644); err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("DELETE", "/api/references/"+post.ID, nil))
			if w.Code != 204 {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatalf("directory remains: %v", err)
			}
			if _, err := repo.Get(context.Background(), post.ID); err != store.ErrReferenceNotFound {
				t.Fatalf("reference remains: %v", err)
			}
			if body, err := os.ReadFile(other); err != nil || string(body) != "keep" {
				t.Fatalf("other cache affected: %v", err)
			}
		})
	}
}

func TestReferenceMediaPostReturnsImmediatelyAndDeleteCancelsIt(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	platform := referencePlatformStub{download: func(ctx context.Context, w io.Writer) (int64, error) {
		close(started)
		_, _ = io.WriteString(w, "partial")
		<-ctx.Done()
		close(stopped)
		return 7, ctx.Err()
	}}
	h, repo, root := newReferenceHandler(t, platform)
	post, err := repo.Upsert(context.Background(), store.ReferencePost{ID: "active", Platform: "channels", ExternalID: "1", VideoURL: "https://cdn/video"})
	if err != nil {
		t.Fatal(err)
	}
	response := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/api/references/"+post.ID+"/media", nil))
		response <- w
	}()
	select {
	case w := <-response:
		var got store.ReferencePost
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if w.Code != 202 || got.ID != post.ID || got.MediaStatus != "downloading" {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("POST waited for the download")
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("download did not start")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/api/references/"+post.ID+"/media", nil))
	if w.Code != 202 {
		t.Fatalf("duplicate POST status=%d", w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("DELETE", "/api/references/"+post.ID, nil))
	if w.Code != 204 {
		t.Fatalf("DELETE status=%d body=%s", w.Code, w.Body.String())
	}
	select {
	case <-stopped:
	default:
		t.Fatal("download still running")
	}
	if _, err := os.Stat(filepath.Join(root, "references", post.ID)); !os.IsNotExist(err) {
		t.Fatalf("cache remains: %v", err)
	}
}
