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

// attachCatalogClients wires catalog path, local intent analysis, optional
// chat ShotSelector, and the HTTP embedder used for full-library neighbors.
// Intent HTTP is not used for Analyze (too slow / reasoning-heavy); the
// chat client is only attached as ShotSelector. Embedder URL/model come
// from the frozen task manifest, then VIDEO_CONSOLE_EMBEDDING_* env
// injected by the parent console. Empty URL/model yields ErrEmbeddingNotConfigured
// and rankLibrary records embedding_disabled.
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
		opts.Analyzer = montageplan.LocalIntentAnalyzer{}
		baseURL := firstNonEmpty(os.Getenv(envIntentBaseURL), manifest.NonSecretSettings.RemixBaseURL, manifest.NonSecretSettings.GrokBaseURL)
		model := firstNonEmpty(os.Getenv(envIntentModel), manifest.NonSecretSettings.RemixModel, manifest.NonSecretSettings.GrokModel)
		if baseURL != "" && model != "" {
			analyzer := montageplan.NewHTTPIntentAnalyzer(montageplan.IntentAnalyzerConfig{
				BaseURL:         baseURL,
				Model:           model,
				APIKey:          firstNonEmpty(os.Getenv(envIntentAPIKey), os.Getenv(envVisionAPIKey)),
				ReasoningEffort: strings.TrimSpace(os.Getenv(envIntentReasoningEffort)),
				Fallback:        montageplan.LocalIntentAnalyzer{},
			})
			if selector, ok := analyzer.(montageplan.ShotSelector); ok && opts.ShotSelector == nil {
				opts.ShotSelector = selector
			}
		}
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
			BaseURL: firstNonEmpty(manifest.NonSecretSettings.EmbeddingBaseURL, os.Getenv("VIDEO_CONSOLE_EMBEDDING_BASE_URL")),
			Model:   firstNonEmpty(manifest.NonSecretSettings.EmbeddingModel, os.Getenv("VIDEO_CONSOLE_EMBEDDING_MODEL")),
			APIKey:  firstNonEmpty(os.Getenv(envEmbeddingAPIKey), os.Getenv(envVisionAPIKey)),
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
