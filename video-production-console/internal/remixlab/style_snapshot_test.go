package remixlab

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"video-production-console/internal/agentruntime/openaicompat"
	"video-production-console/internal/store"
)

func TestStyleBriefSnapshotsStayFrozenAndRerunUsesCurrentAccount(t *testing.T) {
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := store.NewRemixLabRepository(db)
	captured := make(chan string, 2)
	svc := NewService(repo, stubRuntime{view: RuntimeView{RemixBaseURL: "http://example.invalid/v1", RemixModel: "writer", RemixAPIKey: "test"}}, remixFakeProtector{}, root, func(ctx context.Context, opts openaicompat.Options) error {
		captured <- opts.WorkflowJSON
		return fastStubRunner(ctx, opts)
	}, nil, nil)
	st := Store{DataRoot: root}
	id := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	if _, err := st.ApplyStyleForAccount(id, "local_store", &Brief{Offer: "旧套餐", SellingPoints: "128元"}); err != nil {
		t.Fatal(err)
	}
	experiment, err := svc.CreateWorkflowExperiment(t.Context(), "海鲜当天到货。", 1, id, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApplyStyleForAccount(id, "knowledge", &Brief{Offer: "新知识内容", Conversion: "关注"}); err != nil {
		t.Fatal(err)
	}
	assertSnapshot := func(wantStyle, wantOffer string) string {
		t.Helper()
		select {
		case raw := <-captured:
			var wf Workflow
			if err := json.Unmarshal([]byte(raw), &wf); err != nil {
				t.Fatal(err)
			}
			if wf.Style != wantStyle || wf.Brief == nil || wf.Brief.Offer != wantOffer {
				t.Fatalf("wrong snapshot: %+v", wf)
			}
			return raw
		case <-time.After(5 * time.Second):
			t.Fatal("runner not started")
		}
		return ""
	}
	firstJSON := assertSnapshot("local_store", "旧套餐")
	done := waitExperimentTerminal(t, svc, experiment.ID)
	if done.Status != "completed" {
		t.Fatalf("first run failed: %+v", done.Runs)
	}
	rerun, err := svc.Rerun(t.Context(), done.Runs[0].ID, RerunInput{Writer: RerunModel{Model: "writer"}, Reviewer: RerunModel{Model: "reviewer"}})
	if err != nil {
		t.Fatal(err)
	}
	secondJSON := assertSnapshot("knowledge", "新知识内容")
	done = waitExperimentTerminal(t, svc, experiment.ID)
	if done.Status != "completed" {
		t.Fatalf("rerun failed: %+v", done.Runs)
	}
	original, _, _, err := repo.GetExperiment(t.Context(), experiment.ID)
	if err != nil || original.WorkflowJSON != firstJSON {
		t.Fatalf("original snapshot was overwritten: %v", err)
	}
	raw, err := repo.GetRunWorkflowJSON(t.Context(), rerun.RunID)
	if err != nil || raw != secondJSON {
		t.Fatalf("rerun snapshot was not frozen: %v", err)
	}
}
