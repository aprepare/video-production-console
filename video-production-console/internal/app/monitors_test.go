package app

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"video-production-console/internal/config"
	"video-production-console/internal/monitor"
	consoleSettings "video-production-console/internal/settings"
	"video-production-console/internal/store"
)

func TestMonitorAndInsightWiringResumeAndBackground(t *testing.T) {
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	settings := consoleSettings.NewService(store.NewSettingsRepository(db), nil)
	if err := settings.InitializeBootSettings(t.Context(), consoleSettings.BootSettings{DataRoot: root, ListenAddr: "127.0.0.1:2030"}); err != nil {
		t.Fatal(err)
	}
	refs := store.NewReferenceRepository(db)
	if _, err := refs.Upsert(t.Context(), store.ReferencePost{ID: "ref", Platform: "douyin", ExternalID: "post"}); err != nil {
		t.Fatal(err)
	}
	insights := store.NewCommentInsightRepository(db)
	if _, _, err := insights.CreateQueued(t.Context(), store.CommentInsight{ID: "insight", ReferenceID: "ref"}); err != nil {
		t.Fatal(err)
	}
	repo := store.NewMonitorRepository(db)
	task := monitor.DefaultTask()
	task.ID = "task"
	task.Platform = "douyin"
	task.Keyword = "海鲜"
	task.Enabled = false
	task.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	task.UpdatedAt = task.CreatedAt
	task.NextRunAt = task.CreatedAt
	if _, err := repo.CreateTask(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateRun(t.Context(), store.MonitorRun{ID: "stale", TaskID: task.ID, Status: "running", Trigger: "manual", StartedAt: task.CreatedAt}); err != nil {
		t.Fatal(err)
	}
	a := New(Options{DB: db, Config: config.Config{DataRoot: root}, Settings: settings})
	defer a.Close()
	for _, tc := range []struct {
		method, path, body string
		status             int
		contains           string
	}{
		{"GET", "/api/monitors", "", 200, "task"},
		{"GET", "/api/monitors/task", "", 200, "控制台重启中断"},
		{"GET", "/api/comment-insights?reference_id=ref", "", 200, "insight"},
		{"GET", "/api/comment-insights/insight", "", 200, `"status":"failed"`},
		{"POST", "/api/monitors/task/run", "", 202, `"running":true`},
	} {
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.contains) {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	a.RunBackground(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for {
		current, err := repo.GetTask(t.Context(), "task")
		if err != nil {
			t.Fatal(err)
		}
		if !current.Running {
			if current.LastStatus != "failed" || !strings.Contains(current.LastError, "网关") {
				t.Fatalf("background not using monitor: %+v", current)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background monitor never processed queued run")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
