package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildPromptMapsTaskAndIsolatesProject(t *testing.T) {
	t.Setenv("CODEX_TEST_SECRET", "do-not-leak")
	root := t.TempDir()
	projectDir := filepath.Join(root, "projects", "project-123")
	prompt, err := BuildPrompt(TaskContext{
		ProjectID: "project-123", AccountName: "finance", TaskType: "remix",
		WorkspaceDir: root, ProjectDir: projectDir, AllowedDir: filepath.Join(projectDir, "out"),
		AssetPaths:   []string{filepath.Join(projectDir, "source.mp4")},
		OtherProject: filepath.Join(root, "projects", "project-999"),
	})
	if err != nil {
		t.Fatalf("BuildPrompt returned error: %v", err)
	}
	for _, want := range []string{"project-123", "finance", "finance-viral-remix", filepath.Join(projectDir, "source.mp4")} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q: %s", want, prompt)
		}
	}
	if strings.Contains(prompt, "project-999") || strings.Contains(prompt, os.Getenv("CODEX_TEST_SECRET")) {
		t.Fatalf("prompt leaked out-of-scope value: %s", prompt)
	}
}

func TestBuildPromptRejectsUnsupportedTaskAndOutsidePaths(t *testing.T) {
	root := t.TempDir()
	ctx := TaskContext{WorkspaceDir: root, ProjectDir: filepath.Join(root, "project"), TaskType: "unknown"}
	if _, err := BuildPrompt(ctx); err == nil {
		t.Fatal("expected unsupported task type to fail")
	}
	ctx.TaskType = "remix"
	ctx.ProjectDir = filepath.Join(root, "..", "other-project")
	if _, err := BuildPrompt(ctx); err == nil {
		t.Fatal("expected outside project directory to fail")
	}
	ctx.ProjectDir = filepath.Join(root, "project")
	ctx.AllowedDir = filepath.Join(root, "..", "outside")
	if _, err := BuildPrompt(ctx); err == nil {
		t.Fatal("expected outside allowed directory to fail")
	}
	ctx.AllowedDir = filepath.Join(root, "project", "out")
	ctx.AssetPaths = []string{filepath.Join(root, "..", "secret.txt")}
	if _, err := BuildPrompt(ctx); err == nil {
		t.Fatal("expected outside asset path to fail")
	}
}

func TestSkillForTaskRejectsUnknownTask(t *testing.T) {
	if _, err := SkillForTask("unknown"); err == nil {
		t.Fatal("expected unknown task type to fail")
	}
}
