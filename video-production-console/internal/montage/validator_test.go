package montage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"video-production-console/internal/domain"
)

type registrationFixture struct {
	request      ValidationRequest
	receiptPath  string
	workspace    string
	registered   string
	root         string
	sourceID     string
	registeredID string
}

func newRegistrationFixture(t *testing.T, explicitRekeyFields bool, rekeyed bool) registrationFixture {
	t.Helper()
	base := t.TempDir()
	taskID := "task-registration-recovery"
	output := filepath.Join(base, "task-output")
	workspace := filepath.Join(output, "workspace", taskID)
	root := filepath.Join(base, "jianying")
	registered := filepath.Join(root, taskID)
	for _, directory := range []string{workspace, registered, filepath.Join(output, "registration")} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	sourceID := "template-draft-id"
	registeredID := sourceID
	if rekeyed {
		registeredID = "registered-draft-id"
	}
	content := []byte(`{"duration":1000000,"tracks":[]}`)
	for _, directory := range []string{workspace, registered} {
		if err := os.WriteFile(filepath.Join(directory, "draft_content.json"), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeJSONFixture(t, filepath.Join(workspace, "draft_meta_info.json"), map[string]any{"draft_id": sourceID})
	writeJSONFixture(t, filepath.Join(registered, "draft_meta_info.json"), map[string]any{"draft_id": registeredID})
	writeJSONFixture(t, filepath.Join(root, "root_meta_info.json"), map[string]any{
		"all_draft_store": []map[string]any{{"draft_id": registeredID, "draft_fold_path": registered}},
	})

	digestBytes := sha256.Sum256(content)
	digest := hex.EncodeToString(digestBytes[:])
	receipt := map[string]any{
		"status":                    "completed",
		"registered_path":           registered,
		"draft_id":                  registeredID,
		"duration_us":               1000000,
		"source_content_sha256":     digest,
		"registered_content_sha256": digest,
	}
	if explicitRekeyFields {
		receipt["source_draft_id"] = sourceID
		receipt["draft_id_rekeyed"] = rekeyed
	}
	receiptPath := filepath.Join(output, "registration", "registration-result.json")
	writeJSONFixture(t, receiptPath, receipt)

	return registrationFixture{
		request: ValidationRequest{
			TaskID:        taskID,
			WorkspacePath: workspace,
			ReceiptPath:   receiptPath,
			JianyingRoot:  root,
		},
		receiptPath:  receiptPath,
		workspace:    workspace,
		registered:   registered,
		root:         root,
		sourceID:     sourceID,
		registeredID: registeredID,
	}
}

func writeJSONFixture(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRegisteredDraftAcceptsExplicitControlledDraftIDRekey(t *testing.T) {
	fixture := newRegistrationFixture(t, true, true)
	result, err := ValidateRegisteredDraft(fixture.request)
	if err != nil {
		t.Fatalf("controlled draft ID rekey was rejected: %v", err)
	}
	if result.DraftID != fixture.registeredID {
		t.Fatalf("draft ID=%q, want %q", result.DraftID, fixture.registeredID)
	}
}

func TestValidateRegisteredDraftRecoversLegacyControlledDraftIDRekey(t *testing.T) {
	fixture := newRegistrationFixture(t, false, true)
	result, err := ValidateRegisteredDraft(fixture.request)
	if err != nil {
		t.Fatalf("legacy controlled draft ID rekey was rejected: %v", err)
	}
	if result.DraftID != fixture.registeredID {
		t.Fatalf("draft ID=%q, want %q", result.DraftID, fixture.registeredID)
	}
}

func TestValidateRegisteredDraftRejectsUnacknowledgedDraftIDRekey(t *testing.T) {
	fixture := newRegistrationFixture(t, true, true)
	var receipt map[string]any
	data, err := os.ReadFile(fixture.receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &receipt); err != nil {
		t.Fatal(err)
	}
	receipt["draft_id_rekeyed"] = false
	writeJSONFixture(t, fixture.receiptPath, receipt)
	if _, err := ValidateRegisteredDraft(fixture.request); !errors.Is(err, ErrInvalidRegistration) {
		t.Fatalf("unacknowledged rekey error=%v, want ErrInvalidRegistration", err)
	}
}

type countingCommandRunner struct{ calls int }

func (runner *countingCommandRunner) Run(context.Context, CommandSpec, int64) (CommandResult, error) {
	runner.calls++
	return CommandResult{}, errors.New("registration command must not run during recovery")
}

type commandRunnerFunc func(context.Context, CommandSpec, int64) (CommandResult, error)

func (run commandRunnerFunc) Run(ctx context.Context, spec CommandSpec, limit int64) (CommandResult, error) {
	return run(ctx, spec, limit)
}

func TestRegistrarValidatesAuthoritativeReceiptWhenReportedPathIsCorrupted(t *testing.T) {
	fixture := newRegistrationFixture(t, true, true)
	receipt, err := os.ReadFile(fixture.receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	registeredContent, err := os.ReadFile(filepath.Join(fixture.registered, "draft_content.json"))
	if err != nil {
		t.Fatal(err)
	}
	registeredMeta, err := os.ReadFile(filepath.Join(fixture.registered, "draft_meta_info.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(fixture.receiptPath); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(fixture.registered); err != nil {
		t.Fatal(err)
	}

	base := t.TempDir()
	manifest := filepath.Join(base, "task_manifest.json")
	skillRoot := filepath.Join(base, "skill")
	script := filepath.Join(skillRoot, "scripts", "run_montage_job.py")
	python := filepath.Join(base, "python.exe")
	if err := os.MkdirAll(filepath.Dir(script), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{manifest, script, python} {
		if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runner := commandRunnerFunc(func(context.Context, CommandSpec, int64) (CommandResult, error) {
		if err := os.MkdirAll(fixture.registered, 0o700); err != nil {
			return CommandResult{}, err
		}
		if err := os.WriteFile(filepath.Join(fixture.registered, "draft_content.json"), registeredContent, 0o600); err != nil {
			return CommandResult{}, err
		}
		if err := os.WriteFile(filepath.Join(fixture.registered, "draft_meta_info.json"), registeredMeta, 0o600); err != nil {
			return CommandResult{}, err
		}
		if err := os.WriteFile(fixture.receiptPath, receipt, 0o600); err != nil {
			return CommandResult{}, err
		}
		envelope := `{"status":"completed","artifacts":[{"type":"registration_result","path":"C:\\\\corrupted-chinese-path\\\\registration-result.json"}]}`
		return CommandResult{Stdout: []byte(envelope), ExitCode: 0}, nil
	})

	result, err := NewRegistrar(runner).Register(context.Background(), RegisterRequest{
		TaskID:        fixture.request.TaskID,
		ManifestPath:  manifest,
		WorkspacePath: fixture.workspace,
		SkillRoot:     skillRoot,
		ScriptPath:    script,
		PythonBinary:  python,
		JianyingRoot:  fixture.root,
	})
	if err != nil {
		t.Fatalf("validate authoritative registration receipt: %v", err)
	}
	if result.ReceiptPath != fixture.receiptPath {
		t.Fatalf("receipt path=%q, want authoritative path %q", result.ReceiptPath, fixture.receiptPath)
	}
}

func TestRegistrarReusesExistingValidatedRegistrationWithoutRunningCommand(t *testing.T) {
	fixture := newRegistrationFixture(t, false, true)
	base := t.TempDir()
	manifest := filepath.Join(base, "task_manifest.json")
	skillRoot := filepath.Join(base, "skill")
	script := filepath.Join(skillRoot, "scripts", "run_montage_job.py")
	python := filepath.Join(base, "python.exe")
	if err := os.MkdirAll(filepath.Dir(script), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{manifest, script, python} {
		if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runner := &countingCommandRunner{}
	registrar := NewRegistrar(runner)
	result, err := registrar.Register(context.Background(), RegisterRequest{
		TaskID:        fixture.request.TaskID,
		ManifestPath:  manifest,
		WorkspacePath: fixture.workspace,
		SkillRoot:     skillRoot,
		ScriptPath:    script,
		PythonBinary:  python,
		JianyingRoot:  fixture.root,
	})
	if err != nil {
		t.Fatalf("recover existing registration: %v", err)
	}
	if runner.calls != 0 {
		t.Fatalf("registration command calls=%d, want 0", runner.calls)
	}
	if result.RegisteredPath != fixture.registered {
		t.Fatalf("registered path=%q, want %q", result.RegisteredPath, fixture.registered)
	}
}

func TestCoordinatorRecoversExistingRegistrationBeforeResolvingTaskBoundScript(t *testing.T) {
	fixture := newRegistrationFixture(t, false, true)
	coordinator := &Coordinator{runtime: TrustedRuntime{JianyingRoot: fixture.root}}
	result, recovered, err := coordinator.recoverRegisteredDraft(domain.RegistrationAttempt{
		TaskID:        fixture.request.TaskID,
		WorkspacePath: fixture.workspace,
	})
	if err != nil {
		t.Fatalf("recover registered draft: %v", err)
	}
	if !recovered {
		t.Fatal("existing complete registration was not recovered")
	}
	if result.RegisteredPath != fixture.registered {
		t.Fatalf("registered path=%q, want %q", result.RegisteredPath, fixture.registered)
	}
}
