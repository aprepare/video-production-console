package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/domain"
)

type TaskRepository struct{ db *sql.DB }

type TaskResultWrite struct {
	Status           domain.TaskStatus
	Summary          string
	AssistantContent string
	QuestionSchema   *string
	EventKind        string
	RawJSON          string
	ErrorCode        string
	ErrorMessage     string
}

type TaskArtifact struct {
	ID        string
	TaskID    string
	Kind      string
	Path      string
	Filename  string
	MIMEType  string
	Size      int64
	SHA256    string
	CreatedAt time.Time
}

func NewTaskRepository(db *sql.DB) *TaskRepository { return &TaskRepository{db: db} }
func (r *TaskRepository) DB() *sql.DB              { return r.db }

func (r *TaskRepository) Start(ctx context.Context, id, configSnapshotJSON string) error {
	if !json.Valid([]byte(configSnapshotJSON)) {
		return fmt.Errorf("config snapshot must be valid JSON")
	}
	return r.immediate(ctx, "start task", func(q assetDBTX, now time.Time) error {
		result, err := q.ExecContext(ctx, `UPDATE codex_tasks SET status=?,config_snapshot_json=?,started_at=COALESCE(started_at,?),finished_at=NULL,error_code=NULL,error_message=NULL WHERE id=?`, domain.TaskRunning, configSnapshotJSON, now, id)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil {
			return err
		} else if affected != 1 {
			return fmt.Errorf("task %q not found", id)
		}
		return nil
	})
}

func (r *TaskRepository) ExpectedAction(ctx context.Context, id string) (domain.TaskAction, error) {
	var action sql.NullString
	if err := r.db.QueryRowContext(ctx, `SELECT action FROM codex_tasks WHERE id=?`, id).Scan(&action); errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("task %q: %w", id, sql.ErrNoRows)
	} else if err != nil {
		return "", err
	}
	if !action.Valid || strings.TrimSpace(action.String) == "" {
		return "", fmt.Errorf("task %q has no action", id)
	}
	return domain.TaskAction(action.String), nil
}

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
func (r *TaskRepository) List(ctx context.Context, projectID string, status domain.TaskStatus) ([]domain.CodexTask, error) {
	q := `SELECT id,project_id,account_id,type,skill_name,status,codex_session_id,prompt_snapshot,result_summary,error_code,error_message,created_at,started_at,finished_at FROM codex_tasks WHERE 1=1`
	args := []any{}
	if projectID != "" {
		q += " AND project_id=?"
		args = append(args, projectID)
	}
	if status != "" {
		q += " AND status=?"
		args = append(args, status)
	}
	q += " ORDER BY created_at DESC"
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.CodexTask
	for rows.Next() {
		var t domain.CodexTask
		if err := rows.Scan(&t.ID, &t.ProjectID, &t.AccountID, &t.Type, &t.SkillName, &t.Status, &t.CodexSessionID, &t.PromptSnapshot, &t.ResultSummary, &t.ErrorCode, &t.ErrorMessage, &t.CreatedAt, &t.StartedAt, &t.FinishedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (r *TaskRepository) AddMessage(ctx context.Context, message domain.TaskMessage) error {
	if message.ID == "" {
		message.ID = uuid.NewString()
	}
	if message.CreatedAt.IsZero() {
		message.CreatedAt = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO task_messages(id,task_id,role,content,question_schema,created_at) VALUES(?,?,?,?,?,?)`, message.ID, message.TaskID, message.Role, message.Content, nullablePtr(message.QuestionSchema), message.CreatedAt)
	return err
}

func insertMessage(ctx context.Context, q assetDBTX, message domain.TaskMessage) error {
	if message.ID == "" {
		message.ID = uuid.NewString()
	}
	if message.CreatedAt.IsZero() {
		message.CreatedAt = time.Now().UTC()
	}
	_, err := q.ExecContext(ctx, `INSERT INTO task_messages(id,task_id,role,content,question_schema,created_at) VALUES(?,?,?,?,?,?)`, message.ID, message.TaskID, message.Role, message.Content, nullablePtr(message.QuestionSchema), message.CreatedAt)
	return err
}
func nullablePtr(v *string) any {
	if v == nil {
		return nil
	}
	return *v
}
func (r *TaskRepository) Messages(ctx context.Context, taskID string) ([]domain.TaskMessage, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,task_id,role,content,question_schema,created_at FROM task_messages WHERE task_id=? ORDER BY created_at,id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.TaskMessage
	for rows.Next() {
		var m domain.TaskMessage
		var q sql.NullString
		if err := rows.Scan(&m.ID, &m.TaskID, &m.Role, &m.Content, &q, &m.CreatedAt); err != nil {
			return nil, err
		}
		if q.Valid {
			m.QuestionSchema = &q.String
		}
		out = append(out, m)
	}
	return out, rows.Err()
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

func insertEvent(ctx context.Context, q assetDBTX, taskID string, event domain.TaskEvent) error {
	var seq int64
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM task_events WHERE task_id=?`, taskID).Scan(&seq); err != nil {
		return err
	}
	if event.ID == "" {
		event.ID = uuid.NewString()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	_, err := q.ExecContext(ctx, `INSERT INTO task_events(id,task_id,sequence,kind,level,display_text,raw_json,created_at) VALUES(?,?,?,?,?,?,?,?)`, event.ID, taskID, seq, event.Kind, event.Level, event.DisplayText, event.RawJSON, event.CreatedAt)
	return err
}

func (r *TaskRepository) AwaitInput(ctx context.Context, taskID string, result TaskResultWrite) error {
	if result.Status != domain.TaskAwaitingInput || result.QuestionSchema == nil || !json.Valid([]byte(*result.QuestionSchema)) {
		return fmt.Errorf("awaiting input requires a valid question schema")
	}
	return r.immediate(ctx, "await task input", func(q assetDBTX, now time.Time) error {
		if err := insertMessage(ctx, q, domain.TaskMessage{TaskID: taskID, Role: "assistant", Content: result.AssistantContent, QuestionSchema: result.QuestionSchema, CreatedAt: now}); err != nil {
			return err
		}
		if err := insertEvent(ctx, q, taskID, domain.TaskEvent{Kind: result.EventKind, Level: "info", DisplayText: result.Summary, RawJSON: result.RawJSON, CreatedAt: now}); err != nil {
			return err
		}
		updated, err := q.ExecContext(ctx, `UPDATE codex_tasks SET status=?,result_summary=?,error_code=NULL,error_message=NULL,finished_at=NULL WHERE id=?`, domain.TaskAwaitingInput, nullable(result.Summary), taskID)
		if err != nil {
			return err
		}
		if affected, err := updated.RowsAffected(); err != nil || affected != 1 {
			if err != nil {
				return err
			}
			return fmt.Errorf("task %q not found", taskID)
		}
		return nil
	})
}

func (r *TaskRepository) CompleteWithResult(ctx context.Context, taskID string, result TaskResultWrite, artifacts []TaskArtifact, assets []AddAssetVersion) error {
	if result.Status != domain.TaskCompleted && result.Status != domain.TaskFailed {
		return fmt.Errorf("terminal result status must be completed or failed")
	}
	return r.immediate(ctx, "complete task", func(q assetDBTX, now time.Time) error {
		if result.AssistantContent != "" {
			if err := insertMessage(ctx, q, domain.TaskMessage{TaskID: taskID, Role: "assistant", Content: result.AssistantContent, CreatedAt: now}); err != nil {
				return err
			}
		}
		if result.EventKind != "" {
			if err := insertEvent(ctx, q, taskID, domain.TaskEvent{Kind: result.EventKind, Level: "info", DisplayText: result.Summary, RawJSON: result.RawJSON, CreatedAt: now}); err != nil {
				return err
			}
		}
		for _, artifact := range artifacts {
			artifact.TaskID = taskID
			if artifact.CreatedAt.IsZero() {
				artifact.CreatedAt = now
			}
			if err := insertTaskArtifact(ctx, q, artifact); err != nil {
				return err
			}
		}
		assetsRepo := NewAssetRepository(r.db)
		for _, asset := range assets {
			if asset.SourceTaskID == nil {
				asset.SourceTaskID = &taskID
			}
			if _, err := assetsRepo.addVersion(ctx, q, asset, now); err != nil {
				return err
			}
		}
		updated, err := q.ExecContext(ctx, `UPDATE codex_tasks SET status=?,result_summary=?,error_code=?,error_message=?,finished_at=? WHERE id=?`, result.Status, nullable(result.Summary), nullable(result.ErrorCode), nullable(result.ErrorMessage), now, taskID)
		if err != nil {
			return err
		}
		if affected, err := updated.RowsAffected(); err != nil || affected != 1 {
			if err != nil {
				return err
			}
			return fmt.Errorf("task %q not found", taskID)
		}
		return nil
	})
}

func insertTaskArtifact(ctx context.Context, q assetDBTX, artifact TaskArtifact) error {
	if strings.TrimSpace(artifact.TaskID) == "" || strings.TrimSpace(artifact.Kind) == "" || strings.TrimSpace(artifact.Path) == "" || strings.TrimSpace(artifact.Filename) == "" || strings.TrimSpace(artifact.MIMEType) == "" || artifact.Size < 0 {
		return fmt.Errorf("invalid task artifact")
	}
	if artifact.ID == "" {
		artifact.ID = uuid.NewString()
	}
	if artifact.CreatedAt.IsZero() {
		artifact.CreatedAt = time.Now().UTC()
	}
	_, err := q.ExecContext(ctx, `INSERT INTO task_artifacts(id,task_id,kind,path,filename,mime_type,size,sha256,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, artifact.ID, artifact.TaskID, artifact.Kind, artifact.Path, artifact.Filename, artifact.MIMEType, artifact.Size, artifact.SHA256, artifact.CreatedAt)
	return err
}

func (r *TaskRepository) Artifacts(ctx context.Context, taskID string) ([]TaskArtifact, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,task_id,kind,path,filename,mime_type,size,sha256,created_at FROM task_artifacts WHERE task_id=? ORDER BY created_at,id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TaskArtifact{}
	for rows.Next() {
		var artifact TaskArtifact
		if err := rows.Scan(&artifact.ID, &artifact.TaskID, &artifact.Kind, &artifact.Path, &artifact.Filename, &artifact.MIMEType, &artifact.Size, &artifact.SHA256, &artifact.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, artifact)
	}
	return out, rows.Err()
}

func (r *TaskRepository) immediate(ctx context.Context, operation string, fn func(assetDBTX, time.Time) error) error {
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return fmt.Errorf("begin %s: %w", operation, err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()
	if err := fn(conn, time.Now().UTC()); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("commit %s outcome unknown: %w", operation, err)
	}
	committed = true
	return nil
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
