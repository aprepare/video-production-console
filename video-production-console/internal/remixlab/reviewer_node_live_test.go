package remixlab

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// 落盘工作流允许单写手；临时指定审稿后仍须通过拓扑校验。
// 数据目录不存在时跳过，不影响 CI。
func TestLiveWorkflowFilesValidateWithReviewer(t *testing.T) {
	root := filepath.Join("..", "..", "video-console-data", "remix_lab")
	entries, err := os.ReadDir(filepath.Join(root, "workflows"))
	if err != nil {
		t.Skip("no live workflow directory")
	}
	files := []string{filepath.Join(root, "workflow.json")}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".json" {
			files = append(files, filepath.Join(root, "workflows", e.Name()))
		}
	}
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var wf Workflow
		if err := json.Unmarshal(raw, &wf); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if err := ValidateWorkflow(wf); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		withReview, err := withBatchReviewer(wf, &RerunModel{Model: "test-reviewer", ReasoningEffort: "high"})
		if err != nil || workflowReviewer(withReview) == nil {
			t.Fatalf("%s: cannot add run-scoped reviewer: %v", path, err)
		}
	}
}
