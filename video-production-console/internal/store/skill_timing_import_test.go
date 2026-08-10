package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"video-production-console/internal/domain"
)

func skillTimingImportFixture(t *testing.T, withSnapshot bool) (*sql.DB, time.Time) {
	t.Helper()
	db, err := Open(t.TempDir() + "/skill-timings.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	t0 := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('a','A','#fff','active',?,?)`, t0, t0); err != nil {
		t.Fatal(err)
	}
	tasks := NewTaskRepository(db)
	if err := tasks.CreateV2(context.Background(), domain.CodexTask{ID: "task", AccountID: "a", Type: "remix", SkillName: "skill", Action: domain.ActionRemixEnhanced, Status: domain.TaskQueued, PromptSnapshot: "p", CreatedAt: t0}); err != nil {
		t.Fatal(err)
	}
	if withSnapshot {
		if _, err := db.Exec(`INSERT INTO skill_snapshots(id,name,path,sha256,files_json,modified_at,created_at) VALUES('snapshot','skill','/skill','sha','[]',?,?)`, t0, t0); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE codex_tasks SET skill_snapshot_id='snapshot' WHERE id='task'`); err != nil {
			t.Fatal(err)
		}
	}
	return db, t0
}

func skillTimingRun(taskID, snapshotID, externalID string, started time.Time) domain.SkillTimingRun {
	finished := started.Add(1500 * time.Millisecond)
	return domain.SkillTimingRun{
		TaskID: taskID, SkillSnapshotID: snapshotID, PhaseKey: "draft_build", DisplayName: "草稿构建", ExternalID: externalID,
		DetailJSON: `{}`, Attempt: 1, State: domain.PhaseCompleted, StartedAt: started, FinishedAt: finished, DurationMS: 1500,
	}
}

func importSkillTimingInTx(t *testing.T, db *sql.DB, taskID string, runs []domain.SkillTimingRun) error {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := importSkillTimingsTx(context.Background(), tx, taskID, runs, time.Date(2026, 8, 9, 10, 1, 0, 0, time.UTC)); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func TestImportSkillTimingsRequiresTaskBoundSnapshot(t *testing.T) {
	for _, test := range []struct {
		name       string
		withSnap   bool
		taskID     string
		runTaskID  string
		snapshotID string
	}{
		{name: "missing snapshot", withSnap: false, taskID: "task", runTaskID: "task", snapshotID: "snapshot"},
		{name: "task mismatch", withSnap: true, taskID: "task", runTaskID: "other-task", snapshotID: "snapshot"},
		{name: "snapshot mismatch", withSnap: true, taskID: "task", runTaskID: "task", snapshotID: "other-snapshot"},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, t0 := skillTimingImportFixture(t, test.withSnap)
			err := importSkillTimingInTx(t, db, test.taskID, []domain.SkillTimingRun{skillTimingRun(test.runTaskID, test.snapshotID, "run-1", t0.Add(time.Second))})
			if err == nil {
				t.Fatal("invalid task/snapshot binding accepted")
			}
		})
	}
}

func TestImportSkillTimingsIsIdempotentForIdenticalReplay(t *testing.T) {
	db, t0 := skillTimingImportFixture(t, true)
	run := skillTimingRun("task", "snapshot", "run-1", t0.Add(time.Second))
	if err := importSkillTimingInTx(t, db, "task", []domain.SkillTimingRun{run}); err != nil {
		t.Fatal(err)
	}
	if err := importSkillTimingInTx(t, db, "task", []domain.SkillTimingRun{run}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM task_phase_runs WHERE task_id='task' AND source='skill'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("skill timing rows=%d, want 1", count)
	}
}

func TestImportSkillTimingsRejectsConflictAndRollsBackTransaction(t *testing.T) {
	db, t0 := skillTimingImportFixture(t, true)
	original := skillTimingRun("task", "snapshot", "run-1", t0.Add(time.Second))
	if err := importSkillTimingInTx(t, db, "task", []domain.SkillTimingRun{original}); err != nil {
		t.Fatal(err)
	}
	conflict := original
	conflict.DurationMS = 2000
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO task_events(id,task_id,sequence,kind,level,display_text,raw_json,created_at) VALUES('event-rollback','task',1,'test','info','before','{}',?)`, t0); err != nil {
		t.Fatal(err)
	}
	err = importSkillTimingsTx(context.Background(), tx, "task", []domain.SkillTimingRun{conflict}, t0)
	if err == nil {
		t.Fatal("conflicting replay accepted")
	}
	if !errors.Is(tx.Rollback(), nil) {
		t.Fatal("rollback failed")
	}
	var eventCount, timingCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM task_events WHERE id='event-rollback'`).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM task_phase_runs WHERE task_id='task' AND source='skill'`).Scan(&timingCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 0 || timingCount != 1 {
		t.Fatalf("after rollback event=%d timing=%d", eventCount, timingCount)
	}
}
