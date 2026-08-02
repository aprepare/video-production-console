package codex

import (
	"os"
	"strings"
	"testing"
)

func TestBuildPromptMapsTaskAndIsolatesProject(t *testing.T) {
	t.Setenv("CODEX_TEST_SECRET", "do-not-leak")
	prompt := BuildPrompt(TaskContext{
		ProjectID:    "project-123",
		AccountName:  "财经账号",
		TaskType:     "remix",
		ProjectDir:   `C:\\console\\projects\\project-123`,
		AssetPaths:   []string{`C:\\console\\projects\\project-123\\source.mp4`},
		OtherProject: `C:\\console\\projects\\project-999`,
	})
	for _, want := range []string{"project-123", "财经账号", "finance-viral-remix", `C:\\console\\projects\\project-123\\source.mp4`} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q: %s", want, prompt)
		}
	}
	if strings.Contains(prompt, "project-999") || strings.Contains(prompt, os.Getenv("CODEX_TEST_SECRET")) {
		t.Fatalf("prompt leaked an out-of-scope value: %s", prompt)
	}
}

func TestSkillForTaskRejectsUnknownTask(t *testing.T) {
	if _, err := SkillForTask("unknown"); err == nil {
		t.Fatal("expected unknown task type to fail")
	}
}
