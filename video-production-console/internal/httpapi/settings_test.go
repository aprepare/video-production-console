package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"video-production-console/internal/domain"
	consoleSettings "video-production-console/internal/settings"
	"video-production-console/internal/store"
)

type httpFakeProtector struct{}

func (httpFakeProtector) Protect(value []byte) ([]byte, error) {
	return append([]byte("opaque:"), value...), nil
}
func (httpFakeProtector) Unprotect(value []byte) ([]byte, error) {
	return bytes.TrimPrefix(value, []byte("opaque:")), nil
}

type httpRunner struct{ calls [][]string }

func (r *httpRunner) Run(_ context.Context, executable string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, append([]string{executable}, args...))
	if reflect.DeepEqual(args, []string{"mcp", "list"}) {
		return []byte("baokuan enabled"), nil
	}
	return nil, nil
}

type httpDoer struct{}

func (httpDoer) Do(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"materials":[]}`)), Header: make(http.Header)}, nil
}

func TestSettingsHTTPMasksSecretsAndTreatsEmptySecretAsUnchanged(t *testing.T) {
	handler, db, public := newSettingsHTTPTest(t, consoleSettings.Options{})
	body, err := json.Marshal(map[string]any{
		"public":  public,
		"secrets": map[string]string{consoleSettings.SecretGrokAPIKey: "secret-value"},
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewReader(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("PUT status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	assertNoSecretHTTPMaterial(t, recorder.Body.String())

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("GET Cache-Control=%q, want no-store", recorder.Header().Get("Cache-Control"))
	}
	assertNoSecretHTTPMaterial(t, recorder.Body.String())
	var view consoleSettings.View
	if err := json.Unmarshal(recorder.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if !view.Secrets[consoleSettings.SecretGrokAPIKey].Configured || view.Secrets[consoleSettings.SecretGrokAPIKey].Masked == "" {
		t.Fatalf("masked view=%+v", view)
	}

	var secretVersion int
	if err := db.QueryRow(`SELECT version FROM encrypted_secrets WHERE key=?`, consoleSettings.SecretGrokAPIKey).Scan(&secretVersion); err != nil {
		t.Fatal(err)
	}
	body, _ = json.Marshal(map[string]any{
		"public":  public,
		"secrets": map[string]string{consoleSettings.SecretGrokAPIKey: ""},
	})
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewReader(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("empty secret PUT status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("PUT Cache-Control=%q, want no-store", recorder.Header().Get("Cache-Control"))
	}
	var afterVersion int
	if err := db.QueryRow(`SELECT version FROM encrypted_secrets WHERE key=?`, consoleSettings.SecretGrokAPIKey).Scan(&afterVersion); err != nil {
		t.Fatal(err)
	}
	if afterVersion != secretVersion {
		t.Fatalf("empty secret version %d -> %d", secretVersion, afterVersion)
	}
}

func TestSettingsHTTPDependencyProbeAndRepairContracts(t *testing.T) {
	runner := &httpRunner{}
	handler, _, public := newSettingsHTTPTest(t, consoleSettings.Options{Runner: runner, HTTPClient: httpDoer{}})
	body, _ := json.Marshal(map[string]any{"public": public, "secrets": map[string]string{}})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewReader(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("seed status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodPost, "/api/settings/test/baokuan", nil),
		httptest.NewRequest(http.MethodPost, "/api/settings/repair/baokuan-mcp", nil),
	} {
		recorder = httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", request.URL.Path, recorder.Code, recorder.Body.String())
		}
		var health consoleSettings.Health
		if err := json.Unmarshal(recorder.Body.Bytes(), &health); err != nil {
			t.Fatal(err)
		}
		if health.Status != consoleSettings.HealthOK || health.Message == "" {
			t.Fatalf("health=%+v", health)
		}
	}
	if len(runner.calls) != 3 {
		t.Fatalf("runner calls=%+v", runner.calls)
	}
	wantRepair := []string{public.CodexBinaryPath, "mcp", "add", "baokuan", "--", public.BaokuanMCPExecutable, "mcp", "--base", public.BaokuanBaseURL}
	if !reflect.DeepEqual(runner.calls[1], wantRepair) {
		t.Fatalf("repair argv=%v, want %v", runner.calls[1], wantRepair)
	}
}

func TestSettingsHTTPRejectsUnknownOrOversizedInputsWithoutEcho(t *testing.T) {
	handler, _, public := newSettingsHTTPTest(t, consoleSettings.Options{})
	body, _ := json.Marshal(map[string]any{"public": public, "secrets": map[string]string{"unknown": "do-not-echo"}})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewReader(body)))
	if recorder.Code != http.StatusBadRequest || strings.Contains(recorder.Body.String(), "do-not-echo") {
		t.Fatalf("unknown secret response status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"public":{},"secrets":{},"extra":true}`)))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func newSettingsHTTPTest(t *testing.T, options consoleSettings.Options) (http.Handler, *sql.DB, domain.PublicSettings) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	root := t.TempDir()
	dataRoot := filepath.Join(root, "data")
	vault := filepath.Join(root, "vault")
	public := domain.PublicSettings{
		ListenAddr: "127.0.0.1:2030", DataRoot: dataRoot, MaxCodexConcurrency: 2,
		BaokuanBaseURL: "http://127.0.0.1:2022", BaokuanMCPExecutable: filepath.Join(root, "baokuan.exe"),
		ObsidianVault: vault, TopicCardsDir: filepath.Join(vault, "topic-cards"),
		GrokBaseURL: "http://127.0.0.1:3030", GrokModel: "grok-test",
		CodexBinaryPath: filepath.Join(root, "codex.exe"), MediaIndexPath: filepath.Join(dataRoot, "media-index.json"),
		MediaRoot: filepath.Join(root, "media"), JianyingRoot: filepath.Join(root, "jianying"),
	}
	service := consoleSettings.NewService(store.NewSettingsRepository(db), httpFakeProtector{}, options)
	return NewSettingsHandler(service), db, public
}

func assertNoSecretHTTPMaterial(t *testing.T, body string) {
	t.Helper()
	for _, forbidden := range []string{"secret-value", "opaque:", "b3BhcXVl"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("HTTP response leaked %q: %s", forbidden, body)
		}
	}
}
