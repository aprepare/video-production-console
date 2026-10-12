package openaicompat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSingleWriterWorkflowOverridesLegacyPlannerAndReviewerFlags(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.txt")
	if err := os.WriteFile(sourcePath, []byte("这是原文开头。第二句。第三句。后面是需要重构的中段。"), 0600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "output")
	manifest, _ := json.Marshal(map[string]any{"action": "remix.standard", "output_dir": output, "inputs": []map[string]string{{"type": "source_script", "path": sourcePath}}})
	manifestPath := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(manifestPath, manifest, 0600); err != nil {
		t.Fatal(err)
	}
	rules := "仅按用户的头尾锁定与中段重构规则输出。"
	spec := flowSpec{EditorialRules: &rules, Nodes: []flowNode{
		{ID: "source", Type: "input"},
		{ID: "writer", Type: "writer", Config: flowNodeConfig{SystemPrompt: "单写手：内部拆解和自查。", UserTemplate: "{{SOURCE}}"}},
		{ID: "final", Type: "output"},
	}, Edges: [][2]string{{"source", "writer"}, {"writer", "final"}}}
	raw, _ := json.Marshal(spec)
	client := &referencePipelineClient{draft: currentPublishFixture(t)}
	if err := Run(Options{ManifestPath: manifestPath, OutputLastMessage: filepath.Join(dir, "last.json"), BaseURL: "http://local.invalid/v1", APIKey: "fixture", Model: "writer", Client: client, WorkflowJSON: string(raw), Pipeline: PipelineMultiAgent, ReviewerEnabled: true}); err != nil {
		t.Fatal(err)
	}
	if len(client.requests) != 1 {
		t.Fatalf("single-writer workflow called %d models", len(client.requests))
	}
	system := client.requests[0].Messages[0].Content
	if !strings.Contains(system, rules) || strings.Contains(system, EditorialWritingRules) {
		t.Fatal("custom policy was lost or legacy policy leaked into request")
	}
	for _, name := range []string{"continuous_script.txt", "publishing_package.json"} {
		if _, err := os.Stat(filepath.Join(output, name)); err != nil {
			t.Fatal("missing final deliverable", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(output, "review.json")); !os.IsNotExist(err) {
		t.Fatal("unexpected reviewer artifact", err)
	}
}
