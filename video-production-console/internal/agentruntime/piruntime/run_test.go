package piruntime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunPrefersResultJSON(t *testing.T) {
	root := t.TempDir()
	skillRoot := filepath.Join(root, "skill")
	if err := os.MkdirAll(filepath.Join(skillRoot, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillRoot, "SKILL.md"), []byte("# skill"), 0o644); err != nil {
		t.Fatal(err)
	}
	outputDir := filepath.Join(root, "output")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, "task_manifest.json")
	manifest := map[string]any{
		"task_id": "t1", "action": "remix.standard", "skill": "finance-viral-remix", "output_dir": outputDir,
	}
	raw, _ := json.Marshal(manifest)
	if err := os.WriteFile(manifestPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(root, "output-last-message.json")
	result := map[string]any{
		"schema_version": "2.0",
		"task_id":        "t1",
		"action":         "remix.standard",
		"status":         "completed",
		"summary":        "ok",
		"questions":      []any{},
		"artifacts":      []any{},
		"asset_outputs":  []any{},
		"warnings":       []any{},
	}
	resultRaw, _ := json.Marshal(result)

	var sawArgs []string
	err := Run(Options{
		ManifestPath:      manifestPath,
		SkillRoot:         skillRoot,
		OutputLastMessage: outPath,
		Model:             "test-model",
		LookPath:          func(string) (string, error) { return "pi-fake", nil },
		RunCommand: func(name string, args []string, stdin string, dir string) ([]byte, error) {
			if name != "pi-fake" {
				t.Fatalf("name=%q", name)
			}
			sawArgs = append([]string{}, args...)
			if !strings.Contains(stdin, manifestPath) || !strings.Contains(stdin, "# Skill") {
				t.Fatalf("stdin missing prompt pieces: %q", stdin[:min(120, len(stdin))])
			}
			if err := os.WriteFile(filepath.Join(outputDir, "result.json"), resultRaw, 0o644); err != nil {
				return nil, err
			}
			return []byte("done"), nil
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	joined := strings.Join(sawArgs, " ")
	if !strings.Contains(joined, "-p") || !strings.Contains(joined, "--model") || !strings.Contains(joined, "test-model") {
		t.Fatalf("args=%#v", sawArgs)
	}
	got, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(got, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope["status"] != "completed" {
		t.Fatalf("status=%v", envelope["status"])
	}
}

func TestRunFailsWhenPiMissing(t *testing.T) {
	root := t.TempDir()
	skillRoot := filepath.Join(root, "skill")
	if err := os.MkdirAll(skillRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	outputDir := filepath.Join(root, "output")
	manifestPath := filepath.Join(root, "task_manifest.json")
	raw, _ := json.Marshal(map[string]any{
		"task_id": "t2", "action": "topic.brainstorm", "output_dir": outputDir,
	})
	if err := os.WriteFile(manifestPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(root, "output-last-message.json")
	err := Run(Options{
		ManifestPath:      manifestPath,
		SkillRoot:         skillRoot,
		OutputLastMessage: outPath,
		LookPath:          func(string) (string, error) { return "", fmt.Errorf("not found") },
	})
	if err != nil {
		t.Fatalf("Run should write failed envelope, got err=%v", err)
	}
	got, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `"status":"failed"`) || !strings.Contains(string(got), "pi binary not found") {
		t.Fatalf("envelope=%s", got)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
