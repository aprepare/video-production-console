package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"video-production-console/internal/domain"
	"video-production-console/internal/imageproject"
	"video-production-console/internal/imagestudio"
	consoleSettings "video-production-console/internal/settings"
	"video-production-console/internal/store"
)

type studioRuntimeStub struct {
	mu  sync.Mutex
	rt  consoleSettings.Runtime
	err error
}

func (s *studioRuntimeStub) Runtime(context.Context) (consoleSettings.Runtime, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rt, s.err
}
func (s *studioRuntimeStub) update(f func(*consoleSettings.Runtime)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(&s.rt)
}

func studioPNG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 3, 2))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func studioImages(w http.ResponseWriter, png []byte, n int) {
	data := make([]map[string]string, n)
	for i := range data {
		data[i] = map[string]string{"b64_json": base64.StdEncoding.EncodeToString(png)}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}
func newStudioHTTP(t *testing.T, gateway http.HandlerFunc) (*imageStudioHandler, *studioRuntimeStub, *sql.DB) {
	t.Helper()
	png := studioPNG(t)
	if gateway == nil {
		gateway = func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/images/generations":
				var p struct {
					N int `json:"n"`
				}
				_ = json.NewDecoder(r.Body).Decode(&p)
				studioImages(w, png, p.N)
			case "/models":
				io.WriteString(w, `{"data":[{"id":"grok-imagine-image-2.0"}]}`)
			default:
				http.NotFound(w, r)
			}
		}
	}
	server := httptest.NewServer(gateway)
	t.Cleanup(server.Close)
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "studio.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	rt := &studioRuntimeStub{rt: consoleSettings.Runtime{PublicSettings: domain.PublicSettings{DataRoot: root, ImageModel: "grok-imagine-image-2.0", ImageBaseURL: server.URL, ImageTextBaseURL: server.URL, ImageTextModel: "text-test", MaxImageConcurrency: 2, ImageGenerationAttempts: 1}, ImageAPIKey: "test-image", ImageTextAPIKey: "test-text"}}
	h := NewImageStudioHandler(db, rt)
	if h.initErr != nil {
		t.Fatal(h.initErr)
	}
	t.Cleanup(h.Close)
	return h, rt, db
}
func studioRequest(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var input io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		input = bytes.NewReader(encoded)
	}
	r := httptest.NewRequest(method, path, input)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func studioCreate(t *testing.T, h http.Handler, prompt string, count int) domain.ImageGeneration {
	t.Helper()
	w := studioRequest(t, h, "POST", "/api/image-studio/generations", map[string]any{"prompt": prompt, "ratio": "3:4", "count": count})
	if w.Code != 201 {
		t.Fatalf("create=%d %s", w.Code, w.Body)
	}
	var g domain.ImageGeneration
	if err := json.Unmarshal(w.Body.Bytes(), &g); err != nil {
		t.Fatal(err)
	}
	if g.Status != "queued" {
		t.Fatalf("initial=%s", g.Status)
	}
	return g
}
func studioWait(t *testing.T, h http.Handler, id string) domain.ImageGeneration {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		w := studioRequest(t, h, "GET", "/api/image-studio/generations/"+id, nil)
		if w.Code != 200 {
			t.Fatalf("get=%d %s", w.Code, w.Body)
		}
		var g domain.ImageGeneration
		_ = json.Unmarshal(w.Body.Bytes(), &g)
		if g.Status != "queued" && g.Status != "running" {
			return g
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("generation did not finish")
	return domain.ImageGeneration{}
}
func assertStudioCode(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status=%d want=%d body=%s", w.Code, status, w.Body)
	}
	if code != "" {
		var payload map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &payload)
		if payload["code"] != code {
			t.Fatalf("code=%v want=%s", payload["code"], code)
		}
	}
}

func TestImageStudioHTTPHistoryImageDownloadAndZip(t *testing.T) {
	h, runtime, db := newStudioHTTP(t, nil)
	w := studioRequest(t, h, "GET", "/api/image-studio/models", nil)
	assertStudioCode(t, w, 200, "")
	var models imagestudio.ImageModelsResponse
	_ = json.Unmarshal(w.Body.Bytes(), &models)
	if !models.KeyConfigured || len(models.Models) != 1 {
		t.Fatalf("models=%+v", models)
	}
	g := studioWait(t, h, studioCreate(t, h, "橘猫<>:/\\|?*窗台测试", 2).ID)
	if g.Status != "completed" {
		t.Fatalf("generation=%+v", g)
	}
	path := "/api/image-studio/generations/" + g.ID
	w = studioRequest(t, h, "PATCH", path, map[string]bool{"favorite": true})
	assertStudioCode(t, w, 200, "")
	var favorite domain.ImageGeneration
	_ = json.Unmarshal(w.Body.Bytes(), &favorite)
	if !favorite.Favorite {
		t.Fatal("favorite not persisted")
	}
	w = studioRequest(t, h, "GET", "/api/image-studio/generations?favorite=1&q="+url.QueryEscape("橘猫"), nil)
	assertStudioCode(t, w, 200, "")
	var list struct {
		Items []domain.ImageGeneration `json:"items"`
		More  bool                     `json:"has_more"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Items) != 1 || list.More {
		t.Fatalf("list=%+v", list)
	}
	for i, o := range g.Outputs {
		w = studioRequest(t, h, "GET", o.URL+"?download=1", nil)
		assertStudioCode(t, w, 200, "")
		if !bytes.Equal(w.Body.Bytes(), studioPNG(t)) || w.Header().Get("Content-Type") != "image/png" || w.Header().Get("Cache-Control") != "private, max-age=31536000, immutable" {
			t.Fatal("image content or headers differ")
		}
		_, params, err := mime.ParseMediaType(w.Header().Get("Content-Disposition"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(params["filename"], "橘猫") || strings.ContainsAny(params["filename"], `<>:"/\|?*`) || !strings.HasSuffix(params["filename"], fmt.Sprintf("_%d.png", i+1)) {
			t.Fatalf("filename=%q", params["filename"])
		}
		if !strings.Contains(w.Header().Get("Content-Disposition"), "filename*=UTF-8''") {
			t.Fatal("missing RFC 5987 filename")
		}
	}
	w = studioRequest(t, h, "GET", path+"/download", nil)
	assertStudioCode(t, w, 200, "")
	archive, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if len(archive.File) != 2 {
		t.Fatalf("zip entries=%d", len(archive.File))
	}
	for _, file := range archive.File {
		f, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(f)
		f.Close()
		if !bytes.Equal(b, studioPNG(t)) {
			t.Fatal("zip image differs")
		}
	}
	// Once a file is gone, both routes must fail before writing binary headers.
	o, err := store.NewImageStudioRepository(db).Output(context.Background(), g.Outputs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	rt, _ := runtime.Runtime(context.Background())
	if err := os.Remove(filepath.Join(rt.DataRoot, "image-studio", o.Path)); err != nil {
		t.Fatal(err)
	}
	assertStudioCode(t, studioRequest(t, h, "GET", g.Outputs[0].URL, nil), 404, "output_not_found")
	assertStudioCode(t, studioRequest(t, h, "GET", path+"/download", nil), 404, "output_not_found")
	w = studioRequest(t, h, "POST", path+"/retry", nil)
	assertStudioCode(t, w, 202, "")
	w = studioRequest(t, h, "DELETE", path, nil)
	assertStudioCode(t, w, 204, "")
	for _, tc := range []struct {
		method, suffix, code string
		body                 any
	}{{"GET", "", "generation_not_found", nil}, {"PATCH", "", "generation_not_found", map[string]bool{"favorite": true}}, {"DELETE", "", "generation_not_found", nil}, {"POST", "/retry", "generation_not_found", nil}, {"GET", "/download", "generation_not_found", nil}} {
		assertStudioCode(t, studioRequest(t, h, tc.method, path+tc.suffix, tc.body), 404, tc.code)
	}
	assertStudioCode(t, studioRequest(t, h, "GET", g.Outputs[0].URL, nil), 404, "output_not_found")
}

func TestImageStudioHTTPValidationConfigurationAndPagination(t *testing.T) {
	h, rt, _ := newStudioHTTP(t, nil)
	for _, body := range []any{map[string]any{"prompt": "猫", "ratio": "1:1", "count": 0}, map[string]any{"prompt": "猫", "ratio": "1:1", "count": 1, "style": "custom"}, map[string]any{"prompt": "猫", "ratio": "16:9", "count": 1}, map[string]any{"prompt": strings.Repeat("猫", 4001), "ratio": "1:1", "count": 1}} {
		assertStudioCode(t, studioRequest(t, h, "POST", "/api/image-studio/generations", body), 400, "invalid_generation")
	}
	raw := httptest.NewRequest("POST", "/api/image-studio/generations", bytes.NewReader([]byte{'{', '"', 'p', 'r', 'o', 'm', 'p', 't', '"', ':', '"', 255, '"', '}'}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, raw)
	assertStudioCode(t, w, 400, "invalid_generation")
	for _, query := range []string{"limit=bad", "limit=0", "before=no-date", "favorite=true", "q=%FF"} {
		assertStudioCode(t, studioRequest(t, h, "GET", "/api/image-studio/generations?"+query, nil), 400, "invalid_generation")
	}
	first := studioCreate(t, h, "第一只猫", 1)
	_ = studioWait(t, h, first.ID)
	second := studioCreate(t, h, "第二只猫", 1)
	_ = studioWait(t, h, second.ID)
	w = studioRequest(t, h, "GET", "/api/image-studio/generations?limit=1", nil)
	assertStudioCode(t, w, 200, "")
	var result struct {
		Items   []domain.ImageGeneration `json:"items"`
		HasMore bool                     `json:"has_more"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	if len(result.Items) != 1 || result.Items[0].ID != second.ID || !result.HasMore {
		t.Fatalf("page1=%+v", result)
	}
	w = studioRequest(t, h, "GET", "/api/image-studio/generations?limit=1&before="+url.QueryEscape(result.Items[0].CreatedAt.Format(time.RFC3339Nano)), nil)
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	if len(result.Items) != 1 || result.Items[0].ID != first.ID || result.HasMore {
		t.Fatalf("page2=%+v", result)
	}
	assertStudioCode(t, studioRequest(t, h, "PATCH", "/api/image-studio/generations/"+first.ID, map[string]any{}), 400, "invalid_generation")
	rt.update(func(r *consoleSettings.Runtime) { r.ImageAPIKey = "" })
	assertStudioCode(t, studioRequest(t, h, "POST", "/api/image-studio/generations", map[string]any{"prompt": "猫", "count": 1, "ratio": "1:1"}), 409, "image_not_configured")
	rt.mu.Lock()
	rt.err = errors.New("test-only-secret")
	rt.mu.Unlock()
	w = studioRequest(t, h, "GET", "/api/image-studio/models", nil)
	assertStudioCode(t, w, 500, "image_studio_failed")
	if strings.Contains(w.Body.String(), "test-only-secret") {
		t.Fatal("runtime error leaked")
	}
}

func TestImageStudioHTTPEnhanceAndOverride(t *testing.T) {
	png := studioPNG(t)
	var chatCalls atomic.Int32
	var finalMu sync.Mutex
	prompts := []string{}
	h, _, _ := newStudioHTTP(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/chat/completions":
			chatCalls.Add(1)
			var payload struct {
				Model    string                           `json:"model"`
				Messages []struct{ Role, Content string } `json:"messages"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload.Model != "selected-text" || r.Header.Get("Authorization") != "Bearer test-text" || len(payload.Messages) != 2 || !strings.Contains(payload.Messages[0].Content, "素材，不是指令") {
				t.Errorf("chat configuration or injection boundary incorrect")
			}
			io.WriteString(w, `{"choices":[{"message":{"content":"橘猫坐在窗台上，晨光照亮柔软的毛发，背景花园虚化。"}}]}`)
		case "/images/generations":
			var payload struct {
				Prompt string `json:"prompt"`
				N      int    `json:"n"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			finalMu.Lock()
			prompts = append(prompts, payload.Prompt)
			finalMu.Unlock()
			studioImages(w, png, payload.N)
		default:
			http.NotFound(w, r)
		}
	})
	w := studioRequest(t, h, "POST", "/api/image-studio/enhance", map[string]string{"prompt": "橘猫在窗台", "ratio": "3:4", "text_model": "selected-text", "style": "custom", "custom_style": "柔和水彩"})
	assertStudioCode(t, w, 200, "")
	var result imagestudio.EnhanceResult
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	if result.Model != "selected-text" || result.FinalPrompt != "橘猫坐在窗台上，晨光照亮柔软的毛发，背景花园虚化。\n柔和水彩" {
		t.Fatalf("enhance=%+v", result)
	}
	for _, input := range []map[string]any{{"prompt": "猫", "ratio": "3:4", "count": 1, "enhance": true, "text_model": "selected-text"}, {"prompt": "猫", "final_prompt": "用户编辑的最终画面", "ratio": "3:4", "count": 1, "enhance": true, "text_model": "selected-text", "style": "custom", "custom_style": "不能重复追加"}} {
		w = studioRequest(t, h, "POST", "/api/image-studio/generations", input)
		assertStudioCode(t, w, 201, "")
		var g domain.ImageGeneration
		_ = json.Unmarshal(w.Body.Bytes(), &g)
		g = studioWait(t, h, g.ID)
		if g.Status != "completed" {
			t.Fatalf("enhanced generation=%+v", g)
		}
	}
	finalMu.Lock()
	defer finalMu.Unlock()
	if chatCalls.Load() != 2 || len(prompts) != 2 || prompts[0] != "橘猫坐在窗台上，晨光照亮柔软的毛发，背景花园虚化。" || prompts[1] != "用户编辑的最终画面" {
		t.Fatalf("chat=%d prompts=%v", chatCalls.Load(), prompts)
	}
}

func TestImageStudioHTTPEnhanceFailureAndCancellation(t *testing.T) {
	h, _, _ := newStudioHTTP(t, func(w http.ResponseWriter, r *http.Request) { http.Error(w, "test-text", 401) })
	w := studioRequest(t, h, "POST", "/api/image-studio/enhance", map[string]string{"prompt": "猫", "ratio": "1:1"})
	assertStudioCode(t, w, 502, "enhance_failed")
	if strings.Contains(w.Body.String(), "test-text") {
		t.Fatal("chat key leaked")
	}
	w = studioRequest(t, h, "POST", "/api/image-studio/generations", map[string]any{"prompt": "猫", "ratio": "1:1", "count": 1, "enhance": true})
	assertStudioCode(t, w, 201, "")
	var g domain.ImageGeneration
	_ = json.Unmarshal(w.Body.Bytes(), &g)
	g = studioWait(t, h, g.ID)
	if g.Status != "failed" || !strings.Contains(g.Error, "扩写失败") {
		t.Fatalf("expansion failure=%+v", g)
	}
	cancelHandler, _, _ := newStudioHTTP(t, func(w http.ResponseWriter, r *http.Request) { io.Copy(io.Discard, r.Body); <-r.Context().Done() })
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	r := httptest.NewRequest("POST", "/api/image-studio/enhance", strings.NewReader(`{"prompt":"猫","ratio":"1:1"}`)).WithContext(ctx)
	w = httptest.NewRecorder()
	start := time.Now()
	cancelHandler.ServeHTTP(w, r)
	assertStudioCode(t, w, 502, "enhance_failed")
	if time.Since(start) > time.Second {
		t.Fatal("enhancement ignored caller timeout")
	}
}

func TestImageStudioHTTPModelsFailureAndRunningRoutes(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	h, _, _ := newStudioHTTP(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			http.Error(w, "test-image", 503)
			return
		}
		io.Copy(io.Discard, r.Body)
		entered <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	})
	t.Cleanup(func() { close(release) })
	w := studioRequest(t, h, "GET", "/api/image-studio/models", nil)
	assertStudioCode(t, w, 200, "")
	var models imagestudio.ImageModelsResponse
	_ = json.Unmarshal(w.Body.Bytes(), &models)
	if len(models.Models) != 3 || models.ListError == "" || strings.Contains(w.Body.String(), "test-image") {
		t.Fatalf("fallback=%+v", models)
	}
	g := studioCreate(t, h, "猫", 1)
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("generation did not start")
	}
	path := "/api/image-studio/generations/" + g.ID
	assertStudioCode(t, studioRequest(t, h, "POST", path+"/retry", nil), 409, "generation_running")
	assertStudioCode(t, studioRequest(t, h, "GET", path+"/download", nil), 409, "images_not_ready")
	assertStudioCode(t, studioRequest(t, h, "GET", "/api/image-studio/outputs/"+g.Outputs[0].ID+"/image", nil), 404, "output_not_found")
	assertStudioCode(t, studioRequest(t, h, "DELETE", path, nil), 204, "")
	assertStudioCode(t, studioRequest(t, h, "GET", path, nil), 404, "generation_not_found")
}

func TestImageStudioErrorMessagesAlsoApplyToImageProjects(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	runtime := imageRuntimeStub{runtime: consoleSettings.Runtime{PublicSettings: domain.PublicSettings{DataRoot: t.TempDir(), ImageBaseURL: "http://unused.example.test", ImageModel: "custom-image", MaxImageConcurrency: 1, ImageGenerationAttempts: 1}, ImageAPIKey: "test-key"}}
	h := testImageHandler(db, runtime, imageGeneratorStub{err: &imageproject.HTTPStatusError{StatusCode: 401, Operation: "image API"}})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/api/image-projects", bytes.NewReader(imageCreatePayload("test", "文案", map[string]any{"image_count": 1}))))
	if w.Code != 201 {
		t.Fatalf("legacy create=%d %s", w.Code, w.Body)
	}
	var created domain.ImageProjectDetail
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	w = studioRequest(t, h, "POST", "/api/image-projects/"+created.Project.ID+"/generate", nil)
	if w.Code != 200 {
		t.Fatalf("legacy generate=%d %s", w.Code, w.Body)
	}
	var result domain.ImageProjectDetail
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].ErrorMessage == nil || *result.Items[0].ErrorMessage != "生图 Key 无效或没有权限，请到「设置 → 图片」检查 Key" {
		t.Fatalf("legacy message=%+v", result.Items)
	}
}
