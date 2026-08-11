package agentruntime

import "video-production-console/internal/domain"

// CodexAdapter documents the existing Codex exec path.
// Week 1 keeps launching Codex via CommandFactory → BuildExecCommand.
type CodexAdapter struct{}

func (CodexAdapter) Name() RuntimeName { return RuntimeCodex }

func (CodexAdapter) Supports(action domain.TaskAction) bool {
	return true
}
