package app

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"video-production-console/internal/config"
	consoleSettings "video-production-console/internal/settings"
	"video-production-console/internal/store"
)

func TestVideoQCWiringAndRestartRecovery(t *testing.T) {
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	settings := consoleSettings.NewService(store.NewSettingsRepository(db), nil)
	if err := settings.InitializeBootSettings(t.Context(), consoleSettings.BootSettings{DataRoot: root, ListenAddr: "127.0.0.1:12030"}); err != nil {
		t.Fatal(err)
	}
	repo := store.NewVideoQCRepository(db)
	if _, err := repo.CreateQueued(t.Context(), store.VideoQCCheck{ID: "stale", Source: "upload"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkRunning(t.Context(), "stale"); err != nil {
		t.Fatal(err)
	}
	a := New(Options{DB: db, Config: config.Config{DataRoot: root}, Settings: settings})
	defer a.Close()
	for _, tc := range []struct {
		method, path, body string
		status             int
		contains           string
	}{
		{"GET", "/api/video-qc", "", 200, "stale"},
		{"GET", "/api/video-qc/stale", "", 200, "控制台重启中断"},
		{"GET", "/api/video-qc/missing", "", 404, "video_qc_not_found"},
		{"POST", "/api/video-qc", `{"source":"reference","source_id":"ref"}`, 409, "video_qc_not_configured"},
		{"DELETE", "/api/video-qc/stale", "", 204, ""},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		r.Header.Set("Content-Type", "application/json")
		a.Handler().ServeHTTP(w, r)
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.contains) {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
}
