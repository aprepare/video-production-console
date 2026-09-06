package remixlab

import (
	"strings"
	"testing"
)

func TestReferenceDefaultWorkflowHasOneIndependentCandidate(t *testing.T) {
	wf := DefaultWorkflow(AgentPrompts{})
	if err := ValidateWorkflow(wf); err != nil {
		t.Fatal(err)
	}
	agents := 0
	for _, n := range wf.Nodes {
		if n.Type != WorkflowNodeAgent {
			continue
		}
		agents++
		if n.ID != "hook" || n.Title != "参考稿1" || n.Config.Role != "reference" || !strings.Contains(n.Config.SystemPrompt, "continuous_script") || !strings.Contains(n.Config.SystemPrompt, "月度收支计划表") {
			t.Fatalf("unexpected default prewriter node: %s/%s", n.ID, n.Title)
		}
	}
	if agents != 1 {
		t.Fatalf("want one default reference, got %d agents", agents)
	}
}
