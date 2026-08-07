package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	consoleauth "video-production-console/internal/auth"
	"video-production-console/internal/codex"
	"video-production-console/internal/config"
	"video-production-console/internal/domain"
	"video-production-console/internal/store"
	"video-production-console/internal/workflow"
)

type appRemixStarter struct {
	called bool
	run    domain.ProjectWorkflowRun
}

func (s *appRemixStarter) Start(_ context.Context, _ workflow.StartRemix) (domain.ProjectWorkflowRun, error) {
	s.called = true
	return s.run, nil
}

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

func TestProjectRemixRouteUsesInjectedCoordinator(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	accountID, projectID := uuid.NewString(), uuid.NewString()
	if _, err := database.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,'#fff','active',?,?)`, accountID, "account", now, now); err != nil {
		t.Fatal(err)
	}
	if err := store.NewProjectRepository(database).CreateProject(context.Background(), domain.Project{ID: projectID, AccountID: accountID, Title: "project", Stage: domain.StageScript, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	starter := &appRemixStarter{run: domain.ProjectWorkflowRun{ID: uuid.NewString(), ProjectID: projectID, AccountID: accountID, Kind: domain.WorkflowRemix, State: domain.WorkflowRunning, CurrentStep: domain.WorkflowStepTopicCard, ModelName: "gpt-5.4", ReasoningEffort: "high", CreatedAt: now, UpdatedAt: now}}
	application := New(Options{DB: database, Config: config.Config{DataRoot: t.TempDir()}, RemixCoordinator: starter})
	response := httptest.NewRecorder()
	application.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/remix", nil))
	if response.Code != http.StatusOK || !starter.called {
		t.Fatalf("status=%d called=%t body=%s", response.Code, starter.called, response.Body.String())
	}
}

func TestProjectRemixRouteRequiresAuthentication(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	auth := consoleauth.NewService(store.NewAuthStore(database), consoleauth.Options{})
	if err := auth.Bootstrap(context.Background(), "123321"); err != nil {
		t.Fatal(err)
	}
	starter := &appRemixStarter{}
	application := New(Options{DB: database, Config: config.Config{DataRoot: t.TempDir()}, AuthService: auth, RemixCoordinator: starter})
	response := httptest.NewRecorder()
	application.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/projects/"+uuid.NewString()+"/remix", nil))
	if response.Code != http.StatusUnauthorized || starter.called {
		t.Fatalf("status=%d called=%t body=%s", response.Code, starter.called, response.Body.String())
	}
}

func TestProjectRemixAndPublishRoutesAcceptAuthenticatedCSRFRequest(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	auth := consoleauth.NewService(store.NewAuthStore(database), consoleauth.Options{})
	if err := auth.Bootstrap(context.Background(), "123321"); err != nil {
		t.Fatal(err)
	}
	login, err := auth.Login(context.Background(), "123321", "127.0.0.1:1234")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	accountID, projectID := uuid.NewString(), uuid.NewString()
	_, _ = database.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,'#fff','active',?,?)`, accountID, "account", now, now)
	_, _ = database.Exec(`INSERT INTO projects(id,account_id,title,stage,publication_status,created_at,updated_at) VALUES(?,?,'project','review','producing',?,?)`, projectID, accountID, now, now)
	if _, err := store.NewAssetRepository(database).AddVersion(context.Background(), store.AddAssetVersion{ProjectID: &projectID, Type: domain.AssetFinalVideo, Path: "final.mp4", Filename: "final.mp4", MIMEType: "video/mp4", SHA256: "sha"}); err != nil {
		t.Fatal(err)
	}
	starter := &appRemixStarter{run: domain.ProjectWorkflowRun{ID: uuid.NewString(), ProjectID: projectID, AccountID: accountID, Kind: domain.WorkflowRemix, State: domain.WorkflowRunning, CurrentStep: domain.WorkflowStepTopicCard, ModelName: "gpt-5.4", ReasoningEffort: "high", CreatedAt: now, UpdatedAt: now}}
	application := New(Options{DB: database, Config: config.Config{DataRoot: t.TempDir()}, AuthService: auth, RemixCoordinator: starter})
	for _, path := range []string{"/api/projects/" + projectID + "/remix", "/api/projects/" + projectID + "/publish"} {
		request := httptest.NewRequest(http.MethodPost, path, nil)
		request.AddCookie(&http.Cookie{Name: consoleauth.SessionCookieName, Value: login.Token})
		request.AddCookie(&http.Cookie{Name: consoleauth.CSRFCookieName, Value: login.CSRF})
		request.Header.Set(consoleauth.CSRFHeader, login.CSRF)
		response := httptest.NewRecorder()
		application.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
}
