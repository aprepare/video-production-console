package codex

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"video-production-console/internal/domain"
)

func TestPromptUsesManifestPathWithoutExpandingAssetsOrSecrets(t *testing.T) {
	taskID := uuid.NewString()
	manifest := TaskManifest{SchemaVersion: ProtocolSchemaVersion, TaskID: taskID, JobID: taskID, Skill: "finance-viral-remix", Action: domain.ActionRemixEnhanced, Inputs: []ManifestInput{{Path: `C:\secret-project\source.md`}}, OutputDir: `C:\managed\output`, NonSecretSettings: ManifestSettings{MediaRoot: "safe"}}
	manifestPath := filepath.Join(`C:\managed`, "tasks", taskID, "task_manifest.json")
	prompt, err := BuildManifestPrompt(manifest, manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"$finance-viral-remix", "action=enhanced", manifestPath, "manifest inputs as authoritative", "output_dir", "exactly one JSON object", "Do not open WeChat Channels", "Do not launch Jianying"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q: %s", want, prompt)
		}
	}
	for _, forbidden := range []string{manifest.Inputs[0].Path, manifest.OutputDir, "safe", "needs_input"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("prompt leaked %q: %s", forbidden, prompt)
		}
	}
	if len(prompt) > 1200 {
		t.Fatalf("prompt is unbounded: %d", len(prompt))
	}
}

func TestPromptRejectsMismatchedSkillActionAndSecretPath(t *testing.T) {
	manifest := TaskManifest{SchemaVersion: ProtocolSchemaVersion, TaskID: uuid.NewString(), Skill: "finance-topic-selector", Action: domain.ActionMontagePlan}
	if _, err := BuildManifestPrompt(manifest, `C:\tasks\task_manifest.json`); err == nil {
		t.Fatal("expected skill mismatch rejection")
	}
	manifest.Skill = "jianying-montage-draft"
	if _, err := BuildManifestPrompt(manifest, `C:\token=plaintext\task_manifest.json`); err == nil {
		t.Fatal("expected secret-like path rejection")
	}
}
