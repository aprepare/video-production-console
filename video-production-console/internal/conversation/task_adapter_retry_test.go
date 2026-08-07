package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/codex"
	"video-production-console/internal/domain"
	"video-production-console/internal/store"
	"video-production-console/internal/taskcompletion"
)

type recordingRetryGate struct {
	called int
	input  taskcompletion.CompletedInput
}

type recordingCompletionObserver struct {
	called int
	task   domain.CodexTask
	repo   *store.TaskRepository
	err    error
}

func (o *recordingCompletionObserver) AfterTerminal(ctx context.Context, task domain.CodexTask) error {
	persisted, err := o.repo.Get(ctx, task.ID)
	if err != nil {
		return err
	}
	if persisted.Status != task.Status {
		return fmt.Errorf("observer ran before durable status")
	}
	o.called++
	o.task = task
	return o.err
}

func TestTaskAdapterCompletionObserverRunsAfterDurableTurnFailure(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "observer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	_, _ = db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('a','A','#fff','active',?,?)`, now, now)
	repo := store.NewTaskRepository(db)
	taskID, threadID, turnID, sessionID := uuid.NewString(), "thread", "turn", "session"
	_, _ = db.Exec(`INSERT INTO chat_sessions(id,title,source,kind,status,codex_thread_id,working_directory,model,reasoning_effort,skill_names_json,created_at,updated_at) VALUES(?,?,'console','general','running',?,?,'gpt-5.4','high','[]',?,?)`, sessionID, "session", threadID, t.TempDir(), now, now)
	task := domain.CodexTask{ID: taskID, AccountID: "a", Type: "remix", SkillName: "finance-viral-remix", Action: domain.ActionRemixStandard, Status: domain.TaskQueued, PromptSnapshot: "prompt", CreatedAt: now}
	if err := repo.CreateV2(ctx, task); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetTransportMetadata(ctx, taskID, &sessionID, &threadID, &turnID, string(domain.CompletionAgentRunning), codex.TransportAppServer); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(ctx, taskID, domain.TaskRunning, "", "", ""); err != nil {
		t.Fatal(err)
	}
	observer := &recordingCompletionObserver{repo: repo}
	adapter := NewTaskAdapter(repo, nil, nil, TaskCompletionConfig{Observer: observer})
	if err := adapter.FailTurn(ctx, sessionID, turnID, "transport_failed", fmt.Errorf("lost")); err != nil {
		t.Fatal(err)
	}
	if observer.called != 1 || observer.task.Status != domain.TaskFailed {
		t.Fatalf("observer=%+v", observer)
	}
}

func TestTaskAdapterResumeBindReceiptFailureObservesDurableFailureAndPreservesCause(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "bind.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	_, _ = db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('a','A','#fff','active',?,?)`, now, now)
	repo := store.NewTaskRepository(db)
	taskID := uuid.NewString()
	task := domain.CodexTask{ID: taskID, AccountID: "a", Type: "remix", SkillName: "finance-viral-remix", Action: domain.ActionRemixStandard, Status: domain.TaskRunning, PromptSnapshot: "p", CreatedAt: now}
	if err := repo.CreateV2(ctx, task); err != nil {
		t.Fatal(err)
	}
	observerErr := errors.New("observer failed")
	observer := &recordingCompletionObserver{repo: repo, err: observerErr}
	adapter := NewTaskAdapter(repo, nil, nil, TaskCompletionConfig{Observer: observer})
	cause := errors.New("turn changed")
	err = adapter.failBindReceipt(ctx, taskID, cause)
	if !errors.Is(err, cause) {
		t.Fatalf("err=%v", err)
	}
	if !errors.Is(err, observerErr) {
		t.Fatalf("observer error missing: %v", err)
	}
	got, _ := repo.Get(ctx, taskID)
	if got.Status != domain.TaskFailed || observer.called != 1 || observer.task.Status != domain.TaskFailed {
		t.Fatalf("task=%+v observer=%+v", got, observer)
	}
}

func (g *recordingRetryGate) HandleCompleted(_ context.Context, input taskcompletion.CompletedInput) (bool, error) {
	g.called++
	g.input = input
	return true, nil
}

func TestTaskAdapterRetriesAppServerRetainedOutputWithoutStartingAnotherTurn(t *testing.T) {
	testTaskAdapterRetry(t, codex.TransportAppServer)
}

func TestTaskAdapterRetriesLegacyExecRetainedOutputWithoutStartingAnotherProcess(t *testing.T) {
	testTaskAdapterRetry(t, codex.TransportLegacyExec)
}

func testTaskAdapterRetry(t *testing.T, transport string) {
	t.Helper()
	ctx := context.Background()
	dataRoot := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(dataRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(dataRoot, "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('a','A','#fff','active',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	taskID := uuid.NewString()
	repo := store.NewTaskRepository(db)
	task := domain.CodexTask{ID: taskID, AccountID: "a", Type: "montage", SkillName: "jianying-montage-draft", Action: domain.ActionMontageExecute, Status: domain.TaskQueued, PromptSnapshot: "prompt", CreatedAt: now}
	if err := repo.CreateV2(ctx, task); err != nil {
		t.Fatal(err)
	}
	if transport == codex.TransportAppServer {
		threadID, turnID := "thread-1", "turn-1"
		if err := repo.SetTransportMetadata(ctx, taskID, nil, &threadID, &turnID, string(domain.CompletionAgentRunning), transport); err != nil {
			t.Fatal(err)
		}
	} else {
		if _, err := db.Exec(`UPDATE codex_tasks SET transport=?,completion_phase=? WHERE id=?`, transport, domain.CompletionAgentRunning, taskID); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.UpdateStatus(ctx, taskID, domain.TaskFailed, "", "output_invalid", `artifact fields: unknown field "metadata"`); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dataRoot, "projects", taskID, "tasks", taskID, "output")
	workspace := filepath.Join(output, "workspace", taskID)
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "draft_content.json"), []byte(`{"ok":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(output, "production_plan.json")
	if err := os.WriteFile(planPath, []byte(`{"plan":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	resultPlanPath := planPath
	if filepath.Separator == '\\' {
		resultPlanPath = `\\?\` + planPath
	}
	payload := map[string]any{
		"schema_version": "2.0", "task_id": taskID, "action": "montage.execute", "status": "completed", "summary": "ok",
		"questions": []any{}, "asset_outputs": []any{}, "warnings": []any{},
		"artifacts": []any{
			map[string]any{"type": "production_plan", "kind": "file", "path": resultPlanPath},
			map[string]any{"type": "plaintext_workspace", "kind": "directory", "path": workspace, "metadata": map[string]any{"narration_present": true, "bgm_present": true, "sfx_present": true, "transitions_present": true}},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(output), "output-last-message.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	gate := &recordingRetryGate{}
	adapter := NewTaskAdapter(repo, nil, nil, TaskCompletionConfig{DataRoot: dataRoot, Gate: gate})
	if err := adapter.RetryOutput(ctx, taskID); err != nil {
		t.Fatal(err)
	}
	if gate.called != 1 || gate.input.Task.ID != taskID || len(gate.input.Artifacts) != 2 || gate.input.Artifacts[0].Kind != "production_plan" || gate.input.Artifacts[1].Kind != "plaintext_workspace" {
		t.Fatalf("gate call=%d input=%+v", gate.called, gate.input)
	}
	got, err := repo.Get(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	wantStatus := domain.TaskRunning
	if transport == codex.TransportAppServer {
		wantStatus = domain.TaskResuming
	}
	if got.Status != wantStatus || got.ErrorCode != nil || got.ErrorMessage != nil {
		t.Fatalf("retry claim state=%+v", got)
	}
}
