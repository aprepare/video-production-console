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

func TestCustomStyleAppliesToNewRunAndRerunWithItsOwnMetadata(t *testing.T) {
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := store.NewRemixLabRepository(db)
	st := Store{DataRoot: root}
	style, err := st.UpsertStyle(StyleTemplate{Name: "账号自定义写法", Writer: "自定义首版正文要求", WriterUser: "{{SOURCE}}"})
	if err != nil {
		t.Fatal(err)
	}
	id := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	if _, err = st.ApplyStyleForAccount(id, style.ID, nil); err != nil {
		t.Fatal(err)
	}
	captured := make(chan string, 2)
	svc := NewService(repo, stubRuntime{view: RuntimeView{RemixAPIKey: "test", RemixModel: "writer"}}, remixFakeProtector{}, root,
		func(ctx context.Context, opts openaicompat.Options) error {
			captured <- opts.WorkflowJSON
			return fastStubRunner(ctx, opts)
		}, nil, nil)
	exp, err := svc.CreateWorkflowExperiment(t.Context(), "原文用于核验账号风格", 1, id, false)
	if err != nil {
		t.Fatal(err)
	}
	done := waitExperimentTerminal(t, svc, exp.ID)
	if done.Runs[0].PromptID != style.ID || done.Runs[0].PromptName != style.Name {
		t.Fatal("new run still reports the old active prompt")
	}
	first := <-captured
	style.Writer = "自定义新版正文要求"
	if _, err = st.UpsertStyle(style); err != nil {
		t.Fatal(err)
	}
	if _, err = st.ApplyStyleForAccount(id, style.ID, nil); err != nil {
		t.Fatal(err)
	}
	rerun, err := svc.Rerun(t.Context(), done.Runs[0].ID, RerunInput{Writer: RerunModel{Model: "writer"}, Reviewer: RerunModel{Model: "reviewer"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = waitExperimentTerminal(t, svc, exp.ID)
	second := <-captured
	for index, raw := range []string{first, second} {
		var wf Workflow
		if err := json.Unmarshal([]byte(raw), &wf); err != nil {
			t.Fatal(err)
		}
		want := []string{"自定义首版正文要求", "自定义新版正文要求"}[index]
		if workflowWriter(wf).Config.SystemPrompt != want || wf.EditorialRules == nil || *wf.EditorialRules != "" {
			t.Fatalf("run %d lost custom style or added conflicting rules", index)
		}
	}
	record, err := repo.GetRun(t.Context(), rerun.RunID)
	if err != nil || record.PromptID != style.ID || record.PromptName != style.Name {
		t.Fatalf("rerun metadata incorrect: %v", err)
	}
	original, _, _, err := repo.GetExperiment(t.Context(), exp.ID)
	if err != nil || original.WorkflowJSON != first {
		t.Fatalf("historical snapshot was overwritten: %v", err)
	}
}
