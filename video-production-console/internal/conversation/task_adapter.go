package conversation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"video-production-console/internal/codex"
	"video-production-console/internal/domain"
	"video-production-console/internal/store"
	"video-production-console/internal/taskcompletion"
)

// TaskAdapter is the App Server implementation used by codex.CompositeScheduler.
// It performs all work through a persisted chat session and never owns the
// shared process lifecycle.
type TaskAdapter struct {
	tasks              *store.TaskRepository
	broker             *Broker
	rpc                ThreadRPC
	dataRoot           string
	completionGate     taskcompletion.Gate
	completionObserver taskcompletion.Observer
}

type TaskCompletionConfig struct {
	DataRoot string
	Gate     taskcompletion.Gate
	Observer taskcompletion.Observer
}

var ErrOutputRetryNotEligible = errors.New("task output is not eligible for completion retry")

func NewTaskAdapter(tasks *store.TaskRepository, broker *Broker, rpc ThreadRPC, completion ...TaskCompletionConfig) *TaskAdapter {
	adapter := &TaskAdapter{tasks: tasks, broker: broker, rpc: rpc}
	if len(completion) > 0 {
		adapter.dataRoot = strings.TrimSpace(completion[0].DataRoot)
		adapter.completionGate = completion[0].Gate
		adapter.completionObserver = completion[0].Observer
	}
	if broker != nil {
		broker.SetTurnCompletedHandler(adapter)
	}
	return adapter
}

// RetryOutput revalidates retained final JSON from an output_invalid App
// Server or legacy CLI task. It does not send a message or start a process.
func (a *TaskAdapter) RetryOutput(ctx context.Context, taskID string) error {
	if a == nil || a.tasks == nil || strings.TrimSpace(a.dataRoot) == "" {
		return errors.New("App Server output retry is not configured")
	}
	task, err := a.tasks.Get(ctx, strings.TrimSpace(taskID))
	if err != nil {
		return err
	}
	if task.Status != domain.TaskFailed || task.ErrorCode == nil || *task.ErrorCode != "output_invalid" || task.CompletionPhase != string(domain.CompletionAgentRunning) {
		return ErrOutputRetryNotEligible
	}
	projectRootID := task.ID
	if task.ProjectID != nil && strings.TrimSpace(*task.ProjectID) != "" {
		projectRootID = *task.ProjectID
	}
	assetRoot := filepath.Join(a.dataRoot, "projects", projectRootID)
	resultPath := filepath.Join(assetRoot, "tasks", task.ID, "output-last-message.json")
	info, err := os.Lstat(resultPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 2<<20 {
		return fmt.Errorf("retained output is unavailable or unsafe")
	}
	raw, err := os.ReadFile(resultPath)
	if err != nil {
		return fmt.Errorf("read retained output: %w", err)
	}
	turnID, err := a.tasks.ClaimOutputInvalidRetry(ctx, task.ID)
	if errors.Is(err, store.ErrOutputRetryNotEligible) {
		return ErrOutputRetryNotEligible
	}
	if err != nil {
		return err
	}
	runner := codex.NewRunner(nil, a.tasks, task.ID, assetRoot, nil)
	runner.OutputDir = filepath.Join(assetRoot, "tasks", task.ID, "output")
	runner.CompletionGate = a.completionGate
	runner.CompletionObserver = a.completionObserver
	runner.ExpectedTurnID = turnID
	return runner.CompleteAgentResult(ctx, string(raw))
}

func (a *TaskAdapter) Enqueue(ctx context.Context, task domain.CodexTask) error {
	if a == nil || a.tasks == nil || a.broker == nil {
		return errors.New("App Server task adapter is not configured")
	}
	if task.ChatSessionID == nil || strings.TrimSpace(*task.ChatSessionID) == "" {
		return errors.New("App Server task requires a chat session")
	}
	if task.CodexThreadID == nil || strings.TrimSpace(*task.CodexThreadID) == "" {
		return errors.New("App Server task requires a Codex thread")
	}
	if strings.TrimSpace(task.PromptSnapshot) == "" {
		return errors.New("App Server task prompt is required")
	}
	task.Transport = "app_server"
	if task.CompletionPhase == "" {
		task.CompletionPhase = "agent_running"
	}
	if _, err := a.tasks.Get(ctx, task.ID); err != nil {
		if err := a.tasks.CreateV2(ctx, task); err != nil {
			return err
		}
	}
	if err := a.tasks.UpdateStatus(ctx, task.ID, domain.TaskRunning, "", "", ""); err != nil {
		return err
	}
	receipt, err := a.broker.SendTask(ctx, SendInput{SessionID: *task.ChatSessionID, ClientKey: taskClientKeyPrefix + task.ID + ":initial", Text: task.PromptSnapshot})
	if err != nil {
		_ = a.tasks.UpdateStatus(ctx, task.ID, domain.TaskFailed, "", "app_server_enqueue_failed", err.Error())
		return errors.Join(err, a.afterTerminal(ctx, task.ID))
	}
	if receipt.TurnID == "" {
		return nil
	}
	threadID, turnID := strings.TrimSpace(receipt.ThreadID), receipt.TurnID
	if threadID == "" {
		threadID = *task.CodexThreadID
	}
	return a.bindReceipt(ctx, task.ID, task.ChatSessionID, &threadID, turnID)
}

func (a *TaskAdapter) Resume(ctx context.Context, taskID, answer string) error {
	if a == nil || a.tasks == nil || a.broker == nil {
		return errors.New("App Server task adapter is not configured")
	}
	task, err := a.tasks.Get(ctx, taskID)
	if err != nil {
		return err
	}
	if task.ChatSessionID == nil || *task.ChatSessionID == "" {
		return errors.New("App Server task has no chat session")
	}
	if task.Status != domain.TaskAwaitingInput && task.Status != domain.TaskWaitingInput {
		return errors.New("task is not waiting for input")
	}
	if task.CodexTurnID == nil || strings.TrimSpace(*task.CodexTurnID) == "" {
		return errors.New("awaiting App Server task has no completed turn")
	}
	previousTurn := *task.CodexTurnID
	resumeKey := taskClientKeyPrefix + task.ID + ":resume"
	resumeKey += ":" + previousTurn
	if err := a.tasks.BeginAppServerResume(ctx, task.ID, previousTurn, *task.ChatSessionID, resumeKey, answer); err != nil {
		return err
	}
	receipt, err := a.broker.SendTask(ctx, SendInput{SessionID: *task.ChatSessionID, ClientKey: resumeKey, Text: answer})
	if err != nil {
		persistErr := a.tasks.UpdateStatus(ctx, task.ID, domain.TaskFailed, "", "app_server_resume_failed", err.Error())
		return errors.Join(err, persistErr, a.afterTerminal(ctx, task.ID))
	}
	if receipt.TurnID == "" {
		return nil
	}
	if err := a.bindReceipt(ctx, task.ID, task.ChatSessionID, task.CodexThreadID, receipt.TurnID); err != nil {
		return a.failBindReceipt(ctx, task.ID, err)
	}
	return nil
}

func (a *TaskAdapter) failBindReceipt(ctx context.Context, taskID string, cause error) error {
	if err := a.tasks.UpdateStatus(ctx, taskID, domain.TaskFailed, "", "task_turn_bind_failed", cause.Error()); err != nil {
		return errors.Join(cause, err)
	}
	return errors.Join(cause, a.afterTerminal(ctx, taskID))
}

func (a *TaskAdapter) bindReceipt(ctx context.Context, taskID string, sessionID, threadID *string, turnID string) error {
	current, err := a.tasks.Get(ctx, taskID)
	if err != nil {
		return err
	}
	if current.CodexTurnID != nil {
		if *current.CodexTurnID == turnID {
			return nil
		}
		return errors.New("App Server task turn changed before the start receipt was bound")
	}
	return a.tasks.SetTransportMetadata(ctx, taskID, sessionID, threadID, stringPointer(turnID), string(domain.CompletionAgentRunning), codex.TransportAppServer)
}

// CompleteTurn is called only from the console-owned Broker after a durable
// App Server turn/completed notification. A transport completion alone never
// completes the formal task: the assistant item must pass the shared Runner
// result pipeline first.
func (a *TaskAdapter) CompleteTurn(ctx context.Context, sessionID, turnID, resultText string) error {
	if a == nil || a.tasks == nil {
		return errors.New("App Server task completion is not configured")
	}
	task, err := a.tasks.GetByCodexTurn(ctx, turnID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if task.ChatSessionID == nil || *task.ChatSessionID != sessionID || task.CodexTurnID == nil || *task.CodexTurnID != turnID {
		return errors.New("App Server task completion identity mismatch")
	}
	// Terminal results and awaiting-input results are durable idempotency
	// boundaries. Resume binds a new turn and returns the task to running.
	if task.Status == domain.TaskResuming {
		return errCompletionInProgress
	}
	if task.Status != domain.TaskRunning {
		return nil
	}
	if task.CompletionPhase != "" && task.CompletionPhase != string(domain.CompletionAgentRunning) {
		return nil
	}
	if a.dataRoot == "" {
		return errors.New("App Server task completion data root is not configured")
	}
	claimed, err := a.tasks.ClaimAppServerResult(ctx, task.ID, turnID)
	if err != nil {
		return err
	}
	if !claimed {
		return errCompletionInProgress
	}
	projectRootID := task.ID
	if task.ProjectID != nil && strings.TrimSpace(*task.ProjectID) != "" {
		projectRootID = *task.ProjectID
	}
	assetRoot := filepath.Join(a.dataRoot, "projects", projectRootID)
	runner := codex.NewRunner(nil, a.tasks, task.ID, assetRoot, nil)
	runner.OutputDir = filepath.Join(assetRoot, "tasks", task.ID, "output")
	runner.CompletionGate = a.completionGate
	runner.CompletionObserver = a.completionObserver
	runner.ExpectedTurnID = &turnID
	return runner.CompleteAgentResult(ctx, resultText)
}

func (a *TaskAdapter) FailTurn(ctx context.Context, sessionID, turnID, code string, cause error) error {
	if a == nil || a.tasks == nil {
		return errors.New("App Server task completion is not configured")
	}
	task, err := a.tasks.GetByCodexTurn(ctx, turnID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if task.ChatSessionID == nil || *task.ChatSessionID != sessionID {
		return errors.New("App Server task failure identity mismatch")
	}
	if task.Status == domain.TaskCompleted || task.Status == domain.TaskFailed || task.Status == domain.TaskCanceled || task.Status == domain.TaskInterrupted {
		return nil
	}
	message := code
	if cause != nil {
		message = cause.Error()
	}
	failed, err := a.tasks.FailAppServerTurn(ctx, task.ID, turnID, code, message)
	if err != nil || !failed {
		return err
	}
	return a.afterTerminal(ctx, task.ID)
}

func (a *TaskAdapter) TaskTurnStarted(ctx context.Context, clientKey, sessionID, threadID, turnID string) error {
	taskID, err := formalTaskID(clientKey)
	if err != nil {
		return err
	}
	task, err := a.tasks.Get(ctx, taskID)
	if err != nil {
		return err
	}
	fail := func(cause error) error {
		if persistErr := a.tasks.UpdateStatus(ctx, task.ID, domain.TaskFailed, "", "task_turn_bind_failed", cause.Error()); persistErr != nil {
			return errors.Join(cause, persistErr)
		}
		return errors.Join(cause, a.afterTerminal(ctx, task.ID))
	}
	if task.ChatSessionID == nil || *task.ChatSessionID != sessionID {
		return fail(errors.New("formal task start identity mismatch"))
	}
	if task.Status != domain.TaskRunning {
		return fail(fmt.Errorf("formal task cannot bind a turn in status %s", task.Status))
	}
	if err := a.bindReceipt(ctx, task.ID, task.ChatSessionID, &threadID, turnID); err != nil {
		return fail(err)
	}
	return nil
}

func (a *TaskAdapter) TaskDeliveryFailed(ctx context.Context, clientKey, sessionID string, cause error) error {
	taskID, err := formalTaskID(clientKey)
	if err != nil {
		return err
	}
	task, err := a.tasks.Get(ctx, taskID)
	if err != nil {
		return err
	}
	if task.ChatSessionID == nil || *task.ChatSessionID != sessionID {
		return errors.New("formal task queued delivery identity mismatch")
	}
	if task.Status == domain.TaskCompleted || task.Status == domain.TaskFailed || task.Status == domain.TaskCanceled || task.Status == domain.TaskInterrupted {
		return nil
	}
	message := "queued formal task delivery failed"
	if cause != nil {
		message = cause.Error()
	}
	if err := a.tasks.UpdateStatus(ctx, task.ID, domain.TaskFailed, "", "task_delivery_failed", message); err != nil {
		return err
	}
	return a.afterTerminal(ctx, task.ID)
}

func formalTaskID(clientKey string) (string, error) {
	if !strings.HasPrefix(clientKey, taskClientKeyPrefix) {
		return "", errors.New("formal task client key is invalid")
	}
	remainder := strings.TrimPrefix(clientKey, taskClientKeyPrefix)
	taskID, _, ok := strings.Cut(remainder, ":")
	if !ok || strings.TrimSpace(taskID) == "" {
		return "", errors.New("formal task client key is invalid")
	}
	return taskID, nil
}

func (a *TaskAdapter) TaskExecutionConfig(ctx context.Context, clientKey string) (string, string, error) {
	if a == nil || a.tasks == nil {
		return "", "", errors.New("formal task configuration is unavailable")
	}
	taskID, err := formalTaskID(clientKey)
	if err != nil {
		return "", "", err
	}
	task, err := a.tasks.Get(ctx, taskID)
	if err != nil {
		return "", "", err
	}
	return strings.TrimSpace(task.ModelName), strings.TrimSpace(task.ReasoningEffort), nil
}

func (a *TaskAdapter) Cancel(ctx context.Context, task domain.CodexTask) error {
	if a == nil || a.tasks == nil || a.rpc == nil {
		return errors.New("App Server task adapter is not configured")
	}
	if task.CodexThreadID == nil || strings.TrimSpace(*task.CodexThreadID) == "" || task.CodexTurnID == nil || strings.TrimSpace(*task.CodexTurnID) == "" {
		return errors.New("App Server task has no active Codex turn")
	}
	canceled, err := a.tasks.CancelAppServerTurn(ctx, task.ID, *task.CodexTurnID)
	if err != nil {
		return err
	}
	if !canceled {
		return errors.New("App Server task turn is no longer cancelable")
	}
	observerErr := a.afterTerminal(ctx, task.ID)
	if err := a.rpc.Call(ctx, "turn/interrupt", map[string]any{"threadId": *task.CodexThreadID, "turnId": *task.CodexTurnID}, &struct{}{}); err != nil {
		_ = a.tasks.AppendEvent(ctx, task.ID, domain.TaskEvent{Kind: "cancel_interrupt_failed", Level: "error", DisplayText: err.Error()})
		return errors.Join(fmt.Errorf("interrupt Codex turn after durable cancellation: %w", err), observerErr)
	}
	if err := a.tasks.AppendEvent(ctx, task.ID, domain.TaskEvent{Kind: "cancel_requested", Level: "warning", DisplayText: "Task cancellation requested"}); err != nil {
		return err
	}
	return observerErr
}

func (a *TaskAdapter) afterTerminal(ctx context.Context, taskID string) error {
	if a.completionObserver == nil {
		return nil
	}
	task, err := a.tasks.Get(ctx, taskID)
	if err != nil {
		return err
	}
	if err := a.completionObserver.AfterTerminal(ctx, task); err != nil {
		warningErr := a.tasks.AppendEvent(ctx, task.ID, domain.TaskEvent{Kind: taskcompletion.ObserverWarningEvent, Level: "warning", DisplayText: err.Error()})
		return errors.Join(err, warningErr)
	}
	return nil
}

func stringPointer(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}
