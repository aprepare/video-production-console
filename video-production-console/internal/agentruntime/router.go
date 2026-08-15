package agentruntime

import (
	"os"
	"strings"

	"video-production-console/internal/domain"
)

// MontageRuntimeFromEnv returns the configured montage runtime.
// Default is script so montage.execute avoids Codex context blow-ups.
func MontageRuntimeFromEnv() RuntimeName {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(EnvMontageRuntime)))
	switch value {
	case string(RuntimeCodex):
		return RuntimeCodex
	case string(RuntimeScript), "":
		return RuntimeScript
	default:
		return RuntimeScript
	}
}

// LLMRuntimeFromEnv returns the configured remix/topic runtime.
// Default is codex so existing production behavior stays unchanged.
func LLMRuntimeFromEnv() RuntimeName {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(EnvLLMRuntime)))
	switch value {
	case string(RuntimeOpenAI):
		return RuntimeOpenAI
	case string(RuntimePi):
		return RuntimePi
	case string(RuntimeCodex), "":
		return RuntimeCodex
	default:
		return RuntimeCodex
	}
}

// OpenAIConfigFromEnv returns base URL and API key for openai_compat.
func OpenAIConfigFromEnv() (baseURL, apiKey string) {
	return strings.TrimSpace(os.Getenv(EnvOpenAIBaseURL)), strings.TrimSpace(os.Getenv(EnvOpenAIAPIKey))
}

const (
	secretGrokBaseURL  = "GROK_SEARCH_BASE_URL"
	secretGrokAPIKey   = "GROK_SEARCH_API_KEY"
	secretGrokModel    = "GROK_SEARCH_MODEL"
	secretRemixBaseURL = "REMIX_BASE_URL"
	secretRemixAPIKey  = "REMIX_API_KEY"
	secretRemixModel   = "REMIX_MODEL"
)

// ResolveOpenAICompatConfig prefers VIDEO_CONSOLE_OPENAI_*, then the dedicated
// remix endpoint, then the settings-injected Grok endpoint.
func ResolveOpenAICompatConfig(secrets map[string]string) (baseURL, apiKey, model string, ok bool) {
	baseURL, apiKey = OpenAIConfigFromEnv()
	model = firstNonEmpty(
		secretValue(secrets, secretRemixModel),
		strings.TrimSpace(os.Getenv(secretRemixModel)),
		secretValue(secrets, secretGrokModel),
		strings.TrimSpace(os.Getenv(secretGrokModel)),
	)
	if baseURL != "" && apiKey != "" {
		return baseURL, apiKey, model, true
	}
	baseURL = firstNonEmpty(secretValue(secrets, secretRemixBaseURL), strings.TrimSpace(os.Getenv(secretRemixBaseURL)))
	apiKey = firstNonEmpty(secretValue(secrets, secretRemixAPIKey), strings.TrimSpace(os.Getenv(secretRemixAPIKey)))
	if baseURL != "" && apiKey != "" {
		return baseURL, apiKey, model, true
	}
	baseURL = firstNonEmpty(secretValue(secrets, secretGrokBaseURL), strings.TrimSpace(os.Getenv(secretGrokBaseURL)))
	apiKey = firstNonEmpty(secretValue(secrets, secretGrokAPIKey), strings.TrimSpace(os.Getenv(secretGrokAPIKey)))
	if baseURL != "" && apiKey != "" {
		return baseURL, apiKey, model, true
	}
	return "", "", "", false
}

// LLMRuntimePreferred returns the remix runtime. An explicit
// VIDEO_CONSOLE_LLM_RUNTIME value always wins. When the env is empty and a
// Grok/OpenAI-compatible endpoint is configured, remix uses openai_compat.
func LLMRuntimePreferred(secrets map[string]string) RuntimeName {
	explicit := strings.ToLower(strings.TrimSpace(os.Getenv(EnvLLMRuntime)))
	switch explicit {
	case string(RuntimeOpenAI), string(RuntimePi), string(RuntimeCodex):
		return LLMRuntimeFromEnv()
	}
	if baseURL, apiKey := OpenAIConfigFromEnv(); baseURL != "" && apiKey != "" {
		return RuntimeOpenAI
	}
	if secretValue(secrets, secretRemixBaseURL) != "" && secretValue(secrets, secretRemixAPIKey) != "" {
		return RuntimeOpenAI
	}
	if secretValue(secrets, secretGrokBaseURL) != "" && secretValue(secrets, secretGrokAPIKey) != "" {
		return RuntimeOpenAI
	}
	return RuntimeCodex
}

// CompatModelName keeps custom names such as cursor-grok-* unchanged.
// Empty Codex defaults (gpt-*, codex*) fall back to the configured Grok model.
func CompatModelName(taskModel, configuredModel string) string {
	taskModel = strings.TrimSpace(taskModel)
	configuredModel = strings.TrimSpace(configuredModel)
	if shouldSubstituteCompatModel(taskModel) && configuredModel != "" {
		return configuredModel
	}
	if taskModel != "" {
		return taskModel
	}
	return configuredModel
}

func shouldSubstituteCompatModel(taskModel string) bool {
	if taskModel == "" {
		return true
	}
	lower := strings.ToLower(taskModel)
	return strings.HasPrefix(lower, "gpt-") || strings.HasPrefix(lower, "codex")
}

func secretValue(secrets map[string]string, key string) string {
	if secrets == nil {
		return ""
	}
	return strings.TrimSpace(secrets[key])
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// Select chooses a runtime for the task action.
func Select(action domain.TaskAction, preferred RuntimeName) RuntimeName {
	if action == domain.ActionMontageExecute || action == domain.ActionMontagePlan {
		if preferred == RuntimeCodex {
			return RuntimeCodex
		}
		return RuntimeScript
	}
	if isLLMAction(action) {
		switch preferred {
		case RuntimeOpenAI:
			return RuntimeOpenAI
		case RuntimePi:
			return RuntimePi
		default:
			return RuntimeCodex
		}
	}
	return RuntimeCodex
}

func isLLMAction(action domain.TaskAction) bool {
	switch action {
	case domain.ActionTopicBrainstorm, domain.ActionTopicCommit, domain.ActionTopicDeepen,
		domain.ActionRemixStandard, domain.ActionRemixEnhanced, domain.ActionRemixFromTopic, domain.ActionRemixReview:
		return true
	default:
		return false
	}
}
