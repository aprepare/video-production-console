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

// ResolveOpenAICompatConfig prefers the dedicated remix endpoint from settings,
// then process VIDEO_CONSOLE_OPENAI_*, then the settings-injected Grok endpoint.
// A saved 二创 URL must win over a stale parent-process leftover.
func ResolveOpenAICompatConfig(secrets map[string]string) (baseURL, apiKey, model string, ok bool) {
	model = firstNonEmpty(
		secretValue(secrets, secretRemixModel),
		strings.TrimSpace(os.Getenv(secretRemixModel)),
		secretValue(secrets, secretGrokModel),
		strings.TrimSpace(os.Getenv(secretGrokModel)),
	)
	baseURL = firstNonEmpty(secretValue(secrets, secretRemixBaseURL), strings.TrimSpace(os.Getenv(secretRemixBaseURL)))
	apiKey = firstNonEmpty(secretValue(secrets, secretRemixAPIKey), strings.TrimSpace(os.Getenv(secretRemixAPIKey)))
	if baseURL != "" && apiKey != "" {
		return baseURL, apiKey, model, true
	}
	baseURL, apiKey = OpenAIConfigFromEnv()
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

// CompatModelName prefers the task's explicit model. Empty names fall back to
// the configured remix/Grok model. gpt-* is a valid OpenAI-compatible name and
// must not be rewritten to a cursor-* prefix.
func CompatModelName(taskModel, configuredModel string) string {
	taskModel = strings.TrimSpace(taskModel)
	if taskModel != "" {
		return taskModel
	}
	return strings.TrimSpace(configuredModel)
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
