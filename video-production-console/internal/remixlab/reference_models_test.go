package remixlab

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"video-production-console/internal/store"
)

func TestReferenceModelsSelectZeroTwoThreeAndRejectDependentCandidates(t *testing.T) {
	for _, count := range []int{0, 2, 3} {
		choices := make([]RerunModel, count)
		for i := range choices {
			choices[i] = RerunModel{"selected", "high", "priority"}
		}
		wf, err := withReferenceModels(DefaultWorkflow(AgentPrompts{}), choices)
		if err != nil {
			t.Fatal(err)
		}
		// 默认图自带的策划节点不是参考稿，选参考模型时必须原样保留。
		agents, planners := 0, 0
		for _, node := range wf.Nodes {
			if node.Type != WorkflowNodeAgent {
				continue
			}
			if node.Config.Role != "reference" {
				planners++
				continue
			}
			agents++
			if node.Config.Model != "selected" || node.Config.ServiceTier != "priority" {
				t.Fatal("selection lost")
			}
		}
		if agents != count {
			t.Fatalf("got %d references want %d", agents, count)
		}
		if planners != 1 {
			t.Fatalf("planner node must survive reference selection, got %d", planners)
		}
		if count == 2 {
			var ids []string
			for _, node := range wf.Nodes {
				if node.Type == WorkflowNodeAgent && node.Config.Role == "reference" {
					ids = append(ids, node.ID)
				}
			}
			wf.Edges = append(wf.Edges, [2]string{ids[0], ids[1]})
			if ValidateWorkflow(wf) == nil {
				t.Fatal("dependent references accepted as independent")
			}
		}
	}
	if _, err := withReferenceModels(DefaultWorkflow(AgentPrompts{}), []RerunModel{{"", "high", "default"}}); err == nil {
		t.Fatal("empty reference model accepted")
	}
}

func TestRunStagesKeepsReferenceVersionsAfterFinalEdit(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := store.NewRemixLabRepository(db)
	dir := t.TempDir()
	archive := filepath.Join(dir, "reference_drafts")
	if err = os.MkdirAll(archive, 0700); err != nil {
		t.Fatal(err)
	}
	raw := `{"id":"version-one","node_id":"hook","title":"参考稿1","model":"reference-model","created_at":"2026-09-05T07:00:00Z","status":"completed","copy":{"continuous_script":"参考全文与课尾","titles":["参考标题"],"descriptions":["参考描述"]}}`
	if err = os.WriteFile(filepath.Join(archive, "version-one.json"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	wf, _ := json.Marshal(DefaultWorkflow(AgentPrompts{}))
	err = repo.CreateExperiment(t.Context(), store.RemixLabExperimentRecord{ID: "exp-ref", SourceText: "原文", Status: "completed", WorkflowJSON: string(wf), CreatedAt: time.Now(), UpdatedAt: time.Now()}, []store.RemixLabSlotRecord{{ID: "slot-ref", ExperimentID: "exp-ref", Model: "writer", RunCount: 1}}, []store.RemixLabRunRecord{{ID: "run-ref", ExperimentID: "exp-ref", SlotID: "slot-ref", RunIndex: 1, Status: "completed", OutputDir: dir, ContinuousScript: "初稿"}})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(repo, stubRuntime{}, remixFakeProtector{}, t.TempDir(), nil, nil, nil)
	if err = svc.UpdateRunPackage(t.Context(), "run-ref", PackageInput{ContinuousScript: "人工定稿。", ShortTitles: []string{"人工定稿标题"}}); err != nil {
		t.Fatal(err)
	}
	view, err := svc.RunStages(t.Context(), "run-ref")
	if err != nil {
		t.Fatal(err)
	}
	if len(view.ReferenceDrafts) != 1 || view.ReferenceDrafts[0].Copy.ContinuousScript != "参考全文与课尾" || view.ReferenceDrafts[0].Copy.Descriptions[0] != "参考描述" {
		t.Fatalf("reference data lost: %+v", view.ReferenceDrafts)
	}
	got, err := os.ReadFile(filepath.Join(archive, "version-one.json"))
	if err != nil || string(got) != raw {
		t.Fatal("editing final changed the reference archive")
	}
}
