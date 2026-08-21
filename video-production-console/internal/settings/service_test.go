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
	"os"
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

func TestResolveTaskModelPrefersRemixModel(t *testing.T) {
	service, _, _, public := newSettingsTestService(t, Options{})
	public.CodexDefaultModel = "cursor-grok-4.6-xhigh-fast"
	public.RemixModel = "gpt-5.6-sol"
	if _, err := service.PutPublic(t.Context(), public); err != nil {
		t.Fatal(err)
	}
	selection, err := service.ResolveTaskModel(t.Context(), taskmodel.Selection{Kind: taskmodel.KindRemix})
	if err != nil {
		t.Fatal(err)
	}
	if selection.Model != "gpt-5.6-sol" {
		t.Fatalf("ResolveTaskModel()=%+v, want remix model", selection)
	}
}

func TestResolveTaskModelSpokenLinesFallsBackToRemixModel(t *testing.T) {
	service, _, _, public := newSettingsTestService(t, Options{})
	public.CodexDefaultModel = "gpt-5.6-sol"
	public.RemixModel = "cursor-grok-4.6-xhigh-fast"
	if _, err := service.PutPublic(t.Context(), public); err != nil {
		t.Fatal(err)
	}
	selection, err := service.ResolveTaskModel(t.Context(), taskmodel.Selection{Kind: taskmodel.KindSpokenLines})
	if err != nil {
		t.Fatal(err)
	}
	if selection.Model != "cursor-grok-4.6-xhigh-fast" {
		t.Fatalf("ResolveTaskModel()=%+v, want remix model fallback", selection)
	}

	public.SpokenLinesModel = "claude-sonnet-4-6"
	if _, err := service.PutPublic(t.Context(), public); err != nil {
		t.Fatal(err)
	}
	selection, err = service.ResolveTaskModel(t.Context(), taskmodel.Selection{Kind: taskmodel.KindSpokenLines})
	if err != nil {
		t.Fatal(err)
	}
	if selection.Model != "claude-sonnet-4-6" {
		t.Fatalf("ResolveTaskModel()=%+v, want dedicated 口播稿 model", selection)
	}
}

func TestResolveTaskModelUsesCodexDefaultForMontage(t *testing.T) {
	service, _, _, public := newSettingsTestService(t, Options{})
	public.CodexDefaultModel = "gpt-5.6-sol"
	public.RemixModel = "cursor-grok-4.6-xhigh-fast"
	if _, err := service.PutPublic(t.Context(), public); err != nil {
		t.Fatal(err)
	}
	selection, err := service.ResolveTaskModel(t.Context(), taskmodel.Selection{Kind: taskmodel.KindCodex})
	if err != nil {
		t.Fatal(err)
	}
	if selection.Model != "gpt-5.6-sol" {
		t.Fatalf("ResolveTaskModel()=%+v, want Codex default", selection)
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

func TestRuntimeStartsWhenCodexBinaryIsMissing(t *testing.T) {
	service, _, _, public := newSettingsTestService(t, Options{})
	if err := service.InitializeBootSettings(t.Context(), BootSettings{DataRoot: public.DataRoot}); err != nil {
		t.Fatal(err)
	}
	runtime, err := service.Runtime(t.Context())
	if err != nil {
		t.Fatalf("Runtime() error=%v, want boot without Codex to succeed", err)
	}
	if runtime.DataRoot != public.DataRoot {
		t.Fatalf("data root=%q", runtime.DataRoot)
	}
	if runtime.CodexBinaryPath != "" {
		t.Fatalf("codex path=%q, want empty when it was never configured", runtime.CodexBinaryPath)
	}
}

func TestRuntimeSkipsSecretsThatCannotBeDecrypted(t *testing.T) {
	service, db, _, public := newSettingsTestService(t, Options{})
	if _, err := service.PutPublic(t.Context(), public); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO encrypted_secrets(key,ciphertext,version,updated_at) VALUES(?,?,?,?)`, SecretRemixAPIKey, "not-valid-ciphertext", 1, "2026-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	runtime, err := service.Runtime(t.Context())
	if err != nil {
		t.Fatalf("Runtime() error=%v, want undecryptable secrets to be skipped", err)
	}
	if runtime.RemixAPIKey != "" {
		t.Fatalf("remix key=%q, want empty after decrypt failure", runtime.RemixAPIKey)
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

func TestMontageStyleRoundTripsAndValidates(t *testing.T) {
	service, _, _, public := newSettingsTestService(t, Options{})
	public.MontageStyle = domain.MontageStyle{
		CaptionSize: 24, CaptionColor: "#ffffff", CaptionPosition: "bottom",
		KeywordColor: "#ff0000", BGMID: "abc123", BGMVolume: 0.4,
	}
	public.BGMDir = filepath.Join(t.TempDir(), "bgm")
	if _, err := service.PutPublic(t.Context(), public); err != nil {
		t.Fatalf("PutPublic() error=%v", err)
	}
	view, err := service.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	got := view.Public.MontageStyle
	if got.CaptionSize != 24 || got.CaptionColor != "#FFFFFF" || got.CaptionPosition != "bottom" {
		t.Fatalf("caption style did not round trip: %+v", got)
	}
	if got.KeywordSize != 23 || got.KeywordColor != "#FF0000" || got.TitleSize != 16 {
		t.Fatalf("defaults not filled: %+v", got)
	}
	if got.BGMID != "abc123" || got.BGMVolume != 0.4 || view.Public.BGMDir != public.BGMDir {
		t.Fatalf("bgm selection did not round trip: %+v dir=%q", got, view.Public.BGMDir)
	}
	if got.CaptionTransformY() != -0.3 {
		t.Fatalf("bottom position must map to -0.3, got %v", got.CaptionTransformY())
	}
	invalidStyles := []domain.MontageStyle{
		{CaptionSize: 200},
		{CaptionColor: "red"},
		{CaptionPosition: "top"},
		{CaptionFont: "不存在的字体"},
		{BGMVolume: 3},
		{CaptionPosition: "custom", CaptionY: 2},
	}
	for i, style := range invalidStyles {
		bad := public
		bad.MontageStyle = style
		if _, err := service.PutPublic(t.Context(), bad); !errors.Is(err, ErrInvalidSettings) {
			t.Fatalf("invalid style %d accepted: %v", i, err)
		}
	}
	bad := public
	bad.BGMDir = "relative\\bgm"
	if _, err := service.PutPublic(t.Context(), bad); !errors.Is(err, ErrInvalidSettings) {
		t.Fatalf("relative bgm_dir accepted: %v", err)
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
	// A corrupted secret is skipped (treated as unconfigured) so the console
	// still boots, and nothing of the secret material may surface anywhere.
	runtime, err := service.Runtime(t.Context())
	if err != nil {
		if strings.Contains(err.Error(), "do-not-echo") || strings.Contains(err.Error(), "cipher-boundary") {
			t.Fatalf("runtime error leaked secret material: %v", err)
		}
		t.Fatalf("undecryptable secret must be skipped, not fail runtime: %v", err)
	}
	if runtime.GrokAPIKey != "" {
		t.Fatal("corrupted secret must not decode into runtime material")
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

func TestSettingsVolcSpeechKeepsAPIKeyOutOfTheViewAndPublishesTheIDs(t *testing.T) {
	service, _, _, public := newSettingsTestService(t, Options{})
	public.VolcSpeechSpeakerID = "S_volc_speaker"
	public.VolcSpeechResourceID = "seed-icl-2.0"
	view, err := service.Update(t.Context(), public, map[string]string{SecretVolcSpeechAPIKey: "volc-value"})
	if err != nil {
		t.Fatal(err)
	}
	if view.Public.VolcSpeechSpeakerID != public.VolcSpeechSpeakerID || view.Public.VolcSpeechResourceID != public.VolcSpeechResourceID {
		t.Fatalf("public volc speech settings=%+v", view.Public)
	}
	if !view.Secrets[SecretVolcSpeechAPIKey].Configured || view.Secrets[SecretVolcSpeechAPIKey].Masked == "" {
		t.Fatalf("secret status=%+v", view.Secrets)
	}
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("volc-value")) {
		t.Fatalf("HTTP view leaked the Volcengine key: %s", raw)
	}

	runtime, err := service.Runtime(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.VolcSpeechAPIKey != "volc-value" || runtime.VolcSpeechSpeakerID != public.VolcSpeechSpeakerID || runtime.VolcSpeechResourceID != public.VolcSpeechResourceID {
		t.Fatalf("runtime=%+v", runtime)
	}
	runtimeJSON, err := json.Marshal(runtime)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(runtimeJSON, []byte("volc-value")) {
		t.Fatalf("runtime JSON leaked the Volcengine key: %s", runtimeJSON)
	}
}

func TestSettingsAuraSTDVoiceRoundTripKeepsTheKeyMasked(t *testing.T) {
	service, _, _, public := newSettingsTestService(t, Options{})
	public.TTSProvider = "aurastd"
	public.AuraSTDVoiceID = "moss_audio_6b1797c8-2329-11f1-8c29-36c83b29da67"
	public.AuraSTDSpeed = 1.21
	public.AuraSTDVolume = 1.4
	public.AuraSTDPitch = 1
	public.AuraSTDModifyIntensity = 5
	public.AuraSTDModifyTimbre = 6
	view, err := service.Update(t.Context(), public, map[string]string{SecretAuraSTDTTsAPIKey: "aurastd-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if view.Public.AuraSTDSpeed != 1.21 || view.Public.AuraSTDVolume != 1.4 || view.Public.AuraSTDModifyTimbre != 6 {
		t.Fatalf("public aurastd settings=%+v", view.Public)
	}
	if !view.Secrets[SecretAuraSTDTTsAPIKey].Configured || view.Secrets[SecretAuraSTDTTsAPIKey].Masked == "" {
		t.Fatalf("secret status=%+v", view.Secrets)
	}
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("aurastd-secret")) {
		t.Fatalf("HTTP view leaked the Aura Studio key: %s", raw)
	}
	runtime, err := service.Runtime(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.AuraSTDTTsAPIKey != "aurastd-secret" || runtime.AuraSTDVoiceID != public.AuraSTDVoiceID {
		t.Fatalf("runtime=%+v", runtime)
	}
}

// The narration feature is optional, so an unconfigured voice must never block
// an unrelated settings save.
func TestSettingsAcceptEmptyVolcSpeechIDs(t *testing.T) {
	service, _, _, public := newSettingsTestService(t, Options{})
	view, err := service.Update(t.Context(), public, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if view.Public.VolcSpeechSpeakerID != "" || view.Public.VolcSpeechResourceID != "" {
		t.Fatalf("empty volc speech settings=%+v", view.Public)
	}
	if view.Secrets[SecretVolcSpeechAPIKey].Configured || view.Secrets[SecretVolcSpeechAPIKey].Masked != "" {
		t.Fatalf("unconfigured secret status=%+v", view.Secrets[SecretVolcSpeechAPIKey])
	}
}

func TestSettingsImageGenerationConfigurationIsEncryptedAndRuntimeOnly(t *testing.T) {
	service, db, protector, public := newSettingsTestService(t, Options{})
	public.ImageBaseURL = "http://127.0.0.1:8320/v1"
	public.ImageModel = "gpt-image-2"
	public.MaxImageConcurrency = 3
	const imageKey = "test-image-key"
	view, err := service.Update(t.Context(), public, map[string]string{SecretImageAPIKey: imageKey})
	if err != nil {
		t.Fatal(err)
	}
	if view.Public.ImageBaseURL != public.ImageBaseURL || view.Public.ImageModel != public.ImageModel || view.Public.MaxImageConcurrency != 3 {
		t.Fatalf("public image settings=%+v", view.Public)
	}
	if !view.Secrets[SecretImageAPIKey].Configured || view.Secrets[SecretImageAPIKey].Masked != secretMask {
		t.Fatalf("image secret status=%+v", view.Secrets[SecretImageAPIKey])
	}
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(imageKey)) || bytes.Contains(raw, []byte("cipher-boundary")) {
		t.Fatalf("settings view leaked image API key: %s", raw)
	}
	var storedCiphertext string
	if err := db.QueryRow(`SELECT ciphertext FROM encrypted_secrets WHERE key=?`, SecretImageAPIKey).Scan(&storedCiphertext); err != nil {
		t.Fatal(err)
	}
	if storedCiphertext == "" || strings.Contains(storedCiphertext, imageKey) {
		t.Fatal("image API key was not stored as opaque ciphertext")
	}
	if len(protector.protected) != 1 || string(protector.protected[0]) != imageKey {
		t.Fatalf("image key protection calls=%d", len(protector.protected))
	}
	runtime, err := service.Runtime(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.ImageAPIKey != imageKey || runtime.ImageBaseURL != public.ImageBaseURL || runtime.ImageModel != public.ImageModel || runtime.MaxImageConcurrency != 3 {
		t.Fatalf("image runtime base=%q model=%q concurrency=%d key_configured=%t", runtime.ImageBaseURL, runtime.ImageModel, runtime.MaxImageConcurrency, runtime.ImageAPIKey != "")
	}
	runtimeJSON, err := json.Marshal(runtime)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(runtimeJSON, []byte(imageKey)) {
		t.Fatalf("runtime JSON leaked image API key: %s", runtimeJSON)
	}
}

func TestSettingsImageGenerationAttemptsDefaultRoundTripAndValidation(t *testing.T) {
	service, _, _, public := newSettingsTestService(t, Options{})
	public.ImageGenerationAttempts = 4
	view, err := service.Update(t.Context(), public, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if view.Public.ImageGenerationAttempts != 4 {
		t.Fatalf("attempts=%d", view.Public.ImageGenerationAttempts)
	}
	public.ImageGenerationAttempts = 1
	view, err = service.Update(t.Context(), public, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if view.Public.ImageGenerationAttempts != 1 {
		t.Fatalf("attempts=%d", view.Public.ImageGenerationAttempts)
	}
	legacy := public
	legacy.ImageGenerationAttempts = 0
	view, err = service.Update(t.Context(), legacy, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if view.Public.ImageGenerationAttempts != defaultImageGenerationAttempts {
		t.Fatalf("legacy zero attempts=%d", view.Public.ImageGenerationAttempts)
	}
	for _, invalidValue := range []int{-1, 5} {
		candidate := public
		candidate.ImageGenerationAttempts = invalidValue
		if _, err := service.PutPublic(t.Context(), candidate); !errors.Is(err, ErrInvalidSettings) {
			t.Fatalf("attempts=%d error=%v", invalidValue, err)
		}
	}
}

func TestLegacySettingsDefaultImageGenerationValues(t *testing.T) {
	service, db, _, _ := newSettingsTestService(t, Options{})
	if _, err := db.Exec(`DELETE FROM settings WHERE key IN ('image_base_url','image_model','max_image_concurrency','image_generation_attempts')`); err != nil {
		t.Fatal(err)
	}
	view, err := service.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if view.Public.ImageBaseURL != "" || view.Public.ImageModel != defaultImageModel || view.Public.MaxImageConcurrency != defaultMaxImageConcurrency || view.Public.DefaultImageRatio != defaultImageRatio || view.Public.DefaultImageStyle != defaultImageStyle || view.Public.ImageGenerationAttempts != defaultImageGenerationAttempts {
		t.Fatalf("legacy image defaults=%+v", view.Public)
	}
}

func TestLegacySettingsUpdateAppliesImageGenerationDefaults(t *testing.T) {
	service, _, _, legacy := newSettingsTestService(t, Options{})
	legacy.ImageModel = ""
	legacy.MaxImageConcurrency = 0
	legacy.DefaultImageRatio = ""
	legacy.DefaultImageStyle = ""

	view, err := service.Update(t.Context(), legacy, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if view.Public.ImageModel != defaultImageModel || view.Public.MaxImageConcurrency != defaultMaxImageConcurrency || view.Public.DefaultImageRatio != defaultImageRatio || view.Public.DefaultImageStyle != defaultImageStyle {
		t.Fatalf("legacy update image defaults=%+v", view.Public)
	}
}

func TestSettingsImageGenerationValidationAllowsHTTPAndRejectsInvalidValues(t *testing.T) {
	service, _, _, valid := newSettingsTestService(t, Options{})
	for _, baseURL := range []string{"http://images.example.test/v1", "https://images.example.test/v1"} {
		candidate := valid
		candidate.ImageBaseURL = baseURL
		if _, err := service.PutPublic(t.Context(), candidate); err != nil {
			t.Fatalf("base URL %q rejected: %v", baseURL, err)
		}
	}
	tests := []struct {
		name   string
		mutate func(*domain.PublicSettings)
	}{
		{"too much concurrency", func(value *domain.PublicSettings) { value.MaxImageConcurrency = 19 }},
		{"trimmed model", func(value *domain.PublicSettings) { value.ImageModel = " gpt-image-2 " }},
		{"invalid default ratio", func(value *domain.PublicSettings) { value.DefaultImageRatio = "16:9" }},
		{"invalid default style", func(value *domain.PublicSettings) { value.DefaultImageStyle = "unknown" }},
		{"userinfo URL", func(value *domain.PublicSettings) { value.ImageBaseURL = "https://user@images.example.test/v1" }},
		{"fragment URL", func(value *domain.PublicSettings) { value.ImageBaseURL = "https://images.example.test/v1#fragment" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.mutate(&candidate)
			if _, err := service.PutPublic(t.Context(), candidate); !errors.Is(err, ErrInvalidSettings) {
				t.Fatalf("PutPublic() error=%v", err)
			}
		})
	}
}

func TestSettingsMediaIntelligenceDefaultsFillMissingValues(t *testing.T) {
	service, _, _, public := newSettingsTestService(t, Options{})
	legacy := public
	legacy.PexelsAPIBaseURL = ""
	legacy.PixabayAPIBaseURL = ""
	legacy.MaxExternalResultsPerQuery = 0
	view, err := service.Update(t.Context(), legacy, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if view.Public.PexelsAPIBaseURL != "https://api.pexels.com" || view.Public.PixabayAPIBaseURL != "https://pixabay.com" {
		t.Fatalf("provider base defaults=%q/%q", view.Public.PexelsAPIBaseURL, view.Public.PixabayAPIBaseURL)
	}
	if view.Public.MaxExternalResultsPerQuery != 20 {
		t.Fatalf("max_external_results_per_query=%d, want default 20", view.Public.MaxExternalResultsPerQuery)
	}
}

func TestRuntimeClearsStaleFFmpegPathSoConsoleCanBoot(t *testing.T) {
	service, _, _, public := newSettingsTestService(t, Options{})
	if _, err := service.PutPublic(t.Context(), public); err != nil {
		t.Fatal(err)
	}
	public.FFmpegPath = filepath.Join(t.TempDir(), "missing-ffmpeg.exe")
	public.FFprobePath = filepath.Join(t.TempDir(), "missing-ffprobe.exe")
	if _, err := service.repo.UpdatePublic(t.Context(), publicValues(public)); err != nil {
		t.Fatal(err)
	}

	runtime, err := service.Runtime(t.Context())
	if err != nil {
		t.Fatalf("Runtime() error=%v, want stale ffmpeg/ffprobe to be cleared instead of blocking boot", err)
	}
	if runtime.FFmpegPath != "" || runtime.FFprobePath != "" {
		t.Fatalf("runtime binaries=%q/%q, want cleared", runtime.FFmpegPath, runtime.FFprobePath)
	}
	view, err := service.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if view.Public.FFmpegPath != "" || view.Public.FFprobePath != "" {
		t.Fatalf("persisted binaries=%q/%q, want cleared so the next boot stays valid", view.Public.FFmpegPath, view.Public.FFprobePath)
	}
}

func TestSettingsMediaIntelligenceRoundTripsValidConfiguration(t *testing.T) {
	service, _, _, public := newSettingsTestService(t, Options{})
	binaries := t.TempDir()
	ffmpeg := filepath.Join(binaries, "ffmpeg.exe")
	ffprobe := filepath.Join(binaries, "ffprobe.exe")
	for _, path := range []string{ffmpeg, ffprobe} {
		if err := os.WriteFile(path, []byte("binary"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	public.MediaCatalogPath = filepath.Join(public.MediaRoot, "catalog.db")
	public.FFmpegPath = ffmpeg
	public.FFprobePath = ffprobe
	public.VisionBaseURL = "https://vision.example.test/v1"
	public.VisionModel = "vision-x"
	public.EmbeddingBaseURL = "https://embedding.example.test/v1"
	public.EmbeddingModel = "embed-y"
	public.MaxExternalResultsPerQuery = 50
	view, err := service.Update(t.Context(), public, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(view.Public, public) {
		t.Fatalf("round trip=%+v want %+v", view.Public, public)
	}
	runtime, err := service.Runtime(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.MediaCatalogPath != public.MediaCatalogPath || runtime.FFmpegPath != ffmpeg || runtime.VisionModel != "vision-x" || runtime.EmbeddingBaseURL != public.EmbeddingBaseURL {
		t.Fatalf("runtime media settings=%+v", runtime.PublicSettings)
	}
}

func TestSettingsMediaIntelligenceValidationRejectsUnsafeValues(t *testing.T) {
	service, _, _, valid := newSettingsTestService(t, Options{})
	binaries := t.TempDir()
	ffmpeg := filepath.Join(binaries, "ffmpeg.exe")
	if err := os.WriteFile(ffmpeg, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	valid.FFmpegPath = ffmpeg
	if _, err := service.PutPublic(t.Context(), valid); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	tests := []struct {
		name   string
		mutate func(*domain.PublicSettings)
	}{
		{"catalog outside media root", func(v *domain.PublicSettings) { v.MediaCatalogPath = filepath.Join(outside, "catalog.db") }},
		{"catalog relative", func(v *domain.PublicSettings) { v.MediaCatalogPath = "catalog.db" }},
		{"catalog without media root", func(v *domain.PublicSettings) {
			v.MediaCatalogPath = filepath.Join(v.MediaRoot, "catalog.db")
			v.MediaIndexPath = ""
			v.MediaRoot = ""
		}},
		{"ffmpeg relative", func(v *domain.PublicSettings) { v.FFmpegPath = "ffmpeg.exe" }},
		{"ffmpeg missing", func(v *domain.PublicSettings) { v.FFmpegPath = filepath.Join(outside, "missing-ffmpeg.exe") }},
		{"ffmpeg directory", func(v *domain.PublicSettings) { v.FFmpegPath = outside }},
		{"ffprobe missing", func(v *domain.PublicSettings) { v.FFprobePath = filepath.Join(outside, "missing-ffprobe.exe") }},
		{"pexels plain http", func(v *domain.PublicSettings) { v.PexelsAPIBaseURL = "http://api.pexels.com" }},
		{"pexels foreign host", func(v *domain.PublicSettings) { v.PexelsAPIBaseURL = "https://api.pexels.com.evil.test" }},
		{"pexels other host", func(v *domain.PublicSettings) { v.PexelsAPIBaseURL = "https://example.com" }},
		{"pixabay subdomain", func(v *domain.PublicSettings) { v.PixabayAPIBaseURL = "https://api.pixabay.com" }},
		{"pixabay plain http", func(v *domain.PublicSettings) { v.PixabayAPIBaseURL = "http://pixabay.com" }},
		{"external results above cap", func(v *domain.PublicSettings) { v.MaxExternalResultsPerQuery = 51 }},
		{"external results negative", func(v *domain.PublicSettings) { v.MaxExternalResultsPerQuery = -1 }},
		{"vision URL with query", func(v *domain.PublicSettings) { v.VisionBaseURL = "https://vision.example.test/v1?key=leak" }},
		{"vision URL not http", func(v *domain.PublicSettings) { v.VisionBaseURL = "ftp://vision.example.test" }},
		{"vision URL too long", func(v *domain.PublicSettings) {
			v.VisionBaseURL = "https://vision.example.test/" + strings.Repeat("a", 2049)
		}},
		{"embedding URL with fragment", func(v *domain.PublicSettings) { v.EmbeddingBaseURL = "https://embedding.example.test/v1#frag" }},
		{"vision model untrimmed", func(v *domain.PublicSettings) { v.VisionModel = " vision-x " }},
		{"embedding model too long", func(v *domain.PublicSettings) { v.EmbeddingModel = strings.Repeat("m", 129) }},
		{"tts provider unknown", func(v *domain.PublicSettings) { v.TTSProvider = "minimax" }},
		{"aurastd speed too fast", func(v *domain.PublicSettings) { v.AuraSTDSpeed = 2.5 }},
		{"aurastd volume too loud", func(v *domain.PublicSettings) { v.AuraSTDVolume = 11 }},
		{"aurastd sound effect unknown", func(v *domain.PublicSettings) { v.AuraSTDSoundEffects = "echo" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			invalid := valid
			test.mutate(&invalid)
			if _, err := service.PutPublic(t.Context(), invalid); !errors.Is(err, ErrInvalidSettings) {
				t.Fatalf("PutPublic() error=%v, want invalid settings", err)
			}
		})
	}
}

func TestSettingsMediaIntelligenceSecretsStayEncryptedAndRuntimeOnly(t *testing.T) {
	service, db, _, public := newSettingsTestService(t, Options{})
	secrets := map[string]string{
		SecretVisionAPIKey:    "vision-secret-value",
		SecretEmbeddingAPIKey: "embedding-secret-value",
		SecretPixabayAPIKey:   "pixabay-secret-value",
	}
	view, err := service.Update(t.Context(), public, secrets)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range secrets {
		if !view.Secrets[key].Configured || view.Secrets[key].Masked != secretMask {
			t.Fatalf("secret %s status=%+v", key, view.Secrets[key])
		}
		if bytes.Contains(raw, []byte(value)) {
			t.Fatalf("settings view leaked %s", key)
		}
		var ciphertext string
		if err := db.QueryRow(`SELECT ciphertext FROM encrypted_secrets WHERE key=?`, key).Scan(&ciphertext); err != nil {
			t.Fatal(err)
		}
		if ciphertext == "" || strings.Contains(ciphertext, value) {
			t.Fatalf("secret %s was not stored as opaque ciphertext", key)
		}
	}
	runtime, err := service.Runtime(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.VisionAPIKey != "vision-secret-value" || runtime.EmbeddingAPIKey != "embedding-secret-value" || runtime.PixabayAPIKey != "pixabay-secret-value" {
		t.Fatalf("runtime secrets missing: vision=%t embedding=%t pixabay=%t", runtime.VisionAPIKey != "", runtime.EmbeddingAPIKey != "", runtime.PixabayAPIKey != "")
	}
	runtimeJSON, err := json.Marshal(runtime)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range secrets {
		if bytes.Contains(runtimeJSON, []byte(value)) {
			t.Fatalf("runtime JSON leaked a media intelligence key: %s", runtimeJSON)
		}
	}
}

func TestSettingsImageBaseURLRejectsValuesLongerThanSchemaLimit(t *testing.T) {
	service, _, _, valid := newSettingsTestService(t, Options{})
	valid.ImageBaseURL = "https://images.example.test/" + strings.Repeat("a", 2049)
	if _, err := service.PutPublic(t.Context(), valid); !errors.Is(err, ErrInvalidSettings) {
		t.Fatalf("PutPublic() error=%v, want invalid image_base_url", err)
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
		ImageModel: "gpt-image-2", MaxImageConcurrency: 3, ImageGenerationAttempts: 2, DefaultImageRatio: "3:4", DefaultImageStyle: "finance_documentary",
		CodexBinaryPath: filepath.Join(root, "codex.exe"), MediaIndexPath: filepath.Join(mediaRoot, "media-index.json"),
		MediaRoot: mediaRoot, JianyingRoot: filepath.Join(root, "jianying"),
		PexelsAPIBaseURL: "https://api.pexels.com", PixabayAPIBaseURL: "https://pixabay.com",
		MaxExternalResultsPerQuery: 20,
		TTSProvider:            defaultTTSProvider,
		AuraSTDBaseURL:         defaultAuraSTDBaseURL,
		AuraSTDModel:           defaultAuraSTDModel,
		AuraSTDVoiceID:         defaultAuraSTDVoiceID,
		AuraSTDSpeed:           defaultAuraSTDSpeed,
		AuraSTDVolume:          defaultAuraSTDVolume,
		AuraSTDPitch:           defaultAuraSTDPitch,
		AuraSTDLanguageBoost:   defaultAuraSTDLanguageBoost,
		AuraSTDModifyIntensity: defaultAuraSTDModifyIntensity,
		AuraSTDModifyTimbre:    defaultAuraSTDModifyTimbre,
		MontageStyle:           domain.DefaultMontageStyle(),
	}
	return NewService(store.NewSettingsRepository(db), protector, options), db, protector, public
}
