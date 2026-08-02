package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/domain"
)

type TaskRepository struct{ db *sql.DB }

func NewTaskRepository(db *sql.DB) *TaskRepository { return &TaskRepository{db: db} }
func (r *TaskRepository) DB() *sql.DB              { return r.db }

func (r *TaskRepository) Create(ctx context.Context, task domain.CodexTask) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO codex_tasks(id,project_id,account_id,type,skill_name,status,prompt_snapshot,created_at) VALUES(?,?,?,?,?,?,?,?)`, task.ID, task.ProjectID, task.AccountID, task.Type, task.SkillName, task.Status, task.PromptSnapshot, task.CreatedAt)
	return err
}
func (r *TaskRepository) Get(ctx context.Context, id string) (domain.CodexTask, error) {
	var t domain.CodexTask
	err := r.db.QueryRowContext(ctx, `SELECT id,project_id,account_id,type,skill_name,status,codex_session_id,prompt_snapshot,result_summary,error_code,error_message,created_at,started_at,finished_at FROM codex_tasks WHERE id=?`, id).Scan(&t.ID, &t.ProjectID, &t.AccountID, &t.Type, &t.SkillName, &t.Status, &t.CodexSessionID, &t.PromptSnapshot, &t.ResultSummary, &t.ErrorCode, &t.ErrorMessage, &t.CreatedAt, &t.StartedAt, &t.FinishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return t, fmt.Errorf("task %q: %w", id, sql.ErrNoRows)
	}
	return t, err
}
func (r *TaskRepository) SetSession(ctx context.Context, id, session string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE codex_tasks SET codex_session_id=? WHERE id=?`, session, id)
	return err
}
func (r *TaskRepository) UpdateStatus(ctx context.Context, id string, status domain.TaskStatus, summary, code, message string) error {
	now := time.Now().UTC()
	_, err := r.db.ExecContext(ctx, `UPDATE codex_tasks SET status=?,result_summary=?,error_code=?,error_message=?,finished_at=CASE WHEN ? IN ('completed','failed','cancelled') THEN ? ELSE finished_at END,started_at=CASE WHEN ?='running' AND started_at IS NULL THEN ? ELSE started_at END WHERE id=?`, status, nullable(summary), nullable(code), nullable(message), status, now, status, now, id)
	return err
}
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
func (r *TaskRepository) AppendEvent(ctx context.Context, taskID string, event domain.TaskEvent) error {
	var seq int64
	if err := r.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM task_events WHERE task_id=?`, taskID).Scan(&seq); err != nil {
		return err
	}
	if event.ID == "" {
		event.ID = uuid.NewString()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO task_events(id,task_id,sequence,kind,level,display_text,raw_json,created_at) VALUES(?,?,?,?,?,?,?,?)`, event.ID, taskID, seq, event.Kind, event.Level, event.DisplayText, event.RawJSON, event.CreatedAt)
	return err
}
func (r *TaskRepository) Events(ctx context.Context, taskID string) ([]domain.TaskEvent, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,task_id,sequence,kind,level,display_text,raw_json,created_at FROM task_events WHERE task_id=? ORDER BY sequence`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.TaskEvent
	for rows.Next() {
		var e domain.TaskEvent
		if err := rows.Scan(&e.ID, &e.TaskID, &e.Sequence, &e.Kind, &e.Level, &e.DisplayText, &e.RawJSON, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
