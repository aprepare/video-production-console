package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"video-production-console/internal/codex"
	"video-production-console/internal/config"
	"video-production-console/internal/domain"
	"video-production-console/internal/store"
)

type routeTestScheduler struct{}

func (*routeTestScheduler) Enqueue(context.Context, domain.CodexTask) error { return nil }
func (*routeTestScheduler) Resume(context.Context, string, string) error    { return nil }
func (*routeTestScheduler) Cancel(context.Context, string) error            { return nil }
func (*routeTestScheduler) SetLimit(int) error                              { return nil }
func (*routeTestScheduler) Snapshot() codex.SchedulerSnapshot               { return codex.SchedulerSnapshot{} }
func (*routeTestScheduler) Close()                                          {}

func TestHealth(t *testing.T) {
	application := New(Options{})
	request := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	response := httptest.NewRecorder()

	application.Handler().ServeHTTP(response, request)

	result := response.Result()
	defer result.Body.Close()
	body, err := io.ReadAll(result.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	if result.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", result.StatusCode, http.StatusOK)
	}
	if got, want := string(body), "{\"status\":\"ok\"}\n"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestEmbeddedWebConsole(t *testing.T) {
	response := httptest.NewRecorder()
	New(Options{}).Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d", response.Code)
	}
	if !strings.Contains(response.Body.String(), `<div id="root"></div>`) {
		t.Fatal("embedded React root is missing")
	}
}

func TestAccountRoutesAreMounted(t *testing.T) {
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "console.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	application := New(Options{Config: config.Config{DataRoot: root}, DB: db})
	request := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	response := httptest.NewRecorder()

	application.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	if got, want := response.Body.String(), "[]\n"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestProjectRoutesAreMounted(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	application := New(Options{DB: database, Config: config.Config{DataRoot: t.TempDir()}})
	request := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	response := httptest.NewRecorder()
	application.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("GET /api/projects status = %d", response.Code)
	}
}

func TestProjectTopicCardRouteIsMountedOnTaskHandler(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	application := New(Options{
		DB:        database,
		Config:    config.Config{DataRoot: t.TempDir()},
		Scheduler: &routeTestScheduler{},
	})
	request := httptest.NewRequest(http.MethodPost, "/api/projects/00000000-0000-0000-0000-000000000001/topic-card", nil)
	response := httptest.NewRecorder()

	application.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusNotFound, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"code":"project_not_found"`) {
		t.Fatalf("topic-card route did not reach task handler; body = %q", response.Body.String())
	}
}
