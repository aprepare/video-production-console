package remixlab

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"video-production-console/internal/store"
)

func TestFailStaleMarksRunningFailed(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := store.NewRemixLabRepository(db)
	svc := NewService(repo, stubRuntime{view: RuntimeView{RemixAPIKey: "sk-runtime"}}, remixFakeProtector{}, t.TempDir(), fastStubRunner, nil, nil)

	now := time.Now().UTC()
	expID := uuid.NewString()
	slotID := uuid.NewString()
	runID := uuid.NewString()
	if err := repo.CreateExperiment(t.Context(), store.RemixLabExperimentRecord{
		ID: expID, Title: "t", SourceText: "s", PromptStamp: "stamp", Status: "running", CreatedAt: now, UpdatedAt: now,
	}, []store.RemixLabSlotRecord{{ID: slotID, ExperimentID: expID, Model: "m", RunCount: 1}}, []store.RemixLabRunRecord{
		{ID: runID, ExperimentID: expID, SlotID: slotID, RunIndex: 1, Status: "running"},
	}); err != nil {
		t.Fatal(err)
	}

	if err := svc.FailStale(t.Context()); err != nil {
		t.Fatal(err)
	}

	got, err := svc.GetExperiment(t.Context(), expID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "failed" {
		t.Fatalf("experiment status=%q, want failed", got.Status)
	}
	if len(got.Runs) != 1 {
		t.Fatalf("runs=%d", len(got.Runs))
	}
	if got.Runs[0].Status != "failed" {
		t.Fatalf("run status=%q, want failed", got.Runs[0].Status)
	}
	if got.Runs[0].ErrorMessage != "控制台已重启" {
		t.Fatalf("error_message=%q, want 控制台已重启", got.Runs[0].ErrorMessage)
	}
}
