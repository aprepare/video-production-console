package app

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"video-production-console/internal/config"
	consoleSettings "video-production-console/internal/settings"
	"video-production-console/internal/store"
)

func TestRetiredFeatureAPIsCannotReadWriteOrGenerate(t *testing.T) {
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	settings := consoleSettings.NewService(store.NewSettingsRepository(db), nil, consoleSettings.Options{})
	application := New(Options{DB: db, Config: config.Config{DataRoot: root}, Settings: settings})
	defer application.Close()
	for _, path := range []string{"/api/workspace", "/api/workspace/tree", "/api/workspace/file", "/api/ai-shorts", "/api/ai-shorts/styles", "/api/ai-shorts/media-settings", "/api/ai-shorts/retired/generate"} {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete} {
			t.Run(method+" "+path, func(t *testing.T) {
				response := httptest.NewRecorder()
				application.Handler().ServeHTTP(response, httptest.NewRequest(method, path, strings.NewReader("{}")))
				if response.Code != http.StatusGone || !strings.Contains(response.Body.String(), `"code":"feature_removed"`) {
					t.Fatalf("status=%d body=%.200s, want removed feature response", response.Code, response.Body.String())
				}
			})
		}
	}
}
