package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	"video-production-console/internal/assets"
	"video-production-console/internal/remixlab"
	"video-production-console/internal/store"
)

func remixStyleRequest(t *testing.T, handler http.Handler, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	var err error
	if body != nil {
		raw, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, target, bytes.NewReader(raw))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func listedStyles(t *testing.T, handler http.Handler) []remixlab.StyleTemplate {
	t.Helper()
	rec := remixStyleRequest(t, handler, http.MethodGet, "/api/remix-lab/styles", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list styles status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Styles []remixlab.StyleTemplate `json:"styles"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Styles
}

func TestRemixStylesHTTPCRUDAndLargePayload(t *testing.T) {
	handler := newRemixLabTestHandler(t)
	builtins := listedStyles(t, handler)
	if len(builtins) != len(remixlab.BuiltinStyles()) {
		t.Fatalf("builtin styles=%d want=%d", len(builtins), len(remixlab.BuiltinStyles()))
	}

	large := strings.Repeat("界", 50000)
	create := remixlab.StyleTemplate{
		Name: "自定义大模板", Category: "知识", Description: large, Planner: large,
		Writer: large, WriterUser: large, Reviewer: large, Rules: large, Finance: true,
	}
	createdRec := remixStyleRequest(t, handler, http.MethodPost, "/api/remix-lab/styles", create)
	if createdRec.Code != http.StatusOK {
		t.Fatalf("create style status=%d body=%s", createdRec.Code, createdRec.Body.String())
	}
	var created remixlab.StyleTemplate
	if err := json.Unmarshal(createdRec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(created.ID, "custom-") || created.Builtin || !created.Finance || created.Description != large {
		t.Fatalf("created style=%#v", created)
	}

	created.Description = "更新后"
	created.Finance = false
	updatedRec := remixStyleRequest(t, handler, http.MethodPost, "/api/remix-lab/styles", created)
	if updatedRec.Code != http.StatusOK {
		t.Fatalf("update style status=%d body=%s", updatedRec.Code, updatedRec.Body.String())
	}
	styles := listedStyles(t, handler)
	if got := styles[len(styles)-1]; got.ID != created.ID || got.Description != "更新后" || got.Finance {
		t.Fatalf("updated listed style=%#v", got)
	}

	deletedRec := remixStyleRequest(t, handler, http.MethodDelete, "/api/remix-lab/styles/"+created.ID, nil)
	if deletedRec.Code != http.StatusOK || !strings.Contains(deletedRec.Body.String(), `"status":"ok"`) {
		t.Fatalf("delete style status=%d body=%s", deletedRec.Code, deletedRec.Body.String())
	}
	if got := len(listedStyles(t, handler)); got != len(builtins) {
		t.Fatalf("styles after delete=%d want=%d", got, len(builtins))
	}
	missingRec := remixStyleRequest(t, handler, http.MethodDelete, "/api/remix-lab/styles/"+created.ID, nil)
	if missingRec.Code != http.StatusNotFound {
		t.Fatalf("delete missing status=%d body=%s", missingRec.Code, missingRec.Body.String())
	}
}

func TestRemixStylesHTTPBuiltinEditCreatesDerivedAndBuiltinCannotDelete(t *testing.T) {
	handler := newRemixLabTestHandler(t)
	styles := listedStyles(t, handler)
	var builtin remixlab.StyleTemplate
	for _, style := range styles {
		if style.ID == "local_store" {
			builtin = style
			break
		}
	}
	if builtin.ID == "" {
		t.Fatal("local_store builtin missing")
	}
	builtin.Writer = "自定义门店写手"
	builtin.Finance = true
	rec := remixStyleRequest(t, handler, http.MethodPost, "/api/remix-lab/styles", builtin)
	if rec.Code != http.StatusOK {
		t.Fatalf("derive builtin status=%d body=%s", rec.Code, rec.Body.String())
	}
	var derived remixlab.StyleTemplate
	if err := json.Unmarshal(rec.Body.Bytes(), &derived); err != nil {
		t.Fatal(err)
	}
	if derived.ID == builtin.ID || derived.BaseID != builtin.ID || derived.Builtin || !derived.Finance {
		t.Fatalf("derived style=%#v", derived)
	}
	listed := listedStyles(t, handler)
	if listed[1].ID != "universal_remix" || listed[len(listed)-1].ID != derived.ID {
		t.Fatalf("builtin/custom order=%#v", listed)
	}

	deleteBuiltin := remixStyleRequest(t, handler, http.MethodDelete, "/api/remix-lab/styles/local_store", nil)
	if deleteBuiltin.Code != http.StatusConflict {
		t.Fatalf("delete builtin status=%d body=%s", deleteBuiltin.Code, deleteBuiltin.Body.String())
	}
}

func TestRemixStylesHTTPInvalidInputHasNoPersistenceSideEffect(t *testing.T) {
	handler := newRemixLabTestHandler(t)
	before := listedStyles(t, handler)

	invalid := remixStyleRequest(t, handler, http.MethodPost, "/api/remix-lab/styles", remixlab.StyleTemplate{Name: "无写手"})
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid style status=%d body=%s", invalid.Code, invalid.Body.String())
	}
	malformedReq := httptest.NewRequest(http.MethodPost, "/api/remix-lab/styles", strings.NewReader(`{"name":`))
	malformedReq.Header.Set("Content-Type", "application/json")
	malformedRec := httptest.NewRecorder()
	handler.ServeHTTP(malformedRec, malformedReq)
	if malformedRec.Code != http.StatusBadRequest {
		t.Fatalf("malformed style status=%d body=%s", malformedRec.Code, malformedRec.Body.String())
	}
	tooLargeReq := httptest.NewRequest(http.MethodPost, "/api/remix-lab/styles", strings.NewReader(`{"name":"n","writer":"`+strings.Repeat("x", (4<<20)+1)+`"}`))
	tooLargeReq.Header.Set("Content-Type", "application/json")
	tooLargeRec := httptest.NewRecorder()
	handler.ServeHTTP(tooLargeRec, tooLargeReq)
	if tooLargeRec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized style status=%d body=%s", tooLargeRec.Code, tooLargeRec.Body.String())
	}
	after := listedStyles(t, handler)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("invalid requests changed styles\nbefore=%#v\nafter=%#v", before, after)
	}
}

func TestRemixStylesHTTPStorageFailureReturnsInternalServerError(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "remix_lab"), []byte("blocks directory creation"), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	projectRepo := store.NewProjectRepository(db)
	svc := remixlab.NewService(
		store.NewRemixLabRepository(db),
		remixLabStubRuntime{view: remixlab.RuntimeView{DataRoot: root}},
		remixLabHTTPFakeProtector{}, root, remixLabFastRunner, assets.NewService(root), projectRepo,
	)
	handler := NewRemixLabHandler(svc, projectRepo)

	rec := remixStyleRequest(t, handler, http.MethodPost, "/api/remix-lab/styles", remixlab.StyleTemplate{Name: "有效模板", Writer: "有效写手"})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("storage failure status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestRemixWorkflowStyleAndBriefHTTP(t *testing.T) {
	handler := newRemixLabTestHandler(t)
	accountID := uuid.NewString()

	financeBody := map[string]any{
		"style_id": "finance_course",
		"brief":    map[string]any{"industry": " 财经知识 ", "platform": "channels", "duration_s": 60},
	}
	financeRec := remixStyleRequest(t, handler, http.MethodPut, "/api/remix-lab/workflow/style?account_id="+accountID, financeBody)
	if financeRec.Code != http.StatusOK {
		t.Fatalf("apply finance status=%d body=%s", financeRec.Code, financeRec.Body.String())
	}
	financeView := decodeWorkflowViewMap(t, financeRec.Body.Bytes())
	financeWorkflow := financeView["workflow"].(map[string]any)
	if financeWorkflow["style"] != "finance_course" || !workflowHasNode(financeWorkflow, "hook") {
		t.Fatalf("finance workflow=%#v", financeWorkflow)
	}
	brief := financeWorkflow["brief"].(map[string]any)
	if brief["industry"] != "财经知识" || brief["platform"] != "channels" {
		t.Fatalf("normalized brief=%#v", brief)
	}

	// 轻度洗稿不带策划：切过去时 hook 节点应被移除。
	universalRec := remixStyleRequest(t, handler, http.MethodPut, "/api/remix-lab/workflow/style?account_id="+accountID, map[string]any{"style_id": "light_wash"})
	if universalRec.Code != http.StatusOK {
		t.Fatalf("apply universal status=%d body=%s", universalRec.Code, universalRec.Body.String())
	}
	universalView := decodeWorkflowViewMap(t, universalRec.Body.Bytes())
	universalWorkflow := universalView["workflow"].(map[string]any)
	if universalWorkflow["style"] != "light_wash" || workflowHasNode(universalWorkflow, "hook") {
		t.Fatalf("universal workflow=%#v", universalWorkflow)
	}
	writerBefore := workflowNodeConfig(t, universalWorkflow, "writer")
	reviewerBefore := workflowNodeConfig(t, universalWorkflow, "review")

	briefRec := remixStyleRequest(t, handler, http.MethodPut, "/api/remix-lab/workflow/brief?account_id="+accountID, remixlab.Brief{Industry: " 茶叶 ", Platform: "xhs", DurationS: 30})
	if briefRec.Code != http.StatusOK {
		t.Fatalf("update brief status=%d body=%s", briefRec.Code, briefRec.Body.String())
	}
	briefView := decodeWorkflowViewMap(t, briefRec.Body.Bytes())
	briefWorkflow := briefView["workflow"].(map[string]any)
	if !reflect.DeepEqual(writerBefore, workflowNodeConfig(t, briefWorkflow, "writer")) || !reflect.DeepEqual(reviewerBefore, workflowNodeConfig(t, briefWorkflow, "review")) {
		t.Fatal("updating brief changed writer or reviewer prompts")
	}
	updatedBrief := briefWorkflow["brief"].(map[string]any)
	if updatedBrief["industry"] != "茶叶" || updatedBrief["platform"] != "xhs" {
		t.Fatalf("updated brief=%#v", updatedBrief)
	}
}

func TestRemixWorkflowStyleRequiresAccountAndLeavesGlobalUntouched(t *testing.T) {
	handler := newRemixLabTestHandler(t)
	before := remixStyleRequest(t, handler, http.MethodGet, "/api/remix-lab/workflow-prompts", nil)
	if before.Code != http.StatusOK {
		t.Fatalf("get global: %d %s", before.Code, before.Body.String())
	}
	for _, tc := range []struct {
		target string
		body   any
	}{
		{"/api/remix-lab/workflow/style", map[string]any{"style_id": "universal_remix"}},
		{"/api/remix-lab/workflow/brief?account_id=", remixlab.Brief{Offer: "本地套餐"}},
	} {
		rec := remixStyleRequest(t, handler, http.MethodPut, tc.target, tc.body)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid_account_id") {
			t.Errorf("missing account accepted: %d %s", rec.Code, rec.Body.String())
		}
	}
	after := remixStyleRequest(t, handler, http.MethodGet, "/api/remix-lab/workflow-prompts", nil)
	if after.Code != http.StatusOK || before.Body.String() != after.Body.String() {
		t.Fatal("missing account request changed the global workflow")
	}
}

func TestRemixWorkflowStyleHTTPRejectsInvalidInputsWithoutChangingWorkflow(t *testing.T) {
	handler := newRemixLabTestHandler(t)
	accountID := uuid.NewString()
	baseRec := remixStyleRequest(t, handler, http.MethodPut, "/api/remix-lab/workflow/style?account_id="+accountID, map[string]any{"style_id": "universal_remix"})
	if baseRec.Code != http.StatusOK {
		t.Fatalf("setup style status=%d body=%s", baseRec.Code, baseRec.Body.String())
	}
	before := decodeWorkflowViewMap(t, baseRec.Body.Bytes())["workflow"]

	tests := []struct {
		name   string
		target string
		body   any
	}{
		{name: "unknown style", target: "/api/remix-lab/workflow/style?account_id=" + accountID, body: map[string]any{"style_id": "missing"}},
		{name: "invalid brief", target: "/api/remix-lab/workflow/brief?account_id=" + accountID, body: remixlab.Brief{Platform: "wechat"}},
		{name: "bad style account", target: "/api/remix-lab/workflow/style?account_id=not-a-uuid", body: map[string]any{"style_id": "universal_remix"}},
		{name: "bad brief account", target: "/api/remix-lab/workflow/brief?account_id=not-a-uuid", body: remixlab.Brief{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := remixStyleRequest(t, handler, http.MethodPut, tt.target, tt.body)
			want := http.StatusBadRequest
			if tt.name == "unknown style" {
				want = http.StatusNotFound
			}
			if rec.Code != want {
				t.Fatalf("status=%d want=%d body=%s", rec.Code, want, rec.Body.String())
			}
		})
	}

	getRec := remixStyleRequest(t, handler, http.MethodGet, "/api/remix-lab/workflow-prompts?account_id="+accountID, nil)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get workflow status=%d body=%s", getRec.Code, getRec.Body.String())
	}
	after := decodeWorkflowViewMap(t, getRec.Body.Bytes())["workflow"]
	if !reflect.DeepEqual(before, after) {
		t.Fatal("invalid requests changed persisted workflow")
	}
}

func decodeWorkflowViewMap(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if _, ok := out["workflow"].(map[string]any); !ok {
		t.Fatalf("response has no workflow: %s", raw)
	}
	return out
}

func workflowHasNode(workflow map[string]any, id string) bool {
	nodes, _ := workflow["nodes"].([]any)
	for _, raw := range nodes {
		node, _ := raw.(map[string]any)
		if node["id"] == id {
			return true
		}
	}
	return false
}

func workflowNodeConfig(t *testing.T, workflow map[string]any, id string) map[string]any {
	t.Helper()
	nodes, _ := workflow["nodes"].([]any)
	for _, raw := range nodes {
		node, _ := raw.(map[string]any)
		if node["id"] == id {
			config, _ := node["config"].(map[string]any)
			return config
		}
	}
	t.Fatalf("node %q missing", id)
	return nil
}
