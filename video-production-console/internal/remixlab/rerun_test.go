package remixlab

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"video-production-console/internal/agentruntime/openaicompat"
	"video-production-console/internal/store"
)

func TestRerunUsesSnapshotAndRejectsOverlappingRound(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := store.NewRemixLabRepository(db)
	oldWorkflow := DefaultWorkflow(AgentPrompts{})
	oldWorkflow.Nodes = append(oldWorkflow.Nodes, WorkflowNode{ID: "ammo", Type: WorkflowNodeAgent, Title: "旧弹药", Config: WorkflowNodeConfig{SystemPrompt: "旧规则"}})
	oldWorkflow.Edges = append(oldWorkflow.Edges, [2]string{"source", "ammo"}, [2]string{"ammo", "writer"})
	wf, _ := json.Marshal(oldWorkflow)
	err = repo.CreateExperiment(t.Context(), store.RemixLabExperimentRecord{ID: "exp", SourceText: "原文", Status: "completed", WorkflowJSON: string(wf), CreatedAt: time.Now(), UpdatedAt: time.Now()}, []store.RemixLabSlotRecord{{ID: "slot", ExperimentID: "exp", Model: "old", BaseURL: "http://local.invalid/v1", RunCount: 1}}, []store.RemixLabRunRecord{{ID: "old", ExperimentID: "exp", SlotID: "slot", RunIndex: 1, Status: "completed"}})
	if err != nil {
		t.Fatal(err)
	}
	captured := make(chan openaicompat.Options, 1)
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	svc := NewService(repo, stubRuntime{view: RuntimeView{RemixModel: "default", RemixAPIKey: "test-key"}}, remixFakeProtector{}, t.TempDir(), func(ctx context.Context, opts openaicompat.Options) error {
		captured <- opts
		<-release
		return fastStubRunner(ctx, opts)
	}, nil, nil)
	current := DefaultWorkflow(AgentPrompts{})
	for i := range current.Nodes {
		switch current.Nodes[i].ID {
		case "hook":
			current.Nodes[i].Config.Model = "saved-planner"
			current.Nodes[i].Config.ReasoningEffort = "medium"
			current.Nodes[i].Config.ServiceTier = "default"
		case "writer":
			current.Nodes[i].Config.Model = "saved-writer"
			current.Nodes[i].Config.ReasoningEffort = "low"
			current.Nodes[i].Config.ServiceTier = "priority"
		case "review":
			current.Nodes[i].Config.Model = "saved-reviewer"
		}
	}
	if _, err = svc.SaveWorkflowDefinition(current); err != nil {
		t.Fatal(err)
	}
	// hook 是策划节点而不是参考稿：进 Planner，References 为 nil。
	options, err := svc.RerunOptions(t.Context(), "old")
	if err != nil || options.References != nil || options.Planner == nil || options.Planner.Model != "saved-planner" || options.Planner.ReasoningEffort != "medium" || options.Planner.ServiceTier != "default" || options.Writer.Model != "saved-writer" || options.Writer.ServiceTier != "priority" || options.Reviewer.Model != "saved-reviewer" {
		t.Fatalf("rerun options did not use saved role settings: %+v, %v", options, err)
	}
	input := RerunInput{Writer: RerunModel{"claude-writer", "high", "default"}, Reviewer: RerunModel{"reviewer-choice", "low", "priority"}}
	// A stale client may submit the former planner alongside new reference choices.
	// The explicit reference list must win, including when the first ID is hook.
	input.Planner = &RerunModel{"stale-planner", "low", "default"}
	if err = json.Unmarshal([]byte(`{"references":[{"model":"reference-a","reasoning_effort":"medium","service_tier":"priority"},{"model":"reference-b","reasoning_effort":"high","service_tier":"default"}]}`), &input); err != nil {
		t.Fatal(err)
	}
	result, err := svc.Rerun(t.Context(), "old", input)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case opts := <-captured:
		if opts.Model != "claude-writer" || opts.ReasoningEffort != "high" || opts.ServiceTier != "default" {
			t.Fatalf("wrong writer options: %s/%s/%s", opts.Model, opts.ReasoningEffort, opts.ServiceTier)
		}
		var used Workflow
		if err = json.Unmarshal([]byte(opts.WorkflowJSON), &used); err != nil {
			t.Fatal(err)
		}
		review := workflowReviewer(used)
		if review == nil || review.Config.Model != "reviewer-choice" || review.Config.ReasoningEffort != "low" || review.Config.ServiceTier != "priority" {
			t.Fatalf("reviewer override lost: %+v", review)
		}
		referenceCount := 0
		for _, node := range used.Nodes {
			if node.Config.Role == "reference" {
				referenceCount++
			}
			// 显式参考列表只增删参考稿；策划节点保留账号里保存的设置。
			if node.ID == "hook" && (node.Config.Role == "reference" || node.Config.Model != "saved-planner" || node.Config.ReasoningEffort != "medium") {
				t.Fatalf("planner node altered by reference selection: %+v", node.Config)
			}
			if node.Config.Role == "reference" && node.ID == "reference_1" && (node.Config.Model != "reference-a" || node.Config.ReasoningEffort != "medium" || node.Config.ServiceTier != "priority") {
				t.Fatalf("reference override lost: %+v", node.Config)
			}
			if node.ID == "ammo" || node.ID == "facts" {
				t.Fatalf("new round reused old prewriter: %s", node.ID)
			}
		}
		if referenceCount != 2 {
			t.Fatalf("wanted two selected references, got %d", referenceCount)
		}
		oldExperiment, _, _, readErr := repo.GetExperiment(t.Context(), "exp")
		if readErr != nil || oldExperiment.WorkflowJSON != string(wf) {
			t.Fatal("rerun altered historical experiment snapshot")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runner not started")
	}
	if _, err = svc.Rerun(t.Context(), "old", input); !errors.Is(err, store.ErrRemixLabActive) {
		t.Fatalf("overlap accepted: %v", err)
	}
	close(release)
	waitExperimentTerminal(t, svc, "exp")
	if result.RunID == "old" {
		t.Fatal("old run overwritten")
	}
	raw, err := repo.GetRunWorkflowJSON(t.Context(), result.RunID)
	if err != nil || raw == "" {
		t.Fatal("snapshot not persisted")
	}
	if err = repo.DeleteExperiment(t.Context(), "exp"); err != nil {
		t.Fatal(err)
	}
	if raw, err = repo.GetRunWorkflowJSON(t.Context(), result.RunID); err != nil || raw != "" {
		t.Fatal("deleted run left an orphan snapshot")
	}
}

func TestRerunRejectsBadModelSettings(t *testing.T) {
	for _, input := range []RerunModel{{"", "high", "default"}, {"x\ny", "high", "default"}, {"x", "surprise", "default"}, {"x", "high", "invalid"}} {
		if err := validateRerunModel(&input); err == nil {
			t.Fatalf("accepted invalid input: %+v", input)
		}
	}
}
