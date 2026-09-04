package domain

import "time"

type ProjectStage string

const (
	// Deprecated: retained so historical project and audit data can be decoded.
	StageTopic  ProjectStage = "topic"
	StageScript ProjectStage = "script"
	StageAssets ProjectStage = "assets"
	StageMixing ProjectStage = "mixing"
	StageReview ProjectStage = "review"
	// Deprecated: retained so historical project and audit data can be decoded.
	StageReady     ProjectStage = "ready"
	StagePublished ProjectStage = "published"
	StageArchived  ProjectStage = "archived"
)

type TaskStatus string

const (
	TaskQueued        TaskStatus = "queued"
	TaskRunning       TaskStatus = "running"
	TaskAwaitingInput TaskStatus = "awaiting_input"
	TaskResuming      TaskStatus = "resuming"
	TaskCompleted     TaskStatus = "completed"
	TaskFailed        TaskStatus = "failed"
	TaskCanceled      TaskStatus = "canceled"
	TaskInterrupted   TaskStatus = "interrupted"

	// Deprecated: retained while legacy task persistence is migrated.
	TaskWaitingInput TaskStatus = "waiting_input"
	// Deprecated: retained while legacy task persistence is migrated.
	TaskCancelled TaskStatus = "cancelled"
)

// Canonical maps historical spellings to the current persisted vocabulary.
func (s TaskStatus) Canonical() TaskStatus {
	switch s {
	case TaskWaitingInput:
		return TaskAwaitingInput
	case TaskCancelled:
		return TaskCanceled
	default:
		return s
	}
}

func (s TaskStatus) IsTerminal() bool {
	switch s.Canonical() {
	case TaskCompleted, TaskFailed, TaskCanceled, TaskInterrupted:
		return true
	default:
		return false
	}
}

func (s TaskStatus) IsWaitingForInput() bool {
	return s.Canonical() == TaskAwaitingInput
}

func (s TaskStatus) IsActive() bool {
	switch s.Canonical() {
	case TaskQueued, TaskRunning, TaskAwaitingInput, TaskResuming:
		return true
	default:
		return false
	}
}

type Account struct {
	ID                string
	Name              string
	BackgroundAssetID *string
	BackgroundPath    *string
	Color             string
	Status            string
	Overrides         *AccountOverrides
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type Project struct {
	ID            string
	AccountID     string
	Title         string
	Stage         ProjectStage
	Status        ProjectStatus
	TopicCardPath *string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	ReadyAt       *time.Time
	PublishedAt   *time.Time
	PublishNote   *string
}

type Asset struct {
	ID           string
	ProjectID    *string
	AccountID    string
	Type         AssetType
	Path         string
	Filename     string
	MIMEType     string
	Size         int64
	SHA256       string
	Version      int
	Status       string
	CreatedAt    time.Time
	SourceTaskID *string
}

type CodexTask struct {
	ID        string
	ProjectID *string
	AccountID string
	Type      string
	SkillName string
	// Action is the versioned workflow contract used to validate the result.
	// Type is retained only as the compatibility-facing task family.
	Action          TaskAction
	Status          TaskStatus
	CodexSessionID  *string
	ChatSessionID   *string
	CodexThreadID   *string
	CodexTurnID     *string
	CompletionPhase string
	Transport       string
	PromptSnapshot  string
	ModelName       string
	ReasoningEffort string
	ResultSummary   *string
	ErrorCode       *string
	ErrorMessage    *string
	CreatedAt       time.Time
	QueuedAt        *time.Time
	StartedAt       *time.Time
	FinishedAt      *time.Time
}

type TaskEvent struct {
	ID          string    `json:"id"`
	TaskID      string    `json:"task_id"`
	Sequence    int64     `json:"sequence"`
	Kind        string    `json:"kind"`
	Level       string    `json:"level"`
	DisplayText string    `json:"display_text"`
	RawJSON     string    `json:"raw_json,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type TaskMessage struct {
	ID             string    `json:"id"`
	TaskID         string    `json:"task_id"`
	Role           string    `json:"role"`
	Content        string    `json:"content"`
	QuestionSchema *string   `json:"question_schema,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}
