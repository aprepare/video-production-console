package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"video-production-console/internal/domain"
	"video-production-console/internal/refanalysis"
	consoleSettings "video-production-console/internal/settings"
	"video-production-console/internal/store"
)

type analysisRuntimeStub struct{ value consoleSettings.Runtime }

func (s analysisRuntimeStub) Runtime(context.Context) (consoleSettings.Runtime, error) {
	return s.value, nil
}

func analysisHTTPFixture(t *testing.T, customize ...func(*consoleSettings.Runtime)) (http.Handler, *store.ReferenceAnalysisRepository, *store.ReferenceRepository, string) {
	t.Helper()
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	refs := store.NewReferenceRepository(db)
	if _, err := refs.Upsert(t.Context(), store.ReferencePost{ID: "ref", Platform: "douyin", ExternalID: "ref"}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "references", "ref")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "source.mp4"), []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"choices":[{"message":{"content":"{\"summary\":\"手机就能拍\"}"}}]}`)
	}))
	t.Cleanup(server.Close)
	runtime := analysisRuntimeStub{consoleSettings.Runtime{PublicSettings: domain.PublicSettings{DataRoot: root, AnalysisBaseURL: server.URL, AnalysisModel: "test-model"}}}
	for _, change := range customize {
		change(&runtime.value)
	}
	repo := store.NewReferenceAnalysisRepository(db)
	svc := refanalysis.New(repo, refs, runtime, refanalysis.Options{
		DetectShots: func(context.Context, string, string, string) (refanalysis.VideoInfo, []refanalysis.Shot, error) {
			return refanalysis.VideoInfo{DurationS: 1}, []refanalysis.Shot{{Index: 1, StartS: 0, EndS: 1}}, nil
		},
		ExtractFrames: func(ctx context.Context, _, _ string, _ []refanalysis.Shot, dir string) ([]refanalysis.Frame, error) {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, err
			}
			if err := os.WriteFile(filepath.Join(dir, "frame_01.jpg"), []byte{255, 216, 255, 217}, 0o600); err != nil {
				return nil, err
			}
			return []refanalysis.Frame{{Index: 1, TimeS: 0.3, Shot: 1}}, nil
		},
	})
	t.Cleanup(svc.Close)
	return NewReferenceAnalysesHandler(svc), repo, refs, root
}

func serveAnalysis(handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	handler.ServeHTTP(w, r)
	return w
}

func TestReferenceAnalysisHTTPRoutesReportFramesAndDelete(t *testing.T) {
	h, repo, _, root := analysisHTTPFixture(t)
	start := serveAnalysis(h, "POST", "/api/reference-analyses", `{"reference_id":"ref","model":"chosen","focus":"看开头"}`)
	var a store.ReferenceAnalysis
	if start.Code != 202 || json.Unmarshal(start.Body.Bytes(), &a) != nil || a.Status != "queued" || a.Model != "chosen" {
		t.Fatalf("POST: %d %s", start.Code, start.Body.String())
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		current, err := repo.Get(t.Context(), a.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Status == "completed" {
			break
		}
		if current.Status == "failed" || time.Now().After(deadline) {
			t.Fatalf("分析未完成: %+v", current)
		}
		time.Sleep(5 * time.Millisecond)
	}
	get := serveAnalysis(h, "GET", "/api/reference-analyses/"+a.ID, "")
	var detail refanalysis.Analysis
	if get.Code != 200 || json.Unmarshal(get.Body.Bytes(), &detail) != nil || detail.Report == nil || detail.Report.Summary != "手机就能拍" || len(detail.Frames) != 1 {
		t.Fatalf("GET: %d %s", get.Code, get.Body.String())
	}
	if strings.Contains(get.Body.String(), "report_json") || strings.Contains(get.Body.String(), root) {
		t.Fatal("响应暴露内部字段或文件路径")
	}
	frame := serveAnalysis(h, "GET", detail.Frames[0].URL, "")
	if frame.Code != 200 || frame.Header().Get("Content-Type") != "image/jpeg" || frame.Body.String() != string([]byte{255, 216, 255, 217}) {
		t.Fatalf("帧路由: %d %s", frame.Code, frame.Header())
	}
	list := serveAnalysis(h, "GET", "/api/reference-analyses?reference_id=ref", "")
	var items []store.ReferenceAnalysis
	if list.Code != 200 || json.Unmarshal(list.Body.Bytes(), &items) != nil || len(items) != 1 || items[0].ID != a.ID {
		t.Fatalf("列表: %d %s", list.Code, list.Body.String())
	}
	if got := serveAnalysis(h, "GET", "/api/reference-analyses/"+a.ID+"/frames/2", ""); got.Code != 404 {
		t.Fatalf("不存在的帧: %d", got.Code)
	}
	deleted := serveAnalysis(h, "DELETE", "/api/reference-analyses/"+a.ID, "")
	if deleted.Code != 204 {
		t.Fatalf("DELETE: %d %s", deleted.Code, deleted.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "references", "ref", "analysis", a.ID)); !os.IsNotExist(err) {
		t.Fatalf("分析目录仍存在: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "references", "ref", "source.mp4")); err != nil {
		t.Fatal("误删原片")
	}
	if got := serveAnalysis(h, "GET", "/api/reference-analyses/"+a.ID, ""); got.Code != 404 {
		t.Fatalf("删除后 GET: %d", got.Code)
	}
}

func TestReferenceAnalysisHTTPStatusCodesAndTraversal(t *testing.T) {
	h, _, _, root := analysisHTTPFixture(t)
	if err := os.Remove(filepath.Join(root, "references", "ref", "source.mp4")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path, body string
		status             int
		code               string
	}{
		{"POST", "/api/reference-analyses", `{"reference_id":"ref"}`, 409, "reference_media_not_ready"},
		{"POST", "/api/reference-analyses", `{"reference_id":"missing"}`, 404, "reference_not_found"},
		{"POST", "/api/reference-analyses", `{`, 400, "invalid_analysis"},
		{"POST", "/api/reference-analyses", `{"reference_id":"ref","typo":1}`, 400, "invalid_analysis"},
		{"POST", "/api/reference-analyses", `{}`, 400, "invalid_analysis"},
		{"GET", "/api/reference-analyses", "", 400, "invalid_analysis"},
		{"GET", "/api/reference-analyses/missing", "", 404, "reference_analysis_not_found"},
		{"DELETE", "/api/reference-analyses/missing", "", 404, "reference_analysis_not_found"},
		{"GET", "/api/reference-analyses/%2e%2e/frames/1", "", 403, "invalid_frame_path"},
		{"GET", "/api/reference-analyses/ref/frames/%2e%2e%2fsource.mp4", "", 403, "invalid_frame_path"},
		{"GET", "/api/reference-analyses/ref/frames/..%5csource.mp4", "", 403, "invalid_frame_path"},
	} {
		got := serveAnalysis(h, tc.method, tc.path, tc.body)
		if got.Code != tc.status || !strings.Contains(got.Body.String(), tc.code) {
			t.Errorf("%s %s: %d %s", tc.method, tc.path, got.Code, got.Body.String())
		}
	}
}

func TestReferenceAnalysisHTTPRejectsStoredPathEscape(t *testing.T) {
	h, repo, refs, root := analysisHTTPFixture(t)
	if _, err := refs.Upsert(t.Context(), store.ReferencePost{ID: "../escape", Platform: "xhs", ExternalID: "escape"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.CreateQueued(t.Context(), store.ReferenceAnalysis{ID: "unsafe", ReferenceID: "../escape"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkRunning(t.Context(), "unsafe"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetFrames(t.Context(), "unsafe", 1); err != nil {
		t.Fatal(err)
	}
	if err := repo.Complete(t.Context(), "unsafe", `{}`); err != nil {
		t.Fatal(err)
	}
	escape := filepath.Join(root, "escape", "analysis", "unsafe")
	if err := os.MkdirAll(escape, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(escape, "frame_01.jpg")
	if err := os.WriteFile(sentinel, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ method, path string }{{"GET", "/api/reference-analyses/unsafe/frames/1"}, {"GET", "/api/reference-analyses/unsafe"}, {"DELETE", "/api/reference-analyses/unsafe"}} {
		if got := serveAnalysis(h, tc.method, tc.path, ""); got.Code != 403 {
			t.Fatalf("存储路径逃逸: %d %s", got.Code, got.Body.String())
		}
	}
	if raw, err := os.ReadFile(sentinel); err != nil || string(raw) != "outside" {
		t.Fatal("越界文件被修改")
	}
	if _, err := repo.Get(t.Context(), "unsafe"); errors.Is(err, store.ErrReferenceAnalysisNotFound) {
		t.Fatal("失败的目录清理不能先删除记录")
	}
}

func TestReferenceAnalysisHTTPRejectsSymlinkEscape(t *testing.T) {
	h, repo, refs, root := analysisHTTPFixture(t)
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "frame_01.jpg")
	if err := os.WriteFile(sentinel, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(root, "references", "ref", "analysis")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(parent, "linked")); err != nil {
		t.Skipf("本机不允许创建符号链接: %v", err)
	}
	if _, _, err := repo.CreateQueued(t.Context(), store.ReferenceAnalysis{ID: "linked", ReferenceID: "ref"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkRunning(t.Context(), "linked"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetFrames(t.Context(), "linked", 1); err != nil {
		t.Fatal(err)
	}
	if err := repo.Complete(t.Context(), "linked", `{}`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ method, path string }{{"GET", "/api/reference-analyses/linked/frames/1"}, {"GET", "/api/reference-analyses/linked"}, {"DELETE", "/api/reference-analyses/linked"}} {
		if got := serveAnalysis(h, tc.method, tc.path, ""); got.Code != 403 {
			t.Fatalf("符号链接逃逸: %d %s", got.Code, got.Body.String())
		}
	}
	if raw, err := os.ReadFile(sentinel); err != nil || string(raw) != "outside" {
		t.Fatal("越界文件被修改")
	}
	if _, err := refs.Upsert(t.Context(), store.ReferencePost{ID: "linked-source", Platform: "xhs", ExternalID: "linked-source"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "references", "linked-source")); err != nil {
		t.Fatal(err)
	}
	if got := serveAnalysis(h, "POST", "/api/reference-analyses", `{"reference_id":"linked-source"}`); got.Code != 403 {
		t.Fatalf("原片目录符号链接逃逸: %d %s", got.Code, got.Body.String())
	}
}

func TestReferenceAnalysisHTTPRequiresModelConfiguration(t *testing.T) {
	h, repo, _, _ := analysisHTTPFixture(t, func(rt *consoleSettings.Runtime) {
		rt.AnalysisBaseURL = ""
	})
	got := serveAnalysis(h, "POST", "/api/reference-analyses", `{"reference_id":"ref"}`)
	if got.Code != 409 || !strings.Contains(got.Body.String(), "analysis_not_configured") || !strings.Contains(got.Body.String(), "请先在设置里配置拆解模型") {
		t.Fatalf("无模型配置: %d %s", got.Code, got.Body.String())
	}
	rows, err := repo.ListByReference(t.Context(), "ref")
	if err != nil || len(rows) != 0 {
		t.Fatal("未配置模型时不应创建分析记录")
	}
}

func TestAnalysisSettingsHTTPGetIncludesPublicFieldsAndMaskedSecret(t *testing.T) {
	h, _, public := newSettingsHTTPTest(t, consoleSettings.Options{})
	seed, err := json.Marshal(map[string]any{"public": public})
	if err != nil {
		t.Fatal(err)
	}
	if got := serveAnalysis(h, "PUT", "/api/settings", string(seed)); got.Code != 200 {
		t.Fatalf("初始化设置: %d %s", got.Code, got.Body.String())
	}
	put := serveAnalysis(h, "PUT", "/api/settings", `{"public":{"analysis_base_url":"https://analysis.test/v1","analysis_model":"自选模型"},"secrets":{"analysis_api_key":"private-analysis-key"}}`)
	if put.Code != 200 {
		t.Fatalf("PUT: %d %s", put.Code, put.Body.String())
	}
	get := serveAnalysis(h, "GET", "/api/settings", "")
	var view consoleSettings.View
	if get.Code != 200 || json.Unmarshal(get.Body.Bytes(), &view) != nil || view.Public.AnalysisBaseURL != "https://analysis.test/v1" || view.Public.AnalysisModel != "自选模型" || !view.Secrets[consoleSettings.SecretAnalysisAPIKey].Configured {
		t.Fatalf("GET: %d %s", get.Code, get.Body.String())
	}
	if strings.Contains(get.Body.String(), "private-analysis-key") {
		t.Fatal("设置 GET 泄漏密钥")
	}
}
