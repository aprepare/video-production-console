package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/domain"
)

func TestWorkflowBeginRemixIsIdempotentAndActiveIsUnique(t *testing.T) {
	db, accountID, projectID, now := workflowFixture(t)
	repo := NewWorkflowRepository(db)
	first, err := repo.BeginRemix(context.Background(), domain.ProjectWorkflowRun{ID: uuid.NewString(), ProjectID: projectID, AccountID: accountID, Kind: domain.WorkflowRemix, State: domain.WorkflowRunning, CurrentStep: domain.WorkflowStepTopicCard, ModelName: "gpt-5.4", ReasoningEffort: "high", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.BeginRemix(context.Background(), domain.ProjectWorkflowRun{ID: uuid.NewString(), ProjectID: projectID, AccountID: accountID, Kind: domain.WorkflowRemix, State: domain.WorkflowRunning, CurrentStep: domain.WorkflowStepTopicCard, ModelName: "other", ReasoningEffort: "low", CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || second.ModelName != first.ModelName {
		t.Fatalf("repeated begin=%+v, want existing %+v", second, first)
	}
	active, err := repo.ActiveForProject(context.Background(), projectID, domain.WorkflowRemix)
	if err != nil || active.ID != first.ID {
		t.Fatalf("active=%+v err=%v", active, err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM project_workflow_runs WHERE project_id=? AND state='running'`, projectID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("active count=%d err=%v", count, err)
	}
}

func TestWorkflowBeginRemixRejectsContradictoryInitialState(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*domain.ProjectWorkflowRun)
	}{
		{name: "topic task", mutate: func(run *domain.ProjectWorkflowRun) { run.TopicTaskID = stringPtr("") }},
		{name: "remix task", mutate: func(run *domain.ProjectWorkflowRun) { run.RemixTaskID = stringPtr("") }},
		{name: "error code", mutate: func(run *domain.ProjectWorkflowRun) { run.ErrorCode = stringPtr("") }},
		{name: "error message", mutate: func(run *domain.ProjectWorkflowRun) { run.ErrorMessage = stringPtr("") }},
		{name: "finished at", mutate: func(run *domain.ProjectWorkflowRun) { finished := run.CreatedAt; run.FinishedAt = &finished }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, accountID, projectID, now := workflowFixture(t)
			run := domain.ProjectWorkflowRun{ID: uuid.NewString(), ProjectID: projectID, AccountID: accountID, Kind: domain.WorkflowRemix, State: domain.WorkflowRunning, CurrentStep: domain.WorkflowStepTopicCard, ModelName: "gpt-5.4", ReasoningEffort: "high", CreatedAt: now, UpdatedAt: now}
			tt.mutate(&run)
			if _, err := NewWorkflowRepository(db).BeginRemix(context.Background(), run); err == nil {
				t.Fatal("BeginRemix accepted contradictory initial state")
			}
			var count int
			if err := db.QueryRow(`SELECT COUNT(*) FROM project_workflow_runs`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("workflow rows=%d err=%v", count, err)
			}
		})
	}
}

func TestWorkflowTransitionsTopicCardToRemixToCompletedAndFindsBothTasks(t *testing.T) {
	db, accountID, projectID, now := workflowFixture(t)
	topicID, remixID := uuid.NewString(), uuid.NewString()
	seedWorkflowTask(t, db, topicID, projectID, accountID, now)
	seedWorkflowTask(t, db, remixID, projectID, accountID, now)
	repo := NewWorkflowRepository(db)
	run, err := repo.BeginRemix(context.Background(), domain.ProjectWorkflowRun{ID: uuid.NewString(), ProjectID: projectID, AccountID: accountID, Kind: domain.WorkflowRemix, State: domain.WorkflowRunning, CurrentStep: domain.WorkflowStepTopicCard, ModelName: "gpt-5.4", ReasoningEffort: "high", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	run, err = repo.BindTopicTask(context.Background(), run.ID, topicID, now.Add(time.Second))
	if err != nil || run.TopicTaskID == nil || *run.TopicTaskID != topicID {
		t.Fatalf("bind=%+v err=%v", run, err)
	}
	run, err = repo.AdvanceToRemix(context.Background(), run.ID, remixID, now.Add(2*time.Second))
	if err != nil || run.CurrentStep != domain.WorkflowStepRemix || run.RemixTaskID == nil || *run.RemixTaskID != remixID {
		t.Fatalf("advance=%+v err=%v", run, err)
	}
	for _, taskID := range []string{topicID, remixID} {
		found, findErr := repo.ByTask(context.Background(), taskID)
		if findErr != nil || found.ID != run.ID {
			t.Fatalf("ByTask(%s)=%+v err=%v", taskID, found, findErr)
		}
	}
	completed, err := repo.Complete(context.Background(), run.ID, now.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if completed.State != domain.WorkflowCompleted || completed.CurrentStep != domain.WorkflowStepCompleted || completed.FinishedAt == nil {
		t.Fatalf("completed=%+v", completed)
	}
	again, err := repo.Complete(context.Background(), run.ID, now.Add(4*time.Second))
	if err != nil || again.FinishedAt == nil || !again.FinishedAt.Equal(*completed.FinishedAt) {
		t.Fatalf("repeat complete=%+v err=%v", again, err)
	}
}

func TestWorkflowTransitionReplayAndConflictBoundaries(t *testing.T) {
	db, accountID, projectID, now := workflowFixture(t)
	topicID, otherTopicID := uuid.NewString(), uuid.NewString()
	remixID, otherRemixID := uuid.NewString(), uuid.NewString()
	for _, taskID := range []string{topicID, otherTopicID, remixID, otherRemixID} {
		seedWorkflowTask(t, db, taskID, projectID, accountID, now)
	}
	repo := NewWorkflowRepository(db)
	run, err := repo.BeginRemix(context.Background(), domain.ProjectWorkflowRun{ID: uuid.NewString(), ProjectID: projectID, AccountID: accountID, Kind: domain.WorkflowRemix, State: domain.WorkflowRunning, CurrentStep: domain.WorkflowStepTopicCard, ModelName: "gpt-5.4", ReasoningEffort: "high", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AdvanceToRemix(context.Background(), run.ID, remixID, now.Add(time.Second)); !errors.Is(err, ErrWorkflowTransition) {
		t.Fatalf("advance before topic bind err=%v", err)
	}
	if _, err := repo.Complete(context.Background(), run.ID, now.Add(time.Second)); !errors.Is(err, ErrWorkflowTransition) {
		t.Fatalf("early complete err=%v", err)
	}
	bound, err := repo.BindTopicTask(context.Background(), run.ID, topicID, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := repo.BindTopicTask(context.Background(), run.ID, topicID, now.Add(3*time.Second))
	if err != nil || replayed.TopicTaskID == nil || *replayed.TopicTaskID != topicID || !replayed.UpdatedAt.Equal(bound.UpdatedAt) {
		t.Fatalf("topic replay=%+v err=%v", replayed, err)
	}
	if _, err := repo.BindTopicTask(context.Background(), run.ID, otherTopicID, now.Add(4*time.Second)); !errors.Is(err, ErrWorkflowTransition) {
		t.Fatalf("different topic bind err=%v", err)
	}
	advanced, err := repo.AdvanceToRemix(context.Background(), run.ID, remixID, now.Add(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	replayed, err = repo.AdvanceToRemix(context.Background(), run.ID, remixID, now.Add(6*time.Second))
	if err != nil || replayed.RemixTaskID == nil || *replayed.RemixTaskID != remixID || !replayed.UpdatedAt.Equal(advanced.UpdatedAt) {
		t.Fatalf("remix replay=%+v err=%v", replayed, err)
	}
	if _, err := repo.AdvanceToRemix(context.Background(), run.ID, otherRemixID, now.Add(7*time.Second)); !errors.Is(err, ErrWorkflowTransition) {
		t.Fatalf("different remix bind err=%v", err)
	}
}

func TestWorkflowFailPersistsDetailsAndIsIdempotent(t *testing.T) {
	db, accountID, projectID, now := workflowFixture(t)
	repo := NewWorkflowRepository(db)
	run, err := repo.BeginRemix(context.Background(), domain.ProjectWorkflowRun{ID: uuid.NewString(), ProjectID: projectID, AccountID: accountID, Kind: domain.WorkflowRemix, State: domain.WorkflowRunning, CurrentStep: domain.WorkflowStepTopicCard, ModelName: "gpt-5.4", ReasoningEffort: "high", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	failed, err := repo.Fail(context.Background(), run.ID, "topic_failed", "topic commit failed", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if failed.State != domain.WorkflowFailed || failed.ErrorCode == nil || *failed.ErrorCode != "topic_failed" || failed.ErrorMessage == nil || *failed.ErrorMessage != "topic commit failed" || failed.FinishedAt == nil {
		t.Fatalf("failed=%+v", failed)
	}
	again, err := repo.Fail(context.Background(), run.ID, "changed", "changed", now.Add(2*time.Second))
	if err != nil || again.ErrorCode == nil || *again.ErrorCode != "topic_failed" {
		t.Fatalf("repeat fail=%+v err=%v", again, err)
	}
	if _, err := repo.ActiveForProject(context.Background(), projectID, domain.WorkflowRemix); !errors.Is(err, ErrWorkflowNotFound) {
		t.Fatalf("active after failure err=%v", err)
	}
}

func workflowFixture(t *testing.T) (*sql.DB, string, string, time.Time) {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "workflow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := time.Date(2026, 8, 7, 8, 0, 0, 0, time.UTC)
	accountID, projectID := uuid.NewString(), uuid.NewString()
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,'#fff','active',?,?)`, accountID, "a", now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO projects(id,account_id,title,stage,created_at,updated_at) VALUES(?,?,'p','script',?,?)`, projectID, accountID, now, now); err != nil {
		t.Fatal(err)
	}
	return db, accountID, projectID, now
}

func seedWorkflowTask(t *testing.T, db *sql.DB, id, projectID, accountID string, now time.Time) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO codex_tasks(id,project_id,account_id,type,skill_name,status,prompt_snapshot,model_name,reasoning_effort,created_at) VALUES(?,?,?,'workflow','test','queued','prompt','gpt-5.4','high',?)`, id, projectID, accountID, now); err != nil {
		t.Fatal(err)
	}
}

func stringPtr(value string) *string { return &value }
