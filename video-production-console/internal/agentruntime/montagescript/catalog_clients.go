package montagescript

import (
	"encoding/json"
	"os"
	"strings"

	"video-production-console/internal/agentruntime/montageplan"
	"video-production-console/internal/mediacatalog"
)

const (
	envVisionAPIKey          = "VIDEO_CONSOLE_VISION_API_KEY"
	envEmbeddingAPIKey       = "VIDEO_CONSOLE_EMBEDDING_API_KEY"
	envIntentAPIKey          = "VIDEO_CONSOLE_INTENT_API_KEY"
	envIntentBaseURL         = "VIDEO_CONSOLE_INTENT_BASE_URL"
	envIntentModel           = "VIDEO_CONSOLE_INTENT_MODEL"
	envIntentReasoningEffort = "VIDEO_CONSOLE_INTENT_REASONING_EFFORT"
)

type catalogManifestLite struct {
	Skill             string `json:"skill"`
	NonSecretSettings struct {
		MediaCatalogPath     string `json:"media_catalog_path"`
		FFprobePath          string `json:"ffprobe_path"`
		GrokBaseURL          string `json:"grok_base_url"`
		GrokModel            string `json:"grok_model"`
		RemixBaseURL         string `json:"remix_base_url"`
		RemixModel           string `json:"remix_model"`
		RemixReasoningEffort string `json:"remix_reasoning_effort"`
		VisionBaseURL        string `json:"vision_base_url"`
		VisionModel          string `json:"vision_model"`
		EmbeddingBaseURL     string `json:"embedding_base_url"`
		EmbeddingModel       string `json:"embedding_model"`
		MontagePlanVersion   string `json:"montage_plan_version"`
	} `json:"non_secret_settings"`
}

func attachCatalogClients(opts *Options) {
	if opts == nil {
		return
	}
	raw, err := os.ReadFile(strings.TrimSpace(opts.ManifestPath))
	if err != nil {
		return
	}
	var manifest catalogManifestLite
	if json.Unmarshal(raw, &manifest) != nil {
		return
	}
	if strings.TrimSpace(opts.CatalogPath) == "" {
		opts.CatalogPath = strings.TrimSpace(manifest.NonSecretSettings.MediaCatalogPath)
	}
	if strings.TrimSpace(opts.FFprobePath) == "" {
		opts.FFprobePath = strings.TrimSpace(manifest.NonSecretSettings.FFprobePath)
	}
	if opts.Analyzer == nil {
		opts.Analyzer = montageplan.NewHTTPIntentAnalyzer(montageplan.IntentAnalyzerConfig{
			BaseURL:  firstNonEmpty(os.Getenv(envIntentBaseURL), manifest.NonSecretSettings.VisionBaseURL),
			Model:    firstNonEmpty(os.Getenv(envIntentModel), manifest.NonSecretSettings.VisionModel),
			APIKey:   firstNonEmpty(os.Getenv(envIntentAPIKey), os.Getenv(envVisionAPIKey)),
			Fallback: montageplan.LocalIntentAnalyzer{},
		})
	}
	// 大模型字幕分段/关键词先屏蔽，见 montageplan.CaptionLLMLineBreakerEnabled。
	if opts.LineBreaker == nil && montageplan.CaptionLLMLineBreakerEnabled {
		opts.LineBreaker = montageplan.NewHTTPLineBreaker(montageplan.LineBreakerConfig{
			BaseURL:         firstNonEmpty(os.Getenv(envIntentBaseURL), manifest.NonSecretSettings.RemixBaseURL, manifest.NonSecretSettings.GrokBaseURL, manifest.NonSecretSettings.VisionBaseURL),
			Model:           firstNonEmpty(os.Getenv(envIntentModel), manifest.NonSecretSettings.RemixModel, manifest.NonSecretSettings.GrokModel, manifest.NonSecretSettings.VisionModel),
			APIKey:          firstNonEmpty(os.Getenv(envIntentAPIKey), os.Getenv(envVisionAPIKey)),
			ReasoningEffort: firstNonEmpty(os.Getenv(envIntentReasoningEffort), manifest.NonSecretSettings.RemixReasoningEffort),
		})
	}
	if opts.Embedder == nil {
		if embedder, err := mediacatalog.NewHTTPEmbedder(mediacatalog.EmbedderConfig{
			BaseURL: manifest.NonSecretSettings.EmbeddingBaseURL,
			Model:   manifest.NonSecretSettings.EmbeddingModel,
			APIKey:  strings.TrimSpace(os.Getenv(envEmbeddingAPIKey)),
		}); err == nil {
			opts.Embedder = embedder
		}
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
