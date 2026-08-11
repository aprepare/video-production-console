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

// Select chooses a runtime for the task action.
func Select(action domain.TaskAction, preferred RuntimeName) RuntimeName {
	if action == domain.ActionMontageExecute || action == domain.ActionMontagePlan {
		if preferred == RuntimeCodex {
			return RuntimeCodex
		}
		return RuntimeScript
	}
	return RuntimeCodex
}
