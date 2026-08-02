package codex

import (
	"strings"
	"testing"
)

func TestBuildExecCommandUsesArgumentArrayAndSafeEnvironment(t *testing.T) {
	cmd := BuildExecCommand(Config{CodexBinaryPath: "codex", ResultSchema: "schemas/codex-result.schema.json"}, TaskContext{
		TaskType: "topic_select", ProjectDir: `C:\\console\\project`, ProjectID: "p1",
	})
	want := []string{"exec", "--json", "--skip-git-repo-check", "--output-schema", "schemas/codex-result.schema.json", "-C", `C:\\console\\project`, "-"}
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

func TestBuildResumeCommandUsesSessionAndStdin(t *testing.T) {
	cmd := BuildResumeCommand(Config{CodexBinaryPath: "codex", ResultSchema: "result.json"}, "session-1", "answer")
	want := []string{"exec", "resume", "session-1", "--json", "--output-schema", "result.json", "-"}
	if strings.Join(cmd.Args[1:], "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("args = %#v, want %#v", cmd.Args[1:], want)
	}
	if cmd.Stdin == nil {
		t.Fatal("expected answer on stdin")
	}
}
