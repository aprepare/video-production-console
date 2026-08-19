package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"video-production-console/internal/partnergateway"
)

func TestLoadServeConfigRequiresSecretFilesAndTLS(t *testing.T) {
	t.Setenv("PARTNER_GATEWAY_DB", filepath.Join(t.TempDir(), "gateway.db"))
	t.Setenv("PARTNER_GATEWAY_LISTEN", ":2443")
	t.Setenv("PARTNER_GATEWAY_UPSTREAM", "http://127.0.0.1:2001/v1")
	_, err := loadServeConfig()
	if err == nil || !strings.Contains(err.Error(), "UPSTREAM_KEY_FILE") {
		t.Fatalf("err=%v", err)
	}
}

func TestLoadServeConfigRejectsMissingAuraKeyFileAndTLSPaths(t *testing.T) {
	tests := []struct {
		name    string
		missing string
		want    string
	}{
		{name: "aura key file", missing: "PARTNER_GATEWAY_AURA_KEY_FILE", want: "AURA_KEY_FILE"},
		{name: "TLS certificate", missing: "PARTNER_GATEWAY_TLS_CERT", want: "TLS_CERT"},
		{name: "TLS key", missing: "PARTNER_GATEWAY_TLS_KEY", want: "TLS_KEY"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setValidServeEnv(t)
			t.Setenv(test.missing, "")

			_, err := loadServeConfig()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestLoadServeConfigRejectsEmptySecretFiles(t *testing.T) {
	tests := []struct {
		name    string
		envName string
	}{
		{name: "upstream", envName: "PARTNER_GATEWAY_UPSTREAM_KEY_FILE"},
		{name: "aura", envName: "PARTNER_GATEWAY_AURA_KEY_FILE"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setValidServeEnv(t)
			empty := filepath.Join(t.TempDir(), "empty-secret")
			if err := os.WriteFile(empty, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv(test.envName, empty)

			_, err := loadServeConfig()
			if err == nil || !strings.Contains(err.Error(), "empty") {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestReadSecretFileEnforcesSizeLimit(t *testing.T) {
	const oversizedMarker = "OVERSIZED_SECRET_MUST_NOT_LEAK"
	oversizedPath := writeSecretFile(t, "oversized-secret", strings.Repeat(oversizedMarker, 300))

	_, err := readSecretFile("TEST_SECRET_FILE", oversizedPath)
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("err=%v", err)
	}
	if strings.Contains(err.Error(), oversizedMarker) {
		t.Fatalf("secret leaked in error: %q", err)
	}

	const want = "normal-small-secret"
	smallPath := writeSecretFile(t, "small-secret", want+"\n")
	got, err := readSecretFile("TEST_SECRET_FILE", smallPath)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("secret=%q, want %q", got, want)
	}
}

func TestLoadServeConfigRejectsWorldReadableSecretFilesOnLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux permission rule")
	}
	setValidServeEnv(t)
	path := filepath.Join(t.TempDir(), "world-readable")
	if err := os.WriteFile(path, []byte("not-secret-enough"), 0o604); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o604); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PARTNER_GATEWAY_UPSTREAM_KEY_FILE", path)

	_, err := loadServeConfig()
	if err == nil || !strings.Contains(err.Error(), "world-readable") {
		t.Fatalf("err=%v", err)
	}
}

func TestLoadServeConfigTrimsOneTrailingNewlineAndBuildsDefaultCapabilities(t *testing.T) {
	upstreamPath, auraPath := setValidServeEnv(t)
	if err := os.WriteFile(upstreamPath, []byte("upstream-secret\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(auraPath, []byte("aura-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	config, err := loadServeConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.upstreamKey != "upstream-secret\n" {
		t.Fatalf("upstream key=%q", config.upstreamKey)
	}
	if config.aura.APIKey != "aura-secret" {
		t.Fatalf("aura key=%q", config.aura.APIKey)
	}
	if !slices.Equal(config.capabilities.TextModels, []string{"gpt-5.6-sol", "grok-4.6"}) {
		t.Fatalf("text models=%v", config.capabilities.TextModels)
	}
	if !slices.Equal(config.capabilities.ReasoningEfforts, []string{"low", "medium", "high", "xhigh", "max", "ultra"}) {
		t.Fatalf("reasoning efforts=%v", config.capabilities.ReasoningEfforts)
	}
	if config.capabilities.ImageModel != "gpt-image-2" {
		t.Fatalf("image model=%q", config.capabilities.ImageModel)
	}
}

func TestServeDoesNotAcceptSecretFlags(t *testing.T) {
	const secret = "FLAG_SECRET_MUST_NOT_APPEAR"
	var out, errOut bytes.Buffer
	err := run(context.Background(), []string{"serve", "--upstream-key", secret}, &out, &errOut)
	if err == nil || !strings.Contains(err.Error(), "upstream-key") {
		t.Fatalf("err=%v", err)
	}
	if strings.Contains(out.String(), secret) || strings.Contains(errOut.String(), secret) || strings.Contains(err.Error(), secret) {
		t.Fatalf("secret leaked: out=%q stderr=%q err=%q", out.String(), errOut.String(), err)
	}
}

func TestFailedServeConfigDoesNotLeakSecretFileValues(t *testing.T) {
	const upstreamSecret = "DISTINCTIVE_UPSTREAM_SECRET_7F12"
	const auraSecret = "DISTINCTIVE_AURA_SECRET_41B9"
	upstreamPath, auraPath := setValidServeEnv(t)
	if err := os.WriteFile(upstreamPath, []byte(upstreamSecret), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(auraPath, []byte(auraSecret), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PARTNER_GATEWAY_AURA_BASE_URL", "")

	var out, errOut bytes.Buffer
	err := run(context.Background(), []string{"serve"}, &out, &errOut)
	if err == nil {
		t.Fatal("expected configuration error")
	}
	for _, value := range []string{out.String(), errOut.String(), err.Error()} {
		if strings.Contains(value, upstreamSecret) || strings.Contains(value, auraSecret) {
			t.Fatalf("secret leaked in %q", value)
		}
	}
}

func TestRunPartnerCreatePrintsKeyOnce(t *testing.T) {
	var out bytes.Buffer
	err := run(context.Background(), []string{"partner", "create", "--db", filepath.Join(t.TempDir(), "g.db"), "--name", "天中观局"}, &out, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "vpc_") != 1 {
		t.Fatalf("out=%q", out.String())
	}
}

func TestRunPartnerRotateKeyPrintsNewKeyOnce(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "g.db")
	partnerID, originalKey := createPartnerForTest(t, dbPath)
	var out bytes.Buffer

	err := run(context.Background(), []string{"partner", "rotate-key", "--db", dbPath, "--id", partnerID}, &out, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "vpc_") != 1 || strings.Contains(out.String(), originalKey) {
		t.Fatalf("out=%q original=%q", out.String(), originalKey)
	}
}

func TestRunPartnerAdministrativeCommandsDoNotPrintSecrets(t *testing.T) {
	const upstreamSecret = "ADMIN_UPSTREAM_SECRET"
	const auraSecret = "ADMIN_AURA_SECRET"
	dbPath := filepath.Join(t.TempDir(), "g.db")
	partnerID, activationKey := createPartnerForTest(t, dbPath)
	t.Setenv("PARTNER_GATEWAY_UPSTREAM_KEY_FILE", writeSecretFile(t, "upstream", upstreamSecret))
	t.Setenv("PARTNER_GATEWAY_AURA_KEY_FILE", writeSecretFile(t, "aura", auraSecret))

	commands := [][]string{
		{"partner", "list", "--db", dbPath},
		{"partner", "disable", "--db", dbPath, "--id", partnerID},
		{"partner", "enable", "--db", dbPath, "--id", partnerID},
		{"partner", "unbind", "--db", dbPath, "--id", partnerID},
	}
	for _, command := range commands {
		var out, errOut bytes.Buffer
		if err := run(context.Background(), command, &out, &errOut); err != nil {
			t.Fatalf("%v: %v", command, err)
		}
		combined := out.String() + errOut.String()
		for _, secret := range []string{upstreamSecret, auraSecret, activationKey} {
			if strings.Contains(combined, secret) {
				t.Fatalf("%v leaked secret in %q", command, combined)
			}
		}
	}
}

func setValidServeEnv(t *testing.T) (string, string) {
	t.Helper()
	temp := t.TempDir()
	upstreamPath := writeSecretFileAt(t, filepath.Join(temp, "upstream-key"), "upstream-secret")
	auraPath := writeSecretFileAt(t, filepath.Join(temp, "aura-key"), "aura-secret")
	for name, value := range map[string]string{
		"PARTNER_GATEWAY_LISTEN":            ":2443",
		"PARTNER_GATEWAY_DB":                filepath.Join(temp, "gateway.db"),
		"PARTNER_GATEWAY_TLS_CERT":          filepath.Join(temp, "server.crt"),
		"PARTNER_GATEWAY_TLS_KEY":           filepath.Join(temp, "server.key"),
		"PARTNER_GATEWAY_UPSTREAM":          "http://127.0.0.1:2001/v1",
		"PARTNER_GATEWAY_UPSTREAM_KEY_FILE": upstreamPath,
		"PARTNER_GATEWAY_AURA_KEY_FILE":     auraPath,
		"PARTNER_GATEWAY_AURA_BASE_URL":     "https://aura.example.test/v1",
		"PARTNER_GATEWAY_AURA_MODEL":        "aura-model",
		"PARTNER_GATEWAY_AURA_VOICE_ID":     "voice-id",
	} {
		t.Setenv(name, value)
	}
	return upstreamPath, auraPath
}

func writeSecretFile(t *testing.T, name, value string) string {
	t.Helper()
	return writeSecretFileAt(t, filepath.Join(t.TempDir(), name), value)
}

func writeSecretFileAt(t *testing.T, path, value string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func createPartnerForTest(t *testing.T, dbPath string) (string, string) {
	t.Helper()
	var out bytes.Buffer
	if err := run(context.Background(), []string{"partner", "create", "--db", dbPath, "--name", "天中观局"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}

	store, err := partnergateway.OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := partnergateway.NewAuthService(store, nil, nil, 0, "0.1.0", partnergateway.Capabilities{}, partnergateway.AuraRuntime{})
	partners, err := service.ListPartners(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(partners) != 1 {
		t.Fatalf("partners=%d", len(partners))
	}
	output := out.String()
	index := strings.Index(output, "vpc_")
	if index < 0 {
		t.Fatalf("out=%q", output)
	}
	key := strings.Fields(output[index:])[0]
	return partners[0].ID, key
}
