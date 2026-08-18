package domain

import "time"

type WorkflowKind string

const WorkflowRemix WorkflowKind = "remix"

type WorkflowState string

const (
	WorkflowRunning   WorkflowState = "running"
	WorkflowCompleted WorkflowState = "completed"
	WorkflowFailed    WorkflowState = "failed"
	WorkflowCanceled  WorkflowState = "canceled"
)

type WorkflowStep string

const (
	WorkflowStepTopicCard WorkflowStep = "topic_card"
	WorkflowStepRemix     WorkflowStep = "remix"
	WorkflowStepCompleted WorkflowStep = "completed"
)

type ProjectWorkflowRun struct {
	ID              string
	ProjectID       string
	AccountID       string
	Kind            WorkflowKind
	State           WorkflowState
	CurrentStep     WorkflowStep
	TopicTaskID     *string
	RemixTaskID     *string
	ModelName       string
	ReasoningEffort string
	ErrorCode       *string
	ErrorMessage    *string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	FinishedAt      *time.Time
}
