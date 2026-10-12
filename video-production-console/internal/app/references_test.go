package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"video-production-console/internal/config"
	consoleSettings "video-production-console/internal/settings"
	"video-production-console/internal/store"
)

func TestReferenceRoutesReportMissingGatewayConfiguration(t *testing.T) {
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	settings := consoleSettings.NewService(store.NewSettingsRepository(db), nil, consoleSettings.Options{})
	if err := settings.InitializeBootSettings(t.Context(), consoleSettings.BootSettings{DataRoot: root, ListenAddr: "127.0.0.1:2030"}); err != nil {
		t.Fatal(err)
	}
	application := New(Options{DB: db, Config: config.Config{DataRoot: root}, Settings: settings})
	defer application.Close()
	serve := func(method, path, body string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		application.Handler().ServeHTTP(response, httptest.NewRequest(method, path, strings.NewReader(body)))
		return response
	}

	status := serve(http.MethodGet, "/api/references/status", "")
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"configured":false`) {
		t.Fatalf("status=%d body=%s", status.Code, status.Body.String())
	}
	list := serve(http.MethodGet, "/api/references", "")
	var posts []map[string]any
	if list.Code != http.StatusOK || json.Unmarshal(list.Body.Bytes(), &posts) != nil || len(posts) != 0 {
		t.Fatalf("list=%d body=%s", list.Code, list.Body.String())
	}
	collect := serve(http.MethodPost, "/api/references", `{"link":"https://v.douyin.com/x/","transcribe":true}`)
	if collect.Code != http.StatusConflict || !strings.Contains(collect.Body.String(), "platform_api_not_configured") {
		t.Fatalf("collect=%d body=%s", collect.Code, collect.Body.String())
	}
	search := serve(http.MethodGet, "/api/references/search?platform=channels&keyword=茶", "")
	if search.Code != http.StatusBadRequest {
		t.Fatalf("channels search must be rejected, got %d", search.Code)
	}
}
