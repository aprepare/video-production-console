package timing

import (
	"context"
	"time"

	"video-production-console/internal/domain"
	"video-production-console/internal/store"
)

// Record persists one safe classification. Replayed starts collapse through
// external_id; completions close the exact item run. Codex execution is the
// sole exception: StartAppServerTurn owns its start, so generic turn starts are
// ignored and terminal turn boundaries close the currently active execution
// phase even though its host external ID differs from the App Server turn ID.
func Record(ctx context.Context, repo *store.TaskTimingRepository, taskID string, source domain.TaskPhaseSource, classification Classification, at time.Time) error {
	if repo == nil || taskID == "" || classification.PhaseKey == "" || classification.ExternalItemID == "" || at.IsZero() {
		return nil
	}
	phases, err := repo.ForTask(ctx, taskID)
	if err != nil {
		return err
	}
	attempt := 1
	for _, phase := range phases {
		if phase.Attempt > attempt {
			attempt = phase.Attempt
		}
	}
	if classification.Boundary == BoundaryStart {
		if classification.PhaseKey == "codex_execution" {
			return nil
		}
		_, err = repo.StartPhase(ctx, store.StartPhase{
			TaskID: taskID, Attempt: attempt, Key: classification.PhaseKey,
			DisplayName: classification.DisplayName, Source: source,
			ExternalID: classification.ExternalItemID, DetailJSON: classification.DetailJSON, At: at,
		})
		return err
	}

	var target *domain.TaskPhaseRun
	for i := len(phases) - 1; i >= 0; i-- {
		phase := &phases[i]
		if phase.Attempt != attempt || phase.PhaseKey != classification.PhaseKey || phase.FinishedAt != nil {
			continue
		}
		if phase.ExternalID == classification.ExternalItemID || classification.PhaseKey == "codex_execution" {
			target = phase
			break
		}
	}
	if target == nil {
		return nil
	}
	state := domain.PhaseCompleted
	switch classification.Boundary {
	case BoundaryFail:
		state = domain.PhaseFailed
	case BoundaryCancel:
		state = domain.PhaseCanceled
	case BoundaryInterrupt:
		state = domain.PhaseInterrupted
	}
	_, err = repo.FinishPhase(ctx, store.FinishPhase{ID: target.ID, State: state, At: at})
	return err
}
