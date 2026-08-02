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

type AssetType string

const (
	AssetContinuousScript  AssetType = "continuous_script"
	AssetSpokenScript      AssetType = "spoken_script"
	AssetAudio             AssetType = "audio"
	AssetSubtitle          AssetType = "subtitle"
	AssetAccountBackground AssetType = "account_background"
	AssetMixDraft          AssetType = "mix_draft"
	AssetFinalVideo        AssetType = "final_video"
)

type TaskStatus string

const (
	TaskQueued       TaskStatus = "queued"
	TaskRunning      TaskStatus = "running"
	TaskWaitingInput TaskStatus = "waiting_input"
	TaskCompleted    TaskStatus = "completed"
	TaskFailed       TaskStatus = "failed"
	TaskCancelled    TaskStatus = "cancelled"
)

type Account struct {
	ID                string
	Name              string
	BackgroundAssetID *string
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
