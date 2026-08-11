package montagescript

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunWritesEnvelopeWithFakePython(t *testing.T) {
	root := t.TempDir()
	skillRoot := filepath.Join(root, "skill")
	if err := os.MkdirAll(filepath.Join(skillRoot, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillRoot, "scripts", "run_montage_job.py"), []byte("# fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	mediaRoot := filepath.Join(root, "media")
	if err := os.MkdirAll(mediaRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	clip := filepath.Join(mediaRoot, "clip.mp4")
	_ = os.WriteFile(clip, []byte("v"), 0o644)
	indexPath := filepath.Join(root, "index.json")
	_ = os.WriteFile(indexPath, []byte(`[{"id":"c1","category":"Nature_Landscape","relative_path":"clip.mp4","duration_seconds":20}]`), 0o644)
	narration := filepath.Join(root, "n.mp3")
	bg := filepath.Join(root, "bg.png")
	_ = os.WriteFile(narration, []byte("a"), 0o644)
	_ = os.WriteFile(bg, []byte("b"), 0o644)
	taskID := "task-script-run-01"
	outputDir := filepath.Join(root, "output")
	workspace := filepath.Join(outputDir, "workspace", taskID)
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, "task_manifest.json")
	manifest := map[string]any{
		"schema_version": "2.0", "task_id": taskID, "job_id": taskID,
		"action": "montage.execute", "output_dir": outputDir,
		"inputs": []map[string]string{
			{"role": "narration", "path": narration},
			{"role": "account_background", "path": bg},
		},
		"non_secret_settings": map[string]string{"media_root": mediaRoot, "media_index_path": indexPath},
	}
	raw, _ := json.Marshal(manifest)
	_ = os.WriteFile(manifestPath, raw, 0o644)
	last := filepath.Join(root, "output-last-message.json")

	calls := 0
	err := Run(Options{
		ManifestPath:      manifestPath,
		SkillRoot:         skillRoot,
		OutputLastMessage: last,
		Duration:          func(string) (float64, error) { return 12, nil },
		CommandRunner: func(name string, args ...string) ([]byte, error) {
			calls++
			phase := args[1]
			switch phase {
			case "validate-inputs", "validate-plan":
				return []byte(`{"schema_version":"2.0","task_id":"` + taskID + `","action":"montage.execute","status":"completed","summary":"ok","questions":[],"artifacts":[],"asset_outputs":[],"warnings":[]}`), nil
			case "execute":
				envelope := map[string]any{
					"schema_version": "2.0", "task_id": taskID, "action": "montage.execute",
					"status": "completed", "summary": "done", "questions": []any{}, "warnings": []any{},
					"artifacts": []map[string]any{{
						"type": "plaintext_workspace", "kind": "directory", "path": `\\?\` + workspace,
					}},
					"asset_outputs": []any{},
				}
				raw, _ := json.Marshal(envelope)
				_ = os.WriteFile(filepath.Join(outputDir, "result.json"), raw, 0o644)
				return []byte("noise before json"), nil
			default:
				t.Fatalf("unexpected phase %q", phase)
				return nil, nil
			}
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if calls != 3 {
		t.Fatalf("calls=%d", calls)
	}
	body, err := os.ReadFile(last)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"plaintext_workspace"`) {
		t.Fatalf("envelope=%s", body)
	}
	if !strings.Contains(string(body), taskID) {
		t.Fatalf("missing task id in %s", body)
	}
	if strings.Contains(string(body), `\\?\\`) || strings.Contains(string(body), `\\\\?\\`) {
		t.Fatalf("extended path prefix should be stripped: %s", body)
	}
	if !strings.Contains(string(body), filepath.ToSlash(workspace)) && !strings.Contains(string(body), strings.ReplaceAll(workspace, `\`, `\\`)) {
		t.Fatalf("workspace path missing from envelope: %s", body)
	}
}

func TestWriteFailureStillWritesEnvelope(t *testing.T) {
	root := t.TempDir()
	last := filepath.Join(root, "out.json")
	manifest := filepath.Join(root, "m.json")
	_ = os.WriteFile(manifest, []byte(`{"task_id":"abc","action":"montage.execute"}`), 0o644)
	if err := writeFailure(last, manifest, errString("boom")); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(last)
	if !strings.Contains(string(body), `"failed"`) || !strings.Contains(string(body), "boom") {
		t.Fatalf("%s", body)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
