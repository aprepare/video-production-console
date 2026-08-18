package taskcompletion

import (
	"context"

	"video-production-console/internal/domain"
	"video-production-console/internal/store"
)

type CompletedInput struct {
	Task               domain.CodexTask
	ManifestPath       string
	Action             domain.TaskAction
	Summary            string
	RawJSON            string
	Artifacts          []store.TaskArtifact
	ExpectedTurnID     *string
	ValidationPhaseID  string
	AssetCommitPhaseID string
	SkillTimings       []domain.SkillTimingRun
}

type Gate interface {
	HandleCompleted(context.Context, CompletedInput) (bool, error)
}

const ObserverWarningEvent = "workflow_observer_warning"

// Observer runs only after a terminal CodexTask state is durably committed.
type Observer interface {
	AfterTerminal(context.Context, domain.CodexTask) error
}
