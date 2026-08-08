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

type TaskTimingSummary struct {
	TaskID              string
	TotalMS             int64
	PreparationMS       int64
	QueueMS             int64
	ExecutionMS         int64
	QueueEstimated      bool
	SlowestPhase        *TaskPhaseRun
	SlowestPhasePercent float64
	Phases              []TaskPhaseRun
	LegacyWithoutPhases bool
}

type TaskTimingAggregate struct {
	Action            TaskAction
	TaskCount         int
	MedianTotalMS     int64
	MaxTotalMS        int64
	MedianExecutionMS int64
	MaxExecutionMS    int64
	Phases            []TaskPhaseTimingAggregate
}

type TaskPhaseTimingAggregate struct {
	PhaseKey         string
	DisplayName      string
	Samples          int
	MedianDurationMS int64
	MaxDurationMS    int64
}
