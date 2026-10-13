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

func TestReferenceAnalysisRoutesAndStartupResume(t *testing.T) {
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	settings := consoleSettings.NewService(store.NewSettingsRepository(db), nil)
	if err := settings.InitializeBootSettings(t.Context(), consoleSettings.BootSettings{DataRoot: root, ListenAddr: "127.0.0.1:2030"}); err != nil {
		t.Fatal(err)
	}
	refs := store.NewReferenceRepository(db)
	if _, err := refs.Upsert(t.Context(), store.ReferencePost{ID: "ref", Platform: "douyin", ExternalID: "ref"}); err != nil {
		t.Fatal(err)
	}
	repo := store.NewReferenceAnalysisRepository(db)
	if _, _, err := repo.CreateQueued(t.Context(), store.ReferenceAnalysis{ID: "stale", ReferenceID: "ref"}); err != nil {
		t.Fatal(err)
	}
	application := New(Options{DB: db, Config: config.Config{DataRoot: root}, Settings: settings})
	defer application.Close()
	for _, tc := range []struct {
		method, path, body string
		status             int
		contains           string
	}{
		{"GET", "/api/reference-analyses?reference_id=ref", "", 200, "stale"},
		{"GET", "/api/reference-analyses/stale", "", 200, "控制台重启，分析中断，请重新分析"},
		{"POST", "/api/reference-analyses", `{"reference_id":"ref"}`, 409, "reference_media_not_ready"},
	} {
		w := httptest.NewRecorder()
		application.Handler().ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.contains) {
			t.Errorf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
}
