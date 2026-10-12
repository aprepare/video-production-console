package app

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"video-production-console/internal/config"
	consoleSettings "video-production-console/internal/settings"
	"video-production-console/internal/store"
)

func TestMerchantRoutesMountedAlongsideProjects(t *testing.T) {
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	settings := consoleSettings.NewService(store.NewSettingsRepository(db), nil, consoleSettings.Options{})
	application := New(Options{DB: db, Config: config.Config{DataRoot: root}, Settings: settings})
	defer application.Close()
	id := uuid.NewString()
	for _, tc := range []struct {
		method, path string
		status       int
		contains     string
	}{
		{http.MethodGet, "/api/merchants", 200, "[]"},
		{http.MethodGet, "/api/merchants/" + id, 404, "merchant_not_found"},
		{http.MethodGet, "/api/materials/" + id + "/content", 404, "material_not_found"},
		{http.MethodGet, "/api/orders", 200, "[]"},
		{http.MethodGet, "/api/orders/" + id, 404, "order_not_found"},
		{http.MethodDelete, "/api/order-costs/" + id, 404, "cost_not_found"},
		{http.MethodGet, "/api/business/overview", 200, "contribution_cents"},
		{http.MethodPost, "/api/orders/" + id + "/projects/" + uuid.NewString() + "/deliver", 404, "project_not_found"},
		{http.MethodGet, "/api/projects/" + id, 404, "project_not_found"},
	} {
		w := httptest.NewRecorder()
		application.Handler().ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.contains) {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
}
