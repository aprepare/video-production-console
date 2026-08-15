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

const taskColumns = `id,project_id,account_id,type,skill_name,action,status,codex_session_id,chat_session_id,codex_thread_id,codex_turn_id,completion_phase,transport,prompt_snapshot,model_name,reasoning_effort,result_summary,error_code,error_message,created_at,queued_at,started_at,finished_at`
const formalTaskClientKeyPrefix = "__formal_task__:"

type TaskRepository struct {
	db                   *sql.DB
	beforePreparedCommit func() error
}

var ErrOutputRetryNotEligible = errors.New("task output is not eligible for completion retry")

type TaskResultWrite struct {
	Status             domain.TaskStatus
	Summary            string
	AssistantContent   string
	QuestionSchema     *string
	EventKind          string
	RawJSON            string
	ErrorCode          string
	ErrorMessage       string
	ExpectedTurnID     *string
	IdeaSessionID      string
	IdeaCandidates     []domain.IdeaCandidate
	ValidationPhaseID  string
	AssetCommitPhaseID string
	SkillTimings       []domain.SkillTimingRun
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

type LegacyStartClaim struct {
	Task        domain.CodexTask
	Attempt     int
	ExecutionID string
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
	if err := validateTaskTimingBoundaries(task); err != nil {
		return err
	}
	selection, err := taskSelection(task)
	if err != nil {
		return err
	}
	completionPhase, transport, err := normalizeTaskTransport(task.CompletionPhase, task.Transport, true)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO codex_tasks(id,project_id,account_id,type,skill_name,status,codex_session_id,chat_session_id,codex_thread_id,codex_turn_id,completion_phase,transport,prompt_snapshot,model_name,reasoning_effort,created_at,queued_at,started_at,finished_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, task.ID, task.ProjectID, task.AccountID, task.Type, task.SkillName, task.Status, task.CodexSessionID, task.ChatSessionID, task.CodexThreadID, task.CodexTurnID, completionPhase, transport, task.PromptSnapshot, selection.Model, selection.ReasoningEffort, task.CreatedAt, task.QueuedAt, task.StartedAt, task.FinishedAt)
	return err
}

// CreateV2 persists the action required by the manifest/result protocol.
// Create remains solely for reading and migrating pre-protocol task records.
func (r *TaskRepository) CreateV2(ctx context.Context, task domain.CodexTask) error {
	if err := validateTaskTimingBoundaries(task); err != nil {
		return err
	}
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
	_, err = r.db.ExecContext(ctx, `INSERT INTO codex_tasks(id,project_id,account_id,type,skill_name,action,status,codex_session_id,chat_session_id,codex_thread_id,codex_turn_id,completion_phase,transport,prompt_snapshot,model_name,reasoning_effort,created_at,queued_at,started_at,finished_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, task.ID, task.ProjectID, task.AccountID, task.Type, task.SkillName, task.Action, task.Status, task.CodexSessionID, task.ChatSessionID, task.CodexThreadID, task.CodexTurnID, completionPhase, transport, task.PromptSnapshot, selection.Model, selection.ReasoningEffort, task.CreatedAt, task.QueuedAt, task.StartedAt, task.FinishedAt)
	return err
}

// EnsurePreparedTask atomically publishes a formal task and its prepared
// manifest identity. A dispatcher can never observe the task without both.
func (r *TaskRepository) EnsurePreparedTask(ctx context.Context, task domain.CodexTask, skillSnapshotID, manifestPath string) (domain.CodexTask, error) {
	return r.EnsurePreparedTaskAt(ctx, task, skillSnapshotID, manifestPath, task.CreatedAt)
}

// EnsurePreparedTaskAt uses the current preparation request boundary rather
// than an existing task's historical creation time when recording preparation.
func (r *TaskRepository) EnsurePreparedTaskAt(ctx context.Context, task domain.CodexTask, skillSnapshotID, manifestPath string, preparationStartedAt time.Time) (domain.CodexTask, error) {
	if strings.TrimSpace(string(task.Action)) == "" || strings.TrimSpace(task.SkillName) == "" || strings.TrimSpace(skillSnapshotID) == "" || strings.TrimSpace(manifestPath) == "" || task.Status != domain.TaskQueued {
		return domain.CodexTask{}, fmt.Errorf("queued task action, skill, snapshot, and manifest are required")
	}
	if preparationStartedAt.IsZero() {
		preparationStartedAt = task.CreatedAt
	}
	if preparationStartedAt.IsZero() {
		return domain.CodexTask{}, fmt.Errorf("task preparation start time is required")
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
			_, insertErr := q.ExecContext(ctx, `INSERT INTO codex_tasks(id,project_id,account_id,type,skill_name,action,status,codex_session_id,chat_session_id,codex_thread_id,codex_turn_id,completion_phase,transport,prompt_snapshot,model_name,reasoning_effort,skill_snapshot_id,manifest_path,created_at,queued_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, task.ID, task.ProjectID, task.AccountID, task.Type, task.SkillName, task.Action, task.Status, task.CodexSessionID, task.ChatSessionID, task.CodexThreadID, task.CodexTurnID, phase, transport, task.PromptSnapshot, selection.Model, selection.ReasoningEffort, skillSnapshotID, manifestPath, task.CreatedAt, task.QueuedAt)
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
			if err := r.beforePreparedCommit(); err != nil {
				return err
			}
		}
		if err := recordTaskPreparationTimingTx(ctx, q, task.ID, preparationStartedAt, now); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return domain.CodexTask{}, err
	}
	return r.Get(ctx, task.ID)
}

// AdmitQueuedTask atomically makes a queued task visible together with its
// queue boundary. Replays are idempotent and preserve the first acceptance.
func (r *TaskRepository) AdmitQueuedTask(ctx context.Context, task domain.CodexTask, at time.Time) (domain.CodexTask, error) {
	if task.ID == "" || task.Status != domain.TaskQueued || at.IsZero() {
		return domain.CodexTask{}, fmt.Errorf("queued task identity and acceptance time are required")
	}
	if err := validateTaskTimingBoundaries(task); err != nil {
		return domain.CodexTask{}, err
	}
	selection, err := taskSelection(task)
	if err != nil {
		return domain.CodexTask{}, err
	}
	phase, transport, err := normalizeTaskTransport(task.CompletionPhase, task.Transport, true)
	if err != nil {
		return domain.CodexTask{}, err
	}
	err = r.immediate(ctx, "admit queued task", func(q assetDBTX, _ time.Time) error {
		var status domain.TaskStatus
		readErr := q.QueryRowContext(ctx, `SELECT status FROM codex_tasks WHERE id=?`, task.ID).Scan(&status)
		if errors.Is(readErr, sql.ErrNoRows) {
			_, readErr = q.ExecContext(ctx, `INSERT INTO codex_tasks(id,project_id,account_id,type,skill_name,action,status,codex_session_id,chat_session_id,codex_thread_id,codex_turn_id,completion_phase,transport,prompt_snapshot,model_name,reasoning_effort,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, task.ID, task.ProjectID, task.AccountID, task.Type, task.SkillName, task.Action, task.Status, task.CodexSessionID, task.ChatSessionID, task.CodexThreadID, task.CodexTurnID, phase, transport, task.PromptSnapshot, selection.Model, selection.ReasoningEffort, task.CreatedAt)
		} else if readErr == nil && status != domain.TaskQueued {
			return fmt.Errorf("task %q is not queued", task.ID)
		}
		if readErr != nil {
			return readErr
		}
		return admitQueueTx(ctx, q, task.ID, at)
	})
	if err != nil {
		return domain.CodexTask{}, err
	}
	return r.Get(ctx, task.ID)
}

func recordTaskPreparationTimingTx(ctx context.Context, q assetDBTX, taskID string, startedAt, at time.Time) error {
	var exists int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_phase_runs WHERE task_id=? AND attempt=1 AND phase_key='task_prepare' AND source=? AND external_id='task_prepare'`, taskID, domain.PhaseSourceHost).Scan(&exists); err != nil {
		return err
	}
	if exists != 0 {
		return nil
	}
	if at.Before(startedAt) {
		return errors.New("task preparation finish precedes start")
	}
	_, err := q.ExecContext(ctx, `INSERT INTO task_phase_runs(id,task_id,attempt,phase_key,display_name,source,state,started_at,running_at,finished_at,duration_ms,external_id,detail_json,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, uuid.NewString(), taskID, 1, "task_prepare", "任务准备", domain.PhaseSourceHost, domain.PhaseCompleted, startedAt, startedAt, at, at.Sub(startedAt).Milliseconds(), "task_prepare", `{}`, startedAt)
	return err
}

func admitQueueTx(ctx context.Context, q assetDBTX, taskID string, at time.Time) error {
	var existing string
	err := q.QueryRowContext(ctx, `SELECT id FROM task_phase_runs WHERE task_id=? AND phase_key='queue_wait' AND state='running' AND finished_at IS NULL ORDER BY attempt DESC LIMIT 1`, taskID).Scan(&existing)
	if err == nil {
		_, err = q.ExecContext(ctx, `UPDATE codex_tasks SET queued_at=COALESCE(queued_at,?) WHERE id=? AND status=?`, at, taskID, domain.TaskQueued)
		return err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var attempt int
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(MAX(CASE WHEN phase_key IN ('queue_wait','codex_execution') THEN attempt ELSE 0 END),0)+1 FROM task_phase_runs WHERE task_id=?`, taskID).Scan(&attempt); err != nil {
		return err
	}
	result, err := q.ExecContext(ctx, `UPDATE codex_tasks SET queued_at=COALESCE(queued_at,?) WHERE id=? AND status=?`, at, taskID, domain.TaskQueued)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return err
		}
		return fmt.Errorf("task %q is not queued", taskID)
	}
	_, err = q.ExecContext(ctx, `INSERT INTO task_phase_runs(id,task_id,attempt,phase_key,display_name,source,state,started_at,running_at,external_id,detail_json,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, uuid.NewString(), taskID, attempt, "queue_wait", "排队等待", domain.PhaseSourceHost, domain.PhaseRunning, at, at, fmt.Sprintf("queue-accept:%d", attempt), `{}`, at)
	return err
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
			if err := finishActiveLifecyclePhasesTx(ctx, q, id, domain.PhaseInterrupted, now); err != nil {
				return err
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
	err := r.db.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM codex_tasks WHERE id=?`, id).Scan(&t.ID, &t.ProjectID, &t.AccountID, &t.Type, &t.SkillName, &action, &t.Status, &t.CodexSessionID, &t.ChatSessionID, &t.CodexThreadID, &t.CodexTurnID, &t.CompletionPhase, &t.Transport, &t.PromptSnapshot, &t.ModelName, &t.ReasoningEffort, &t.ResultSummary, &t.ErrorCode, &t.ErrorMessage, &t.CreatedAt, &t.QueuedAt, &t.StartedAt, &t.FinishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return t, fmt.Errorf("task %q: %w", id, sql.ErrNoRows)
	}
	if action.Valid {
		t.Action = domain.TaskAction(action.String)
	}
	return t, err
}

// ActiveByProjectAction returns the newest task that can still consume or
// produce project inputs for an action. Callers use it to make remix starts
// idempotent without treating terminal history as a conflict.
func (r *TaskRepository) ActiveByProjectAction(ctx context.Context, projectID string, action domain.TaskAction) (domain.CodexTask, error) {
	var task domain.CodexTask
	var storedAction sql.NullString
	err := r.db.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM codex_tasks
		WHERE project_id=? AND action=? AND status IN ('queued','running','awaiting_input')
		ORDER BY created_at DESC,id DESC LIMIT 1`, projectID, action).Scan(
		&task.ID, &task.ProjectID, &task.AccountID, &task.Type, &task.SkillName, &storedAction, &task.Status,
		&task.CodexSessionID, &task.ChatSessionID, &task.CodexThreadID, &task.CodexTurnID,
		&task.CompletionPhase, &task.Transport, &task.PromptSnapshot, &task.ModelName,
		&task.ReasoningEffort, &task.ResultSummary, &task.ErrorCode, &task.ErrorMessage,
		&task.CreatedAt, &task.QueuedAt, &task.StartedAt, &task.FinishedAt,
	)
	if err != nil {
		return domain.CodexTask{}, err
	}
	if storedAction.Valid {
		task.Action = domain.TaskAction(storedAction.String)
	}
	return task, nil
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
		&t.CreatedAt, &t.QueuedAt, &t.StartedAt, &t.FinishedAt)
	if err != nil {
		return domain.CodexTask{}, err
	}
	t.Action = domain.TaskAction(action)
	return t, nil
}

// ClaimAppServerResult atomically grants one completion notification ownership
// of all formal result writes for the currently bound turn.
func (r *TaskRepository) ClaimAppServerResult(ctx context.Context, taskID, turnID string) (bool, error) {
	_, claimed, err := r.ClaimAppServerResultValidation(ctx, taskID, turnID, time.Now().UTC())
	return claimed, err
}

// ClaimAppServerResultValidation closes the observable Codex execution and
// starts result validation in the same transaction that claims completion.
func (r *TaskRepository) ClaimAppServerResultValidation(ctx context.Context, taskID, turnID string, at time.Time) (string, bool, error) {
	taskID, turnID = strings.TrimSpace(taskID), strings.TrimSpace(turnID)
	if taskID == "" || turnID == "" || at.IsZero() {
		return "", false, fmt.Errorf("task, Codex turn id, and validation time are required")
	}
	validationID := ""
	claimed := false
	err := r.immediate(ctx, "claim App Server task result", func(q assetDBTX, _ time.Time) error {
		var status domain.TaskStatus
		err := q.QueryRowContext(ctx, `SELECT status FROM codex_tasks
			WHERE id=? AND transport='app_server' AND codex_turn_id=? AND completion_phase=?`, taskID, turnID, string(domain.CompletionAgentRunning)).Scan(&status)
		if errors.Is(err, sql.ErrNoRows) || status != domain.TaskRunning {
			return nil
		}
		if err != nil {
			return err
		}
		var startErr error
		validationID, _, startErr = beginResultValidationTx(ctx, q, taskID, domain.PhaseSourceHost, at)
		if startErr != nil {
			return startErr
		}
		result, err := q.ExecContext(ctx, `UPDATE codex_tasks SET status=?
			WHERE id=? AND transport='app_server' AND codex_turn_id=?
			  AND status=? AND completion_phase=?`, domain.TaskResuming, taskID, turnID, domain.TaskRunning, string(domain.CompletionAgentRunning))
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return errors.New("task changed before result validation claim")
		}
		claimed = true
		return nil
	})
	if err != nil {
		return "", false, err
	}
	return validationID, claimed, nil
}

// BeginResultValidation closes the active execution phase and starts a new
// host validation attempt without changing task status.
func (r *TaskRepository) BeginResultValidation(ctx context.Context, taskID string, source domain.TaskPhaseSource, at time.Time) (string, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" || at.IsZero() {
		return "", fmt.Errorf("task and validation time are required")
	}
	if source != domain.PhaseSourceHost && source != domain.PhaseSourceAppServer {
		return "", fmt.Errorf("invalid result validation source %q", source)
	}
	phaseID := ""
	err := r.immediate(ctx, "begin task result validation", func(q assetDBTX, _ time.Time) error {
		var err error
		phaseID, _, err = beginResultValidationTx(ctx, q, taskID, source, at)
		return err
	})
	return phaseID, err
}

func beginResultValidationTx(ctx context.Context, q assetDBTX, taskID string, source domain.TaskPhaseSource, at time.Time) (string, int, error) {
	attempt := 0
	var executionID string
	var executionStarted time.Time
	err := q.QueryRowContext(ctx, `SELECT id,attempt,started_at FROM task_phase_runs
		WHERE task_id=? AND phase_key='codex_execution' AND state='running' AND finished_at IS NULL
		ORDER BY attempt DESC,started_at DESC,id DESC LIMIT 1`, taskID).Scan(&executionID, &attempt, &executionStarted)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", 0, err
	}
	if err == nil {
		if at.Before(executionStarted) {
			return "", 0, errors.New("result validation precedes Codex execution")
		}
		result, updateErr := q.ExecContext(ctx, `UPDATE task_phase_runs SET state=?,finished_at=?,duration_ms=?
			WHERE id=? AND state='running' AND finished_at IS NULL`, domain.PhaseCompleted, at, at.Sub(executionStarted).Milliseconds(), executionID)
		if updateErr != nil {
			return "", 0, updateErr
		}
		if affected, updateErr := result.RowsAffected(); updateErr != nil || affected != 1 {
			if updateErr != nil {
				return "", 0, updateErr
			}
			return "", 0, errors.New("Codex execution changed before result validation")
		}
	} else if err := q.QueryRowContext(ctx, `SELECT COALESCE(MAX(attempt),0)+1 FROM task_phase_runs WHERE task_id=? AND phase_key='result_validation'`, taskID).Scan(&attempt); err != nil {
		return "", 0, err
	}
	if attempt <= 0 {
		attempt = 1
	}
	phaseID := uuid.NewString()
	_, err = q.ExecContext(ctx, `INSERT INTO task_phase_runs(id,task_id,attempt,phase_key,display_name,source,state,started_at,running_at,external_id,detail_json,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, phaseID, taskID, attempt, "result_validation", "结果校验", source, domain.PhaseRunning, at, at, fmt.Sprintf("result-validation:%d", attempt), `{}`, at)
	return phaseID, attempt, err
}

// BeginAssetCommit closes one validated result attempt and starts the durable
// task/artifact/asset transaction timer in the same transaction.
func (r *TaskRepository) BeginAssetCommit(ctx context.Context, taskID, validationPhaseID string, at time.Time) (string, error) {
	taskID, validationPhaseID = strings.TrimSpace(taskID), strings.TrimSpace(validationPhaseID)
	if taskID == "" || validationPhaseID == "" || at.IsZero() {
		return "", fmt.Errorf("task, validation phase, and asset commit time are required")
	}
	assetCommitID := ""
	err := r.immediate(ctx, "begin task asset commit", func(q assetDBTX, _ time.Time) error {
		var attempt int
		if _, err := finishPhaseByIDTx(ctx, q, taskID, validationPhaseID, "result_validation", domain.PhaseCompleted, at, &attempt); err != nil {
			return err
		}
		err := q.QueryRowContext(ctx, `SELECT id FROM task_phase_runs WHERE task_id=? AND attempt=? AND phase_key='asset_commit' ORDER BY created_at DESC,id DESC LIMIT 1`, taskID, attempt).Scan(&assetCommitID)
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		assetCommitID = uuid.NewString()
		_, err = q.ExecContext(ctx, `INSERT INTO task_phase_runs(id,task_id,attempt,phase_key,display_name,source,state,started_at,running_at,external_id,detail_json,created_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, assetCommitID, taskID, attempt, "asset_commit", "资产入库", domain.PhaseSourceHost, domain.PhaseRunning, at, at, fmt.Sprintf("asset-commit:%d", attempt), `{}`, at)
		return err
	})
	return assetCommitID, err
}

// ClaimOutputInvalidRetry reclaims retained output for strict result
// revalidation. App Server tasks keep their bound turn identity; legacy CLI
// tasks have no turn identity. This method never starts or resumes a model.
func (r *TaskRepository) ClaimOutputInvalidRetry(ctx context.Context, taskID string) (*string, error) {
	turnID, _, err := r.ClaimOutputInvalidRetryValidation(ctx, taskID, time.Now().UTC())
	return turnID, err
}

// ClaimOutputInvalidRetryValidation atomically reopens the task and creates a
// fresh validation attempt for the retained result without a Codex phase.
func (r *TaskRepository) ClaimOutputInvalidRetryValidation(ctx context.Context, taskID string, at time.Time) (*string, string, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" || at.IsZero() {
		return nil, "", ErrOutputRetryNotEligible
	}
	var turnID *string
	validationID := ""
	err := r.immediate(ctx, "claim output-invalid retry", func(q assetDBTX, _ time.Time) error {
		var transport string
		var nullableTurnID sql.NullString
		if err := q.QueryRowContext(ctx, `SELECT transport,codex_turn_id FROM codex_tasks
			WHERE id=? AND ((transport='app_server' AND codex_turn_id IS NOT NULL) OR transport='legacy_exec')
			  AND status=? AND completion_phase=? AND error_code='output_invalid'`, taskID, domain.TaskFailed, string(domain.CompletionAgentRunning)).Scan(&transport, &nullableTurnID); errors.Is(err, sql.ErrNoRows) {
			return ErrOutputRetryNotEligible
		} else if err != nil {
			return err
		}
		status := domain.TaskRunning
		if transport == "app_server" {
			status = domain.TaskResuming
			value := strings.TrimSpace(nullableTurnID.String)
			if !nullableTurnID.Valid || value == "" {
				return ErrOutputRetryNotEligible
			}
			turnID = &value
		} else if transport != "legacy_exec" {
			return ErrOutputRetryNotEligible
		}
		var err error
		validationID, _, err = beginResultValidationTx(ctx, q, taskID, domain.PhaseSourceHost, at)
		if err != nil {
			return err
		}
		result, err := q.ExecContext(ctx, `UPDATE codex_tasks SET status=?,result_summary=NULL,error_code=NULL,error_message=NULL,finished_at=NULL WHERE id=? AND status=? AND completion_phase=? AND error_code='output_invalid'`, status, taskID, domain.TaskFailed, string(domain.CompletionAgentRunning))
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			if err != nil {
				return err
			}
			return ErrOutputRetryNotEligible
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	return turnID, validationID, nil
}

func (r *TaskRepository) CancelAppServerTurn(ctx context.Context, taskID, turnID string) (bool, error) {
	taskID, turnID = strings.TrimSpace(taskID), strings.TrimSpace(turnID)
	if taskID == "" || turnID == "" {
		return false, fmt.Errorf("task and Codex turn ids are required")
	}
	changed := false
	err := r.immediate(ctx, "cancel App Server task turn", func(q assetDBTX, now time.Time) error {
		var status domain.TaskStatus
		err := q.QueryRowContext(ctx, `SELECT status FROM codex_tasks
			WHERE id=? AND transport='app_server' AND codex_turn_id=? AND status IN (?,?)`, taskID, turnID, domain.TaskRunning, domain.TaskResuming).Scan(&status)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := finishActiveLifecyclePhasesTx(ctx, q, taskID, domain.PhaseCanceled, now); err != nil {
			return err
		}
		result, err := q.ExecContext(ctx, `UPDATE codex_tasks SET status=?,error_code='canceled',error_message='task canceled',finished_at=?
			WHERE id=? AND transport='app_server' AND codex_turn_id=? AND status=?`, domain.TaskCanceled, now, taskID, turnID, status)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		changed = affected == 1
		return err
	})
	return changed, err
}

func (r *TaskRepository) FailAppServerTurn(ctx context.Context, taskID, turnID, code, message string) (bool, error) {
	taskID, turnID = strings.TrimSpace(taskID), strings.TrimSpace(turnID)
	if taskID == "" || turnID == "" {
		return false, fmt.Errorf("task and Codex turn ids are required")
	}
	changed := false
	err := r.immediate(ctx, "fail App Server task turn", func(q assetDBTX, now time.Time) error {
		var status domain.TaskStatus
		err := q.QueryRowContext(ctx, `SELECT status FROM codex_tasks
			WHERE id=? AND transport='app_server' AND codex_turn_id=? AND status IN (?,?)`, taskID, turnID, domain.TaskRunning, domain.TaskResuming).Scan(&status)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := finishActiveLifecyclePhasesTx(ctx, q, taskID, domain.PhaseFailed, now); err != nil {
			return err
		}
		result, err := q.ExecContext(ctx, `UPDATE codex_tasks SET status=?,error_code=?,error_message=?,finished_at=?
			WHERE id=? AND transport='app_server' AND codex_turn_id=? AND status=?`, domain.TaskFailed, nullable(code), nullable(message), now, taskID, turnID, status)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		changed = affected == 1
		return err
	})
	return changed, err
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
			  AND status IN (?,?)`, domain.TaskResuming, string(domain.CompletionAgentRunning), taskID, expectedTurnID, domain.TaskAwaitingInput, domain.TaskWaitingInput)
		if err != nil {
			return err
		}
		if affected, err := updated.RowsAffected(); err != nil || affected != 1 {
			if err != nil {
				return err
			}
			return fmt.Errorf("task %q is no longer awaiting the expected turn", taskID)
		}
		var attempt int
		if err := q.QueryRowContext(ctx, `SELECT COALESCE(MAX(attempt),0)+1 FROM task_phase_runs WHERE task_id=?`, taskID).Scan(&attempt); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `INSERT INTO task_phase_runs(id,task_id,attempt,phase_key,display_name,source,state,started_at,running_at,external_id,detail_json,created_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, uuid.NewString(), taskID, attempt, "queue_wait", "排队等待", domain.PhaseSourceHost, domain.PhaseRunning, now, now, "app-server-resume-accept", `{}`, now); err != nil {
			return err
		}
		return nil
	})
}

// StartAppServerTurn atomically binds a durable turn notification, closes the
// matching queue wait, starts execution, and transitions the task to running.
// Replaying the same notification after commit is a successful no-op.
func (r *TaskRepository) StartAppServerTurn(ctx context.Context, taskID, sessionID, threadID, turnID string, at time.Time) error {
	taskID, sessionID = strings.TrimSpace(taskID), strings.TrimSpace(sessionID)
	threadID, turnID = strings.TrimSpace(threadID), strings.TrimSpace(turnID)
	if taskID == "" || sessionID == "" || threadID == "" || turnID == "" || at.IsZero() {
		return fmt.Errorf("task, session, thread, turn, and start time are required")
	}
	return r.immediate(ctx, "start App Server task turn", func(q assetDBTX, _ time.Time) error {
		var status domain.TaskStatus
		var persistedSession, persistedThread, persistedTurn sql.NullString
		var persistedStarted sql.NullTime
		if err := q.QueryRowContext(ctx, `SELECT status,chat_session_id,codex_thread_id,codex_turn_id,started_at FROM codex_tasks WHERE id=? AND transport='app_server'`, taskID).
			Scan(&status, &persistedSession, &persistedThread, &persistedTurn, &persistedStarted); err != nil {
			return err
		}
		if !persistedSession.Valid || persistedSession.String != sessionID {
			return errors.New("formal task start identity mismatch")
		}
		if persistedStarted.Valid && persistedThread.Valid && persistedThread.String == threadID && persistedTurn.Valid && persistedTurn.String == turnID {
			return nil
		}
		if status != domain.TaskQueued && status != domain.TaskResuming {
			return fmt.Errorf("formal task cannot bind a turn in status %s", status)
		}
		var queueID string
		var attempt int
		var queueStarted time.Time
		queueErr := q.QueryRowContext(ctx, `SELECT id,attempt,started_at FROM task_phase_runs
			WHERE task_id=? AND phase_key='queue_wait' AND state='running' AND finished_at IS NULL
			ORDER BY attempt DESC,started_at DESC,id DESC LIMIT 1`, taskID).Scan(&queueID, &attempt, &queueStarted)
		if queueErr != nil && !errors.Is(queueErr, sql.ErrNoRows) {
			return fmt.Errorf("active queue wait for task %q: %w", taskID, queueErr)
		}
		if queueErr == nil {
			if at.Before(queueStarted) {
				return errors.New("turn start precedes queue acceptance")
			}
			result, err := q.ExecContext(ctx, `UPDATE task_phase_runs SET state=?,finished_at=?,duration_ms=? WHERE id=? AND state='running' AND finished_at IS NULL`, domain.PhaseCompleted, at, at.Sub(queueStarted).Milliseconds(), queueID)
			if err != nil {
				return err
			}
			if affected, err := result.RowsAffected(); err != nil || affected != 1 {
				if err != nil {
					return err
				}
				return errors.New("queue wait changed before turn start")
			}
		} else if err := q.QueryRowContext(ctx, `SELECT COALESCE(MAX(attempt),0)+1 FROM task_phase_runs WHERE task_id=?`, taskID).Scan(&attempt); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `INSERT INTO task_phase_runs(id,task_id,attempt,phase_key,display_name,source,state,started_at,running_at,external_id,detail_json,created_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, uuid.NewString(), taskID, attempt, "codex_execution", "模型执行", domain.PhaseSourceAppServer, domain.PhaseRunning, at, at, "app-server-execution", `{}`, at); err != nil {
			return err
		}
		updated, err := q.ExecContext(ctx, `UPDATE codex_tasks SET status=?,codex_thread_id=?,codex_turn_id=?,completion_phase=?,started_at=COALESCE(started_at,?)
			WHERE id=? AND status IN (?,?)`, domain.TaskRunning, threadID, turnID, string(domain.CompletionAgentRunning), at, taskID, domain.TaskQueued, domain.TaskResuming)
		if err != nil {
			return err
		}
		if affected, err := updated.RowsAffected(); err != nil || affected != 1 {
			if err != nil {
				return err
			}
			return errors.New("task changed before turn start")
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
		if err := rows.Scan(&t.ID, &t.ProjectID, &t.AccountID, &t.Type, &t.SkillName, &action, &t.Status, &t.CodexSessionID, &t.ChatSessionID, &t.CodexThreadID, &t.CodexTurnID, &t.CompletionPhase, &t.Transport, &t.PromptSnapshot, &t.ModelName, &t.ReasoningEffort, &t.ResultSummary, &t.ErrorCode, &t.ErrorMessage, &t.CreatedAt, &t.QueuedAt, &t.StartedAt, &t.FinishedAt); err != nil {
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
	_, err := r.db.ExecContext(ctx, `UPDATE codex_tasks SET status=?,result_summary=?,error_code=?,error_message=?,finished_at=CASE WHEN ? IN ('completed','failed','canceled','cancelled','interrupted') THEN COALESCE(finished_at,?) ELSE finished_at END,started_at=CASE WHEN ?='running' AND started_at IS NULL THEN ? ELSE started_at END WHERE id=?`, status, nullable(summary), nullable(code), nullable(message), status, now, status, now, id)
	return err
}

func (r *TaskRepository) MarkQueued(ctx context.Context, id string, at time.Time) error {
	if strings.TrimSpace(id) == "" || at.IsZero() {
		return fmt.Errorf("task and queue time are required")
	}
	result, err := r.db.ExecContext(ctx, `UPDATE codex_tasks SET queued_at=COALESCE(queued_at,?) WHERE id=? AND (queued_at IS NOT NULL OR (?>=created_at AND (started_at IS NULL OR ?<=started_at) AND (started_at IS NOT NULL OR finished_at IS NULL OR ?<=finished_at)))`, at, id, at, at, at)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return err
	} else if affected != 1 {
		var exists int
		if readErr := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM codex_tasks WHERE id=?`, id).Scan(&exists); readErr != nil {
			return readErr
		}
		if exists == 0 {
			return fmt.Errorf("task %q: %w", id, sql.ErrNoRows)
		}
		return fmt.Errorf("queue time is outside task timing boundaries")
	}
	return nil
}

func (r *TaskRepository) MarkRunning(ctx context.Context, id string, at time.Time) error {
	if strings.TrimSpace(id) == "" || at.IsZero() {
		return fmt.Errorf("task and running time are required")
	}
	result, err := r.db.ExecContext(ctx, `UPDATE codex_tasks SET status=?,started_at=COALESCE(started_at,?) WHERE id=? AND status=?`, domain.TaskRunning, at, id, domain.TaskQueued)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return err
	} else if affected != 1 {
		return fmt.Errorf("task %q is not queued", id)
	}
	return nil
}

func (r *TaskRepository) ClaimLegacyStart(ctx context.Context, id string, at time.Time) (LegacyStartClaim, error) {
	var claim LegacyStartClaim
	if strings.TrimSpace(id) == "" || at.IsZero() {
		return claim, fmt.Errorf("task and start time are required")
	}
	err := r.immediate(ctx, "claim legacy task start", func(q assetDBTX, _ time.Time) error {
		var transport string
		if err := q.QueryRowContext(ctx, `SELECT transport FROM codex_tasks WHERE id=? AND status=?`, id, domain.TaskQueued).Scan(&transport); err != nil {
			return fmt.Errorf("task %q is not queued: %w", id, err)
		}
		if transport != "legacy_exec" {
			return fmt.Errorf("task %q is not legacy exec", id)
		}
		var queueID string
		var queueStarted time.Time
		queueErr := q.QueryRowContext(ctx, `SELECT id,attempt,started_at FROM task_phase_runs WHERE task_id=? AND phase_key='queue_wait' AND state='running' AND finished_at IS NULL ORDER BY attempt DESC,started_at DESC,id DESC LIMIT 1`, id).Scan(&queueID, &claim.Attempt, &queueStarted)
		if errors.Is(queueErr, sql.ErrNoRows) {
			if err := q.QueryRowContext(ctx, `SELECT COALESCE(MAX(attempt),0)+1 FROM task_phase_runs WHERE task_id=?`, id).Scan(&claim.Attempt); err != nil {
				return err
			}
		} else if queueErr != nil {
			return queueErr
		} else {
			if at.Before(queueStarted) {
				return errors.New("task start precedes queue acceptance")
			}
			if _, err := q.ExecContext(ctx, `UPDATE task_phase_runs SET state=?,finished_at=?,duration_ms=? WHERE id=? AND state='running' AND finished_at IS NULL`, domain.PhaseCompleted, at, at.Sub(queueStarted).Milliseconds(), queueID); err != nil {
				return err
			}
		}
		claim.ExecutionID = uuid.NewString()
		if _, err := q.ExecContext(ctx, `INSERT INTO task_phase_runs(id,task_id,attempt,phase_key,display_name,source,state,started_at,running_at,external_id,detail_json,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, claim.ExecutionID, id, claim.Attempt, "codex_execution", "模型执行", domain.PhaseSourceHost, domain.PhaseRunning, at, at, fmt.Sprintf("legacy-execution:%d", claim.Attempt), `{}`, at); err != nil {
			return err
		}
		result, err := q.ExecContext(ctx, `UPDATE codex_tasks SET status=?,started_at=COALESCE(started_at,?),finished_at=NULL,error_code=NULL,error_message=NULL WHERE id=? AND status=?`, domain.TaskRunning, at, id, domain.TaskQueued)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			if err != nil {
				return err
			}
			return fmt.Errorf("task %q changed before start", id)
		}
		return nil
	})
	if err != nil {
		return LegacyStartClaim{}, err
	}
	claim.Task, err = r.Get(ctx, id)
	return claim, err
}

func (r *TaskRepository) BeginLegacyResume(ctx context.Context, id, answer string, at time.Time) error {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(answer) == "" || at.IsZero() {
		return errors.New("task, answer, and resume time are required")
	}
	return r.immediate(ctx, "begin legacy task resume", func(q assetDBTX, _ time.Time) error {
		var session sql.NullString
		if err := q.QueryRowContext(ctx, `SELECT codex_session_id FROM codex_tasks WHERE id=? AND transport='legacy_exec' AND status IN (?,?)`, id, domain.TaskAwaitingInput, domain.TaskWaitingInput).Scan(&session); err != nil {
			return err
		}
		if !session.Valid || strings.TrimSpace(session.String) == "" {
			return errors.New("task has no codex session")
		}
		if err := insertMessage(ctx, q, domain.TaskMessage{TaskID: id, Role: "user", Content: answer, CreatedAt: at}); err != nil {
			return err
		}
		result, err := q.ExecContext(ctx, `UPDATE codex_tasks SET status=?,result_summary=NULL,error_code=NULL,error_message=NULL,finished_at=NULL WHERE id=? AND status IN (?,?)`, domain.TaskQueued, id, domain.TaskAwaitingInput, domain.TaskWaitingInput)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			if err != nil {
				return err
			}
			return errors.New("task is no longer awaiting input")
		}
		return admitQueueTx(ctx, q, id, at)
	})
}

func finishPhaseByIDTx(ctx context.Context, q assetDBTX, taskID, phaseID, key string, state domain.TaskPhaseState, at time.Time, attempt *int) (bool, error) {
	var phaseAttempt int
	var started time.Time
	var persistedState domain.TaskPhaseState
	var finished sql.NullTime
	err := q.QueryRowContext(ctx, `SELECT attempt,started_at,state,finished_at FROM task_phase_runs WHERE id=? AND task_id=? AND phase_key=?`, phaseID, taskID, key).Scan(&phaseAttempt, &started, &persistedState, &finished)
	if err != nil {
		return false, err
	}
	if attempt != nil {
		*attempt = phaseAttempt
	}
	if finished.Valid {
		if persistedState != state {
			return false, fmt.Errorf("phase %q already finished as %s", phaseID, persistedState)
		}
		return false, nil
	}
	if persistedState != domain.PhaseQueued && persistedState != domain.PhaseRunning {
		return false, fmt.Errorf("phase %q is not active", phaseID)
	}
	if at.Before(started) {
		return false, errors.New("phase finish precedes start")
	}
	result, err := q.ExecContext(ctx, `UPDATE task_phase_runs SET state=?,finished_at=?,duration_ms=? WHERE id=? AND finished_at IS NULL AND state IN ('queued','running')`, state, at, at.Sub(started).Milliseconds(), phaseID)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected != 1 {
		return false, fmt.Errorf("phase %q changed before finish", phaseID)
	}
	return true, nil
}

func finishResultWritePhasesTx(ctx context.Context, q assetDBTX, taskID string, result TaskResultWrite, at time.Time) error {
	validationID := strings.TrimSpace(result.ValidationPhaseID)
	assetCommitID := strings.TrimSpace(result.AssetCommitPhaseID)
	if validationID == "" && assetCommitID == "" {
		return nil
	}
	if validationID == "" {
		return errors.New("asset commit timing requires its validation phase")
	}
	if assetCommitID == "" {
		if result.Status != domain.TaskFailed {
			return errors.New("validated successful result persistence requires an asset commit phase")
		}
		_, err := finishPhaseByIDTx(ctx, q, taskID, validationID, "result_validation", domain.PhaseFailed, at, nil)
		return err
	}
	var validationAttempt int
	var validationState domain.TaskPhaseState
	var validationFinished sql.NullTime
	if err := q.QueryRowContext(ctx, `SELECT attempt,state,finished_at FROM task_phase_runs WHERE id=? AND task_id=? AND phase_key='result_validation'`, validationID, taskID).Scan(&validationAttempt, &validationState, &validationFinished); err != nil {
		return err
	}
	if validationState != domain.PhaseCompleted || !validationFinished.Valid {
		return errors.New("asset commit timing requires completed result validation")
	}
	var assetAttempt int
	if _, err := finishPhaseByIDTx(ctx, q, taskID, assetCommitID, "asset_commit", domain.PhaseCompleted, at, &assetAttempt); err != nil {
		return err
	}
	if assetAttempt != validationAttempt {
		return errors.New("asset commit timing attempt does not match result validation")
	}
	return nil
}

func finishActivePhaseTx(ctx context.Context, q assetDBTX, taskID, key string, state domain.TaskPhaseState, at time.Time) (bool, error) {
	var id string
	var started time.Time
	err := q.QueryRowContext(ctx, `SELECT id,started_at FROM task_phase_runs WHERE task_id=? AND phase_key=? AND state IN ('queued','running') AND finished_at IS NULL ORDER BY attempt DESC,started_at DESC,id DESC LIMIT 1`, taskID, key).Scan(&id, &started)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if at.Before(started) {
		return false, errors.New("phase finish precedes start")
	}
	result, err := q.ExecContext(ctx, `UPDATE task_phase_runs SET state=?,finished_at=?,duration_ms=? WHERE id=? AND state IN ('queued','running') AND finished_at IS NULL`, state, at, at.Sub(started).Milliseconds(), id)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

func finishActiveLifecyclePhasesTx(ctx context.Context, q assetDBTX, taskID string, state domain.TaskPhaseState, at time.Time) error {
	for _, key := range []string{"queue_wait", "codex_execution", "result_validation", "asset_commit", "jianying_registration"} {
		if _, err := finishActivePhaseTx(ctx, q, taskID, key, state, at); err != nil {
			return err
		}
	}
	return nil
}

func (r *TaskRepository) FailTask(ctx context.Context, id, code, message string, at time.Time) error {
	return r.immediate(ctx, "fail task", func(q assetDBTX, _ time.Time) error {
		if err := finishActiveLifecyclePhasesTx(ctx, q, id, domain.PhaseFailed, at); err != nil {
			return err
		}
		result, err := q.ExecContext(ctx, `UPDATE codex_tasks SET status=?,error_code=?,error_message=?,finished_at=COALESCE(finished_at,?) WHERE id=? AND status NOT IN (?,?,?,?,?)`, domain.TaskFailed, nullable(code), nullable(message), at, id, domain.TaskCompleted, domain.TaskFailed, domain.TaskCanceled, domain.TaskCancelled, domain.TaskInterrupted)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return fmt.Errorf("task %q is terminal", id)
		}
		return nil
	})
}

func (r *TaskRepository) FailQueuedTask(ctx context.Context, id, code, message string, at time.Time) (bool, error) {
	changed := false
	err := r.immediate(ctx, "fail queued task", func(q assetDBTX, _ time.Time) error {
		var status domain.TaskStatus
		if err := q.QueryRowContext(ctx, `SELECT status FROM codex_tasks WHERE id=?`, id).Scan(&status); err != nil {
			return err
		}
		if status != domain.TaskQueued {
			return nil
		}
		if _, err := finishActivePhaseTx(ctx, q, id, "queue_wait", domain.PhaseFailed, at); err != nil {
			return err
		}
		result, err := q.ExecContext(ctx, `UPDATE codex_tasks SET status=?,error_code=?,error_message=?,finished_at=? WHERE id=? AND status=?`, domain.TaskFailed, nullable(code), nullable(message), at, id, domain.TaskQueued)
		if err != nil {
			return err
		}
		n, e := result.RowsAffected()
		changed = n == 1
		return e
	})
	return changed, err
}

func (r *TaskRepository) CancelTask(ctx context.Context, id string, at time.Time) (bool, error) {
	changed := false
	err := r.immediate(ctx, "cancel task", func(q assetDBTX, _ time.Time) error {
		var status domain.TaskStatus
		if err := q.QueryRowContext(ctx, `SELECT status FROM codex_tasks WHERE id=?`, id).Scan(&status); err != nil {
			return err
		}
		if status == domain.TaskCompleted || status == domain.TaskFailed || status == domain.TaskCanceled || status == domain.TaskCancelled || status == domain.TaskInterrupted {
			return nil
		}
		if err := finishActiveLifecyclePhasesTx(ctx, q, id, domain.PhaseCanceled, at); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `UPDATE montage_registration_attempts SET state=?,error_code='registration_canceled',error_message='registration canceled with task',finished_at=? WHERE task_id=? AND state IN ('queued','running')`, domain.RegistrationInterrupted, at, id); err != nil {
			return err
		}
		result, err := q.ExecContext(ctx, `UPDATE codex_tasks SET status=?,error_code='canceled',error_message='task canceled',finished_at=? WHERE id=? AND status=?`, domain.TaskCanceled, at, id, status)
		if err != nil {
			return err
		}
		n, e := result.RowsAffected()
		changed = n == 1
		return e
	})
	return changed, err
}

func validateTaskTimingBoundaries(task domain.CodexTask) error {
	if task.QueuedAt != nil && task.QueuedAt.Before(task.CreatedAt) {
		return fmt.Errorf("queue time precedes task creation")
	}
	if task.StartedAt != nil {
		if task.StartedAt.Before(task.CreatedAt) {
			return fmt.Errorf("task start precedes creation")
		}
		if task.QueuedAt != nil && task.QueuedAt.After(*task.StartedAt) {
			return fmt.Errorf("queue time follows task start")
		}
	} else if task.QueuedAt != nil && task.FinishedAt != nil && task.QueuedAt.After(*task.FinishedAt) {
		return fmt.Errorf("queue time follows task finish")
	}
	if task.FinishedAt != nil && (task.FinishedAt.Before(task.CreatedAt) || task.StartedAt != nil && task.FinishedAt.Before(*task.StartedAt)) {
		return fmt.Errorf("task finish precedes an earlier boundary")
	}
	return nil
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
		if result.ValidationPhaseID == "" && result.AssetCommitPhaseID == "" {
			if _, err := finishActivePhaseTx(ctx, q, taskID, "codex_execution", domain.PhaseCompleted, now); err != nil {
				return err
			}
		} else if err := finishResultWritePhasesTx(ctx, q, taskID, result, now); err != nil {
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
		if err := importSkillTimingsTx(ctx, q, taskID, result.SkillTimings, now); err != nil {
			return err
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
		executionState := domain.PhaseCompleted
		if result.Status == domain.TaskFailed {
			executionState = domain.PhaseFailed
		}
		if result.ValidationPhaseID == "" && result.AssetCommitPhaseID == "" {
			if _, err := finishActivePhaseTx(ctx, q, taskID, "codex_execution", executionState, now); err != nil {
				return err
			}
		} else if err := finishResultWritePhasesTx(ctx, q, taskID, result, now); err != nil {
			return err
		}
		query, args := claimedResultUpdate(`UPDATE codex_tasks SET status=?,completion_phase=CASE WHEN ?='' THEN completion_phase ELSE ? END,result_summary=?,error_code=?,error_message=?,finished_at=CASE WHEN ? IS NULL THEN NULL ELSE COALESCE(finished_at,?) END WHERE id=?`, []any{status, phase, phase, nullable(result.Summary), nullable(result.ErrorCode), nullable(result.ErrorMessage), finishedAt, finishedAt, taskID}, result.ExpectedTurnID)
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

func importSkillTimingsTx(ctx context.Context, q assetDBTX, taskID string, runs []domain.SkillTimingRun, createdAt time.Time) error {
	if len(runs) == 0 {
		return nil
	}
	var snapshot sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT skill_snapshot_id FROM codex_tasks WHERE id=?`, taskID).Scan(&snapshot); err != nil {
		return err
	}
	if !snapshot.Valid || strings.TrimSpace(snapshot.String) == "" {
		return errors.New("skill timing import requires a task-bound skill snapshot")
	}
	for _, run := range runs {
		if run.TaskID != taskID || run.SkillSnapshotID != snapshot.String || run.Attempt <= 0 || run.StartedAt.IsZero() || run.FinishedAt.Before(run.StartedAt) || run.DurationMS != run.FinishedAt.Sub(run.StartedAt).Milliseconds() || (run.State != domain.PhaseCompleted && run.State != domain.PhaseFailed) || !json.Valid([]byte(run.DetailJSON)) {
			return errors.New("invalid skill timing run")
		}
		var existing domain.TaskPhaseRun
		err := q.QueryRowContext(ctx, `SELECT id,state,started_at,finished_at,duration_ms,detail_json FROM task_phase_runs WHERE task_id=? AND attempt=? AND phase_key=? AND source=? AND external_id=?`, taskID, run.Attempt, run.PhaseKey, domain.PhaseSourceSkill, run.ExternalID).Scan(&existing.ID, &existing.State, &existing.StartedAt, &existing.FinishedAt, &existing.DurationMS, &existing.DetailJSON)
		if err == nil {
			if existing.State != run.State || !existing.StartedAt.Equal(run.StartedAt) || existing.FinishedAt == nil || !existing.FinishedAt.Equal(run.FinishedAt) || existing.DurationMS == nil || *existing.DurationMS != run.DurationMS || existing.DetailJSON != run.DetailJSON {
				return errors.New("conflicting replayed skill timing run")
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		_, err = q.ExecContext(ctx, `INSERT INTO task_phase_runs(id,task_id,attempt,phase_key,display_name,source,state,started_at,running_at,finished_at,duration_ms,external_id,detail_json,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, uuid.NewString(), taskID, run.Attempt, run.PhaseKey, run.DisplayName, domain.PhaseSourceSkill, run.State, run.StartedAt, run.StartedAt, run.FinishedAt, run.DurationMS, run.ExternalID, run.DetailJSON, createdAt)
		if err != nil {
			return err
		}
	}
	return nil
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
	return runImmediate(ctx, r.db, operation, nil, fn)
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
