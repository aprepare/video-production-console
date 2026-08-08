package domain

import "time"

type TaskPhaseSource string

const (
	PhaseSourceHost      TaskPhaseSource = "host"
	PhaseSourceAppServer TaskPhaseSource = "app_server"
	PhaseSourceSkill     TaskPhaseSource = "skill"
)

type TaskPhaseState string

const (
	PhaseQueued      TaskPhaseState = "queued"
	PhaseRunning     TaskPhaseState = "running"
	PhaseCompleted   TaskPhaseState = "completed"
	PhaseFailed      TaskPhaseState = "failed"
	PhaseCanceled    TaskPhaseState = "canceled"
	PhaseInterrupted TaskPhaseState = "interrupted"
)

type TaskPhaseRun struct {
	ID          string
	TaskID      string
	PhaseKey    string
	DisplayName string
	ExternalID  string
	DetailJSON  string
	Attempt     int
	Source      TaskPhaseSource
	State       TaskPhaseState
	StartedAt   time.Time
	RunningAt   *time.Time
	FinishedAt  *time.Time
	DurationMS  *int64
	CreatedAt   time.Time
}
