package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildExecCommandUsesArgumentArrayAndSafeEnvironment(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "project"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx := TaskContext{
		TaskType: "topic_select", WorkspaceDir: root, ProjectDir: filepath.Join(root, "project"), ProjectID: "p1",
	}
	guard, err := OpenProjectDirGuard(ctx)
	if err != nil {
		t.Fatalf("OpenProjectDirGuard returned error: %v", err)
	}
	defer guard.Close()
	ctx.ProjectDirGuard = guard
	cmd, err := BuildExecCommand(Config{CodexBinaryPath: "codex", ResultSchema: "schemas/codex-result.schema.json"}, ctx)
	if err != nil {
		t.Fatalf("BuildExecCommand returned error: %v", err)
	}
	want := []string{"exec", "--json", "--skip-git-repo-check", "--output-schema", "schemas/codex-result.schema.json", "-C", filepath.Join(root, "project"), "-"}
	if strings.Join(cmd.Args[1:], "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("args = %#v, want %#v", cmd.Args[1:], want)
	}
	if len(cmd.Env) == 0 {
		t.Fatal("expected restricted environment")
	}
	for _, value := range cmd.Env {
		if strings.HasPrefix(value, "CODEX_TEST_SECRET=") {
			t.Fatal("secret environment variable leaked")
		}
	}
}

func TestBuildExecCommandRejectsUnsupportedTask(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "project"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildExecCommand(Config{CodexBinaryPath: "codex"}, TaskContext{TaskType: "unknown", WorkspaceDir: root, ProjectDir: filepath.Join(root, "project")}); err == nil {
		t.Fatal("expected unsupported task type to fail")
	}
}

func TestBuildExecCommandRejectsUnguardedContext(t *testing.T) {
	root := t.TempDir()
	if _, err := BuildExecCommand(Config{CodexBinaryPath: "codex"}, TaskContext{TaskType: "topic_select", WorkspaceDir: root, ProjectDir: filepath.Join(root, "project")}); err == nil {
		t.Fatal("expected unguarded command construction to fail")
	}
}

func TestProjectDirGuardOwnsExclusiveLockForLifecycle(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "project"), 0700); err != nil {
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

func TestBuildResumeCommandUsesSessionAndStdin(t *testing.T) {
	cmd := BuildResumeCommand(Config{CodexBinaryPath: "codex", ResultSchema: "result.json"}, "session-1", "answer")
	want := []string{"exec", "resume", "session-1", "--json", "--output-schema", "result.json", "-"}
	if strings.Join(cmd.Args[1:], "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("args = %#v, want %#v", cmd.Args[1:], want)
	}
	if cmd.Stdin == nil {
		t.Fatal("expected answer on stdin")
	}
	got, err := StdinText(cmd.Stdin)
	if err != nil {
		t.Fatalf("read stdin: %v", err)
	}
	if got != "answer" {
		t.Fatalf("stdin = %q, want answer", got)
	}
}

func TestSafeEnvironmentIsDeterministicallyOrdered(t *testing.T) {
	entries := (Config{}).SafeEnvironment()
	for i := 1; i < len(entries); i++ {
		if entries[i-1] > entries[i] {
			t.Fatalf("environment is not sorted: %q before %q", entries[i-1], entries[i])
		}
	}
}
