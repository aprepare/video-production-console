package app

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"video-production-console/internal/config"
	consoleSettings "video-production-console/internal/settings"
	"video-production-console/internal/store"
)

func TestReplicateWorkbenchWiring(t *testing.T) {
	root := t.TempDir()
	db, e := store.Open(filepath.Join(root, "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	settings := consoleSettings.NewService(store.NewSettingsRepository(db), nil)
	if e = settings.InitializeBootSettings(t.Context(), consoleSettings.BootSettings{DataRoot: root, ListenAddr: "127.0.0.1:12030"}); e != nil {
		t.Fatal(e)
	}
	a := New(Options{DB: db, Config: config.Config{DataRoot: root}, Settings: settings})
	defer a.Close()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/replicate", strings.NewReader(`{"blank":true,"title":"测试"}`))
	r.Header.Set("Content-Type", "application/json")
	a.Handler().ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var p struct {
		ID string `json:"id"`
	}
	if e = json.Unmarshal(w.Body.Bytes(), &p); e != nil || p.ID == "" {
		t.Fatal(e)
	}
	for _, path := range []string{"/api/replicate", "/api/replicate/" + p.ID, "/api/replicate/providers"} {
		w = httptest.NewRecorder()
		a.Handler().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 {
			t.Fatalf("%s %d %s", path, w.Code, w.Body)
		}
	}
}
