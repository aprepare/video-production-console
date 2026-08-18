package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeCatalogService records calls and returns scripted answers so the handler
// contract can be pinned without the real repository.
type fakeCatalogService struct {
	status       CatalogStatus
	statusErr    error
	page         CatalogSourcesPage
	sourcesErr   error
	lastFilter   CatalogSourceFilter
	indexErr     error
	indexCalls   int
	retryID      string
	retryErr     error
	providers    CatalogProviders
	providersErr error
	searchPage   CatalogSearchPage
	searchErr    error
	lastSearch   CatalogSearchQuery
	importResult CatalogImportResult
	importErr    error
	lastImport   CatalogImportRequest
}

func (f *fakeCatalogService) Status(context.Context) (CatalogStatus, error) {
	return f.status, f.statusErr
}

func (f *fakeCatalogService) Sources(_ context.Context, filter CatalogSourceFilter) (CatalogSourcesPage, error) {
	f.lastFilter = filter
	return f.page, f.sourcesErr
}

func (f *fakeCatalogService) StartIndex(context.Context) (CatalogStatus, error) {
	f.indexCalls++
	return f.status, f.indexErr
}

func (f *fakeCatalogService) RetrySource(_ context.Context, id string) (CatalogStatus, error) {
	f.retryID = id
	return f.status, f.retryErr
}

func (f *fakeCatalogService) Providers(context.Context) (CatalogProviders, error) {
	return f.providers, f.providersErr
}

func (f *fakeCatalogService) Search(_ context.Context, query CatalogSearchQuery) (CatalogSearchPage, error) {
	f.lastSearch = query
	return f.searchPage, f.searchErr
}

func (f *fakeCatalogService) Import(_ context.Context, request CatalogImportRequest) (CatalogImportResult, error) {
	f.lastImport = request
	return f.importResult, f.importErr
}

func catalogRequest(t *testing.T, handler http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(method, target, nil))
	return response
}

func TestMediaCatalogStatusReturnsPlannedContract(t *testing.T) {
	service := &fakeCatalogService{status: CatalogStatus{
		State:  "analyzing",
		Counts: CatalogCounts{Sources: 12, Shots: 328, ReadyShots: 310, FailedShots: 18},
		ActiveJob: &CatalogActiveJob{
			ID: "job-1", Phase: "analyzing", CompletedUnits: 61, TotalUnits: 100,
		},
		Warnings: []CatalogWarning{{Code: "rights_unknown", Count: 2}},
	}}
	response := catalogRequest(t, NewMediaCatalogHandler(service), http.MethodGet, "/api/media-catalog/status")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload struct {
		State  string `json:"state"`
		Counts struct {
			Sources     int `json:"sources"`
			Shots       int `json:"shots"`
			ReadyShots  int `json:"ready_shots"`
			FailedShots int `json:"failed_shots"`
		} `json:"counts"`
		ActiveJob *struct {
			ID             string `json:"id"`
			Phase          string `json:"phase"`
			CompletedUnits int    `json:"completed_units"`
			TotalUnits     int    `json:"total_units"`
		} `json:"active_job"`
		Warnings []struct {
			Code  string `json:"code"`
			Count int    `json:"count"`
		} `json:"warnings"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if payload.State != "analyzing" || payload.Counts.ReadyShots != 310 || payload.Counts.FailedShots != 18 {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	if payload.ActiveJob == nil || payload.ActiveJob.Phase != "analyzing" || payload.ActiveJob.CompletedUnits != 61 {
		t.Fatalf("unexpected active job: %+v", payload.ActiveJob)
	}
	if len(payload.Warnings) != 1 || payload.Warnings[0].Code != "rights_unknown" {
		t.Fatalf("unexpected warnings: %+v", payload.Warnings)
	}
}

func TestMediaCatalogStatusAlwaysSerializesWarningsArray(t *testing.T) {
	service := &fakeCatalogService{status: CatalogStatus{State: "idle"}}
	response := catalogRequest(t, NewMediaCatalogHandler(service), http.MethodGet, "/api/media-catalog/status")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), `"warnings":[]`) {
		t.Fatalf("warnings must serialize as an empty array; body = %s", response.Body.String())
	}
}

func TestMediaCatalogNotConfiguredDoesNotEchoPaths(t *testing.T) {
	service := &fakeCatalogService{
		statusErr: fmt.Errorf("%w: open C:\\secret\\media\\catalog.db api_key=sk-test", ErrCatalogNotConfigured),
	}
	response := catalogRequest(t, NewMediaCatalogHandler(service), http.MethodGet, "/api/media-catalog/status")
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	body := response.Body.String()
	if !strings.Contains(body, `"code":"catalog_not_configured"`) {
		t.Fatalf("body = %s, want catalog_not_configured", body)
	}
	for _, leak := range []string{"C:\\", "secret", "api_key", "sk-test"} {
		if strings.Contains(body, leak) {
			t.Fatalf("response leaks %q; body = %s", leak, body)
		}
	}
}

func TestMediaCatalogInternalErrorDoesNotEchoDetails(t *testing.T) {
	service := &fakeCatalogService{
		statusErr: fmt.Errorf("walk originals directory %q failed", "C:\\media\\movies"),
	}
	response := catalogRequest(t, NewMediaCatalogHandler(service), http.MethodGet, "/api/media-catalog/status")
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	if strings.Contains(response.Body.String(), "C:\\") {
		t.Fatalf("internal error must not echo absolute paths; body = %s", response.Body.String())
	}
}

func TestMediaCatalogSourcesPassesFiltersThrough(t *testing.T) {
	service := &fakeCatalogService{page: CatalogSourcesPage{Sources: []CatalogSource{{
		ID: "source-1", Kind: "movie", Subtype: "video", Origin: "local",
		RelativePath: "originals/movies/a.mp4", Status: "failed", ErrorCode: "probe_failed",
	}}}}
	response := catalogRequest(t, NewMediaCatalogHandler(service), http.MethodGet,
		"/api/media-catalog/sources?kind=movie&status=failed&cursor=abc")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if service.lastFilter != (CatalogSourceFilter{Kind: "movie", Status: "failed", Cursor: "abc"}) {
		t.Fatalf("filter = %+v", service.lastFilter)
	}
	if !strings.Contains(response.Body.String(), `"relative_path":"originals/movies/a.mp4"`) {
		t.Fatalf("body = %s", response.Body.String())
	}
}

func TestMediaCatalogSourcesRejectsUnknownFilterValues(t *testing.T) {
	for _, target := range []string{
		"/api/media-catalog/sources?kind=bogus",
		"/api/media-catalog/sources?status=bogus",
	} {
		service := &fakeCatalogService{}
		response := catalogRequest(t, NewMediaCatalogHandler(service), http.MethodGet, target)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want %d", target, response.Code, http.StatusBadRequest)
		}
		if !strings.Contains(response.Body.String(), `"code":"catalog_filter_invalid"`) {
			t.Fatalf("%s body = %s", target, response.Body.String())
		}
	}
}

func TestMediaCatalogSourcesAlwaysSerializesArray(t *testing.T) {
	service := &fakeCatalogService{}
	response := catalogRequest(t, NewMediaCatalogHandler(service), http.MethodGet, "/api/media-catalog/sources")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), `"sources":[]`) {
		t.Fatalf("sources must serialize as an empty array; body = %s", response.Body.String())
	}
}

func TestMediaCatalogIndexStartsAndReportsAccepted(t *testing.T) {
	service := &fakeCatalogService{status: CatalogStatus{
		State:     "scanning",
		ActiveJob: &CatalogActiveJob{ID: "job-1", Phase: "scanning"},
	}}
	response := catalogRequest(t, NewMediaCatalogHandler(service), http.MethodPost, "/api/media-catalog/index")
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if service.indexCalls != 1 {
		t.Fatalf("indexCalls = %d", service.indexCalls)
	}
	if !strings.Contains(response.Body.String(), `"state":"scanning"`) {
		t.Fatalf("body = %s", response.Body.String())
	}
}

func TestMediaCatalogIndexConflictWhenJobActive(t *testing.T) {
	service := &fakeCatalogService{indexErr: ErrCatalogJobActive}
	response := catalogRequest(t, NewMediaCatalogHandler(service), http.MethodPost, "/api/media-catalog/index")
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusConflict)
	}
	if !strings.Contains(response.Body.String(), `"code":"catalog_job_active"`) {
		t.Fatalf("body = %s", response.Body.String())
	}
}

func TestMediaCatalogRetryPassesSourceID(t *testing.T) {
	service := &fakeCatalogService{status: CatalogStatus{State: "ready"}}
	response := catalogRequest(t, NewMediaCatalogHandler(service), http.MethodPost, "/api/media-catalog/sources/source-9/retry")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if service.retryID != "source-9" {
		t.Fatalf("retryID = %q", service.retryID)
	}
}

func TestMediaCatalogRetryUnknownSourceIsNotFound(t *testing.T) {
	service := &fakeCatalogService{retryErr: ErrCatalogSourceNotFound}
	response := catalogRequest(t, NewMediaCatalogHandler(service), http.MethodPost, "/api/media-catalog/sources/missing/retry")
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
	if !strings.Contains(response.Body.String(), `"code":"catalog_source_not_found"`) {
		t.Fatalf("body = %s", response.Body.String())
	}
}

func TestMediaCatalogProvidersListsAvailability(t *testing.T) {
	service := &fakeCatalogService{providers: CatalogProviders{Providers: []CatalogProviderStatus{
		{Name: "pexels", Configured: true},
		{Name: "pixabay", Configured: false},
	}}}
	response := catalogRequest(t, NewMediaCatalogHandler(service), http.MethodGet, "/api/media-catalog/providers")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"name":"pexels"`) || !strings.Contains(response.Body.String(), `"configured":false`) {
		t.Fatalf("body = %s", response.Body.String())
	}
}

func TestMediaCatalogProvidersAlwaysSerializesArray(t *testing.T) {
	response := catalogRequest(t, NewMediaCatalogHandler(&fakeCatalogService{}), http.MethodGet, "/api/media-catalog/providers")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), `"providers":[]`) {
		t.Fatalf("providers must serialize as an empty array; body = %s", response.Body.String())
	}
}

func TestMediaCatalogSearchPassesQueryThrough(t *testing.T) {
	service := &fakeCatalogService{searchPage: CatalogSearchPage{Assets: []CatalogRemoteAsset{{
		Provider: "pexels", ID: "101", Kind: "image", Creator: "Ada", LicenseCode: "pexels",
	}}}}
	response := catalogRequest(t, NewMediaCatalogHandler(service), http.MethodGet,
		"/api/media-catalog/search?provider=pexels&q=lake&limit=8")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if service.lastSearch != (CatalogSearchQuery{Provider: "pexels", Query: "lake", Limit: 8}) {
		t.Fatalf("search = %+v", service.lastSearch)
	}
	if !strings.Contains(response.Body.String(), `"creator":"Ada"`) {
		t.Fatalf("body = %s", response.Body.String())
	}
}

func TestMediaCatalogSearchRejectsBadLimit(t *testing.T) {
	response := catalogRequest(t, NewMediaCatalogHandler(&fakeCatalogService{}), http.MethodGet,
		"/api/media-catalog/search?provider=pexels&q=lake&limit=-1")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	if !strings.Contains(response.Body.String(), `"code":"catalog_search_invalid"`) {
		t.Fatalf("body = %s", response.Body.String())
	}
}

func TestMediaCatalogSearchUnconfiguredDoesNotEchoSecrets(t *testing.T) {
	service := &fakeCatalogService{
		searchErr: fmt.Errorf("%w: missing key sk-live-secret C:\\keys\\pexels", ErrProviderNotConfigured),
	}
	response := catalogRequest(t, NewMediaCatalogHandler(service), http.MethodGet,
		"/api/media-catalog/search?provider=pexels&q=lake")
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	body := response.Body.String()
	if !strings.Contains(body, `"code":"provider_not_configured"`) {
		t.Fatalf("body = %s", body)
	}
	for _, leak := range []string{"sk-live-secret", "C:\\", "keys"} {
		if strings.Contains(body, leak) {
			t.Fatalf("response leaks %q; body = %s", leak, body)
		}
	}
}

func TestMediaCatalogImportRemoteJSON(t *testing.T) {
	service := &fakeCatalogService{importResult: CatalogImportResult{
		Source:         CatalogSource{ID: "src-1", Kind: "image", Origin: "pexels", RelativePath: "originals/images/a.png", Status: "ready"},
		Created:        true,
		Publishability: "local_draft_only",
	}}
	body := `{"source":"remote","provider":"pexels","id":"101","kind":"image","download_url":"https://images.pexels.com/a.jpg","page_url":"https://www.pexels.com/photo/101/","creator":"Ada","license_code":"pexels","license_url":"https://www.pexels.com/license/"}`
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/media-catalog/import", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	NewMediaCatalogHandler(service).ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if service.lastImport.Source != "remote" || service.lastImport.Remote.ID != "101" || service.lastImport.Remote.Creator != "Ada" {
		t.Fatalf("import = %+v", service.lastImport)
	}
	if !strings.Contains(response.Body.String(), `"publishability":"local_draft_only"`) {
		t.Fatalf("body = %s", response.Body.String())
	}
}

func TestMediaCatalogImportInvalidDoesNotEchoPaths(t *testing.T) {
	service := &fakeCatalogService{
		importErr: fmt.Errorf("%w: open C:\\secret\\media\\file.png key=sk-test", ErrCatalogImportInvalid),
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/media-catalog/import", strings.NewReader(`{"source":"remote","provider":"pexels"}`))
	request.Header.Set("Content-Type", "application/json")
	NewMediaCatalogHandler(service).ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	body := response.Body.String()
	if !strings.Contains(body, `"code":"catalog_import_invalid"`) {
		t.Fatalf("body = %s", body)
	}
	for _, leak := range []string{"C:\\", "secret", "sk-test"} {
		if strings.Contains(body, leak) {
			t.Fatalf("response leaks %q; body = %s", leak, body)
		}
	}
}

func TestMediaCatalogRejectsWrongMethods(t *testing.T) {
	service := &fakeCatalogService{}
	handler := NewMediaCatalogHandler(service)
	for target, method := range map[string]string{
		"/api/media-catalog/status": http.MethodPost,
		"/api/media-catalog/index":  http.MethodGet,
	} {
		response := catalogRequest(t, handler, method, target)
		if response.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s %s status = %d, want %d", method, target, response.Code, http.StatusMethodNotAllowed)
		}
	}
}
