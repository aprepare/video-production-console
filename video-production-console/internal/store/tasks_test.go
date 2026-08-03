package store

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
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

func newTaskResultRepository(t *testing.T) (*sql.DB, *TaskRepository, domain.CodexTask) {
	t.Helper()
	db, err := Open(t.TempDir() + "/task-result.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := time.Now().UTC()
	accountID := uuid.NewString()
	projectID := uuid.NewString()
	taskID := uuid.NewString()
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,?,?,?,?)`, accountID, "A", "#fff", "active", now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO projects(id,account_id,title,stage,created_at,updated_at) VALUES(?,?,?,?,?,?)`, projectID, accountID, "P", domain.StageScript, now, now); err != nil {
		t.Fatal(err)
	}
	repo := NewTaskRepository(db)
	task := domain.CodexTask{ID: taskID, ProjectID: &projectID, AccountID: accountID, Type: "remix", SkillName: "finance-viral-remix", Status: domain.TaskQueued, PromptSnapshot: "prompt", CreatedAt: now}
	if err := repo.Create(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE codex_tasks SET action=? WHERE id=?`, domain.ActionRemixStandard, task.ID); err != nil {
		t.Fatal(err)
	}
	return db, repo, task
}

func TestTaskRepositoryStartsWithPersistedCommandSnapshot(t *testing.T) {
	db, repo, task := newTaskResultRepository(t)
	snapshot := `{"binary":"codex","args":["exec"],"environment_keys":[]}`
	if err := repo.Start(context.Background(), task.ID, snapshot); err != nil {
		t.Fatal(err)
	}
	var status domain.TaskStatus
	var got string
	if err := db.QueryRow(`SELECT status,config_snapshot_json FROM codex_tasks WHERE id=?`, task.ID).Scan(&status, &got); err != nil {
		t.Fatal(err)
	}
	if status != domain.TaskRunning || got != snapshot {
		t.Fatalf("status=%s snapshot=%q", status, got)
	}
}

func TestTaskRepositoryAwaitInputPersistsConversationAtomically(t *testing.T) {
	_, repo, task := newTaskResultRepository(t)
	if err := repo.Start(context.Background(), task.ID, `{}`); err != nil {
		t.Fatal(err)
	}
	question := `[{"text":"pick","options":["a","b"]}]`
	write := TaskResultWrite{Status: domain.TaskAwaitingInput, Summary: "choose", AssistantContent: "choose", QuestionSchema: &question, EventKind: "result_awaiting_input", RawJSON: `{"status":"awaiting_input"}`}
	if err := repo.AwaitInput(context.Background(), task.ID, write); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.TaskAwaitingInput || got.ResultSummary == nil || *got.ResultSummary != "choose" {
		t.Fatalf("task=%+v", got)
	}
	messages, err := repo.Messages(context.Background(), task.ID)
	if err != nil || len(messages) != 1 || messages[0].Role != "assistant" || messages[0].QuestionSchema == nil || *messages[0].QuestionSchema != question {
		t.Fatalf("messages=%+v err=%v", messages, err)
	}
	events, err := repo.Events(context.Background(), task.ID)
	if err != nil || len(events) != 1 || events[0].Kind != "result_awaiting_input" {
		t.Fatalf("events=%+v err=%v", events, err)
	}
}

func TestTaskRepositoryAwaitInputRollsBackMessageAndEventOnStatusFailure(t *testing.T) {
	db, repo, task := newTaskResultRepository(t)
	if err := repo.Start(context.Background(), task.ID, `{}`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER reject_awaiting BEFORE UPDATE ON codex_tasks WHEN NEW.status='awaiting_input' BEGIN SELECT RAISE(ABORT,'reject awaiting'); END`); err != nil {
		t.Fatal(err)
	}
	question := `[{"text":"pick","options":[]}]`
	err := repo.AwaitInput(context.Background(), task.ID, TaskResultWrite{Status: domain.TaskAwaitingInput, Summary: "choose", AssistantContent: "choose", QuestionSchema: &question, EventKind: "result_awaiting_input", RawJSON: `{}`})
	if err == nil {
		t.Fatal("expected transaction failure")
	}
	assertTaskResultCounts(t, db, task.ID, domain.TaskRunning, 0, 0, 0, 0)
}

func TestTaskRepositoryCompleteSeparatesArtifactsAndFormalAssets(t *testing.T) {
	db, repo, task := newTaskResultRepository(t)
	if err := repo.Start(context.Background(), task.ID, `{}`); err != nil {
		t.Fatal(err)
	}
	artifact := TaskArtifact{Kind: "qc_report", Path: `C:\managed\qc.json`, Filename: "qc.json", MIMEType: "application/json", Size: 12, SHA256: "artifact-sha"}
	asset := AddAssetVersion{ProjectID: task.ProjectID, Type: domain.AssetContinuousScript, StorageKind: domain.StorageFile, Path: `C:\managed\script.md`, Filename: "script.md", MIMEType: "text/markdown", Size: 6, SHA256: "asset-sha", SourceTaskID: &task.ID}
	write := TaskResultWrite{Status: domain.TaskCompleted, Summary: "done", AssistantContent: "done", EventKind: "result_completed", RawJSON: `{"status":"completed"}`}
	if err := repo.CompleteWithResult(context.Background(), task.ID, write, []TaskArtifact{artifact}, []AddAssetVersion{asset}); err != nil {
		t.Fatal(err)
	}
	assertTaskResultCounts(t, db, task.ID, domain.TaskCompleted, 1, 1, 1, 1)
	var artifactPath, assetPath string
	if err := db.QueryRow(`SELECT path FROM task_artifacts WHERE task_id=?`, task.ID).Scan(&artifactPath); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT path FROM asset_versions WHERE source_task_id=?`, task.ID).Scan(&assetPath); err != nil {
		t.Fatal(err)
	}
	if artifactPath == assetPath {
		t.Fatalf("engineering artifact was registered as formal asset: %q", artifactPath)
	}
}

func TestTaskRepositoryCompleteRollsBackAllResultWritesWhenAssetFails(t *testing.T) {
	db, repo, task := newTaskResultRepository(t)
	if err := repo.Start(context.Background(), task.ID, `{}`); err != nil {
		t.Fatal(err)
	}
	artifact := TaskArtifact{Kind: "qc_report", Path: `C:\managed\qc.json`, Filename: "qc.json", MIMEType: "application/json", Size: 12, SHA256: "artifact-sha"}
	valid := AddAssetVersion{ProjectID: task.ProjectID, Type: domain.AssetContinuousScript, StorageKind: domain.StorageFile, Path: `C:\managed\script.md`, Filename: "script.md", MIMEType: "text/markdown", Size: 6, SHA256: "asset-sha", SourceTaskID: &task.ID}
	invalid := valid
	invalid.Type = ""
	err := repo.CompleteWithResult(context.Background(), task.ID, TaskResultWrite{Status: domain.TaskCompleted, Summary: "done", AssistantContent: "done", EventKind: "result_completed", RawJSON: `{}`}, []TaskArtifact{artifact}, []AddAssetVersion{valid, invalid})
	if err == nil {
		t.Fatal("expected formal asset failure")
	}
	assertTaskResultCounts(t, db, task.ID, domain.TaskRunning, 0, 0, 0, 0)
	var items int
	if err := db.QueryRow(`SELECT COUNT(*) FROM asset_items`).Scan(&items); err != nil || items != 0 {
		t.Fatalf("asset_items=%d err=%v", items, err)
	}
}

func assertTaskResultCounts(t *testing.T, db *sql.DB, taskID string, status domain.TaskStatus, messages, events, artifacts, assets int) {
	t.Helper()
	var gotStatus domain.TaskStatus
	if err := db.QueryRow(`SELECT status FROM codex_tasks WHERE id=?`, taskID).Scan(&gotStatus); err != nil {
		t.Fatal(err)
	}
	if gotStatus != status {
		t.Fatalf("status=%s want=%s", gotStatus, status)
	}
	for table, want := range map[string]int{"task_messages": messages, "task_events": events, "task_artifacts": artifacts, "asset_versions": assets} {
		var got int
		query := `SELECT COUNT(*) FROM ` + table
		if table != "asset_versions" {
			query += ` WHERE task_id=?`
		} else {
			query += ` WHERE source_task_id=?`
		}
		if err := db.QueryRow(query, taskID).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s=%d want=%d", table, got, want)
		}
	}
}
