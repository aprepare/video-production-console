package imagevideo

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestValidateRegistrationBindingRejectsScenicInputsAndEscapes(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobID := uuid.NewString()
	projectID := uuid.NewString()
	_, fingerprint, err := BuiltInTemplate()
	if err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(root, "image-projects", projectID, "jobs", jobID, "output", "workspace", jobID)
	outputDir := filepath.Join(root, "image-projects", projectID, "jobs", jobID, "output")
	manifest := filepath.Join(outputDir, "task_manifest.json")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	job := Job{
		ID: jobID, ProjectID: projectID, TemplateVersion: BuiltInTemplateVersion, TemplateFingerprint: fingerprint,
		DraftRelativePath:    filepath.ToSlash(filepath.Join(jobID, "output", "workspace", jobID)),
		DraftFingerprint:     strings.Repeat("ab", 32),
		ManifestRelativePath: filepath.ToSlash(filepath.Join(jobID, "output", "task_manifest.json")),
	}
	writeImageVideoManifest(t, manifest, job, outputDir, "图文视频-"+jobID, "")
	request := RegistrationRequest{
		JobID: jobID, ProjectID: projectID, DisplayName: "图文视频-" + jobID,
		ManifestPath: manifest, WorkspacePath: workspace, OutputDir: outputDir,
		TemplateVersion: BuiltInTemplateVersion, TemplateFingerprint: fingerprint, DraftFingerprint: job.DraftFingerprint,
	}
	if _, err := ValidateRegistrationBinding(root, job, request); err != nil {
		t.Fatalf("valid binding rejected: %v", err)
	}

	bad := request
	bad.OutputDir = filepath.Join(root, "escape")
	if _, err := ValidateRegistrationBinding(root, job, bad); err == nil {
		t.Fatal("escaped output dir must be rejected")
	}

	rewriteJSON(t, manifest, func(value map[string]any) {
		value["inputs"] = []any{map[string]any{"type": "subtitle_srt", "role": "subtitle_srt"}}
	})
	if _, err := ValidateRegistrationBinding(root, job, request); err == nil {
		t.Fatal("scenic subtitle input must be rejected")
	}
}

func TestHostRegistrarPublishesDraftWithoutScenicInputs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobID := uuid.NewString()
	projectID := uuid.NewString()
	_, fingerprint, err := BuiltInTemplate()
	if err != nil {
		t.Fatal(err)
	}
	outputDir := filepath.Join(root, "image-projects", projectID, "jobs", jobID, "output")
	workspace := filepath.Join(outputDir, "workspace", jobID)
	jianying := filepath.Join(root, "jianying")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(jianying, 0o700); err != nil {
		t.Fatal(err)
	}
	content := []byte(`{"duration":4450000,"tracks":[{"name":"主画面"},{"name":"旁白"}],"spoken_captions":false,"editable_titles":false}`)
	if err := os.WriteFile(filepath.Join(workspace, "draft_content.json"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(workspace, "draft_meta_info.json"), map[string]any{"draft_id": "source-draft-id", "draft_name": "temp"})
	displayName := "图文视频-" + jobID
	writeImageVideoManifest(t, filepath.Join(outputDir, "task_manifest.json"), Job{ID: jobID, ProjectID: projectID}, outputDir, displayName, "")

	var validated bool
	registrar, err := NewHostRegistrar(root, jianying, "", func(request RegistrationRequest, receiptPath string) (RegistrationResult, error) {
		validated = true
		data, err := os.ReadFile(receiptPath)
		if err != nil {
			return RegistrationResult{}, err
		}
		if strings.Contains(strings.ToLower(string(data)), "subtitle_srt") || strings.Contains(strings.ToLower(string(data)), "account_background") {
			t.Fatal("receipt must not mention scenic inputs")
		}
		var receipt map[string]any
		if json.Unmarshal(data, &receipt) != nil || receipt["status"] != "completed" {
			t.Fatalf("receipt=%s", data)
		}
		return RegistrationResult{
			RegisteredPath: receipt["registered_path"].(string), ReceiptPath: receiptPath,
			DraftID: receipt["draft_id"].(string), DisplayName: request.DisplayName, DurationUS: 4450000,
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	registrar.WithLockRoot(filepath.Join(root, "locks"))
	result, err := registrar.Register(context.Background(), RegistrationRequest{
		JobID: jobID, ProjectID: projectID, DisplayName: displayName,
		ManifestPath:  filepath.Join(outputDir, "task_manifest.json"),
		WorkspacePath: workspace, OutputDir: outputDir, JianyingRoot: jianying,
		TemplateVersion: BuiltInTemplateVersion, TemplateFingerprint: fingerprint,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !validated {
		t.Fatal("validator was not invoked")
	}
	registered := filepath.Join(jianying, jobID)
	if result.RegisteredPath != registered {
		t.Fatalf("registered path = %s", result.RegisteredPath)
	}
	if _, err := os.Stat(filepath.Join(registered, "draft_content.json")); err != nil {
		t.Fatal(err)
	}
	index, err := os.ReadFile(filepath.Join(jianying, "root_meta_info.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(index), displayName) || !strings.Contains(string(index), result.DraftID) {
		t.Fatalf("root index missing draft: %s", index)
	}
	again, err := registrar.Register(context.Background(), RegistrationRequest{
		JobID: jobID, ProjectID: projectID, DisplayName: displayName,
		ManifestPath:  filepath.Join(outputDir, "task_manifest.json"),
		WorkspacePath: workspace, OutputDir: outputDir, JianyingRoot: jianying,
		TemplateVersion: BuiltInTemplateVersion, TemplateFingerprint: fingerprint,
	})
	if err != nil {
		t.Fatal(err)
	}
	if again.DraftID != result.DraftID {
		t.Fatalf("idempotent register changed draft id %s -> %s", result.DraftID, again.DraftID)
	}
}

func TestHostRegistrarRejectsCaptionTracks(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobID := uuid.NewString()
	outputDir := filepath.Join(root, "out")
	workspace := filepath.Join(outputDir, "workspace", jobID)
	jianying := filepath.Join(root, "jy")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(jianying, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "draft_content.json"), []byte(`{"duration":1000,"tracks":[{"name":"字幕"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(workspace, "draft_meta_info.json"), map[string]any{"draft_id": "id", "draft_name": "n"})
	registrar, err := NewHostRegistrar(root, jianying, "", func(RegistrationRequest, string) (RegistrationResult, error) {
		t.Fatal("validator must not run")
		return RegistrationResult{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	registrar.WithLockRoot(filepath.Join(root, "locks"))
	_, err = registrar.Register(context.Background(), RegistrationRequest{
		JobID: jobID, DisplayName: "x", WorkspacePath: workspace, OutputDir: outputDir, JianyingRoot: jianying,
	})
	if err == nil || !strings.Contains(err.Error(), "caption or title") {
		t.Fatalf("err=%v", err)
	}
}

func writeImageVideoManifest(t *testing.T, path string, job Job, outputDir, displayName, machineProfile string) {
	t.Helper()
	writeJSON(t, path, map[string]any{
		"schema_version":     "2.0",
		"task_id":            job.ID,
		"job_id":             job.ID,
		"skill":              "jianying-image-video",
		"action":             "imagevideo.register",
		"inputs":             []any{},
		"engineering_inputs": []any{},
		"output_dir":         outputDir,
		"skill_snapshot_id":  BuiltInManifestSkillID(),
		"non_secret_settings": map[string]any{
			"machine_profile_path": machineProfile,
			"draft_display_name":   displayName,
		},
	})
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func rewriteJSON(t *testing.T, path string, mutate func(map[string]any)) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	mutate(value)
	writeJSON(t, path, value)
}
