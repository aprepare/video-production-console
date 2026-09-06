package openaicompat

import "testing"

func TestResolveReviewerSettingsDoesNotInheritWriterModel(t *testing.T) {
	spec := flowSpec{Nodes: []flowNode{
		{ID: "review", Type: "reviewer", Title: "审稿终审", Config: flowNodeConfig{
			SystemPrompt: "审稿规则",
		}},
	}}
	run, model, effort, prompt := resolveReviewerSettings(Options{
		DefaultModel:    "grok-4.6-fast",
		DefaultEffort:   "low",
		Model:           "claude-opus-4-6-thinking",
		ReasoningEffort: "high",
	}, &spec, "claude-opus-4-6-thinking", "high")
	if !run {
		t.Fatal("workflow with reviewer node must run review")
	}
	if model != "grok-4.6-fast" {
		t.Fatalf("empty reviewer model must use default档 %q, not writer %q", model, "claude-opus-4-6-thinking")
	}
	if effort != "low" {
		t.Fatalf("empty reviewer effort must use default档, got %q", effort)
	}
	if prompt != "审稿规则" {
		t.Fatalf("prompt=%q", prompt)
	}

	spec.Nodes[0].Config.Model = "gpt-review"
	spec.Nodes[0].Config.ReasoningEffort = "medium"
	_, model, effort, _ = resolveReviewerSettings(Options{DefaultModel: "grok-4.6-fast", DefaultEffort: "low"}, &spec, "claude-opus-4-6-thinking", "high")
	if model != "gpt-review" || effort != "medium" {
		t.Fatalf("explicit reviewer config lost: model=%q effort=%q", model, effort)
	}
}

func TestResolveReviewerSettingsLegacyPipelineKeepsWriterModel(t *testing.T) {
	run, model, effort, _ := resolveReviewerSettings(Options{
		ReviewerEnabled: true,
		DefaultModel:    "grok-4.6-fast",
	}, nil, "writer-model", "xhigh")
	if !run || model != "writer-model" || effort != "xhigh" {
		t.Fatalf("legacy reviewer: run=%v model=%q effort=%q", run, model, effort)
	}
}
