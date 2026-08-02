package store

import (
	"context"
	"testing"
	"time"

	"video-production-console/internal/domain"
)

func TestTaskRepositoryPersistsTaskEventsAndStatus(t *testing.T) {
	db, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	if _, err = db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('a','A','#fff','active',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	r := NewTaskRepository(db)
	task := domain.CodexTask{ID: "t1", AccountID: "a", Type: "topic_select", SkillName: "finance-topic-selector", Status: domain.TaskQueued, PromptSnapshot: "p", CreatedAt: now}
	if err := r.Create(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if err := r.AppendEvent(context.Background(), task.ID, domain.TaskEvent{ID: "e1", Kind: "agent_message", Level: "info", DisplayText: "hello", RawJSON: `{"type":"x"}`, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := r.UpdateStatus(context.Background(), task.ID, domain.TaskCompleted, "done", "", ""); err != nil {
		t.Fatal(err)
	}
	got, err := r.Get(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.TaskCompleted || got.ResultSummary == nil || *got.ResultSummary != "done" {
		t.Fatalf("got=%+v", got)
	}
	events, err := r.Events(context.Background(), task.ID)
	if err != nil || len(events) != 1 || events[0].Sequence != 1 {
		t.Fatalf("events=%+v err=%v", events, err)
	}
}
