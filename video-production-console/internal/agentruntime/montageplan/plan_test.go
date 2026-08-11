package montageplan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildProducesApprovedPlan(t *testing.T) {
	root := t.TempDir()
	mediaRoot := filepath.Join(root, "media")
	clipPath := filepath.Join(mediaRoot, "clip.mp4")
	if err := os.MkdirAll(mediaRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(clipPath, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(root, "media_index.json")
	index := []map[string]any{{
		"id": "clip-001", "category": "Nature_Landscape",
		"relative_path": "clip.mp4", "duration_seconds": 20,
	}}
	rawIndex, _ := json.Marshal(index)
	if err := os.WriteFile(indexPath, rawIndex, 0o644); err != nil {
		t.Fatal(err)
	}
	narration := filepath.Join(root, "narration.mp3")
	background := filepath.Join(root, "bg.png")
	script := filepath.Join(root, "script.md")
	srt := filepath.Join(root, "sub.srt")
	for _, path := range []string{narration, background, script, srt} {
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	taskID := "task-abcdef123456"
	outputDir := filepath.Join(root, "output")
	manifestPath := filepath.Join(root, "task_manifest.json")
	manifest := map[string]any{
		"task_id": taskID, "job_id": taskID, "action": "montage.execute", "output_dir": outputDir,
		"inputs": []map[string]string{
			{"role": "narration", "path": narration},
			{"role": "account_background", "path": background},
			{"role": "continuous_script", "path": script},
			{"role": "subtitle_srt", "path": srt},
		},
		"non_secret_settings": map[string]string{
			"media_root": mediaRoot, "media_index_path": indexPath, "draft_display_name": "房贷困境与时代反思",
		},
	}
	rawManifest, _ := json.Marshal(manifest)
	if err := os.WriteFile(manifestPath, rawManifest, 0o644); err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(outputDir, "production_plan.json")
	if err := Build(Options{
		ManifestPath: manifestPath,
		PlanPath:     planPath,
		Duration:     func(string) (float64, error) { return 25.0, nil },
	}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	raw, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	var plan map[string]any
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	if plan["status"] != "approved" || plan["model_role"] != "planner" {
		t.Fatalf("status/role = %v/%v", plan["status"], plan["model_role"])
	}
	if plan["project_duration_s"].(float64) != 25 {
		t.Fatalf("duration = %v", plan["project_duration_s"])
	}
	timeline, ok := plan["timeline"].([]any)
	if !ok || len(timeline) == 0 {
		t.Fatal("timeline missing")
	}
	first := timeline[0].(map[string]any)
	if first["source_path"] != clipPath {
		t.Fatalf("source_path = %v", first["source_path"])
	}
	concurrency := plan["concurrency"].(map[string]any)
	if concurrency["job_id"] != taskID {
		t.Fatalf("job_id = %v", concurrency["job_id"])
	}
	graphics := plan["graphics"].(map[string]any)
	title := graphics["title"].(map[string]any)["text"].(string)
	if runeCount := len([]rune(title)); runeCount < 6 || runeCount > 8 {
		t.Fatalf("title %q has %d runes", title, runeCount)
	}
}
