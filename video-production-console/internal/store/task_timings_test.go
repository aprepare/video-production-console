package store

import (
	"context"
	"testing"
	"time"

	"video-production-console/internal/domain"
)

func timingFixture(t *testing.T) (*TaskTimingRepository, *TaskRepository, string, time.Time) {
	t.Helper()
	db, err := Open(t.TempDir() + "/timings.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	t0 := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('a','A','#fff','active',?,?)`, t0, t0); err != nil {
		t.Fatal(err)
	}
	tasks := NewTaskRepository(db)
	if err := tasks.CreateV2(context.Background(), domain.CodexTask{ID: "task", AccountID: "a", Type: "remix", SkillName: "finance-viral-remix", Action: domain.ActionRemixEnhanced, Status: domain.TaskQueued, PromptSnapshot: "p", CreatedAt: t0}); err != nil {
		t.Fatal(err)
	}
	return NewTaskTimingRepository(db), tasks, "task", t0
}

func TestTaskPhaseLifecycleIdempotencyAttemptsAndRestart(t *testing.T) {
	repo, _, taskID, t0 := timingFixture(t)
	ctx := context.Background()
	queued, err := repo.StartPhase(ctx, StartPhase{TaskID: taskID, Attempt: 1, Key: "codex_execution", DisplayName: "Codex 执行", Source: domain.PhaseSourceAppServer, State: domain.PhaseQueued, ExternalID: "item-1", At: t0.Add(time.Second)})
	if err != nil || queued.State != domain.PhaseQueued || queued.RunningAt != nil {
		t.Fatalf("queued=%+v err=%v", queued, err)
	}
	duplicate, err := repo.StartPhase(ctx, StartPhase{TaskID: taskID, Attempt: 1, Key: "codex_execution", DisplayName: "Codex 执行", Source: domain.PhaseSourceAppServer, State: domain.PhaseQueued, ExternalID: "item-1", At: t0.Add(2 * time.Second)})
	if err != nil || duplicate.ID != queued.ID || !duplicate.StartedAt.Equal(queued.StartedAt) {
		t.Fatalf("duplicate=%+v err=%v", duplicate, err)
	}
	runningAt := t0.Add(3 * time.Second)
	running, err := repo.MarkPhaseRunning(ctx, queued.ID, runningAt)
	if err != nil || running.RunningAt == nil || !running.RunningAt.Equal(runningAt) || !running.StartedAt.Equal(queued.StartedAt) {
		t.Fatalf("running=%+v err=%v", running, err)
	}
	finishedAt := t0.Add(5500 * time.Millisecond)
	finished, err := repo.FinishPhase(ctx, FinishPhase{ID: queued.ID, State: domain.PhaseCompleted, At: finishedAt})
	if err != nil || finished.DurationMS == nil || *finished.DurationMS != 4500 {
		t.Fatalf("finished=%+v err=%v", finished, err)
	}
	for attempt, state := range map[int]domain.TaskPhaseState{2: domain.PhaseFailed, 3: domain.PhaseCanceled, 4: domain.PhaseInterrupted} {
		phase, err := repo.StartPhase(ctx, StartPhase{TaskID: taskID, Attempt: attempt, Key: "result_validation", DisplayName: "结果校验", Source: domain.PhaseSourceHost, At: t0.Add(time.Duration(attempt) * time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repo.FinishPhase(ctx, FinishPhase{ID: phase.ID, State: state, At: phase.StartedAt.Add(time.Second)}); err != nil {
			t.Fatalf("finish %s: %v", state, err)
		}
	}
	uncertain, err := repo.StartPhase(ctx, StartPhase{TaskID: taskID, Attempt: 5, Key: "asset_commit", DisplayName: "资产入库", Source: domain.PhaseSourceHost, At: t0.Add(5 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if count, err := repo.InterruptRunning(ctx, uncertain.StartedAt.Add(2*time.Second)); err != nil || count != 1 {
		t.Fatalf("interrupt count=%d err=%v", count, err)
	}
	phases, err := repo.ForTask(ctx, taskID)
	if err != nil || len(phases) != 5 || phases[4].State != domain.PhaseInterrupted {
		t.Fatalf("phases=%+v err=%v", phases, err)
	}
}

func TestTaskPhaseRejectsNegativeDurationAndInvalidTransitions(t *testing.T) {
	repo, _, taskID, t0 := timingFixture(t)
	phase, err := repo.StartPhase(context.Background(), StartPhase{TaskID: taskID, Attempt: 1, Key: "result_validation", DisplayName: "结果校验", Source: domain.PhaseSourceHost, At: t0.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.FinishPhase(context.Background(), FinishPhase{ID: phase.ID, State: domain.PhaseCompleted, At: t0}); err == nil {
		t.Fatal("negative duration accepted")
	}
}

func TestTaskTimingSummaryRunningTerminalLegacyAndProjectAggregate(t *testing.T) {
	repo, tasks, taskID, t0 := timingFixture(t)
	ctx := context.Background()
	queuedAt := t0.Add(2 * time.Second)
	if err := tasks.MarkQueued(ctx, taskID, queuedAt); err != nil {
		t.Fatal(err)
	}
	phase, err := repo.StartPhase(ctx, StartPhase{TaskID: taskID, Attempt: 1, Key: "codex_execution", DisplayName: "Codex 执行", Source: domain.PhaseSourceHost, State: domain.PhaseQueued, At: t0.Add(5 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	queuedSummary, err := repo.SummaryForTask(ctx, taskID, t0.Add(15*time.Second))
	if err != nil || queuedSummary.QueueMS != 13000 || queuedSummary.ExecutionMS != 0 {
		t.Fatalf("queued summary=%+v err=%v", queuedSummary, err)
	}
	if _, err := repo.MarkPhaseRunning(ctx, phase.ID, t0.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.DB().Exec(`UPDATE codex_tasks SET status='running',started_at=? WHERE id=?`, t0.Add(5*time.Second), taskID); err != nil {
		t.Fatal(err)
	}
	running, err := repo.SummaryForTask(ctx, taskID, t0.Add(15*time.Second))
	if err != nil || running.TotalMS != 15000 || running.PreparationMS != 2000 || running.QueueMS != 3000 || running.ExecutionMS != 10000 || running.QueueEstimated {
		t.Fatalf("running summary=%+v err=%v", running, err)
	}
	if _, err := repo.FinishPhase(ctx, FinishPhase{ID: phase.ID, State: domain.PhaseCompleted, At: t0.Add(13 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.DB().Exec(`UPDATE codex_tasks SET status='completed',started_at=?,finished_at=? WHERE id=?`, t0.Add(5*time.Second), t0.Add(15*time.Second), taskID); err != nil {
		t.Fatal(err)
	}
	terminal, err := repo.SummaryForTask(ctx, taskID, t0.Add(time.Hour))
	if err != nil || terminal.TotalMS != 15000 || terminal.SlowestPhase == nil || terminal.SlowestPhase.PhaseKey != "codex_execution" || terminal.SlowestPhasePercent != 80 {
		t.Fatalf("terminal summary=%+v err=%v", terminal, err)
	}
	legacy := domain.CodexTask{ID: "legacy", AccountID: "a", Type: "remix", SkillName: "finance-viral-remix", Action: domain.ActionRemixEnhanced, Status: domain.TaskCompleted, PromptSnapshot: "p", CreatedAt: t0}
	if err := tasks.CreateV2(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.DB().Exec(`UPDATE codex_tasks SET started_at=?,finished_at=? WHERE id='legacy'`, t0.Add(time.Second), t0.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	legacySummary, err := repo.SummaryForTask(ctx, "legacy", t0.Add(time.Hour))
	if err != nil || !legacySummary.QueueEstimated || legacySummary.QueueMS != 1000 || !legacySummary.LegacyWithoutPhases {
		t.Fatalf("legacy summary=%+v err=%v", legacySummary, err)
	}
	aggregates, err := repo.ProjectSummary(ctx, "", 10)
	if err != nil || len(aggregates) != 1 || aggregates[0].Action != domain.ActionRemixEnhanced || aggregates[0].TaskCount != 2 || len(aggregates[0].Phases) != 1 || aggregates[0].Phases[0].PhaseKey != "codex_execution" || aggregates[0].Phases[0].MedianDurationMS != 8000 || aggregates[0].Phases[0].MaxDurationMS != 8000 {
		t.Fatalf("aggregates=%+v err=%v", aggregates, err)
	}
}
