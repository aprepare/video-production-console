package httpapi

import (
	"context"
	"errors"
	"testing"
	"time"

	"video-production-console/internal/domain"
	"video-production-console/internal/store"
)

type timingPreparer struct {
	repo *store.TaskRepository
	err  error
}

func (p timingPreparer) Prepare(ctx context.Context, task domain.CodexTask, _ TaskManifestRequest) error {
	if p.err != nil {
		return p.err
	}
	return p.repo.CreateV2(ctx, task)
}

func TestTaskPrepareTimingSharedHelperSuccessAndFailure(t *testing.T) {
	for _, test := range []struct {
		name          string
		prepareErr    error
		wantState     domain.TaskPhaseState
		wantScheduled int
	}{
		{name: "success", wantState: domain.PhaseCompleted, wantScheduled: 1},
		{name: "failure", prepareErr: errors.New("manifest invalid"), wantState: domain.PhaseFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, err := store.Open(t.TempDir() + "/prepare.db")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			created := time.Date(2026, 8, 8, 8, 0, 0, 0, time.UTC)
			finished := created.Add(1500 * time.Millisecond)
			if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('a','A','#fff','active',?,?)`, created, created); err != nil {
				t.Fatal(err)
			}
			repo := store.NewTaskRepository(db)
			task := domain.CodexTask{ID: "task-" + test.name, AccountID: "a", Type: "remix", SkillName: "finance-viral-remix", Action: domain.ActionRemixEnhanced, Status: domain.TaskQueued, PromptSnapshot: "p", CreatedAt: created}
			scheduled := 0
			_, err = prepareAndPublishTask(context.Background(), db, timingPreparer{repo: repo, err: test.prepareErr}, task, TaskManifestRequest{}, created, func(context.Context, domain.CodexTask) error { scheduled++; return nil }, func() time.Time { return finished })
			if test.prepareErr == nil && err != nil || test.prepareErr != nil && err == nil {
				t.Fatalf("prepare err=%v", err)
			}
			if scheduled != test.wantScheduled {
				t.Fatalf("scheduled=%d", scheduled)
			}
			persisted, readErr := repo.Get(context.Background(), task.ID)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if test.prepareErr != nil && persisted.Status != domain.TaskFailed {
				t.Fatalf("task=%+v", persisted)
			}
			phases, readErr := store.NewTaskTimingRepository(db).ForTask(context.Background(), task.ID)
			if readErr != nil || len(phases) != 1 || phases[0].PhaseKey != "task_prepare" || phases[0].DisplayName != "任务准备" || phases[0].State != test.wantState || phases[0].DurationMS == nil || *phases[0].DurationMS != 1500 {
				t.Fatalf("phases=%+v err=%v", phases, readErr)
			}
		})
	}
}
