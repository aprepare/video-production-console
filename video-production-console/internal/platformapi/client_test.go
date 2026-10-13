package platformapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNumParsesGatewayCounts(t *testing.T) {
	cases := map[any]int64{
		"231108": 231108, "1.2万": 12000, "3w": 30000, "10万+": 100000,
		"2亿": 200000000, float64(42): 42, "": 0, nil: 0, "abc": 0, "1,234": 1234,
	}
	for in, want := range cases {
		if got := num(in); got != want {
			t.Errorf("num(%#v)=%d want %d", in, got, want)
		}
	}
}

func TestExtractLinkAndDetectPlatform(t *testing.T) {
	cases := []struct {
		in       string
		link     string
		platform Platform
	}{
		{"7.94 复制打开抖音，看看【海吃王】 https://v.douyin.com/abcDEF/ 在日照", "https://v.douyin.com/abcDEF/", Douyin},
		{"https://weixin.qq.com/sph/AbCd12", "https://weixin.qq.com/sph/AbCd12", Channels},
		{"看看这篇 http://xhslink.com/a/xyz，挺好", "http://xhslink.com/a/xyz", XHS},
		{"6a79c9a0000000020600ddd4", "6a79c9a0000000020600ddd4", XHS},
		{"随便一段字", "随便一段字", ""},
	}
	for _, tc := range cases {
		link := ExtractLink(tc.in)
		if link != tc.link {
			t.Errorf("ExtractLink(%q)=%q want %q", tc.in, link, tc.link)
		}
		if got := DetectPlatform(link); got != tc.platform {
			t.Errorf("DetectPlatform(%q)=%q want %q", link, got, tc.platform)
		}
	}
	if id := xhsNoteID("https://www.xiaohongshu.com/explore/6A79C9A0000000020600DDD4?xsec=1"); id != "6a79c9a0000000020600ddd4" {
		t.Fatalf("xhsNoteID=%q", id)
	}
}

func TestSearchDouyinNormalizesStringCountsAndSendsBearer(t *testing.T) {
	var auth, keyword string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		keyword = r.URL.Query().Get("keyword")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true, "has_more": true, "cursor": 8, "search_id": "s1",
			"items": []map[string]any{{
				"aweme_id": "7618399307196009764", "desc": "在日照岚山怼一顿海鲜 #日照美食",
				"duration": "260970", "digg_count": "231108", "comment_count": "6644",
				"play_url": "https://cdn.example/v.mp4", "nickname": "海吃王",
			}},
		})
	}))
	defer srv.Close()
	client, err := New(srv.URL, "tok-123", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	page, err := client.Search(context.Background(), Douyin, "岚山海鲜", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer tok-123" || keyword != "岚山海鲜" {
		t.Fatalf("auth=%q keyword=%q", auth, keyword)
	}
	if !page.HasMore || page.Cursor != "8" || page.SearchID != "s1" || len(page.Items) != 1 {
		t.Fatalf("page=%+v", page)
	}
	post := page.Items[0]
	if post.Likes != 231108 || post.Comments != 6644 || post.DurationS < 260 || post.DurationS > 262 {
		t.Fatalf("post counts=%+v", post)
	}
	if post.Title != "在日照岚山怼一顿海鲜" || post.URL != "https://www.douyin.com/video/7618399307196009764" {
		t.Fatalf("post title/url=%q %q", post.Title, post.URL)
	}
}

func TestUpstreamFailureAndUnauthorizedAreReadable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "xhs") {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "http": 406, "msg": "expired"})
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	client, _ := New(srv.URL, "secret-token", srv.Client())
	if _, err := client.Search(context.Background(), XHS, "茶", "", "", ""); err == nil || !strings.Contains(err.Error(), "登录态过期") {
		t.Fatalf("xhs err=%v", err)
	}
	if _, err := client.Status(context.Background()); err == nil || !strings.Contains(err.Error(), "Token 无效") {
		t.Fatalf("status err=%v", err)
	}
}

func TestDownloadProxiesGatewayPathsWithToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer t" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = io.WriteString(w, "image-bytes")
	}))
	defer srv.Close()
	client, _ := New(srv.URL, "t", srv.Client())
	var buf bytes.Buffer
	if _, err := client.Download(context.Background(), XHS, "/api/img?u=x", &buf, 1024); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "image-bytes" {
		t.Fatalf("body=%q", buf.String())
	}
	if _, err := client.Download(context.Background(), XHS, "/api/img?u=x", &bytes.Buffer{}, 3); err == nil {
		t.Fatal("size limit not enforced")
	}
}

func TestDownloadReturnsHTTPStatusWithoutSignedURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	client, _ := New(srv.URL, "private-token", srv.Client())
	_, err := client.Download(context.Background(), Douyin, srv.URL+"/video?signature=private-signature", io.Discard, 1024)
	var status *DownloadHTTPError
	if !errors.As(err, &status) || status.StatusCode != 403 {
		t.Fatalf("HTTP error=%v", err)
	}
	if strings.Contains(err.Error(), "private-") || strings.Contains(err.Error(), srv.URL) {
		t.Fatalf("unsafe error=%v", err)
	}
}

func TestDownloadUsesPlatformUAAndRedactsNetworkErrors(t *testing.T) {
	for _, tc := range []struct {
		platform Platform
		ua       string
	}{{Douyin, "com.ss.android.ugc.aweme"}, {Channels, "MicroMessenger"}} {
		t.Run(string(tc.platform), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.Contains(r.UserAgent(), tc.ua) {
					t.Error("platform UA missing")
				}
				if r.Header.Get("Authorization") != "" {
					t.Error("gateway token sent to CDN")
				}
				_, _ = io.WriteString(w, "video")
			}))
			defer srv.Close()
			client, _ := New(srv.URL, "private-token", srv.Client())
			if _, err := client.Download(context.Background(), tc.platform, srv.URL+"/video", io.Discard, 5); err != nil {
				t.Fatal(err)
			}
			if _, err := client.Download(context.Background(), tc.platform, srv.URL+"/video", io.Discard, 4); !errors.Is(err, ErrMediaTooLarge) {
				t.Fatalf("limit error=%v", err)
			}
			srv.Close()
			_, err := client.Download(context.Background(), tc.platform, srv.URL+"/video?signature=private-signature", io.Discard, 5)
			if err == nil || strings.Contains(err.Error(), "private-") || strings.Contains(err.Error(), srv.URL) {
				t.Fatalf("network error=%v", err)
			}
		})
	}
}

func TestNewRequiresURLAndToken(t *testing.T) {
	if _, err := New("", "t", nil); err != ErrNotConfigured {
		t.Fatalf("err=%v", err)
	}
	if _, err := New("http://x", "", nil); err != ErrNotConfigured {
		t.Fatalf("err=%v", err)
	}
	if _, err := New("ftp://x", "t", nil); err == nil {
		t.Fatal("ftp accepted")
	}
}

func TestTranscriptionSubmitAndPoll(t *testing.T) {
	var submitted map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tingwu/transcribe":
			_ = json.NewDecoder(r.Body).Decode(&submitted)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "task_id": "T1", "status": "ONGOING", "wait": false})
		case "/api/tingwu/task":
			if r.URL.Query().Get("id") != "T1" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true, "task_id": "T1", "status": "COMPLETED", "transcript": "俺点了三个菜。", "duration_ms": 43300,
				"sentences": []map[string]any{{"start_ms": 0, "end_ms": 8040, "text": "俺点了三个菜。"}, {"text": ""}},
			})
		}
	}))
	defer srv.Close()
	client, _ := New(srv.URL, "t", srv.Client())
	started, err := client.StartTranscription(context.Background(), "https://cdn.example/v.mp4")
	if err != nil || started.TaskID != "T1" || started.Done() {
		t.Fatalf("start=%+v err=%v", started, err)
	}
	if submitted["video_url"] != "https://cdn.example/v.mp4" || submitted["wait"] != false {
		t.Fatalf("submitted=%v", submitted)
	}
	done, err := client.Transcription(context.Background(), "T1")
	if err != nil || !done.Done() || done.Text != "俺点了三个菜。" || len(done.Sentences) != 1 || done.Sentences[0].EndMS != 8040 {
		t.Fatalf("done=%+v err=%v", done, err)
	}
}

func TestTranscriptionErrorsAreReadable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "msg": "RuntimeError: ffmpeg 抽音失败 le 5.9\nmoov atom not found"})
	}))
	defer srv.Close()
	client, _ := New(srv.URL, "t", srv.Client())
	_, err := client.StartTranscription(context.Background(), "https://cdn.example/v.mp4")
	if err == nil || strings.Contains(err.Error(), "moov") || !strings.Contains(err.Error(), "口播转写失败") {
		t.Fatalf("err=%v", err)
	}
	if _, err := client.StartTranscription(context.Background(), ""); err == nil {
		t.Fatal("empty video url accepted")
	}
}

func TestResolveRejectsDouyinWebLinks(t *testing.T) {
	client, _ := New("http://127.0.0.1:1", "t", nil)
	if _, err := client.Resolve(context.Background(), "https://www.douyin.com/video/7669644282001082623"); err == nil || !strings.Contains(err.Error(), "v.douyin.com") {
		t.Fatalf("err=%v", err)
	}
}
