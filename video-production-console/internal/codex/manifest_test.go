package codex

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"video-production-console/internal/domain"
)

func TestBuildManifestLocksMontageInputsAndUsesTaskAsJob(t *testing.T) {
	taskID, projectID, accountID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	root := t.TempDir()
	paths := []string{filepath.Join(root, "spoken.md"), filepath.Join(root, "narration.wav"), filepath.Join(root, "subtitles.srt"), filepath.Join(root, "background.png")}
	for _, path := range paths {
		if err := os.WriteFile(path, []byte("input"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	profile := filepath.Join(root, "machine-profile.json")
	if err := os.WriteFile(profile, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	project := domain.Project{ID: projectID, AccountID: accountID}
	inputs := []domain.AssetVersion{
		manifestVersion(projectID, accountID, domain.AssetSpokenScript, paths[0]),
		manifestVersion(projectID, accountID, domain.AssetNarration, paths[1]),
		manifestVersion(projectID, accountID, domain.AssetSubtitleSRT, paths[2]),
		manifestVersion("", accountID, domain.AssetAccountBackground, paths[3]),
	}
	manifest, err := BuildManifest(BuildManifestInput{
		Task: domain.CodexTask{ID: taskID}, Project: &project, Inputs: inputs,
		Action: domain.ActionMontagePlan, OutputDir: filepath.Join(root, "tasks", taskID, "output"),
		SkillSnapshot:     domain.SkillSnapshot{ID: uuid.NewString(), Name: "jianying-montage-draft"},
		NonSecretSettings: ManifestSettings{MediaRoot: `C:\media`, MachineProfilePath: profile},
	})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.JobID != taskID || manifest.TaskID != taskID {
		t.Fatalf("task/job IDs not locked: %#v", manifest)
	}
	if manifest.Skill != "jianying-montage-draft" || manifest.Action != domain.ActionMontagePlan {
		t.Fatalf("wrong action mapping: %#v", manifest)
	}
	for _, role := range []string{"spoken_script", "narration", "subtitle_srt", "account_background"} {
		if !manifestHasRole(manifest, role) {
			t.Fatalf("role %q missing from %#v", role, manifest.Inputs)
		}
	}
	for _, input := range manifest.Inputs {
		if input.VersionID == "" || input.SHA256 == "" || input.Size == 0 || input.Path == "" || input.Role == "" {
			t.Fatalf("input is not version-locked: %#v", input)
		}
	}
}

func TestBuildManifestUsesOneDomainToWireActionMapping(t *testing.T) {
	tests := []struct {
		action      domain.TaskAction
		skill, wire string
	}{
		{domain.ActionTopicBrainstorm, "finance-topic-selector", "brainstorm"},
		{domain.ActionTopicCommit, "finance-topic-selector", "commit_topic"},
		{domain.ActionTopicDeepen, "finance-topic-selector", "deepen"},
		{domain.ActionRemixStandard, "finance-viral-remix", "standard"},
		{domain.ActionRemixEnhanced, "finance-viral-remix", "enhanced"},
		{domain.ActionRemixFromTopic, "finance-viral-remix", "from_topic_card"},
		{domain.ActionSpokenFormat, "finance-viral-remix", "spoken_format"},
		{domain.ActionRemixReview, "finance-viral-remix", "review"},
		{domain.ActionMontagePlan, "jianying-montage-draft", "plan"},
		{domain.ActionMontageExecute, "jianying-montage-draft", "execute"},
	}
	for _, tt := range tests {
		t.Run(string(tt.action), func(t *testing.T) {
			got, err := ResolveAction(tt.action)
			if err != nil || got.Skill != tt.skill || got.WireAction != tt.wire {
				t.Fatalf("ResolveAction(%q)=%#v,%v", tt.action, got, err)
			}
		})
	}
	if _, err := ResolveAction(domain.TaskAction("commit_topic")); err == nil {
		t.Fatal("wire action must not be accepted as a domain action")
	}
}

func TestBuildManifestRejectsMissingMontageInput(t *testing.T) {
	projectID, accountID := uuid.NewString(), uuid.NewString()
	taskID, projectRoot := uuid.NewString(), t.TempDir()
	base := BuildManifestInput{
		Task: domain.CodexTask{ID: taskID}, Action: domain.ActionMontagePlan,
		Project:   &domain.Project{ID: projectID, AccountID: accountID},
		OutputDir: filepath.Join(projectRoot, "tasks", taskID, "output"), SkillSnapshot: domain.SkillSnapshot{ID: uuid.NewString()},
		Inputs: []domain.AssetVersion{
			manifestVersion(projectID, accountID, domain.AssetSpokenScript, `C:\x\spoken.md`),
		},
	}
	if _, err := BuildManifest(base); err == nil || !strings.Contains(err.Error(), "narration") {
		t.Fatalf("expected missing montage roles, got %v", err)
	}
}

func TestBuildAndWriteManifestRejectSchemaInvalidInputs(t *testing.T) {
	projectID, accountID := uuid.NewString(), uuid.NewString()
	projectRoot := t.TempDir()
	sourcePath := filepath.Join(projectRoot, "source.md")
	if err := os.WriteFile(sourcePath, []byte("input"), 0o600); err != nil {
		t.Fatal(err)
	}
	valid := manifestVersion(projectID, accountID, domain.AssetSourceScript, sourcePath)
	taskID := uuid.NewString()
	base := BuildManifestInput{
		Task: domain.CodexTask{ID: taskID}, Project: &domain.Project{ID: projectID, AccountID: accountID},
		Action: domain.ActionRemixStandard, OutputDir: filepath.Join(projectRoot, "tasks", taskID, "output"), SkillSnapshot: domain.SkillSnapshot{ID: uuid.NewString()}, Inputs: []domain.AssetVersion{valid},
	}
	tests := []struct {
		name   string
		mutate func(*domain.AssetVersion)
	}{
		{"zero version", func(v *domain.AssetVersion) { v.Version = 0 }},
		{"empty MIME", func(v *domain.AssetVersion) { v.MIMEType = "" }},
		{"invalid MIME", func(v *domain.AssetVersion) { v.MIMEType = "not a mime" }},
		{"invalid SHA", func(v *domain.AssetVersion) { v.SHA256 = "abc" }},
		{"legacy asset type", func(v *domain.AssetVersion) { v.Type = domain.AssetAudio }},
	}
	for _, tt := range tests {
		t.Run("build "+tt.name, func(t *testing.T) {
			input := base
			version := valid
			tt.mutate(&version)
			input.Inputs = []domain.AssetVersion{version}
			if _, err := BuildManifest(input); err == nil {
				t.Fatal("expected schema-invalid input rejection")
			}
		})
	}
	manifest, err := BuildManifest(base)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Inputs[0].Role = "source_script"
	if _, err := WriteManifest(manifest, ManifestRoots{Project: projectRoot}); err == nil || !strings.Contains(err.Error(), "role") {
		t.Fatalf("expected role mismatch rejection in WriteManifest, got %v", err)
	}
}

func TestBuildManifestDerivesPrimaryRoleAndRejectsAssetIdentitySpoofing(t *testing.T) {
	projectID, accountID := uuid.NewString(), uuid.NewString()
	taskID, projectRoot := uuid.NewString(), t.TempDir()
	source := filepath.Join(projectRoot, "source.md")
	if err := os.WriteFile(source, []byte("input"), 0o600); err != nil {
		t.Fatal(err)
	}
	input := BuildManifestInput{
		Task: domain.CodexTask{ID: taskID}, Project: &domain.Project{ID: projectID, AccountID: accountID},
		Action: domain.ActionRemixStandard, OutputDir: filepath.Join(projectRoot, "tasks", taskID, "output"), SkillSnapshot: domain.SkillSnapshot{ID: uuid.NewString()},
		Inputs: []domain.AssetVersion{manifestVersion(projectID, accountID, domain.AssetSourceScript, source)},
	}
	manifest, err := BuildManifest(input)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Inputs[0].Role != "primary_source" {
		t.Fatalf("role=%q", manifest.Inputs[0].Role)
	}
	wrongProject := uuid.NewString()
	input.Inputs[0].ProjectID = &wrongProject
	if _, err := BuildManifest(input); err == nil {
		t.Fatal("expected project identity spoof rejection")
	}
}

func TestWriteManifestEnforcesManagedRootsAndAtomicNoOverwrite(t *testing.T) {
	root := t.TempDir()
	projectRoot := filepath.Join(root, "projects", uuid.NewString())
	accountRoot := filepath.Join(root, "account-assets")
	obsidianRoot := filepath.Join(root, "vault")
	for _, dir := range []string{projectRoot, accountRoot, obsidianRoot} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	projectID, accountID, taskID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	writeFile := func(path string) {
		if err := os.WriteFile(path, []byte("input"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	spoken := filepath.Join(projectRoot, "spoken.md")
	background := filepath.Join(accountRoot, "background.png")
	writeFile(spoken)
	writeFile(background)
	profile := filepath.Join(projectRoot, "machine-profile.json")
	writeFile(profile)
	manifest, err := BuildManifest(BuildManifestInput{
		Task: domain.CodexTask{ID: taskID}, Project: &domain.Project{ID: projectID, AccountID: accountID},
		Action: domain.ActionMontagePlan, OutputDir: filepath.Join(projectRoot, "tasks", taskID, "output"),
		SkillSnapshot: domain.SkillSnapshot{ID: uuid.NewString()},
		Inputs: []domain.AssetVersion{
			manifestVersion(projectID, accountID, domain.AssetSpokenScript, spoken),
			manifestVersion(projectID, accountID, domain.AssetNarration, spoken),
			manifestVersion(projectID, accountID, domain.AssetSubtitleSRT, spoken),
			manifestVersion("", accountID, domain.AssetAccountBackground, background),
		},
		NonSecretSettings: ManifestSettings{MachineProfilePath: profile},
	})
	if err != nil {
		t.Fatal(err)
	}
	path, err := WriteManifest(manifest, ManifestRoots{Project: projectRoot, AccountAssets: accountRoot, Obsidian: obsidianRoot})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "task_manifest.json" {
		t.Fatalf("path=%s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var written TaskManifest
	if err := json.Unmarshal(data, &written); err != nil {
		t.Fatal(err)
	}
	if written.OutputDir != filepath.Clean(manifest.OutputDir) {
		t.Fatalf("output=%q", written.OutputDir)
	}
	manifest.NonSecretSettings.MediaRoot = "changed"
	if _, err := WriteManifest(manifest, ManifestRoots{Project: projectRoot, AccountAssets: accountRoot, Obsidian: obsidianRoot}); err == nil {
		t.Fatal("expected non-overwrite rejection")
	}
}

func TestWriteManifestRejectsRootAndSymlinkEscapes(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	projectRoot := filepath.Join(root, "project")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(projectRoot, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	inputPath := filepath.Join(link, "input.md")
	if err := os.WriteFile(filepath.Join(outside, "input.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	projectID, accountID := uuid.NewString(), uuid.NewString()
	taskID := uuid.NewString()
	manifest, err := BuildManifest(BuildManifestInput{
		Task: domain.CodexTask{ID: taskID}, Project: &domain.Project{ID: projectID, AccountID: accountID},
		Action: domain.ActionRemixStandard, OutputDir: filepath.Join(projectRoot, "tasks", taskID, "output"),
		SkillSnapshot: domain.SkillSnapshot{ID: uuid.NewString()},
		Inputs:        []domain.AssetVersion{manifestVersion(projectID, accountID, domain.AssetSourceScript, inputPath)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := WriteManifest(manifest, ManifestRoots{Project: projectRoot, AccountAssets: filepath.Join(root, "accounts"), Obsidian: filepath.Join(root, "vault")}); err == nil {
		t.Fatal("expected symlink escape rejection")
	}
}

func TestWriteManifestPropagatesAtomicFileStepFailuresWithoutPublishing(t *testing.T) {
	for _, tt := range []struct {
		name   string
		inject func(*manifestFileOperations)
	}{
		{"chmod", func(ops *manifestFileOperations) {
			ops.chmod = func(*os.File, os.FileMode) error { return errors.New("forced chmod failure") }
		}},
		{"write", func(ops *manifestFileOperations) {
			ops.write = func(*os.File, []byte) (int, error) { return 0, errors.New("forced write failure") }
		}},
		{"short write", func(ops *manifestFileOperations) {
			ops.write = func(_ *os.File, data []byte) (int, error) { return len(data) - 1, nil }
		}},
		{"sync", func(ops *manifestFileOperations) {
			ops.sync = func(*os.File) error { return errors.New("forced sync failure") }
		}},
		{"close", func(ops *manifestFileOperations) {
			ops.close = func(file *os.File) error { _ = file.Close(); return errors.New("forced close failure") }
		}},
		{"link conflict", func(ops *manifestFileOperations) {
			ops.link = func(string, string) error { return os.ErrExist }
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			projectID, accountID, taskID := uuid.NewString(), uuid.NewString(), uuid.NewString()
			source := filepath.Join(root, "source.md")
			if err := os.WriteFile(source, []byte("input"), 0o600); err != nil {
				t.Fatal(err)
			}
			manifest, err := BuildManifest(BuildManifestInput{
				Task: domain.CodexTask{ID: taskID}, Project: &domain.Project{ID: projectID, AccountID: accountID}, Action: domain.ActionRemixStandard,
				OutputDir: filepath.Join(root, "tasks", taskID, "output"), SkillSnapshot: domain.SkillSnapshot{ID: uuid.NewString()}, Inputs: []domain.AssetVersion{manifestVersion(projectID, accountID, domain.AssetSourceScript, source)},
			})
			if err != nil {
				t.Fatal(err)
			}
			original := manifestFileOps
			t.Cleanup(func() { manifestFileOps = original })
			injected := original
			tt.inject(&injected)
			manifestFileOps = injected
			if _, err := WriteManifest(manifest, ManifestRoots{Project: root}); err == nil {
				t.Fatal("expected injected file error")
			}
			finalPath := filepath.Join(root, "tasks", taskID, "task_manifest.json")
			if _, err := os.Stat(finalPath); !os.IsNotExist(err) {
				t.Fatalf("failed write published manifest: %v", err)
			}
			matches, err := filepath.Glob(filepath.Join(root, "tasks", taskID, ".task_manifest-*.tmp"))
			if err != nil || len(matches) != 0 {
				t.Fatalf("temporary files left behind: %v, %v", matches, err)
			}
		})
	}
}

func TestWriteManifestRejectsApprovalModeOutsideSchemaEnum(t *testing.T) {
	root := t.TempDir()
	projectID, accountID := uuid.NewString(), uuid.NewString()
	source := filepath.Join(root, "source.md")
	if err := os.WriteFile(source, []byte("input"), 0o600); err != nil {
		t.Fatal(err)
	}
	taskID := uuid.NewString()
	manifest, err := BuildManifest(BuildManifestInput{
		Task: domain.CodexTask{ID: taskID}, Project: &domain.Project{ID: projectID, AccountID: accountID}, Action: domain.ActionRemixStandard,
		OutputDir: filepath.Join(root, "tasks", taskID, "output"), SkillSnapshot: domain.SkillSnapshot{ID: uuid.NewString()}, Inputs: []domain.AssetVersion{manifestVersion(projectID, accountID, domain.AssetSourceScript, source)},
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest.ApprovalMode = "launch_ui_without_plan"
	if _, err := WriteManifest(manifest, ManifestRoots{Project: root}); err == nil {
		t.Fatal("expected approval_mode enum rejection")
	}
}

func TestTaskManifestSchemaConstrainsExactTypeRolePairs(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "schemas", "task-manifest.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	properties := schema["properties"].(map[string]any)
	inputs := properties["inputs"].(map[string]any)
	items := inputs["items"].(map[string]any)
	pairs, ok := items["oneOf"].([]any)
	if !ok {
		t.Fatal("input schema must define oneOf exact type/role pairs")
	}
	found := false
	for _, raw := range pairs {
		pair := raw.(map[string]any)["properties"].(map[string]any)
		typ := pair["type"].(map[string]any)["const"]
		role := pair["role"].(map[string]any)["const"]
		if typ == "source_script" && role == "primary_source" {
			found = true
		}
		if typ == "source_script" && role != "primary_source" {
			t.Fatalf("source_script permits role %v", role)
		}
	}
	if !found {
		t.Fatal("source_script/primary_source pair missing")
	}
}

func TestBuildAndWriteManifestRequireDedicatedCanonicalOutputDirectory(t *testing.T) {
	root := t.TempDir()
	projectID, accountID, taskID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	source := filepath.Join(root, "source.md")
	if err := os.WriteFile(source, []byte("input"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := BuildManifestInput{
		Task: domain.CodexTask{ID: taskID}, Project: &domain.Project{ID: projectID, AccountID: accountID}, Action: domain.ActionRemixStandard,
		OutputDir: root, SkillSnapshot: domain.SkillSnapshot{ID: uuid.NewString()}, Inputs: []domain.AssetVersion{manifestVersion(projectID, accountID, domain.AssetSourceScript, source)},
	}
	if _, err := BuildManifest(base); err == nil {
		t.Fatal("BuildManifest accepted project root as output_dir")
	}
	base.OutputDir = filepath.Join(root, "tasks", taskID, "output")
	manifest, err := BuildManifest(base)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := resolvePath(base.OutputDir)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.OutputDir != expected {
		t.Fatalf("output_dir=%q want canonical %q", manifest.OutputDir, expected)
	}
	for _, invalid := range []string{root, filepath.Join(root, "tasks", taskID), source, filepath.Join(root, "tasks", taskID, "task_manifest.json")} {
		copy := manifest
		copy.OutputDir = invalid
		if _, err := WriteManifest(copy, ManifestRoots{Project: root}); err == nil {
			t.Fatalf("WriteManifest accepted output_dir %q", invalid)
		}
	}
}

func TestWriteManifestRejectsInputOverlappingDedicatedOutput(t *testing.T) {
	root := t.TempDir()
	projectID, accountID, taskID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	output := filepath.Join(root, "tasks", taskID, "output")
	if err := os.MkdirAll(output, 0o755); err != nil {
		t.Fatal(err)
	}
	overlap := filepath.Join(output, "source.md")
	if err := os.WriteFile(overlap, []byte("input"), 0o600); err != nil {
		t.Fatal(err)
	}
	overlappingInput := BuildManifestInput{
		Task: domain.CodexTask{ID: taskID}, Project: &domain.Project{ID: projectID, AccountID: accountID}, Action: domain.ActionRemixStandard,
		OutputDir: output, SkillSnapshot: domain.SkillSnapshot{ID: uuid.NewString()}, Inputs: []domain.AssetVersion{manifestVersion(projectID, accountID, domain.AssetSourceScript, overlap)},
	}
	if _, err := BuildManifest(overlappingInput); err == nil {
		t.Fatal("BuildManifest accepted input/output overlap")
	}
	manifestPath := filepath.Join(root, "tasks", taskID, "task_manifest.json")
	if err := os.WriteFile(manifestPath, []byte("not a manifest"), 0o600); err != nil {
		t.Fatal(err)
	}
	overlappingInput.Inputs = []domain.AssetVersion{manifestVersion(projectID, accountID, domain.AssetSourceScript, manifestPath)}
	if _, err := BuildManifest(overlappingInput); err == nil {
		t.Fatal("BuildManifest accepted input/manifest overlap")
	}
	if err := os.Remove(manifestPath); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "source.md")
	if err := os.WriteFile(source, []byte("input"), 0o600); err != nil {
		t.Fatal(err)
	}
	overlappingInput.Inputs = []domain.AssetVersion{manifestVersion(projectID, accountID, domain.AssetSourceScript, source)}
	manifest, err := BuildManifest(overlappingInput)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Inputs[0].Path = overlap
	if _, err := WriteManifest(manifest, ManifestRoots{Project: root}); err == nil {
		t.Fatal("expected input/output overlap rejection")
	}
}

func TestTaskManifestSchemaUsesFlatPublicSettingsAllowlist(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "schemas", "task-manifest.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	settings := schema["properties"].(map[string]any)["non_secret_settings"].(map[string]any)
	if settings["additionalProperties"] != false {
		t.Fatal("non_secret_settings must reject unknown/nested properties")
	}
	properties, ok := settings["properties"].(map[string]any)
	if !ok {
		t.Fatal("non_secret_settings public property allowlist missing")
	}
	for _, key := range []string{"baokuan_base_url", "obsidian_vault", "topic_cards_dir", "grok_model", "media_root", "jianying_root"} {
		if _, ok := properties[key]; !ok {
			t.Fatalf("public setting %q missing", key)
		}
	}
	for _, forbidden := range []string{"api_key", "token", "password", "authorization"} {
		if _, ok := properties[forbidden]; ok {
			t.Fatalf("credential setting %q allowed", forbidden)
		}
	}
}

func TestTaskManifestSchemaConstrainsDedicatedOutputShape(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "schemas", "task-manifest.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	output := schema["properties"].(map[string]any)["output_dir"].(map[string]any)
	if _, ok := output["pattern"].(string); !ok {
		t.Fatal("output_dir schema lacks dedicated task output pattern")
	}
}

func TestBuildManifestAddsTopicActionSettingsAndEngineeringCandidateInput(t *testing.T) {
	root := t.TempDir()
	projectID, accountID, taskID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	output := filepath.Join(root, "tasks", taskID, "output")
	candidates := filepath.Join(root, "candidates.json")
	if err := os.WriteFile(candidates, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := BuildManifest(BuildManifestInput{Task: domain.CodexTask{ID: taskID}, Project: &domain.Project{ID: projectID, AccountID: accountID}, Action: domain.ActionTopicCommit, OutputDir: output, ExpectedOutputs: []ExpectedOutput{{Type: "topic_card", Required: true, Description: "card"}}, SkillSnapshot: domain.SkillSnapshot{ID: uuid.NewString()}, NonSecretSettings: ManifestSettings{SessionID: "session-1", CandidateID: "candidate-1", TopicCandidatesPath: candidates, ObsidianVault: root, TopicCardsDir: "cards"}})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.NonSecretSettings.SessionID != "session-1" || manifest.NonSecretSettings.CandidateID != "candidate-1" || manifest.NonSecretSettings.TopicCandidatesPath == "" || len(manifest.EngineeringInputs) != 1 || manifest.EngineeringInputs[0].Type != "topic_candidates" {
		t.Fatalf("topic commit contract missing: %#v", manifest)
	}
}

func TestBuildManifestRejectsExpectedOutputOutsideActionAllowlist(t *testing.T) {
	root, taskID := t.TempDir(), uuid.NewString()
	_, err := BuildManifest(BuildManifestInput{Task: domain.CodexTask{ID: taskID}, Action: domain.ActionTopicBrainstorm, OutputDir: filepath.Join(root, "tasks", taskID, "output"), ExpectedOutputs: []ExpectedOutput{{Type: "mix_draft", Required: true}}, SkillSnapshot: domain.SkillSnapshot{ID: uuid.NewString()}, NonSecretSettings: ManifestSettings{SessionID: "session-1"}})
	if err == nil {
		t.Fatal("topic action accepted montage expected output")
	}
}

func TestWriteManifestCreatesOutputAndRejectsUnsafeProjectRoot(t *testing.T) {
	root := t.TempDir()
	projectID, accountID, taskID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	output := filepath.Join(root, "tasks", taskID, "output")
	manifest, err := BuildManifest(BuildManifestInput{Task: domain.CodexTask{ID: taskID}, Project: &domain.Project{ID: projectID, AccountID: accountID}, Action: domain.ActionTopicBrainstorm, OutputDir: output, ExpectedOutputs: []ExpectedOutput{{Type: "topic_candidates", Required: true, Description: "candidates"}}, SkillSnapshot: domain.SkillSnapshot{ID: uuid.NewString()}, NonSecretSettings: ManifestSettings{SessionID: "session-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := WriteManifest(manifest, ManifestRoots{Project: root}); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(output); err != nil || !info.IsDir() {
		t.Fatalf("WriteManifest did not create output_dir: %v", err)
	}
	fileRoot := filepath.Join(root, "project-file")
	if err := os.WriteFile(fileRoot, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteManifest(manifest, ManifestRoots{Project: fileRoot}); err == nil {
		t.Fatal("ordinary file accepted as project root")
	}
}

func TestWriteManifestRejectsOutputDirectorySymlinkEscape(t *testing.T) {
	root, outside, taskID := t.TempDir(), t.TempDir(), uuid.NewString()
	taskDir := filepath.Join(root, "tasks", taskID)
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(taskDir, "output")
	if err := os.Symlink(outside, output); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	manifest := TaskManifest{SchemaVersion: ProtocolSchemaVersion, TaskID: taskID, JobID: taskID, Skill: "finance-topic-selector", Action: domain.ActionTopicBrainstorm, Inputs: []ManifestInput{}, EngineeringInputs: []EngineeringInput{}, OutputDir: output, ExpectedOutputs: []ExpectedOutput{{Type: "topic_candidates", Required: true}}, ApprovalMode: "plan_then_wait", SkillSnapshotID: uuid.NewString(), NonSecretSettings: ManifestSettings{SessionID: "session-1"}}
	if _, err := WriteManifest(manifest, ManifestRoots{Project: root}); err == nil {
		t.Fatal("output_dir symlink escape accepted")
	}
}

func manifestVersion(projectID, accountID string, typ domain.AssetType, path string) domain.AssetVersion {
	project := (*string)(nil)
	if projectID != "" {
		project = &projectID
	}
	data, _ := os.ReadFile(path)
	digest := sha256.Sum256(data)
	return domain.AssetVersion{ID: uuid.NewString(), AssetID: uuid.NewString(), ProjectID: project, AccountID: accountID, Type: typ, Version: 2, StorageKind: domain.StorageFile, Path: path, Filename: filepath.Base(path), MIMEType: "application/octet-stream", Size: int64(len(data)), SHA256: hex.EncodeToString(digest[:]), State: domain.AssetReady}
}

func manifestHasRole(manifest TaskManifest, role string) bool {
	for _, input := range manifest.Inputs {
		if input.Role == role {
			return true
		}
	}
	return false
}
