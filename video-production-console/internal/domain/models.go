package domain

import "time"

type ProjectStage string

const (
	StageTopic     ProjectStage = "topic"
	StageScript    ProjectStage = "script"
	StageAssets    ProjectStage = "assets"
	StageMixing    ProjectStage = "mixing"
	StageReview    ProjectStage = "review"
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

type Account struct {
	ID                string
	Name              string
	BackgroundAssetID *string
	BackgroundPath    *string
	Color             string
	Status            string
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
	ID             string
	ProjectID      *string
	AccountID      string
	Type           string
	SkillName      string
	Status         TaskStatus
	CodexSessionID *string
	PromptSnapshot string
	ResultSummary  *string
	ErrorCode      *string
	ErrorMessage   *string
	CreatedAt      time.Time
	StartedAt      *time.Time
	FinishedAt     *time.Time
}

type TaskEvent struct {
	ID          string
	TaskID      string
	Sequence    int64
	Kind        string
	Level       string
	DisplayText string
	RawJSON     string
	CreatedAt   time.Time
}

type TaskMessage struct {
	ID             string
	TaskID         string
	Role           string
	Content        string
	QuestionSchema *string
	CreatedAt      time.Time
}
