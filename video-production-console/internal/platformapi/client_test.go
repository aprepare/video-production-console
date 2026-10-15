package platformapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
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
			"ok": true, "has_more": true, "cursor": 8, "search_id": "s1", "login_wall": true,
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
	if !page.HasMore || page.Cursor != "8" || page.SearchID != "s1" || !page.LoginWall || len(page.Items) != 1 {
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

func TestSearchNormalizesXHSVideoAndFiltersEmptyDouyinShells(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/xhs/search":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "items": []map[string]any{
				{"id": "x1", "title": "海鲜", "type": "video", "video_url": "http://sns-video-v4.xhscdn.com/a.mp4", "duration": 93.5},
				{"id": "x2", "title": "图文", "type": "normal", "video_url": "http://sns-video-v4.xhscdn.com/stale.mp4", "duration": 8.0},
			}})
		case "/api/douyin/search":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "items": []map[string]any{
				{"aweme_id": "empty", "desc": "", "play_url": nil, "cover": nil, "digg_count": nil},
				{"aweme_id": "real", "desc": "有内容", "play_url": "https://v.douyinvod.com/v.mp4"},
			}})
		}
	}))
	defer srv.Close()
	client, _ := New(srv.URL, "t", srv.Client())
	xhs, err := client.Search(context.Background(), XHS, "海鲜", "", "", "")
	if err != nil || len(xhs.Items) != 2 || xhs.Items[0].VideoURL != "http://sns-video-v4.xhscdn.com/a.mp4" || xhs.Items[0].DurationS != 93.5 || xhs.Items[1].VideoURL != "" || xhs.Items[1].DurationS != 0 {
		t.Fatalf("xhs=%+v err=%v", xhs, err)
	}
	douyin, err := client.Search(context.Background(), Douyin, "海鲜", "", "", "")
	if err != nil || len(douyin.Items) != 1 || douyin.Items[0].ExternalID != "real" {
		t.Fatalf("douyin=%+v err=%v", douyin, err)
	}
}

func TestResolveXHSNormalizesVideoFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "note": map[string]any{
			"id": "6a79c9a0000000020600ddd4", "type": "video", "video_url": "http://sns-video-al.xhscdn.com/a.mp4", "duration": 12.25,
		}})
	}))
	defer srv.Close()
	client, _ := New(srv.URL, "t", srv.Client())
	post, err := client.Resolve(context.Background(), "6a79c9a0000000020600ddd4")
	if err != nil || post.VideoURL != "http://sns-video-al.xhscdn.com/a.mp4" || post.DurationS != 12.25 {
		t.Fatalf("post=%+v err=%v", post, err)
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
	buf.Reset()
	if _, err := client.Download(context.Background(), XHS, "/cache/notes/x/00.jpg?v=jpg", &buf, 1024); err != nil || buf.String() != "image-bytes" {
		t.Fatalf("cache gateway download body=%q err=%v", buf.String(), err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestOpenMediaValidatesHostsAndForwardsRange(t *testing.T) {
	var gotRange, gotUA string
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotRange, gotUA = r.Header.Get("Range"), r.UserAgent()
		return &http.Response{
			StatusCode: http.StatusPartialContent,
			Header:     http.Header{"Content-Type": {"video/mp4"}, "Content-Range": {"bytes 0-3/9"}},
			Body:       io.NopCloser(strings.NewReader("part")), Request: r,
		}, nil
	})}
	client, _ := New("https://gateway.example", "t", httpClient)
	resp, err := client.OpenMedia(context.Background(), XHS, "http://sns-video-v4.xhscdn.com/a.mp4", "bytes=0-3")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 206 || string(body) != "part" || gotRange != "bytes=0-3" || !strings.Contains(gotUA, "Mozilla") {
		t.Fatalf("status=%d body=%q range=%q ua=%q", resp.StatusCode, body, gotRange, gotUA)
	}
	for _, rawURL := range []string{
		"https://evil.example/video", "https://evilxhscdn.com/video", "https://xhscdn.com.evil.example/video", "http://127.0.0.1/video", "http://localhost/video", "ftp://sns-video-v4.xhscdn.com/video",
	} {
		if _, err := client.OpenMedia(context.Background(), XHS, rawURL, ""); !errors.Is(err, ErrMediaHostNotAllowed) {
			t.Errorf("url=%q err=%v", rawURL, err)
		}
	}
}

func TestValidateMediaURLRejectsCredentialsFragmentsAndGatewayTraversal(t *testing.T) {
	for _, rawURL := range []string{
		"https://user:pass@ci.xhscdn.com/a.jpg",
		"https://ci.xhscdn.com/a.jpg#fragment",
		"/api/../status",
		"/api/%2e%2e/status",
		"/cache/%252e%252e/status",
		"/cache/%5c..%5cstatus",
		"/api/xhs/search?keyword=x",
		"/api/status",
		"/api/tingwu/task?id=1",
		"/cache/other/x.jpg",
	} {
		if err := ValidateMediaURL(XHS, rawURL); !errors.Is(err, ErrMediaHostNotAllowed) {
			t.Errorf("url=%q err=%v", rawURL, err)
		}
	}
	for _, rawURL := range []string{"https://ci.xhscdn.com/a.jpg?u=https%3A%2F%2Fevil.example", "/api/img?u=https%3A%2F%2Fci.xhscdn.com%2Fa", "/cache/notes/69f43a770000000235016058/00.jpg?v=jpg"} {
		if err := ValidateMediaURL(XHS, rawURL); err != nil {
			t.Errorf("safe url=%q err=%v", rawURL, err)
		}
	}
	if err := ValidateMediaURL(Douyin, "/api/img?u=x"); !errors.Is(err, ErrMediaHostNotAllowed) {
		t.Fatalf("cross-platform gateway err=%v", err)
	}
}

func TestOpenMediaRejectsRedirects(t *testing.T) {
	var requestedHosts []string
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requestedHosts = append(requestedHosts, r.URL.Hostname())
		if r.URL.Hostname() == "evil.example" {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("leaked")), Request: r}, nil
		}
		return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {"https://evil.example/video"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})}
	client, _ := New("https://gateway.example", "t", httpClient)
	resp, err := client.OpenMedia(context.Background(), XHS, "https://sns-video-v4.xhscdn.com/a.mp4", "")
	if resp != nil {
		resp.Body.Close()
	}
	if !errors.Is(err, ErrMediaRedirect) {
		t.Fatalf("redirect err=%v", err)
	}
	if len(requestedHosts) != 1 || requestedHosts[0] != "sns-video-v4.xhscdn.com" {
		t.Fatalf("redirect made upstream requests to %v", requestedHosts)
	}
}

func TestXHSCommentsRequestsAllFor120SecondsAndFlattensSubs(t *testing.T) {
	var query url.Values
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		query = r.URL.Query()
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) < 119*time.Second || time.Until(deadline) > 121*time.Second {
			t.Errorf("deadline=%v ok=%v", deadline, ok)
		}
		comments := make([]map[string]any, 151)
		for i := range comments {
			comments[i] = map[string]any{"nick": "甲", "content": "主评论", "like": 9, "subs": []map[string]any{{"nick": "乙", "content": "子评论", "like": 3}}}
		}
		body, _ := json.Marshal(map[string]any{"ok": true, "comments": comments})
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)), Request: r}, nil
	})}
	client, _ := New("https://gateway.example", "t", httpClient)
	comments, err := client.Comments(context.Background(), XHS, "note1", 500)
	if err != nil || query.Get("id") != "note1" || query.Get("all") != "1" || len(comments) != 300 {
		t.Fatalf("comments=%+v query=%v err=%v", comments, query, err)
	}
	if comments[1].Author != "↳ 乙" || comments[1].Text != "子评论" || comments[1].Likes != 3 {
		t.Fatalf("sub=%+v", comments[1])
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

func TestDownloadAcceptsXHSPartialContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Range", "bytes 0-4/5")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = io.WriteString(w, "video")
	}))
	defer srv.Close()
	client, _ := New(srv.URL, "t", srv.Client())
	var out bytes.Buffer
	n, err := client.Download(context.Background(), XHS, srv.URL+"/video", &out, 10)
	if err != nil || n != 5 || out.String() != "video" {
		t.Fatalf("n=%d body=%q err=%v", n, out.String(), err)
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

func TestDouyinImagePostsHaveNoVideo(t *testing.T) {
	post := douyinPost(map[string]any{"aweme_id": "7658994183646877108", "desc": "日照去哪吃海鲜", "type": "images", "duration": float64(15000), "cover": "https://p3.douyinpic.com/a.jpeg", "digg_count": float64(257)})
	if post.VideoURL != "" || post.DurationS != 0 || post.CoverURL == "" {
		t.Fatalf("image post = %+v", post)
	}
	music := douyinPost(map[string]any{"aweme_id": "1", "desc": "x", "play_url": "https://v3-music.douyinstatic.com/a/?mime_type=audio_mp4", "duration": float64(15000)})
	if music.VideoURL != "" {
		t.Fatalf("audio-only play_url kept: %q", music.VideoURL)
	}
	video := douyinPost(map[string]any{"aweme_id": "2", "desc": "x", "type": "video", "play_url": "https://v5.zjcdn.com/a/?mime_type=video_mp4", "duration": float64(50876)})
	if video.VideoURL == "" || video.DurationS < 50 {
		t.Fatalf("video post = %+v", video)
	}
}
