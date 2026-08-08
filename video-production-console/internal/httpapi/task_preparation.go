package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"video-production-console/internal/domain"
	"video-production-console/internal/store"
)

type taskPublishError struct{ err error }

func (e taskPublishError) Error() string { return e.err.Error() }
func (e taskPublishError) Unwrap() error { return e.err }

func prepareAndPublishTask(ctx context.Context, db *sql.DB, preparer TaskManifestPreparer, task domain.CodexTask, request TaskManifestRequest, startedAt time.Time, publish func(context.Context, domain.CodexTask) error, now func() time.Time) (domain.CodexTask, error) {
	if db == nil || preparer == nil || publish == nil {
		return domain.CodexTask{}, errors.New("task preparation service is unavailable")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if startedAt.IsZero() {
		startedAt = now()
	}
	prepareErr := preparer.Prepare(ctx, task, request)
	finishedAt := now()
	if prepareErr != nil {
		if err := persistPreparationFailure(ctx, db, task, finishedAt, prepareErr); err != nil {
			return domain.CodexTask{}, errors.Join(prepareErr, err)
		}
	}
	timings := store.NewTaskTimingRepository(db)
	phase, phaseErr := timings.StartPhase(ctx, store.StartPhase{TaskID: task.ID, Attempt: 1, Key: "task_prepare", DisplayName: "任务准备", Source: domain.PhaseSourceHost, ExternalID: "task_prepare", At: startedAt})
	if phaseErr == nil {
		state := domain.PhaseCompleted
		if prepareErr != nil {
			state = domain.PhaseFailed
		}
		_, phaseErr = timings.FinishPhase(ctx, store.FinishPhase{ID: phase.ID, State: state, At: finishedAt})
	}
	if phaseErr != nil {
		return domain.CodexTask{}, errors.Join(prepareErr, fmt.Errorf("persist task preparation timing: %w", phaseErr))
	}
	if prepareErr != nil {
		return domain.CodexTask{}, prepareErr
	}
	if err := publish(ctx, task); err != nil {
		return domain.CodexTask{}, taskPublishError{err: err}
	}
	persisted, err := store.NewTaskRepository(db).Get(context.WithoutCancel(ctx), task.ID)
	if err != nil {
		return domain.CodexTask{}, err
	}
	return persisted, nil
}

func persistPreparationFailure(ctx context.Context, db *sql.DB, task domain.CodexTask, finishedAt time.Time, cause error) error {
	repo := store.NewTaskRepository(db)
	message, code := cause.Error(), "task_prepare_failed"
	task.Status, task.ErrorCode, task.ErrorMessage, task.FinishedAt = domain.TaskFailed, &code, &message, &finishedAt
	if err := repo.CreateV2(ctx, task); err == nil {
		return nil
	} else if _, readErr := repo.Get(ctx, task.ID); errors.Is(readErr, sql.ErrNoRows) {
		return err
	} else if readErr != nil {
		return readErr
	}
	_, err := db.ExecContext(ctx, `UPDATE codex_tasks SET status=?,error_code=?,error_message=?,finished_at=COALESCE(finished_at,?) WHERE id=?`, domain.TaskFailed, code, message, finishedAt, task.ID)
	return err
}
