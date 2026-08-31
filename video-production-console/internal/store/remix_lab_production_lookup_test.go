package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestGetProductionByProjectID(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := NewRemixLabRepository(db)
	now := time.Date(2026, 8, 31, 8, 0, 0, 0, time.UTC)
	exp := RemixLabExperimentRecord{
		ID: "11111111-1111-1111-1111-111111111111", Title: "实验", SourceText: "原文",
		Status: "completed", CreatedAt: now, UpdatedAt: now,
	}
	slot := RemixLabSlotRecord{ID: "22222222-2222-2222-2222-222222222222", ExperimentID: exp.ID, Label: "s", Model: "m", RunCount: 1}
	oldRun := RemixLabRunRecord{
		ID: "33333333-3333-3333-3333-333333333333", ExperimentID: exp.ID, SlotID: slot.ID, RunIndex: 1, Status: "completed",
	}
	newRun := RemixLabRunRecord{
		ID: "44444444-4444-4444-4444-444444444444", ExperimentID: exp.ID, SlotID: slot.ID, RunIndex: 2, Status: "completed",
	}
	if err := repo.CreateExperiment(context.Background(), exp, []RemixLabSlotRecord{slot}, []RemixLabRunRecord{oldRun, newRun}); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpsertProduction(context.Background(), RemixLabProductionRecord{
		RunID: oldRun.ID, ExperimentID: exp.ID, Status: "completed", Step: "done",
		ProjectID: "proj-1", CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpsertProduction(context.Background(), RemixLabProductionRecord{
		RunID: newRun.ID, ExperimentID: exp.ID, Status: "running", Step: "montage",
		ProjectID: "proj-1", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetProductionByProjectID(context.Background(), "proj-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.RunID != newRun.ID {
		t.Fatalf("got %+v", got)
	}
	if _, err := repo.GetProductionByProjectID(context.Background(), "missing"); err != ErrRemixLabNotFound {
		t.Fatalf("missing project: %v", err)
	}
}
