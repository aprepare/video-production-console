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
	"video-production-console/internal/taskmodel"
)

const taskColumns = `id,project_id,account_id,type,skill_name,action,status,codex_session_id,chat_session_id,codex_thread_id,codex_turn_id,completion_phase,transport,prompt_snapshot,model_name,reasoning_effort,result_summary,error_code,error_message,created_at,started_at,finished_at`
const formalTaskClientKeyPrefix = "__formal_task__:"

type TaskRepository struct {
	db                   *sql.DB
	beforePreparedCommit func() error
}

var ErrOutputRetryNotEligible = errors.New("task output is not eligible for completion retry")

type TaskResultWrite struct {
	Status           domain.TaskStatus
	Summary          string
	AssistantContent string
	QuestionSchema   *string
	EventKind        string
	RawJSON          string
	ErrorCode        string
	ErrorMessage     string
	ExpectedTurnID   *string
	IdeaSessionID    string
	IdeaCandidates   []domain.IdeaCandidate
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
	selection, err := taskSelection(task)
	if err != nil {
		return err
	}
	completionPhase, transport, err := normalizeTaskTransport(task.CompletionPhase, task.Transport, true)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO codex_tasks(id,project_id,account_id,type,skill_name,status,codex_session_id,chat_session_id,codex_thread_id,codex_turn_id,completion_phase,transport,prompt_snapshot,model_name,reasoning_effort,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, task.ID, task.ProjectID, task.AccountID, task.Type, task.SkillName, task.Status, task.CodexSessionID, task.ChatSessionID, task.CodexThreadID, task.CodexTurnID, completionPhase, transport, task.PromptSnapshot, selection.Model, selection.ReasoningEffort, task.CreatedAt)
	return err
}

// CreateV2 persists the action required by the manifest/result protocol.
// Create remains solely for reading and migrating pre-protocol task records.
func (r *TaskRepository) CreateV2(ctx context.Context, task domain.CodexTask) error {
	if strings.TrimSpace(string(task.Action)) == "" {
		return fmt.Errorf("task action is required")
	}
	if strings.TrimSpace(task.SkillName) == "" {
		return fmt.Errorf("task skill is required")
	}
	selection, err := taskSelection(task)
	if err != nil {
		return err
	}
	completionPhase, transport, err := normalizeTaskTransport(task.CompletionPhase, task.Transport, true)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO codex_tasks(id,project_id,account_id,type,skill_name,action,status,codex_session_id,chat_session_id,codex_thread_id,codex_turn_id,completion_phase,transport,prompt_snapshot,model_name,reasoning_effort,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, task.ID, task.ProjectID, task.AccountID, task.Type, task.SkillName, task.Action, task.Status, task.CodexSessionID, task.ChatSessionID, task.CodexThreadID, task.CodexTurnID, completionPhase, transport, task.PromptSnapshot, selection.Model, selection.ReasoningEffort, task.CreatedAt)
	return err
}

// EnsurePreparedTask atomically publishes a formal task and its prepared
// manifest identity. A dispatcher can never observe the task without both.
func (r *TaskRepository) EnsurePreparedTask(ctx context.Context, task domain.CodexTask, skillSnapshotID, manifestPath string) (domain.CodexTask, error) {
	if strings.TrimSpace(string(task.Action)) == "" || strings.TrimSpace(task.SkillName) == "" || strings.TrimSpace(skillSnapshotID) == "" || strings.TrimSpace(manifestPath) == "" || task.Status != domain.TaskQueued {
		return domain.CodexTask{}, fmt.Errorf("queued task action, skill, snapshot, and manifest are required")
	}
	selection, err := taskSelection(task)
	if err != nil {
		return domain.CodexTask{}, err
	}
	phase, transport, err := normalizeTaskTransport(task.CompletionPhase, task.Transport, true)
	if err != nil {
		return domain.CodexTask{}, err
	}
	err = r.immediate(ctx, "ensure prepared task", func(q assetDBTX, now time.Time) error {
		var project sql.NullString
		var account, typ, skill string
		var action domain.TaskAction
		var status domain.TaskStatus
		var prompt, model, effort, existingPhase, existingTransport string
		var snapshot, path sql.NullString
		readErr := q.QueryRowContext(ctx, `SELECT project_id,account_id,type,skill_name,action,status,prompt_snapshot,model_name,reasoning_effort,completion_phase,transport,skill_snapshot_id,manifest_path FROM codex_tasks WHERE id=?`, task.ID).Scan(&project, &account, &typ, &skill, &action, &status, &prompt, &model, &effort, &existingPhase, &existingTransport, &snapshot, &path)
		if errors.Is(readErr, sql.ErrNoRows) {
			_, insertErr := q.ExecContext(ctx, `INSERT INTO codex_tasks(id,project_id,account_id,type,skill_name,action,status,codex_session_id,chat_session_id,codex_thread_id,codex_turn_id,completion_phase,transport,prompt_snapshot,model_name,reasoning_effort,skill_snapshot_id,manifest_path,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, task.ID, task.ProjectID, task.AccountID, task.Type, task.SkillName, task.Action, task.Status, task.CodexSessionID, task.ChatSessionID, task.CodexThreadID, task.CodexTurnID, phase, transport, task.PromptSnapshot, selection.Model, selection.ReasoningEffort, skillSnapshotID, manifestPath, task.CreatedAt)
			if insertErr != nil {
				return insertErr
			}
		} else if readErr != nil {
			return readErr
		} else {
			projectMatches := (task.ProjectID == nil && !project.Valid) || (task.ProjectID != nil && project.Valid && *task.ProjectID == project.String)
			identityMatches := projectMatches && account == task.AccountID && typ == task.Type && skill == task.SkillName && action == task.Action && model == selection.Model && effort == selection.ReasoningEffort && existingPhase == phase && existingTransport == transport
			if !identityMatches {
				return fmt.Errorf("prepared task identity conflict")
			}
			if snapshot.Valid || path.Valid {
				if !snapshot.Valid || !path.Valid || snapshot.String != skillSnapshotID || path.String != manifestPath || prompt != task.PromptSnapshot {
					return fmt.Errorf("prepared task manifest conflict")
				}
			} else {
				if status != domain.TaskQueued {
					return fmt.Errorf("cannot prepare task in status %s", status)
				}
				if _, updateErr := q.ExecContext(ctx, `UPDATE codex_tasks SET prompt_snapshot=?,skill_snapshot_id=?,manifest_path=? WHERE id=? AND status=? AND skill_snapshot_id IS NULL AND manifest_path IS NULL`, task.PromptSnapshot, skillSnapshotID, manifestPath, task.ID, domain.TaskQueued); updateErr != nil {
					return updateErr
				}
			}
		}
		if r.beforePreparedCommit != nil {
			return r.beforePreparedCommit()
		}
		return nil
	})
	if err != nil {
		return domain.CodexTask{}, err
	}
	return r.Get(ctx, task.ID)
}

func (r *TaskRepository) PreparedManifest(ctx context.Context, id string) (string, string, error) {
	var snapshot, path string
	err := r.db.QueryRowContext(ctx, `SELECT skill_snapshot_id,manifest_path FROM codex_tasks WHERE id=? AND skill_snapshot_id IS NOT NULL AND manifest_path IS NOT NULL`, id).Scan(&snapshot, &path)
	return snapshot, path, err
}

func taskSelection(task domain.CodexTask) (taskmodel.Selection, error) {
	return taskmodel.Resolve(taskmodel.Selection{Model: taskmodel.DefaultModel, ReasoningEffort: taskmodel.DefaultReasoningEffort}, taskmodel.Selection{Model: task.ModelName, ReasoningEffort: task.ReasoningEffort})
}

func normalizeTaskTransport(completionPhase, transport string, applyDefaults bool) (string, string, error) {
	completionPhase = strings.TrimSpace(completionPhase)
	transport = strings.TrimSpace(transport)
	if applyDefaults {
		if completionPhase == "" {
			completionPhase = "agent_running"
		}
		if transport == "" {
			transport = "legacy_exec"
		}
	}
	if completionPhase == "" || transport == "" {
		return "", "", fmt.Errorf("completion phase and transport are required")
	}
	if transport != "legacy_exec" && transport != "app_server" {
		return "", "", fmt.Errorf("unsupported task transport %q", transport)
	}
	switch completionPhase {
	case "agent_running", "plaintext_ready", "registering", "registered":
	default:
		return "", "", fmt.Errorf("unsupported task completion phase %q", completionPhase)
	}
	return completionPhase, transport, nil
}

// InterruptInFlight marks tasks whose process could not have survived this
// console restart. It deliberately never requeues them: a user must inspect
// and explicitly start a new task rather than accidentally running stale work.
func (r *TaskRepository) InterruptInFlight(ctx context.Context) (int, error) {
	interrupted := 0
	err := r.immediate(ctx, "interrupt in-flight tasks", func(q assetDBTX, now time.Time) error {
		// A durable completion inbox owns replay after restart. Re-open a claim
		// that stopped between ClaimAppServerResult and its transactional result
		// write so the Broker can claim and validate the same turn again.
		if _, err := q.ExecContext(ctx, `UPDATE codex_tasks SET status=?
			WHERE status=? AND transport='app_server' AND codex_turn_id IS NOT NULL
			  AND EXISTS (
				SELECT 1 FROM chat_completion_inbox c
				WHERE c.codex_turn_id=codex_tasks.codex_turn_id
				  AND c.status IN ('pending','processing')
			  )`, domain.TaskRunning, domain.TaskResuming); err != nil {
			return err
		}
		rows, err := q.QueryContext(ctx, `SELECT id FROM codex_tasks
			WHERE status IN (?,?)
			  AND NOT (
				status=? AND transport='app_server' AND codex_turn_id IS NULL
				AND EXISTS (
					SELECT 1 FROM chat_outbox o
					WHERE o.session_id=codex_tasks.chat_session_id
					  AND substr(o.client_key,1,length(? || codex_tasks.id || ':'))=? || codex_tasks.id || ':'
					  AND o.delivery_status IN ('pending','queued')
				)
			  )
			  AND NOT (
				status IN (?,?) AND transport='app_server' AND codex_turn_id IS NOT NULL
				AND EXISTS (
					SELECT 1 FROM chat_completion_inbox c
					WHERE c.codex_turn_id=codex_tasks.codex_turn_id
					  AND c.status IN ('pending','processing')
				)
			  )
			ORDER BY created_at,id`, domain.TaskRunning, domain.TaskResuming, domain.TaskRunning, formalTaskClientKeyPrefix, formalTaskClientKeyPrefix, domain.TaskRunning, domain.TaskResuming)
		if err != nil {
			return err
		}
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, id := range ids {
			updated, err := q.ExecContext(ctx, `UPDATE codex_tasks SET status=?,error_code=?,error_message=?,finished_at=? WHERE id=? AND status IN (?,?)`, domain.TaskInterrupted, "interrupted_on_restart", "Console restarted before this task finished.", now, id, domain.TaskRunning, domain.TaskResuming)
			if err != nil {
				return err
			}
			affected, err := updated.RowsAffected()
			if err != nil {
				return err
			}
			if affected != 1 {
				return fmt.Errorf("task %q changed while recovering", id)
			}
			if err := insertEvent(ctx, q, id, domain.TaskEvent{Kind: "interrupted_on_restart", Level: "warning", DisplayText: "Task interrupted because the console restarted.", CreatedAt: now}); err != nil {
				return err
			}
			interrupted++
		}
		return nil
	})
	return interrupted, err
}
func (r *TaskRepository) Get(ctx context.Context, id string) (domain.CodexTask, error) {
	var t domain.CodexTask
	var action sql.NullString
	err := r.db.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM codex_tasks WHERE id=?`, id).Scan(&t.ID, &t.ProjectID, &t.AccountID, &t.Type, &t.SkillName, &action, &t.Status, &t.CodexSessionID, &t.ChatSessionID, &t.CodexThreadID, &t.CodexTurnID, &t.CompletionPhase, &t.Transport, &t.PromptSnapshot, &t.ModelName, &t.ReasoningEffort, &t.ResultSummary, &t.ErrorCode, &t.ErrorMessage, &t.CreatedAt, &t.StartedAt, &t.FinishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return t, fmt.Errorf("task %q: %w", id, sql.ErrNoRows)
	}
	if action.Valid {
		t.Action = domain.TaskAction(action.String)
	}
	return t, err
}

// GetByCodexTurn resolves the formal App Server task bound to a transport
// turn. The browser never supplies this identifier; it comes only from the
// console-owned Broker's durable turn mapping.
func (r *TaskRepository) GetByCodexTurn(ctx context.Context, turnID string) (domain.CodexTask, error) {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return domain.CodexTask{}, fmt.Errorf("Codex turn id is required")
	}
	var t domain.CodexTask
	var action string
	err := r.db.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM codex_tasks
        WHERE codex_turn_id=? AND transport='app_server'
        ORDER BY created_at DESC,id DESC LIMIT 1`, turnID).Scan(
		&t.ID, &t.ProjectID, &t.AccountID, &t.Type, &t.SkillName, &action, &t.Status,
		&t.CodexSessionID, &t.ChatSessionID, &t.CodexThreadID, &t.CodexTurnID,
		&t.CompletionPhase, &t.Transport, &t.PromptSnapshot, &t.ModelName,
		&t.ReasoningEffort, &t.ResultSummary, &t.ErrorCode, &t.ErrorMessage,
		&t.CreatedAt, &t.StartedAt, &t.FinishedAt)
	if err != nil {
		return domain.CodexTask{}, err
	}
	t.Action = domain.TaskAction(action)
	return t, nil
}

// ClaimAppServerResult atomically grants one completion notification ownership
// of all formal result writes for the currently bound turn.
func (r *TaskRepository) ClaimAppServerResult(ctx context.Context, taskID, turnID string) (bool, error) {
	taskID, turnID = strings.TrimSpace(taskID), strings.TrimSpace(turnID)
	if taskID == "" || turnID == "" {
		return false, fmt.Errorf("task and Codex turn ids are required")
	}
	result, err := r.db.ExecContext(ctx, `UPDATE codex_tasks SET status=?
        WHERE id=? AND transport='app_server' AND codex_turn_id=?
		  AND status=? AND completion_phase=?`, domain.TaskResuming, taskID, turnID, domain.TaskRunning, string(domain.CompletionAgentRunning))
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

// ClaimOutputInvalidRetry reclaims retained output for strict result
// revalidation. App Server tasks keep their bound turn identity; legacy CLI
// tasks have no turn identity. This method never starts or resumes a model.
func (r *TaskRepository) ClaimOutputInvalidRetry(ctx context.Context, taskID string) (*string, error) {
	var transport string
	var nullableTurnID sql.NullString
	err := r.db.QueryRowContext(ctx, `UPDATE codex_tasks
		SET status=CASE WHEN transport='app_server' THEN ? ELSE ? END,
		    result_summary=NULL,error_code=NULL,error_message=NULL,finished_at=NULL
		WHERE id=?
		  AND ((transport='app_server' AND codex_turn_id IS NOT NULL) OR transport='legacy_exec')
		  AND status=? AND completion_phase=? AND error_code='output_invalid'
		RETURNING transport,codex_turn_id`, domain.TaskResuming, domain.TaskRunning, strings.TrimSpace(taskID), domain.TaskFailed, string(domain.CompletionAgentRunning)).Scan(&transport, &nullableTurnID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrOutputRetryNotEligible
	}
	if err != nil {
		return nil, err
	}
	if transport == "legacy_exec" {
		return nil, nil
	}
	turnID := strings.TrimSpace(nullableTurnID.String)
	if transport != "app_server" || !nullableTurnID.Valid || turnID == "" {
		return nil, ErrOutputRetryNotEligible
	}
	return &turnID, nil
}

func (r *TaskRepository) CancelAppServerTurn(ctx context.Context, taskID, turnID string) (bool, error) {
	result, err := r.db.ExecContext(ctx, `UPDATE codex_tasks SET status=?,error_code='cancelled',error_message='task cancelled',finished_at=?
        WHERE id=? AND transport='app_server' AND codex_turn_id=? AND status IN (?,?)`, domain.TaskCanceled, time.Now().UTC(), taskID, turnID, domain.TaskRunning, domain.TaskResuming)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

func (r *TaskRepository) FailAppServerTurn(ctx context.Context, taskID, turnID, code, message string) (bool, error) {
	result, err := r.db.ExecContext(ctx, `UPDATE codex_tasks SET status=?,error_code=?,error_message=?,finished_at=?
        WHERE id=? AND transport='app_server' AND codex_turn_id=? AND status IN (?,?)`, domain.TaskFailed, nullable(code), nullable(message), time.Now().UTC(), taskID, turnID, domain.TaskRunning, domain.TaskResuming)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

// BeginAppServerResume records the answer and claims the awaiting task for a
// new, currently-unbound App Server turn in one transaction.
func (r *TaskRepository) BeginAppServerResume(ctx context.Context, taskID, expectedTurnID, sessionID, clientKey, answer string) error {
	taskID, expectedTurnID, sessionID = strings.TrimSpace(taskID), strings.TrimSpace(expectedTurnID), strings.TrimSpace(sessionID)
	clientKey, answer = strings.TrimSpace(clientKey), strings.TrimSpace(answer)
	if taskID == "" || expectedTurnID == "" || sessionID == "" || clientKey == "" || answer == "" {
		return fmt.Errorf("task, expected turn, session, client key, and answer are required")
	}
	return r.immediate(ctx, "begin App Server task resume", func(q assetDBTX, now time.Time) error {
		if err := insertMessage(ctx, q, domain.TaskMessage{TaskID: taskID, Role: "user", Content: answer, CreatedAt: now}); err != nil {
			return err
		}
		var sequence int64
		if err := q.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM chat_messages WHERE session_id=?`, sessionID).Scan(&sequence); err != nil {
			return err
		}
		messageID, outboxID := uuid.NewString(), uuid.NewString()
		if _, err := q.ExecContext(ctx, `INSERT INTO chat_messages(id,session_id,role,kind,content,delivery_status,client_key,sequence,created_at)
            VALUES(?,?,?,?,?,?,?,?,?)`, messageID, sessionID, "user", "input", answer, domain.ChatOutboxQueued, clientKey, sequence, now); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `INSERT INTO chat_outbox(id,session_id,message_id,client_key,content,delivery_mode,delivery_status,attempts,available_at,created_at,updated_at)
            VALUES(?,?,?,?,?,?,?,?,?,?,?)`, outboxID, sessionID, messageID, clientKey, answer, domain.DeliveryQueue, domain.ChatOutboxQueued, 0, now, now, now); err != nil {
			return err
		}
		updated, err := q.ExecContext(ctx, `UPDATE codex_tasks
            SET status=?,codex_turn_id=NULL,completion_phase=?,result_summary=NULL,error_code=NULL,error_message=NULL,finished_at=NULL
            WHERE id=? AND transport='app_server' AND codex_turn_id=?
              AND status IN (?,?)`, domain.TaskRunning, string(domain.CompletionAgentRunning), taskID, expectedTurnID, domain.TaskAwaitingInput, domain.TaskWaitingInput)
		if err != nil {
			return err
		}
		if affected, err := updated.RowsAffected(); err != nil || affected != 1 {
			if err != nil {
				return err
			}
			return fmt.Errorf("task %q is no longer awaiting the expected turn", taskID)
		}
		return nil
	})
}
func (r *TaskRepository) List(ctx context.Context, projectID string, status domain.TaskStatus) ([]domain.CodexTask, error) {
	q := `SELECT ` + taskColumns + ` FROM codex_tasks WHERE 1=1`
	args := []any{}
	if projectID != "" {
		q += " AND project_id=?"
		args = append(args, projectID)
	}
	if status != "" {
		q += " AND status=?"
		args = append(args, status)
	}
	q += " ORDER BY created_at DESC,id DESC"
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.CodexTask
	for rows.Next() {
		var t domain.CodexTask
		var action sql.NullString
		if err := rows.Scan(&t.ID, &t.ProjectID, &t.AccountID, &t.Type, &t.SkillName, &action, &t.Status, &t.CodexSessionID, &t.ChatSessionID, &t.CodexThreadID, &t.CodexTurnID, &t.CompletionPhase, &t.Transport, &t.PromptSnapshot, &t.ModelName, &t.ReasoningEffort, &t.ResultSummary, &t.ErrorCode, &t.ErrorMessage, &t.CreatedAt, &t.StartedAt, &t.FinishedAt); err != nil {
			return nil, err
		}
		if action.Valid {
			t.Action = domain.TaskAction(action.String)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *TaskRepository) CompletedByProject(ctx context.Context, projectID string) ([]domain.CodexTask, error) {
	return r.List(ctx, projectID, domain.TaskCompleted)
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

func (r *TaskRepository) SetPreparedManifest(ctx context.Context, id, skillSnapshotID, manifestPath string) error {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(skillSnapshotID) == "" || strings.TrimSpace(manifestPath) == "" {
		return fmt.Errorf("task, Skill snapshot, and manifest path are required")
	}
	result, err := r.db.ExecContext(ctx, `UPDATE codex_tasks SET skill_snapshot_id=?,manifest_path=? WHERE id=?`, skillSnapshotID, manifestPath, id)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return err
		}
		return fmt.Errorf("task %q: %w", id, sql.ErrNoRows)
	}
	return nil
}

func (r *TaskRepository) SetTransportMetadata(ctx context.Context, id string, chatSessionID, threadID, turnID *string, completionPhase, transport string) error {
	trimOptional := func(value *string) *string {
		if value == nil {
			return nil
		}
		trimmed := strings.TrimSpace(*value)
		if trimmed == "" {
			return nil
		}
		return &trimmed
	}
	chatSessionID = trimOptional(chatSessionID)
	threadID = trimOptional(threadID)
	turnID = trimOptional(turnID)
	completionPhase, transport, err := normalizeTaskTransport(completionPhase, transport, false)
	if err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `UPDATE codex_tasks SET chat_session_id=?,codex_thread_id=?,codex_turn_id=?,completion_phase=?,transport=? WHERE id=?`, chatSessionID, threadID, turnID, completionPhase, transport, id)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return err
	} else if affected != 1 {
		return fmt.Errorf("task %q: %w", id, sql.ErrNoRows)
	}
	return nil
}
func (r *TaskRepository) UpdateStatus(ctx context.Context, id string, status domain.TaskStatus, summary, code, message string) error {
	now := time.Now().UTC()
	_, err := r.db.ExecContext(ctx, `UPDATE codex_tasks SET status=?,result_summary=?,error_code=?,error_message=?,finished_at=CASE WHEN ? IN ('completed','failed','canceled','cancelled','interrupted') THEN ? ELSE finished_at END,started_at=CASE WHEN ?='running' AND started_at IS NULL THEN ? ELSE started_at END WHERE id=?`, status, nullable(summary), nullable(code), nullable(message), status, now, status, now, id)
	return err
}
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
func (r *TaskRepository) AppendEvent(ctx context.Context, taskID string, event domain.TaskEvent) error {
	return r.immediate(ctx, "append task event", func(q assetDBTX, now time.Time) error {
		if event.CreatedAt.IsZero() {
			event.CreatedAt = now
		}
		return insertEvent(ctx, q, taskID, event)
	})
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
		if err := requireResultClaim(ctx, q, taskID, result.ExpectedTurnID); err != nil {
			return err
		}
		if err := insertMessage(ctx, q, domain.TaskMessage{TaskID: taskID, Role: "assistant", Content: result.AssistantContent, QuestionSchema: result.QuestionSchema, CreatedAt: now}); err != nil {
			return err
		}
		if err := insertEvent(ctx, q, taskID, domain.TaskEvent{Kind: result.EventKind, Level: "info", DisplayText: result.Summary, RawJSON: result.RawJSON, CreatedAt: now}); err != nil {
			return err
		}
		query, args := claimedResultUpdate(`UPDATE codex_tasks SET status=?,result_summary=?,error_code=NULL,error_message=NULL,finished_at=NULL WHERE id=?`, []any{domain.TaskAwaitingInput, nullable(result.Summary), taskID}, result.ExpectedTurnID)
		updated, err := q.ExecContext(ctx, query, args...)
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
		if err := requireResultClaim(ctx, q, taskID, result.ExpectedTurnID); err != nil {
			return err
		}
		var action string
		if err := q.QueryRowContext(ctx, `SELECT COALESCE(action,'') FROM codex_tasks WHERE id=?`, taskID).Scan(&action); err != nil {
			return err
		}
		// A successful montage executor is intentionally non-terminal. Its
		// plaintext workspace still needs trusted host registration before a
		// formal Jianying draft may be recorded.
		montagePlaintext := action == string(domain.ActionMontageExecute) && result.Status == domain.TaskCompleted
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
		if montagePlaintext && len(assets) != 0 {
			return fmt.Errorf("montage.execute cannot create formal assets before host registration")
		}
		if result.IdeaSessionID != "" || len(result.IdeaCandidates) != 0 {
			if err := replaceIdeaCandidates(ctx, q, result.IdeaSessionID, taskID, result.IdeaCandidates, now); err != nil {
				return err
			}
		}
		for _, asset := range assets {
			if asset.SourceTaskID == nil {
				asset.SourceTaskID = &taskID
			}
			version, err := assetsRepo.addVersion(ctx, q, asset, now)
			if err != nil {
				return err
			}
			if version.Type == domain.AssetTopicCard && version.ProjectID != nil {
				if _, err := q.ExecContext(ctx, `UPDATE projects SET topic_card_path=?,updated_at=? WHERE id=?`, version.Path, now, *version.ProjectID); err != nil {
					return err
				}
				if _, err := q.ExecContext(ctx, `UPDATE idea_sessions SET topic_card_path=?,topic_card_state='已写入',topic_card_sha256=?,project_id=?,updated_at=? WHERE project_id=?`, version.Path, version.SHA256, *version.ProjectID, now, *version.ProjectID); err != nil {
					return err
				}
			}
		}
		status, phase, finishedAt := result.Status, "", any(now)
		if montagePlaintext {
			status, phase, finishedAt = domain.TaskRunning, "plaintext_ready", nil
		}
		query, args := claimedResultUpdate(`UPDATE codex_tasks SET status=?,completion_phase=CASE WHEN ?='' THEN completion_phase ELSE ? END,result_summary=?,error_code=?,error_message=?,finished_at=? WHERE id=?`, []any{status, phase, phase, nullable(result.Summary), nullable(result.ErrorCode), nullable(result.ErrorMessage), finishedAt, taskID}, result.ExpectedTurnID)
		updated, err := q.ExecContext(ctx, query, args...)
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

func requireResultClaim(ctx context.Context, q assetDBTX, taskID string, expectedTurnID *string) error {
	if expectedTurnID == nil {
		return nil
	}
	turnID := strings.TrimSpace(*expectedTurnID)
	if turnID == "" {
		return fmt.Errorf("expected claimed turn is required")
	}
	var exists int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM codex_tasks WHERE id=? AND transport='app_server' AND codex_turn_id=? AND status=? AND completion_phase=?`, taskID, turnID, domain.TaskResuming, string(domain.CompletionAgentRunning)).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("task %q no longer owns claimed turn %q", taskID, turnID)
	}
	return err
}

func claimedResultUpdate(query string, args []any, expectedTurnID *string) (string, []any) {
	if expectedTurnID == nil {
		return query, args
	}
	query += ` AND transport='app_server' AND codex_turn_id=? AND status=? AND completion_phase=?`
	return query, append(args, strings.TrimSpace(*expectedTurnID), domain.TaskResuming, string(domain.CompletionAgentRunning))
}

func replaceIdeaCandidates(ctx context.Context, q assetDBTX, sessionID, taskID string, candidates []domain.IdeaCandidate, now time.Time) error {
	if strings.TrimSpace(sessionID) == "" || len(candidates) == 0 {
		return fmt.Errorf("brainstorm result requires session and candidates")
	}
	var exists int
	if err := q.QueryRowContext(ctx, `SELECT 1 FROM idea_sessions WHERE id=? AND EXISTS (SELECT 1 FROM idea_messages WHERE session_id=idea_sessions.id AND task_id=?)`, sessionID, taskID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return ErrIdeaSessionNotFound
	} else if err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `DELETE FROM idea_candidates WHERE session_id=?`, sessionID); err != nil {
		return err
	}
	for i, candidate := range candidates {
		created := candidate.CreatedAt
		if created.IsZero() {
			created = now
		}
		if strings.TrimSpace(candidate.ID) == "" || strings.TrimSpace(candidate.Title) == "" {
			return fmt.Errorf("candidate %d is missing id or title", i)
		}
		if _, err := q.ExecContext(ctx, `INSERT INTO idea_candidates(id,session_id,task_id,position,title,summary,score,source,selected,created_at) VALUES(?,?,?,?,?,?,?,?,0,?)`, candidate.ID, sessionID, taskID, i+1, candidate.Title, candidate.Summary, candidate.Score, candidate.Source, created); err != nil {
			return err
		}
	}
	_, err := q.ExecContext(ctx, `UPDATE idea_sessions SET status='planning',updated_at=? WHERE id=?`, now, sessionID)
	return err
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
