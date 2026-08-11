package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/agentruntime"
	"video-production-console/internal/codex"
	"video-production-console/internal/config"
	"video-production-console/internal/domain"
	"video-production-console/internal/security"
	"video-production-console/internal/store"
	"video-production-console/internal/taskcompletion"
)

func TestNewServerHasDefensiveTimeouts(t *testing.T) {
	server := newServer("127.0.0.1:2030", http.NewServeMux())
	if server.ReadHeaderTimeout != 10*time.Second || server.ReadTimeout != 2*time.Minute ||
		server.WriteTimeout != 2*time.Minute || server.IdleTimeout != time.Minute || server.MaxHeaderBytes != 1<<20 {
		t.Fatalf("server limits = header:%v read:%v write:%v idle:%v max-header:%d",
			server.ReadHeaderTimeout, server.ReadTimeout, server.WriteTimeout, server.IdleTimeout, server.MaxHeaderBytes)
	}
}

func TestServeUntilShutdownStopsOnCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	server := newServer("127.0.0.1:0", http.NewServeMux())
	if err := serveUntilShutdown(ctx, server); err != nil {
		t.Fatalf("serveUntilShutdown: %v", err)
	}
}

func TestInitializeAdministratorSkipsBootstrapForExistingAdmin(t *testing.T) {
	err := initializeAdministrator(context.Background(), func(context.Context) (store.Admin, error) {
		return store.Admin{ID: "admin"}, nil
	}, func(context.Context, string) error {
		t.Fatal("bootstrap called")
		return nil
	}, func(string) (string, bool) {
		t.Fatal("environment read")
		return "", false
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestInitializeAdministratorRequiresExplicitPassword(t *testing.T) {
	err := initializeAdministrator(context.Background(), func(context.Context) (store.Admin, error) {
		return store.Admin{}, store.ErrUnauthenticated
	}, func(context.Context, string) error {
		t.Fatal("bootstrap called without password")
		return nil
	}, func(key string) (string, bool) {
		if key != initialPasswordEnvironment {
			t.Fatalf("lookup key=%q", key)
		}
		return "", false
	})
	if err == nil || !strings.Contains(err.Error(), initialPasswordEnvironment) {
		t.Fatalf("err=%v", err)
	}
}

func TestDefaultCodexTaskProjectRootUsesDocumentsMisc(t *testing.T) {
	home := filepath.Join(t.TempDir(), "user")
	want := filepath.Join(home, "Documents", "杂项")
	if err := os.MkdirAll(want, 0o700); err != nil {
		t.Fatal(err)
	}
	got := defaultCodexTaskProjectRoot(home)
	if got != want {
		t.Fatalf("task project root=%q want=%q", got, want)
	}
}

func TestDefaultCodexTaskProjectRootDoesNotFallbackWhenUnavailable(t *testing.T) {
	home := filepath.Join(t.TempDir(), "user")
	if got := defaultCodexTaskProjectRoot(home); got != "" {
		t.Fatalf("task project root=%q, want unavailable root to remain empty", got)
	}
}

func TestResolveBootDataRootIsStableForDistExecutable(t *testing.T) {
	installRoot := t.TempDir()
	executablePath := filepath.Join(installRoot, "dist", "video-production-console.exe")
	workingDirectory := filepath.Join(installRoot, "dist")

	got := resolveBootDataRoot("./video-console-data", executablePath, workingDirectory)
	want := filepath.Join(installRoot, "video-console-data")
	if got != want {
		t.Fatalf("data root = %q, want stable install root %q", got, want)
	}
}

func TestResolveResultSchemaPrefersControlledInstallRoot(t *testing.T) {
	installRoot := t.TempDir()
	executableDir := filepath.Join(installRoot, "bin")
	if err := os.Mkdir(executableDir, 0o700); err != nil {
		t.Fatal(err)
	}
	executablePath := filepath.Join(executableDir, "video-console.exe")
	if err := os.WriteFile(executablePath, []byte("exe"), 0o600); err != nil {
		t.Fatal(err)
	}
	installSchema := writeSchemaFixture(t, installRoot)
	developmentRoot := t.TempDir()
	_ = writeSchemaFixture(t, developmentRoot)

	got, err := resolveResultSchemaPath(executablePath, developmentRoot)
	if err != nil {
		t.Fatalf("resolveResultSchemaPath returned error: %v", err)
	}
	if got != installSchema {
		t.Fatalf("schema = %q, want install-root schema %q", got, installSchema)
	}
}

func TestResolveResultSchemaUsesOnlyExplicitDevelopmentFallback(t *testing.T) {
	executableRoot := t.TempDir()
	executablePath := filepath.Join(executableRoot, "video-console.exe")
	if err := os.WriteFile(executablePath, []byte("exe"), 0o600); err != nil {
		t.Fatal(err)
	}
	developmentRoot := t.TempDir()
	developmentSchema := writeSchemaFixture(t, developmentRoot)
	ambientRoot := t.TempDir()
	_ = writeSchemaFixture(t, ambientRoot)
	originalDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(ambientRoot); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalDirectory) })

	got, err := resolveResultSchemaPath(executablePath, developmentRoot)
	if err != nil {
		t.Fatalf("resolveResultSchemaPath returned error: %v", err)
	}
	if got != developmentSchema {
		t.Fatalf("schema = %q, want explicit development fallback %q", got, developmentSchema)
	}
}

func TestResolveResultSchemaRejectsMissingOrNonRegularFile(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		executableRoot := t.TempDir()
		executablePath := filepath.Join(executableRoot, "video-console.exe")
		if err := os.WriteFile(executablePath, []byte("exe"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := resolveResultSchemaPath(executablePath, ""); err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("expected missing schema to fail fast, got %v", err)
		}
	})
	t.Run("non-regular", func(t *testing.T) {
		executableRoot := t.TempDir()
		executablePath := filepath.Join(executableRoot, "video-console.exe")
		if err := os.WriteFile(executablePath, []byte("exe"), 0o600); err != nil {
			t.Fatal(err)
		}
		schemaDirectory := filepath.Join(executableRoot, "schemas", "codex-result.schema.json")
		if err := os.MkdirAll(schemaDirectory, 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := resolveResultSchemaPath(executablePath, ""); err == nil || !strings.Contains(err.Error(), "regular file") {
			t.Fatalf("expected non-regular schema to fail fast, got %v", err)
		}
	})
}

func writeSchemaFixture(t *testing.T, root string) string {
	t.Helper()
	schemaDirectory := filepath.Join(root, "schemas")
	if err := os.MkdirAll(schemaDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(schemaDirectory, "codex-result.schema.json")
	if err := os.WriteFile(path, []byte(`{"type":"object"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func TestCodexCommandFactoriesUseUnifiedBuilders(t *testing.T) {
	dataRoot := t.TempDir()
	schema := filepath.Join(t.TempDir(), "codex-result.schema.json")
	base := testCodexCommandConfig(t, schema)
	base.SecretEnvironment = map[string]string{"GROK_SEARCH_API_KEY": "secret"}
	makeCommand, makeResume := newCodexCommandFactories(config.Config{DataRoot: dataRoot}, base, nil)
	projectID := "project-1"
	task := domain.CodexTask{ID: "task-1", ProjectID: &projectID, Type: "topic_select", PromptSnapshot: "stored prompt", ModelName: "openai/custom", ReasoningEffort: "high"}

	cmd, root, err := makeCommand(task)
	if err != nil {
		t.Fatalf("makeCommand returned error: %v", err)
	}
	wantRoot := filepath.Join(dataRoot, "projects", projectID)
	wantTaskRoot := filepath.Join(wantRoot, "tasks", task.ID)
	wantLast := filepath.Join(wantTaskRoot, "output-last-message.json")
	wantArgs := []string{
		"codex", "--ask-for-approval", "never", "--sandbox", "workspace-write", "exec", "--json", "--skip-git-repo-check",
		"-m", "openai/custom", "-c", `model_reasoning_effort="high"`,
		"--output-schema", schema,
		"--output-last-message", wantLast,
		"-C", wantRoot, "-",
	}
	if !reflect.DeepEqual(cmd.Args, wantArgs) {
		t.Fatalf("exec args = %#v, want %#v", cmd.Args, wantArgs)
	}
	if cmd.Dir != wantRoot || root != wantRoot {
		t.Fatalf("exec roots: Dir=%q root=%q want=%q", cmd.Dir, root, wantRoot)
	}
	prompt, err := codex.StdinText(cmd.Stdin)
	if err != nil {
		t.Fatalf("read exec stdin: %v", err)
	}
	wantManifest := filepath.Join(wantTaskRoot, "task_manifest.json")
	if !strings.Contains(prompt, wantManifest) {
		t.Fatalf("exec prompt does not reference task manifest %q: %s", wantManifest, prompt)
	}
	if _, err := os.Stat(filepath.Join(wantRoot, projectLockNameForTest)); !os.IsNotExist(err) {
		t.Fatalf("command factory left project guard behind: %v", err)
	}
	if err := os.WriteFile(wantManifest, []byte(`{}`), 0o600); err != nil {
		t.Fatalf("write prepared manifest fixture: %v", err)
	}

	sessionID := "session-1"
	task.CodexSessionID = &sessionID
	resume, resumeRoot, err := makeResume(task, "answer")
	if err != nil {
		t.Fatalf("makeResume returned error: %v", err)
	}
	resumeWant := []string{
		"codex", "--ask-for-approval", "never", "--sandbox", "workspace-write", "exec", "resume", "--json",
		"-m", "openai/custom", "-c", `model_reasoning_effort="high"`,
		"--output-schema", schema,
		"--output-last-message", wantLast,
		"session-1", "-",
	}
	if !reflect.DeepEqual(resume.Args, resumeWant) {
		t.Fatalf("resume args = %#v, want %#v", resume.Args, resumeWant)
	}
	if resume.Dir != wantRoot || resumeRoot != wantRoot {
		t.Fatalf("resume roots: Dir=%q root=%q want=%q", resume.Dir, resumeRoot, wantRoot)
	}
	manifestEnvironment := "VIDEO_CONSOLE_TASK_MANIFEST=" + wantManifest
	if !slices.Contains(resume.Env, manifestEnvironment) {
		t.Fatalf("resume environment does not contain %q: %#v", manifestEnvironment, resume.Env)
	}
	answer, err := codex.StdinText(resume.Stdin)
	if err != nil || answer != "answer" {
		t.Fatalf("resume stdin = %q, err=%v", answer, err)
	}
}

func TestCodexCommandFactoryUsesPreparedManifestAction(t *testing.T) {
	dataRoot := t.TempDir()
	schema := filepath.Join(t.TempDir(), "codex-result.schema.json")
	base := testCodexCommandConfig(t, schema)
	makeCommand, _ := newCodexCommandFactories(config.Config{DataRoot: dataRoot}, base, nil)

	projectID := uuid.NewString()
	accountID := uuid.NewString()
	taskID := uuid.NewString()
	task := domain.CodexTask{ID: taskID, ProjectID: &projectID, AccountID: accountID, Type: "remix", SkillName: "finance-viral-remix", Action: domain.ActionRemixEnhanced}
	root, _, err := managedTaskRoot(dataRoot, task)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := managedTaskDirectory(root, taskID); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(root, "source.md")
	source := []byte("source script")
	if err := os.WriteFile(sourcePath, source, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(source)
	manifest, err := codex.BuildManifest(codex.BuildManifestInput{
		Task:      task,
		Project:   &domain.Project{ID: projectID, AccountID: accountID},
		Action:    task.Action,
		OutputDir: filepath.Join(root, "tasks", taskID, "output"),
		Inputs: []domain.AssetVersion{{
			ID: uuid.NewString(), AssetID: uuid.NewString(), ProjectID: &projectID, AccountID: accountID,
			Type: domain.AssetSourceScript, Version: 1, StorageKind: domain.StorageFile, Path: sourcePath,
			Filename: "source.md", MIMEType: "text/markdown", Size: int64(len(source)), SHA256: hex.EncodeToString(digest[:]), State: domain.AssetReady,
		}},
		SkillSnapshot: domain.SkillSnapshot{ID: uuid.NewString(), Name: "finance-viral-remix"},
	})
	if err != nil {
		t.Fatalf("build manifest: %v", err)
	}
	if _, err := codex.WriteManifest(manifest, codex.ManifestRoots{Project: root}); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	cmd, _, err := makeCommand(task)
	if err != nil {
		t.Fatalf("make command: %v", err)
	}
	prompt, err := codex.StdinText(cmd.Stdin)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "action=enhanced") || strings.Contains(prompt, "action=standard") {
		t.Fatalf("command did not use manifest action: %q", prompt)
	}
}

type completionObserverSetterStub struct{ observer taskcompletion.Observer }

func (s *completionObserverSetterStub) SetCompletionObserver(observer taskcompletion.Observer) {
	s.observer = observer
}

func TestCompletionObserverWiringUsesSameObserverForLegacyAndAppServer(t *testing.T) {
	observer := &completionObserverStubMain{}
	legacy := &completionObserverSetterStub{}
	config := wireTaskCompletion(legacy, "data", nil, observer)
	if legacy.observer != observer || config.Observer != observer || config.DataRoot != "data" {
		t.Fatalf("legacy=%v config=%+v", legacy.observer, config)
	}
}

type failingRemixReconciler struct{}

func (failingRemixReconciler) ReconcileTerminalWorkflows(context.Context) error {
	return errors.New("reconcile failed")
}

func TestRemixWorkflowStartupReconcileLogsAndContinues(t *testing.T) {
	var logged string
	reconcileRemixWorkflows(context.Background(), failingRemixReconciler{}, func(format string, args ...any) { logged = fmt.Sprintf(format, args...) })
	if !strings.Contains(logged, "reconcile failed") {
		t.Fatalf("log=%q", logged)
	}
}

type montageDisplayReconcilerStub struct {
	queued int
	err    error
}

func (stub montageDisplayReconcilerStub) ReconcileDisplayNames(context.Context) (int, error) {
	return stub.queued, stub.err
}

func TestBackfillDisplayStartupSchedulingLogsAndContinues(t *testing.T) {
	var logged string
	reconcileMontageDisplayNames(context.Background(), montageDisplayReconcilerStub{err: errors.New("queue failed")}, func(format string, args ...any) {
		logged = fmt.Sprintf(format, args...)
	})
	if !strings.Contains(logged, "queue failed") {
		t.Fatalf("log=%q", logged)
	}
}

type completionObserverStubMain struct{}

func (*completionObserverStubMain) AfterTerminal(context.Context, domain.CodexTask) error { return nil }

func TestCodexCommandFactoriesRejectProjectRootEscape(t *testing.T) {
	dataRoot := t.TempDir()
	base := testCodexCommandConfig(t, filepath.Join(t.TempDir(), "schema.json"))
	makeCommand, _ := newCodexCommandFactories(config.Config{DataRoot: dataRoot}, base, nil)
	projectID := ".."
	if _, _, err := makeCommand(domain.CodexTask{ID: "task-1", ProjectID: &projectID, Type: "topic_select"}); err == nil {
		t.Fatal("expected project root escape to be rejected")
	}
}

func TestCodexCommandFactoriesRejectTaskRootEscape(t *testing.T) {
	dataRoot := t.TempDir()
	base := testCodexCommandConfig(t, filepath.Join(t.TempDir(), "schema.json"))
	makeCommand, _ := newCodexCommandFactories(config.Config{DataRoot: dataRoot}, base, nil)
	projectID := "project-1"
	if _, _, err := makeCommand(domain.CodexTask{ID: "..", ProjectID: &projectID, Type: "topic_select"}); err == nil {
		t.Fatal("expected task root escape to be rejected")
	}
}

func testCodexCommandConfig(t *testing.T, schema string) codex.Config {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "codex.exe")
	if err := os.WriteFile(binary, []byte("test binary placeholder"), 0o700); err != nil {
		t.Fatal(err)
	}
	return codex.Config{
		CodexBinaryPath: "codex",
		ResultSchema:    schema,
		Redactor:        security.NewRedactor(),
		BinaryResolver: func(string) (string, error) {
			return binary, nil
		},
	}
}

func TestCodexCommandFactoryUsesMontageScriptRuntimeByDefault(t *testing.T) {
	t.Setenv(agentruntime.EnvMontageRuntime, "script")
	dataRoot := t.TempDir()
	skillRoot := filepath.Join(t.TempDir(), "jianying-montage-draft")
	if err := os.MkdirAll(filepath.Join(skillRoot, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillRoot, "scripts", "run_montage_job.py"), []byte("#"), 0o644); err != nil {
		t.Fatal(err)
	}
	base := testCodexCommandConfig(t, filepath.Join(t.TempDir(), "schema.json"))
	makeCommand, _ := newCodexCommandFactories(config.Config{DataRoot: dataRoot}, base, func(string) (string, error) {
		return skillRoot, nil
	})
	projectID := "project-montage"
	taskID := "task-montage-1"
	task := domain.CodexTask{ID: taskID, ProjectID: &projectID, Type: "montage", Action: domain.ActionMontageExecute}
	root, _, err := managedTaskRoot(dataRoot, task)
	if err != nil {
		t.Fatal(err)
	}
	taskRoot, err := managedTaskDirectory(root, taskID)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(taskRoot, "task_manifest.json")
	if err := os.WriteFile(manifestPath, []byte(`{"task_id":"task-montage-1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd, gotRoot, err := makeCommand(task)
	if err != nil {
		t.Fatalf("makeCommand: %v", err)
	}
	if gotRoot != root {
		t.Fatalf("root=%q want %q", gotRoot, root)
	}
	joined := strings.Join(cmd.Args, " ")
	if !strings.Contains(joined, "montage-script-run") || !strings.Contains(joined, "--skill-root") {
		t.Fatalf("args=%#v", cmd.Args)
	}
	if !strings.Contains(joined, "--output-last-message") {
		t.Fatalf("missing output-last-message in %#v", cmd.Args)
	}
}

func TestCodexCommandFactoryHonorsCodexMontageRuntime(t *testing.T) {
	t.Setenv(agentruntime.EnvMontageRuntime, "codex")
	dataRoot := t.TempDir()
	base := testCodexCommandConfig(t, filepath.Join(t.TempDir(), "schema.json"))
	makeCommand, _ := newCodexCommandFactories(config.Config{DataRoot: dataRoot}, base, func(string) (string, error) {
		return t.TempDir(), nil
	})
	projectID := "project-montage"
	taskID := "task-montage-2"
	task := domain.CodexTask{ID: taskID, ProjectID: &projectID, Type: "montage", Action: domain.ActionMontageExecute, ModelName: "gpt-5.6-sol", ReasoningEffort: "medium"}
	root, _, err := managedTaskRoot(dataRoot, task)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := managedTaskDirectory(root, taskID); err != nil {
		t.Fatal(err)
	}
	cmd, _, err := makeCommand(task)
	if err != nil {
		t.Fatalf("makeCommand: %v", err)
	}
	if strings.Contains(strings.Join(cmd.Args, " "), "montage-script-run") {
		t.Fatalf("expected Codex exec, got %#v", cmd.Args)
	}
}

func TestCodexCommandFactoryUsesOpenAICompatWhenConfigured(t *testing.T) {
	t.Setenv(agentruntime.EnvLLMRuntime, "openai_compat")
	t.Setenv(agentruntime.EnvOpenAIBaseURL, "https://example.invalid/v1")
	t.Setenv(agentruntime.EnvOpenAIAPIKey, "test-key")
	dataRoot := t.TempDir()
	skillRoot := filepath.Join(t.TempDir(), "finance-viral-remix")
	if err := os.MkdirAll(filepath.Join(skillRoot, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	base := testCodexCommandConfig(t, filepath.Join(t.TempDir(), "schema.json"))
	makeCommand, _ := newCodexCommandFactories(config.Config{DataRoot: dataRoot}, base, func(name string) (string, error) {
		if name != "finance-viral-remix" {
			t.Fatalf("skill=%q", name)
		}
		return skillRoot, nil
	})
	projectID := "project-remix"
	taskID := "task-remix-1"
	task := domain.CodexTask{ID: taskID, ProjectID: &projectID, Type: "remix", Action: domain.ActionRemixStandard, ModelName: "gpt-test"}
	root, _, err := managedTaskRoot(dataRoot, task)
	if err != nil {
		t.Fatal(err)
	}
	taskRoot, err := managedTaskDirectory(root, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(taskRoot, "task_manifest.json"), []byte(`{"task_id":"task-remix-1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd, _, err := makeCommand(task)
	if err != nil {
		t.Fatalf("makeCommand: %v", err)
	}
	joined := strings.Join(cmd.Args, " ")
	if !strings.Contains(joined, "openai-compat-run") || !strings.Contains(joined, "--model") {
		t.Fatalf("args=%#v", cmd.Args)
	}
}

func TestCodexCommandFactoryFallsBackWhenOpenAIKeyMissing(t *testing.T) {
	t.Setenv(agentruntime.EnvLLMRuntime, "openai_compat")
	t.Setenv(agentruntime.EnvOpenAIBaseURL, "https://example.invalid/v1")
	t.Setenv(agentruntime.EnvOpenAIAPIKey, "")
	dataRoot := t.TempDir()
	base := testCodexCommandConfig(t, filepath.Join(t.TempDir(), "schema.json"))
	makeCommand, _ := newCodexCommandFactories(config.Config{DataRoot: dataRoot}, base, func(string) (string, error) {
		return t.TempDir(), nil
	})
	projectID := "project-remix"
	taskID := "task-remix-2"
	task := domain.CodexTask{ID: taskID, ProjectID: &projectID, Type: "remix", Action: domain.ActionRemixStandard, ModelName: "gpt-5.6-sol", ReasoningEffort: "medium"}
	root, _, err := managedTaskRoot(dataRoot, task)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := managedTaskDirectory(root, taskID); err != nil {
		t.Fatal(err)
	}
	cmd, _, err := makeCommand(task)
	if err != nil {
		t.Fatalf("makeCommand: %v", err)
	}
	if strings.Contains(strings.Join(cmd.Args, " "), "openai-compat-run") {
		t.Fatalf("expected Codex fallback, got %#v", cmd.Args)
	}
}

func TestCodexCommandFactoryUsesPiWhenBinaryAvailable(t *testing.T) {
	t.Setenv(agentruntime.EnvLLMRuntime, "pi")
	prev := lookPathPi
	lookPathPi = func(file string) (string, error) {
		if file != "pi" {
			t.Fatalf("lookPath file=%q", file)
		}
		return `C:\fake\pi.exe`, nil
	}
	t.Cleanup(func() { lookPathPi = prev })

	dataRoot := t.TempDir()
	skillRoot := filepath.Join(t.TempDir(), "finance-viral-remix")
	if err := os.MkdirAll(skillRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	base := testCodexCommandConfig(t, filepath.Join(t.TempDir(), "schema.json"))
	makeCommand, _ := newCodexCommandFactories(config.Config{DataRoot: dataRoot}, base, func(name string) (string, error) {
		if name != "finance-viral-remix" {
			t.Fatalf("skill=%q", name)
		}
		return skillRoot, nil
	})
	projectID := "project-remix"
	taskID := "task-remix-pi-1"
	task := domain.CodexTask{ID: taskID, ProjectID: &projectID, Type: "remix", Action: domain.ActionRemixStandard, ModelName: "pi-model"}
	root, _, err := managedTaskRoot(dataRoot, task)
	if err != nil {
		t.Fatal(err)
	}
	taskRoot, err := managedTaskDirectory(root, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(taskRoot, "task_manifest.json"), []byte(`{"task_id":"task-remix-pi-1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd, _, err := makeCommand(task)
	if err != nil {
		t.Fatalf("makeCommand: %v", err)
	}
	joined := strings.Join(cmd.Args, " ")
	if !strings.Contains(joined, "pi-run") || !strings.Contains(joined, "--model") {
		t.Fatalf("args=%#v", cmd.Args)
	}
}

func TestCodexCommandFactoryFallsBackWhenPiMissing(t *testing.T) {
	t.Setenv(agentruntime.EnvLLMRuntime, "pi")
	prev := lookPathPi
	lookPathPi = func(string) (string, error) {
		return "", fmt.Errorf("not found")
	}
	t.Cleanup(func() { lookPathPi = prev })

	dataRoot := t.TempDir()
	base := testCodexCommandConfig(t, filepath.Join(t.TempDir(), "schema.json"))
	makeCommand, _ := newCodexCommandFactories(config.Config{DataRoot: dataRoot}, base, func(string) (string, error) {
		return t.TempDir(), nil
	})
	projectID := "project-remix"
	taskID := "task-remix-pi-2"
	task := domain.CodexTask{ID: taskID, ProjectID: &projectID, Type: "remix", Action: domain.ActionRemixStandard, ModelName: "gpt-5.6-sol", ReasoningEffort: "medium"}
	root, _, err := managedTaskRoot(dataRoot, task)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := managedTaskDirectory(root, taskID); err != nil {
		t.Fatal(err)
	}
	cmd, _, err := makeCommand(task)
	if err != nil {
		t.Fatalf("makeCommand: %v", err)
	}
	if strings.Contains(strings.Join(cmd.Args, " "), "pi-run") {
		t.Fatalf("expected Codex fallback, got %#v", cmd.Args)
	}
}

func TestTaskCommandConfigStripsGrokSecretsForMontage(t *testing.T) {
	base := codex.Config{SecretEnvironment: map[string]string{
		"GROK_SEARCH_API_KEY": "secret",
		"PEXELS_API_KEY":      "pexels",
	}}
	got := taskCommandConfig(base, domain.CodexTask{Action: domain.ActionMontageExecute, ModelName: "gpt-5.6-sol", ReasoningEffort: "medium"}, t.TempDir(), t.TempDir())
	if _, ok := got.SecretEnvironment["GROK_SEARCH_API_KEY"]; ok {
		t.Fatal("montage tasks must not receive Grok secrets")
	}
	if got.SecretEnvironment["PEXELS_API_KEY"] != "pexels" {
		t.Fatalf("pexels secret = %q", got.SecretEnvironment["PEXELS_API_KEY"])
	}
}

func TestLoadCodexSecretEnvironmentReadsOnlyExplicitAllowlist(t *testing.T) {
	wantLookups := []string{
		"GROK_SEARCH_BASE_URL", "GROK_SEARCH_MODEL", "GROK_SEARCH_API_KEY", "PEXELS_API_KEY",
	}
	values := map[string]string{
		"GROK_SEARCH_BASE_URL": "https://search.invalid",
		"GROK_SEARCH_API_KEY":  "secret",
		"UNRELATED_SECRET":     "must-not-be-read",
	}
	var lookedUp []string
	got := loadCodexSecretEnvironment(func(key string) (string, bool) {
		lookedUp = append(lookedUp, key)
		value, ok := values[key]
		return value, ok
	})
	if !reflect.DeepEqual(lookedUp, wantLookups) {
		t.Fatalf("lookups = %#v, want %#v", lookedUp, wantLookups)
	}
	want := map[string]string{
		"GROK_SEARCH_BASE_URL": "https://search.invalid",
		"GROK_SEARCH_API_KEY":  "secret",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("environment = %#v, want %#v", got, want)
	}
}

const projectLockNameForTest = ".video-production-console.lock"
