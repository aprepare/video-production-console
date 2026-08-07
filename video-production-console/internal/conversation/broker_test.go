package conversation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/codexapp"
	"video-production-console/internal/domain"
	"video-production-console/internal/store"
)

func TestAutoDeliverySteersActiveTurn(t *testing.T) {
	ctx, repo, session := brokerFixture(t)
	turnID := "turn-7"
	createActiveTurn(t, ctx, repo, session.ID, turnID)
	rpc := &fakeRPC{turnID: turnID}
	broker := NewBroker(repo, rpc)

	receipt, err := broker.Send(ctx, SendInput{SessionID: session.ID, ClientKey: "m1", Text: "continue", Delivery: domain.DeliveryAuto})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Method != "turn/steer" || receipt.TurnID != turnID {
		t.Fatalf("receipt=%+v", receipt)
	}
	if got := rpc.param("expectedTurnId"); got != turnID {
		t.Fatalf("expectedTurnId=%v", got)
	}
}

func TestAutoDeliveryStartsIdleTurn(t *testing.T) {
	ctx, repo, session := brokerFixture(t)
	rpc := &fakeRPC{turnID: "turn-new"}
	broker := NewBroker(repo, rpc)

	receipt, err := broker.Send(ctx, SendInput{SessionID: session.ID, ClientKey: "m1", Text: "start", Delivery: domain.DeliveryAuto})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Method != "turn/start" || receipt.TurnID != "turn-new" {
		t.Fatalf("receipt=%+v", receipt)
	}
}

func TestAutoDeliveryOverridesThreadWithWritableNoApprovalPolicy(t *testing.T) {
	ctx, repo, session := brokerFixture(t)
	rpc := &fakeRPC{turnID: "turn-writable"}
	broker := NewBroker(repo, rpc)

	if _, err := broker.Send(ctx, SendInput{SessionID: session.ID, ClientKey: "writable", Text: "start", Delivery: domain.DeliveryAuto}); err != nil {
		t.Fatal(err)
	}
	params, ok := rpc.params[0]["sandboxPolicy"].(map[string]any)
	if !ok || params["type"] != "workspaceWrite" {
		t.Fatalf("sandbox policy=%#v, want workspaceWrite", rpc.params[0]["sandboxPolicy"])
	}
	if got := rpc.params[0]["approvalPolicy"]; got != "never" {
		t.Fatalf("approval policy=%v, want never", got)
	}
}

type missingThreadStartRPC struct {
	fakeRPC
	turnStarts int
}

func (r *missingThreadStartRPC) Call(_ context.Context, method string, params, result any) error {
	r.mu.Lock()
	r.methods = append(r.methods, method)
	copyParams, _ := params.(map[string]any)
	r.params = append(r.params, copyParams)
	r.mu.Unlock()
	switch method {
	case "turn/start":
		r.turnStarts++
		if r.turnStarts == 1 {
			return &codexapp.RPCError{Code: -32600, Message: "thread not found: stale-thread"}
		}
		data, _ := json.Marshal(map[string]string{"turnId": "turn-retried"})
		return json.Unmarshal(data, result)
	case "thread/start":
		data, _ := json.Marshal(map[string]string{"threadId": "replacement-thread"})
		return json.Unmarshal(data, result)
	default:
		return nil
	}
}

func TestAutoDeliveryReplacesMissingThreadAndRetriesOnce(t *testing.T) {
	ctx, repo, session := brokerFixture(t)
	rpc := &missingThreadStartRPC{}
	broker := NewBroker(repo, rpc)

	receipt, err := broker.Send(ctx, SendInput{SessionID: session.ID, ClientKey: "replace-missing-thread", Text: "start", Delivery: domain.DeliveryAuto})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.ThreadID != "replacement-thread" || receipt.TurnID != "turn-retried" {
		t.Fatalf("receipt=%+v", receipt)
	}
	updated, err := repo.GetSession(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.CodexThreadID == nil || *updated.CodexThreadID != "replacement-thread" {
		t.Fatalf("session thread=%v, want replacement-thread", updated.CodexThreadID)
	}
	if rpc.callCount() != 3 || rpc.method(0) != "turn/start" || rpc.method(1) != "thread/start" || rpc.method(2) != "turn/start" {
		t.Fatalf("calls=%v", rpc.methods)
	}
}

func TestTaskTurnStartedRebindsTaskWhenSessionThreadWasReplaced(t *testing.T) {
	ctx, db, conversations, session := brokerFixtureWithDB(t)
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('a','A','#fff','active',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	tasks := store.NewTaskRepository(db)
	oldThread := *session.CodexThreadID
	task := domain.CodexTask{ID: "task-rebind", AccountID: "a", Type: "remix", SkillName: "finance-viral-remix", Action: domain.ActionRemixStandard, Status: domain.TaskRunning, ChatSessionID: &session.ID, CodexThreadID: &oldThread, Transport: "app_server", CompletionPhase: string(domain.CompletionAgentRunning), PromptSnapshot: "prompt", CreatedAt: now}
	if err := tasks.CreateV2(ctx, task); err != nil {
		t.Fatal(err)
	}
	if err := conversations.SetSessionThread(ctx, session.ID, "replacement-thread"); err != nil {
		t.Fatal(err)
	}
	adapter := &TaskAdapter{tasks: tasks}
	if err := adapter.TaskTurnStarted(ctx, taskClientKeyPrefix+task.ID+":initial", session.ID, "replacement-thread", "turn-rebound"); err != nil {
		t.Fatal(err)
	}
	updated, err := tasks.Get(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.CodexThreadID == nil || *updated.CodexThreadID != "replacement-thread" || updated.CodexTurnID == nil || *updated.CodexTurnID != "turn-rebound" {
		t.Fatalf("task binding=%+v", updated)
	}
}

func TestTaskDeliveryQueuesBehindActiveTurnAndStartsExclusively(t *testing.T) {
	ctx, db, repo, session := brokerFixtureWithDB(t)
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('a','A','#fff','active',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	tasks := store.NewTaskRepository(db)
	task := domain.CodexTask{ID: "task-1", AccountID: "a", Type: "remix", SkillName: "finance-viral-remix", Action: domain.ActionRemixStandard, Status: domain.TaskRunning, ChatSessionID: &session.ID, CodexThreadID: session.CodexThreadID, Transport: "app_server", CompletionPhase: string(domain.CompletionAgentRunning), PromptSnapshot: "prompt", CreatedAt: now}
	if err := tasks.CreateV2(ctx, task); err != nil {
		t.Fatal(err)
	}
	createActiveTurn(t, ctx, repo, session.ID, "turn-chat")
	rpc := &fakeRPC{turnID: "turn-task"}
	broker := NewBroker(repo, rpc)
	broker.rememberTurn("turn-chat", session.ID)
	receipt, err := broker.SendTask(ctx, SendInput{SessionID: session.ID, ClientKey: taskClientKeyPrefix + "task-1:initial", Text: "formal task"})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.State != "queued" || rpc.callCount() != 0 {
		t.Fatalf("receipt=%+v calls=%d", receipt, rpc.callCount())
	}
	rpc.notifications <- codexapp.Notification{Method: "turn/completed", Params: []byte(`{"turn":{"id":"turn-chat"}}`)}
	deadline := time.After(time.Second)
	for rpc.callCount() != 1 {
		select {
		case <-deadline:
			t.Fatalf("RPC calls=%d, want exclusive turn/start", rpc.callCount())
		case <-time.After(time.Millisecond):
		}
	}
	if rpc.method(0) != "turn/start" {
		t.Fatalf("method=%q", rpc.method(0))
	}
}

func TestCompletionFailureKeepsTurnMappedForReplay(t *testing.T) {
	ctx, repo, session := brokerFixture(t)
	createActiveTurn(t, ctx, repo, session.ID, "turn-fail")
	rpc := &fakeRPC{}
	handler := &failingCompletionHandler{called: make(chan struct{}, 1)}
	broker := NewBroker(repo, rpc)
	broker.SetTurnCompletedHandler(handler)
	broker.rememberTurn("turn-fail", session.ID)
	rpc.notifications <- codexapp.Notification{Method: "turn/completed", Params: []byte(`{"turn":{"id":"turn-fail"}}`)}
	select {
	case <-handler.called:
	case <-time.After(time.Second):
		t.Fatal("completion failure was ignored")
	}
	if got := broker.sessionForTurn("turn-fail"); got != session.ID {
		t.Fatalf("turn mapping released after persistence failure: %q", got)
	}
	deadline := time.After(time.Second)
	for {
		item, err := repo.CompletionForTurn(ctx, "turn-fail")
		if err == nil && item.Status == domain.ChatCompletionPending && item.LastError != nil {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("completion was not retained for retry: %+v err=%v", item, err)
		case <-time.After(time.Millisecond):
		}
	}
}

func TestTurnCompletionFailureReadsNestedTurnError(t *testing.T) {
	err := turnCompletionFailure([]byte(`{"turn":{"id":"turn-502","status":"failed","error":{"message":"unexpected status 502 Bad Gateway"}}}`))
	if err == nil || err.Error() != "unexpected status 502 Bad Gateway" {
		t.Fatalf("failure=%v, want nested App Server turn error", err)
	}
}

func TestQueuedFormalTaskStartFailureIsPersisted(t *testing.T) {
	ctx, repo, session := brokerFixture(t)
	createActiveTurn(t, ctx, repo, session.ID, "turn-before-failure")
	rpc := &fakeRPC{callErr: errors.New("turn start unavailable")}
	handler := &recordingCompletionHandler{deliveryFailed: make(chan struct{}, 1)}
	broker := NewBroker(repo, rpc)
	broker.SetTurnCompletedHandler(handler)
	broker.rememberTurn("turn-before-failure", session.ID)
	if _, err := broker.SendTask(ctx, SendInput{SessionID: session.ID, ClientKey: taskClientKeyPrefix + "task-failed:initial", Text: "formal task"}); err != nil {
		t.Fatal(err)
	}
	rpc.notifications <- codexapp.Notification{Method: "turn/completed", Params: []byte(`{"turn":{"id":"turn-before-failure"}}`)}
	select {
	case <-handler.deliveryFailed:
	case <-time.After(time.Second):
		t.Fatal("queued formal task delivery failure was not persisted")
	}
	pending, err := repo.PendingOutbox(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("failed queued outbox remained pending: %+v", pending)
	}
}

func TestSendTaskLeaseConflictTerminatesDurableQueueAndTask(t *testing.T) {
	ctx, repo, session := brokerFixture(t)
	if ok, err := repo.AcquireThreadLease(ctx, domain.ThreadLease{ThreadID: *session.CodexThreadID, SessionID: session.ID, OwnerID: "other-console", ExpiresAt: time.Now().UTC().Add(time.Minute)}); err != nil || !ok {
		t.Fatalf("acquire competing lease ok=%v err=%v", ok, err)
	}
	handler := &recordingCompletionHandler{deliveryFailed: make(chan struct{}, 1)}
	broker := NewBroker(repo, &fakeRPC{turnID: "must-not-start"})
	broker.SetTurnCompletedHandler(handler)
	_, err := broker.SendTask(ctx, SendInput{SessionID: session.ID, ClientKey: taskClientKeyPrefix + "task-lease:initial", Text: "formal task"})
	if err == nil {
		t.Fatal("expected lease conflict")
	}
	select {
	case <-handler.deliveryFailed:
	case <-time.After(time.Second):
		t.Fatal("formal task failure callback was not invoked")
	}
	pending, err := repo.PendingOutbox(ctx, session.ID)
	if err != nil || len(pending) != 0 {
		t.Fatalf("live queued outbox remained after synchronous failure: %+v err=%v", pending, err)
	}
	failed, err := repo.FailedOutbox(ctx, session.ID)
	if err != nil || len(failed) != 1 {
		t.Fatalf("failed outbox=%+v err=%v", failed, err)
	}
}

func TestExplicitSteerConflictsWithoutActiveTurn(t *testing.T) {
	ctx, repo, session := brokerFixture(t)
	rpc := &fakeRPC{turnID: "unexpected"}
	broker := NewBroker(repo, rpc)

	if _, err := broker.Send(ctx, SendInput{SessionID: session.ID, ClientKey: "m1", Text: "steer", Delivery: domain.DeliverySteer}); err == nil {
		t.Fatal("expected steer conflict")
	}
	if got := rpc.callCount(); got != 0 {
		t.Fatalf("RPC calls=%d, want 0", got)
	}
}

func TestQueuedDeliveryStartsExactlyOnceAfterCompletion(t *testing.T) {
	ctx, repo, session := brokerFixture(t)
	createActiveTurn(t, ctx, repo, session.ID, "turn-previous")
	rpc := &fakeRPC{turnID: "turn-next"}
	broker := NewBroker(repo, rpc)
	broker.rememberTurn("turn-previous", session.ID)

	receipt, err := broker.Send(ctx, SendInput{SessionID: session.ID, ClientKey: "queued", Text: "next", Delivery: domain.DeliveryQueue})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.State != "queued" {
		t.Fatalf("receipt=%+v", receipt)
	}
	rpc.notifications <- codexapp.Notification{Method: "turn/completed", Params: []byte(`{"turn":{"id":"turn-previous"}}`)}
	rpc.notifications <- codexapp.Notification{Method: "turn/completed", Params: []byte(`{"turn":{"id":"turn-previous"}}`)}

	deadline := time.After(time.Second)
	for rpc.callCount() != 1 {
		select {
		case <-deadline:
			t.Fatalf("RPC calls=%d, want one queued start", rpc.callCount())
		case <-time.After(time.Millisecond):
		}
	}
	if got := rpc.method(0); got != "turn/start" {
		t.Fatalf("method=%q, want turn/start", got)
	}
}

func TestCompletedNotificationFindsPersistedTurnAfterBrokerRestart(t *testing.T) {
	ctx, repo, session := brokerFixture(t)
	createActiveTurn(t, ctx, repo, session.ID, "turn-before-restart")
	rpc := &fakeRPC{turnID: "turn-after-restart"}
	if _, err := repo.Enqueue(ctx, session.ID, "queued-recovery", "continue", domain.DeliveryQueue); err != nil {
		t.Fatal(err)
	}

	// A fresh Broker has no in-memory map. Completion must still resolve the
	// durable turn record and release the queued message.
	second := NewBroker(repo, rpc)
	rpc.notifications <- codexapp.Notification{Method: "turn/completed", Params: []byte(`{"turn":{"id":"turn-before-restart"}}`)}
	deadline := time.After(time.Second)
	for rpc.callCount() != 1 {
		select {
		case <-deadline:
			t.Fatalf("RPC calls=%d, want recovered queued start", rpc.callCount())
		case <-time.After(time.Millisecond):
		}
	}
	_ = second // keeps the notification consumer alive for the assertion above.
}

func TestRecoverFailsUncertainSendingFormalTaskWithoutDuplicateRPC(t *testing.T) {
	ctx, repo, session := brokerFixture(t)
	clientKey := taskClientKeyPrefix + "task-uncertain:initial"
	outbox, err := repo.Enqueue(ctx, session.ID, clientKey, "formal task", domain.DeliveryQueue)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateOutboxStatus(ctx, outbox.ID, domain.ChatOutboxQueued, domain.ChatOutboxSending, nil, nil); err != nil {
		t.Fatal(err)
	}
	rpc := &fakeRPC{turnID: "must-not-start"}
	handler := &recordingCompletionHandler{deliveryFailed: make(chan struct{}, 1)}
	broker := NewBroker(repo, rpc)
	broker.SetTurnCompletedHandler(handler)
	if err := broker.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-handler.deliveryFailed:
	case <-time.After(time.Second):
		t.Fatal("uncertain formal task was not failed")
	}
	if got := rpc.callCount(); got != 0 {
		t.Fatalf("RPC calls=%d, want zero for uncertain sending delivery", got)
	}
	failed, err := repo.FailedOutbox(ctx, session.ID)
	if err != nil || len(failed) != 1 {
		t.Fatalf("failed=%+v err=%v", failed, err)
	}
}

func TestRecoverStartsQueuedIdleDeliveryExactlyOnce(t *testing.T) {
	ctx, repo, session := brokerFixture(t)
	if _, err := repo.Enqueue(ctx, session.ID, "recover-queued", "continue", domain.DeliveryQueue); err != nil {
		t.Fatal(err)
	}
	rpc := &fakeRPC{turnID: "turn-recovered"}
	broker := NewBroker(repo, rpc)
	if err := broker.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if err := broker.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if got := rpc.callCount(); got != 1 {
		t.Fatalf("RPC calls=%d, want one recovered start", got)
	}
}

func TestRecoverInterruptsUnreconciledDurableTurnAndReleasesQueue(t *testing.T) {
	ctx, repo, session := brokerFixture(t)
	createActiveTurn(t, ctx, repo, session.ID, "turn-active-on-restart")
	if _, err := repo.Enqueue(ctx, session.ID, "recover-waits", "later", domain.DeliveryQueue); err != nil {
		t.Fatal(err)
	}
	rpc := &fakeRPC{turnID: "turn-after-recovery"}
	broker := NewBroker(repo, rpc)
	if err := broker.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if got := rpc.callCount(); got != 1 {
		t.Fatalf("RPC calls=%d, want one queued start after stale turn interruption", got)
	}
	pending, err := repo.PendingOutbox(ctx, session.ID)
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
	active, err := repo.ActiveTurn(ctx, session.ID)
	if err != nil || active.CodexTurnID == nil || *active.CodexTurnID != "turn-after-recovery" {
		t.Fatalf("active=%+v err=%v", active, err)
	}
}

func TestRecoverDoesNotSendQueuedFormalOutboxForTerminalTask(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/terminal-formal.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('a','A','#fff','active',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	repo := store.NewConversationRepository(db)
	sessionID, threadID, taskID := uuid.NewString(), "thread-terminal", uuid.NewString()
	session := domain.ChatSession{ID: sessionID, Title: "formal", Kind: domain.ChatGeneral, Status: domain.ChatIdle, CodexThreadID: &threadID}
	if err := repo.CreateSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	tasks := store.NewTaskRepository(db)
	task := domain.CodexTask{ID: taskID, AccountID: "a", Type: "remix", SkillName: "finance-viral-remix", Action: domain.ActionRemixStandard, Status: domain.TaskInterrupted, ChatSessionID: &sessionID, CodexThreadID: &threadID, Transport: "app_server", CompletionPhase: string(domain.CompletionAgentRunning), PromptSnapshot: "prompt", CreatedAt: now}
	if err := tasks.CreateV2(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Enqueue(context.Background(), sessionID, taskClientKeyPrefix+taskID+":initial", "prompt", domain.DeliveryQueue); err != nil {
		t.Fatal(err)
	}
	rpc := &fakeRPC{turnID: "must-not-start"}
	broker := NewBroker(repo, rpc)
	if err := broker.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := rpc.callCount(); got != 0 {
		t.Fatalf("RPC calls=%d, want zero for terminal formal task", got)
	}
	failed, err := repo.FailedOutbox(context.Background(), sessionID)
	if err != nil || len(failed) != 1 {
		t.Fatalf("failed=%+v err=%v", failed, err)
	}
}

func TestSuccessfulNotificationsProjectSessionSemanticEvents(t *testing.T) {
	ctx, repo, session := brokerFixture(t)
	createActiveTurn(t, ctx, repo, session.ID, "turn-semantic")
	rpc := &fakeRPC{}
	broker := NewBroker(repo, rpc)
	broker.rememberTurn("turn-semantic", session.ID)
	for _, notification := range []codexapp.Notification{
		{Method: "turn/started", Params: []byte(`{"turn":{"id":"turn-semantic"}}`)},
		{Method: "item/updated", Params: []byte(`{"turnId":"turn-semantic","item":{"id":"tool-1","type":"tool_call"}}`)},
		{Method: "item/completed", Params: []byte(`{"turnId":"turn-semantic","item":{"id":"assistant-1","type":"agent_message","text":"done"}}`)},
		{Method: "turn/completed", Params: []byte(`{"turn":{"id":"turn-semantic"}}`)},
	} {
		rpc.notifications <- notification
	}
	deadline := time.After(time.Second)
	for {
		events, err := repo.SemanticForSession(ctx, session.ID, 0, 20)
		if err == nil && len(events) >= 4 {
			kinds := map[string]bool{}
			for _, event := range events {
				kinds[event.Kind] = true
			}
			for _, kind := range []string{string(domain.SemanticPhaseProgress), string(domain.SemanticToolActivity), string(domain.SemanticAssistantMessage), string(domain.SemanticTurnCompleted)} {
				if !kinds[kind] {
					t.Fatalf("semantic kinds=%v, missing %q", kinds, kind)
				}
			}
			break
		}
		select {
		case <-deadline:
			t.Fatalf("semantic events were not projected: err=%v", err)
		case <-time.After(time.Millisecond):
		}
	}
	_ = broker
}

func brokerFixture(t *testing.T) (context.Context, *store.ConversationRepository, domain.ChatSession) {
	ctx, _, repo, session := brokerFixtureWithDB(t)
	return ctx, repo, session
}

func brokerFixtureWithDB(t *testing.T) (context.Context, *sql.DB, *store.ConversationRepository, domain.ChatSession) {
	t.Helper()
	db, err := store.Open(t.TempDir() + "/broker.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := store.NewConversationRepository(db)
	threadID := "thread-1"
	session := domain.ChatSession{ID: uuid.NewString(), Title: "chat", Kind: domain.ChatGeneral, Status: domain.ChatIdle, CodexThreadID: &threadID}
	if err := repo.CreateSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	return context.Background(), db, repo, session
}

func createActiveTurn(t *testing.T, ctx context.Context, repo *store.ConversationRepository, sessionID, codexTurnID string) {
	t.Helper()
	turn := domain.ChatTurn{ID: uuid.NewString(), SessionID: sessionID, Status: domain.ChatTurnIdle, Delivery: domain.DeliveryAuto}
	if err := repo.CreateTurn(ctx, turn); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateTurnStatus(ctx, turn.ID, domain.ChatTurnIdle, domain.ChatTurnRunning, &codexTurnID, nil, nil); err != nil {
		t.Fatal(err)
	}
}

type fakeRPC struct {
	mu            sync.Mutex
	turnID        string
	methods       []string
	params        []map[string]any
	notifications chan codexapp.Notification
	callErr       error
}

type failingCompletionHandler struct{ called chan struct{} }

func (f *failingCompletionHandler) CompleteTurn(context.Context, string, string, string) error {
	return errors.New("unexpected completion")
}
func (f *failingCompletionHandler) FailTurn(context.Context, string, string, string, error) error {
	select {
	case f.called <- struct{}{}:
	default:
	}
	return errors.New("database unavailable")
}
func (f *failingCompletionHandler) TaskTurnStarted(context.Context, string, string, string, string) error {
	return nil
}
func (f *failingCompletionHandler) TaskDeliveryFailed(context.Context, string, string, error) error {
	return nil
}

type recordingCompletionHandler struct{ deliveryFailed chan struct{} }

func (r *recordingCompletionHandler) CompleteTurn(context.Context, string, string, string) error {
	return nil
}
func (r *recordingCompletionHandler) FailTurn(context.Context, string, string, string, error) error {
	return nil
}
func (r *recordingCompletionHandler) TaskTurnStarted(context.Context, string, string, string, string) error {
	return nil
}
func (r *recordingCompletionHandler) TaskDeliveryFailed(context.Context, string, string, error) error {
	select {
	case r.deliveryFailed <- struct{}{}:
	default:
	}
	return nil
}

func (f *fakeRPC) Call(_ context.Context, method string, params, result any) error {
	f.mu.Lock()
	f.methods = append(f.methods, method)
	copyParams, _ := params.(map[string]any)
	f.params = append(f.params, copyParams)
	f.mu.Unlock()
	if f.callErr != nil {
		return f.callErr
	}
	if target, ok := result.(*turnResult); ok {
		target.TurnID = f.turnID
	}
	return nil
}

func (f *fakeRPC) Notifications() <-chan codexapp.Notification {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.notifications == nil {
		f.notifications = make(chan codexapp.Notification, 8)
	}
	return f.notifications
}

func (f *fakeRPC) param(key string) any {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.params) == 0 {
		return nil
	}
	return f.params[len(f.params)-1][key]
}

func (f *fakeRPC) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.methods)
}

func (f *fakeRPC) method(index int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.methods[index]
}
