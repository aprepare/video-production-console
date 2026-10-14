package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	consoleauth "video-production-console/internal/auth"
	"video-production-console/internal/config"
	"video-production-console/internal/mcpserver"
	"video-production-console/internal/store"
)

func mcpTestApp(t *testing.T) (*App, string) {
	t.Helper()
	database, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	auth := consoleauth.NewService(store.NewAuthStore(database), consoleauth.Options{})
	if err := auth.Bootstrap(context.Background(), "123321"); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	application := New(Options{DB: database, Config: config.Config{DataRoot: root, ListenAddr: "127.0.0.1:2030"}, AuthService: auth, InternalToken: "internal-test"})
	return application, root
}

func TestMCPEndpointRequiresTokenAndReachesTheAPI(t *testing.T) {
	application, root := mcpTestApp(t)
	call := func(token, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
		req.RemoteAddr = "127.0.0.1:40000"
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		application.Handler().ServeHTTP(rec, req)
		return rec
	}
	if rec := call("", `{"jsonrpc":"2.0","id":1,"method":"initialize"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", rec.Code)
	}
	token := mcpserver.NewTokenStore(root).Current()
	rec := call(token, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_accounts","arguments":{}}}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `HTTP 200`) {
		t.Fatalf("list_accounts via MCP: %d %s", rec.Code, rec.Body.String())
	}
	// 令牌接口只给登录的操作员；不带会话 401，AI 走内部令牌 403。
	for _, tc := range []struct {
		internal string
		want     int
	}{{"", http.StatusUnauthorized}, {"internal-test", http.StatusForbidden}} {
		req := httptest.NewRequest(http.MethodGet, "/api/mcp/token", nil)
		req.RemoteAddr = "127.0.0.1:40000"
		if tc.internal != "" {
			req.Header.Set("X-Internal-Token", tc.internal)
		}
		got := httptest.NewRecorder()
		application.Handler().ServeHTTP(got, req)
		if got.Code != tc.want {
			t.Fatalf("token endpoint internal=%q: %d", tc.internal, got.Code)
		}
	}
	rec = call(token, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"console_api","arguments":{"method":"POST","path":"/api/mcp/token/rotate"}}}`)
	if !strings.Contains(rec.Body.String(), "不对 AI 开放") {
		t.Fatalf("AI rotated its own token: %s", rec.Body.String())
	}
}
