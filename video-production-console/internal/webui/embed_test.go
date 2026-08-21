package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesSPAFallbackForProjectRoutes(t *testing.T) {
	handler := Handler()
	for _, path := range []string{"/", "/projects", "/projects/e8b33417-13bc-4a83-9d7d-7956a4b031de"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s status=%d", path, response.Code)
		}
		if !strings.Contains(response.Body.String(), `<div id="root"></div>`) {
			t.Fatalf("%s missing React root", path)
		}
	}
}

func TestHandlerStillServesStaticAssets(t *testing.T) {
	entries, err := files.ReadDir("dist/assets")
	if err != nil || len(entries) == 0 {
		t.Fatalf("list embedded assets: %v", err)
	}
	var asset string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".js") {
			asset = entry.Name()
			break
		}
	}
	if asset == "" {
		t.Fatal("no embedded javascript asset")
	}
	response := httptest.NewRecorder()
	Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/assets/"+asset, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d", response.Code)
	}
	if response.Body.Len() == 0 {
		t.Fatal("expected asset body")
	}
}

func TestHandlerSPARoutesAndMethods(t *testing.T) {
	h := Handler()
	for _, path := range []string{"/", "/projects", "/projects/e8b33417-13bc-4a83-9d7d-7956a4b031de", "/image-projects", "/image-projects/e8b33417-13bc-4a83-9d7d-7956a4b031de", "/image-videos", "/image-videos/e8b33417-13bc-4a83-9d7d-7956a4b031de"} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			r := httptest.NewRecorder(); h.ServeHTTP(r, httptest.NewRequest(method, path, nil))
			if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `<div id="root"></div>`) { t.Fatalf("%s %s status=%d", method, path, r.Code) }
		}
	}
	r := httptest.NewRecorder(); h.ServeHTTP(r, httptest.NewRequest(http.MethodPost, "/projects", nil)); if r.Code != http.StatusMethodNotAllowed { t.Fatalf("POST status=%d", r.Code) }
}
