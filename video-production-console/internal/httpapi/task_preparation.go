package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/domain"
	"video-production-console/internal/store"
	"video-production-console/internal/taskmodel"
)

type taskPublishError struct{ err error }

func (e taskPublishError) Error() string { return e.err.Error() }
func (e taskPublishError) Unwrap() error { return e.err }

func prepareAndPublishTask(ctx context.Context, db *sql.DB, preparer TaskManifestPreparer, task domain.CodexTask, request TaskManifestRequest, startedAt time.Time, publish func(context.Context, domain.CodexTask) error, now func() time.Time) (domain.CodexTask, error) {
	if db == nil || publish == nil {
		return domain.CodexTask{}, errors.New("task preparation service is unavailable")
	}
	if preparer == nil {
		return publishQueuedTask(ctx, db, task, publish)
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if startedAt.IsZero() {
		startedAt = now()
	}
	request.PreparationStartedAt = startedAt
	prepareErr := preparer.Prepare(ctx, task, request)
	if prepareErr != nil {
		finishedAt := now()
		if err := persistPreparationFailure(ctx, db, task, startedAt, finishedAt, prepareErr); err != nil {
			return domain.CodexTask{}, errors.Join(prepareErr, err)
		}
		return domain.CodexTask{}, prepareErr
	}
	if _, _, err := store.NewTaskRepository(db).PreparedManifest(ctx, task.ID); err != nil {
		return domain.CodexTask{}, fmt.Errorf("prepared task manifest was not persisted: %w", err)
	}
	return publishQueuedTask(ctx, db, task, publish)
}

func publishQueuedTask(ctx context.Context, db *sql.DB, task domain.CodexTask, publish func(context.Context, domain.CodexTask) error) (domain.CodexTask, error) {
	if db == nil || publish == nil {
		return domain.CodexTask{}, errors.New("task publishing service is unavailable")
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

func persistPreparationFailure(ctx context.Context, db *sql.DB, task domain.CodexTask, startedAt, finishedAt time.Time, cause error) error {
	if db == nil || cause == nil || startedAt.IsZero() || finishedAt.IsZero() || finishedAt.Before(startedAt) {
		return errors.New("valid task preparation failure boundaries are required")
	}
	selection, err := taskmodel.Resolve(
		taskmodel.Selection{Model: taskmodel.DefaultModel, ReasoningEffort: taskmodel.DefaultReasoningEffort},
		taskmodel.Selection{Model: task.ModelName, ReasoningEffort: task.ReasoningEffort},
	)
	if err != nil {
		return err
	}
	completionPhase := task.CompletionPhase
	if completionPhase == "" {
		completionPhase = "agent_running"
	}
	transport := task.Transport
	if transport == "" {
		transport = "legacy_exec"
	}
	message, code := cause.Error(), "task_prepare_failed"
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var status domain.TaskStatus
	readErr := tx.QueryRowContext(ctx, `SELECT status FROM codex_tasks WHERE id=?`, task.ID).Scan(&status)
	switch {
	case errors.Is(readErr, sql.ErrNoRows):
		_, err = tx.ExecContext(ctx, `INSERT INTO codex_tasks(id,project_id,account_id,type,skill_name,action,status,codex_session_id,chat_session_id,codex_thread_id,codex_turn_id,completion_phase,transport,prompt_snapshot,model_name,reasoning_effort,error_code,error_message,created_at,finished_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, task.ID, task.ProjectID, task.AccountID, task.Type, task.SkillName, task.Action, domain.TaskFailed, task.CodexSessionID, task.ChatSessionID, task.CodexThreadID, task.CodexTurnID, completionPhase, transport, task.PromptSnapshot, selection.Model, selection.ReasoningEffort, code, message, task.CreatedAt, finishedAt)
	case readErr != nil:
		return readErr
	default:
		_, err = tx.ExecContext(ctx, `UPDATE codex_tasks SET status=?,error_code=?,error_message=?,finished_at=COALESCE(finished_at,?) WHERE id=?`, domain.TaskFailed, code, message, finishedAt, task.ID)
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO task_phase_runs(id,task_id,attempt,phase_key,display_name,source,state,started_at,running_at,finished_at,duration_ms,external_id,detail_json,created_at)
		SELECT ?,?,1,'task_prepare','任务准备',?,'failed',?,?,?,?, 'task_prepare','{}',?
		WHERE NOT EXISTS (SELECT 1 FROM task_phase_runs WHERE task_id=? AND attempt=1 AND phase_key='task_prepare' AND source=? AND external_id='task_prepare')`, uuid.NewString(), task.ID, domain.PhaseSourceHost, startedAt, startedAt, finishedAt, finishedAt.Sub(startedAt).Milliseconds(), startedAt, task.ID, domain.PhaseSourceHost)
	if err != nil {
		return err
	}
	return tx.Commit()
}
