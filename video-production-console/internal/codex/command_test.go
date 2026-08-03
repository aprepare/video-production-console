package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"video-production-console/internal/security"
)

func commandFixture(t *testing.T) (Config, TaskContext) {
	t.Helper()
	workspace := t.TempDir()
	project := filepath.Join(workspace, "project")
	if err := os.Mkdir(project, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx := TaskContext{
		TaskType: "topic_select", WorkspaceDir: workspace, ProjectDir: project, ProjectID: "p1",
	}
	guard, err := OpenProjectDirGuard(ctx)
	if err != nil {
		t.Fatalf("OpenProjectDirGuard returned error: %v", err)
	}
	t.Cleanup(func() { _ = guard.Close() })
	ctx.ProjectDirGuard = guard
	binaryPath := filepath.Join(workspace, "codex.exe")
	if err := os.WriteFile(binaryPath, []byte("test binary placeholder"), 0o700); err != nil {
		t.Fatal(err)
	}
	return Config{
		CodexBinaryPath:   "codex",
		ResultSchema:      filepath.Join(workspace, "codex-result.schema.json"),
		OutputLastMessage: filepath.Join(project, "output-last-message.json"),
		WorkingDirectory:  project,
		Redactor:          security.NewRedactor(),
		BinaryResolver: func(name string) (string, error) {
			return binaryPath, nil
		},
	}, ctx
}

func TestWindowsDefaultBinaryResolverUsesNativeExecutable(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows native executable resolution")
	}
	binDir := t.TempDir()
	helper := filepath.Join(binDir, "codex.exe")
	copyCurrentTestExecutable(t, helper)
	t.Setenv("PATH", binDir)

	cfg, ctx := commandFixture(t)
	cfg.BinaryResolver = nil
	cmd, err := BuildExecCommand(cfg, ctx)
	if err != nil {
		t.Fatalf("BuildExecCommand returned error: %v", err)
	}
	wantPath, err := filepath.EvalSymlinks(helper)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(cmd.Path, wantPath) {
		t.Fatalf("Path = %q, want native executable %q", cmd.Path, wantPath)
	}
	if cmd.Args[0] != "codex" {
		t.Fatalf("Args[0] = %q, want configured binary name", cmd.Args[0])
	}
}

func TestWindowsBinaryResolverRejectsCommandScripts(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows command script rejection")
	}
	for _, extension := range []string{".cmd", ".bat", ".ps1"} {
		t.Run(extension, func(t *testing.T) {
			cfg, ctx := commandFixture(t)
			cfg.BinaryResolver = func(string) (string, error) {
				return filepath.Join(t.TempDir(), "codex"+extension), nil
			}
			if _, err := BuildExecCommand(cfg, ctx); err == nil || !strings.Contains(err.Error(), "CodexBinaryPath") {
				t.Fatalf("expected %s resolver result to be rejected with configuration guidance, got %v", extension, err)
			}
		})
	}
}

func TestWindowsBinaryResolverRejectsExeSymlinkToCommandScript(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows command script rejection")
	}
	directory := t.TempDir()
	script := filepath.Join(directory, "codex.cmd")
	if err := os.WriteFile(script, []byte("@exit /b 0\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "codex.exe")
	if err := os.Symlink(script, link); err != nil {
		t.Skipf("file symlinks unavailable: %v", err)
	}
	cfg, ctx := commandFixture(t)
	cfg.BinaryResolver = func(string) (string, error) { return link, nil }
	if _, err := BuildExecCommand(cfg, ctx); err == nil || !strings.Contains(err.Error(), "CodexBinaryPath") {
		t.Fatalf("expected canonical command script target to be rejected with configuration guidance, got %v", err)
	}
}

func copyCurrentTestExecutable(t *testing.T, destination string) {
	t.Helper()
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, data, 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestBuildExecCommandV2UsesExactArgumentsStdinAndManagedDirectory(t *testing.T) {
	cfg, ctx := commandFixture(t)
	cmd, err := BuildExecCommand(cfg, ctx)
	if err != nil {
		t.Fatalf("BuildExecCommand returned error: %v", err)
	}
	want := []string{
		"codex", "exec", "--json", "--skip-git-repo-check",
		"--output-schema", cfg.ResultSchema,
		"--output-last-message", cfg.OutputLastMessage,
		"-C", cfg.WorkingDirectory, "-",
	}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("args = %#v, want %#v", cmd.Args, want)
	}
	if cmd.Dir != cfg.WorkingDirectory {
		t.Fatalf("Dir = %q, want %q", cmd.Dir, cfg.WorkingDirectory)
	}
	gotPrompt, err := StdinText(cmd.Stdin)
	if err != nil {
		t.Fatalf("read stdin: %v", err)
	}
	wantPrompt, err := BuildPrompt(ctx)
	if err != nil {
		t.Fatalf("BuildPrompt returned error: %v", err)
	}
	if gotPrompt != wantPrompt {
		t.Fatalf("stdin = %q, want %q", gotPrompt, wantPrompt)
	}
}

func TestBuildExecCommandV2UsesOnlyAllowlistedEnvironment(t *testing.T) {
	t.Setenv("CODEX_TEST_SECRET", "must-not-leak")
	t.Setenv("GROK_SEARCH_API_KEY", "inherited-must-not-leak")
	t.Setenv("USERPROFILE", `C:\safe-user-profile`)
	cfg, ctx := commandFixture(t)
	cfg.SecretEnvironment = map[string]string{
		"GROK_SEARCH_BASE_URL": "https://search.invalid",
		"GROK_SEARCH_MODEL":    "grok-test",
		"GROK_SEARCH_API_KEY":  "grok-explicit-secret",
		"PEXELS_API_KEY":       "pexels-explicit-secret",
		"UNSUPPORTED_SECRET":   "must-not-leak",
		"grok_search_api_key":  "wrong-case-must-not-leak",
	}
	cmd, err := BuildExecCommand(cfg, ctx)
	if err != nil {
		t.Fatalf("BuildExecCommand returned error: %v", err)
	}

	systemKeys := map[string]bool{
		"PATH": true, "PATHEXT": true, "SYSTEMROOT": true, "WINDIR": true,
		"COMSPEC": true, "TEMP": true, "TMP": true, "USERPROFILE": true,
	}
	secretKeys := map[string]string{
		"GROK_SEARCH_BASE_URL": "https://search.invalid",
		"GROK_SEARCH_MODEL":    "grok-test",
		"GROK_SEARCH_API_KEY":  "grok-explicit-secret",
		"PEXELS_API_KEY":       "pexels-explicit-secret",
	}
	gotSecrets := map[string]string{}
	gotSystem := map[string]string{}
	for _, entry := range cmd.Env {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			t.Fatalf("malformed environment entry %q", entry)
		}
		if !systemKeys[key] {
			if _, ok := secretKeys[key]; !ok {
				t.Fatalf("non-allowlisted environment key leaked: %q", key)
			}
		}
		if _, ok := secretKeys[key]; ok {
			gotSecrets[key] = value
		}
		if systemKeys[key] {
			gotSystem[key] = value
		}
		if strings.Contains(value, "must-not-leak") {
			t.Fatalf("inherited or unsupported environment value leaked in %q", key)
		}
	}
	if !reflect.DeepEqual(gotSecrets, secretKeys) {
		t.Fatalf("explicit secrets = %#v, want %#v", gotSecrets, secretKeys)
	}
	if gotSystem["USERPROFILE"] != `C:\safe-user-profile` {
		t.Fatalf("USERPROFILE = %q, want explicitly allowlisted value", gotSystem["USERPROFILE"])
	}
}

func TestSnapshotCommandContainsOnlyRedactedInspectableMetadata(t *testing.T) {
	cfg, ctx := commandFixture(t)
	secret := cfg.WorkingDirectory
	cfg.SecretEnvironment = map[string]string{"GROK_SEARCH_API_KEY": secret}
	cmd, err := BuildExecCommand(cfg, ctx)
	if err != nil {
		t.Fatalf("BuildExecCommand returned error: %v", err)
	}
	snapshot := SnapshotCommand(cmd, cfg.Redactor)
	if snapshot.Binary != "codex" {
		t.Fatalf("binary = %q", snapshot.Binary)
	}
	if snapshot.WorkingDirectory != security.Mask {
		t.Fatalf("working directory was not redacted: %q", snapshot.WorkingDirectory)
	}
	if !reflect.DeepEqual(snapshot.EnvironmentKeys, sortedEnvironmentKeys(cmd.Env)) {
		t.Fatalf("environment keys = %#v, want %#v", snapshot.EnvironmentKeys, sortedEnvironmentKeys(cmd.Env))
	}
	for _, key := range snapshot.EnvironmentKeys {
		if strings.Contains(key, "=") {
			t.Fatalf("snapshot contains an environment value: %q", key)
		}
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("snapshot leaked registered secret: %s", encoded)
	}
}

func TestBuildExecCommandRejectsInvalidOutputAndWorkingPaths(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config, TaskContext)
	}{
		{"empty schema", func(cfg *Config, _ TaskContext) { cfg.ResultSchema = "" }},
		{"relative schema", func(cfg *Config, _ TaskContext) { cfg.ResultSchema = "schema.json" }},
		{"empty last message", func(cfg *Config, _ TaskContext) { cfg.OutputLastMessage = "" }},
		{"relative last message", func(cfg *Config, _ TaskContext) { cfg.OutputLastMessage = "last.json" }},
		{"empty working directory", func(cfg *Config, _ TaskContext) { cfg.WorkingDirectory = "" }},
		{"relative working directory", func(cfg *Config, _ TaskContext) { cfg.WorkingDirectory = "project" }},
		{"working directory outside task root", func(cfg *Config, ctx TaskContext) { cfg.WorkingDirectory = ctx.WorkspaceDir }},
		{"last message outside task root", func(cfg *Config, ctx TaskContext) {
			cfg.OutputLastMessage = filepath.Join(ctx.WorkspaceDir, "last.json")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, ctx := commandFixture(t)
			tt.mutate(&cfg, ctx)
			if _, err := BuildExecCommand(cfg, ctx); err == nil {
				t.Fatal("expected invalid command configuration to fail")
			}
		})
	}
}

func TestBuildExecCommandCanonicalizesAbsolutePaths(t *testing.T) {
	cfg, ctx := commandFixture(t)
	cfg.ResultSchema = filepath.Join(ctx.WorkspaceDir, "schema-dir", "..", "schema.json")
	cfg.OutputLastMessage = filepath.Join(ctx.ProjectDir, "output", "..", "last.json")
	cfg.WorkingDirectory = filepath.Join(ctx.ProjectDir, "child", "..")
	cmd, err := BuildExecCommand(cfg, ctx)
	if err != nil {
		t.Fatalf("BuildExecCommand returned error: %v", err)
	}
	wantSchema := filepath.Join(ctx.WorkspaceDir, "schema.json")
	wantLast := filepath.Join(ctx.ProjectDir, "last.json")
	wantArgs := []string{"codex", "exec", "--json", "--skip-git-repo-check", "--output-schema", wantSchema, "--output-last-message", wantLast, "-C", ctx.ProjectDir, "-"}
	if !reflect.DeepEqual(cmd.Args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", cmd.Args, wantArgs)
	}
	if cmd.Dir != ctx.ProjectDir {
		t.Fatalf("Dir = %q, want %q", cmd.Dir, ctx.ProjectDir)
	}
}

func TestBuildExecCommandRejectsUnsupportedTask(t *testing.T) {
	cfg, ctx := commandFixture(t)
	ctx.TaskType = "unknown"
	if _, err := BuildExecCommand(cfg, ctx); err == nil {
		t.Fatal("expected unsupported task type to fail")
	}
}

func TestBuildExecCommandRejectsUnguardedContext(t *testing.T) {
	cfg, ctx := commandFixture(t)
	ctx.ProjectDirGuard = nil
	if _, err := BuildExecCommand(cfg, ctx); err == nil {
		t.Fatal("expected unguarded command construction to fail")
	}
}

func TestProjectDirGuardOwnsExclusiveLockForLifecycle(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "project"), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx := TaskContext{TaskType: "topic_select", WorkspaceDir: root, ProjectDir: filepath.Join(root, "project")}
	guard, err := OpenProjectDirGuard(ctx)
	if err != nil {
		t.Fatalf("OpenProjectDirGuard returned error: %v", err)
	}
	if guard.CanonicalProjectDir() != filepath.Join(root, "project") {
		t.Fatalf("canonical project = %q", guard.CanonicalProjectDir())
	}
	if _, err := OpenProjectDirGuard(ctx); err == nil {
		t.Fatal("expected second guard to be rejected while first is open")
	}
	if err := guard.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	guard2, err := OpenProjectDirGuard(ctx)
	if err != nil {
		t.Fatalf("expected guard to be reusable after close: %v", err)
	}
	defer guard2.Close()
}

func TestBuildResumeCommandV2UsesExactArgumentsStdinAndManagedDirectory(t *testing.T) {
	cfg, _ := commandFixture(t)
	cmd, err := BuildResumeCommand(cfg, "session-1", "answer")
	if err != nil {
		t.Fatalf("BuildResumeCommand returned error: %v", err)
	}
	want := []string{
		"codex", "exec", "resume", "--json",
		"--output-schema", cfg.ResultSchema,
		"--output-last-message", cfg.OutputLastMessage,
		"session-1", "-",
	}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("args = %#v, want %#v", cmd.Args, want)
	}
	if cmd.Dir != cfg.WorkingDirectory {
		t.Fatalf("Dir = %q, want %q", cmd.Dir, cfg.WorkingDirectory)
	}
	got, err := StdinText(cmd.Stdin)
	if err != nil {
		t.Fatalf("read stdin: %v", err)
	}
	if got != "answer" {
		t.Fatalf("stdin = %q, want answer", got)
	}
}

func TestBuildResumeCommandSnapshotRedactsSessionIDWithoutChangingCommand(t *testing.T) {
	cfg, _ := commandFixture(t)
	sessionID := "session-secret-123"
	cmd, err := BuildResumeCommand(cfg, sessionID, "answer")
	if err != nil {
		t.Fatalf("BuildResumeCommand returned error: %v", err)
	}
	if cmd.Args[len(cmd.Args)-2] != sessionID {
		t.Fatalf("actual command session = %q, want %q", cmd.Args[len(cmd.Args)-2], sessionID)
	}
	snapshot := SnapshotCommand(cmd, cfg.Redactor)
	joined := strings.Join(snapshot.Args, "\x00")
	if strings.Contains(joined, sessionID) || !strings.Contains(joined, security.Mask) {
		t.Fatalf("snapshot args did not stably redact session: %#v", snapshot.Args)
	}
}

func TestBuildResumeCommandRejectsInvalidSession(t *testing.T) {
	cfg, _ := commandFixture(t)
	for _, sessionID := range []string{"", " ", "-dangerous-option", "../session", "session id", strings.Repeat("a", 129)} {
		t.Run(sessionID, func(t *testing.T) {
			if _, err := BuildResumeCommand(cfg, sessionID, "answer"); err == nil {
				t.Fatalf("expected session %q to be rejected", sessionID)
			}
		})
	}
}

func TestBuildResumeCommandRejectsInvalidOutputAndWorkingPaths(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"empty schema", func(cfg *Config) { cfg.ResultSchema = "" }},
		{"relative schema", func(cfg *Config) { cfg.ResultSchema = "schema.json" }},
		{"empty last message", func(cfg *Config) { cfg.OutputLastMessage = "" }},
		{"relative last message", func(cfg *Config) { cfg.OutputLastMessage = "last.json" }},
		{"empty working directory", func(cfg *Config) { cfg.WorkingDirectory = "" }},
		{"relative working directory", func(cfg *Config) { cfg.WorkingDirectory = "project" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, _ := commandFixture(t)
			tt.mutate(&cfg)
			if _, err := BuildResumeCommand(cfg, "session-1", "answer"); err == nil {
				t.Fatal("expected invalid command configuration to fail")
			}
		})
	}
}

func TestSafeEnvironmentIsDeterministicallyOrdered(t *testing.T) {
	cfg, _ := commandFixture(t)
	cfg.SecretEnvironment = map[string]string{
		"PEXELS_API_KEY": "pexels", "GROK_SEARCH_API_KEY": "grok",
	}
	entries := cfg.SafeEnvironment()
	for i := 1; i < len(entries); i++ {
		if entries[i-1] > entries[i] {
			t.Fatalf("environment is not sorted: %q before %q", entries[i-1], entries[i])
		}
	}
}
