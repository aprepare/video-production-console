package montage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"video-production-console/internal/domain"
	"video-production-console/internal/store"
	"video-production-console/internal/taskcompletion"
)

type registerer interface {
	Register(context.Context, RegisterRequest) (RegisterResult, error)
	Reconcile(context.Context, ReconcileRequest) (ReconcileResult, error)
}
type registrationJob struct {
	Attempt   domain.RegistrationAttempt
	Reconcile *domain.DraftDisplayReconcileCandidate
}

type TrustedRuntime struct {
	MachineProfilePath   string
	MachineProfileSHA256 string
	JianyingRoot         string
	PythonBinary         string
}

var ErrCoordinatorStopped = errors.New("montage registration coordinator is stopped")
var ErrRegistrationBackpressure = errors.New("montage registration queue is full")

const registrationEnqueueWait = 30 * time.Second

type Coordinator struct {
	tasks     *store.TaskRepository
	repo      *store.MontageRepository
	registrar registerer
	queue     chan registrationJob
	wg        sync.WaitGroup
	once      sync.Once
	lifecycle sync.Mutex
	stopped   atomic.Bool
	sends     sync.WaitGroup
	ctx       context.Context
	cancel    context.CancelFunc
	runtime   TrustedRuntime
}

func NewCoordinator(tasks *store.TaskRepository, registrar registerer, runtime TrustedRuntime) *Coordinator {
	workerCtx, cancel := context.WithCancel(context.Background())
	coordinator := &Coordinator{tasks: tasks, repo: store.NewMontageRepository(tasks.DB()), registrar: registrar, queue: make(chan registrationJob, 32), ctx: workerCtx, cancel: cancel, runtime: runtime}
	coordinator.wg.Add(1)
	go coordinator.run()
	return coordinator
}

func ResolveTrustedRuntime(machineProfilePath, configuredJianyingRoot string) (TrustedRuntime, error) {
	profilePath, err := canonicalNoFollow(machineProfilePath, false)
	if err != nil {
		return TrustedRuntime{}, errors.New("trusted machine profile is not a canonical no-follow file")
	}
	profileData, err := readBounded(profilePath, 4<<20)
	if err != nil {
		return TrustedRuntime{}, err
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(profileData, &fields) != nil {
		return TrustedRuntime{}, errors.New("trusted machine profile is invalid")
	}
	var pythonBinary, profileJianyingRoot string
	_ = json.Unmarshal(fields["python_binary"], &pythonBinary)
	_ = json.Unmarshal(fields["jianying_root"], &profileJianyingRoot)
	if strings.TrimSpace(pythonBinary) == "" {
		pythonBinary = "python"
	}
	if !filepath.IsAbs(pythonBinary) {
		pythonBinary, err = exec.LookPath(pythonBinary)
		if err != nil {
			return TrustedRuntime{}, errors.New("trusted Python executable could not be resolved")
		}
	}
	pythonBinary, err = canonicalNoFollow(pythonBinary, false)
	if err != nil {
		return TrustedRuntime{}, errors.New("trusted Python executable is not canonical")
	}
	jianyingRoot := profileJianyingRoot
	if strings.TrimSpace(configuredJianyingRoot) != "" {
		if strings.TrimSpace(profileJianyingRoot) != "" && !samePath(profileJianyingRoot, configuredJianyingRoot) {
			return TrustedRuntime{}, errors.New("trusted Jianying root disagrees with machine profile")
		}
		jianyingRoot = configuredJianyingRoot
	}
	jianyingRoot, err = canonicalNoFollow(jianyingRoot, true)
	if err != nil {
		return TrustedRuntime{}, errors.New("trusted Jianying root is not canonical")
	}
	profileHash, err := hashFile(profilePath)
	if err != nil {
		return TrustedRuntime{}, err
	}
	return TrustedRuntime{MachineProfilePath: profilePath, MachineProfileSHA256: profileHash, JianyingRoot: jianyingRoot, PythonBinary: pythonBinary}, nil
}

func (c *Coordinator) HandleCompleted(ctx context.Context, input taskcompletion.CompletedInput) (bool, error) {
	if input.Action != domain.ActionMontageExecute {
		return false, nil
	}
	if c == nil || c.tasks == nil || c.repo == nil || c.registrar == nil {
		return true, errors.New("montage registration coordinator is not configured")
	}
	var workspace string
	for _, artifact := range input.Artifacts {
		if artifact.Kind != "plaintext_workspace" {
			continue
		}
		if workspace != "" {
			return true, fmt.Errorf("montage result contains multiple plaintext workspaces")
		}
		workspace = artifact.Path
	}
	if workspace == "" {
		return true, fmt.Errorf("montage result has no plaintext workspace")
	}
	manifestPath, workspacePath, err := c.validateRetainedPaths(input.Task.ID, input.ManifestPath, workspace)
	if err != nil {
		return true, err
	}
	for i := range input.Artifacts {
		if input.Artifacts[i].Kind == "plaintext_workspace" {
			input.Artifacts[i].Path = workspacePath
		}
	}
	attempt, err := c.repo.CompleteAndBegin(ctx, store.CompleteRegistration{TaskID: input.Task.ID, ManifestPath: manifestPath, WorkspacePath: workspacePath, Result: store.TaskResultWrite{Status: domain.TaskCompleted, Summary: input.Summary, AssistantContent: input.Summary, EventKind: "plaintext_ready", RawJSON: input.RawJSON, ExpectedTurnID: input.ExpectedTurnID}, Artifacts: input.Artifacts})
	if err != nil {
		return true, err
	}
	if err := c.enqueue(ctx, attempt); err != nil {
		if failErr := c.repo.Fail(context.Background(), attempt.ID, "registration_enqueue_cancelled", "Registration could not be queued."); failErr != nil {
			c.reportFailure(attempt.TaskID, "registration_enqueue_failure_persist_failed", failErr)
		}
		return true, err
	}
	return true, nil
}

func (c *Coordinator) Retry(ctx context.Context, taskID string) (domain.RegistrationAttempt, error) {
	if c == nil || c.repo == nil {
		return domain.RegistrationAttempt{}, errors.New("montage registration coordinator is not configured")
	}
	attempt, err := c.repo.BeginRetry(ctx, taskID)
	if err != nil {
		return domain.RegistrationAttempt{}, err
	}
	if err := c.enqueue(ctx, attempt); err != nil {
		if failErr := c.repo.Fail(context.Background(), attempt.ID, "registration_enqueue_failed", "Registration could not be queued."); failErr != nil {
			c.reportFailure(attempt.TaskID, "registration_enqueue_failure_persist_failed", failErr)
		}
		return domain.RegistrationAttempt{}, err
	}
	return attempt, nil
}

// Recover performs startup-only registration recovery. It never invokes Codex:
// running attempts become interrupted and durable queued attempts are resubmitted
// only to the trusted host registration worker.
func (c *Coordinator) Recover(ctx context.Context) (domain.RegistrationRecovery, error) {
	if c == nil || c.repo == nil {
		return domain.RegistrationRecovery{}, errors.New("montage registration coordinator is not configured")
	}
	recovery, err := c.repo.RecoverActive(ctx)
	if err != nil {
		return recovery, err
	}
	for _, attempt := range recovery.Queued {
		if err := c.enqueue(ctx, attempt); err != nil {
			c.reportFailure(attempt.TaskID, "registration_recovery_enqueue_failed", err)
			return recovery, err
		}
	}
	return recovery, nil
}

func (c *Coordinator) ReconcileDisplayNames(ctx context.Context) (int, error) {
	if c == nil || c.repo == nil {
		return 0, errors.New("montage registration coordinator is not configured")
	}
	candidates, err := c.repo.DraftsNeedingDisplayName(ctx, c.runtime.JianyingRoot)
	if err != nil {
		return 0, err
	}
	queued := 0
	for _, candidate := range candidates {
		if err := c.enqueueReconciliation(ctx, candidate); err != nil {
			return queued, err
		}
		queued++
	}
	return queued, nil
}

func (c *Coordinator) enqueue(ctx context.Context, attempt domain.RegistrationAttempt) error {
	return c.enqueueJob(ctx, registrationJob{Attempt: attempt})
}

func (c *Coordinator) enqueueReconciliation(ctx context.Context, candidate domain.DraftDisplayReconcileCandidate) error {
	return c.enqueueJob(ctx, registrationJob{Reconcile: &candidate})
}

func (c *Coordinator) enqueueJob(ctx context.Context, job registrationJob) error {
	c.lifecycle.Lock()
	if c.stopped.Load() {
		c.lifecycle.Unlock()
		return ErrCoordinatorStopped
	}
	c.sends.Add(1)
	c.lifecycle.Unlock()
	defer c.sends.Done()
	var workerDone <-chan struct{}
	if c.ctx != nil {
		workerDone = c.ctx.Done()
	}
	timer := time.NewTimer(registrationEnqueueWait)
	defer timer.Stop()
	select {
	case c.queue <- job:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-workerDone:
		return ErrCoordinatorStopped
	case <-timer.C:
		return ErrRegistrationBackpressure
	}
}

func (c *Coordinator) validateRetainedPaths(taskID, manifestPath, workspacePath string) (string, string, error) {
	if c == nil || c.tasks == nil {
		return "", "", store.ErrRegistrationInputInvalid
	}
	var storedManifest string
	if err := c.tasks.DB().QueryRow(`SELECT COALESCE(manifest_path,'') FROM codex_tasks WHERE id=? AND action=?`, taskID, domain.ActionMontageExecute).Scan(&storedManifest); err != nil {
		return "", "", fmt.Errorf("%w: task manifest binding", store.ErrRegistrationInputInvalid)
	}
	manifest, err := canonicalNoFollow(manifestPath, false)
	if err != nil || !samePath(manifest, storedManifest) {
		return "", "", fmt.Errorf("%w: manifest is not the task-bound canonical no-follow file", store.ErrRegistrationInputInvalid)
	}
	data, err := readBounded(manifest, 4<<20)
	if err != nil {
		return "", "", fmt.Errorf("%w: read manifest", store.ErrRegistrationInputInvalid)
	}
	var identity struct {
		TaskID    string `json:"task_id"`
		JobID     string `json:"job_id"`
		OutputDir string `json:"output_dir"`
	}
	if json.Unmarshal(data, &identity) != nil || identity.TaskID != taskID || identity.JobID != taskID {
		return "", "", fmt.Errorf("%w: manifest identity", store.ErrRegistrationInputInvalid)
	}
	output, err := canonicalNoFollow(identity.OutputDir, true)
	if err != nil {
		return "", "", fmt.Errorf("%w: output directory", store.ErrRegistrationInputInvalid)
	}
	workspace, err := canonicalNoFollow(workspacePath, true)
	if err != nil || !samePath(workspace, filepath.Join(output, "workspace", taskID)) {
		return "", "", fmt.Errorf("%w: workspace is not the canonical task workspace", store.ErrRegistrationInputInvalid)
	}
	if !samePath(output, filepath.Join(filepath.Dir(manifest), "output")) {
		return "", "", fmt.Errorf("%w: output directory is outside the task binding", store.ErrRegistrationInputInvalid)
	}
	if check, checkErr := canonicalNoFollow(manifest, false); checkErr != nil || !samePath(check, manifest) {
		return "", "", fmt.Errorf("%w: manifest identity changed", store.ErrRegistrationInputInvalid)
	}
	return manifest, workspace, nil
}

func (c *Coordinator) run() {
	defer c.wg.Done()
	for job := range c.queue {
		if job.Reconcile != nil {
			c.processReconciliation(*job.Reconcile)
		} else {
			c.process(job)
		}
	}
}

func (c *Coordinator) processReconciliation(candidate domain.DraftDisplayReconcileCandidate) {
	attempt := domain.RegistrationAttempt{TaskID: candidate.TaskID, ManifestPath: candidate.ManifestPath, WorkspacePath: candidate.WorkspacePath}
	runtime, err := c.resolveRuntime(attempt)
	if err != nil {
		c.reportFailure(candidate.TaskID, "draft_display_reconciliation_config_invalid", err)
		return
	}
	result, err := c.registrar.Reconcile(c.ctx, ReconcileRequest{
		TaskID: candidate.TaskID, DisplayName: candidate.DisplayName,
		ManifestPath: runtime.ManifestPath, WorkspacePath: runtime.WorkspacePath,
		RegisteredPath: candidate.RegisteredPath, SkillRoot: runtime.SkillRoot,
		ScriptPath: runtime.ScriptPath, PythonBinary: runtime.PythonBinary, JianyingRoot: runtime.JianyingRoot,
	})
	if err != nil {
		c.reportFailure(candidate.TaskID, "draft_display_reconciliation_failed", err)
		return
	}
	if err := c.repo.CompleteDraftDisplayReconcile(c.ctx, domain.DraftDisplayReconcileSuccess{Candidate: candidate, SHA256: result.DirectorySHA256}); err != nil {
		c.reportFailure(candidate.TaskID, "draft_display_reconciliation_commit_failed", err)
	}
}

func (c *Coordinator) process(job registrationJob) {
	ctx := c.ctx
	if err := c.repo.MarkRunning(ctx, job.Attempt.ID); err != nil {
		c.reportFailure(job.Attempt.TaskID, "registration_start_failed", err)
		return
	}
	if _, err := c.verifyWorkspaceDigest(ctx, job.Attempt); err != nil {
		if failErr := c.repo.Fail(context.Background(), job.Attempt.ID, "plaintext_workspace_changed", err.Error()); failErr != nil {
			c.reportFailure(job.Attempt.TaskID, "workspace_digest_failure_persist_failed", failErr)
		}
		return
	}
	result, recovered, err := c.recoverRegisteredDraft(job.Attempt)
	if err != nil {
		if failErr := c.repo.Fail(context.Background(), job.Attempt.ID, "registration_recovery_invalid", err.Error()); failErr != nil {
			c.reportFailure(job.Attempt.TaskID, "registration_failure_persist_failed", failErr)
		}
		return
	}
	if !recovered {
		runtime, resolveErr := c.resolveRuntime(job.Attempt)
		if resolveErr != nil {
			if failErr := c.repo.Fail(context.Background(), job.Attempt.ID, "registration_config_invalid", resolveErr.Error()); failErr != nil {
				c.reportFailure(job.Attempt.TaskID, "registration_failure_persist_failed", failErr)
			}
			return
		}
		result, err = c.registrar.Register(ctx, runtime)
		if err != nil {
			if failErr := c.repo.Fail(context.Background(), job.Attempt.ID, "registration_failed", err.Error()); failErr != nil {
				c.reportFailure(job.Attempt.TaskID, "registration_failure_persist_failed", failErr)
			}
			return
		}
	}
	workspaceDigest, err := c.verifyWorkspaceDigest(ctx, job.Attempt)
	if err != nil {
		if failErr := c.repo.Fail(context.Background(), job.Attempt.ID, "plaintext_workspace_changed_during_registration", err.Error()); failErr != nil {
			c.reportFailure(job.Attempt.TaskID, "workspace_digest_failure_persist_failed", failErr)
		}
		return
	}
	if err := c.repo.Succeed(ctx, store.RegistrationSuccess{AttemptID: job.Attempt.ID, RegisteredPath: result.RegisteredPath, ReceiptPath: result.ReceiptPath, SHA256: result.DirectorySHA256, WorkspaceSHA256: workspaceDigest, Filename: result.DisplayName}); err != nil {
		if failErr := c.repo.FailCommit(context.Background(), job.Attempt.ID, err.Error()); failErr != nil {
			c.reportFailure(job.Attempt.TaskID, "registration_commit_reconcile_failed", failErr)
		}
	}
}

// recoverRegisteredDraft validates a registration already published by an
// earlier attempt. Recovery deliberately runs before task-bound script
// resolution: no executable is needed when the authoritative receipt,
// registered directory, content fingerprints, draft IDs, and Jianying index
// already prove that the shared-state mutation completed successfully.
func (c *Coordinator) recoverRegisteredDraft(attempt domain.RegistrationAttempt) (RegisterResult, bool, error) {
	if c == nil || strings.TrimSpace(attempt.TaskID) == "" || strings.TrimSpace(attempt.WorkspacePath) == "" || strings.TrimSpace(c.runtime.JianyingRoot) == "" {
		return RegisterResult{}, false, nil
	}
	workspace, err := canonicalNoFollow(attempt.WorkspacePath, true)
	if err != nil {
		return RegisterResult{}, false, invalidRegistration("workspace is unavailable")
	}
	root, err := canonicalNoFollow(c.runtime.JianyingRoot, true)
	if err != nil {
		return RegisterResult{}, false, invalidRegistration("Jianying root is unavailable")
	}
	receiptPath := filepath.Join(filepath.Dir(filepath.Dir(workspace)), "registration", "registration-result.json")
	targetPath := filepath.Join(root, attempt.TaskID)
	receiptExists, err := retainedPathExists(receiptPath)
	if err != nil {
		return RegisterResult{}, false, invalidRegistration("existing registration receipt could not be inspected")
	}
	targetExists, err := retainedPathExists(targetPath)
	if err != nil {
		return RegisterResult{}, false, invalidRegistration("existing registered draft could not be inspected")
	}
	if !receiptExists && !targetExists {
		return RegisterResult{}, false, nil
	}
	if !receiptExists || !targetExists {
		return RegisterResult{}, false, invalidRegistration("existing registration is incomplete")
	}
	displayName, err := frozenDraftDisplayName(attempt.ManifestPath, attempt.TaskID)
	if err != nil {
		return RegisterResult{}, false, err
	}
	result, err := ValidateRegisteredDraft(ValidationRequest{
		TaskID:        attempt.TaskID,
		DisplayName:   displayName,
		WorkspacePath: workspace,
		ReceiptPath:   receiptPath,
		JianyingRoot:  root,
	})
	if err != nil {
		return RegisterResult{}, false, err
	}
	return result, true, nil
}

func (c *Coordinator) verifyWorkspaceDigest(ctx context.Context, attempt domain.RegistrationAttempt) (string, error) {
	artifact, err := c.repo.PlaintextWorkspaceArtifact(ctx, attempt.ID)
	if err != nil {
		return "", fmt.Errorf("load durable plaintext workspace identity: %w", err)
	}
	workspace, err := canonicalNoFollow(attempt.WorkspacePath, true)
	if err != nil || !samePath(workspace, artifact.Path) {
		return "", errors.New("plaintext workspace path no longer matches its durable artifact")
	}
	digest, err := hashPlaintextWorkspace(ctx, workspace)
	if err != nil {
		return "", fmt.Errorf("hash plaintext workspace: %w", err)
	}
	if !strings.EqualFold(digest, artifact.SHA256) {
		return "", errors.New("plaintext workspace digest no longer matches its durable artifact")
	}
	return strings.ToLower(digest), nil
}

func (c *Coordinator) reportFailure(taskID, code string, err error) {
	if c == nil || strings.TrimSpace(taskID) == "" || err == nil {
		return
	}
	log.Printf("montage registration warning task=%s code=%s: %v", taskID, code, err)
	if c.tasks != nil {
		if eventErr := c.tasks.AppendEvent(context.Background(), taskID, domain.TaskEvent{Kind: code, Level: "warning", DisplayText: err.Error()}); eventErr != nil {
			log.Printf("montage registration warning event failed task=%s code=%s: %v", taskID, code, eventErr)
		}
	}
}

func (c *Coordinator) resolveRuntime(attempt domain.RegistrationAttempt) (RegisterRequest, error) {
	manifestPath, workspacePath, err := c.validateRetainedPaths(attempt.TaskID, attempt.ManifestPath, attempt.WorkspacePath)
	if err != nil {
		return RegisterRequest{}, err
	}
	var skillRoot, skillFilesJSON string
	err = c.tasks.DB().QueryRow(`SELECT snapshot.path,snapshot.files_json FROM codex_tasks task JOIN skill_snapshots snapshot ON snapshot.id=task.skill_snapshot_id WHERE task.id=? AND task.manifest_path=? AND snapshot.name='jianying-montage-draft'`, attempt.TaskID, manifestPath).Scan(&skillRoot, &skillFilesJSON)
	if err != nil {
		return RegisterRequest{}, fmt.Errorf("resolve montage Skill snapshot: %w", err)
	}
	skillRoot, err = canonicalNoFollow(skillRoot, true)
	if err != nil {
		return RegisterRequest{}, errors.New("task-bound Skill snapshot is not a canonical no-follow directory")
	}
	scriptPath, err := canonicalNoFollow(filepath.Join(skillRoot, "scripts", "run_montage_job.py"), false)
	if err != nil || !WithinJianyingRoot(skillRoot, scriptPath) {
		return RegisterRequest{}, errors.New("task-bound registration script is unavailable")
	}
	var skillFiles []domain.SkillFileSnapshot
	if json.Unmarshal([]byte(skillFilesJSON), &skillFiles) != nil {
		return RegisterRequest{}, errors.New("task-bound Skill file identity is invalid")
	}
	expectedScriptHash := ""
	for _, file := range skillFiles {
		if filepath.ToSlash(file.Path) == "scripts/run_montage_job.py" {
			expectedScriptHash = file.SHA256
			break
		}
	}
	actualScriptHash, hashErr := hashFile(scriptPath)
	if hashErr != nil || expectedScriptHash == "" || !strings.EqualFold(actualScriptHash, expectedScriptHash) {
		return RegisterRequest{}, errors.New("task-bound registration script fingerprint changed")
	}
	manifestData, err := readBounded(manifestPath, 4<<20)
	if err != nil {
		return RegisterRequest{}, err
	}
	var manifest struct {
		TaskID            string `json:"task_id"`
		JobID             string `json:"job_id"`
		NonSecretSettings struct {
			MachineProfilePath string          `json:"machine_profile_path"`
			DraftDisplayName   json.RawMessage `json:"draft_display_name"`
		} `json:"non_secret_settings"`
	}
	if json.Unmarshal(manifestData, &manifest) != nil || manifest.TaskID != attempt.TaskID || manifest.JobID != attempt.TaskID || strings.TrimSpace(manifest.NonSecretSettings.MachineProfilePath) == "" {
		return RegisterRequest{}, errors.New("manifest task identity or machine profile is invalid")
	}
	displayName, err := decodeFrozenDraftDisplayName(manifest.NonSecretSettings.DraftDisplayName, attempt.TaskID)
	if err != nil {
		return RegisterRequest{}, err
	}
	profilePath, err := canonicalNoFollow(manifest.NonSecretSettings.MachineProfilePath, false)
	if err != nil || !samePath(profilePath, c.runtime.MachineProfilePath) {
		return RegisterRequest{}, errors.New("manifest machine profile does not match trusted runtime configuration")
	}
	profileHash, err := hashFile(profilePath)
	if err != nil || !strings.EqualFold(profileHash, c.runtime.MachineProfileSHA256) {
		return RegisterRequest{}, errors.New("trusted machine profile fingerprint changed")
	}
	pythonBinary, err := canonicalNoFollow(c.runtime.PythonBinary, false)
	if err != nil || !samePath(pythonBinary, c.runtime.PythonBinary) {
		return RegisterRequest{}, errors.New("trusted Python executable identity changed")
	}
	jianyingRoot, err := canonicalNoFollow(c.runtime.JianyingRoot, true)
	if err != nil || !samePath(jianyingRoot, c.runtime.JianyingRoot) {
		return RegisterRequest{}, errors.New("trusted Jianying root identity changed")
	}
	return RegisterRequest{TaskID: attempt.TaskID, DisplayName: displayName, ManifestPath: manifestPath, WorkspacePath: workspacePath, SkillRoot: skillRoot, ScriptPath: scriptPath, PythonBinary: pythonBinary, JianyingRoot: jianyingRoot}, nil
}

func frozenDraftDisplayName(manifestPath, taskID string) (string, error) {
	manifest, err := canonicalNoFollow(manifestPath, false)
	if err != nil {
		return "", invalidRegistration("task manifest is unavailable")
	}
	data, err := readBounded(manifest, 4<<20)
	if err != nil {
		return "", invalidRegistration("task manifest could not be read")
	}
	var identity struct {
		TaskID            string `json:"task_id"`
		JobID             string `json:"job_id"`
		NonSecretSettings struct {
			DraftDisplayName json.RawMessage `json:"draft_display_name"`
		} `json:"non_secret_settings"`
	}
	if json.Unmarshal(data, &identity) != nil || identity.TaskID != taskID || identity.JobID != taskID {
		return "", invalidRegistration("task manifest display identity is invalid")
	}
	return decodeFrozenDraftDisplayName(identity.NonSecretSettings.DraftDisplayName, taskID)
}

func decodeFrozenDraftDisplayName(raw json.RawMessage, taskID string) (string, error) {
	if len(raw) == 0 {
		return taskID, nil
	}
	var displayName string
	if json.Unmarshal(raw, &displayName) != nil || strings.TrimSpace(displayName) == "" || displayName != strings.TrimSpace(displayName) || utf8.RuneCountInString(displayName) > 68 || strings.ContainsAny(displayName, `<>:"/\|?*`) || strings.HasSuffix(displayName, ".") {
		return "", invalidRegistration("task manifest display identity is invalid")
	}
	for _, character := range displayName {
		if character < 0x20 {
			return "", invalidRegistration("task manifest display identity is invalid")
		}
	}
	return displayName, nil
}

func trustedPythonBinary(value string) (string, error) {
	value = strings.TrimSpace(value)
	if !filepath.IsAbs(value) {
		return "", errors.New("trusted Python executable must be absolute")
	}
	canonical, err := canonicalNoFollow(value, false)
	if err != nil {
		return "", errors.New("trusted Python executable is not a canonical no-follow file")
	}
	return canonical, nil
}

func (c *Coordinator) AuditMixDrafts(ctx context.Context, trustedJianyingRoot string) (domain.MixDraftAudit, error) {
	if c == nil || c.repo == nil {
		return domain.MixDraftAudit{}, errors.New("montage registration coordinator is not configured")
	}
	return c.repo.AuditMixDrafts(ctx, trustedJianyingRoot)
}

func (c *Coordinator) Close() {
	if c == nil {
		return
	}
	c.once.Do(func() {
		c.lifecycle.Lock()
		c.stopped.Store(true)
		if c.cancel != nil {
			c.cancel()
		}
		c.lifecycle.Unlock()
		c.sends.Wait()
		close(c.queue)
		c.wg.Wait()
	})
}

var _ taskcompletion.Gate = (*Coordinator)(nil)
