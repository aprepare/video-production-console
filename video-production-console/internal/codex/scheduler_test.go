package codex

import (
	"context"
	"database/sql"
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

var _ *sql.DB
