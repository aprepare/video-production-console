package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"video-production-console/internal/remixlab"
	"video-production-console/internal/store"
)

func TestWorkflowRunFreezesBatchReviewerOverride(t *testing.T) {
	db, handler := newRemixLabTestEnv(t)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("POST", "/api/remix-lab/workflow/run", strings.NewReader(`{"source":"一篇批量二创原文","models":["writer-a","writer-b"],"reviewer":{"model":"reviewer-x","reasoning_effort":"high","service_tier":"priority"}}`)))
	if rec.Code != 202 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var result remixlab.Experiment
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	repo := store.NewRemixLabRepository(db)
	deadline := time.Now().Add(3 * time.Second)
	for {
		exp, slots, runs, err := repo.GetExperiment(t.Context(), result.ID)
		if err != nil {
			t.Fatal(err)
		}
		var w remixlab.Workflow
		if err = json.Unmarshal([]byte(exp.WorkflowJSON), &w); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, n := range w.Nodes {
			if n.Type == "reviewer" {
				found = n.Config.Model == "reviewer-x" && n.Config.ReasoningEffort == "high" && n.Config.ServiceTier == "priority"
			}
		}
		if !found || len(slots) != 2 {
			t.Fatal("override or multi-writer slots lost")
		}
		done := true
		for _, r := range runs {
			if r.Status == "queued" || r.Status == "running" {
				done = false
			}
		}
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("run did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
