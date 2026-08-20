package config

import (
	"path/filepath"
	"strings"
)

// Config contains the local console's runtime settings.
type Config struct {
	ListenAddr         string
	AppRoot            string
	DataRoot           string
	DatabasePath       string
	BaokuanBaseURL     string
	CodexBinaryPath    string
	ObsidianVault      string
	MachineProfilePath string
}

// ApplyEnvironment honors partner-launcher roots when they are present.
// Owner mode is unchanged when the variables are unset.
func ApplyEnvironment(cfg Config, lookup func(string) (string, bool)) Config {
	if lookup == nil {
		return cfg
	}
	if value, ok := lookup("VIDEO_CONSOLE_APP_ROOT"); ok {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			cfg.AppRoot = filepath.Clean(trimmed)
		}
	}
	if value, ok := lookup("VIDEO_CONSOLE_DATA_ROOT"); ok {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			cfg.DataRoot = filepath.Clean(trimmed)
			cfg.DatabasePath = filepath.Join(cfg.DataRoot, "console.db")
		}
	}
	return cfg
}

// Default returns settings suitable for running the console locally.
func Default() Config {
	return Config{
		ListenAddr:      "127.0.0.1:2030",
		DataRoot:        "./video-console-data",
		DatabasePath:    "./video-console-data/console.db",
		BaokuanBaseURL:  "http://127.0.0.1:2022",
		CodexBinaryPath: "codex",
		ObsidianVault:   "",
	}
}
