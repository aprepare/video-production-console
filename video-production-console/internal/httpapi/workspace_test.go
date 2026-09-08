package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"video-production-console/internal/workspace"
)

func TestWorkspaceHTTPReadAndWrite(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "agent.md"), []byte("# 入口\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	handler := NewWorkspaceHandler(workspace.Store{Root: root})

	treeRes := httptest.NewRecorder()
	handler.ServeHTTP(treeRes, httptest.NewRequest(http.MethodGet, "/api/workspace/tree", nil))
	if treeRes.Code != http.StatusOK || !strings.Contains(treeRes.Body.String(), "agent.md") {
		t.Fatalf("tree status=%d body=%s", treeRes.Code, treeRes.Body.String())
	}

	escapeRes := httptest.NewRecorder()
	handler.ServeHTTP(escapeRes, httptest.NewRequest(http.MethodGet, "/api/workspace/file?path=../secret.md", nil))
	if escapeRes.Code != http.StatusBadRequest {
		t.Fatalf("escape status=%d body=%s", escapeRes.Code, escapeRes.Body.String())
	}

	putReq := httptest.NewRequest(http.MethodPut, "/api/workspace/file", strings.NewReader(`{"path":"agent.md","content":"# 改过了\n"}`))
	putReq.Header.Set("Content-Type", "application/json")
	putRes := httptest.NewRecorder()
	handler.ServeHTTP(putRes, putReq)
	if putRes.Code != http.StatusOK || !strings.Contains(putRes.Body.String(), "改过了") {
		t.Fatalf("put status=%d body=%s", putRes.Code, putRes.Body.String())
	}
	got, err := os.ReadFile(filepath.Join(root, "agent.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "# 改过了\n" {
		t.Fatalf("disk=%q", got)
	}
}
