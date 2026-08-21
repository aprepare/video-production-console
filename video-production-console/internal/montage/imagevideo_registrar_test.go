package montage

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"video-production-console/internal/imagevideo"
)

func TestValidateImageVideoRegistrationAcceptsHostPublishedDraft(t *testing.T) {
	root := t.TempDir()
	jobID := uuid.NewString()
	projectID := uuid.NewString()
	_, fingerprint, err := imagevideo.BuiltInTemplate()
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
	content := []byte(`{"duration":6000000,"tracks":[{"name":"主画面"},{"name":"旁白"}]}`)
	if err := os.WriteFile(filepath.Join(workspace, "draft_content.json"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(workspace, "draft_meta_info.json"), map[string]any{"draft_id": "source-draft-id", "draft_name": "temp"})
	displayName := "图文视频-" + jobID
	writeJSON(t, filepath.Join(outputDir, "task_manifest.json"), map[string]any{
		"schema_version":     "2.0",
		"task_id":            jobID,
		"job_id":             jobID,
		"skill":              "jianying-image-video",
		"action":             "imagevideo.register",
		"inputs":             []any{},
		"engineering_inputs": []any{},
		"output_dir":         outputDir,
		"skill_snapshot_id":  imagevideo.BuiltInManifestSkillID(),
		"non_secret_settings": map[string]any{
			"draft_display_name": displayName,
		},
	})
	registrar, err := imagevideo.NewHostRegistrar(root, jianying, "", ValidateImageVideoRegistration)
	if err != nil {
		t.Fatal(err)
	}
	registrar.WithLockRoot(filepath.Join(root, "locks"))
	result, err := registrar.Register(context.Background(), imagevideo.RegistrationRequest{
		JobID: jobID, ProjectID: projectID, DisplayName: displayName,
		ManifestPath:  filepath.Join(outputDir, "task_manifest.json"),
		WorkspacePath: workspace, OutputDir: outputDir, JianyingRoot: jianying,
		TemplateVersion: imagevideo.BuiltInTemplateVersion, TemplateFingerprint: fingerprint,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.DraftID == "" || result.DurationUS != 6000000 {
		t.Fatalf("result=%+v", result)
	}
	if _, err := os.Stat(filepath.Join(jianying, jobID, "draft_content.json")); err != nil {
		t.Fatal(err)
	}
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
