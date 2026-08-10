package settings

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"video-production-console/internal/domain"
	"video-production-console/internal/security"
	"video-production-console/internal/store"
	"video-production-console/internal/taskmodel"
)

type fakeProtector struct {
	protected   [][]byte
	unprotected [][]byte
	corrupt     bool
}

func (f *fakeProtector) Protect(plain []byte) ([]byte, error) {
	f.protected = append(f.protected, bytes.Clone(plain))
	return append([]byte("cipher-boundary:"), plain...), nil
}

func (f *fakeProtector) Unprotect(ciphertext []byte) ([]byte, error) {
	f.unprotected = append(f.unprotected, bytes.Clone(ciphertext))
	if f.corrupt || !bytes.HasPrefix(ciphertext, []byte("cipher-boundary:")) {
		return nil, errors.New("fake protector rejected opaque bytes")
	}
	return bytes.Clone(bytes.TrimPrefix(ciphertext, []byte("cipher-boundary:"))), nil
}

func TestSettingsPublicUpdateSecretMaskingAndRuntimeSeparation(t *testing.T) {
	service, db, protector, public := newSettingsTestService(t, Options{})
	version, err := service.PutPublic(t.Context(), public)
	if err != nil || version != 1 {
		t.Fatalf("PutPublic() version=%d err=%v", version, err)
	}
	if err := service.PutSecret(t.Context(), SecretGrokAPIKey, "secret-value"); err != nil {
		t.Fatal(err)
	}
	if err := service.PutSecret(t.Context(), SecretPexelsAPIKey, "pexels-value"); err != nil {
		t.Fatal(err)
	}

	view, err := service.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if view.SettingsVersion != 1 || !reflect.DeepEqual(view.Public, public) {
		t.Fatalf("view=%+v", view)
	}
	if !view.Secrets[SecretGrokAPIKey].Configured || view.Secrets[SecretGrokAPIKey].Masked == "" {
		t.Fatalf("secret status=%+v", view.Secrets)
	}
	for _, forbidden := range []string{"secret-value", "pexels-value", "cipher-boundary", "c2VjcmV0"} {
		if bytes.Contains(raw, []byte(forbidden)) {
			t.Fatalf("HTTP view leaked %q: %s", forbidden, raw)
		}
	}

	runtime, err := service.Runtime(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GrokAPIKey != "secret-value" || runtime.PexelsAPIKey != "pexels-value" || !reflect.DeepEqual(runtime.PublicSettings, public) {
		t.Fatalf("runtime=%+v", runtime)
	}
	runtimeJSON, err := json.Marshal(runtime)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(runtimeJSON, []byte("secret-value")) || bytes.Contains(runtimeJSON, []byte("pexels-value")) {
		t.Fatalf("runtime JSON leaked decrypted values: %s", runtimeJSON)
	}
	if len(protector.unprotected) != 2 {
		t.Fatalf("unprotect calls=%d", len(protector.unprotected))
	}

	var beforeVersion int
	if err := db.QueryRow(`SELECT version FROM encrypted_secrets WHERE key=?`, SecretGrokAPIKey).Scan(&beforeVersion); err != nil {
		t.Fatal(err)
	}
	protectCalls := len(protector.protected)
	if err := service.PutSecret(t.Context(), SecretGrokAPIKey, ""); err != nil {
		t.Fatal(err)
	}
	var afterVersion int
	if err := db.QueryRow(`SELECT version FROM encrypted_secrets WHERE key=?`, SecretGrokAPIKey).Scan(&afterVersion); err != nil {
		t.Fatal(err)
	}
	if beforeVersion != afterVersion || len(protector.protected) != protectCalls {
		t.Fatalf("empty secret changed state: versions %d -> %d, protect calls %d -> %d", beforeVersion, afterVersion, protectCalls, len(protector.protected))
	}
}

func TestSettingsPublicCodexDefaultsWhenKeysAreMissingOrEmpty(t *testing.T) {
	service, db, _, _ := newSettingsTestService(t, Options{})
	assertDefaults := func() {
		view, err := service.Get(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if view.Public.CodexDefaultModel != taskmodel.DefaultModel || view.Public.CodexDefaultReasoningEffort != taskmodel.DefaultReasoningEffort {
			t.Fatalf("codex defaults=%q/%q", view.Public.CodexDefaultModel, view.Public.CodexDefaultReasoningEffort)
		}
	}
	assertDefaults()
	if _, err := db.Exec(`INSERT INTO settings(key,value) VALUES('codex_default_model',''),('codex_default_reasoning_effort','')`); err != nil {
		t.Fatal(err)
	}
	assertDefaults()
	if _, err := db.Exec(`UPDATE settings SET value='   ' WHERE key IN ('codex_default_model','codex_default_reasoning_effort')`); err != nil {
		t.Fatal(err)
	}
	assertDefaults()
}

func TestSettingsPublicCodexDefaultsRoundTripAndResolveTaskModel(t *testing.T) {
	service, _, _, public := newSettingsTestService(t, Options{})
	public.CodexDefaultModel = "openai/gpt-5.6-sol:preview"
	public.CodexDefaultReasoningEffort = "xhigh"
	if _, err := service.PutPublic(t.Context(), public); err != nil {
		t.Fatal(err)
	}
	view, err := service.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(view.Public, public) {
		t.Fatalf("round trip=%+v, want %+v", view.Public, public)
	}
	selection, err := service.ResolveTaskModel(t.Context(), taskmodel.Selection{ReasoningEffort: " ULTRA "})
	if err != nil {
		t.Fatal(err)
	}
	want := taskmodel.Selection{Model: public.CodexDefaultModel, ReasoningEffort: "ultra"}
	if selection != want {
		t.Fatalf("ResolveTaskModel()=%+v, want %+v", selection, want)
	}
}

func TestResolveTaskModelUsesSavedDefaultsAfterRuntimeWasCached(t *testing.T) {
	service, _, _, public := newSettingsTestService(t, Options{})
	public.CodexDefaultModel = "gpt-5.6-terra"
	public.CodexDefaultReasoningEffort = "high"
	if _, err := service.PutPublic(t.Context(), public); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Runtime(t.Context()); err != nil {
		t.Fatal(err)
	}

	public.CodexDefaultModel = "gpt-5.6-sol"
	public.CodexDefaultReasoningEffort = "medium"
	if _, err := service.PutPublic(t.Context(), public); err != nil {
		t.Fatal(err)
	}
	selection, err := service.ResolveTaskModel(t.Context(), taskmodel.Selection{})
	if err != nil {
		t.Fatal(err)
	}
	want := taskmodel.Selection{Model: "gpt-5.6-sol", ReasoningEffort: "medium"}
	if selection != want {
		t.Fatalf("ResolveTaskModel()=%+v, want newly saved %+v", selection, want)
	}
}

func TestSettingsPublicRejectsInvalidCodexDefaultsWithoutPartialWrite(t *testing.T) {
	service, _, _, valid := newSettingsTestService(t, Options{})
	if _, err := service.PutPublic(t.Context(), valid); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*domain.PublicSettings)
	}{
		{"model", func(value *domain.PublicSettings) { value.CodexDefaultModel = "bad model\nsecret" }},
		{"model whitespace", func(value *domain.PublicSettings) { value.CodexDefaultModel = " gpt-5.6-sol " }},
		{"effort", func(value *domain.PublicSettings) { value.CodexDefaultReasoningEffort = "impossible" }},
		{"effort uppercase", func(value *domain.PublicSettings) { value.CodexDefaultReasoningEffort = "HIGH" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			invalid := valid
			test.mutate(&invalid)
			if _, err := service.PutPublic(t.Context(), invalid); !errors.Is(err, ErrInvalidSettings) {
				t.Fatalf("PutPublic() error=%v", err)
			}
			view, err := service.Get(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if view.SettingsVersion != 1 || !reflect.DeepEqual(view.Public, valid) {
				t.Fatalf("invalid update was partial: %+v", view)
			}
		})
	}
}

func TestFreshDatabaseBootInitializationMakesRuntimeConfigured(t *testing.T) {
	service, _, _, public := newSettingsTestService(t, Options{})
	if _, err := service.Runtime(t.Context()); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("fresh Runtime() error=%v, want ErrNotConfigured", err)
	}
	boot := BootSettings{
		DataRoot:        public.DataRoot,
		CodexBinaryPath: public.CodexBinaryPath,
	}
	if err := service.InitializeBootSettings(t.Context(), boot); err != nil {
		t.Fatalf("InitializeBootSettings() error=%v", err)
	}
	runtime, err := service.Runtime(t.Context())
	if err != nil {
		t.Fatalf("Runtime() after boot initialization error=%v", err)
	}
	if runtime.DataRoot != boot.DataRoot || runtime.CodexBinaryPath != boot.CodexBinaryPath {
		t.Fatalf("runtime=%+v boot=%+v", runtime, boot)
	}
}

func TestLegacyBootWithoutCodexTaskProjectRootRemainsSchemaShaped(t *testing.T) {
	service, _, _, public := newSettingsTestService(t, Options{})
	// A legacy installation has no codex_task_project_root row. Boot must still
	// initialize and expose the required JSON property as an empty string.
	if err := service.InitializeBootSettings(t.Context(), BootSettings{
		DataRoot:        public.DataRoot,
		CodexBinaryPath: public.CodexBinaryPath,
	}); err != nil {
		t.Fatalf("legacy InitializeBootSettings() error=%v", err)
	}
	view, err := service.Get(t.Context())
	if err != nil {
		t.Fatalf("legacy Get() error=%v", err)
	}
	if view.Public.CodexTaskProjectRoot != "" {
		t.Fatalf("legacy task project root=%q, want empty", view.Public.CodexTaskProjectRoot)
	}
	encoded, err := json.Marshal(view.Public)
	if err != nil {
		t.Fatal(err)
	}
	var properties map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &properties); err != nil {
		t.Fatal(err)
	}
	if _, ok := properties["codex_task_project_root"]; !ok {
		t.Fatalf("legacy JSON omitted required property: %s", encoded)
	}
	if _, err := service.Runtime(t.Context()); err != nil {
		t.Fatalf("legacy Runtime() error=%v", err)
	}
}

func TestBootInitializationDoesNotOverwriteExistingUserValues(t *testing.T) {
	service, db, _, public := newSettingsTestService(t, Options{})
	customRoot := filepath.Join(t.TempDir(), "custom-data")
	customCodex := filepath.Join(t.TempDir(), "custom-codex.exe")
	if _, err := db.Exec(`INSERT INTO settings(key,value) VALUES('data_root',?)`, customRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE settings SET value=? WHERE key='codex_binary_path'`, customCodex); err != nil {
		t.Fatal(err)
	}
	if err := service.InitializeBootSettings(t.Context(), BootSettings{DataRoot: public.DataRoot, CodexBinaryPath: public.CodexBinaryPath, ListenAddr: public.ListenAddr}); err != nil {
		t.Fatal(err)
	}
	view, err := service.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if view.Public.DataRoot != customRoot || view.Public.CodexBinaryPath != customCodex {
		t.Fatalf("boot overwrote user values: %+v", view.Public)
	}
}

func TestSettingsValidationRejectsUnsafeValuesWithoutPartialWrite(t *testing.T) {
	service, _, _, valid := newSettingsTestService(t, Options{})
	if version, err := service.PutPublic(t.Context(), valid); err != nil || version != 1 {
		t.Fatalf("seed version=%d err=%v", version, err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	tests := []struct {
		name   string
		mutate func(*domain.PublicSettings)
	}{
		{"zero concurrency", func(v *domain.PublicSettings) { v.MaxCodexConcurrency = 0 }},
		{"too much concurrency", func(v *domain.PublicSettings) { v.MaxCodexConcurrency = 5 }},
		{"listen URL", func(v *domain.PublicSettings) { v.ListenAddr = "http://127.0.0.1:2030" }},
		{"listen missing port", func(v *domain.PublicSettings) { v.ListenAddr = "127.0.0.1" }},
		{"remote baokuan", func(v *domain.PublicSettings) { v.BaokuanBaseURL = "https://example.com" }},
		{"baokuan userinfo", func(v *domain.PublicSettings) { v.BaokuanBaseURL = "http://user@127.0.0.1:2022" }},
		{"baokuan fragment", func(v *domain.PublicSettings) { v.BaokuanBaseURL = "http://127.0.0.1:2022/#secret" }},
		{"relative data root", func(v *domain.PublicSettings) { v.DataRoot = "relative" }},
		{"noncanonical data root", func(v *domain.PublicSettings) {
			v.DataRoot = v.DataRoot + string(filepath.Separator) + "child" + string(filepath.Separator) + ".."
		}},
		{"topic cards escape vault", func(v *domain.PublicSettings) { v.TopicCardsDir = outside }},
		{"media index escape media root", func(v *domain.PublicSettings) { v.MediaIndexPath = filepath.Join(outside, "index.json") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			invalid := valid
			test.mutate(&invalid)
			if _, err := service.PutPublic(t.Context(), invalid); !errors.Is(err, ErrInvalidSettings) {
				t.Fatalf("PutPublic() error=%v", err)
			}
			view, err := service.Get(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if view.SettingsVersion != 1 || !reflect.DeepEqual(view.Public, valid) {
				t.Fatalf("invalid update was partial: %+v", view)
			}
		})
	}
}

func TestSettingsAllowsSpecificLANListenAddress(t *testing.T) {
	service, _, _, public := newSettingsTestService(t, Options{})
	public.ListenAddr = "192.168.10.25:2030"
	if version, err := service.PutPublic(t.Context(), public); err != nil || version != 1 {
		t.Fatalf("LAN listen address version=%d err=%v", version, err)
	}
}

func TestSettingsSecretSizeAndCorruptionErrorsDoNotExposeSecretMaterial(t *testing.T) {
	service, _, protector, public := newSettingsTestService(t, Options{})
	if _, err := service.PutPublic(t.Context(), public); err != nil {
		t.Fatal(err)
	}
	oversized := strings.Repeat("x", security.MaxSecretSize+1)
	if err := service.PutSecret(t.Context(), SecretGrokAPIKey, oversized); !errors.Is(err, security.ErrSecretTooLarge) {
		t.Fatalf("oversized error=%v", err)
	}
	if len(protector.protected) != 0 {
		t.Fatal("oversized secret reached protector")
	}
	if err := service.PutSecret(t.Context(), "unknown_secret", "value"); !errors.Is(err, ErrUnknownSecret) {
		t.Fatalf("unknown key error=%v", err)
	}
	if err := service.PutSecret(t.Context(), SecretGrokAPIKey, "do-not-echo"); err != nil {
		t.Fatal(err)
	}
	protector.corrupt = true
	_, err := service.Runtime(t.Context())
	if err == nil || strings.Contains(err.Error(), "do-not-echo") || strings.Contains(err.Error(), "cipher-boundary") {
		t.Fatalf("runtime error leaked secret material: %v", err)
	}
}

type failingProtector struct{}

func (failingProtector) Protect(value []byte) ([]byte, error) {
	return nil, errors.New("failed to protect " + string(value))
}
func (failingProtector) Unprotect(value []byte) ([]byte, error) {
	return nil, errors.New("failed to unprotect " + string(value))
}

func TestSettingsSanitizesInjectedProtectorErrors(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service := NewService(store.NewSettingsRepository(db), failingProtector{})
	err = service.PutSecret(t.Context(), SecretGrokAPIKey, "must-not-appear")
	if err == nil || strings.Contains(err.Error(), "must-not-appear") {
		t.Fatalf("protector error was not sanitized: %v", err)
	}
}

type failSecondProtector struct{ calls int }

func (p *failSecondProtector) Protect(value []byte) ([]byte, error) {
	p.calls++
	if p.calls == 2 {
		return nil, errors.New("second protection failed")
	}
	return append([]byte("cipher-boundary:"), value...), nil
}
func (*failSecondProtector) Unprotect(value []byte) ([]byte, error) {
	return bytes.TrimPrefix(value, []byte("cipher-boundary:")), nil
}

func TestSettingsUpdateRollsBackPublicAndAllSecretsWhenProtectionFails(t *testing.T) {
	service, db, _, public := newSettingsTestService(t, Options{})
	if _, err := service.PutPublic(t.Context(), public); err != nil {
		t.Fatal(err)
	}
	if err := service.PutSecret(t.Context(), SecretGrokAPIKey, "old-grok"); err != nil {
		t.Fatal(err)
	}
	updated := public
	updated.MaxCodexConcurrency = 4
	failing := NewService(store.NewSettingsRepository(db), &failSecondProtector{})
	_, err := failing.Update(t.Context(), updated, map[string]string{
		SecretGrokAPIKey:   "new-grok",
		SecretPexelsAPIKey: "new-pexels",
	})
	if err == nil {
		t.Fatal("Update() succeeded despite second protection failure")
	}
	values, version, err := store.NewSettingsRepository(db).Public(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if version != 1 || values["max_codex_concurrency"] != "2" {
		t.Fatalf("public settings changed after protection failure: version=%d values=%v", version, values)
	}
	secret, err := store.NewSettingsRepository(db).Secret(t.Context(), SecretGrokAPIKey)
	if err != nil || secret.Version != 1 {
		t.Fatalf("grok secret changed after protection failure: %+v err=%v", secret, err)
	}
	if _, err := store.NewSettingsRepository(db).Secret(t.Context(), SecretPexelsAPIKey); !errors.Is(err, store.ErrSecretNotFound) {
		t.Fatalf("pexels secret persisted after protection failure: %v", err)
	}
}

type blockingProtector struct {
	entered chan struct{}
	release chan struct{}
}

func (p *blockingProtector) Protect(value []byte) ([]byte, error) {
	close(p.entered)
	<-p.release
	return append([]byte("cipher-boundary:"), value...), nil
}
func (*blockingProtector) Unprotect(value []byte) ([]byte, error) {
	return bytes.TrimPrefix(value, []byte("cipher-boundary:")), nil
}

func TestConcurrentSettingsUpdatesNeverMixPublicAndSecretValues(t *testing.T) {
	_, db, _, public := newSettingsTestService(t, Options{})
	repo := store.NewSettingsRepository(db)
	blocker := &blockingProtector{entered: make(chan struct{}), release: make(chan struct{})}
	serviceA := NewService(repo, blocker)
	serviceB := NewService(repo, &fakeProtector{})
	publicA, publicB := public, public
	publicA.GrokModel = "model-A"
	publicB.GrokModel = "model-B"
	errorsA := make(chan error, 1)
	go func() {
		_, err := serviceA.Update(t.Context(), publicA, map[string]string{SecretGrokAPIKey: "secret-A"})
		errorsA <- err
	}()
	<-blocker.entered
	if _, err := serviceB.Update(t.Context(), publicB, map[string]string{SecretGrokAPIKey: "secret-B"}); err != nil {
		t.Fatal(err)
	}
	close(blocker.release)
	if err := <-errorsA; err != nil {
		t.Fatal(err)
	}
	values, _, err := repo.Public(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	secret, err := repo.Secret(t.Context(), SecretGrokAPIKey)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(secret.Ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	plain := string(bytes.TrimPrefix(ciphertext, []byte("cipher-boundary:")))
	if (values["grok_model"] == "model-A") != (plain == "secret-A") {
		t.Fatalf("mixed settings generation: model=%q secret=%q", values["grok_model"], plain)
	}
}

type runnerCall struct {
	executable string
	args       []string
}

type deadlineRunner struct{ sawDeadline bool }

func (runner *deadlineRunner) Run(ctx context.Context, _ string, _ ...string) ([]byte, error) {
	_, runner.sawDeadline = ctx.Deadline()
	return []byte("codex 1.0"), nil
}

func TestCommandDependencyProbeAddsDeadline(t *testing.T) {
	runner := &deadlineRunner{}
	service, _, _, public := newSettingsTestService(t, Options{Runner: runner})
	if _, err := service.PutPublic(t.Context(), public); err != nil {
		t.Fatal(err)
	}
	if health := service.TestDependency(t.Context(), "codex"); health.Status != HealthOK {
		t.Fatalf("codex health=%+v", health)
	}
	if !runner.sawDeadline {
		t.Fatal("command probe context had no deadline")
	}
}

func TestBoundedCommandOutputDiscardsBytesPastLimit(t *testing.T) {
	buffer := newBoundedOutput(maxCommandOutputSize)
	input := bytes.Repeat([]byte("x"), maxCommandOutputSize*2)
	if _, err := buffer.Write(input); err != nil {
		t.Fatal(err)
	}
	if len(buffer.Bytes()) != maxCommandOutputSize {
		t.Fatalf("bounded output size=%d", len(buffer.Bytes()))
	}
}

type fakeRunner struct {
	calls []runnerCall
}

func (f *fakeRunner) Run(_ context.Context, executable string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, runnerCall{executable, append([]string(nil), args...)})
	if reflect.DeepEqual(args, []string{"mcp", "list"}) {
		return []byte("baokuan enabled"), nil
	}
	return []byte("configured"), nil
}

type fakeHTTPClient struct {
	urls []string
}

func (f *fakeHTTPClient) Do(request *http.Request) (*http.Response, error) {
	f.urls = append(f.urls, request.URL.String())
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"materials":[]}`)), Header: make(http.Header)}, nil
}

type countingReadCloser struct {
	reader io.Reader
	read   int64
}

func (r *countingReadCloser) Read(buffer []byte) (int, error) {
	n, err := r.reader.Read(buffer)
	r.read += int64(n)
	return n, err
}
func (r *countingReadCloser) Close() error { return nil }

type grokProbeHTTPClient struct {
	status  int
	body    *countingReadCloser
	request *http.Request
	err     error
}

func (c *grokProbeHTTPClient) Do(request *http.Request) (*http.Response, error) {
	c.request = request.Clone(request.Context())
	if c.err != nil {
		return nil, c.err
	}
	return &http.Response{StatusCode: c.status, Body: c.body, Header: make(http.Header)}, nil
}

func TestSettingsDependencyProbeAndRepairUseInjectedClientsAndArgumentArrays(t *testing.T) {
	runner := &fakeRunner{}
	httpClient := &fakeHTTPClient{}
	service, _, _, public := newSettingsTestService(t, Options{Runner: runner, HTTPClient: httpClient})
	if _, err := service.PutPublic(t.Context(), public); err != nil {
		t.Fatal(err)
	}

	health := service.TestDependency(t.Context(), "baokuan")
	if health.Status != HealthOK || health.Message == "" || len(httpClient.urls) != 1 {
		t.Fatalf("baokuan health=%+v urls=%v", health, httpClient.urls)
	}
	if len(runner.calls) != 1 || !reflect.DeepEqual(runner.calls[0].args, []string{"mcp", "list"}) {
		t.Fatalf("probe runner calls=%+v", runner.calls)
	}

	health = service.RepairBaokuanMCP(t.Context())
	if health.Status != HealthOK {
		t.Fatalf("repair health=%+v", health)
	}
	wantRepair := []string{"mcp", "add", "baokuan", "--", public.BaokuanMCPExecutable, "mcp", "--base", public.BaokuanBaseURL}
	if len(runner.calls) != 3 || runner.calls[1].executable != public.CodexBinaryPath || !reflect.DeepEqual(runner.calls[1].args, wantRepair) || !reflect.DeepEqual(runner.calls[2].args, []string{"mcp", "list"}) {
		t.Fatalf("repair runner calls=%+v", runner.calls)
	}
	encoded, _ := json.Marshal(health)
	for _, forbidden := range []string{"secret-value", "environment", "argv", "configured"} {
		if bytes.Contains(bytes.ToLower(encoded), []byte(forbidden)) {
			t.Fatalf("health response exposed internals: %s", encoded)
		}
	}
}

func TestGrokDependencyProbeUsesOpenAIAuthenticationAndBoundsResponse(t *testing.T) {
	body := &countingReadCloser{reader: strings.NewReader(strings.Repeat("x", 128<<10))}
	client := &grokProbeHTTPClient{status: http.StatusOK, body: body}
	service, _, _, public := newSettingsTestService(t, Options{HTTPClient: client})
	if _, err := service.PutPublic(t.Context(), public); err != nil {
		t.Fatal(err)
	}
	if err := service.PutSecret(t.Context(), SecretGrokAPIKey, "grok-secret"); err != nil {
		t.Fatal(err)
	}
	health := service.TestDependency(t.Context(), "grok")
	if health.Status != HealthOK {
		t.Fatalf("grok health=%+v", health)
	}
	if client.request == nil || client.request.Method != http.MethodGet || client.request.URL.Path != "/v1/models" || client.request.Header.Get("Authorization") != "Bearer grok-secret" {
		t.Fatalf("grok probe request=%+v", client.request)
	}
	if body.read > 64<<10 {
		t.Fatalf("probe read %d bytes, want bounded body", body.read)
	}
}

func TestGrokDependencyProbeDistinguishesMissingAndBadCredentials(t *testing.T) {
	missingClient := &grokProbeHTTPClient{status: http.StatusOK, body: &countingReadCloser{reader: strings.NewReader(`{}`)}}
	service, db, _, public := newSettingsTestService(t, Options{HTTPClient: missingClient})
	if _, err := service.PutPublic(t.Context(), public); err != nil {
		t.Fatal(err)
	}
	if health := service.TestDependency(t.Context(), "grok"); health.Status != HealthNotConfigured {
		t.Fatalf("missing key health=%+v", health)
	}

	badClient := &grokProbeHTTPClient{status: http.StatusUnauthorized, body: &countingReadCloser{reader: strings.NewReader(`{"error":"invalid_api_key"}`)}}
	service = NewService(store.NewSettingsRepository(db), &fakeProtector{}, Options{HTTPClient: badClient})
	if err := service.PutSecret(t.Context(), SecretGrokAPIKey, "bad-secret"); err != nil {
		t.Fatal(err)
	}
	if health := service.TestDependency(t.Context(), "grok"); health.Status != HealthOffline || strings.Contains(strings.ToLower(health.Message), "api") {
		t.Fatalf("bad key health=%+v", health)
	}

	offlineClient := &grokProbeHTTPClient{err: errors.New("dial failed with bad-secret")}
	service = NewService(store.NewSettingsRepository(db), &fakeProtector{}, Options{HTTPClient: offlineClient})
	if health := service.TestDependency(t.Context(), "grok"); health.Status != HealthOffline || strings.Contains(health.Message, "bad-secret") {
		t.Fatalf("offline health=%+v", health)
	}
}

func TestHTTPClientRedirectsAreDisabledWithoutMutatingInjectedClient(t *testing.T) {
	original := &http.Client{}
	hardened, ok := noRedirectHTTPClient(original).(*http.Client)
	if !ok || hardened == original || hardened.CheckRedirect == nil {
		t.Fatalf("hardened client=%T %#v", hardened, hardened)
	}
	if err := hardened.CheckRedirect(httptest.NewRequest(http.MethodGet, "http://example.test", nil), nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("CheckRedirect() error=%v", err)
	}
	if original.CheckRedirect != nil {
		t.Fatal("injected client was mutated")
	}
}

func TestCodexTaskProjectRootRoundTripsAndValidates(t *testing.T) {
	service, _, _, public := newSettingsTestService(t, Options{})
	public.CodexTaskProjectRoot = filepath.Join(t.TempDir(), "Documents", "杂项")
	if _, err := service.PutPublic(t.Context(), public); err != nil {
		t.Fatal(err)
	}
	view, err := service.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if view.Public.CodexTaskProjectRoot != public.CodexTaskProjectRoot {
		t.Fatalf("task project root=%q want %q", view.Public.CodexTaskProjectRoot, public.CodexTaskProjectRoot)
	}
	for _, invalidRoot := range []string{"relative\\misc", public.CodexTaskProjectRoot + string(filepath.Separator) + ".." + string(filepath.Separator) + "other"} {
		invalid := public
		invalid.CodexTaskProjectRoot = invalidRoot
		if _, err := service.PutPublic(t.Context(), invalid); !errors.Is(err, ErrInvalidSettings) {
			t.Fatalf("root %q error=%v, want invalid settings", invalidRoot, err)
		}
	}
}

func TestCodexTaskProjectRootRequiresRestart(t *testing.T) {
	service, _, _, public := newSettingsTestService(t, Options{})
	if _, err := service.PutPublic(t.Context(), public); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Runtime(t.Context()); err != nil {
		t.Fatal(err)
	}
	updated := public
	updated.CodexTaskProjectRoot = filepath.Join(t.TempDir(), "misc")
	view, err := service.Update(t.Context(), updated, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if !view.RestartRequired {
		t.Fatal("restart_required=false after Codex task project root change")
	}
	if view.ActivePublic.CodexTaskProjectRoot != public.CodexTaskProjectRoot || view.Public.CodexTaskProjectRoot != updated.CodexTaskProjectRoot {
		t.Fatalf("configured/active roots=%q/%q", view.Public.CodexTaskProjectRoot, view.ActivePublic.CodexTaskProjectRoot)
	}
}

func newSettingsTestService(t *testing.T, options Options) (*Service, *sql.DB, *fakeProtector, domain.PublicSettings) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	protector := &fakeProtector{}
	root := t.TempDir()
	dataRoot := filepath.Join(root, "data")
	vault := filepath.Join(root, "vault")
	mediaRoot := filepath.Join(root, "media")
	public := domain.PublicSettings{
		ListenAddr: "127.0.0.1:2030", DataRoot: dataRoot, MaxCodexConcurrency: 2,
		CodexDefaultModel: taskmodel.DefaultModel, CodexDefaultReasoningEffort: taskmodel.DefaultReasoningEffort,
		BaokuanBaseURL: "http://127.0.0.1:2022", BaokuanMCPExecutable: filepath.Join(root, "baokuan.exe"),
		ObsidianVault: vault, TopicCardsDir: filepath.Join(vault, "topic-cards"),
		GrokBaseURL: "http://127.0.0.1:3030", GrokModel: "grok-test",
		CodexBinaryPath: filepath.Join(root, "codex.exe"), MediaIndexPath: filepath.Join(mediaRoot, "media-index.json"),
		MediaRoot: mediaRoot, JianyingRoot: filepath.Join(root, "jianying"), CodexHistoryLimit: 10,
	}
	return NewService(store.NewSettingsRepository(db), protector, options), db, protector, public
}
