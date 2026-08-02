package config

import "testing"

func TestDefault(t *testing.T) {
	got := Default()

	if got.ListenAddr != "0.0.0.0:2030" {
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
}
