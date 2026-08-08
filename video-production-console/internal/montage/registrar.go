package montage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var ErrInvalidRegistration = errors.New("invalid montage registration")

type ReconcileBusyError struct {
	RetryAfter time.Duration
	OwnerJobID string
}

func (err *ReconcileBusyError) Error() string {
	if err == nil {
		return "Jianying reconciliation lock is busy"
	}
	return fmt.Sprintf("Jianying reconciliation lock is busy; retry after %s (owner %s)", err.RetryAfter, err.OwnerJobID)
}

type CommandSpec struct {
	Program string
	Args    []string
	Dir     string
}
type CommandResult struct {
	Stdout, Stderr []byte
	ExitCode       int
}
type CommandRunner interface {
	Run(context.Context, CommandSpec, int64) (CommandResult, error)
}
type RegisterRequest struct{ TaskID, DisplayName, ManifestPath, WorkspacePath, SkillRoot, ScriptPath, PythonBinary, JianyingRoot string }
type RegisterResult struct {
	RegisteredPath, ReceiptPath, DraftID, DisplayName, SourceContentSHA256, RegisteredContentSHA256, DirectorySHA256 string
	DurationUS                                                                                                       int64
}
type ReconcileRequest struct {
	TaskID, DisplayName, ManifestPath, WorkspacePath, RegisteredPath, SkillRoot, ScriptPath, PythonBinary, JianyingRoot string
	ExpectedDirectorySHA256                                                                                             string
}
type Registrar struct{ runner CommandRunner }

func NewRegistrar(runner CommandRunner) *Registrar { return &Registrar{runner: runner} }

func (r *Registrar) Register(ctx context.Context, request RegisterRequest) (RegisterResult, error) {
	if r == nil || r.runner == nil {
		return RegisterResult{}, errors.New("montage registrar is not configured")
	}
	if request.TaskID == "" || request.DisplayName == "" || request.ManifestPath == "" || request.WorkspacePath == "" || request.SkillRoot == "" || request.ScriptPath == "" || request.PythonBinary == "" || request.JianyingRoot == "" {
		return RegisterResult{}, fmt.Errorf("%w: missing registration input", ErrInvalidRegistration)
	}
	manifest, manifestErr := canonicalNoFollow(request.ManifestPath, false)
	workspace, workspaceErr := canonicalNoFollow(request.WorkspacePath, true)
	skillRoot, skillErr := canonicalNoFollow(request.SkillRoot, true)
	script, scriptErr := canonicalNoFollow(request.ScriptPath, false)
	root, rootErr := canonicalNoFollow(request.JianyingRoot, true)
	python, pythonErr := trustedPythonBinary(request.PythonBinary)
	if manifestErr != nil || workspaceErr != nil || skillErr != nil || scriptErr != nil || rootErr != nil || pythonErr != nil || !samePath(manifest, request.ManifestPath) || !samePath(workspace, request.WorkspacePath) || !samePath(skillRoot, request.SkillRoot) || !samePath(script, filepath.Join(skillRoot, "scripts", "run_montage_job.py")) || !samePath(root, request.JianyingRoot) {
		return RegisterResult{}, fmt.Errorf("%w: registration script is unavailable", ErrInvalidRegistration)
	}
	receiptPath := filepath.Join(filepath.Dir(filepath.Dir(workspace)), "registration", "registration-result.json")
	targetPath := filepath.Join(root, request.TaskID)
	receiptExists, err := retainedPathExists(receiptPath)
	if err != nil {
		return RegisterResult{}, fmt.Errorf("%w: existing registration receipt could not be inspected", ErrInvalidRegistration)
	}
	targetExists, err := retainedPathExists(targetPath)
	if err != nil {
		return RegisterResult{}, fmt.Errorf("%w: existing registered draft could not be inspected", ErrInvalidRegistration)
	}
	if receiptExists || targetExists {
		if !receiptExists || !targetExists {
			return RegisterResult{}, fmt.Errorf("%w: existing registration is incomplete", ErrInvalidRegistration)
		}
		return ValidateRegisteredDraft(ValidationRequest{TaskID: request.TaskID, DisplayName: request.DisplayName, WorkspacePath: workspace, ReceiptPath: receiptPath, JianyingRoot: root})
	}
	command, err := r.runner.Run(ctx, CommandSpec{Program: python, Args: []string{script, "register", "--manifest", manifest, "--draft", workspace}, Dir: skillRoot}, 1<<20)
	if err != nil {
		return RegisterResult{}, err
	}
	envelope, parseErr := parseRegistrationEnvelope(command.Stdout)
	if parseErr != nil {
		return RegisterResult{}, parseErr
	}
	if command.ExitCode != 0 || envelope.Status != "completed" {
		message := strings.TrimSpace(envelope.Summary)
		if message == "" {
			message = "registration command failed"
		}
		return RegisterResult{}, fmt.Errorf("%w: %s", ErrInvalidRegistration, message)
	}
	receiptReported := false
	for _, artifact := range envelope.Artifacts {
		if artifact.Type == "registration_result" {
			receiptReported = true
			break
		}
	}
	if !receiptReported {
		return RegisterResult{}, fmt.Errorf("%w: registration receipt was not reported", ErrInvalidRegistration)
	}
	// The registration subprocess is required to report that it produced a
	// receipt, but the subprocess-provided path is not authoritative. On
	// Windows, stdout encoding can corrupt non-ASCII path components even when
	// the receipt was written successfully. Validate the task-bound location
	// derived before execution instead; all receipt contents, fingerprints,
	// registered paths, and Jianying index entries remain strictly verified.
	return ValidateRegisteredDraft(ValidationRequest{TaskID: request.TaskID, DisplayName: request.DisplayName, WorkspacePath: workspace, ReceiptPath: receiptPath, JianyingRoot: root})
}

func (r *Registrar) Reconcile(ctx context.Context, request ReconcileRequest) (ReconcileResult, error) {
	if r == nil || r.runner == nil || request.TaskID == "" || request.DisplayName == "" || request.ManifestPath == "" || request.WorkspacePath == "" || request.RegisteredPath == "" || request.SkillRoot == "" || request.ScriptPath == "" || request.PythonBinary == "" || request.JianyingRoot == "" || !validHash(request.ExpectedDirectorySHA256) {
		return ReconcileResult{}, fmt.Errorf("%w: missing reconciliation input", ErrInvalidRegistration)
	}
	manifest, manifestErr := canonicalNoFollow(request.ManifestPath, false)
	workspace, workspaceErr := canonicalNoFollow(request.WorkspacePath, true)
	registered, registeredErr := canonicalNoFollow(request.RegisteredPath, true)
	skillRoot, skillErr := canonicalNoFollow(request.SkillRoot, true)
	script, scriptErr := canonicalNoFollow(request.ScriptPath, false)
	root, rootErr := canonicalNoFollow(request.JianyingRoot, true)
	python, pythonErr := trustedPythonBinary(request.PythonBinary)
	if manifestErr != nil || workspaceErr != nil || registeredErr != nil || skillErr != nil || scriptErr != nil || rootErr != nil || pythonErr != nil || !samePath(script, filepath.Join(skillRoot, "scripts", "run_montage_job.py")) || !samePath(registered, filepath.Join(root, request.TaskID)) {
		return ReconcileResult{}, fmt.Errorf("%w: reconciliation runtime is unavailable", ErrInvalidRegistration)
	}
	receiptPath := filepath.Join(filepath.Dir(filepath.Dir(workspace)), "registration", "reconciliation-result.json")
	validation := ReconcileValidationRequest{TaskID: request.TaskID, DisplayName: request.DisplayName, WorkspacePath: workspace, RegisteredPath: registered, ReceiptPath: receiptPath, JianyingRoot: root, ExpectedDirectorySHA256: request.ExpectedDirectorySHA256}
	if exists, err := retainedPathExists(receiptPath); err != nil {
		return ReconcileResult{}, err
	} else if exists {
		return ValidateReconciledDraft(validation)
	}
	command, err := r.runner.Run(ctx, CommandSpec{Program: python, Args: []string{script, "reconcile-name", "--manifest", manifest, "--draft", registered, "--expected-directory-sha256", strings.ToLower(request.ExpectedDirectorySHA256)}, Dir: skillRoot}, 1<<20)
	if err != nil {
		return ReconcileResult{}, err
	}
	envelope, err := parseRegistrationEnvelope(command.Stdout)
	if err != nil {
		return ReconcileResult{}, err
	}
	if envelope.Status == "awaiting_input" && envelope.Retry.AfterSeconds > 0 {
		return ReconcileResult{}, &ReconcileBusyError{RetryAfter: time.Duration(envelope.Retry.AfterSeconds) * time.Second, OwnerJobID: strings.TrimSpace(envelope.Retry.OwnerJobID)}
	}
	if command.ExitCode != 0 || envelope.Status != "completed" {
		return ReconcileResult{}, fmt.Errorf("%w: %s", ErrInvalidRegistration, strings.TrimSpace(envelope.Summary))
	}
	reported := false
	for _, artifact := range envelope.Artifacts {
		if artifact.Type == "reconciliation_result" {
			reported = true
		}
	}
	if !reported {
		return ReconcileResult{}, fmt.Errorf("%w: reconciliation receipt was not reported", ErrInvalidRegistration)
	}
	return ValidateReconciledDraft(validation)
}

func retainedPathExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

type registrationEnvelope struct {
	Status    string `json:"status"`
	Summary   string `json:"summary"`
	Artifacts []struct {
		Type string `json:"type"`
		Path string `json:"path"`
	} `json:"artifacts"`
	Retry struct {
		AfterSeconds int    `json:"after_seconds"`
		OwnerJobID   string `json:"owner_job_id"`
	} `json:"retry"`
}

func parseRegistrationEnvelope(raw []byte) (registrationEnvelope, error) {
	if len(raw) == 0 || len(raw) > 1<<20 {
		return registrationEnvelope{}, fmt.Errorf("%w: invalid registration response size", ErrInvalidRegistration)
	}
	var envelope registrationEnvelope
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	if err := decoder.Decode(&envelope); err != nil {
		return envelope, fmt.Errorf("%w: malformed registration response", ErrInvalidRegistration)
	}
	return envelope, nil
}

func WithinJianyingRoot(root, target string) bool {
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	rel, err := filepath.Rel(root, target)
	return err == nil && rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
