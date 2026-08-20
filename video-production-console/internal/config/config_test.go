package config

import (
	"path/filepath"
	"testing"
)

func TestDefault(t *testing.T) {
	got := Default()

	if got.ListenAddr != "127.0.0.1:2030" {
		t.Errorf("ListenAddr = %q", got.ListenAddr)
	}
	if got.DataRoot != "./video-console-data" {
		t.Errorf("DataRoot = %q", got.DataRoot)
	}
	if got.DatabasePath != "./video-console-data/console.db" {
		t.Errorf("DatabasePath = %q", got.DatabasePath)
	}
	if got.BaokuanBaseURL != "http://127.0.0.1:2022" {
		t.Errorf("BaokuanBaseURL = %q", got.BaokuanBaseURL)
	}
	if got.CodexBinaryPath != "codex" {
		t.Errorf("CodexBinaryPath = %q", got.CodexBinaryPath)
	}
	if got.ObsidianVault != "" {
		t.Errorf("ObsidianVault = %q", got.ObsidianVault)
	}
	if got.AppRoot != "" {
		t.Errorf("AppRoot = %q", got.AppRoot)
	}
}

func TestApplyEnvironmentHonorsPartnerAppAndDataRoots(t *testing.T) {
	appRoot := filepath.Join(t.TempDir(), "app", "0.1.0")
	dataRoot := filepath.Join(t.TempDir(), "data")
	got := ApplyEnvironment(Default(), func(key string) (string, bool) {
		switch key {
		case "VIDEO_CONSOLE_APP_ROOT":
			return appRoot, true
		case "VIDEO_CONSOLE_DATA_ROOT":
			return dataRoot, true
		default:
			return "", false
		}
	})
	if got.AppRoot != filepath.Clean(appRoot) {
		t.Fatalf("AppRoot=%q", got.AppRoot)
	}
	if got.DataRoot != filepath.Clean(dataRoot) {
		t.Fatalf("DataRoot=%q", got.DataRoot)
	}
	if got.DatabasePath != filepath.Join(dataRoot, "console.db") {
		t.Fatalf("DatabasePath=%q", got.DatabasePath)
	}
}

func TestApplyEnvironmentLeavesOwnerDefaultsWhenUnset(t *testing.T) {
	base := Default()
	got := ApplyEnvironment(base, func(string) (string, bool) { return "", false })
	if got != base {
		t.Fatalf("owner config changed without env: %+v", got)
	}
}
