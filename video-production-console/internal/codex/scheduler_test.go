package codex

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"
	"video-production-console/internal/domain"
	"video-production-console/internal/store"
)

func schedulerDB(t *testing.T) *store.TaskRepository {
	t.Helper()
	d, err := store.Open(t.TempDir() + "/db.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return store.NewTaskRepository(d)
}
func TestSchedulerLimitAndValidation(t *testing.T) {
	repo := schedulerDB(t)
	s, e := NewScheduler(repo, 2, func(domain.CodexTask) (*exec.Cmd, string, error) {
		return exec.Command("cmd", "/c", "exit", "0"), t.TempDir(), nil
	}, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e := s.SetLimit(0); e == nil {
		t.Fatal("expected limit error")
	}
	if e := s.SetLimit(4); e != nil {
		t.Fatal(e)
	}
	if s.Snapshot().Limit != 4 {
		t.Fatal("limit not updated")
	}
}
func TestSchedulerEnqueuePersistsQueuedTask(t *testing.T) {
	repo := schedulerDB(t)
	_, _ = repo.DB().Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('a','A','#000','active',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)
	s, e := NewScheduler(repo, 1, func(task domain.CodexTask) (*exec.Cmd, string, error) {
		return exec.Command("cmd", "/c", "exit", "0"), t.TempDir(), nil
	}, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	id := "task-1"
	now := time.Now().UTC()
	err := s.Enqueue(context.Background(), domain.CodexTask{ID: id, AccountID: "a", Type: "topic_select", SkillName: "finance-topic-selector", Status: domain.TaskQueued, PromptSnapshot: "x", CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	_, err = repo.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
}

func TestQueueBoundaryStartsOnlyAfterLegacySlotAndProjectLock(t *testing.T) {
	repo := schedulerDB(t)
	_, _ = repo.DB().Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('a','A','#000','active',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseFactory := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseFactory()
	s, err := NewScheduler(repo, 1, func(domain.CodexTask) (*exec.Cmd, string, error) {
		close(entered)
		<-release
		cmd := exec.Command(os.Args[0], "-test.run=TestSchedulerBlockingProcess")
		cmd.Env = append(os.Environ(), "VIDEO_CONSOLE_SCHEDULER_BLOCKING_PROCESS=1")
		return cmd, t.TempDir(), nil
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	task := domain.CodexTask{ID: "queue-boundary", AccountID: "a", Type: "topic_select", SkillName: "finance-topic-selector", Status: domain.TaskQueued, PromptSnapshot: "x", CreatedAt: time.Now().UTC()}
	if err := s.Enqueue(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler did not inspect queued task")
	}
	if err := s.Enqueue(context.Background(), task); err != nil {
		t.Fatalf("idempotent enqueue: %v", err)
	}
	queued, err := repo.Get(context.Background(), task.ID)
	if err != nil || queued.Status != domain.TaskQueued || queued.QueuedAt == nil || queued.StartedAt != nil {
		t.Fatalf("queued task=%+v err=%v", queued, err)
	}
	phases, err := store.NewTaskTimingRepository(repo.DB()).ForTask(context.Background(), task.ID)
	if err != nil || len(phases) != 1 || phases[0].PhaseKey != "queue_wait" || phases[0].Attempt != 1 || phases[0].State != domain.PhaseRunning || !phases[0].StartedAt.Equal(*queued.QueuedAt) {
		t.Fatalf("queued phases=%+v err=%v", phases, err)
	}
	releaseFactory()
	waitForTaskStatus(t, repo, task.ID, domain.TaskRunning)
	running, _ := repo.Get(context.Background(), task.ID)
	if running.StartedAt == nil {
		t.Fatal("legacy task has no actual start boundary")
	}
	phases, err = store.NewTaskTimingRepository(repo.DB()).ForTask(context.Background(), task.ID)
	if err != nil || len(phases) != 2 || phases[0].PhaseKey != "queue_wait" || phases[0].State != domain.PhaseCompleted || phases[1].PhaseKey != "codex_execution" || phases[1].State != domain.PhaseRunning {
		t.Fatalf("running phases=%+v err=%v", phases, err)
	}
}

func TestQueueBoundaryCommandBuildFailureDoesNotFabricateExecution(t *testing.T) {
	repo := schedulerDB(t)
	_, _ = repo.DB().Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('a','A','#000','active',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)
	s, err := NewScheduler(repo, 1, func(domain.CodexTask) (*exec.Cmd, string, error) { return nil, "", errors.New("build failed") }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	task := domain.CodexTask{ID: "queue-build-fail", AccountID: "a", Type: "topic_select", SkillName: "finance-topic-selector", Status: domain.TaskQueued, PromptSnapshot: "x", CreatedAt: time.Now().UTC()}
	if err := s.Enqueue(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	waitForTaskStatus(t, repo, task.ID, domain.TaskFailed)
	phases, err := store.NewTaskTimingRepository(repo.DB()).ForTask(context.Background(), task.ID)
	if err != nil || len(phases) != 1 || phases[0].PhaseKey != "queue_wait" || phases[0].State != domain.PhaseFailed {
		t.Fatalf("phases=%+v err=%v", phases, err)
	}
}

func TestSchedulerClaimFailureRollsBackStartBeforeQueuedFailure(t *testing.T) {
	repo := schedulerDB(t)
	_, _ = repo.DB().Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('a','A','#000','active',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)
	if _, err := repo.DB().Exec(`CREATE TRIGGER reject_scheduler_execution BEFORE INSERT ON task_phase_runs WHEN NEW.phase_key='codex_execution' BEGIN SELECT RAISE(ABORT,'reject execution'); END`); err != nil {
		t.Fatal(err)
	}
	s, err := NewScheduler(repo, 1, func(domain.CodexTask) (*exec.Cmd, string, error) {
		return exec.Command("cmd", "/c", "exit", "0"), t.TempDir(), nil
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	task := domain.CodexTask{ID: "claim-rollback", AccountID: "a", Type: "topic_select", SkillName: "finance-topic-selector", Status: domain.TaskQueued, PromptSnapshot: "x", CreatedAt: time.Now().UTC()}
	if err := s.Enqueue(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	waitForTaskStatus(t, repo, task.ID, domain.TaskFailed)
	phases, err := store.NewTaskTimingRepository(repo.DB()).ForTask(context.Background(), task.ID)
	if err != nil || len(phases) != 1 || phases[0].PhaseKey != "queue_wait" || phases[0].State != domain.PhaseFailed {
		t.Fatalf("phases=%+v err=%v", phases, err)
	}
}

func TestSchedulerResumeRollsBackAnswerWhenQueueAdmissionFails(t *testing.T) {
	repo := schedulerDB(t)
	_, _ = repo.DB().Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('a','A','#000','active',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)
	sessionID := "legacy-session"
	task := domain.CodexTask{ID: "resume-rollback", AccountID: "a", Type: "topic_select", SkillName: "finance-topic-selector", Action: domain.ActionTopicBrainstorm, Status: domain.TaskAwaitingInput, CodexSessionID: &sessionID, PromptSnapshot: "x", CreatedAt: time.Now().UTC()}
	if err := repo.CreateV2(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB().Exec(`CREATE TRIGGER reject_resume_queue BEFORE INSERT ON task_phase_runs WHEN NEW.phase_key='queue_wait' BEGIN SELECT RAISE(ABORT,'reject queue'); END`); err != nil {
		t.Fatal(err)
	}
	s, err := NewScheduler(repo, 1, func(domain.CodexTask) (*exec.Cmd, string, error) {
		return exec.Command("cmd", "/c", "exit", "0"), t.TempDir(), nil
	}, func(domain.CodexTask, string) (*exec.Cmd, string, error) {
		return exec.Command("cmd", "/c", "exit", "0"), t.TempDir(), nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Resume(context.Background(), task.ID, "answer"); err == nil {
		t.Fatal("resume succeeded despite queue timing failure")
	}
	persisted, err := repo.Get(context.Background(), task.ID)
	if err != nil || persisted.Status != domain.TaskAwaitingInput {
		t.Fatalf("task=%+v err=%v", persisted, err)
	}
	messages, err := repo.Messages(context.Background(), task.ID)
	if err != nil || len(messages) != 0 {
		t.Fatalf("messages=%+v err=%v", messages, err)
	}
}

func TestSchedulerCancelRollsBackQueueTimingWhenStatusWriteFails(t *testing.T) {
	repo := schedulerDB(t)
	_, _ = repo.DB().Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('a','A','#000','active',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)
	s, err := NewScheduler(repo, 1, func(domain.CodexTask) (*exec.Cmd, string, error) {
		return exec.Command("cmd", "/c", "exit", "0"), t.TempDir(), nil
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.mu.Lock()
	s.running["slot-blocker"] = &scheduled{cancel: func() {}}
	s.mu.Unlock()
	task := domain.CodexTask{ID: "cancel-rollback", AccountID: "a", Type: "topic_select", SkillName: "finance-topic-selector", Status: domain.TaskQueued, PromptSnapshot: "x", CreatedAt: time.Now().UTC()}
	if _, err := repo.AdmitQueuedTask(context.Background(), task, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB().Exec(`CREATE TRIGGER reject_scheduler_cancel BEFORE UPDATE ON codex_tasks WHEN NEW.status='canceled' BEGIN SELECT RAISE(ABORT,'reject cancel'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.Cancel(context.Background(), task.ID); err == nil {
		t.Fatal("cancel succeeded despite status write failure")
	}
	persisted, err := repo.Get(context.Background(), task.ID)
	if err != nil || persisted.Status != domain.TaskQueued {
		t.Fatalf("task=%+v err=%v", persisted, err)
	}
	phases, err := store.NewTaskTimingRepository(repo.DB()).ForTask(context.Background(), task.ID)
	if err != nil || len(phases) != 1 || phases[0].State != domain.PhaseRunning || phases[0].FinishedAt != nil {
		t.Fatalf("phases=%+v err=%v", phases, err)
	}
}

func TestSchedulerBlockingProcess(t *testing.T) {
	if os.Getenv("VIDEO_CONSOLE_SCHEDULER_BLOCKING_PROCESS") != "1" {
		return
	}
	time.Sleep(30 * time.Second)
}

func TestSchedulerRunsIndependentProjectlessTasksConcurrently(t *testing.T) {
	repo := schedulerDB(t)
	_, _ = repo.DB().Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES
		('account-1','A1','#000','active',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP),
		('account-2','A2','#111','active',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)
	root := t.TempDir()
	s, err := NewScheduler(repo, 2, func(domain.CodexTask) (*exec.Cmd, string, error) {
		cmd := exec.Command(os.Args[0], "-test.run=TestSchedulerBlockingProcess")
		cmd.Env = append(os.Environ(), "VIDEO_CONSOLE_SCHEDULER_BLOCKING_PROCESS=1")
		return cmd, root, nil
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	now := time.Now().UTC()
	for _, task := range []domain.CodexTask{
		{ID: "projectless-1", AccountID: "account-1", Type: "topic_select", SkillName: "finance-topic-selector", Status: domain.TaskQueued, PromptSnapshot: "first", CreatedAt: now},
		{ID: "projectless-2", AccountID: "account-2", Type: "topic_select", SkillName: "finance-topic-selector", Status: domain.TaskQueued, PromptSnapshot: "second", CreatedAt: now.Add(time.Millisecond)},
	} {
		if err := s.Enqueue(context.Background(), task); err != nil {
			t.Fatal(err)
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if snapshot := s.Snapshot(); snapshot.Running == 2 && snapshot.Queued == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("snapshot = %#v, want two projectless tasks running concurrently", s.Snapshot())
}

func TestSchedulerCancelRunningTaskPersistsCancelledStatus(t *testing.T) {
	repo := schedulerDB(t)
	_, _ = repo.DB().Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES
		('account-cancel','Cancel','#000','active',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)
	root := t.TempDir()
	s, err := NewScheduler(repo, 1, func(domain.CodexTask) (*exec.Cmd, string, error) {
		cmd := exec.Command(os.Args[0], "-test.run=TestSchedulerBlockingProcess")
		cmd.Env = append(os.Environ(), "VIDEO_CONSOLE_SCHEDULER_BLOCKING_PROCESS=1")
		return cmd, root, nil
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	observed := make(chan domain.CodexTask, 1)
	s.SetCompletionObserver(completionObserverFunc(func(ctx context.Context, task domain.CodexTask) error {
		persisted, err := repo.Get(ctx, task.ID)
		if err == nil && persisted.Status == domain.TaskCanceled {
			observed <- task
		}
		return err
	}))

	task := domain.CodexTask{
		ID: "cancel-running", AccountID: "account-cancel", Type: "topic_select",
		SkillName: "finance-topic-selector", Status: domain.TaskQueued,
		PromptSnapshot: "cancel me", CreatedAt: time.Now().UTC(),
	}
	if err := s.Enqueue(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	waitForTaskStatus(t, repo, task.ID, domain.TaskRunning)
	if err := s.Cancel(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	waitForTaskStatus(t, repo, task.ID, domain.TaskCanceled)

	got, err := repo.Get(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ErrorCode == nil || *got.ErrorCode != "canceled" {
		t.Fatalf("error code = %v, want canceled", got.ErrorCode)
	}
	select {
	case task := <-observed:
		if task.Status != domain.TaskCanceled {
			t.Fatalf("observed=%+v", task)
		}
	case <-time.After(time.Second):
		t.Fatal("completion observer was not called")
	}
}

func waitForTaskStatus(t *testing.T, repo *store.TaskRepository, taskID string, want domain.TaskStatus) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		task, err := repo.Get(context.Background(), taskID)
		if err == nil && task.Status == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	task, err := repo.Get(context.Background(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	t.Fatalf("task status = %q, want %q (code=%v message=%v)", task.Status, want, task.ErrorCode, task.ErrorMessage)
}

var _ *sql.DB
