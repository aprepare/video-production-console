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

// Select chooses a runtime for the task action.
func Select(action domain.TaskAction, preferred RuntimeName) RuntimeName {
	if action == domain.ActionMontageExecute || action == domain.ActionMontagePlan {
		if preferred == RuntimeCodex {
			return RuntimeCodex
		}
		return RuntimeScript
	}
	if isLLMAction(action) {
		if preferred == RuntimeOpenAI {
			return RuntimeOpenAI
		}
		return RuntimeCodex
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
