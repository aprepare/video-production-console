package agentruntime

import "video-production-console/internal/domain"

// ScriptAdapter is the local deterministic montage runtime.
type ScriptAdapter struct{}

func (ScriptAdapter) Name() RuntimeName { return RuntimeScript }

func (ScriptAdapter) Supports(action domain.TaskAction) bool {
	return action == domain.ActionMontageExecute || action == domain.ActionMontagePlan
}
