package remixlab

import (
	"encoding/json"
	"testing"
)

func TestBatchReviewerIsScopedToRunAndCanBeDisabled(t *testing.T) {
	original := Workflow{Version: 1, Name: "单写手", Nodes: []WorkflowNode{{ID: "source", Type: WorkflowNodeInput, Title: "原文"}, {ID: "writer", Type: WorkflowNodeWriter, Title: "写手"}, {ID: "final", Type: WorkflowNodeOutput, Title: "定稿"}}, Edges: [][2]string{{"source", "writer"}, {"writer", "final"}}}
	before, _ := json.Marshal(original)
	got, err := withBatchReviewer(original, &RerunModel{Model: "review-model", ReasoningEffort: "high", ServiceTier: "priority"})
	if err != nil {
		t.Fatal(err)
	}
	if err = ValidateWorkflow(got); err != nil {
		t.Fatal(err)
	}
	review := workflowReviewer(got)
	if review == nil || review.Config.Model != "review-model" || review.Config.ServiceTier != "priority" {
		t.Fatal("reviewer settings lost")
	}
	after, _ := json.Marshal(original)
	if string(before) != string(after) {
		t.Fatal("mutated saved workflow")
	}
	without, err := withBatchReviewer(got, &RerunModel{})
	if err != nil || ValidateWorkflow(without) != nil || workflowReviewer(without) != nil {
		t.Fatal("reviewer not removed cleanly", err)
	}
	if _, err = withBatchReviewer(original, &RerunModel{Model: "bad\nmodel"}); err == nil {
		t.Fatal("invalid model accepted")
	}
}
