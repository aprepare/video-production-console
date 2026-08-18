package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"video-production-console/internal/baokuan"
)

func TestLibraryProxyAndDependencyOffline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/channels/library/materials/search" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		fmt.Fprint(w, `{"materials":[{"id":"1"}]}`)
	}))
	defer srv.Close()
	h := NewDependenciesHandler(baokuan.NewClient(srv.URL), "missing-codex", "")
	req := httptest.NewRequest(http.MethodGet, "/api/library/materials/search?limit=100", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	off := NewDependenciesHandler(baokuan.NewClient("http://127.0.0.1:1"), "missing-codex", "")
	rec = httptest.NewRecorder()
	off.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/dependencies", nil))
	if rec.Code != 200 {
		t.Fatalf("dependency status=%d", rec.Code)
	}
}
