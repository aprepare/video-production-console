package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"video-production-console/internal/remixlab"
	"video-production-console/internal/store"
)

func TestRerunAppendsVersionWithIndependentModels(t *testing.T) {
	db, h := newRemixLabTestEnv(t)
	repo := store.NewRemixLabRepository(db)
	wf, _ := json.Marshal(remixlab.DefaultWorkflow(remixlab.AgentPrompts{}))
	err := repo.CreateExperiment(context.Background(), store.RemixLabExperimentRecord{ID: "existing", SourceText: "原文事实与家庭财务", WorkflowJSON: string(wf), Status: "completed", ProduceAuto: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}, []store.RemixLabSlotRecord{{ID: "old-slot", ExperimentID: "existing", Model: "old-writer", BaseURL: "http://console.example/v1", RunCount: 1}}, []store.RemixLabRunRecord{{ID: "old-run", ExperimentID: "existing", SlotID: "old-slot", RunIndex: 1, Status: "completed", ContinuousScript: "必须保留的旧稿", Comment: "旧稿批注"}})
	if err != nil {
		t.Fatal(err)
	}
	get := httptest.NewRecorder()
	h.ServeHTTP(get, httptest.NewRequest("GET", "/api/remix-lab/runs/old-run/rerun-options", nil))
	if get.Code != 200 {
		t.Fatalf("options: %d %s", get.Code, get.Body.String())
	}
	body := `{"writer":{"model":"claude-test-writer","reasoning_effort":"high","service_tier":"default"},"reviewer":{"model":"claude-test-reviewer","reasoning_effort":"medium","service_tier":"priority"}}`
	post := httptest.NewRecorder()
	h.ServeHTTP(post, httptest.NewRequest("POST", "/api/remix-lab/runs/old-run/rerun", strings.NewReader(body)))
	if post.Code != 202 {
		t.Fatalf("rerun: %d %s", post.Code, post.Body.String())
	}
	var result struct {
		Experiment remixlab.Experiment `json:"experiment"`
		RunID      string              `json:"run_id"`
	}
	if err := json.Unmarshal(post.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Experiment.ID != "existing" || result.RunID == "old-run" || len(result.Experiment.Runs) != 2 {
		t.Fatalf("lost experiment/version: %+v", result)
	}
	var run store.RemixLabRunRecord
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		run, err = repo.GetRun(context.Background(), result.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if run.Status == "completed" || run.Status == "failed" {
			break
		}
	}
	if run.Status != "completed" {
		t.Fatalf("run did not finish: %+v", run)
	}
	old, _ := repo.GetRun(context.Background(), "old-run")
	if old.ContinuousScript != "必须保留的旧稿" || old.Comment != "旧稿批注" || old.Status != "completed" {
		t.Fatalf("old version changed: %+v", old)
	}
	exp, slots, _, _ := repo.GetExperiment(context.Background(), "existing")
	if exp.WorkflowJSON != string(wf) || !exp.ProduceAuto {
		t.Fatal("historical workflow mutated")
	}
	if len(slots) != 2 || slots[1].Model != "claude-test-writer" || slots[1].ReasoningEffort != "high" || slots[1].ServiceTier != "default" {
		t.Fatalf("writer override not isolated: %+v", slots)
	}
	get = httptest.NewRecorder()
	h.ServeHTTP(get, httptest.NewRequest("GET", "/api/remix-lab/runs/"+result.RunID+"/stages", nil))
	if get.Code != 200 || !strings.Contains(get.Body.String(), "claude-test-reviewer") {
		t.Fatalf("reviewer snapshot not used: %s", get.Body.String())
	}
	var snapshot string
	if err := db.QueryRow(`SELECT workflow_json FROM remix_lab_run_configs WHERE run_id=?`, result.RunID).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(snapshot, `"service_tier":"priority"`) {
		t.Fatal("reviewer tier lost")
	}
	prod, err := repo.GetProduction(context.Background(), result.RunID)
	if err != nil || prod.Auto || prod.Status != "waiting_confirm" || prod.ProjectID != "" {
		t.Fatalf("unexpected production: %+v %v", prod, err)
	}
	var count int
	_ = db.QueryRow(`SELECT COUNT(*) FROM remix_lab_experiments`).Scan(&count)
	if count != 1 {
		t.Fatalf("created another project/experiment: %d", count)
	}
}
