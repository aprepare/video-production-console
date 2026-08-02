package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"video-production-console/internal/config"
	"video-production-console/internal/store"
)

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
