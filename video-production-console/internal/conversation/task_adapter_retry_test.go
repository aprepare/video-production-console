package conversation

import (
	"context"
	"database/sql"
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

type failingRetryGate struct{ err error }

func (g *failingRetryGate) HandleCompleted(context.Context, taskcompletion.CompletedInput) (bool, error) {
	return false, g.err
}

func TestTaskAdapterRetryOutputAppServerRetainedOutputWithoutStartingAnotherTurn(t *testing.T) {
	testTaskAdapterRetry(t, codex.TransportAppServer)
}

func TestTaskAdapterRetryOutputLegacyExecRetainedOutputWithoutStartingAnotherProcess(t *testing.T) {
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
	timings := store.NewTaskTimingRepository(db)
	previousValidation, err := timings.StartPhase(ctx, store.StartPhase{TaskID: taskID, Attempt: 1, Key: "result_validation", DisplayName: "结果校验", Source: domain.PhaseSourceHost, ExternalID: "result-validation:1", At: now.Add(-time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := timings.FinishPhase(ctx, store.FinishPhase{ID: previousValidation.ID, State: domain.PhaseFailed, At: now}); err != nil {
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
	phases, err := timings.ForTask(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	validationRuns := make([]domain.TaskPhaseRun, 0, 2)
	for _, phase := range phases {
		if phase.PhaseKey == "result_validation" {
			validationRuns = append(validationRuns, phase)
		}
	}
	if len(validationRuns) != 2 || validationRuns[0].ID == validationRuns[1].ID || validationRuns[0].Attempt != 1 || validationRuns[1].Attempt != 2 || validationRuns[0].State != domain.PhaseFailed || validationRuns[1].State != domain.PhaseCompleted {
		t.Fatalf("validation runs=%+v", validationRuns)
	}
}

func TestTaskAdapterCompleteTurnPersistsTimingAndDuplicateIsIdempotent(t *testing.T) {
	ctx, db, repo, adapter, task, sessionID, turnID := appServerCompletionFixture(t, domain.ActionRemixStandard, nil, nil)
	raw := fmt.Sprintf(`{"schema_version":"2.0","task_id":%q,"action":"remix.standard","status":"completed","summary":"ok","questions":[],"artifacts":[],"asset_outputs":[],"warnings":[]}`, task.ID)
	for range 2 {
		if err := adapter.CompleteTurn(ctx, sessionID, turnID, raw); err != nil {
			t.Fatal(err)
		}
	}
	persisted, err := repo.Get(ctx, task.ID)
	if err != nil || persisted.Status != domain.TaskCompleted {
		t.Fatalf("task=%+v err=%v", persisted, err)
	}
	phases, err := store.NewTaskTimingRepository(db).ForTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]domain.TaskPhaseState{}
	counts := map[string]int{}
	for _, phase := range phases {
		states[phase.PhaseKey] = phase.State
		counts[phase.PhaseKey]++
	}
	for _, key := range []string{"codex_execution", "result_validation", "asset_commit"} {
		if counts[key] != 1 || states[key] != domain.PhaseCompleted {
			t.Fatalf("phase counts=%v states=%v phases=%+v", counts, states, phases)
		}
	}
}

func TestTaskAdapterCompleteTurnClaimDBFailureRollsBackTiming(t *testing.T) {
	ctx, db, repo, adapter, task, sessionID, turnID := appServerCompletionFixture(t, domain.ActionRemixStandard, nil, nil)
	if _, err := db.Exec(`CREATE TRIGGER reject_completion_claim BEFORE UPDATE ON codex_tasks WHEN NEW.status='resuming' BEGIN SELECT RAISE(ABORT,'reject completion claim'); END`); err != nil {
		t.Fatal(err)
	}
	raw := fmt.Sprintf(`{"schema_version":"2.0","task_id":%q,"action":"remix.standard","status":"completed","summary":"ok","questions":[],"artifacts":[],"asset_outputs":[],"warnings":[]}`, task.ID)
	if err := adapter.CompleteTurn(ctx, sessionID, turnID, raw); err == nil {
		t.Fatal("completion claim succeeded despite injected DB failure")
	}
	persisted, err := repo.Get(ctx, task.ID)
	if err != nil || persisted.Status != domain.TaskRunning {
		t.Fatalf("task=%+v err=%v", persisted, err)
	}
	phases, err := store.NewTaskTimingRepository(db).ForTask(ctx, task.ID)
	if err != nil || len(phases) != 1 || phases[0].PhaseKey != "codex_execution" || phases[0].State != domain.PhaseRunning || phases[0].FinishedAt != nil {
		t.Fatalf("rolled-back phases=%+v err=%v", phases, err)
	}
}

func TestTaskAdapterFailureAndCancelCloseActiveTiming(t *testing.T) {
	t.Run("failure", func(t *testing.T) {
		ctx, db, repo, adapter, task, sessionID, turnID := appServerCompletionFixture(t, domain.ActionRemixStandard, nil, nil)
		if err := adapter.FailTurn(ctx, sessionID, turnID, "transport_failed", errors.New("lost transport")); err != nil {
			t.Fatal(err)
		}
		assertTaskAndActiveTimingTerminal(t, ctx, db, repo, task.ID, domain.TaskFailed, domain.PhaseFailed)
	})
	t.Run("cancel", func(t *testing.T) {
		rpc := &fakeRPC{}
		ctx, db, repo, adapter, task, _, _ := appServerCompletionFixture(t, domain.ActionRemixStandard, nil, rpc)
		if err := adapter.Cancel(ctx, task); err != nil {
			t.Fatal(err)
		}
		assertTaskAndActiveTimingTerminal(t, ctx, db, repo, task.ID, domain.TaskCanceled, domain.PhaseCanceled)
		if rpc.callCount() != 1 || rpc.method(0) != "turn/interrupt" {
			t.Fatalf("RPC methods=%v", rpc.methods)
		}
	})
}

func TestTaskAdapterCompletionGateFailureDoesNotPersistFormalResult(t *testing.T) {
	gateErr := errors.New("registration rejected")
	ctx, db, repo, adapter, task, sessionID, turnID := appServerCompletionFixture(t, domain.ActionMontageExecute, &failingRetryGate{err: gateErr}, nil)
	output := filepath.Join(adapter.dataRoot, "projects", task.ID, "tasks", task.ID, "output")
	workspace := filepath.Join(output, "workspace", task.ID)
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
		"schema_version": "2.0", "task_id": task.ID, "action": "montage.execute", "status": "completed", "summary": "ok",
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
	err = adapter.CompleteTurn(ctx, sessionID, turnID, string(raw))
	if !errors.Is(err, gateErr) {
		t.Fatalf("error=%v", err)
	}
	persisted, err := repo.Get(ctx, task.ID)
	if err != nil || persisted.Status != domain.TaskFailed || persisted.ErrorCode == nil || *persisted.ErrorCode != "completion_gate_failed" {
		t.Fatalf("task=%+v err=%v", persisted, err)
	}
	var artifactCount, assetCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM task_artifacts WHERE task_id=?`, task.ID).Scan(&artifactCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM asset_versions WHERE source_task_id=?`, task.ID).Scan(&assetCount); err != nil {
		t.Fatal(err)
	}
	if artifactCount != 0 || assetCount != 0 {
		t.Fatalf("artifacts=%d assets=%d", artifactCount, assetCount)
	}
	phases, err := store.NewTaskTimingRepository(db).ForTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]domain.TaskPhaseState{}
	for _, phase := range phases {
		states[phase.PhaseKey] = phase.State
	}
	if states["codex_execution"] != domain.PhaseCompleted || states["result_validation"] != domain.PhaseCompleted || states["asset_commit"] != domain.PhaseFailed {
		t.Fatalf("phase states=%v phases=%+v", states, phases)
	}
}

func appServerCompletionFixture(t *testing.T, action domain.TaskAction, gate taskcompletion.Gate, rpc ThreadRPC) (context.Context, *sql.DB, *store.TaskRepository, *TaskAdapter, domain.CodexTask, string, string) {
	t.Helper()
	ctx, db, _, session := brokerFixtureWithDB(t)
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('completion-a','A','#fff','active',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	taskID, turnID := uuid.NewString(), "turn-"+uuid.NewString()
	task := domain.CodexTask{ID: taskID, AccountID: "completion-a", Type: "remix", SkillName: "finance-viral-remix", Action: action, Status: domain.TaskRunning, ChatSessionID: &session.ID, CodexThreadID: session.CodexThreadID, CodexTurnID: &turnID, Transport: codex.TransportAppServer, CompletionPhase: string(domain.CompletionAgentRunning), PromptSnapshot: "prompt", CreatedAt: now, StartedAt: &now}
	if action == domain.ActionMontageExecute {
		task.Type = "montage"
		task.SkillName = "jianying-montage-draft"
	}
	repo := store.NewTaskRepository(db)
	if err := repo.CreateV2(ctx, task); err != nil {
		t.Fatal(err)
	}
	if _, err := store.NewTaskTimingRepository(db).StartPhase(ctx, store.StartPhase{TaskID: task.ID, Attempt: 1, Key: "codex_execution", DisplayName: "Codex 执行", Source: domain.PhaseSourceAppServer, ExternalID: "app-server-execution", At: now}); err != nil {
		t.Fatal(err)
	}
	dataRoot := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(dataRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	adapter := NewTaskAdapter(repo, nil, rpc, TaskCompletionConfig{DataRoot: dataRoot, Gate: gate})
	return ctx, db, repo, adapter, task, session.ID, turnID
}

func assertTaskAndActiveTimingTerminal(t *testing.T, ctx context.Context, db *sql.DB, repo *store.TaskRepository, taskID string, taskState domain.TaskStatus, phaseState domain.TaskPhaseState) {
	t.Helper()
	persisted, err := repo.Get(ctx, taskID)
	if err != nil || persisted.Status != taskState {
		t.Fatalf("task=%+v err=%v", persisted, err)
	}
	phases, err := store.NewTaskTimingRepository(db).ForTask(ctx, taskID)
	if err != nil || len(phases) != 1 || phases[0].PhaseKey != "codex_execution" || phases[0].State != phaseState || phases[0].FinishedAt == nil {
		t.Fatalf("phases=%+v err=%v", phases, err)
	}
}
