package codex

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"video-production-console/internal/domain"
)

func TestValidateResultEnvelopeAcceptsExecutionTimingsReceipt(t *testing.T) {
	out := t.TempDir()
	path := filepath.Join(out, "execution-timings.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":"1.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	taskID := uuid.NewString()
	envelope := ResultEnvelope{
		SchemaVersion: ProtocolSchemaVersion, TaskID: taskID, Action: domain.ActionMontageExecute,
		Status: "completed", Summary: "done", Questions: []Question{}, AssetOutputs: []AssetOutput{}, Warnings: []string{},
		Artifacts: []ArtifactOutput{{Type: "execution_timings", Path: path, Description: "file", RelativePath: "execution-timings.json", SHA256: sha256HexForTest(t, path)}},
	}
	if err := ValidateResultEnvelope(envelope, taskID, envelope.Action, out); err != nil {
		t.Fatal(err)
	}
	envelope.Artifacts[0].RelativePath = "../execution-timings.json"
	if err := ValidateResultEnvelope(envelope, taskID, envelope.Action, out); err == nil {
		t.Fatal("execution timing path escape accepted")
	}
	envelope.Artifacts[0].RelativePath = "execution-timings.json"
	envelope.Artifacts[0].SHA256 = strings.Repeat("0", 64)
	if err := ValidateResultEnvelope(envelope, taskID, envelope.Action, out); err == nil {
		t.Fatal("execution timing sha mismatch accepted")
	}
}

func TestValidateResultEnvelopeAcceptsCompletedAndTransitionStatus(t *testing.T) {
	out := t.TempDir()
	file := filepath.Join(out, "script.md")
	if err := os.WriteFile(file, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	taskID := uuid.NewString()
	envelope := ResultEnvelope{SchemaVersion: ProtocolSchemaVersion, TaskID: taskID, Action: domain.ActionRemixEnhanced, Status: "completed", Summary: "done", Questions: []Question{}, Artifacts: []ArtifactOutput{}, AssetOutputs: []AssetOutput{{Type: domain.AssetContinuousScript, Path: file, StorageKind: domain.StorageFile, Filename: "script.md", MIME: "text/markdown", Size: 5, SHA256: sha256HexForTest(t, file)}}, Warnings: []string{}}
	if err := ValidateResultEnvelope(envelope, taskID, domain.ActionRemixEnhanced, out); err != nil {
		t.Fatal(err)
	}
	envelope.Status = "needs_input"
	envelope.Questions = []Question{{Text: "Pick one", Options: []string{"a"}}}
	envelope.AssetOutputs = []AssetOutput{}
	data, _ := json.Marshal(envelope)
	got, err := ValidateResultEnvelopeJSON(data, taskID, domain.ActionRemixEnhanced, out)
	if err != nil || got.Status != "awaiting_input" {
		t.Fatalf("got=%#v err=%v", got, err)
	}
}

func TestValidateResultEnvelopeAcceptsWindowsExtendedLengthArtifactPath(t *testing.T) {
	if filepath.Separator != '\\' {
		t.Skip("Windows path semantics only")
	}
	out := t.TempDir()
	file := filepath.Join(out, "script.md")
	if err := os.WriteFile(file, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	extended := `\\?\` + file
	taskID := uuid.NewString()
	envelope := ResultEnvelope{SchemaVersion: ProtocolSchemaVersion, TaskID: taskID, Action: domain.ActionRemixEnhanced, Status: "completed", Summary: "done", Questions: []Question{}, Artifacts: []ArtifactOutput{}, AssetOutputs: []AssetOutput{{Type: domain.AssetContinuousScript, Path: extended, StorageKind: domain.StorageFile, Filename: "script.md", MIME: "text/markdown", Size: 5, SHA256: sha256HexForTest(t, file)}}, Warnings: []string{}}
	if err := ValidateResultEnvelope(envelope, taskID, envelope.Action, out); err != nil {
		t.Fatalf("extended-length path should be accepted: %v", err)
	}
}

func TestValidateResultEnvelopeRejectsInvalidContract(t *testing.T) {
	out, taskID := t.TempDir(), uuid.NewString()
	base := ResultEnvelope{SchemaVersion: ProtocolSchemaVersion, TaskID: taskID, Action: domain.ActionMontagePlan, Status: "completed", Summary: "ok", Questions: []Question{}, Artifacts: []ArtifactOutput{}, AssetOutputs: []AssetOutput{}, Warnings: []string{}}
	tests := []struct {
		name   string
		mutate func(*ResultEnvelope)
	}{
		{"missing schema", func(v *ResultEnvelope) { v.SchemaVersion = "" }},
		{"task mismatch", func(v *ResultEnvelope) { v.TaskID = uuid.NewString() }},
		{"action mismatch", func(v *ResultEnvelope) { v.Action = domain.ActionRemixStandard }},
		{"awaiting without question", func(v *ResultEnvelope) { v.Status = "awaiting_input" }},
		{"unknown asset type", func(v *ResultEnvelope) {
			v.AssetOutputs = []AssetOutput{{Type: domain.AssetType("debug_log"), Path: filepath.Join(out, "x")}}
		}},
		{"outside artifact", func(v *ResultEnvelope) {
			v.Artifacts = []ArtifactOutput{{Type: "report", Path: filepath.Join(out, "..", "escape.txt"), Description: "x"}}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := base
			tt.mutate(&got)
			if err := ValidateResultEnvelope(got, taskID, domain.ActionMontagePlan, out); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
	data, _ := json.Marshal(base)
	data = []byte(strings.TrimSuffix(string(data), "}") + `,"unexpected":true}`)
	if _, err := ValidateResultEnvelopeJSON(data, taskID, domain.ActionMontagePlan, out); err == nil {
		t.Fatal("expected unknown field rejection")
	}
	missingSummary := []byte(`{"schema_version":"2.0","task_id":"` + taskID + `","action":"montage.plan","status":"completed","questions":[],"artifacts":[],"asset_outputs":[],"warnings":[]}`)
	if _, err := ValidateResultEnvelopeJSON(missingSummary, taskID, domain.ActionMontagePlan, out); err == nil {
		t.Fatal("expected missing exact field rejection")
	}
}

func TestValidateResultEnvelopeAllowsOnlyMontageExecutePlaintextWorkspaceDirectory(t *testing.T) {
	out := t.TempDir()
	workspace := filepath.Join(out, "plaintext_workspace")
	if err := os.Mkdir(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "draft.json"), []byte(`{"ok":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	digest, err := HashResultDirectory(workspace)
	if err != nil {
		t.Fatal(err)
	}
	taskID := uuid.NewString()
	envelope := ResultEnvelope{SchemaVersion: ProtocolSchemaVersion, TaskID: taskID, Action: domain.ActionMontageExecute, Status: "completed", Summary: "done", Questions: []Question{}, Artifacts: []ArtifactOutput{{Type: "plaintext_workspace", Path: workspace, Description: "directory", SHA256: digest}}, AssetOutputs: []AssetOutput{}, Warnings: []string{}}
	if err := ValidateResultEnvelope(envelope, taskID, envelope.Action, out); err != nil {
		t.Fatal(err)
	}
	envelope.Artifacts[0].SHA256 = strings.Repeat("0", 64)
	if err := ValidateResultEnvelope(envelope, taskID, envelope.Action, out); err == nil {
		t.Fatal("expected plaintext workspace hash mismatch")
	}
	envelope.Artifacts[0].SHA256 = digest
	envelope.Action = domain.ActionMontagePlan
	if err := ValidateResultEnvelope(envelope, taskID, envelope.Action, out); err == nil {
		t.Fatal("montage.plan must not accept a directory artifact")
	}
}

func TestValidateResultEnvelopeJSONAcceptsPlaintextWorkspacePresenceMetadata(t *testing.T) {
	out := t.TempDir()
	workspace := filepath.Join(out, "workspace")
	if err := os.Mkdir(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "draft_content.json"), []byte(`{"ok":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	taskID := uuid.NewString()
	data := []byte(`{"schema_version":"2.0","task_id":"` + taskID + `","action":"montage.execute","status":"completed","summary":"ok","questions":[],"artifacts":[{"type":"plaintext_workspace","kind":"directory","path":` + mustJSONQuote(t, workspace) + `,"metadata":{"narration_present":true,"bgm_present":true,"sfx_present":true,"transitions_present":true}}],"asset_outputs":[],"warnings":[]}`)
	if _, err := ValidateResultEnvelopeJSON(data, taskID, domain.ActionMontageExecute, out); err != nil {
		t.Fatalf("plaintext workspace presence metadata should be accepted: %v", err)
	}

	badType := strings.Replace(string(data), `"type":"plaintext_workspace"`, `"type":"production_plan"`, 1)
	if _, err := ValidateResultEnvelopeJSON([]byte(badType), taskID, domain.ActionMontageExecute, out); err == nil {
		t.Fatal("metadata on a non-workspace artifact must be rejected")
	}
	missingPresence := strings.Replace(string(data), `,"transitions_present":true`, "", 1)
	if _, err := ValidateResultEnvelopeJSON([]byte(missingPresence), taskID, domain.ActionMontageExecute, out); err == nil {
		t.Fatal("incomplete workspace presence metadata must be rejected")
	}
}

func TestValidateResultEnvelopeRejectsMontageExecuteMixDraftAssetOutput(t *testing.T) {
	out, taskID := t.TempDir(), uuid.NewString()
	plan := filepath.Join(out, "production_plan.json")
	if err := os.WriteFile(plan, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(out, "workspace")
	draft := filepath.Join(out, "draft")
	for _, dir := range []string{workspace, draft} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	workspaceHash, err := HashResultDirectory(workspace)
	if err != nil {
		t.Fatal(err)
	}
	draftHash, err := HashResultDirectory(draft)
	if err != nil {
		t.Fatal(err)
	}
	envelope := ResultEnvelope{
		SchemaVersion: ProtocolSchemaVersion,
		TaskID:        taskID,
		Action:        domain.ActionMontageExecute,
		Status:        "completed",
		Summary:       "ok",
		Questions:     []Question{},
		Artifacts: []ArtifactOutput{
			{Type: "production_plan", Path: plan, Description: "file"},
			{Type: "plaintext_workspace", Path: workspace, Description: "directory", SHA256: workspaceHash},
		},
		AssetOutputs: []AssetOutput{{Type: domain.AssetMixDraft, Path: draft, StorageKind: domain.StorageDirectory, Filename: "draft", MIME: "inode/directory", SHA256: draftHash}},
		Warnings:     []string{},
	}
	err = ValidateResultEnvelope(envelope, taskID, envelope.Action, out)
	if err == nil {
		t.Fatal("montage.execute must reject a Codex-produced mix_draft asset")
	}
	if got, want := err.Error(), `asset output 0 type "mix_draft" is not allowed for action "montage.execute"`; got != want {
		t.Fatalf("rejection = %q, want action/type allowlist error %q", got, want)
	}
}

func TestValidateResultEnvelopeRejectsArtifactDirectoryAssetPathOverlap(t *testing.T) {
	out, taskID := t.TempDir(), uuid.NewString()
	draft := filepath.Join(out, "draft")
	child := filepath.Join(draft, "engineering", "report.json")
	if err := os.MkdirAll(filepath.Dir(child), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(child, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := ResultEnvelope{SchemaVersion: ProtocolSchemaVersion, TaskID: taskID, Action: domain.ActionMontageExecute, Status: "completed", Summary: "ok", Questions: []Question{}, Warnings: []string{}}
	base.AssetOutputs = []AssetOutput{{Type: domain.AssetMixDraft, Path: draft, StorageKind: domain.StorageDirectory, Filename: "draft", MIME: "inode/directory"}}
	base.Artifacts = []ArtifactOutput{{Type: "report", Path: child, Description: "engineering report"}}
	if err := ValidateResultEnvelope(base, taskID, base.Action, out); err == nil {
		t.Fatal("expected artifact descendant overlap rejection")
	}
	base.Artifacts[0].Path = out
	if err := ValidateResultEnvelope(base, taskID, base.Action, out); err == nil {
		t.Fatal("expected artifact ancestor overlap rejection")
	}
}

func TestValidateResultEnvelopeRequiresNonEmptyQuestionOptions(t *testing.T) {
	out, taskID := t.TempDir(), uuid.NewString()
	base := ResultEnvelope{SchemaVersion: ProtocolSchemaVersion, TaskID: taskID, Action: domain.ActionTopicBrainstorm, Status: "awaiting_input", Summary: "choose", Artifacts: []ArtifactOutput{}, AssetOutputs: []AssetOutput{}, Warnings: []string{}}
	for _, options := range [][]string{{}, {""}, {"valid", "  "}} {
		base.Questions = []Question{{Text: "Pick", Options: options}}
		if err := ValidateResultEnvelope(base, taskID, base.Action, out); err == nil {
			t.Fatalf("expected options %#v rejection", options)
		}
	}
}

func TestValidateResultEnvelopeJSONRejectsExplicitDirectorySHA256(t *testing.T) {
	out, taskID := t.TempDir(), uuid.NewString()
	draft := filepath.Join(out, "draft")
	if err := os.MkdirAll(draft, 0o755); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"schema_version":"2.0","task_id":"` + taskID + `","action":"montage.execute","status":"completed","summary":"ok","questions":[],"artifacts":[],"asset_outputs":[{"type":"mix_draft","path":` + mustJSONQuote(t, draft) + `,"storage_kind":"directory","filename":"draft","mime":"inode/directory","size":0,"sha256":""}],"warnings":[]}`)
	if _, err := ValidateResultEnvelopeJSON(data, taskID, domain.ActionMontageExecute, out); err == nil {
		t.Fatal("expected explicit directory sha256 field rejection")
	}
}

func TestValidateResultEnvelopeJSONRejectsCaseVariantUnknownFields(t *testing.T) {
	out, taskID := t.TempDir(), uuid.NewString()
	draft := filepath.Join(out, "draft")
	if err := os.MkdirAll(draft, 0o755); err != nil {
		t.Fatal(err)
	}
	dirHash, err := HashResultDirectory(draft)
	if err != nil {
		t.Fatal(err)
	}
	base := `{"schema_version":"2.0","task_id":"` + taskID + `","action":"montage.execute","status":"completed","summary":"ok","questions":[],"artifacts":[],"asset_outputs":[{"type":"mix_draft","path":` + mustJSONQuote(t, draft) + `,"storage_kind":"directory","filename":"draft","mime":"inode/directory","size":0,"sha256":` + mustJSONQuote(t, dirHash) + `}],"warnings":[]}`
	cases := []string{
		strings.Replace(base, `"size":0`, `"size":0,"SHA256":""`, 1),
		strings.Replace(base, `"status":"completed"`, `"status":"completed","Status":"failed"`, 1),
		strings.Replace(base, `"questions":[]`, `"questions":[{"text":"Pick","Text":"override","options":["a"]}]`, 1),
	}
	for i, data := range cases {
		if _, err := ValidateResultEnvelopeJSON([]byte(data), taskID, domain.ActionMontageExecute, out); err == nil {
			t.Fatalf("case %d: expected case-variant unknown field rejection", i)
		}
	}
}

func TestValidateResultEnvelopeRejectsAnyArtifactAssetOrAssetAssetOverlap(t *testing.T) {
	out, taskID := t.TempDir(), uuid.NewString()
	dir := filepath.Join(out, "draft")
	child := filepath.Join(dir, "video.mp4")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(child, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	dirHash, err := HashResultDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	base := ResultEnvelope{SchemaVersion: ProtocolSchemaVersion, TaskID: taskID, Action: domain.ActionMontageExecute, Status: "completed", Summary: "ok", Questions: []Question{}, Warnings: []string{}, Artifacts: []ArtifactOutput{}}
	base.AssetOutputs = []AssetOutput{
		{Type: domain.AssetMixDraft, Path: dir, StorageKind: domain.StorageDirectory, Filename: "draft", MIME: "inode/directory", SHA256: dirHash},
		{Type: domain.AssetFinalVideo, Path: child, StorageKind: domain.StorageFile, Filename: "video.mp4", MIME: "video/mp4", Size: 5, SHA256: sha256HexForTest(t, child)},
	}
	if err := ValidateResultEnvelope(base, taskID, base.Action, out); err == nil {
		t.Fatal("expected file-in-directory asset overlap rejection")
	}
	base.AssetOutputs = base.AssetOutputs[:1]
	base.Artifacts = []ArtifactOutput{{Type: "report", Path: child, Description: "artifact in asset dir"}}
	if err := ValidateResultEnvelope(base, taskID, base.Action, out); err == nil {
		t.Fatal("expected artifact/asset overlap rejection")
	}
}

func TestValidateResultEnvelopeJSONRejectsDuplicateKeysAndNulls(t *testing.T) {
	out, taskID := t.TempDir(), uuid.NewString()
	base := `{"schema_version":"2.0","task_id":"` + taskID + `","action":"topic.brainstorm","status":"completed","summary":"ok","questions":[],"artifacts":[],"asset_outputs":[],"warnings":[]}`
	cases := []string{
		strings.Replace(base, `"summary":"ok"`, `"summary":"ok","summary":"override"`, 1),
		strings.Replace(base, `"questions":[]`, `"questions":[{"text":"Pick","text":"Override","options":["a"]}]`, 1),
		strings.Replace(base, `"summary":"ok"`, `"summary":null`, 1),
		strings.Replace(base, `"warnings":[]`, `"warnings":null`, 1),
		strings.Replace(base, `"questions":[]`, `"questions":null`, 1),
		strings.Replace(base, `"warnings":[]`, `"warnings":[null]`, 1),
	}
	for i, data := range cases {
		if _, err := ValidateResultEnvelopeJSON([]byte(data), taskID, domain.ActionTopicBrainstorm, out); err == nil {
			t.Fatalf("case %d accepted invalid strict JSON", i)
		}
	}
}

func TestValidateResultEnvelopeVerifiesFileAndDirectoryContentHashes(t *testing.T) {
	out, taskID := t.TempDir(), uuid.NewString()
	file := filepath.Join(out, "script.md")
	if err := os.WriteFile(file, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(out, "draft")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plan.json"), []byte("plan"), 0o600); err != nil {
		t.Fatal(err)
	}
	dirHash, err := HashResultDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	base := ResultEnvelope{SchemaVersion: ProtocolSchemaVersion, TaskID: taskID, Action: domain.ActionMontageExecute, Status: "completed", Summary: "ok", Questions: []Question{}, Artifacts: []ArtifactOutput{}, Warnings: []string{}}
	base.AssetOutputs = []AssetOutput{{Type: domain.AssetContinuousScript, Path: file, StorageKind: domain.StorageFile, Filename: "script.md", MIME: "text/markdown", Size: 8, SHA256: sha256HexForTest(t, file)}}
	if err := os.WriteFile(file, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateResultEnvelope(base, taskID, base.Action, out); err == nil {
		t.Fatal("expected tampered file hash rejection")
	}
	base.AssetOutputs = []AssetOutput{{Type: domain.AssetMixDraft, Path: dir, StorageKind: domain.StorageDirectory, Filename: "draft", MIME: "inode/directory", SHA256: dirHash}}
	if err := os.WriteFile(filepath.Join(dir, "extra.txt"), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateResultEnvelope(base, taskID, base.Action, out); err == nil {
		t.Fatal("expected tampered directory hash rejection")
	}
}

func TestValidateResultEnvelopeNormalizesMontageCompatibilityFields(t *testing.T) {
	out, taskID := t.TempDir(), uuid.NewString()
	workspace := filepath.Join(out, "workspace", taskID)
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	plan := filepath.Join(out, "production_plan.json")
	if err := os.WriteFile(plan, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"schema_version":"2.0","task_id":"` + taskID + `","action":"montage.execute","status":"completed","summary":"ok","questions":[],"artifacts":[{"type":"production_plan","path":` + mustJSONQuote(t, plan) + `,"kind":"file"},{"type":"plaintext_workspace","path":` + mustJSONQuote(t, workspace) + `,"kind":"directory","metadata":{"narration_present":true,"bgm_present":true,"sfx_present":true,"transitions_present":true}}],"asset_outputs":[],"warnings":[]}`)
	got, err := ValidateResultEnvelopeJSON(data, taskID, domain.ActionMontageExecute, out)
	if err != nil {
		t.Fatal(err)
	}
	if got.Artifacts[0].Description != "file" || got.Artifacts[1].Description != "directory" || len(got.AssetOutputs) != 0 {
		t.Fatalf("compatibility result was not normalized: %#v", got)
	}
}

func TestValidateResultEnvelopeNormalizesStringQuestionsAndRejectsAssetsBeforeCompletion(t *testing.T) {
	out, taskID := t.TempDir(), uuid.NewString()
	data := []byte(`{"schema_version":"2.0","task_id":"` + taskID + `","action":"montage.plan","status":"awaiting_input","summary":"need profile","questions":["Provide machine profile."],"artifacts":[],"asset_outputs":[],"warnings":[]}`)
	got, err := ValidateResultEnvelopeJSON(data, taskID, domain.ActionMontagePlan, out)
	if err != nil {
		t.Fatal(err)
	}
	if got.Questions[0].Text != "Provide machine profile." || len(got.Questions[0].Options) != 1 {
		t.Fatalf("string question was not normalized: %#v", got.Questions)
	}
	file := filepath.Join(out, "script.txt")
	if err := os.WriteFile(file, []byte("script"), 0o600); err != nil {
		t.Fatal(err)
	}
	data = []byte(`{"schema_version":"2.0","task_id":"` + taskID + `","action":"remix.standard","status":"failed","summary":"failed","questions":[],"artifacts":[],"asset_outputs":[{"type":"continuous_script","path":` + mustJSONQuote(t, file) + `,"storage_kind":"file","filename":"script.txt","mime":"text/plain","size":6,"sha256":` + mustJSONQuote(t, sha256HexForTest(t, file)) + `}],"warnings":[]}`)
	if _, err := ValidateResultEnvelopeJSON(data, taskID, domain.ActionRemixStandard, out); err == nil {
		t.Fatal("failed result registered a formal asset")
	}
}

func TestValidateResultEnvelopeTopicCardUsesVaultReceiptAndActionMatrix(t *testing.T) {
	out, vault, taskID := t.TempDir(), t.TempDir(), uuid.NewString()
	cards := filepath.Join(vault, "cards")
	if err := os.MkdirAll(cards, 0o755); err != nil {
		t.Fatal(err)
	}
	card := filepath.Join(cards, "card.md")
	if err := os.WriteFile(card, []byte("# card"), 0o600); err != nil {
		t.Fatal(err)
	}
	envelope := ResultEnvelope{SchemaVersion: ProtocolSchemaVersion, TaskID: taskID, Action: domain.ActionTopicCommit, Status: "completed", Summary: "done", Questions: []Question{}, Artifacts: []ArtifactOutput{{Type: "topic_card", Path: card, RelativePath: "cards/card.md", SHA256: sha256HexForTest(t, card), Description: "card"}}, AssetOutputs: []AssetOutput{}, Warnings: []string{}}
	if err := ValidateResultEnvelopeWithRoots(envelope, taskID, domain.ActionTopicCommit, out, ManifestRoots{Obsidian: vault, TopicCards: cards}); err != nil {
		t.Fatal(err)
	}
	envelope.Artifacts[0].SHA256 = strings.Repeat("0", 64)
	if err := ValidateResultEnvelopeWithRoots(envelope, taskID, domain.ActionTopicCommit, out, ManifestRoots{Obsidian: vault, TopicCards: cards}); err == nil {
		t.Fatal("topic card accepted an invalid receipt hash")
	}
}

func TestValidateResultEnvelopeRejectsActionAssetMismatchAndMalformedMediaType(t *testing.T) {
	out, taskID := t.TempDir(), uuid.NewString()
	file := filepath.Join(out, "script.txt")
	if err := os.WriteFile(file, []byte("script"), 0o600); err != nil {
		t.Fatal(err)
	}
	envelope := ResultEnvelope{SchemaVersion: ProtocolSchemaVersion, TaskID: taskID, Action: domain.ActionMontagePlan, Status: "completed", Summary: "done", Questions: []Question{}, Artifacts: []ArtifactOutput{}, AssetOutputs: []AssetOutput{{Type: domain.AssetContinuousScript, Path: file, StorageKind: domain.StorageFile, Filename: "script.txt", MIME: "text/plain", Size: 6, SHA256: sha256HexForTest(t, file)}}, Warnings: []string{}}
	if err := ValidateResultEnvelope(envelope, taskID, envelope.Action, out); err == nil {
		t.Fatal("montage plan accepted remix asset")
	}
	envelope.Action = domain.ActionRemixStandard
	envelope.AssetOutputs[0].MIME = "text/plain; charset"
	if err := ValidateResultEnvelope(envelope, taskID, envelope.Action, out); err == nil {
		t.Fatal("malformed MIME parameters accepted")
	}
}

func TestValidateResultEnvelopeRejectsSpokenScriptForRemixActions(t *testing.T) {
	out, taskID := t.TempDir(), uuid.NewString()
	file := filepath.Join(out, "spoken_script.txt")
	if err := os.WriteFile(file, []byte("spoken"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, action := range []domain.TaskAction{domain.ActionRemixStandard, domain.ActionRemixEnhanced, domain.ActionRemixFromTopic} {
		t.Run(string(action), func(t *testing.T) {
			envelope := ResultEnvelope{SchemaVersion: ProtocolSchemaVersion, TaskID: taskID, Action: action, Status: "completed", Summary: "done", Questions: []Question{}, Artifacts: []ArtifactOutput{}, AssetOutputs: []AssetOutput{{Type: domain.AssetSpokenScript, Path: file, StorageKind: domain.StorageFile, Filename: "spoken_script.txt", MIME: "text/plain; charset=utf-8", Size: 6, SHA256: sha256HexForTest(t, file)}}, Warnings: []string{}}
			if err := ValidateResultEnvelope(envelope, taskID, action, out); err == nil || !strings.Contains(err.Error(), "spoken_script") {
				t.Fatalf("remix action accepted spoken_script result: %v", err)
			}
		})
	}
}

func TestValidateResultEnvelopeJSONRejectsCompatibilitySpokenScript(t *testing.T) {
	out, taskID := t.TempDir(), uuid.NewString()
	file := filepath.Join(out, "spoken.txt")
	if err := os.WriteFile(file, []byte("spoken"), 0o600); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"schema_version":"2.0","task_id":"` + taskID + `","action":"remix.standard","status":"completed","summary":"done","questions":[],"artifacts":[],"asset_outputs":[{"type":"spoken_script","kind":"file","path":` + mustJSONQuote(t, file) + `,"metadata":{}}],"warnings":[]}`)
	if _, err := ValidateResultEnvelopeJSON(data, taskID, domain.ActionRemixStandard, out); err == nil || !strings.Contains(err.Error(), "spoken_script") {
		t.Fatalf("compatibility result accepted spoken_script: %v", err)
	}
}

func TestValidateResultEnvelopeRejectsSpokenScriptForNonRemixAction(t *testing.T) {
	out, taskID := t.TempDir(), uuid.NewString()
	file := filepath.Join(out, "spoken.txt")
	if err := os.WriteFile(file, []byte("spoken"), 0o600); err != nil {
		t.Fatal(err)
	}
	envelope := ResultEnvelope{SchemaVersion: ProtocolSchemaVersion, TaskID: taskID, Action: domain.ActionTopicBrainstorm, Status: "completed", Summary: "done", Questions: []Question{}, Artifacts: []ArtifactOutput{}, AssetOutputs: []AssetOutput{{Type: domain.AssetSpokenScript, Path: file, StorageKind: domain.StorageFile, Filename: "spoken.txt", MIME: "text/plain", Size: 6, SHA256: sha256HexForTest(t, file)}}, Warnings: []string{}}
	if err := ValidateResultEnvelope(envelope, taskID, domain.ActionTopicBrainstorm, out); err == nil || !strings.Contains(err.Error(), "spoken_script") {
		t.Fatalf("non-remix action accepted spoken_script: %v", err)
	}
}

func TestCompletedResultMustDeliverManifestRequiredOutputs(t *testing.T) {
	out, taskID := t.TempDir(), uuid.NewString()
	manifest := TaskManifest{TaskID: taskID, Action: domain.ActionRemixStandard, OutputDir: out, ExpectedOutputs: []ExpectedOutput{{Type: "continuous_script", Required: true}}}
	envelope := ResultEnvelope{SchemaVersion: ProtocolSchemaVersion, TaskID: taskID, Action: domain.ActionRemixStandard, Status: "completed", Summary: "done", Questions: []Question{}, Artifacts: []ArtifactOutput{}, AssetOutputs: []AssetOutput{}, Warnings: []string{}}
	if err := ValidateResultEnvelopeAgainstManifest(envelope, manifest, ManifestRoots{}); err == nil {
		t.Fatal("completed result omitted required output")
	}
}

func sha256HexForTest(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func mustJSONQuote(t *testing.T, value string) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestSchemasAreStrictAndParseable(t *testing.T) {
	for _, name := range []string{"task-manifest.schema.json", "codex-result.schema.json", "topic-candidates.schema.json"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "schemas", name))
		if err != nil {
			t.Fatal(err)
		}
		var schema map[string]any
		if err := json.Unmarshal(data, &schema); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if schema["additionalProperties"] != false {
			t.Fatalf("%s top-level is not strict", name)
		}
		if !strings.Contains(string(data), `"const": "2.0"`) {
			t.Fatalf("%s does not freeze schema version", name)
		}
	}
	topic, err := os.ReadFile(filepath.Join("..", "..", "schemas", "topic-candidates.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"minItems": 3`, `"maxItems": 5`, `"additionalProperties": false`} {
		if !strings.Contains(string(topic), want) {
			t.Fatalf("topic schema missing %s", want)
		}
	}
}

func TestSpokenConsoleProtocolIsAbsentFromSchemas(t *testing.T) {
	for _, name := range []string{"task-manifest.schema.json", "codex-result.schema.json"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "schemas", name))
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"remix.spoken_format", "spoken_script"} {
			if strings.Contains(string(data), forbidden) {
				t.Fatalf("%s still exposes deprecated console value %q", name, forbidden)
			}
		}
	}
}
