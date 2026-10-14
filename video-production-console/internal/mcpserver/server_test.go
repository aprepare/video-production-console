package mcpserver

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type seenRequest struct {
	method, path, query, body, internal string
}

func testServer(t *testing.T) (*Server, *[]seenRequest) {
	t.Helper()
	var seen []seenRequest
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		seen = append(seen, seenRequest{r.Method, r.URL.Path, r.URL.RawQuery, string(raw), r.Header.Get("X-Internal-Token")})
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/missing") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":"not_found","message":"没找到"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	s := New(api, "internal-secret", func() string { return "jy_token" }, "test")
	s.tools = append(s.tools,
		Tool{Name: "probe_create", Method: http.MethodPost, Path: "/api/things/{id}/items", Params: map[string]Param{
			"id":    {Type: "string", Required: true, In: "path"},
			"limit": {Type: "integer", In: "query"},
			"title": {Type: "string", Required: true},
		}},
		Tool{Name: "probe_delete", Method: http.MethodDelete, Path: "/api/things/{id}", Confirm: "删除这条记录", Params: map[string]Param{
			"id": {Type: "string", Required: true, In: "path"},
		}},
	)
	for _, tool := range s.tools[len(s.tools)-2:] {
		s.byName[tool.Name] = tool
	}
	return s, &seen
}

func rpc(t *testing.T, s *Server, body, token, remote string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.RemoteAddr = remote
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

func callResult(t *testing.T, rec *httptest.ResponseRecorder) (string, bool) {
	t.Helper()
	var resp struct {
		Result struct {
			Content []struct{ Text string } `json:"content"`
			IsError bool                    `json:"isError"`
		} `json:"result"`
		Error *rpcError `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bad response %q: %v", rec.Body.String(), err)
	}
	if resp.Error != nil {
		t.Fatalf("rpc error: %+v", resp.Error)
	}
	return resp.Result.Content[0].Text, resp.Result.IsError
}

func TestMCPRejectsRemoteAndMissingToken(t *testing.T) {
	s, _ := testServer(t)
	init := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
	if rec := rpc(t, s, init, "jy_token", "192.168.1.5:5000"); rec.Code != http.StatusForbidden {
		t.Fatalf("remote: %d", rec.Code)
	}
	if rec := rpc(t, s, init, "", "127.0.0.1:5000"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", rec.Code)
	}
	if rec := rpc(t, s, init, "wrong", "127.0.0.1:5000"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: %d", rec.Code)
	}
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(init))
	req.RemoteAddr = "127.0.0.1:5000"
	req.Header.Set("Authorization", "Bearer jy_token")
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site origin: %d", rec.Code)
	}
}

func TestMCPInitializeListAndNotification(t *testing.T) {
	s, _ := testServer(t)
	rec := rpc(t, s, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`, "jy_token", "127.0.0.1:5000")
	if !strings.Contains(rec.Body.String(), ProtocolVersion) || !strings.Contains(rec.Body.String(), `"tools"`) {
		t.Fatalf("initialize: %s", rec.Body.String())
	}
	if rec := rpc(t, s, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, "jy_token", "127.0.0.1:5000"); rec.Code != http.StatusAccepted || rec.Body.Len() != 0 {
		t.Fatalf("notification: %d %q", rec.Code, rec.Body.String())
	}
	rec = rpc(t, s, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, "jy_token", "127.0.0.1:5000")
	var list struct {
		Result struct {
			Tools []struct {
				Name        string         `json:"name"`
				Description string         `json:"description"`
				InputSchema map[string]any `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range list.Result.Tools {
		names[tool.Name] = true
		if tool.InputSchema["type"] != "object" {
			t.Fatalf("%s schema = %v", tool.Name, tool.InputSchema)
		}
		if tool.Name == "probe_delete" && !strings.Contains(tool.Description, "需要用户确认") {
			t.Fatalf("confirm hint missing: %s", tool.Description)
		}
	}
	for _, want := range []string{"console_api", "console_api_catalog", "probe_create"} {
		if !names[want] {
			t.Fatalf("tools/list missing %s", want)
		}
	}
}

func TestMCPToolCallsReachTheAPIWithInternalToken(t *testing.T) {
	s, seen := testServer(t)
	rec := rpc(t, s, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"probe_create","arguments":{"id":"a b","limit":5,"title":"新标题"}}}`, "jy_token", "127.0.0.1:5000")
	text, isErr := callResult(t, rec)
	if isErr || !strings.Contains(text, "HTTP 200") {
		t.Fatalf("call: %v %s", isErr, text)
	}
	got := (*seen)[0]
	if got.method != http.MethodPost || got.path != "/api/things/a b/items" || got.query != "limit=5" || got.internal != "internal-secret" {
		t.Fatalf("forwarded %+v", got)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(got.body), &body)
	if body["title"] != "新标题" || body["id"] != nil || body["limit"] != nil {
		t.Fatalf("body = %v", body)
	}

	text, isErr = callResult(t, rpc(t, s, `{"jsonrpc":"2.0","id":41,"method":"tools/call","params":{"name":"probe_create","arguments":{"id":"x","title":"t","extra":1}}}`, "jy_token", "127.0.0.1:5000"))
	if !isErr || !strings.Contains(text, "extra") || len(*seen) != 1 {
		t.Fatalf("unknown arg must be refused before calling the API: %v %s", isErr, text)
	}
	text, isErr = callResult(t, rpc(t, s, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"probe_create","arguments":{"id":"x"}}}`, "jy_token", "127.0.0.1:5000"))
	if !isErr || !strings.Contains(text, "title") {
		t.Fatalf("missing required: %v %s", isErr, text)
	}

	text, isErr = callResult(t, rpc(t, s, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"console_api","arguments":{"method":"GET","path":"/api/things/missing?q=1"}}}`, "jy_token", "127.0.0.1:5000"))
	if !isErr || !strings.Contains(text, "HTTP 404") || !strings.Contains(text, "没找到") {
		t.Fatalf("generic 404: %v %s", isErr, text)
	}
}

func TestMCPConfirmAndForbiddenPaths(t *testing.T) {
	s, seen := testServer(t)
	text, isErr := callResult(t, rpc(t, s, `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"probe_delete","arguments":{"id":"x"}}}`, "jy_token", "127.0.0.1:5000"))
	if !isErr || !strings.Contains(text, "需要用户确认") || len(*seen) != 0 {
		t.Fatalf("unconfirmed delete ran: %v %s %v", isErr, text, *seen)
	}
	_, isErr = callResult(t, rpc(t, s, `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"probe_delete","arguments":{"id":"x","confirm":true}}}`, "jy_token", "127.0.0.1:5000"))
	if isErr || len(*seen) != 1 || (*seen)[0].method != http.MethodDelete || (*seen)[0].body != "" {
		t.Fatalf("confirmed delete: %v %v", isErr, *seen)
	}
	for _, path := range []string{"/api/mcp/token", "/api/auth/logout", "/mcp"} {
		text, isErr := callResult(t, rpc(t, s, `{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"console_api","arguments":{"method":"GET","path":"`+path+`"}}}`, "jy_token", "127.0.0.1:5000"))
		if !isErr || len(*seen) != 1 {
			t.Fatalf("%s reached api: %s", path, text)
		}
	}
}
