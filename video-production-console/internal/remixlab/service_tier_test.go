package remixlab

import (
	"context"
	"path/filepath"
	"testing"

	"video-production-console/internal/agentruntime/openaicompat"
	"video-production-console/internal/store"
)

func TestFastPresetWorkflowSnapshotAndRun(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo := store.NewRemixLabRepository(db)
	requests := make(chan openaicompat.Options, 4)
	runner := func(ctx context.Context, opts openaicompat.Options) error {
		requests <- opts
		return fastStubRunner(ctx, opts)
	}
	svc := NewService(repo, stubRuntime{view: RuntimeView{RemixBaseURL: "http://example.test", RemixAPIKey: "test"}}, remixFakeProtector{}, t.TempDir(), runner, nil, nil)
	defaults, err := svc.SavePresets(t.Context(), []SlotInput{{Model: "chosen", ReasoningEffort: "high", ServiceTier: "fast"}})
	if err != nil {
		t.Fatal(err)
	}
	if defaults.Presets[0].ServiceTier != "priority" {
		t.Fatal("preset Fast not saved")
	}
	for _, override := range []string{"", "default"} {
		wf, err := svc.WorkflowForAccount("")
		if err != nil {
			t.Fatal(err)
		}
		for i := range wf.Nodes {
			if wf.Nodes[i].Type == WorkflowNodeWriter {
				wf.Nodes[i].Config.ServiceTier = override
			}
		}
		if err := (Store{DataRoot: svc.dataRoot}).SaveWorkflow(wf); err != nil {
			t.Fatal(err)
		}
		exp, err := svc.CreateWorkflowExperiment(t.Context(), "测试原文", 1, "", false)
		if err != nil {
			t.Fatal(err)
		}
		waitExperimentTerminal(t, svc, exp.ID)
		opts := <-requests
		want := "priority"
		if override != "" {
			want = override
		}
		if opts.ServiceTier != want || opts.Model != "chosen" || opts.ReasoningEffort != "high" {
			t.Fatalf("runtime config=%+v", opts)
		}
		record, slots, _, err := repo.GetExperiment(t.Context(), exp.ID)
		if err != nil {
			t.Fatal(err)
		}
		if slots[0].ServiceTier != want {
			t.Fatal("slot tier not persisted")
		}
		snapshot, ok := parseWorkflowJSON(record.WorkflowJSON)
		if !ok || workflowWriter(snapshot).Config.ServiceTier != want {
			t.Fatal("writer tier not frozen")
		}
		if workflowReviewer(snapshot).Config.ServiceTier != "" {
			t.Fatal("writer Fast leaked to reviewer")
		}
	}
}
