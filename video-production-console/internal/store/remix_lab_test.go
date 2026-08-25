package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestRemixLabRepositoryRoundTrip(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo := NewRemixLabRepository(db)
	now := time.Date(2026, 8, 25, 8, 0, 0, 0, time.UTC)
	exp := RemixLabExperimentRecord{ID: "11111111-1111-1111-1111-111111111111", Title: "银行开始往外轰钱了", SourceText: "原文", PromptStamp: "语感回流 2026-08-25 禁空收尾", Status: "running", CreatedAt: now, UpdatedAt: now}
	slots := []RemixLabSlotRecord{{ID: "22222222-2222-2222-2222-222222222222", ExperimentID: exp.ID, SortIndex: 0, Label: "deepseek-v4-pro", Model: "deepseek-v4-pro", RunCount: 2}}
	runs := []RemixLabRunRecord{
		{ID: "33333333-3333-3333-3333-333333333331", ExperimentID: exp.ID, SlotID: slots[0].ID, RunIndex: 1, Status: "queued"},
		{ID: "33333333-3333-3333-3333-333333333332", ExperimentID: exp.ID, SlotID: slots[0].ID, RunIndex: 2, Status: "queued"},
	}
	if err := repo.CreateExperiment(t.Context(), exp, slots, runs); err != nil {
		t.Fatal(err)
	}
	list, err := repo.ListExperiments(t.Context())
	if err != nil || len(list) != 1 || list[0].Title != exp.Title {
		t.Fatalf("list=%v err=%v", list, err)
	}
	_, gotSlots, gotRuns, err := repo.GetExperiment(t.Context(), exp.ID)
	if err != nil || len(gotSlots) != 1 || len(gotRuns) != 2 {
		t.Fatalf("slots=%d runs=%d err=%v", len(gotSlots), len(gotRuns), err)
	}
	if err := repo.PutPresetJSON(t.Context(), `{"slots":[]}`); err != nil {
		t.Fatal(err)
	}
	raw, err := repo.GetPresetJSON(t.Context())
	if err != nil || raw != `{"slots":[]}` {
		t.Fatalf("preset=%q err=%v", raw, err)
	}
}

func TestRemixLabFailNonTerminal(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo := NewRemixLabRepository(db)
	now := time.Now().UTC()
	exp := RemixLabExperimentRecord{ID: "11111111-1111-1111-1111-111111111111", Title: "t", SourceText: "s", PromptStamp: "stamp", Status: "running", CreatedAt: now, UpdatedAt: now}
	slot := RemixLabSlotRecord{ID: "22222222-2222-2222-2222-222222222222", ExperimentID: exp.ID, Model: "m", RunCount: 1}
	run := RemixLabRunRecord{ID: "33333333-3333-3333-3333-333333333333", ExperimentID: exp.ID, SlotID: slot.ID, RunIndex: 1, Status: "running"}
	if err := repo.CreateExperiment(t.Context(), exp, []RemixLabSlotRecord{slot}, []RemixLabRunRecord{run}); err != nil {
		t.Fatal(err)
	}
	n, err := repo.FailNonTerminal(t.Context(), "控制台已重启", now)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	got, err := repo.GetRun(t.Context(), run.ID)
	if err != nil || got.Status != "failed" || got.ErrorMessage != "控制台已重启" {
		t.Fatalf("%+v err=%v", got, err)
	}
	exp2, _, _, err := repo.GetExperiment(t.Context(), exp.ID)
	if err != nil || exp2.Status != "failed" {
		t.Fatalf("exp=%+v err=%v", exp2, err)
	}
}

func TestRemixLabUpdateRunRecomputesFromDBExperimentID(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo := NewRemixLabRepository(db)
	now := time.Now().UTC()
	exp := RemixLabExperimentRecord{ID: "11111111-1111-1111-1111-111111111111", Title: "t", SourceText: "s", PromptStamp: "stamp", Status: "running", CreatedAt: now, UpdatedAt: now}
	slot := RemixLabSlotRecord{ID: "22222222-2222-2222-2222-222222222222", ExperimentID: exp.ID, Model: "m", RunCount: 1}
	run := RemixLabRunRecord{ID: "33333333-3333-3333-3333-333333333333", ExperimentID: exp.ID, SlotID: slot.ID, RunIndex: 1, Status: "queued"}
	if err := repo.CreateExperiment(t.Context(), exp, []RemixLabSlotRecord{slot}, []RemixLabRunRecord{run}); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateRun(t.Context(), RemixLabRunRecord{
		ID:               run.ID,
		Status:           "completed",
		ContinuousScript: "脚本",
	}); err != nil {
		t.Fatal(err)
	}
	got, _, _, err := repo.GetExperiment(t.Context(), exp.ID)
	if err != nil || got.Status != "completed" {
		t.Fatalf("exp=%+v err=%v, want status=completed", got, err)
	}
}

func TestUpdateRunCommentDoesNotChangeStatusOrScript(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo := NewRemixLabRepository(db)
	now := time.Now().UTC()
	exp := RemixLabExperimentRecord{ID: "11111111-1111-1111-1111-111111111111", Title: "t", SourceText: "s", PromptStamp: "stamp", Status: "completed", CreatedAt: now, UpdatedAt: now}
	slot := RemixLabSlotRecord{ID: "22222222-2222-2222-2222-222222222222", ExperimentID: exp.ID, Model: "m", RunCount: 1}
	run := RemixLabRunRecord{
		ID:               "33333333-3333-3333-3333-333333333333",
		ExperimentID:     exp.ID,
		SlotID:           slot.ID,
		RunIndex:         1,
		Status:           "completed",
		ContinuousScript: "keep-me",
		TitlesJSON:       `["a"]`,
	}
	if err := repo.CreateExperiment(t.Context(), exp, []RemixLabSlotRecord{slot}, []RemixLabRunRecord{run}); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateRunComment(t.Context(), run.ID, "批注"); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetRun(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "completed" || got.ContinuousScript != "keep-me" || got.Comment != "批注" || got.TitlesJSON != `["a"]` {
		t.Fatalf("got=%+v", got)
	}
	exp2, _, _, err := repo.GetExperiment(t.Context(), exp.ID)
	if err != nil || exp2.Status != "completed" {
		t.Fatalf("exp=%+v err=%v", exp2, err)
	}
}

func TestUpdateRunAdoptedProjectDoesNotChangeStatusOrScript(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo := NewRemixLabRepository(db)
	now := time.Now().UTC()
	exp := RemixLabExperimentRecord{ID: "11111111-1111-1111-1111-111111111111", Title: "t", SourceText: "s", PromptStamp: "stamp", Status: "completed", CreatedAt: now, UpdatedAt: now}
	slot := RemixLabSlotRecord{ID: "22222222-2222-2222-2222-222222222222", ExperimentID: exp.ID, Model: "m", RunCount: 1}
	run := RemixLabRunRecord{
		ID:               "33333333-3333-3333-3333-333333333333",
		ExperimentID:     exp.ID,
		SlotID:           slot.ID,
		RunIndex:         1,
		Status:           "completed",
		ContinuousScript: "keep-me",
	}
	if err := repo.CreateExperiment(t.Context(), exp, []RemixLabSlotRecord{slot}, []RemixLabRunRecord{run}); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateRunAdoptedProject(t.Context(), run.ID, "proj-1"); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetRun(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "completed" || got.ContinuousScript != "keep-me" || got.AdoptedProjectID != "proj-1" {
		t.Fatalf("got=%+v", got)
	}
}
