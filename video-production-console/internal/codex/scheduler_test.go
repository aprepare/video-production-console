package codex

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
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
