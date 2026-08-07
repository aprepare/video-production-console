package taskcompletion

import (
	"context"

	"video-production-console/internal/domain"
	"video-production-console/internal/store"
)

type CompletedInput struct {
	Task           domain.CodexTask
	ManifestPath   string
	Action         domain.TaskAction
	Summary        string
	RawJSON        string
	Artifacts      []store.TaskArtifact
	ExpectedTurnID *string
}

type Gate interface {
	HandleCompleted(context.Context, CompletedInput) (bool, error)
}
