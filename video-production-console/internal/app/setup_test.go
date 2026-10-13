package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	consoleauth "video-production-console/internal/auth"
	"video-production-console/internal/config"
	"video-production-console/internal/settings"
	"video-production-console/internal/store"
)

func TestSetupRoutesUseAdministratorSessionAndCSRF(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	root := t.TempDir()
	binary := filepath.Join(root, "codex.exe")
	if err := os.WriteFile(binary, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := settings.NewService(store.NewSettingsRepository(db), nil)
	if err := cfg.InitializeBootSettings(t.Context(), settings.BootSettings{ListenAddr: "127.0.0.1:2030", DataRoot: root, CodexBinaryPath: binary}); err != nil {
		t.Fatal(err)
	}
	auth := consoleauth.NewService(store.NewAuthStore(db), consoleauth.Options{})
	if err := auth.Bootstrap(t.Context(), "123321"); err != nil {
		t.Fatal(err)
	}
	login, err := auth.Login(t.Context(), "123321", "127.0.0.1:1234")
	if err != nil {
		t.Fatal(err)
	}
	application := New(Options{DB: db, Config: config.Config{DataRoot: root}, Settings: cfg, AuthService: auth})
	defer application.Close()
	for _, tc := range []struct {
		method, path, body string
		allowed            int
	}{
		{"GET", "/api/setup/status", "", 200}, {"GET", "/api/setup/models", "", 200},
		{"POST", "/api/setup/activate", `{"code":"invalid"}`, 400},
		{"POST", "/api/setup/export-code", `{}`, 200},
		{"POST", "/api/jianying-drafts/detect/apply", `{"draft_root":13}`, 400},
	} {
		for _, credentials := range []string{"none", "session", "csrf"} {
			request := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			want := tc.allowed
			if credentials == "none" {
				want = 401
			} else {
				request.AddCookie(&http.Cookie{Name: consoleauth.SessionCookieName, Value: login.Token})
				if credentials == "csrf" {
					request.AddCookie(&http.Cookie{Name: consoleauth.CSRFCookieName, Value: login.CSRF})
					request.Header.Set(consoleauth.CSRFHeader, login.CSRF)
				} else if tc.method == "POST" {
					want = 403
				}
			}
			response := httptest.NewRecorder()
			application.Handler().ServeHTTP(response, request)
			if response.Code != want {
				t.Fatalf("%s %s 凭据=%s 状态=%d 期望=%d", tc.method, tc.path, credentials, response.Code, want)
			}
			if response.Code == 200 && response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("配置响应没有禁止缓存")
			}
		}
	}
}
