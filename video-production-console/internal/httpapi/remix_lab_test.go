package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"video-production-console/internal/agentruntime/openaicompat"
	"video-production-console/internal/assets"
	"video-production-console/internal/domain"
	"video-production-console/internal/remixlab"
	"video-production-console/internal/store"
)

type remixLabHTTPFakeProtector struct{}

func (remixLabHTTPFakeProtector) Protect(value []byte) ([]byte, error) {
	return append([]byte("opaque:"), value...), nil
}

func (remixLabHTTPFakeProtector) Unprotect(value []byte) ([]byte, error) {
	return bytes.TrimPrefix(value, []byte("opaque:")), nil
}

type remixLabStubRuntime struct {
	view remixlab.RuntimeView
}

func (s remixLabStubRuntime) Runtime(context.Context) (remixlab.RuntimeView, error) {
	return s.view, nil
}

func remixLabFastRunner(_ context.Context, opts openaicompat.Options) error {
	dir := filepath.Dir(opts.OutputLastMessage)
	return os.WriteFile(filepath.Join(dir, "continuous_script.txt"), []byte("http-stub-script"), 0o644)
}

func newRemixLabTestHandler(t *testing.T) http.Handler {
	t.Helper()
	_, handler := newRemixLabTestEnv(t)
	return handler
}

func newRemixLabTestEnv(t *testing.T) (*sql.DB, http.Handler) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	dataRoot := t.TempDir()
	projectRepo := store.NewProjectRepository(db)
	svc := remixlab.NewService(
		store.NewRemixLabRepository(db),
		remixLabStubRuntime{view: remixlab.RuntimeView{
			RemixBaseURL:         "http://console.example/v1",
			RemixModel:           "grok-4.6-fast",
			RemixReasoningEffort: "medium",
			RemixAPIKey:          "sk-runtime-secret",
			DataRoot:             dataRoot,
		}},
		remixLabHTTPFakeProtector{},
		dataRoot,
		remixLabFastRunner,
		assets.NewService(dataRoot),
		projectRepo,
	)
	return db, NewRemixLabHandler(svc, projectRepo)
}

func assertNoSecretLeak(t *testing.T, body string) {
	t.Helper()
	if strings.Contains(body, "sk-") {
		t.Fatalf("response leaked secret material: %s", body)
	}
}

func TestRemixLabHTTPRoundTrip(t *testing.T) {
	handler := newRemixLabTestHandler(t)

	defaultsReq := httptest.NewRequest(http.MethodGet, "/api/remix-lab/defaults", nil)
	defaultsRes := httptest.NewRecorder()
	handler.ServeHTTP(defaultsRes, defaultsReq)
	if defaultsRes.Code != http.StatusOK {
		t.Fatalf("defaults status=%d body=%s", defaultsRes.Code, defaultsRes.Body.String())
	}
	assertNoSecretLeak(t, defaultsRes.Body.String())
	var defaults remixlab.DefaultsView
	if err := json.Unmarshal(defaultsRes.Body.Bytes(), &defaults); err != nil {
		t.Fatal(err)
	}
	if defaults.RemixBaseURL != "http://console.example/v1" {
		t.Fatalf("defaults=%+v", defaults)
	}
	if !defaults.RemixAPIKeyConfigured {
		t.Fatalf("expected remix_api_key_configured true: %+v", defaults)
	}

	createBody := `{"source":"原文啊","slots":[{"model":"m","run_count":2}]}`
	createReq := httptest.NewRequest(http.MethodPost, "/api/remix-lab/experiments", strings.NewReader(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	createRes := httptest.NewRecorder()
	handler.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusAccepted {
		t.Fatalf("create status=%d body=%s", createRes.Code, createRes.Body.String())
	}
	assertNoSecretLeak(t, createRes.Body.String())
	var exp remixlab.Experiment
	if err := json.Unmarshal(createRes.Body.Bytes(), &exp); err != nil {
		t.Fatal(err)
	}
	if len(exp.Runs) != 2 {
		t.Fatalf("runs=%+v", exp.Runs)
	}
	runID := exp.Runs[0].ID

	patchReq := httptest.NewRequest(http.MethodPatch, "/api/remix-lab/runs/"+runID, strings.NewReader(`{"comment":"开头不够狠"}`))
	patchReq.Header.Set("Content-Type", "application/json")
	patchRes := httptest.NewRecorder()
	handler.ServeHTTP(patchRes, patchReq)
	if patchRes.Code != http.StatusOK {
		t.Fatalf("patch status=%d body=%s", patchRes.Code, patchRes.Body.String())
	}

	deadline := time.Now().Add(3 * time.Second)
	var got remixlab.Experiment
	for {
		getReq := httptest.NewRequest(http.MethodGet, "/api/remix-lab/experiments/"+exp.ID, nil)
		getRes := httptest.NewRecorder()
		handler.ServeHTTP(getRes, getReq)
		if getRes.Code != http.StatusOK {
			t.Fatalf("get status=%d body=%s", getRes.Code, getRes.Body.String())
		}
		assertNoSecretLeak(t, getRes.Body.String())
		if err := json.Unmarshal(getRes.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, run := range got.Runs {
			if run.ID == runID && run.Comment == "开头不够狠" {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("comment missing after patch: %+v", got.Runs)
		}
		if got.Status == "completed" || got.Status == "partial" || got.Status == "failed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for experiment terminal status: %+v", got)
		}
		time.Sleep(10 * time.Millisecond)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/remix-lab/experiments", nil)
	listRes := httptest.NewRecorder()
	handler.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listRes.Code, listRes.Body.String())
	}
	assertNoSecretLeak(t, listRes.Body.String())
	listBody := listRes.Body.String()
	if strings.Contains(listBody, "source_text") || strings.Contains(listBody, "continuous_script") {
		t.Fatalf("list must omit source_text and continuous_script: %s", listBody)
	}

	badReq := httptest.NewRequest(http.MethodPost, "/api/remix-lab/experiments", strings.NewReader(`{"source":"原文啊","slots":[{"model":"m","run_count":4}]}`))
	badReq.Header.Set("Content-Type", "application/json")
	badRes := httptest.NewRecorder()
	handler.ServeHTTP(badRes, badReq)
	if badRes.Code != http.StatusBadRequest {
		t.Fatalf("bad create status=%d body=%s", badRes.Code, badRes.Body.String())
	}
	var errBody map[string]string
	if err := json.Unmarshal(badRes.Body.Bytes(), &errBody); err != nil {
		t.Fatal(err)
	}
	if errBody["code"] != "invalid_remix_lab" {
		t.Fatalf("error body=%v", errBody)
	}
}

func TestRemixLabAdoptHTTP(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "adopt.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	root := t.TempDir()
	assetSvc := assets.NewService(root)
	projectRepo := store.NewProjectRepository(db)
	svc := remixlab.NewService(
		store.NewRemixLabRepository(db),
		remixLabStubRuntime{view: remixlab.RuntimeView{
			RemixBaseURL: "http://console.example/v1",
			RemixModel:   "m",
			RemixAPIKey:  "sk-runtime-secret",
			DataRoot:     root,
		}},
		remixLabHTTPFakeProtector{},
		root,
		remixLabFastRunner,
		assetSvc,
		projectRepo,
	)
	handler := NewRemixLabHandler(svc, projectRepo)

	accountID := uuid.NewString()
	projectID := uuid.NewString()
	now := time.Now().UTC()
	if _, err := store.NewAccountRepository(db).CreateWithBackground(context.Background(), domain.Account{
		ID: accountID, Name: "http-adopt", Color: "#fff", Status: "active", CreatedAt: now, UpdatedAt: now,
	}, store.NewBackground{
		ID: uuid.NewString(), Path: filepath.Join(root, "bg.png"), Filename: "bg.png", MIMEType: "image/png", Size: 1, SHA256: "sha",
	}); err != nil {
		t.Fatal(err)
	}
	if err := projectRepo.CreateProject(context.Background(), domain.Project{
		ID: projectID, AccountID: accountID, Title: "adopt-target", Stage: domain.StageScript, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	createReq := httptest.NewRequest(http.MethodPost, "/api/remix-lab/experiments", strings.NewReader(`{"source":"采用原文","slots":[{"model":"m","run_count":1}]}`))
	createReq.Header.Set("Content-Type", "application/json")
	createRes := httptest.NewRecorder()
	handler.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusAccepted {
		t.Fatalf("create status=%d body=%s", createRes.Code, createRes.Body.String())
	}
	var exp remixlab.Experiment
	if err := json.Unmarshal(createRes.Body.Bytes(), &exp); err != nil {
		t.Fatal(err)
	}
	runID := exp.Runs[0].ID

	deadline := time.Now().Add(3 * time.Second)
	for {
		getReq := httptest.NewRequest(http.MethodGet, "/api/remix-lab/experiments/"+exp.ID, nil)
		getRes := httptest.NewRecorder()
		handler.ServeHTTP(getRes, getReq)
		if getRes.Code != http.StatusOK {
			t.Fatalf("get status=%d body=%s", getRes.Code, getRes.Body.String())
		}
		if err := json.Unmarshal(getRes.Body.Bytes(), &exp); err != nil {
			t.Fatal(err)
		}
		if exp.Status == "completed" || exp.Status == "partial" || exp.Status == "failed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for experiment: %+v", exp)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if exp.Status != "completed" {
		t.Fatalf("status=%q", exp.Status)
	}

	adoptReq := httptest.NewRequest(http.MethodPost, "/api/remix-lab/runs/"+runID+"/adopt", strings.NewReader(`{"project_id":"`+projectID+`"}`))
	adoptReq.Header.Set("Content-Type", "application/json")
	adoptRes := httptest.NewRecorder()
	handler.ServeHTTP(adoptRes, adoptReq)
	if adoptRes.Code != http.StatusOK {
		t.Fatalf("adopt status=%d body=%s", adoptRes.Code, adoptRes.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/remix-lab/experiments/"+exp.ID, nil)
	getRes := httptest.NewRecorder()
	handler.ServeHTTP(getRes, getReq)
	if getRes.Code != http.StatusOK {
		t.Fatalf("get after adopt status=%d body=%s", getRes.Code, getRes.Body.String())
	}
	assertNoSecretLeak(t, getRes.Body.String())
	if err := json.Unmarshal(getRes.Body.Bytes(), &exp); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, run := range exp.Runs {
		if run.ID == runID {
			found = true
			if run.AdoptedProjectID != projectID {
				t.Fatalf("adopted_project_id=%q want %q", run.AdoptedProjectID, projectID)
			}
		}
	}
	if !found {
		t.Fatalf("run %s missing: %+v", runID, exp.Runs)
	}

	missingReq := httptest.NewRequest(http.MethodPost, "/api/remix-lab/runs/"+runID+"/adopt", strings.NewReader(`{"project_id":"`+uuid.NewString()+`"}`))
	missingReq.Header.Set("Content-Type", "application/json")
	missingRes := httptest.NewRecorder()
	handler.ServeHTTP(missingRes, missingReq)
	if missingRes.Code != http.StatusNotFound {
		t.Fatalf("missing project status=%d body=%s", missingRes.Code, missingRes.Body.String())
	}
	var errBody map[string]string
	if err := json.Unmarshal(missingRes.Body.Bytes(), &errBody); err != nil {
		t.Fatal(err)
	}
	if errBody["code"] != "project_not_found" {
		t.Fatalf("error body=%v", errBody)
	}
}

func TestRemixLabPromptLibraryAndAgentSettings(t *testing.T) {
	handler := newRemixLabTestHandler(t)

	listReq := httptest.NewRequest(http.MethodGet, "/api/remix-lab/prompts", nil)
	listRes := httptest.NewRecorder()
	handler.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK {
		t.Fatalf("list prompts status=%d body=%s", listRes.Code, listRes.Body.String())
	}
	var listed struct {
		Prompts []remixlab.PromptTemplate `json:"prompts"`
	}
	if err := json.Unmarshal(listRes.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Prompts) < 2 || listed.Prompts[0].ID != "elder_stable" {
		t.Fatalf("prompts=%+v", listed.Prompts)
	}

	createExp := httptest.NewRequest(http.MethodPost, "/api/remix-lab/experiments", strings.NewReader(`{"source":"交叉原文","prompt_ids":["elder_stable","bone_flesh"],"slots":[{"model":"m1","run_count":1},{"model":"m2","run_count":1}]}`))
	createExp.Header.Set("Content-Type", "application/json")
	createRes := httptest.NewRecorder()
	handler.ServeHTTP(createRes, createExp)
	if createRes.Code != http.StatusAccepted {
		t.Fatalf("cross create status=%d body=%s", createRes.Code, createRes.Body.String())
	}
	var exp remixlab.Experiment
	if err := json.Unmarshal(createRes.Body.Bytes(), &exp); err != nil {
		t.Fatal(err)
	}
	if exp.PromptStamp != "交叉试验" || len(exp.Runs) != 4 {
		t.Fatalf("cross exp=%+v", exp)
	}

	// 等实验落到终态再继续：异步运行协程还在往 DataRoot 写文件时就让测试
	// 返回，Windows 下 TempDir 清理会撞上「directory is not empty」。
	waitDeadline := time.Now().Add(3 * time.Second)
	for {
		getReq := httptest.NewRequest(http.MethodGet, "/api/remix-lab/experiments/"+exp.ID, nil)
		getRes := httptest.NewRecorder()
		handler.ServeHTTP(getRes, getReq)
		if getRes.Code != http.StatusOK {
			t.Fatalf("get cross exp status=%d body=%s", getRes.Code, getRes.Body.String())
		}
		var polled remixlab.Experiment
		if err := json.Unmarshal(getRes.Body.Bytes(), &polled); err != nil {
			t.Fatal(err)
		}
		if polled.Status == "completed" || polled.Status == "partial" || polled.Status == "failed" {
			break
		}
		if time.Now().After(waitDeadline) {
			t.Fatalf("timeout waiting for cross experiment terminal status: %+v", polled)
		}
		time.Sleep(10 * time.Millisecond)
	}

	putAgent := httptest.NewRequest(http.MethodPut, "/api/remix-lab/agent-settings", strings.NewReader(`{"model":"claude-sonnet-4-6","api_key":"sk-agent-secret"}`))
	putAgent.Header.Set("Content-Type", "application/json")
	putRes := httptest.NewRecorder()
	handler.ServeHTTP(putRes, putAgent)
	if putRes.Code != http.StatusOK {
		t.Fatalf("put agent status=%d body=%s", putRes.Code, putRes.Body.String())
	}
	assertNoSecretLeak(t, putRes.Body.String())
	if strings.Contains(putRes.Body.String(), "api_key\":") && strings.Contains(putRes.Body.String(), "sk-") {
		t.Fatal("agent settings leaked api key field")
	}

	getAgent := httptest.NewRequest(http.MethodGet, "/api/remix-lab/agent-settings", nil)
	getRes := httptest.NewRecorder()
	handler.ServeHTTP(getRes, getAgent)
	if getRes.Code != http.StatusOK {
		t.Fatalf("get agent status=%d body=%s", getRes.Code, getRes.Body.String())
	}
	assertNoSecretLeak(t, getRes.Body.String())
	var agent remixlab.AgentSettingsView
	if err := json.Unmarshal(getRes.Body.Bytes(), &agent); err != nil {
		t.Fatal(err)
	}
	if agent.Model != "claude-sonnet-4-6" || !agent.APIKeyConfigured {
		t.Fatalf("%+v", agent)
	}
}

func TestRemixLabDeleteExperimentHTTP(t *testing.T) {
	handler := newRemixLabTestHandler(t)
	createReq := httptest.NewRequest(http.MethodPost, "/api/remix-lab/experiments", strings.NewReader(`{"source":"待删原文","slots":[{"model":"m","run_count":1}]}`))
	createReq.Header.Set("Content-Type", "application/json")
	createRes := httptest.NewRecorder()
	handler.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusAccepted {
		t.Fatalf("create status=%d body=%s", createRes.Code, createRes.Body.String())
	}
	var exp remixlab.Experiment
	if err := json.Unmarshal(createRes.Body.Bytes(), &exp); err != nil {
		t.Fatal(err)
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/api/remix-lab/experiments/"+exp.ID, nil)
	delRes := httptest.NewRecorder()
	handler.ServeHTTP(delRes, delReq)
	if delRes.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", delRes.Code, delRes.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/remix-lab/experiments/"+exp.ID, nil)
	getRes := httptest.NewRecorder()
	handler.ServeHTTP(getRes, getReq)
	if getRes.Code != http.StatusNotFound {
		t.Fatalf("get after delete status=%d body=%s", getRes.Code, getRes.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/remix-lab/experiments", nil)
	listRes := httptest.NewRecorder()
	handler.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listRes.Code, listRes.Body.String())
	}
	var listed []remixlab.ExperimentSummary
	if err := json.Unmarshal(listRes.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed) != 0 {
		t.Fatalf("listed=%+v", listed)
	}

	again := httptest.NewRequest(http.MethodDelete, "/api/remix-lab/experiments/"+exp.ID, nil)
	againRes := httptest.NewRecorder()
	handler.ServeHTTP(againRes, again)
	if againRes.Code != http.StatusNotFound {
		t.Fatalf("second delete status=%d body=%s", againRes.Code, againRes.Body.String())
	}
}

func TestRemixLabAgentHistoryHTTP(t *testing.T) {
	handler := newRemixLabTestHandler(t)
	get := httptest.NewRequest(http.MethodGet, "/api/remix-lab/agent/history", nil)
	getRes := httptest.NewRecorder()
	handler.ServeHTTP(getRes, get)
	if getRes.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", getRes.Code, getRes.Body.String())
	}
	if !strings.Contains(getRes.Body.String(), `"turns":[]`) {
		t.Fatalf("get body=%s", getRes.Body.String())
	}
	del := httptest.NewRequest(http.MethodDelete, "/api/remix-lab/agent/history", nil)
	delRes := httptest.NewRecorder()
	handler.ServeHTTP(delRes, del)
	if delRes.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", delRes.Code, delRes.Body.String())
	}
}

func TestRemixLabAgentLastEmptyHTTP(t *testing.T) {
	handler := newRemixLabTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/remix-lab/agent/last", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"found":false`) {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestWriteRemixLabErrorSurfacesAgentUpstream(t *testing.T) {
	rec := httptest.NewRecorder()
	writeRemixLabError(rec, fmt.Errorf("%w: decode chat response: json cannot unmarshal object", remixlab.ErrAgentUpstream))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "智能体请求失败") || !strings.Contains(rec.Body.String(), "decode chat response") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestWriteRemixLabErrorExplainsGateway504(t *testing.T) {
	rec := httptest.NewRecorder()
	writeRemixLabError(rec, fmt.Errorf("%w: chat completions status 504: error code: 504", remixlab.ErrAgentUpstream))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "上游网关 504") || !strings.Contains(rec.Body.String(), "后台") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}
