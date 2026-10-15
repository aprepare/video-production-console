package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"video-production-console/internal/domain"
	"video-production-console/internal/monitor"
	"video-production-console/internal/platformapi"
	"video-production-console/internal/settings"
	"video-production-console/internal/store"
)

type monitorReferencesStub struct {
	refs     *store.ReferenceRepository
	collects atomic.Int32
}

func (*monitorReferencesStub) Search(context.Context, string, string, string, string, string) (platformapi.SearchPage, error) {
	return platformapi.SearchPage{}, nil
}
func (*monitorReferencesStub) ScoreCover(context.Context, string, string) ([]byte, error) {
	return nil, nil
}
func (f *monitorReferencesStub) Collect(ctx context.Context, _ string, p *platformapi.Post, _, _ string, _ bool) (store.ReferencePost, error) {
	f.collects.Add(1)
	return f.refs.Upsert(ctx, store.ReferencePost{ID: "collected", Platform: string(p.Platform), ExternalID: p.ExternalID, Title: p.Title})
}

func monitorFixture(t *testing.T) (http.Handler, *store.MonitorRepository, *monitorReferencesStub, *sql.DB) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": `{"items":[{"external_id":"post","score":82,"reason":"适合借鉴","tags":["价格对比"]}]}`}}}})
	}))
	t.Cleanup(model.Close)
	repo := store.NewMonitorRepository(db)
	refs := &monitorReferencesStub{refs: store.NewReferenceRepository(db)}
	runtime := analysisRuntimeStub{settings.Runtime{PublicSettings: domain.PublicSettings{AnalysisBaseURL: model.URL, AnalysisModel: "fake"}}}
	s := monitor.New(repo, refs, refs.refs, runtime, monitor.Options{Jitter: func(time.Duration) time.Duration { return 0 }})
	t.Cleanup(s.Close)
	return NewMonitorsHandler(s), repo, refs, db
}
func monitorRequest(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	h.ServeHTTP(w, r)
	return w
}
func createHTTPMonitor(t *testing.T, h http.Handler) store.MonitorTask {
	t.Helper()
	w := monitorRequest(h, "POST", "/api/monitors", `{"platform":"douyin","keyword":"岚山海鲜","enabled":false}`)
	var task store.MonitorTask
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &task) != nil {
		t.Fatalf("create=%d %s", w.Code, w.Body.String())
	}
	return task
}
func TestMonitorHTTPTaskRoutesPartialPatchAndManualConflict(t *testing.T) {
	h, _, _, _ := monitorFixture(t)
	task := createHTTPMonitor(t, h)
	if task.Name != "岚山海鲜" || task.Enabled || !task.ScoreEnabled || task.Pages != 1 || task.ID == "" {
		t.Fatalf("defaults=%+v", task)
	}
	for _, path := range []string{"/api/monitors", "/api/monitors/" + task.ID} {
		if w := monitorRequest(h, "GET", path, ""); w.Code != 200 || !strings.Contains(w.Body.String(), task.ID) {
			t.Fatalf("GET %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	w := monitorRequest(h, "PATCH", "/api/monitors/"+task.ID, `{"score_enabled":false,"score_threshold":0,"surge_like_ratio":0,"interval_minutes":60}`)
	var updated store.MonitorTask
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &updated) != nil || updated.ScoreEnabled || updated.ScoreThreshold != 0 || updated.SurgeLikeRatio != 0 || updated.Keyword != task.Keyword || updated.NextRunAt == task.NextRunAt {
		t.Fatalf("patch=%d %s", w.Code, w.Body.String())
	}
	w = monitorRequest(h, "POST", "/api/monitors/"+task.ID+"/run", "")
	if w.Code != 202 || !strings.Contains(w.Body.String(), `"running":true`) {
		t.Fatalf("run=%d %s", w.Code, w.Body.String())
	}
	w = monitorRequest(h, "POST", "/api/monitors/"+task.ID+"/run", "")
	if w.Code != 409 || !strings.Contains(w.Body.String(), `"code":"monitor_running"`) {
		t.Fatalf("duplicate=%d %s", w.Code, w.Body.String())
	}
	w = monitorRequest(h, "GET", "/api/monitors/"+task.ID+"/runs?limit=20", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"trigger":"manual"`) {
		t.Fatalf("runs=%d %s", w.Code, w.Body.String())
	}
	w = monitorRequest(h, "DELETE", "/api/monitors/"+task.ID, "")
	if w.Code != 204 {
		t.Fatalf("delete=%d %s", w.Code, w.Body.String())
	}
	w = monitorRequest(h, "GET", "/api/monitors/"+task.ID, "")
	if w.Code != 404 {
		t.Fatalf("deleted=%d", w.Code)
	}
}
func TestMonitorHTTPFeedItemActionsAndRouteSpecificity(t *testing.T) {
	h, repo, refs, _ := monitorFixture(t)
	task := createHTTPMonitor(t, h)
	_, err := repo.CreateRun(t.Context(), store.MonitorRun{ID: "run", TaskID: task.ID, Status: "completed", Trigger: "manual", StartedAt: time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		t.Fatal(err)
	}
	item, _, err := repo.UpsertItem(t.Context(), task.ID, "run", platformapi.Post{Platform: platformapi.Douyin, ExternalID: "post", Title: "海鲜", Likes: 100}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	w := monitorRequest(h, "GET", "/api/monitors/feed", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"items":[`) || !strings.Contains(w.Body.String(), `"counts":{"all":1`) {
		t.Fatalf("feed matched task route: %d %s", w.Code, w.Body.String())
	}
	w = monitorRequest(h, "PATCH", "/api/monitors/items/"+item.ID, `{"read":true}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"read":true`) {
		t.Fatalf("item patch=%d %s", w.Code, w.Body.String())
	}
	w = monitorRequest(h, "POST", "/api/monitors/items/read-all", `{"task_id":""}`)
	if w.Code != 204 {
		t.Fatalf("read-all matched item route: %d %s", w.Code, w.Body.String())
	}
	w = monitorRequest(h, "POST", "/api/monitors/items/"+item.ID+"/score", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"score":82`) || !strings.Contains(w.Body.String(), `"score_tags":["价格对比"]`) {
		t.Fatalf("score=%d %s", w.Code, w.Body.String())
	}
	for i := 0; i < 2; i++ {
		w = monitorRequest(h, "POST", "/api/monitors/items/"+item.ID+"/collect", `{"transcribe":false}`)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"id":"collected"`) {
			t.Fatalf("collect=%d %s", w.Code, w.Body.String())
		}
	}
	if refs.collects.Load() != 1 {
		t.Fatal("repeat collect was not idempotent")
	}
	w = monitorRequest(h, "GET", "/api/monitors/feed?filter=high&limit=1", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"high":1`) {
		t.Fatalf("high=%d %s", w.Code, w.Body.String())
	}
	w = monitorRequest(h, "PATCH", "/api/monitors/items/"+item.ID, `{"dismissed":true}`)
	if w.Code != 200 {
		t.Fatalf("dismiss=%d %s", w.Code, w.Body.String())
	}
	w = monitorRequest(h, "GET", "/api/monitors/feed?task_id="+task.ID, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"items":[]`) {
		t.Fatalf("dismissed feed=%d %s", w.Code, w.Body.String())
	}
}
func TestMonitorHTTPEveryRouteFailure(t *testing.T) {
	h, _, _, db := monitorFixture(t)
	for _, tc := range []struct {
		method, path, body string
		status             int
		code               string
	}{
		{"POST", "/api/monitors", `{"platform":"channels","keyword":"海鲜"}`, 400, "invalid_monitor"},
		{"POST", "/api/monitors", `{"platform":"xhs","keyword":"海鲜","pages":0}`, 400, "invalid_monitor"},
		{"POST", "/api/monitors", `{"platform":"xhs","keyword":"海鲜","id":"bad"}`, 400, "invalid_monitor"},
		{"GET", "/api/monitors/missing", "", 404, "monitor_not_found"},
		{"PATCH", "/api/monitors/missing", `{"enabled":false}`, 404, "monitor_not_found"},
		{"DELETE", "/api/monitors/missing", "", 404, "monitor_not_found"},
		{"POST", "/api/monitors/missing/run", "", 404, "monitor_not_found"},
		{"GET", "/api/monitors/missing/runs", "", 404, "monitor_not_found"},
		{"GET", "/api/monitors/feed?filter=bad", "", 400, "invalid_monitor"},
		{"GET", "/api/monitors/feed?limit=bad", "", 400, "invalid_monitor"},
		{"PATCH", "/api/monitors/items/missing", `{"read":true}`, 404, "monitor_item_not_found"},
		{"PATCH", "/api/monitors/items/missing", `{}`, 400, "invalid_monitor"},
		{"POST", "/api/monitors/items/read-all", `{"task_id":"missing"}`, 404, "monitor_not_found"},
		{"POST", "/api/monitors/items/missing/collect", `{"transcribe":false}`, 404, "monitor_item_not_found"},
		{"POST", "/api/monitors/items/missing/score", "", 404, "monitor_item_not_found"},
	} {
		w := monitorRequest(h, tc.method, tc.path, tc.body)
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.code) {
			t.Errorf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if w := monitorRequest(h, "GET", "/api/monitors", ""); w.Code != 500 || strings.Contains(w.Body.String(), "database") {
		t.Fatalf("list failure=%d %s", w.Code, w.Body.String())
	}
}
