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
	ID          string          `json:"id"`
	TaskID      string          `json:"task_id"`
	PhaseKey    string          `json:"phase_key"`
	DisplayName string          `json:"display_name"`
	ExternalID  string          `json:"external_id"`
	DetailJSON  string          `json:"detail_json"`
	Attempt     int             `json:"attempt"`
	Source      TaskPhaseSource `json:"source"`
	State       TaskPhaseState  `json:"state"`
	StartedAt   time.Time       `json:"started_at"`
	RunningAt   *time.Time      `json:"running_at"`
	FinishedAt  *time.Time      `json:"finished_at"`
	DurationMS  *int64          `json:"duration_ms"`
	CreatedAt   time.Time       `json:"created_at"`
}

type SkillTimingRun struct {
	TaskID          string
	SkillSnapshotID string
	PhaseKey        string
	DisplayName     string
	ExternalID      string
	DetailJSON      string
	Attempt         int
	State           TaskPhaseState
	StartedAt       time.Time
	FinishedAt      time.Time
	DurationMS      int64
}

type TaskTimingSummary struct {
	TaskID              string         `json:"task_id"`
	TotalMS             int64          `json:"total_ms"`
	PreparationMS       int64          `json:"preparation_ms"`
	QueueMS             int64          `json:"queue_ms"`
	ExecutionMS         int64          `json:"execution_ms"`
	QueueEstimated      bool           `json:"queue_estimated"`
	SlowestPhase        *TaskPhaseRun  `json:"slowest_phase"`
	SlowestPhasePercent float64        `json:"slowest_phase_percent"`
	Phases              []TaskPhaseRun `json:"phases"`
	LegacyWithoutPhases bool           `json:"legacy_without_phases"`
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
