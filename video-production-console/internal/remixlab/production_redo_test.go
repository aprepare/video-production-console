package remixlab

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"video-production-console/internal/agentruntime/openaicompat"
	"video-production-console/internal/store"
)

type recordingLauncher struct {
	launched []string
}

func (l *recordingLauncher) Launch(runID string) {
	l.launched = append(l.launched, runID)
}

// 造一条已完成的生产记录（挂在真实 run 上，满足外键），返回可重做的现场。
func redoFixture(t *testing.T) (*Service, *recordingLauncher, string) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo := store.NewRemixLabRepository(db)

	runner := func(_ context.Context, opts openaicompat.Options) error {
		return os.WriteFile(opts.OutputLastMessage, []byte(`{"status":"failed","summary":"stub"}`), 0o644)
	}
	svc := NewService(repo, stubRuntime{view: RuntimeView{RemixAPIKey: "sk-runtime"}}, remixFakeProtector{}, t.TempDir(), runner, nil, nil)
	exp, err := svc.CreateExperiment(t.Context(), "重做测试原文一二三四五", []SlotInput{{Model: "m", RunCount: 1}})
	if err != nil {
		t.Fatal(err)
	}
	done := waitExperimentTerminal(t, svc, exp.ID)
	runID := done.Runs[0].ID

	now := time.Now().UTC()
	rec := store.RemixLabProductionRecord{
		RunID: runID, ExperimentID: exp.ID, AccountID: "acct-1",
		Status: "completed", Step: "done", ProjectID: "proj-1",
		SpokenTaskID: "task-spoken", CaptionTaskID: "task-captions", MontageTaskID: "task-montage",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := repo.UpsertProduction(t.Context(), rec); err != nil {
		t.Fatal(err)
	}
	launcher := &recordingLauncher{}
	svc.SetProducer(launcher)
	return svc, launcher, runID
}

func TestRedoProductionStepFromSpoken(t *testing.T) {
	svc, launcher, runID := redoFixture(t)

	if err := svc.RedoProductionStep(t.Context(), runID, "spoken"); err != nil {
		t.Fatal(err)
	}
	rec, err := svc.repo.GetProduction(t.Context(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != "running" || rec.Step != "spoken" {
		t.Fatalf("record after redo: %+v", rec)
	}
	if rec.SpokenTaskID != "" || rec.CaptionTaskID != "" || rec.MontageTaskID != "" {
		t.Fatalf("task ids must be cleared: %+v", rec)
	}
	if !rec.ForceNarration {
		t.Fatalf("redo spoken must force narration: %+v", rec)
	}
	if len(launcher.launched) != 1 || launcher.launched[0] != runID {
		t.Fatalf("launcher calls: %v", launcher.launched)
	}

	// 运行中再重做：拒绝。
	if err := svc.RedoProductionStep(t.Context(), runID, "montage"); !errors.Is(err, ErrProductionActive) {
		t.Fatalf("expected ErrProductionActive, got %v", err)
	}
}

func TestRedoProductionStepMontageOnly(t *testing.T) {
	svc, launcher, runID := redoFixture(t)

	if err := svc.RedoProductionStep(t.Context(), runID, "montage"); err != nil {
		t.Fatal(err)
	}
	rec, err := svc.repo.GetProduction(t.Context(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Step != "montage" || rec.Status != "running" {
		t.Fatalf("record after redo: %+v", rec)
	}
	if rec.SpokenTaskID != "task-spoken" || rec.CaptionTaskID != "task-captions" {
		t.Fatalf("upstream task ids must be kept: %+v", rec)
	}
	if rec.MontageTaskID != "" {
		t.Fatalf("montage task id must be cleared: %+v", rec)
	}
	if rec.ForceNarration {
		t.Fatalf("redo montage must not force narration: %+v", rec)
	}
	if len(launcher.launched) != 1 {
		t.Fatalf("launcher calls: %v", launcher.launched)
	}
}

func TestRedoProductionStepRejectsInvalid(t *testing.T) {
	svc, _, runID := redoFixture(t)

	// 实验没有工作流快照 → 字幕关键词默认关闭，不能重做该步。
	if err := svc.RedoProductionStep(t.Context(), runID, "captions"); !errors.Is(err, ErrProductionStepInvalid) {
		t.Fatalf("expected ErrProductionStepInvalid for disabled captions, got %v", err)
	}
	if err := svc.RedoProductionStep(t.Context(), runID, "project"); !errors.Is(err, ErrProductionStepInvalid) {
		t.Fatalf("expected ErrProductionStepInvalid for project, got %v", err)
	}
}
