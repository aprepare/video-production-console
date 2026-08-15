package catalogbuilder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigRejectsMissingOrRelativeMediaRoot(t *testing.T) {
	if err := (Config{}).Validate(); err == nil {
		t.Fatal("empty media root accepted")
	}
	if err := (Config{MediaRoot: "relative"}).Validate(); err == nil {
		t.Fatal("relative media root accepted")
	}
	root := t.TempDir()
	if err := (Config{MediaRoot: root}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestPublicConfigMasksSecrets(t *testing.T) {
	got := Config{VisionAPIKey: "abcdefghij", EmbeddingAPIKey: "xyz"}.Public()
	if got.VisionAPIKey != "****ghij" || got.EmbeddingAPIKey != "****" {
		t.Fatalf("masked=%+v", got)
	}
}

func TestLoadConfigAppliesEnvOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog-builder.config.json")
	root := t.TempDir()
	cfg := Config{MediaRoot: root, VisionAPIKey: "file-vision", EmbeddingAPIKey: "file-embed"}
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envVisionAPIKey, "env-vision")
	t.Setenv(envEmbeddingAPIKey, "env-embed")
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.VisionAPIKey != "env-vision" || loaded.EmbeddingAPIKey != "env-embed" {
		t.Fatalf("loaded=%+v", loaded)
	}
	missing, err := LoadConfig(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatal(err)
	}
	if missing.VisionAPIKey != "env-vision" {
		t.Fatalf("missing file should still apply env, got %+v", missing)
	}
}

func TestSaveConfigWritesJSONWithoutLeakingRelativeRoot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "catalog-builder.config.json")
	if err := SaveConfig(path, Config{MediaRoot: "not-abs"}); err == nil {
		t.Fatal("relative root saved")
	}
	root := t.TempDir()
	if err := SaveConfig(path, Config{MediaRoot: root, VisionAPIKey: "secret-key"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "secret-key") {
		t.Fatal("local config should keep the key on this machine")
	}
}
