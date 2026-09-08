package httpapi

import (
	"encoding/json"
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

	readRes := httptest.NewRecorder()
	handler.ServeHTTP(readRes, httptest.NewRequest(http.MethodGet, "/api/workspace/file?path=agent.md", nil))
	var file map[string]any
	if err := json.Unmarshal(readRes.Body.Bytes(), &file); err != nil {
		t.Fatal(err)
	}
	revision, _ := file["revision"].(string)
	if len(revision) != 64 {
		t.Fatalf("missing content revision: %s", readRes.Body.String())
	}
	payload, _ := json.Marshal(map[string]string{"path": "agent.md", "content": "# 改过了\n", "expected_revision": revision})
	putReq := httptest.NewRequest(http.MethodPut, "/api/workspace/file", strings.NewReader(string(payload)))
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
	// A second editor still holding the original revision must not replace the saved draft.
	conflict := httptest.NewRecorder()
	handler.ServeHTTP(conflict, httptest.NewRequest(http.MethodPut, "/api/workspace/file", strings.NewReader(string(payload))))
	if conflict.Code != http.StatusConflict {
		t.Fatalf("stale save status=%d body=%s", conflict.Code, conflict.Body.String())
	}
	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, httptest.NewRequest(http.MethodPut, "/api/workspace/file", strings.NewReader(`{"path":"agent.md","content":"stale"}`)))
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("unversioned save status=%d", missing.Code)
	}
	got, _ = os.ReadFile(filepath.Join(root, "agent.md"))
	if string(got) != "# 改过了\n" {
		t.Fatalf("conflicting save changed disk: %q", got)
	}
}
