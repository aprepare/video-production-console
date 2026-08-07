package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"video-production-console/internal/domain"
)

var (
	ErrWorkflowNotFound   = errors.New("workflow run not found")
	ErrWorkflowTransition = errors.New("invalid workflow transition")
)

const workflowColumns = `id,project_id,account_id,kind,state,current_step,topic_task_id,remix_task_id,model_name,reasoning_effort,error_code,error_message,created_at,updated_at,finished_at`

type WorkflowRepository struct{ db *sql.DB }

func NewWorkflowRepository(db *sql.DB) *WorkflowRepository { return &WorkflowRepository{db: db} }

func (r *WorkflowRepository) BeginRemix(ctx context.Context, requested domain.ProjectWorkflowRun) (out domain.ProjectWorkflowRun, returnErr error) {
	if requested.Kind == "" {
		requested.Kind = domain.WorkflowRemix
	}
	if requested.State == "" {
		requested.State = domain.WorkflowRunning
	}
	if requested.CurrentStep == "" {
		requested.CurrentStep = domain.WorkflowStepTopicCard
	}
	if requested.UpdatedAt.IsZero() {
		requested.UpdatedAt = requested.CreatedAt
	}
	if requested.ID == "" || requested.ProjectID == "" || requested.AccountID == "" || requested.Kind != domain.WorkflowRemix || requested.State != domain.WorkflowRunning || requested.CurrentStep != domain.WorkflowStepTopicCard || requested.TopicTaskID != nil || requested.RemixTaskID != nil || requested.ErrorCode != nil || requested.ErrorMessage != nil || requested.FinishedAt != nil || strings.TrimSpace(requested.ModelName) == "" || strings.TrimSpace(requested.ReasoningEffort) == "" || requested.CreatedAt.IsZero() {
		return out, fmt.Errorf("begin remix: invalid workflow run")
	}
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return out, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return out, fmt.Errorf("begin remix transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()
	out, err = queryWorkflow(ctx, conn, `SELECT `+workflowColumns+` FROM project_workflow_runs WHERE project_id=? AND kind=? AND state='running'`, requested.ProjectID, requested.Kind)
	if err == nil {
		if _, err = conn.ExecContext(ctx, `COMMIT`); err != nil {
			return out, fmt.Errorf("commit begin remix outcome unknown: %w", err)
		}
		committed = true
		return out, nil
	}
	if !errors.Is(err, ErrWorkflowNotFound) {
		return out, err
	}
	var accountID string
	if err := conn.QueryRowContext(ctx, `SELECT account_id FROM projects WHERE id=?`, requested.ProjectID).Scan(&accountID); errors.Is(err, sql.ErrNoRows) {
		return out, ErrProjectNotFound
	} else if err != nil {
		return out, err
	}
	if accountID != requested.AccountID {
		return out, fmt.Errorf("begin remix: project account mismatch")
	}
	_, err = conn.ExecContext(ctx, `INSERT INTO project_workflow_runs(`+workflowColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, requested.ID, requested.ProjectID, requested.AccountID, requested.Kind, requested.State, requested.CurrentStep, requested.TopicTaskID, requested.RemixTaskID, requested.ModelName, requested.ReasoningEffort, requested.ErrorCode, requested.ErrorMessage, requested.CreatedAt, requested.UpdatedAt, requested.FinishedAt)
	if err != nil {
		return out, fmt.Errorf("insert remix workflow: %w", err)
	}
	out = requested
	if _, err = conn.ExecContext(ctx, `COMMIT`); err != nil {
		return out, fmt.Errorf("commit begin remix outcome unknown: %w", err)
	}
	committed = true
	return out, nil
}

func (r *WorkflowRepository) BindTopicTask(ctx context.Context, runID, taskID string, now time.Time) (domain.ProjectWorkflowRun, error) {
	return r.transition(ctx, runID, func(q assetDBTX, run domain.ProjectWorkflowRun) error {
		if run.TopicTaskID != nil {
			if *run.TopicTaskID == taskID {
				return nil
			}
			return ErrWorkflowTransition
		}
		if run.State != domain.WorkflowRunning || run.CurrentStep != domain.WorkflowStepTopicCard {
			return ErrWorkflowTransition
		}
		result, err := q.ExecContext(ctx, `UPDATE project_workflow_runs SET topic_task_id=?,updated_at=? WHERE id=? AND state='running' AND current_step='topic_card' AND topic_task_id IS NULL`, taskID, now, runID)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return ErrWorkflowTransition
		}
		return nil
	})
}

func (r *WorkflowRepository) AdvanceToRemix(ctx context.Context, runID, taskID string, now time.Time) (domain.ProjectWorkflowRun, error) {
	return r.transition(ctx, runID, func(q assetDBTX, run domain.ProjectWorkflowRun) error {
		if run.CurrentStep == domain.WorkflowStepRemix && run.RemixTaskID != nil && *run.RemixTaskID == taskID {
			return nil
		}
		if run.State != domain.WorkflowRunning || run.CurrentStep != domain.WorkflowStepTopicCard || run.TopicTaskID == nil || run.RemixTaskID != nil {
			return ErrWorkflowTransition
		}
		result, err := q.ExecContext(ctx, `UPDATE project_workflow_runs SET current_step='remix',remix_task_id=?,updated_at=? WHERE id=? AND state='running' AND current_step='topic_card' AND topic_task_id IS NOT NULL AND remix_task_id IS NULL`, taskID, now, runID)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return ErrWorkflowTransition
		}
		return nil
	})
}

func (r *WorkflowRepository) Complete(ctx context.Context, runID string, now time.Time) (domain.ProjectWorkflowRun, error) {
	return r.transition(ctx, runID, func(q assetDBTX, run domain.ProjectWorkflowRun) error {
		if run.State == domain.WorkflowCompleted {
			return nil
		}
		if run.State != domain.WorkflowRunning || run.CurrentStep != domain.WorkflowStepRemix {
			return ErrWorkflowTransition
		}
		_, err := q.ExecContext(ctx, `UPDATE project_workflow_runs SET state='completed',current_step='completed',updated_at=?,finished_at=? WHERE id=? AND state='running' AND current_step='remix'`, now, now, runID)
		return err
	})
}

func (r *WorkflowRepository) Fail(ctx context.Context, runID, code, message string, now time.Time) (domain.ProjectWorkflowRun, error) {
	return r.transition(ctx, runID, func(q assetDBTX, run domain.ProjectWorkflowRun) error {
		if run.State == domain.WorkflowFailed {
			return nil
		}
		if run.State != domain.WorkflowRunning {
			return ErrWorkflowTransition
		}
		_, err := q.ExecContext(ctx, `UPDATE project_workflow_runs SET state='failed',error_code=?,error_message=?,updated_at=?,finished_at=? WHERE id=? AND state='running'`, nullable(code), nullable(message), now, now, runID)
		return err
	})
}

func (r *WorkflowRepository) ActiveForProject(ctx context.Context, projectID string, kind domain.WorkflowKind) (domain.ProjectWorkflowRun, error) {
	return queryWorkflow(ctx, r.db, `SELECT `+workflowColumns+` FROM project_workflow_runs WHERE project_id=? AND kind=? AND state='running'`, projectID, kind)
}

func (r *WorkflowRepository) ByTask(ctx context.Context, taskID string) (domain.ProjectWorkflowRun, error) {
	return queryWorkflow(ctx, r.db, `SELECT `+workflowColumns+` FROM project_workflow_runs WHERE topic_task_id=? OR remix_task_id=? ORDER BY created_at DESC,id DESC LIMIT 1`, taskID, taskID)
}

func (r *WorkflowRepository) transition(ctx context.Context, runID string, change func(assetDBTX, domain.ProjectWorkflowRun) error) (out domain.ProjectWorkflowRun, returnErr error) {
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return out, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return out, err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()
	out, err = queryWorkflow(ctx, conn, `SELECT `+workflowColumns+` FROM project_workflow_runs WHERE id=?`, runID)
	if err != nil {
		return out, err
	}
	if err := change(conn, out); err != nil {
		return out, err
	}
	out, err = queryWorkflow(ctx, conn, `SELECT `+workflowColumns+` FROM project_workflow_runs WHERE id=?`, runID)
	if err != nil {
		return out, err
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return out, fmt.Errorf("commit workflow transition outcome unknown: %w", err)
	}
	committed = true
	return out, nil
}

type workflowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func queryWorkflow(ctx context.Context, q workflowQueryer, query string, args ...any) (domain.ProjectWorkflowRun, error) {
	var run domain.ProjectWorkflowRun
	err := q.QueryRowContext(ctx, query, args...).Scan(&run.ID, &run.ProjectID, &run.AccountID, &run.Kind, &run.State, &run.CurrentStep, &run.TopicTaskID, &run.RemixTaskID, &run.ModelName, &run.ReasoningEffort, &run.ErrorCode, &run.ErrorMessage, &run.CreatedAt, &run.UpdatedAt, &run.FinishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return run, ErrWorkflowNotFound
	}
	return run, err
}
