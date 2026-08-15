package catalogbuilder

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	envVisionAPIKey    = "CATALOG_BUILDER_VISION_API_KEY"
	envEmbeddingAPIKey = "CATALOG_BUILDER_EMBEDDING_API_KEY"
)

// Config is the local builder settings. Secrets stay on this machine: they
// are never written into a catalog pack.
type Config struct {
	MediaRoot        string `json:"media_root"`
	FFmpegPath       string `json:"ffmpeg_path"`
	FFprobePath      string `json:"ffprobe_path"`
	VisionBaseURL    string `json:"vision_base_url"`
	VisionModel      string `json:"vision_model"`
	VisionAPIKey     string `json:"vision_api_key"`
	EmbeddingBaseURL     string `json:"embedding_base_url"`
	EmbeddingModel       string `json:"embedding_model"`
	EmbeddingAPIKey      string `json:"embedding_api_key"`
	AnalysisConcurrency  int    `json:"analysis_concurrency"`
}

func (c Config) Validate() error {
	root := strings.TrimSpace(c.MediaRoot)
	if root == "" || !filepath.IsAbs(root) {
		return fmt.Errorf("%w: media_root must be an absolute path", errInvalidConfig)
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("%w: media_root must be an existing directory", errInvalidConfig)
	}
	return nil
}

func (c Config) Public() Config {
	c.VisionAPIKey = maskSecret(c.VisionAPIKey)
	c.EmbeddingAPIKey = maskSecret(c.EmbeddingAPIKey)
	return c
}

func maskSecret(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) <= 4 {
		return "****"
	}
	return "****" + value[len(value)-4:]
}

func applyEnvOverrides(cfg Config) Config {
	if value := strings.TrimSpace(os.Getenv(envVisionAPIKey)); value != "" {
		cfg.VisionAPIKey = value
	}
	if value := strings.TrimSpace(os.Getenv(envEmbeddingAPIKey)); value != "" {
		cfg.EmbeddingAPIKey = value
	}
	return cfg
}

func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return applyEnvOverrides(Config{}), nil
		}
		return Config{}, fmt.Errorf("read catalog-builder config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("%w: catalog-builder config is not valid JSON", errInvalidConfig)
	}
	return applyEnvOverrides(cfg), nil
}

func SaveConfig(path string, cfg Config) error {
	prepared, err := cfg.Prepare(filepath.Dir(path))
	if err != nil {
		return err
	}
	cfg = prepared
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create catalog-builder config directory: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode catalog-builder config: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write catalog-builder config: %w", err)
	}
	return nil
}
