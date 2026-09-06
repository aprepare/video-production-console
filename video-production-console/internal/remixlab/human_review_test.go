package remixlab

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestHumanReviewWorkflowRemovesLegacyGateWithoutChangingSnapshot(t *testing.T) {
	w := DefaultWorkflow(AgentPrompts{})
	// Build a historical graph even after the default changes.
	nodes := w.Nodes[:0]
	for _, n := range w.Nodes {
		if n.Type != WorkflowNodeSelfcheck {
			nodes = append(nodes, n)
		}
	}
	w.Nodes = nodes
	w.Nodes = append(w.Nodes, WorkflowNode{ID: "old_gate", Type: WorkflowNodeSelfcheck, Title: "旧机械节点", Config: WorkflowNodeConfig{LenMinRatio: 9, OverlapMaxPct: 99}})
	edges := w.Edges[:0]
	for _, e := range w.Edges {
		if e[0] != "selfcheck" && e[1] != "selfcheck" && !(e[0] == "writer" && e[1] == "review") {
			edges = append(edges, e)
		}
	}
	w.Edges = append(edges, [2]string{"writer", "old_gate"}, [2]string{"old_gate", "review"})
	raw, _ := json.Marshal(w)
	path := filepath.Join(t.TempDir(), "snapshot.json")
	_ = os.WriteFile(path, raw, 0600)
	got, err := decodeWorkflow(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range got.Nodes {
		if n.Type == WorkflowNodeSelfcheck {
			t.Fatal("legacy gate still exposed")
		}
	}
	if err := ValidateWorkflow(got); err != nil {
		t.Fatalf("bridged workflow invalid: %v", err)
	}
	saved, _ := os.ReadFile(path)
	if string(saved) != string(raw) {
		t.Fatal("history changed")
	}
	parsed, ok := parseWorkflowJSON(string(raw))
	if !ok || len(parsed.Nodes) != len(got.Nodes) {
		t.Fatal("run snapshot not normalized")
	}
	for _, n := range DefaultWorkflow(AgentPrompts{}).Nodes {
		if n.Type == WorkflowNodeSelfcheck {
			t.Fatal("default gate remains")
		}
	}
}
