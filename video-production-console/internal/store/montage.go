package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/domain"
)

type MontageRepository struct {
	db     *sql.DB
	commit func(context.Context, *sql.Conn) error
}

func NewMontageRepository(db *sql.DB) *MontageRepository { return &MontageRepository{db: db} }

type BeginRegistration struct{ TaskID, ManifestPath, WorkspacePath string }
type RegistrationSuccess struct {
	AttemptID, RegisteredPath, ReceiptPath, SHA256, WorkspaceSHA256, Filename string
}

var ErrRegistrationNotRetryable = errors.New("montage registration is not retryable")

var (
	ErrRegistrationActive       = errors.New("montage registration already active")
	ErrRegistrationNotFound     = errors.New("montage registration not found")
	ErrRegistrationInputInvalid = errors.New("montage registration retained input is invalid")
)

const registrationReconcileTimeout = 5 * time.Second

func registrationReconcileContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), registrationReconcileTimeout)
}

type CompleteRegistration struct {
	TaskID, ManifestPath, WorkspacePath string
	Result                              TaskResultWrite
	Artifacts                           []TaskArtifact
}

// CompleteAndBegin atomically persists the plaintext result and its durable
// trusted-host registration job. There is no crash boundary at which a
// plaintext-ready montage exists without a queued registration attempt.
func (r *MontageRepository) CompleteAndBegin(ctx context.Context, input CompleteRegistration) (domain.RegistrationAttempt, error) {
	if strings.TrimSpace(input.TaskID) == "" || strings.TrimSpace(input.ManifestPath) == "" || strings.TrimSpace(input.WorkspacePath) == "" || input.Result.Status != domain.TaskCompleted {
		return domain.RegistrationAttempt{}, ErrRegistrationInputInvalid
	}
	manifestPath, err := canonicalRetainedPath(input.ManifestPath, false)
	if err != nil {
		return domain.RegistrationAttempt{}, fmt.Errorf("%w: manifest", ErrRegistrationInputInvalid)
	}
	workspacePath, err := canonicalRetainedPath(input.WorkspacePath, true)
	if err != nil {
		return domain.RegistrationAttempt{}, fmt.Errorf("%w: workspace", ErrRegistrationInputInvalid)
	}
	input.ManifestPath, input.WorkspacePath = manifestPath, workspacePath
	workspaceArtifacts := 0
	for _, artifact := range input.Artifacts {
		if artifact.Kind == "plaintext_workspace" {
			workspaceArtifacts++
			if !sameStorePath(artifact.Path, workspacePath) || !validStoredSHA256(artifact.SHA256) {
				return domain.RegistrationAttempt{}, ErrRegistrationInputInvalid
			}
		}
	}
	if workspaceArtifacts != 1 {
		return domain.RegistrationAttempt{}, ErrRegistrationInputInvalid
	}
	var attempt domain.RegistrationAttempt
	err = r.immediate(ctx, "complete montage and queue registration", func(q assetDBTX, now time.Time) error {
		if err := requireResultClaim(ctx, q, input.TaskID, input.Result.ExpectedTurnID); err != nil {
			return err
		}
		var action string
		if err := q.QueryRowContext(ctx, `SELECT COALESCE(action,'') FROM codex_tasks WHERE id=?`, input.TaskID).Scan(&action); err != nil {
			return err
		}
		if action != string(domain.ActionMontageExecute) {
			return ErrRegistrationInputInvalid
		}
		if input.Result.AssistantContent != "" {
			if err := insertMessage(ctx, q, domain.TaskMessage{TaskID: input.TaskID, Role: "assistant", Content: input.Result.AssistantContent, CreatedAt: now}); err != nil {
				return err
			}
		}
		if input.Result.EventKind != "" {
			if err := insertEvent(ctx, q, input.TaskID, domain.TaskEvent{Kind: input.Result.EventKind, Level: "info", DisplayText: input.Result.Summary, RawJSON: input.Result.RawJSON, CreatedAt: now}); err != nil {
				return err
			}
		}
		for _, artifact := range input.Artifacts {
			artifact.TaskID = input.TaskID
			if artifact.CreatedAt.IsZero() {
				artifact.CreatedAt = now
			}
			if err := insertTaskArtifact(ctx, q, artifact); err != nil {
				return err
			}
		}
		var err error
		attempt, err = insertQueuedRegistration(ctx, q, BeginRegistration{TaskID: input.TaskID, ManifestPath: input.ManifestPath, WorkspacePath: input.WorkspacePath}, now)
		if err != nil {
			return err
		}
		query, args := claimedResultUpdate(`UPDATE codex_tasks SET status=?,completion_phase=?,result_summary=?,error_code=NULL,error_message=NULL,finished_at=NULL WHERE id=?`, []any{domain.TaskRunning, domain.CompletionPlaintextReady, nullable(input.Result.Summary), input.TaskID}, input.Result.ExpectedTurnID)
		result, err := q.ExecContext(ctx, query, args...)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			if err != nil {
				return err
			}
			return ErrRegistrationNotFound
		}
		return nil
	})
	return r.reconcileQueuedCommit(ctx, attempt, err)
}

func validStoredSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

func (r *MontageRepository) Begin(ctx context.Context, input BeginRegistration) (domain.RegistrationAttempt, error) {
	if strings.TrimSpace(input.TaskID) == "" || strings.TrimSpace(input.ManifestPath) == "" || strings.TrimSpace(input.WorkspacePath) == "" {
		return domain.RegistrationAttempt{}, fmt.Errorf("task, manifest, and workspace are required")
	}
	manifestPath, err := canonicalRetainedPath(input.ManifestPath, false)
	if err != nil {
		return domain.RegistrationAttempt{}, fmt.Errorf("%w: manifest", ErrRegistrationInputInvalid)
	}
	workspacePath, err := canonicalRetainedPath(input.WorkspacePath, true)
	if err != nil {
		return domain.RegistrationAttempt{}, fmt.Errorf("%w: workspace", ErrRegistrationInputInvalid)
	}
	input.ManifestPath, input.WorkspacePath = manifestPath, workspacePath
	var attempt domain.RegistrationAttempt
	err = r.immediate(ctx, "begin montage registration", func(q assetDBTX, now time.Time) error {
		var action string
		var status domain.TaskStatus
		if err := q.QueryRowContext(ctx, `SELECT COALESCE(action,''),status FROM codex_tasks WHERE id=?`, input.TaskID).Scan(&action, &status); err != nil {
			return err
		}
		if action != string(domain.ActionMontageExecute) {
			return fmt.Errorf("task is not a montage execution")
		}
		var err error
		attempt, err = insertQueuedRegistration(ctx, q, input, now)
		if err != nil {
			return err
		}
		_, err = q.ExecContext(ctx, `UPDATE codex_tasks SET status=?,completion_phase=?,error_code=NULL,error_message=NULL,finished_at=NULL WHERE id=?`, domain.TaskRunning, domain.CompletionPlaintextReady, input.TaskID)
		return err
	})
	return r.reconcileQueuedCommit(ctx, attempt, err)
}

// BeginRetry creates an attempt exclusively from the latest durable attempt.
// No caller-controlled path participates in a retry.
func (r *MontageRepository) BeginRetry(ctx context.Context, taskID string) (domain.RegistrationAttempt, error) {
	if strings.TrimSpace(taskID) == "" {
		return domain.RegistrationAttempt{}, ErrRegistrationNotRetryable
	}
	var attempt domain.RegistrationAttempt
	err := r.immediate(ctx, "retry montage registration", func(q assetDBTX, now time.Time) error {
		var prior domain.RegistrationAttempt
		err := q.QueryRowContext(ctx, `SELECT id,task_id,manifest_path,workspace_path,state,attempt,registered_path,receipt_path,error_code,error_message,started_at,finished_at FROM montage_registration_attempts WHERE task_id=? ORDER BY attempt DESC LIMIT 1`, taskID).Scan(&prior.ID, &prior.TaskID, &prior.ManifestPath, &prior.WorkspacePath, &prior.State, &prior.Attempt, &prior.RegisteredPath, &prior.ReceiptPath, &prior.ErrorCode, &prior.ErrorMessage, &prior.StartedAt, &prior.FinishedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrRegistrationNotFound
		}
		if err != nil {
			return err
		}
		if prior.State != domain.RegistrationFailed && prior.State != domain.RegistrationInterrupted {
			if prior.State == domain.RegistrationQueued || prior.State == domain.RegistrationRunning {
				return ErrRegistrationActive
			}
			return fmt.Errorf("%w: state %s", ErrRegistrationInputInvalid, prior.State)
		}
		var action string
		if err := q.QueryRowContext(ctx, `SELECT COALESCE(action,'') FROM codex_tasks WHERE id=?`, prior.TaskID).Scan(&action); err != nil {
			return err
		}
		if action != string(domain.ActionMontageExecute) {
			return fmt.Errorf("%w: source task is not montage.execute", ErrRegistrationInputInvalid)
		}
		manifestPath, manifestErr := canonicalRetainedPath(prior.ManifestPath, false)
		if manifestErr != nil || !sameStorePath(manifestPath, prior.ManifestPath) {
			return fmt.Errorf("%w: saved manifest is unavailable", ErrRegistrationInputInvalid)
		}
		workspacePath, workspaceErr := canonicalRetainedPath(prior.WorkspacePath, true)
		if workspaceErr != nil || !sameStorePath(workspacePath, prior.WorkspacePath) {
			return fmt.Errorf("%w: saved workspace is unavailable", ErrRegistrationInputInvalid)
		}
		attempt, err = insertQueuedRegistration(ctx, q, BeginRegistration{TaskID: prior.TaskID, ManifestPath: prior.ManifestPath, WorkspacePath: prior.WorkspacePath}, now)
		if err != nil {
			return err
		}
		_, err = q.ExecContext(ctx, `UPDATE codex_tasks SET status=?,completion_phase=?,error_code=NULL,error_message=NULL,finished_at=NULL WHERE id=?`, domain.TaskRunning, domain.CompletionPlaintextReady, prior.TaskID)
		return err
	})
	return r.reconcileQueuedCommit(ctx, attempt, err)
}

func canonicalRetainedPath(path string, directory bool) (string, error) {
	path = normalizeWindowsExtendedPath(path)
	if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) {
		return "", os.ErrInvalid
	}
	abs := filepath.Clean(path)
	before, err := os.Lstat(abs)
	if err != nil || before.Mode()&os.ModeSymlink != 0 || before.IsDir() != directory || (!directory && !before.Mode().IsRegular()) {
		return "", os.ErrInvalid
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil || !sameStorePath(abs, resolved) {
		return "", os.ErrInvalid
	}
	return abs, nil
}

func normalizeWindowsExtendedPath(path string) string {
	if filepath.Separator != '\\' {
		return path
	}
	if strings.HasPrefix(path, `\\?\UNC\`) {
		return `\\` + strings.TrimPrefix(path, `\\?\UNC\`)
	}
	return strings.TrimPrefix(path, `\\?\`)
}

func sameStorePath(left, right string) bool {
	if filepath.Separator == '\\' {
		return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

func (r *MontageRepository) reconcileQueuedCommit(ctx context.Context, attempt domain.RegistrationAttempt, err error) (domain.RegistrationAttempt, error) {
	if err == nil {
		return attempt, nil
	}
	if commitOutcome(err) != CommitUnknown || attempt.ID == "" {
		return domain.RegistrationAttempt{}, err
	}
	reconcileCtx, cancel := registrationReconcileContext(ctx)
	defer cancel()
	persisted, lookupErr := r.Attempt(reconcileCtx, attempt.ID)
	if lookupErr == nil && persisted.State == domain.RegistrationQueued && persisted.TaskID == attempt.TaskID && persisted.ManifestPath == attempt.ManifestPath && persisted.WorkspacePath == attempt.WorkspacePath {
		return persisted, nil
	}
	if errors.Is(lookupErr, sql.ErrNoRows) {
		return domain.RegistrationAttempt{}, err
	}
	if lookupErr != nil {
		return domain.RegistrationAttempt{}, fmt.Errorf("reconcile unknown registration commit: %w", lookupErr)
	}
	return domain.RegistrationAttempt{}, fmt.Errorf("reconcile unknown registration commit: durable attempt has state %s", persisted.State)
}

func insertQueuedRegistration(ctx context.Context, q assetDBTX, input BeginRegistration, now time.Time) (domain.RegistrationAttempt, error) {
	var active, number int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM montage_registration_attempts WHERE task_id=? AND state IN ('queued','running')`, input.TaskID).Scan(&active); err != nil {
		return domain.RegistrationAttempt{}, err
	}
	if active != 0 {
		return domain.RegistrationAttempt{}, ErrRegistrationActive
	}
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(MAX(attempt),0)+1 FROM montage_registration_attempts WHERE task_id=?`, input.TaskID).Scan(&number); err != nil {
		return domain.RegistrationAttempt{}, err
	}
	attempt := domain.RegistrationAttempt{ID: uuid.NewString(), TaskID: input.TaskID, ManifestPath: input.ManifestPath, WorkspacePath: input.WorkspacePath, State: domain.RegistrationQueued, Attempt: number, StartedAt: now}
	_, err := q.ExecContext(ctx, `INSERT INTO montage_registration_attempts(id,task_id,manifest_path,workspace_path,state,attempt,started_at) VALUES(?,?,?,?,?,?,?)`, attempt.ID, attempt.TaskID, attempt.ManifestPath, attempt.WorkspacePath, attempt.State, attempt.Attempt, attempt.StartedAt)
	return attempt, err
}

// RecoverActive interrupts work that was inside the host registration process
// and returns still-queued work for safe re-enqueueing.
func (r *MontageRepository) RecoverActive(ctx context.Context) (domain.RegistrationRecovery, error) {
	recovery, err := r.recoverActiveOnce(ctx)
	if commitOutcome(err) != CommitUnknown {
		return recovery, err
	}
	// The first COMMIT may or may not have reached SQLite. A fresh transaction
	// re-reads the active set and deterministically applies any remaining work.
	reconcileCtx, cancel := registrationReconcileContext(ctx)
	defer cancel()
	recovery, err = r.recoverActiveOnce(reconcileCtx)
	if commitOutcome(err) != CommitUnknown {
		return recovery, err
	}
	active, lookupErr := r.activeAttempts(reconcileCtx)
	if lookupErr != nil {
		return domain.RegistrationRecovery{}, fmt.Errorf("reconcile unknown recovery commit: %w", lookupErr)
	}
	final := domain.RegistrationRecovery{}
	for _, attempt := range active {
		if attempt.State == domain.RegistrationRunning {
			return final, err
		}
		if attempt.State == domain.RegistrationQueued {
			final.Queued = append(final.Queued, attempt)
		}
	}
	return final, nil
}

func (r *MontageRepository) activeAttempts(ctx context.Context) ([]domain.RegistrationAttempt, error) {
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	rows, err := conn.QueryContext(ctx, `SELECT id,task_id,manifest_path,workspace_path,state,attempt,registered_path,receipt_path,error_code,error_message,started_at,finished_at FROM montage_registration_attempts WHERE state IN ('queued','running') ORDER BY started_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var active []domain.RegistrationAttempt
	for rows.Next() {
		var item domain.RegistrationAttempt
		if err := rows.Scan(&item.ID, &item.TaskID, &item.ManifestPath, &item.WorkspacePath, &item.State, &item.Attempt, &item.RegisteredPath, &item.ReceiptPath, &item.ErrorCode, &item.ErrorMessage, &item.StartedAt, &item.FinishedAt); err != nil {
			return nil, err
		}
		active = append(active, item)
	}
	return active, rows.Err()
}

func (r *MontageRepository) recoverActiveOnce(ctx context.Context) (domain.RegistrationRecovery, error) {
	var recovery domain.RegistrationRecovery
	err := r.immediate(ctx, "recover montage registrations", func(q assetDBTX, now time.Time) error {
		rows, err := q.QueryContext(ctx, `SELECT id,task_id,manifest_path,workspace_path,state,attempt,registered_path,receipt_path,error_code,error_message,started_at,finished_at FROM montage_registration_attempts WHERE state IN ('queued','running') ORDER BY started_at,id`)
		if err != nil {
			return err
		}
		var active []domain.RegistrationAttempt
		for rows.Next() {
			var item domain.RegistrationAttempt
			if err := rows.Scan(&item.ID, &item.TaskID, &item.ManifestPath, &item.WorkspacePath, &item.State, &item.Attempt, &item.RegisteredPath, &item.ReceiptPath, &item.ErrorCode, &item.ErrorMessage, &item.StartedAt, &item.FinishedAt); err != nil {
				_ = rows.Close()
				return err
			}
			active = append(active, item)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, item := range active {
			if item.State == domain.RegistrationQueued {
				manifestPath, manifestErr := canonicalRetainedPath(item.ManifestPath, false)
				workspacePath, workspaceErr := canonicalRetainedPath(item.WorkspacePath, true)
				if manifestErr == nil && workspaceErr == nil && sameStorePath(manifestPath, item.ManifestPath) && sameStorePath(workspacePath, item.WorkspacePath) {
					if _, err := q.ExecContext(ctx, `UPDATE codex_tasks SET status=?,completion_phase=?,error_code=NULL,error_message=NULL,finished_at=NULL WHERE id=?`, domain.TaskRunning, domain.CompletionPlaintextReady, item.TaskID); err != nil {
						return err
					}
					recovery.Queued = append(recovery.Queued, item)
					continue
				}
				code, message := "registration_recovery_input_missing", "Saved manifest or plaintext workspace is unavailable after restart."
				if _, err := q.ExecContext(ctx, `UPDATE montage_registration_attempts SET state=?,error_code=?,error_message=?,finished_at=? WHERE id=? AND state=?`, domain.RegistrationFailed, code, message, now, item.ID, domain.RegistrationQueued); err != nil {
					return err
				}
				if _, err := q.ExecContext(ctx, `UPDATE codex_tasks SET status=?,completion_phase=?,error_code=?,error_message=?,finished_at=? WHERE id=?`, domain.TaskFailed, domain.CompletionPlaintextReady, code, message, now, item.TaskID); err != nil {
					return err
				}
				continue
			}
			code, message := "registration_interrupted_on_restart", "Console restarted while trusted-host registration was running."
			result, err := q.ExecContext(ctx, `UPDATE montage_registration_attempts SET state=?,error_code=?,error_message=?,finished_at=? WHERE id=? AND state=?`, domain.RegistrationInterrupted, code, message, now, item.ID, domain.RegistrationRunning)
			if err != nil {
				return err
			}
			if affected, err := result.RowsAffected(); err != nil || affected != 1 {
				if err != nil {
					return err
				}
				return fmt.Errorf("registration attempt changed while recovering")
			}
			item.State, item.ErrorCode, item.ErrorMessage, item.FinishedAt = domain.RegistrationInterrupted, &code, &message, &now
			recovery.Interrupted = append(recovery.Interrupted, item)
			if _, err := q.ExecContext(ctx, `UPDATE codex_tasks SET status=?,completion_phase=?,error_code=?,error_message=?,finished_at=? WHERE id=?`, domain.TaskFailed, domain.CompletionPlaintextReady, code, message, now, item.TaskID); err != nil {
				return err
			}
		}
		return nil
	})
	return recovery, err
}

func (r *MontageRepository) MarkRunning(ctx context.Context, id string) error {
	err := r.immediate(ctx, "start montage registration", func(q assetDBTX, now time.Time) error {
		result, err := q.ExecContext(ctx, `UPDATE montage_registration_attempts SET state=?,started_at=? WHERE id=? AND state=?`, domain.RegistrationRunning, now, id, domain.RegistrationQueued)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			if err != nil {
				return err
			}
			return fmt.Errorf("registration attempt is not queued")
		}
		_, err = q.ExecContext(ctx, `UPDATE codex_tasks SET completion_phase=? WHERE id=(SELECT task_id FROM montage_registration_attempts WHERE id=?)`, domain.CompletionRegistering, id)
		return err
	})
	if commitOutcome(err) == CommitUnknown {
		reconcileCtx, cancel := registrationReconcileContext(ctx)
		defer cancel()
		if attempt, lookupErr := r.Attempt(reconcileCtx, id); lookupErr == nil && attempt.State == domain.RegistrationRunning {
			return nil
		}
	}
	return err
}

func (r *MontageRepository) Succeed(ctx context.Context, success RegistrationSuccess) error {
	if strings.TrimSpace(success.AttemptID) == "" || strings.TrimSpace(success.RegisteredPath) == "" || strings.TrimSpace(success.ReceiptPath) == "" || len(success.SHA256) != 64 || len(success.WorkspaceSHA256) != 64 {
		return fmt.Errorf("invalid montage registration success")
	}
	if success.Filename == "" {
		success.Filename = filepath.Base(success.RegisteredPath)
	}
	err := r.immediate(ctx, "complete montage registration", func(q assetDBTX, now time.Time) error {
		var taskID, accountID, projectID string
		var state domain.RegistrationState
		if err := q.QueryRowContext(ctx, `SELECT attempt.task_id,attempt.state,task.account_id,COALESCE(task.project_id,'') FROM montage_registration_attempts attempt JOIN codex_tasks task ON task.id=attempt.task_id WHERE attempt.id=?`, success.AttemptID).Scan(&taskID, &state, &accountID, &projectID); err != nil {
			return err
		}
		if state == domain.RegistrationSucceeded {
			var count int
			if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM asset_versions version JOIN codex_tasks task ON task.id=version.source_task_id JOIN task_artifacts artifact ON artifact.task_id=task.id AND artifact.kind='plaintext_workspace' AND LOWER(artifact.sha256)=? WHERE version.type=? AND version.source_task_id=? AND version.path=? AND version.sha256=? AND version.state=? AND task.status=? AND task.completion_phase=?`, strings.ToLower(success.WorkspaceSHA256), domain.AssetMixDraft, taskID, success.RegisteredPath, strings.ToLower(success.SHA256), domain.AssetReady, domain.TaskCompleted, domain.CompletionRegistered).Scan(&count); err != nil {
				return err
			}
			if count != 1 {
				return fmt.Errorf("succeeded registration is missing its verified ready asset")
			}
			return nil
		}
		if state != domain.RegistrationRunning || projectID == "" {
			return fmt.Errorf("registration attempt is not running or task has no project")
		}
		var workspaceArtifacts int
		if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_artifacts WHERE task_id=? AND kind='plaintext_workspace' AND LOWER(sha256)=?`, taskID, strings.ToLower(success.WorkspaceSHA256)).Scan(&workspaceArtifacts); err != nil {
			return err
		}
		if workspaceArtifacts != 1 {
			return fmt.Errorf("plaintext workspace durable digest changed before registration completion")
		}
		if _, err := NewAssetRepository(r.db).addVersion(ctx, q, AddAssetVersion{ProjectID: &projectID, AccountID: accountID, Type: domain.AssetMixDraft, StorageKind: domain.StorageDirectory, Path: success.RegisteredPath, Filename: success.Filename, MIMEType: "inode/directory", Size: 0, SHA256: strings.ToLower(success.SHA256), SourceTaskID: &taskID}, now); err != nil {
			return err
		}
		result, err := q.ExecContext(ctx, `UPDATE montage_registration_attempts SET state=?,registered_path=?,receipt_path=?,error_code=NULL,error_message=NULL,finished_at=? WHERE id=? AND state=?`, domain.RegistrationSucceeded, success.RegisteredPath, success.ReceiptPath, now, success.AttemptID, domain.RegistrationRunning)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			if err != nil {
				return err
			}
			return fmt.Errorf("registration attempt changed during completion")
		}
		if _, err := q.ExecContext(ctx, `UPDATE codex_tasks SET status=?,completion_phase=?,error_code=NULL,error_message=NULL,finished_at=? WHERE id=?`, domain.TaskCompleted, domain.CompletionRegistered, now, taskID); err != nil {
			return err
		}
		_, err = q.ExecContext(ctx, `UPDATE projects SET stage=CASE WHEN stage IN ('topic','script','assets','mixing') THEN 'review' ELSE stage END,updated_at=? WHERE id=?`, now, projectID)
		return err
	})
	if commitOutcome(err) == CommitUnknown {
		reconcileCtx, cancel := registrationReconcileContext(ctx)
		defer cancel()
		if durable, lookupErr := r.registrationSuccessDurable(reconcileCtx, success); lookupErr == nil && durable {
			return nil
		}
	}
	return err
}

func (r *MontageRepository) registrationSuccessDurable(ctx context.Context, success RegistrationSuccess) (bool, error) {
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Close()
	var count int
	err = conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM montage_registration_attempts attempt JOIN codex_tasks task ON task.id=attempt.task_id JOIN asset_versions version ON version.source_task_id=task.id JOIN task_artifacts artifact ON artifact.task_id=task.id AND artifact.kind='plaintext_workspace' AND LOWER(artifact.sha256)=? WHERE attempt.id=? AND attempt.state=? AND attempt.registered_path=? AND attempt.receipt_path=? AND version.type=? AND version.path=? AND version.sha256=? AND version.state=? AND task.status=? AND task.completion_phase=?`, strings.ToLower(success.WorkspaceSHA256), success.AttemptID, domain.RegistrationSucceeded, success.RegisteredPath, success.ReceiptPath, domain.AssetMixDraft, success.RegisteredPath, strings.ToLower(success.SHA256), domain.AssetReady, domain.TaskCompleted, domain.CompletionRegistered).Scan(&count)
	return count == 1, err
}

// AuditMixDrafts removes ready status from formal drafts that cannot prove
// trusted-host registration or whose canonical location is not under root.
func (r *MontageRepository) AuditMixDrafts(ctx context.Context, trustedJianyingRoot string) (domain.MixDraftAudit, error) {
	root, err := canonicalMontageDirectory(trustedJianyingRoot)
	if err != nil {
		return domain.MixDraftAudit{}, fmt.Errorf("canonical Jianying root: %w", err)
	}
	type candidate struct {
		id, path   string
		taskID     *string
		action     string
		taskExists int
		succeeded  int
	}
	rows, err := r.db.QueryContext(ctx, `SELECT version.id,version.path,version.source_task_id,COALESCE(task.action,''),CASE WHEN task.id IS NULL THEN 0 ELSE 1 END,CASE WHEN EXISTS(SELECT 1 FROM montage_registration_attempts attempt WHERE attempt.task_id=version.source_task_id AND attempt.state='succeeded' AND attempt.registered_path=version.path) THEN 1 ELSE 0 END FROM asset_versions version LEFT JOIN codex_tasks task ON task.id=version.source_task_id WHERE version.type=? AND version.state=? ORDER BY version.created_at,version.id`, domain.AssetMixDraft, domain.AssetReady)
	if err != nil {
		return domain.MixDraftAudit{}, err
	}
	var candidates []candidate
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.id, &item.path, &item.taskID, &item.action, &item.taskExists, &item.succeeded); err != nil {
			_ = rows.Close()
			return domain.MixDraftAudit{}, err
		}
		candidates = append(candidates, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return domain.MixDraftAudit{}, err
	}
	if err := rows.Close(); err != nil {
		return domain.MixDraftAudit{}, err
	}
	report := domain.MixDraftAudit{Inspected: len(candidates)}
	for _, item := range candidates {
		reason := ""
		switch {
		case item.taskID == nil:
			reason = "mix_draft has no source task"
		case item.taskExists == 0:
			reason = "mix_draft source task does not exist"
		case item.action != string(domain.ActionMontageExecute):
			reason = "mix_draft source task is not montage.execute"
		case item.succeeded == 0:
			reason = "montage.execute source task has no matching succeeded registration attempt"
		}
		registered, pathErr := canonicalMontageDirectory(item.path)
		if pathErr != nil {
			if reason == "" {
				reason = "registered draft directory is unavailable"
			}
		} else if !montagePathWithin(root, registered) {
			if reason == "" {
				reason = "registered draft path is outside trusted canonical Jianying root"
			} else {
				reason += "; registered draft path is outside trusted canonical Jianying root"
			}
		}
		if reason != "" {
			report.Findings = append(report.Findings, domain.MixDraftAuditFinding{AssetVersionID: item.id, TaskID: item.taskID, Path: item.path, Reason: reason})
		}
	}
	if len(report.Findings) == 0 {
		return report, nil
	}
	err = r.immediate(ctx, "audit montage assets", func(q assetDBTX, _ time.Time) error {
		for _, finding := range report.Findings {
			result, err := q.ExecContext(ctx, `UPDATE asset_versions SET state=?,stale_reason=? WHERE id=? AND type=? AND state=?`, domain.AssetStale, finding.Reason, finding.AssetVersionID, domain.AssetMixDraft, domain.AssetReady)
			if err != nil {
				return err
			}
			affected, err := result.RowsAffected()
			if err != nil {
				return err
			}
			report.Staled += int(affected)
		}
		return nil
	})
	return report, err
}

func canonicalMontageDirectory(path string) (string, error) {
	if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) {
		return "", os.ErrInvalid
	}
	clean := filepath.Clean(path)
	before, err := os.Lstat(clean)
	if err != nil || before.Mode()&os.ModeSymlink != 0 || !before.IsDir() {
		if err != nil {
			return "", err
		}
		return "", os.ErrInvalid
	}
	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil || !sameStorePath(clean, resolved) {
		return "", os.ErrInvalid
	}
	return clean, nil
}

func montagePathWithin(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	return err == nil && rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (r *MontageRepository) Fail(ctx context.Context, id, code, message string) error {
	err := r.immediate(ctx, "fail montage registration", func(q assetDBTX, now time.Time) error {
		result, err := q.ExecContext(ctx, `UPDATE montage_registration_attempts SET state=?,error_code=?,error_message=?,finished_at=? WHERE id=? AND state IN ('queued','running')`, domain.RegistrationFailed, code, message, now, id)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			if err != nil {
				return err
			}
			return fmt.Errorf("registration attempt is not active")
		}
		_, err = q.ExecContext(ctx, `UPDATE codex_tasks SET status=?,completion_phase=?,error_code=?,error_message=?,finished_at=? WHERE id=(SELECT task_id FROM montage_registration_attempts WHERE id=?)`, domain.TaskFailed, domain.CompletionPlaintextReady, code, message, now, id)
		return err
	})
	if commitOutcome(err) == CommitUnknown {
		reconcileCtx, cancel := registrationReconcileContext(ctx)
		defer cancel()
		if attempt, lookupErr := r.Attempt(reconcileCtx, id); lookupErr == nil && attempt.State == domain.RegistrationFailed && attempt.ErrorCode != nil && *attempt.ErrorCode == code {
			return nil
		}
	}
	return err
}

// FailCommit conservatively repairs a completion that could not be committed
// or verified. Terminal succeeded attempts are never moved backwards: a fully
// committed transaction is accepted, while an inconsistent ready asset is
// made stale and the task is failed for audit.
func (r *MontageRepository) FailCommit(ctx context.Context, id, message string) error {
	err := r.immediate(ctx, "fail montage registration commit", func(q assetDBTX, now time.Time) error {
		var taskID string
		var registeredPath sql.NullString
		var state domain.RegistrationState
		if err := q.QueryRowContext(ctx, `SELECT task_id,state,registered_path FROM montage_registration_attempts WHERE id=?`, id).Scan(&taskID, &state, &registeredPath); err != nil {
			return err
		}
		if state == domain.RegistrationSucceeded {
			var complete int
			if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM asset_versions version JOIN codex_tasks task ON task.id=version.source_task_id WHERE version.type=? AND version.source_task_id=? AND version.path=? AND version.state=? AND task.status=? AND task.completion_phase=?`, domain.AssetMixDraft, taskID, registeredPath.String, domain.AssetReady, domain.TaskCompleted, domain.CompletionRegistered).Scan(&complete); err != nil {
				return err
			}
			if complete == 1 {
				return nil
			}
			reason := "registration database completion could not be verified: " + message
			if _, err := q.ExecContext(ctx, `UPDATE asset_versions SET state=?,stale_reason=? WHERE type=? AND source_task_id=? AND state=?`, domain.AssetStale, reason, domain.AssetMixDraft, taskID, domain.AssetReady); err != nil {
				return err
			}
			_, err := q.ExecContext(ctx, `UPDATE codex_tasks SET status=?,completion_phase=?,error_code=?,error_message=?,finished_at=? WHERE id=?`, domain.TaskFailed, domain.CompletionPlaintextReady, "registration_commit_failed", message, now, taskID)
			return err
		}
		if state != domain.RegistrationRunning {
			return fmt.Errorf("registration attempt cannot fail commit from state %s", state)
		}
		code := "registration_commit_failed"
		result, err := q.ExecContext(ctx, `UPDATE montage_registration_attempts SET state=?,error_code=?,error_message=?,finished_at=? WHERE id=? AND state=?`, domain.RegistrationFailed, code, message, now, id, domain.RegistrationRunning)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			if err != nil {
				return err
			}
			return fmt.Errorf("registration attempt changed during commit failure")
		}
		_, err = q.ExecContext(ctx, `UPDATE codex_tasks SET status=?,completion_phase=?,error_code=?,error_message=?,finished_at=? WHERE id=?`, domain.TaskFailed, domain.CompletionPlaintextReady, code, message, now, taskID)
		return err
	})
	if commitOutcome(err) == CommitUnknown {
		reconcileCtx, cancel := registrationReconcileContext(ctx)
		defer cancel()
		attempt, lookupErr := r.Attempt(reconcileCtx, id)
		if lookupErr == nil && attempt.State == domain.RegistrationFailed {
			return nil
		}
		if lookupErr == nil && attempt.State == domain.RegistrationSucceeded {
			var status domain.TaskStatus
			if taskErr := r.db.QueryRowContext(reconcileCtx, `SELECT status FROM codex_tasks WHERE id=?`, attempt.TaskID).Scan(&status); taskErr == nil && (status == domain.TaskCompleted || status == domain.TaskFailed) {
				return nil
			}
		}
	}
	return err
}

func (r *MontageRepository) Attempts(ctx context.Context, taskID string) ([]domain.RegistrationAttempt, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,task_id,manifest_path,workspace_path,state,attempt,registered_path,receipt_path,error_code,error_message,started_at,finished_at FROM montage_registration_attempts WHERE task_id=? ORDER BY attempt DESC`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.RegistrationAttempt
	for rows.Next() {
		var item domain.RegistrationAttempt
		if err := rows.Scan(&item.ID, &item.TaskID, &item.ManifestPath, &item.WorkspacePath, &item.State, &item.Attempt, &item.RegisteredPath, &item.ReceiptPath, &item.ErrorCode, &item.ErrorMessage, &item.StartedAt, &item.FinishedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *MontageRepository) Latest(ctx context.Context, taskID string) (domain.RegistrationAttempt, error) {
	items, err := r.Attempts(ctx, taskID)
	if err != nil {
		return domain.RegistrationAttempt{}, err
	}
	if len(items) == 0 {
		return domain.RegistrationAttempt{}, sql.ErrNoRows
	}
	return items[0], nil
}

func (r *MontageRepository) Attempt(ctx context.Context, id string) (domain.RegistrationAttempt, error) {
	var item domain.RegistrationAttempt
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return item, err
	}
	defer conn.Close()
	err = conn.QueryRowContext(ctx, `SELECT id,task_id,manifest_path,workspace_path,state,attempt,registered_path,receipt_path,error_code,error_message,started_at,finished_at FROM montage_registration_attempts WHERE id=?`, id).Scan(&item.ID, &item.TaskID, &item.ManifestPath, &item.WorkspacePath, &item.State, &item.Attempt, &item.RegisteredPath, &item.ReceiptPath, &item.ErrorCode, &item.ErrorMessage, &item.StartedAt, &item.FinishedAt)
	return item, err
}

func (r *MontageRepository) PlaintextWorkspaceArtifact(ctx context.Context, attemptID string) (TaskArtifact, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT artifact.id,artifact.task_id,artifact.kind,artifact.path,artifact.filename,artifact.mime_type,artifact.size,artifact.sha256,artifact.created_at FROM montage_registration_attempts attempt JOIN task_artifacts artifact ON artifact.task_id=attempt.task_id AND artifact.kind='plaintext_workspace' WHERE attempt.id=? LIMIT 2`, attemptID)
	if err != nil {
		return TaskArtifact{}, err
	}
	defer rows.Close()
	var artifacts []TaskArtifact
	for rows.Next() {
		var artifact TaskArtifact
		if err := rows.Scan(&artifact.ID, &artifact.TaskID, &artifact.Kind, &artifact.Path, &artifact.Filename, &artifact.MIMEType, &artifact.Size, &artifact.SHA256, &artifact.CreatedAt); err != nil {
			return TaskArtifact{}, err
		}
		artifacts = append(artifacts, artifact)
	}
	if err := rows.Err(); err != nil {
		return TaskArtifact{}, err
	}
	if len(artifacts) != 1 || !validStoredSHA256(artifacts[0].SHA256) {
		return TaskArtifact{}, ErrRegistrationInputInvalid
	}
	return artifacts[0], nil
}

func (r *MontageRepository) immediate(ctx context.Context, operation string, fn func(assetDBTX, time.Time) error) error {
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
	var commitErr error
	if r.commit != nil {
		commitErr = r.commit(ctx, conn)
	} else {
		_, commitErr = conn.ExecContext(ctx, `COMMIT`)
	}
	if commitErr != nil {
		return &CommitOutcomeError{Outcome: CommitUnknown, Err: fmt.Errorf("commit %s outcome unknown: %w", operation, commitErr)}
	}
	committed = true
	return nil
}
