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
	ID             string            `json:"id"`
	TaskID         string            `json:"task_id"`
	ManifestPath   string            `json:"manifest_path"`
	WorkspacePath  string            `json:"workspace_path"`
	State          RegistrationState `json:"state"`
	Attempt        int               `json:"attempt"`
	RegisteredPath *string           `json:"registered_path"`
	ReceiptPath    *string           `json:"receipt_path"`
	ErrorCode      *string           `json:"error_code"`
	ErrorMessage   *string           `json:"error_message"`
	StartedAt      time.Time         `json:"started_at"`
	FinishedAt     *time.Time        `json:"finished_at"`
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
