package config

// Config contains the local console's runtime settings.
type Config struct {
	ListenAddr      string
	DataRoot        string
	DatabasePath    string
	BaokuanBaseURL  string
	CodexBinaryPath string
	ObsidianVault   string
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
