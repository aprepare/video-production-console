package openaicompat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunWritesEnvelopeFromResultJSON(t *testing.T) {
	root := t.TempDir()
	skillRoot := filepath.Join(root, "skill")
	_ = os.MkdirAll(filepath.Join(skillRoot, "references"), 0o755)
	_ = os.MkdirAll(filepath.Join(skillRoot, "scripts"), 0o755)
	_ = os.WriteFile(filepath.Join(skillRoot, "SKILL.md"), []byte("# skill"), 0o644)
	_ = os.WriteFile(filepath.Join(skillRoot, "references", "console-contract.md"), []byte("# contract"), 0o644)

	outputDir := filepath.Join(root, "output")
	_ = os.MkdirAll(outputDir, 0o755)
	manifestPath := filepath.Join(root, "task_manifest.json")
	manifest := map[string]any{
		"task_id": "task-openai-1", "action": "remix.standard", "skill": "finance-viral-remix",
		"output_dir": outputDir, "inputs": []any{},
	}
	raw, _ := json.Marshal(manifest)
	_ = os.WriteFile(manifestPath, raw, 0o644)
	last := filepath.Join(root, "output-last-message.json")

	client := &scriptedClient{outputDir: outputDir, taskID: "task-openai-1"}
	if err := Run(Options{
		ManifestPath:      manifestPath,
		SkillRoot:         skillRoot,
		OutputLastMessage: last,
		Model:             "test-model",
		BaseURL:           "http://example.invalid/v1",
		APIKey:            "test-key",
		Client:            client,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	body, err := os.ReadFile(last)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"completed"`) || !strings.Contains(string(body), "task-openai-1") {
		t.Fatalf("envelope=%s", body)
	}
	if client.calls < 2 {
		t.Fatalf("calls=%d", client.calls)
	}
}

type scriptedClient struct {
	outputDir string
	taskID    string
	calls     int
}

func (c *scriptedClient) Chat(req ChatRequest) (ChatResponse, error) {
	c.calls++
	if c.calls == 1 {
		scriptPath := filepath.ToSlash(filepath.Join(c.outputDir, "continuous_script.txt"))
		args, _ := json.Marshal(map[string]string{"path": scriptPath, "content": "continuous body"})
		return ChatResponse{Choices: []struct {
			Message Message `json:"message"`
		}{{Message: Message{Role: "assistant", ToolCalls: []ToolCall{{
			ID: "1", Type: "function", Function: ToolCallFunc{Name: "write_file", Arguments: string(args)},
		}}}}}}, nil
	}
	envelope := map[string]any{
		"schema_version": "2.0", "task_id": c.taskID, "action": "remix.standard",
		"status": "completed", "summary": "ok", "questions": []any{}, "warnings": []any{},
		"artifacts": []any{}, "asset_outputs": []any{},
	}
	raw, _ := json.Marshal(envelope)
	resultPath := filepath.ToSlash(filepath.Join(c.outputDir, "result.json"))
	args, _ := json.Marshal(map[string]string{"path": resultPath, "content": string(raw)})
	return ChatResponse{Choices: []struct {
		Message Message `json:"message"`
	}{{Message: Message{Role: "assistant", ToolCalls: []ToolCall{{
		ID: "2", Type: "function", Function: ToolCallFunc{Name: "write_file", Arguments: string(args)},
	}}}}}}, nil
}
