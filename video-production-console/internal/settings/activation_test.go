package settings

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"video-production-console/internal/security"
)

type activationFixture struct {
	Version   int               `json:"v"`
	IssuedTo  string            `json:"issued_to"`
	IssuedAt  string            `json:"issued_at"`
	ExpiresAt string            `json:"expires_at"`
	Note      string            `json:"note"`
	Public    map[string]string `json:"public"`
	Secrets   map[string]string `json:"secrets"`
}

func encodeActivationFixture(t *testing.T, fixture activationFixture) string {
	t.Helper()
	raw, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return "JYC1." + base64.RawURLEncoding.EncodeToString(compressed.Bytes())
}

func TestActivateCodeMergesAllowedSettingsAndDoesNotExposeValues(t *testing.T) {
	now := time.Date(2026, 10, 13, 8, 0, 0, 0, time.UTC)
	service, _, _, local := newSettingsTestService(t, Options{Now: func() time.Time { return now }})
	local.RemixBaseURL = "https://old.example.test/v1"
	local.RemixModel = "old-model"
	if _, err := service.PutPublic(t.Context(), local); err != nil {
		t.Fatal(err)
	}
	code := encodeActivationFixture(t, activationFixture{
		Version: 1, IssuedTo: "测试客户", IssuedAt: now.Add(-time.Hour).Format(time.RFC3339), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339),
		Public:  map[string]string{"remix_base_url": "https://new.example.test/v1", "remix_model": "gpt-5.6-sol"},
		Secrets: map[string]string{SecretRemixAPIKey: "fixture-remix-secret"},
	})

	result, err := service.ActivateCode(t.Context(), code)
	if err != nil {
		t.Fatal(err)
	}
	if result.IssuedTo != "测试客户" || result.ExpiresAt != now.Add(time.Hour).Format(time.RFC3339) {
		t.Fatalf("activation metadata=%+v", result)
	}
	if !reflect.DeepEqual(result.AppliedPublic, []string{"remix_base_url", "remix_model"}) || !reflect.DeepEqual(result.AppliedSecrets, []string{SecretRemixAPIKey}) || len(result.Skipped) != 0 {
		t.Fatalf("activation key result=%+v", result)
	}
	view, err := service.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if view.SettingsVersion != 2 {
		t.Fatalf("settings version=%d, want exactly one activation update", view.SettingsVersion)
	}
	if view.Public.RemixBaseURL != "https://new.example.test/v1" || view.Public.RemixModel != "gpt-5.6-sol" {
		t.Fatal("activation did not apply the allowed public settings")
	}
	if view.Public.DataRoot != local.DataRoot || view.Public.JianyingRoot != local.JianyingRoot || view.Public.ListenAddr != local.ListenAddr {
		t.Fatal("activation replaced machine-local settings")
	}
	secret, _, configured, err := service.secretValue(t.Context(), SecretRemixAPIKey)
	if err != nil || !configured || secret != "fixture-remix-secret" {
		t.Fatal("activation did not persist the secret")
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"fixture-remix-secret", "new.example.test", code} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatal("activation result exposed a configuration value")
		}
	}
}

func TestActivateCodeRejectsForbiddenKeysWithoutChangingSettings(t *testing.T) {
	now := time.Date(2026, 10, 13, 8, 0, 0, 0, time.UTC)
	service, _, _, local := newSettingsTestService(t, Options{Now: func() time.Time { return now }})
	if _, err := service.PutPublic(t.Context(), local); err != nil {
		t.Fatal(err)
	}
	forbiddenValue := filepath.Join(t.TempDir(), "must-not-be-used")
	code := encodeActivationFixture(t, activationFixture{
		Version: 1, IssuedAt: now.Format(time.RFC3339),
		Public: map[string]string{
			"data_root": forbiddenValue, "unlisted_public": "hidden-public-value",
			"https://signed.example.test/?token=key-secret": "hidden-url-key-value",
		},
		Secrets: map[string]string{"unlisted_secret": "hidden-secret-value"},
	})

	_, err := service.ActivateCode(t.Context(), code)
	if !errors.Is(err, ErrInvalidActivationCode) {
		t.Fatalf("ActivateCode() error class=%T", err)
	}
	message := err.Error()
	for _, key := range []string{"data_root", "unlisted_public", "unlisted_secret"} {
		if !strings.Contains(message, key) {
			t.Fatalf("rejected key %q missing from error", key)
		}
	}
	if !strings.Contains(message, "非法键名") {
		t.Fatal("unsafe rejected key did not use the fixed placeholder")
	}
	for _, forbidden := range []string{forbiddenValue, "hidden-public-value", "hidden-secret-value", "signed.example.test", "key-secret", code} {
		if strings.Contains(message, forbidden) {
			t.Fatal("activation error exposed an untrusted value")
		}
	}
	view, getErr := service.Get(t.Context())
	if getErr != nil {
		t.Fatal(getErr)
	}
	if view.Public.DataRoot != local.DataRoot {
		t.Fatal("rejected activation changed saved settings")
	}
}

func TestActivateCodeTreatsOversizedSecretAsInvalidCode(t *testing.T) {
	now := time.Date(2026, 10, 13, 8, 0, 0, 0, time.UTC)
	service, _, _, local := newSettingsTestService(t, Options{Now: func() time.Time { return now }})
	if _, err := service.PutPublic(t.Context(), local); err != nil {
		t.Fatal(err)
	}
	code := encodeActivationFixture(t, activationFixture{
		Version: 1, IssuedAt: now.Format(time.RFC3339),
		Secrets: map[string]string{SecretRemixAPIKey: strings.Repeat("s", security.MaxSecretSize+1)},
	})
	if _, err := service.ActivateCode(t.Context(), code); !errors.Is(err, ErrInvalidActivationCode) {
		t.Fatalf("oversized secret error class=%T", err)
	}
}

func TestActivateCodeRejectsExpiredAndMalformedCodesWithSafeErrors(t *testing.T) {
	now := time.Date(2026, 10, 13, 8, 0, 0, 0, time.UTC)
	service, _, _, local := newSettingsTestService(t, Options{Now: func() time.Time { return now }})
	if _, err := service.PutPublic(t.Context(), local); err != nil {
		t.Fatal(err)
	}
	expired := encodeActivationFixture(t, activationFixture{
		Version: 1, IssuedAt: now.Add(-2 * time.Hour).Format(time.RFC3339), ExpiresAt: now.Add(-time.Hour).Format(time.RFC3339),
		Public: map[string]string{"remix_model": "expired-model"},
	})
	if _, err := service.ActivateCode(t.Context(), expired); !errors.Is(err, ErrInvalidActivationCode) || err.Error() != "配置码已过期，请联系服务商" {
		t.Fatalf("expired code error=%q", safeTestError(err))
	}

	for _, malformed := range []string{"not-a-code", "JYC1.https://signed.example.test/?token=fixture-secret", "JYC1.%%%"} {
		_, err := service.ActivateCode(t.Context(), malformed)
		if !errors.Is(err, ErrInvalidActivationCode) {
			t.Fatalf("malformed code error class=%T", err)
		}
		for _, forbidden := range []string{"fixture-secret", "signed.example.test", malformed} {
			if strings.Contains(err.Error(), forbidden) {
				t.Fatal("malformed activation error exposed input")
			}
		}
	}
}

func TestActivateCodeHandlesReservedAnalysisKeysAccordingToCurrentSchema(t *testing.T) {
	now := time.Date(2026, 10, 13, 8, 0, 0, 0, time.UTC)
	service, _, _, local := newSettingsTestService(t, Options{Now: func() time.Time { return now }})
	if _, err := service.PutPublic(t.Context(), local); err != nil {
		t.Fatal(err)
	}
	code := encodeActivationFixture(t, activationFixture{
		Version: 1, IssuedAt: now.Format(time.RFC3339),
		Public:  map[string]string{"analysis_base_url": "https://analysis.example.test/v1", "analysis_model": "analysis-model"},
		Secrets: map[string]string{"analysis_api_key": "analysis-secret"},
	})
	result, err := service.ActivateCode(t.Context(), code)
	if err != nil {
		t.Fatal(err)
	}
	wantSkipped := make([]string, 0, 3)
	for _, key := range []string{"analysis_base_url", "analysis_model"} {
		if _, known := publicJSONKeys[key]; !known {
			wantSkipped = append(wantSkipped, key)
		}
	}
	if !containsString(secretKeys, "analysis_api_key") {
		wantSkipped = append(wantSkipped, "analysis_api_key")
	}
	sort.Strings(wantSkipped)
	if !reflect.DeepEqual(result.Skipped, wantSkipped) {
		t.Fatalf("reserved analysis skipped=%v want %v", result.Skipped, wantSkipped)
	}
}

func TestActivateCodeEnforcesRawAndExpandedSizeLimits(t *testing.T) {
	service, _, _, local := newSettingsTestService(t, Options{})
	if _, err := service.PutPublic(t.Context(), local); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ActivateCode(t.Context(), strings.Repeat("x", (64<<10)+1)); !errors.Is(err, ErrInvalidActivationCode) {
		t.Fatalf("oversized raw code error class=%T", err)
	}
	expanded := encodeActivationFixture(t, activationFixture{
		Version: 1, IssuedAt: time.Now().UTC().Format(time.RFC3339), Note: strings.Repeat("n", (256<<10)+1),
	})
	if len(expanded) >= 64<<10 {
		t.Fatal("expanded-limit fixture unexpectedly exceeds the raw limit")
	}
	if _, err := service.ActivateCode(t.Context(), expanded); !errors.Is(err, ErrInvalidActivationCode) {
		t.Fatalf("oversized expanded code error class=%T", err)
	}
}

func TestExportCodeUsesSavedNonEmptyAllowlistedValuesAndSelectedKeys(t *testing.T) {
	now := time.Date(2026, 10, 13, 8, 0, 0, 0, time.UTC)
	source, _, _, public := newSettingsTestService(t, Options{Now: func() time.Time { return now }})
	public.RemixModel = "gpt-5.6-sol"
	public.RemixCheckModel = ""
	if _, err := source.Update(t.Context(), public, map[string]string{SecretRemixAPIKey: "exported-secret"}); err != nil {
		t.Fatal(err)
	}
	result, err := source.ExportCode(t.Context(), ExportCodeRequest{
		IssuedTo: "导出客户", ExpiresDays: 7, IncludeSecrets: true, Keys: []string{"remix_model", "remix_check_model", SecretRemixAPIKey},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.PublicKeys, []string{"remix_model"}) || !reflect.DeepEqual(result.SecretKeys, []string{SecretRemixAPIKey}) || !strings.HasPrefix(result.Code, "JYC1.") {
		t.Fatalf("exported keys public=%v secret=%v code_present=%t", result.PublicKeys, result.SecretKeys, result.Code != "")
	}

	destination, _, _, local := newSettingsTestService(t, Options{Now: func() time.Time { return now }})
	if _, err := destination.PutPublic(t.Context(), local); err != nil {
		t.Fatal(err)
	}
	activation, err := destination.ActivateCode(t.Context(), result.Code)
	if err != nil {
		t.Fatal(err)
	}
	if activation.IssuedTo != "导出客户" || activation.ExpiresAt != now.AddDate(0, 0, 7).Format(time.RFC3339) {
		t.Fatalf("export metadata=%+v", activation)
	}
	secret, _, configured, err := destination.secretValue(t.Context(), SecretRemixAPIKey)
	if err != nil || !configured || secret != "exported-secret" {
		t.Fatal("exported secret did not round trip")
	}
}

func TestExportCodeReadsNewlySavedSecretInsteadOfCachedRuntime(t *testing.T) {
	now := time.Date(2026, 10, 13, 8, 0, 0, 0, time.UTC)
	source, _, _, public := newSettingsTestService(t, Options{Now: func() time.Time { return now }})
	public.RemixModel = "gpt-5.6-sol"
	if _, err := source.Update(t.Context(), public, map[string]string{SecretRemixAPIKey: "old-saved-secret"}); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Runtime(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Update(t.Context(), public, map[string]string{SecretRemixAPIKey: "new-saved-secret"}); err != nil {
		t.Fatal(err)
	}
	exported, err := source.ExportCode(t.Context(), ExportCodeRequest{IncludeSecrets: true, Keys: []string{SecretRemixAPIKey}})
	if err != nil {
		t.Fatal(err)
	}

	destination, _, _, local := newSettingsTestService(t, Options{Now: func() time.Time { return now }})
	if _, err := destination.PutPublic(t.Context(), local); err != nil {
		t.Fatal(err)
	}
	if _, err := destination.ActivateCode(t.Context(), exported.Code); err != nil {
		t.Fatal(err)
	}
	secret, _, configured, err := destination.secretValue(t.Context(), SecretRemixAPIKey)
	if err != nil || !configured || secret != "new-saved-secret" {
		t.Fatal("export used a stale runtime secret")
	}
}

func TestExportCodeRejectsInvalidInputWithSafeClientError(t *testing.T) {
	service, _, _, public := newSettingsTestService(t, Options{})
	if _, err := service.PutPublic(t.Context(), public); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		request ExportCodeRequest
		listed  string
	}{
		{name: "negative expiry", request: ExportCodeRequest{ExpiresDays: -1}},
		{name: "overflowing expiry", request: ExportCodeRequest{ExpiresDays: int(^uint(0) >> 1)}},
		{name: "unknown identifier key", request: ExportCodeRequest{Keys: []string{"unknown_setting"}}, listed: "unknown_setting"},
		{name: "URL key", request: ExportCodeRequest{Keys: []string{"https://signed.example.test/?token=export-secret"}}, listed: "非法键名"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := service.ExportCode(t.Context(), test.request)
			if !errors.Is(err, ErrInvalidExportRequest) {
				t.Fatalf("ExportCode() error class=%T", err)
			}
			if test.listed != "" && !strings.Contains(err.Error(), test.listed) {
				t.Fatalf("ExportCode() error omitted %q", test.listed)
			}
			for _, forbidden := range []string{"signed.example.test", "export-secret"} {
				if strings.Contains(err.Error(), forbidden) {
					t.Fatal("export input error exposed an unsafe key")
				}
			}
		})
	}

	boundaryService, _, _, boundaryPublic := newSettingsTestService(t, Options{Now: func() time.Time {
		return time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
	}})
	if _, err := boundaryService.PutPublic(t.Context(), boundaryPublic); err != nil {
		t.Fatal(err)
	}
	if _, err := boundaryService.ExportCode(t.Context(), ExportCodeRequest{ExpiresDays: 1}); !errors.Is(err, ErrInvalidExportRequest) {
		t.Fatalf("unrepresentable RFC3339 expiry error class=%T", err)
	}
}

func TestExportCodeDoesNotClassifySavedSecretFailureAsInvalidInput(t *testing.T) {
	service, _, protector, public := newSettingsTestService(t, Options{})
	if _, err := service.Update(t.Context(), public, map[string]string{SecretRemixAPIKey: "saved-secret"}); err != nil {
		t.Fatal(err)
	}
	protector.corrupt = true
	_, err := service.ExportCode(t.Context(), ExportCodeRequest{IncludeSecrets: true, Keys: []string{SecretRemixAPIKey}})
	if err == nil || errors.Is(err, ErrInvalidExportRequest) {
		t.Fatalf("saved secret failure classification invalid_input=%t", errors.Is(err, ErrInvalidExportRequest))
	}
}

func TestAutoActivateAppliesEachFileDigestOnce(t *testing.T) {
	now := time.Date(2026, 10, 13, 8, 0, 0, 0, time.UTC)
	service, _, _, local := newSettingsTestService(t, Options{Now: func() time.Time { return now }})
	if _, err := service.PutPublic(t.Context(), local); err != nil {
		t.Fatal(err)
	}
	dataRoot := t.TempDir()
	provision := filepath.Join(dataRoot, "provision")
	if err := os.MkdirAll(provision, 0o755); err != nil {
		t.Fatal(err)
	}
	code := encodeActivationFixture(t, activationFixture{
		Version: 1, IssuedAt: now.Format(time.RFC3339), Public: map[string]string{"remix_model": "auto-model"},
	})
	activationPath := filepath.Join(provision, "activation.jyc")
	if err := os.WriteFile(activationPath, []byte(code), 0o600); err != nil {
		t.Fatal(err)
	}
	applied, err := service.AutoActivate(t.Context(), dataRoot)
	if err != nil || !applied {
		t.Fatalf("first AutoActivate() applied=%t err=%v", applied, safeTestError(err))
	}
	view, err := service.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	firstVersion := view.SettingsVersion
	applied, err = service.AutoActivate(t.Context(), dataRoot)
	if err != nil || applied {
		t.Fatalf("second AutoActivate() applied=%t err=%v", applied, safeTestError(err))
	}
	view, err = service.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if view.SettingsVersion != firstVersion || view.Public.RemixModel != "auto-model" {
		t.Fatal("duplicate auto activation changed settings")
	}
	digest := sha256.Sum256([]byte(code))
	recorded, err := os.ReadFile(filepath.Join(provision, "activation.applied"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(recorded)) != hex.EncodeToString(digest[:]) {
		t.Fatal("auto activation did not record the applied digest")
	}

	updatedCode := encodeActivationFixture(t, activationFixture{
		Version: 1, IssuedAt: now.Format(time.RFC3339), Public: map[string]string{"remix_model": "updated-auto-model"},
	})
	if err := os.WriteFile(activationPath, []byte(updatedCode), 0o600); err != nil {
		t.Fatal(err)
	}
	applied, err = service.AutoActivate(t.Context(), dataRoot)
	if err != nil || !applied {
		t.Fatalf("updated AutoActivate() applied=%t err=%v", applied, safeTestError(err))
	}
	view, err = service.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if view.SettingsVersion != firstVersion+1 || view.Public.RemixModel != "updated-auto-model" {
		t.Fatal("updated auto activation did not apply exactly once")
	}
	updatedDigest := sha256.Sum256([]byte(updatedCode))
	recorded, err = os.ReadFile(filepath.Join(provision, "activation.applied"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(recorded)) != hex.EncodeToString(updatedDigest[:]) {
		t.Fatal("auto activation did not atomically replace the applied digest")
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func safeTestError(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}
