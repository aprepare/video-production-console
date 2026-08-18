package domain

type SemanticEventKind string

const (
	SemanticPhaseStarted     SemanticEventKind = "phase_started"
	SemanticPhaseProgress    SemanticEventKind = "phase_progress"
	SemanticAssistantMessage SemanticEventKind = "assistant_message"
	SemanticToolActivity     SemanticEventKind = "tool_activity"
	SemanticQuestion         SemanticEventKind = "user_question"
	SemanticApproval         SemanticEventKind = "approval_required"
	SemanticArtifact         SemanticEventKind = "artifact_ready"
	SemanticWarning          SemanticEventKind = "warning"
	SemanticFailure          SemanticEventKind = "failure"
	SemanticTurnCompleted    SemanticEventKind = "turn_completed"
)
