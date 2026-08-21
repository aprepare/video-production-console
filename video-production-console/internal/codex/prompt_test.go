package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"video-production-console/internal/domain"
)

func TestPromptUsesManifestPathWithoutExpandingAssetsOrSecrets(t *testing.T) {
	taskID := uuid.NewString()
	manifest := TaskManifest{SchemaVersion: ProtocolSchemaVersion, TaskID: taskID, JobID: taskID, Skill: "finance-viral-remix", Action: domain.ActionRemixEnhanced, Inputs: []ManifestInput{{Path: `C:\secret-project\source.md`}}, OutputDir: filepath.Join(`C:\managed`, "tasks", taskID, "output"), NonSecretSettings: ManifestSettings{MediaRoot: "safe"}}
	manifestPath := filepath.Join(`C:\managed`, "tasks", taskID, "task_manifest.json")
	prompt, err := BuildManifestPrompt(manifest, manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"$finance-viral-remix", "action=enhanced", taskManifestEnvironmentKey, "manifest inputs as authoritative", "output_dir", "exactly one JSON object", "never pipe non-ASCII", "Do not open WeChat Channels", "Do not launch Jianying", "No web/Grok search"} {
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

func TestRemixReviewPromptReadsRevisionNotes(t *testing.T) {
	taskID := uuid.NewString()
	manifest := TaskManifest{
		SchemaVersion: ProtocolSchemaVersion,
		TaskID:        taskID,
		JobID:         taskID,
		Skill:         "finance-viral-remix",
		Action:        domain.ActionRemixReview,
		OutputDir:     filepath.Join(`C:\managed`, "tasks", taskID, "output"),
		NonSecretSettings: ManifestSettings{RevisionNotes: "开场更口语"},
	}
	prompt, err := BuildManifestPrompt(manifest, filepath.Join(`C:\managed`, "tasks", taskID, "task_manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"action=review", "revision_notes", "continuous_script", "publishing_package"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("review prompt missing %q: %s", want, prompt)
		}
	}
	if len(prompt) > 1200 {
		t.Fatalf("review prompt is unbounded: %d", len(prompt))
	}
}

func TestMontageExecutePromptKeepsPlanFirstAndNoDelegation(t *testing.T) {
	taskID := uuid.NewString()
	manifest := TaskManifest{
		SchemaVersion: ProtocolSchemaVersion,
		TaskID:        taskID,
		JobID:         taskID,
		Skill:         "jianying-montage-draft",
		Action:        domain.ActionMontageExecute,
		OutputDir:     filepath.Join(`C:\managed`, "tasks", taskID, "output"),
	}
	prompt, err := BuildManifestPrompt(manifest, filepath.Join(`C:\managed`, "tasks", taskID, "task_manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"$jianying-montage-draft",
		"action=execute",
		"console-contract.md",
		"validate-inputs",
		"production_plan.json",
		"validate-plan",
		"execute --plan",
		"No web/Grok search",
		"no subagents",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("montage prompt missing %q: %s", want, prompt)
		}
	}
	if len(prompt) > 1200 {
		t.Fatalf("montage prompt is unbounded: %d", len(prompt))
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

func TestResolveTaskActionMapsLegacyTypesAndRejectsSkillCrossovers(t *testing.T) {
	action, resolved, err := ResolveTaskAction("remix", "")
	if err != nil || action != domain.ActionRemixStandard || resolved.Skill != "finance-viral-remix" {
		t.Fatalf("legacy remix resolution = %q %#v, %v", action, resolved, err)
	}
	action, resolved, err = ResolveTaskAction("remix", domain.ActionRemixEnhanced)
	if err != nil || action != domain.ActionRemixEnhanced || resolved.WireAction != "enhanced" {
		t.Fatalf("enhanced remix resolution = %q %#v, %v", action, resolved, err)
	}
	if _, _, err := ResolveTaskAction("remix", domain.ActionMontagePlan); err == nil {
		t.Fatal("expected a cross-skill action to be rejected")
	}
}

func TestSkillForTaskAndResolveActionImageVideo(t *testing.T) {
	skill, err := SkillForTask("image_video")
	if err != nil || skill != "jianying-montage-draft" {
		t.Fatalf("SkillForTask(image_video)=%q %v", skill, err)
	}
	action, resolved, err := ResolveTaskAction("image_video", "")
	if err != nil || action != domain.ActionMontageExecute || resolved.Skill != "jianying-montage-draft" || resolved.WireAction != "execute" {
		t.Fatalf("ResolveTaskAction(image_video)=%q %#v %v", action, resolved, err)
	}
}

func TestSkillForTaskAndResolveActionMovieMontage(t *testing.T) {
	skill, err := SkillForTask("movie_montage")
	if err != nil || skill != MovieMontageSkill {
		t.Fatalf("SkillForTask(movie_montage)=%q %v", skill, err)
	}
	action, resolved, err := ResolveTaskAction("movie_montage", "")
	if err != nil || action != domain.ActionMontageExecute || resolved.Skill != MovieMontageSkill || resolved.WireAction != "execute" {
		t.Fatalf("ResolveTaskAction(movie_montage)=%q %#v %v", action, resolved, err)
	}
	resolved, err = ResolveMontageSkill(domain.ActionMontageExecute, MovieMontageSkill)
	if err != nil || resolved.Skill != MovieMontageSkill {
		t.Fatalf("ResolveMontageSkill=%#v %v", resolved, err)
	}
}

func TestResolveTaskActionRejectsSpokenConsoleWorkflow(t *testing.T) {
	if _, _, err := ResolveTaskAction("spoken_format", ""); err == nil {
		t.Fatal("legacy spoken_format task type must not start a new console task")
	}
	if _, _, err := ResolveTaskAction("remix", domain.ActionSpokenFormat); err == nil {
		t.Fatal("deprecated remix.spoken_format action must not start a new console task")
	}
	action, resolved, err := ResolveTaskAction("remix", domain.ActionRemixSpokenLines)
	if err != nil || action != domain.ActionRemixSpokenLines || resolved.WireAction != "spoken_lines" {
		t.Fatalf("spoken_lines resolution = %q %#v %v", action, resolved, err)
	}
}

func TestManifestPromptRejectsPathOutsideCanonicalTaskLocation(t *testing.T) {
	root, taskID := t.TempDir(), uuid.NewString()
	output := filepath.Join(root, "tasks", taskID, "output")
	if err := os.MkdirAll(output, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := TaskManifest{SchemaVersion: ProtocolSchemaVersion, TaskID: taskID, JobID: taskID, Skill: "finance-topic-selector", Action: domain.ActionTopicBrainstorm, OutputDir: output}
	if _, err := BuildManifestPrompt(manifest, filepath.Join(root, "other", "task_manifest.json")); err == nil {
		t.Fatal("expected manifest-path escape rejection")
	}
}

func TestBuildManifestAppServerPromptUsesVerifiedManifestPath(t *testing.T) {
	root, taskID := t.TempDir(), uuid.NewString()
	output := filepath.Join(root, "tasks", taskID, "output")
	if err := os.MkdirAll(output, 0o755); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, "tasks", taskID, "task_manifest.json")
	manifest := TaskManifest{SchemaVersion: ProtocolSchemaVersion, TaskID: taskID, JobID: taskID, Skill: "jianying-montage-draft", Action: domain.ActionMontageExecute, OutputDir: output}
	prompt, err := BuildManifestAppServerPrompt(manifest, manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, manifestPath) || strings.Contains(prompt, taskManifestEnvironmentKey) {
		t.Fatalf("App Server prompt does not contain the verified manifest path: %s", prompt)
	}
}
