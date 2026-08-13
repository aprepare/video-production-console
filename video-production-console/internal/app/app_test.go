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
	"video-production-console/internal/httpapi"
	"video-production-console/internal/logging"
	consoleSettings "video-production-console/internal/settings"
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

func TestRequestIDMiddlewareIsMountedOnEveryRoute(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	auth := consoleauth.NewService(store.NewAuthStore(database), consoleauth.Options{})
	if err := auth.Bootstrap(context.Background(), "123321"); err != nil {
		t.Fatal(err)
	}
	for name, application := range map[string]*App{
		"unauthenticated": New(Options{}),
		"protected":       New(Options{DB: database, Config: config.Config{DataRoot: t.TempDir()}, AuthService: auth}),
	} {
		response := httptest.NewRecorder()
		application.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/accounts", nil))
		if response.Header().Get(logging.RequestIDHeader) == "" {
			t.Fatalf("%s: response is missing the %s header", name, logging.RequestIDHeader)
		}

		echoed := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/health", nil)
		request.Header.Set(logging.RequestIDHeader, "upstream-7")
		application.Handler().ServeHTTP(echoed, request)
		if got := echoed.Header().Get(logging.RequestIDHeader); got != "upstream-7" {
			t.Fatalf("%s: %s = %q, want upstream-7", name, logging.RequestIDHeader, got)
		}
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

func TestStandaloneChatAndHistoryRoutesAreNotMounted(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	application := New(Options{Config: config.Config{DataRoot: t.TempDir()}, DB: database})

	for _, path := range []string{"/api/chat/sessions", "/api/codex/history"} {
		response := httptest.NewRecorder()
		application.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `<div id="root"></div>`) {
			t.Fatalf("GET %s should fall through to the embedded console; status=%d body=%q", path, response.Code, response.Body.String())
		}
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

func TestManualNarrationAssetUploadStaysOnProjectAssetHandler(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	application := New(Options{DB: database, Config: config.Config{DataRoot: t.TempDir()}})
	request := httptest.NewRequest(http.MethodPost, "/api/projects/00000000-0000-0000-0000-000000000001/assets/narration", nil)
	response := httptest.NewRecorder()

	application.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusNotFound, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"code":"project_not_found"`) {
		t.Fatalf("manual narration upload did not reach project asset handler; body = %q", response.Body.String())
	}
}

func TestAutomaticNarrationGenerationStaysOnNarrationHandler(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	application := New(Options{DB: database, Config: config.Config{DataRoot: t.TempDir()}})
	request := httptest.NewRequest(http.MethodPost, "/api/projects/00000000-0000-0000-0000-000000000001/narration", nil)
	response := httptest.NewRecorder()

	application.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusNotFound, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"code":"project_not_found"`) {
		t.Fatalf("automatic narration generation did not reach narration handler; body = %q", response.Body.String())
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

func TestImageProjectRoutesRequireAuthentication(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	auth := consoleauth.NewService(store.NewAuthStore(database), consoleauth.Options{})
	if err := auth.Bootstrap(context.Background(), "123321"); err != nil {
		t.Fatal(err)
	}
	settings := consoleSettings.NewService(store.NewSettingsRepository(database), nil, consoleSettings.Options{})
	application := New(Options{DB: database, Config: config.Config{DataRoot: t.TempDir()}, AuthService: auth, Settings: settings})
	for _, path := range []string{"/api/image-projects", "/api/image-projects/" + uuid.NewString() + "/download"} {
		response := httptest.NewRecorder()
		application.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
}

// conflictCatalogService accepts the first StartIndex and reports every later
// one as an active-job conflict, mirroring the single-global-job rule.
type conflictCatalogService struct{ started int }

func (s *conflictCatalogService) Status(context.Context) (httpapi.CatalogStatus, error) {
	return httpapi.CatalogStatus{State: "idle"}, nil
}

func (s *conflictCatalogService) Sources(context.Context, httpapi.CatalogSourceFilter) (httpapi.CatalogSourcesPage, error) {
	return httpapi.CatalogSourcesPage{}, nil
}

func (s *conflictCatalogService) StartIndex(context.Context) (httpapi.CatalogStatus, error) {
	s.started++
	if s.started > 1 {
		return httpapi.CatalogStatus{}, httpapi.ErrCatalogJobActive
	}
	return httpapi.CatalogStatus{State: "scanning", ActiveJob: &httpapi.CatalogActiveJob{ID: "job-1", Phase: "scanning"}}, nil
}

func (s *conflictCatalogService) RetrySource(context.Context, string) (httpapi.CatalogStatus, error) {
	return httpapi.CatalogStatus{State: "idle"}, nil
}

func (s *conflictCatalogService) Providers(context.Context) (httpapi.CatalogProviders, error) {
	return httpapi.CatalogProviders{Providers: []httpapi.CatalogProviderStatus{}}, nil
}

func (s *conflictCatalogService) Search(context.Context, httpapi.CatalogSearchQuery) (httpapi.CatalogSearchPage, error) {
	return httpapi.CatalogSearchPage{Assets: []httpapi.CatalogRemoteAsset{}}, nil
}

func (s *conflictCatalogService) Import(context.Context, httpapi.CatalogImportRequest) (httpapi.CatalogImportResult, error) {
	return httpapi.CatalogImportResult{}, nil
}

func TestMediaCatalogRoutesAreMountedNextToSettings(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	settings := consoleSettings.NewService(store.NewSettingsRepository(database), nil, consoleSettings.Options{})
	application := New(Options{DB: database, Config: config.Config{DataRoot: t.TempDir()}, Settings: settings})

	response := httptest.NewRecorder()
	application.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/media-catalog/status", nil))

	// Settings exist but no catalog path is configured, so the mounted route
	// must answer with the explicit error code instead of falling through.
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"code":"catalog_not_configured"`) {
		t.Fatalf("body = %q, want catalog_not_configured", response.Body.String())
	}
}

func TestMediaCatalogRoutesAreNotMountedWithoutSettings(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	application := New(Options{DB: database, Config: config.Config{DataRoot: t.TempDir()}})

	response := httptest.NewRecorder()
	application.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/media-catalog/status", nil))

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `<div id="root"></div>`) {
		t.Fatalf("route should fall through to the embedded console; status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestMediaCatalogSecondIndexRequestConflicts(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	settings := consoleSettings.NewService(store.NewSettingsRepository(database), nil, consoleSettings.Options{})
	catalog := &conflictCatalogService{}
	application := New(Options{DB: database, Config: config.Config{DataRoot: t.TempDir()}, Settings: settings, MediaCatalog: catalog})

	first := httptest.NewRecorder()
	application.Handler().ServeHTTP(first, httptest.NewRequest(http.MethodPost, "/api/media-catalog/index", nil))
	if first.Code != http.StatusAccepted {
		t.Fatalf("first status = %d, body = %s", first.Code, first.Body.String())
	}

	second := httptest.NewRecorder()
	application.Handler().ServeHTTP(second, httptest.NewRequest(http.MethodPost, "/api/media-catalog/index", nil))
	if second.Code != http.StatusConflict {
		t.Fatalf("second status = %d, body = %s", second.Code, second.Body.String())
	}
	if !strings.Contains(second.Body.String(), `"code":"catalog_job_active"`) {
		t.Fatalf("second body = %q", second.Body.String())
	}
}

func TestMediaCatalogGetUsesSessionAndPostRequiresCSRF(t *testing.T) {
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
	settings := consoleSettings.NewService(store.NewSettingsRepository(database), nil, consoleSettings.Options{})
	catalog := &conflictCatalogService{}
	application := New(Options{DB: database, Config: config.Config{DataRoot: t.TempDir()}, AuthService: auth, Settings: settings, MediaCatalog: catalog})

	// Without a session every catalog route is rejected.
	anonymous := httptest.NewRecorder()
	application.Handler().ServeHTTP(anonymous, httptest.NewRequest(http.MethodGet, "/api/media-catalog/status", nil))
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d, body = %s", anonymous.Code, anonymous.Body.String())
	}

	// GET needs the session only; the CSRF token is not required.
	read := httptest.NewRequest(http.MethodGet, "/api/media-catalog/status", nil)
	read.AddCookie(&http.Cookie{Name: consoleauth.SessionCookieName, Value: login.Token})
	readResponse := httptest.NewRecorder()
	application.Handler().ServeHTTP(readResponse, read)
	if readResponse.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body = %s", readResponse.Code, readResponse.Body.String())
	}

	// POST without the CSRF header must be rejected by the shared middleware.
	blocked := httptest.NewRequest(http.MethodPost, "/api/media-catalog/index", nil)
	blocked.AddCookie(&http.Cookie{Name: consoleauth.SessionCookieName, Value: login.Token})
	blockedResponse := httptest.NewRecorder()
	application.Handler().ServeHTTP(blockedResponse, blocked)
	if blockedResponse.Code != http.StatusForbidden {
		t.Fatalf("POST without CSRF status = %d, body = %s", blockedResponse.Code, blockedResponse.Body.String())
	}

	// A full session + CSRF pair reaches the handler.
	allowed := httptest.NewRequest(http.MethodPost, "/api/media-catalog/index", nil)
	allowed.AddCookie(&http.Cookie{Name: consoleauth.SessionCookieName, Value: login.Token})
	allowed.AddCookie(&http.Cookie{Name: consoleauth.CSRFCookieName, Value: login.CSRF})
	allowed.Header.Set(consoleauth.CSRFHeader, login.CSRF)
	allowedResponse := httptest.NewRecorder()
	application.Handler().ServeHTTP(allowedResponse, allowed)
	if allowedResponse.Code != http.StatusAccepted {
		t.Fatalf("POST with CSRF status = %d, body = %s", allowedResponse.Code, allowedResponse.Body.String())
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
