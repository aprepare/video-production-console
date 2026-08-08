package domain

import "time"

type TaskCompletionPhase string

const (
	CompletionAgentRunning   TaskCompletionPhase = "agent_running"
	CompletionPlaintextReady TaskCompletionPhase = "plaintext_ready"
	CompletionRegistering    TaskCompletionPhase = "registering"
	CompletionRegistered     TaskCompletionPhase = "registered"
)

type RegistrationState string

const (
	RegistrationQueued      RegistrationState = "queued"
	RegistrationRunning     RegistrationState = "running"
	RegistrationSucceeded   RegistrationState = "succeeded"
	RegistrationFailed      RegistrationState = "failed"
	RegistrationInterrupted RegistrationState = "interrupted"
)

type RegistrationAttempt struct {
	ID, TaskID, ManifestPath, WorkspacePath              string
	State                                                RegistrationState
	Attempt                                              int
	RegisteredPath, ReceiptPath, ErrorCode, ErrorMessage *string
	StartedAt                                            time.Time
	FinishedAt                                           *time.Time
}

// RegistrationRecovery is the durable work discovered during console startup.
// Queued attempts may be submitted to the trusted registration worker again;
// interrupted attempts are retained for an explicit user retry.
type RegistrationRecovery struct {
	Queued      []RegistrationAttempt
	Interrupted []RegistrationAttempt
}

type MixDraftAuditFinding struct {
	AssetVersionID string
	TaskID         *string
	Path           string
	Reason         string
}

type MixDraftAudit struct {
	Inspected int
	Staled    int
	Findings  []MixDraftAuditFinding
}

type DraftDisplayReconcileCandidate struct {
	AssetVersionID, AssetID, TaskID, ManifestPath, WorkspacePath, RegisteredPath string
	CurrentFilename, CurrentSHA256, DisplayName                                  string
}

type DraftDisplayReconcileSuccess struct {
	Candidate DraftDisplayReconcileCandidate
	SHA256    string
	DraftID   string
}
