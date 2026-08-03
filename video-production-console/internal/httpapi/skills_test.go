package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"video-production-console/internal/domain"
	"video-production-console/internal/skillregistry"
	"video-production-console/internal/store"
)

func TestSkillsHTTPScansListsAndGetsPersistedSnapshots(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	root := filepath.Join(t.TempDir(), "finance-topic-selector")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("skill body"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry := skillregistry.NewService(store.NewSkillRepository(db), skillregistry.Options{Roots: []skillregistry.Root{{Name: "finance-topic-selector", Path: root}}})
	handler := NewSkillsHandler(registry)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/skills/scan", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("scan status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var scanned []domain.SkillSnapshot
	if err := json.Unmarshal(recorder.Body.Bytes(), &scanned); err != nil {
		t.Fatal(err)
	}
	if len(scanned) != 1 || scanned[0].Name != "finance-topic-selector" || scanned[0].SHA256 == "" || len(scanned[0].Files) != 1 {
		t.Fatalf("scanned=%+v", scanned)
	}

	for _, path := range []string{"/api/skills", "/api/skills/finance-topic-selector"} {
		recorder = httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s status=%d body=%s", path, recorder.Code, recorder.Body.String())
		}
		if !json.Valid(recorder.Body.Bytes()) {
			t.Fatalf("GET %s invalid JSON: %s", path, recorder.Body.String())
		}
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/skills/not-installed", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("missing skill status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
