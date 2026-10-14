package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"video-production-console/internal/domain"
	"video-production-console/internal/store"
)

func TestTaskCenterDismissHidesOnlyFinishedTasks(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/dismiss.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	created := time.Date(2026, 10, 14, 8, 0, 0, 0, time.UTC)
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('a','A','#fff','active',?,?)`, created, created); err != nil {
		t.Fatal(err)
	}
	ids := map[domain.TaskStatus]string{
		domain.TaskFailed:  "11111111-1111-4111-8111-111111111111",
		domain.TaskRunning: "22222222-2222-4222-8222-222222222222",
	}
	tasks := store.NewTaskRepository(db)
	for status, id := range ids {
		task := domain.CodexTask{ID: id, AccountID: "a", Type: "remix", SkillName: "finance-viral-remix", Action: domain.ActionRemixEnhanced, Status: domain.TaskQueued, PromptSnapshot: "p", CreatedAt: created}
		if err := tasks.CreateV2(context.Background(), task); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE codex_tasks SET status=? WHERE id=?`, status, id); err != nil {
			t.Fatal(err)
		}
	}
	handler := NewTasksHandler(db, nil, nil, nil)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(recorder, req)
		return recorder
	}
	listed := func(path string) map[string]bool {
		recorder := request(http.MethodGet, path, "")
		var views []struct{ ID string }
		if err := json.Unmarshal(recorder.Body.Bytes(), &views); err != nil {
			t.Fatalf("%s: %v %s", path, err, recorder.Body.String())
		}
		out := map[string]bool{}
		for _, view := range views {
			out[view.ID] = true
		}
		return out
	}

	if res := request(http.MethodPost, "/api/tasks/dismiss", `{"ids":["`+ids[domain.TaskRunning]+`"]}`); res.Code != http.StatusConflict {
		t.Fatalf("running task: %d %s", res.Code, res.Body.String())
	}
	if res := request(http.MethodPost, "/api/tasks/dismiss", `{"ids":["not-a-uuid"]}`); res.Code != http.StatusBadRequest {
		t.Fatalf("bad id: %d", res.Code)
	}
	for range 2 {
		if res := request(http.MethodPost, "/api/tasks/dismiss", `{"ids":["`+ids[domain.TaskFailed]+`"]}`); res.Code != http.StatusOK {
			t.Fatalf("dismiss failed task: %d %s", res.Code, res.Body.String())
		}
	}
	center := listed("/api/tasks?center=1")
	if center[ids[domain.TaskFailed]] || !center[ids[domain.TaskRunning]] {
		t.Fatalf("task center = %v", center)
	}
	if all := listed("/api/tasks"); !all[ids[domain.TaskFailed]] || !all[ids[domain.TaskRunning]] {
		t.Fatalf("project listing must keep dismissed tasks: %v", all)
	}
}
