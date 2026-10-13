package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"video-production-console/internal/jianyingdraft"
	"video-production-console/internal/settings"
	"video-production-console/internal/setup"
	"video-production-console/internal/store"
)

type setupHTTPProtector struct{}

func (setupHTTPProtector) Protect(value []byte) ([]byte, error) {
	return append([]byte("opaque:"), value...), nil
}

func (setupHTTPProtector) Unprotect(value []byte) ([]byte, error) {
	return bytes.Clone(bytes.TrimPrefix(value, []byte("opaque:"))), nil
}

func newSetupHTTPTest(t *testing.T) (http.Handler, *settings.Service, string) {
	t.Helper()
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
	cfg := settings.NewService(store.NewSettingsRepository(db), setupHTTPProtector{})
	if err := cfg.InitializeBootSettings(t.Context(), settings.BootSettings{ListenAddr: "127.0.0.1:2030", DataRoot: root, CodexBinaryPath: binary}); err != nil {
		t.Fatal(err)
	}
	drafts := t.TempDir()
	service := setup.NewService(cfg, setup.Options{Detect: func() (jianyingdraft.Detection, error) {
		return jianyingdraft.Detection{Candidates: []jianyingdraft.Candidate{{DraftRoot: drafts, HasRootMeta: true}}, Running: true}, nil
	}})
	return NewSetupHandler(service, cfg), cfg, drafts
}

func TestSetupHTTPDetectionApplyAndNoStore(t *testing.T) {
	handler, cfg, root := newSetupHTTPTest(t)
	if _, err := cfg.Runtime(t.Context()); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/jianying-drafts/detect", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("检测状态=%d", response.Code)
	}
	var detection setup.Detection
	if err := json.Unmarshal(response.Body.Bytes(), &detection); err != nil {
		t.Fatal(err)
	}
	if !detection.Running || len(detection.Candidates) != 1 || detection.ConfiguredMatches {
		t.Fatal("检测结果不正确")
	}
	for _, tc := range []struct {
		root   string
		status int
	}{{t.TempDir(), 400}, {root, 200}} {
		input, _ := json.Marshal(map[string]string{"draft_root": tc.root})
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/jianying-drafts/detect/apply", bytes.NewReader(input)))
		if response.Code != tc.status {
			t.Fatalf("应用状态=%d，期望%d", response.Code, tc.status)
		}
		if tc.status == 200 {
			var result setup.ApplyResult
			if json.Unmarshal(response.Body.Bytes(), &result) != nil || !result.RestartRequired || result.MachineProfileUpdated || result.JianyingRoot != root {
				t.Fatal("重启状态或档案同步状态错误")
			}
		}
	}
}

func TestSetupHTTPExportActivateDoesNotReturnSettingValues(t *testing.T) {
	handler, cfg, _ := newSetupHTTPTest(t)
	view, _ := cfg.Get(t.Context())
	view.Public.RemixModel = "export-model-marker"
	if _, err := cfg.Update(t.Context(), view.Public, map[string]string{settings.SecretRemixAPIKey: "private-http-marker"}); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/setup/export-code", strings.NewReader(`{"issued_to":"测试客户","include_secrets":true,"keys":["remix_model","remix_api_key"]}`)))
	if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("导出状态=%d", response.Code)
	}
	var exported settings.ExportCodeResult
	if err := json.Unmarshal(response.Body.Bytes(), &exported); err != nil {
		t.Fatal("配置码响应格式错误")
	}
	if !strings.HasPrefix(exported.Code, "JYC1.") || len(exported.SecretKeys) != 1 {
		t.Fatal("导出内容缺失")
	}
	input, _ := json.Marshal(map[string]string{"code": exported.Code})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/setup/activate", bytes.NewReader(input)))
	if response.Code != 200 {
		t.Fatalf("激活状态=%d", response.Code)
	}
	for _, forbidden := range []string{exported.Code, "private-http-marker", "export-model-marker"} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Fatal("激活响应回显了配置值")
		}
	}
	var activated settings.ActivationResult
	if json.Unmarshal(response.Body.Bytes(), &activated) != nil || activated.IssuedTo != "测试客户" || len(activated.AppliedPublic) != 1 || len(activated.AppliedSecrets) != 1 {
		t.Fatal("激活响应键列表错误")
	}
	if secret, err := cfg.ReadSecret(t.Context(), settings.SecretRemixAPIKey); err != nil || secret != "private-http-marker" {
		t.Fatal("导出再激活后未保留原始密钥")
	}
}

func TestSetupHTTPRejectsMalformedOversizedAndInvalidRequests(t *testing.T) {
	handler, _, _ := newSetupHTTPTest(t)
	for _, tc := range []struct {
		path, body string
		status     int
	}{
		{"/api/setup/activate", `{"code":"private-http-marker"}`, 400},
		{"/api/setup/activate", `{"code":"JYC1.invalid","unknown":"private-http-marker"}`, 400},
		{"/api/setup/activate", `{"code":"` + strings.Repeat("x", 70<<10) + `"}`, 413},
		{"/api/setup/activate", `{"code":"JYC1.invalid"} {}`, 400},
		{"/api/setup/export-code", `{"expires_days":-1}`, 400},
		{"/api/setup/export-code", `{"keys":["data_root"]}`, 400},
		{"/api/jianying-drafts/detect/apply", `{"draft_root":13}`, 400},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body)))
		if response.Code != tc.status {
			t.Fatalf("%s 状态=%d 期望=%d", tc.path, response.Code, tc.status)
		}
		if strings.Contains(response.Body.String(), "private-http-marker") {
			t.Fatal("错误响应暴露请求内容")
		}
	}
}

func TestSetupHTTPStatusAndModelsJSONContract(t *testing.T) {
	handler, _, _ := newSetupHTTPTest(t)
	for _, path := range []string{"/api/setup/status", "/api/setup/models"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s 状态=%d", path, response.Code)
		}
		var payload map[string]json.RawMessage
		if json.Unmarshal(response.Body.Bytes(), &payload) != nil {
			t.Fatal("响应非JSON")
		}
		if path == "/api/setup/status" {
			if string(payload["ready"]) != "false" || len(payload["items"]) == 0 {
				t.Fatal("体检响应缺少字段")
			}
		} else if string(payload["models"]) != "[]" || len(payload["errors"]) == 0 {
			t.Fatal("模型空结果应返回数组")
		}
	}
}
