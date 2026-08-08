// Package conversation coordinates durable user messages with a console-owned
// Codex App Server thread. It deliberately keeps the browser out of the RPC
// protocol: every accepted message first has a durable outbox record.
package conversation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/codexapp"
	"video-production-console/internal/domain"
	"video-production-console/internal/progress"
	"video-production-console/internal/store"
	phasetiming "video-production-console/internal/timing"
)

const (
	brokerLeaseTTL      = 30 * time.Second
	staleTurnText       = "the active Codex turn changed before this message was delivered"
	taskClientKeyPrefix = "__formal_task__:"
	completionWorkers   = 4
	completionQueueSize = 64
	completionTimeout   = 5 * time.Minute
	completionRetry     = 2 * time.Second
)

var errCompletionInProgress = errors.New("formal task completion is already in progress")

// RPC is the narrow App Server surface used by the Broker. It is intentionally
// small so conversation routing can be exercised without a real Codex process.
type RPC interface {
	Call(context.Context, string, any, any) error
	Notifications() <-chan codexapp.Notification
}

type SendInput struct {
	SessionID string
	ClientKey string
	Text      string
	Delivery  domain.DeliveryMode
}

type SendReceipt struct {
	MessageID string
	Method    string
	ThreadID  string
	TurnID    string
	State     string
}

type TurnCompletedHandler interface {
	CompleteTurn(context.Context, string, string, string) error
	FailTurn(context.Context, string, string, string, error) error
	TaskTurnStarted(context.Context, string, string, string, string) error
	TaskDeliveryFailed(context.Context, string, string, error) error
}

type FormalTaskExecutionConfigProvider interface {
	TaskExecutionConfig(context.Context, string) (string, string, error)
}

// Broker serializes local writes per session and supplements that mutex with a
// database lease, which prevents two console processes from steering one thread.
type Broker struct {
	repo *store.ConversationRepository
	rpc  RPC

	ownerID   string
	startedAt time.Time

	mu        sync.Mutex
	sessions  map[string]*sync.Mutex
	turns     map[string]string // Codex turn ID -> console session ID
	completed TurnCompletedHandler

	completionWake chan struct{}
}

func (b *Broker) SetTurnCompletedHandler(handler TurnCompletedHandler) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.completed = handler
	b.mu.Unlock()
	select {
	case b.completionWake <- struct{}{}:
	default:
	}
}

func NewBroker(repo *store.ConversationRepository, rpc RPC) *Broker {
	b := &Broker{
		repo:           repo,
		rpc:            rpc,
		ownerID:        uuid.NewString(),
		startedAt:      time.Now().UTC(),
		sessions:       make(map[string]*sync.Mutex),
		turns:          make(map[string]string),
		completionWake: make(chan struct{}, completionQueueSize),
	}
	if rpc != nil {
		for range completionWorkers {
			go b.consumeCompletions()
		}
		go b.consumeNotifications(rpc.Notifications())
	}
	return b
}

// Recover restores durable Broker ownership after process startup. An outbox
// left in sending has an uncertain RPC outcome, so it is failed for explicit
// retry instead of risking a duplicate App Server call. Pending and queued
// deliveries are resumed only when their session has no durable active turn.
func (b *Broker) Recover(ctx context.Context) error {
	if b == nil || b.repo == nil || b.rpc == nil {
		return errors.New("conversation broker is not configured")
	}
	if err := b.repo.ResetProcessingCompletions(ctx); err != nil {
		return fmt.Errorf("reset interrupted turn completions: %w", err)
	}
	if _, err := b.repo.InterruptUnreconciledActiveTurns(ctx, b.startedAt); err != nil {
		return fmt.Errorf("interrupt unreconciled conversation turns: %w", err)
	}
	completionErr := b.drainCompletions(ctx)
	activeTurns, err := b.repo.ActiveTurns(ctx)
	if err != nil {
		return errors.Join(completionErr, fmt.Errorf("list active conversation turns: %w", err))
	}
	activeSessions := make(map[string]bool, len(activeTurns))
	for _, turn := range activeTurns {
		activeSessions[turn.SessionID] = true
		if turn.CodexTurnID != nil {
			b.rememberTurn(*turn.CodexTurnID, turn.SessionID)
		}
	}
	outboxes, err := b.repo.RecoveryOutbox(ctx)
	if err != nil {
		return fmt.Errorf("list recoverable conversation deliveries: %w", err)
	}
	for _, outbox := range outboxes {
		if outbox.DeliveryStatus != domain.ChatOutboxSending {
			continue
		}
		cause := errors.New("delivery was interrupted after RPC ownership became uncertain; retry explicitly with a new client key")
		failed, err := b.repo.FailSendingOutbox(ctx, outbox.ID, cause)
		if err != nil {
			return fmt.Errorf("fail uncertain conversation delivery %q: %w", outbox.ID, err)
		}
		if !failed {
			continue
		}
		if strings.HasPrefix(outbox.ClientKey, taskClientKeyPrefix) {
			b.mu.Lock()
			completed := b.completed
			b.mu.Unlock()
			if completed == nil {
				return errors.New("formal task delivery failure handler is not configured")
			}
			if err := completed.TaskDeliveryFailed(ctx, outbox.ClientKey, outbox.SessionID, cause); err != nil {
				return fmt.Errorf("fail uncertain formal task delivery: %w", err)
			}
		}
	}
	for _, outbox := range outboxes {
		if outbox.DeliveryStatus == domain.ChatOutboxSending || activeSessions[outbox.SessionID] {
			continue
		}
		deliverable, statusErr := b.formalOutboxDeliverable(ctx, outbox)
		if statusErr != nil {
			return errors.Join(completionErr, statusErr)
		}
		if !deliverable {
			continue
		}
		unlock := b.lockSession(outbox.SessionID)
		_, deliveryErr := b.deliverLocked(ctx, outbox, outbox.DeliveryStatus == domain.ChatOutboxQueued)
		unlock()
		if deliveryErr != nil {
			if strings.HasPrefix(outbox.ClientKey, taskClientKeyPrefix) {
				return errors.Join(completionErr, b.failSynchronousTaskDelivery(ctx, outbox.SessionID, outbox.ClientKey, deliveryErr))
			}
			return fmt.Errorf("recover conversation delivery %q: %w", outbox.ID, deliveryErr)
		}
		activeSessions[outbox.SessionID] = true
	}
	return completionErr
}

func formalTaskIDFromClientKey(clientKey string) string {
	value := strings.TrimPrefix(clientKey, taskClientKeyPrefix)
	if index := strings.IndexByte(value, ':'); index >= 0 {
		value = value[:index]
	}
	return strings.TrimSpace(value)
}

func (b *Broker) formalOutboxDeliverable(ctx context.Context, outbox domain.ChatOutbox) (bool, error) {
	if !strings.HasPrefix(outbox.ClientKey, taskClientKeyPrefix) {
		return true, nil
	}
	status, statusErr := b.repo.FormalTaskStatus(ctx, formalTaskIDFromClientKey(outbox.ClientKey), outbox.SessionID)
	if statusErr == nil && status == domain.TaskRunning {
		return true, nil
	}
	cause := errors.New("formal task is no longer running; queued delivery will not be sent")
	if statusErr != nil {
		cause = fmt.Errorf("formal task is unavailable: %w", statusErr)
	}
	if err := b.repo.UpdateOutboxStatus(ctx, outbox.ID, outbox.DeliveryStatus, domain.ChatOutboxFailed, nil, stringPtr(cause.Error())); err != nil {
		return false, fmt.Errorf("close terminal formal task delivery: %w", err)
	}
	return false, nil
}

// Send persists the input before touching App Server. A duplicate client key
// returns the original outbox item and never invokes Codex again.
func (b *Broker) Send(ctx context.Context, input SendInput) (SendReceipt, error) {
	if b == nil || b.repo == nil || b.rpc == nil {
		return SendReceipt{}, errors.New("conversation broker is not configured")
	}
	input.Text = strings.TrimSpace(input.Text)
	if input.SessionID == "" || input.ClientKey == "" || input.Text == "" {
		return SendReceipt{}, errors.New("session id, client key, and text are required")
	}
	if strings.HasPrefix(input.ClientKey, taskClientKeyPrefix) {
		return SendReceipt{}, errors.New("reserved conversation client key")
	}
	if input.Delivery == "" {
		input.Delivery = domain.DeliveryAuto
	}

	outbox, err := b.repo.Enqueue(ctx, input.SessionID, input.ClientKey, input.Text, input.Delivery)
	if err != nil {
		return SendReceipt{}, fmt.Errorf("enqueue conversation message: %w", err)
	}
	if outbox.DeliveryStatus == domain.ChatOutboxAccepted {
		return receiptFor(outbox, "accepted"), nil
	}
	if outbox.DeliveryStatus == domain.ChatOutboxSending {
		return receiptFor(outbox, "sending"), nil
	}
	if outbox.DeliveryStatus == domain.ChatOutboxFailed {
		return receiptFor(outbox, "failed"), errors.New("the prior delivery for this client key failed; send again with a new client key to retry")
	}
	if input.Delivery == domain.DeliveryQueue || outbox.DeliveryStatus == domain.ChatOutboxQueued {
		return receiptFor(outbox, "queued"), nil
	}

	return b.deliver(ctx, outbox)
}

// SendTask uses queue semantics even when the session currently has an active
// turn. Unlike ordinary DeliveryAuto it can never steer task input into a turn
// owned by chat or another formal task.
func (b *Broker) SendTask(ctx context.Context, input SendInput) (SendReceipt, error) {
	if b == nil || b.repo == nil || b.rpc == nil {
		return SendReceipt{}, errors.New("conversation broker is not configured")
	}
	input.Text = strings.TrimSpace(input.Text)
	if input.SessionID == "" || input.ClientKey == "" || input.Text == "" {
		return SendReceipt{}, errors.New("session id, client key, and text are required")
	}
	if !strings.HasPrefix(input.ClientKey, taskClientKeyPrefix) {
		return SendReceipt{}, errors.New("formal task client key is required")
	}
	input.Delivery = domain.DeliveryQueue
	outbox, err := b.repo.Enqueue(ctx, input.SessionID, input.ClientKey, input.Text, input.Delivery)
	if err != nil {
		return SendReceipt{}, b.failSynchronousTaskDelivery(ctx, input.SessionID, input.ClientKey, fmt.Errorf("enqueue task message: %w", err))
	}
	if outbox.DeliveryStatus == domain.ChatOutboxAccepted || outbox.DeliveryStatus == domain.ChatOutboxSending {
		return receiptFor(outbox, string(outbox.DeliveryStatus)), nil
	}
	if outbox.DeliveryStatus == domain.ChatOutboxFailed {
		cause := errors.New("the prior task delivery for this client key failed")
		return receiptFor(outbox, "failed"), b.failSynchronousTaskDelivery(ctx, input.SessionID, input.ClientKey, cause)
	}
	receipt, err := b.deliverTask(ctx, outbox)
	if err != nil {
		return receipt, b.failSynchronousTaskDelivery(ctx, input.SessionID, input.ClientKey, err)
	}
	return receipt, nil
}

func (b *Broker) failSynchronousTaskDelivery(ctx context.Context, sessionID, clientKey string, cause error) error {
	outboxErr := b.repo.FailOutboxByClientKey(ctx, sessionID, clientKey, cause)
	b.mu.Lock()
	completed := b.completed
	b.mu.Unlock()
	var taskErr error
	if completed == nil {
		taskErr = errors.New("formal task delivery failure handler is not configured")
	} else {
		taskErr = completed.TaskDeliveryFailed(ctx, clientKey, sessionID, cause)
	}
	return errors.Join(cause, outboxErr, taskErr)
}

func (b *Broker) deliverTask(ctx context.Context, outbox domain.ChatOutbox) (SendReceipt, error) {
	unlock := b.lockSession(outbox.SessionID)
	defer unlock()
	current, err := b.repo.Enqueue(ctx, outbox.SessionID, outbox.ClientKey, outbox.Content, domain.DeliveryQueue)
	if err != nil {
		return SendReceipt{}, err
	}
	if current.DeliveryStatus == domain.ChatOutboxAccepted || current.DeliveryStatus == domain.ChatOutboxSending {
		return receiptFor(current, string(current.DeliveryStatus)), nil
	}
	return b.deliverLocked(ctx, current, true)
}

func (b *Broker) deliver(ctx context.Context, outbox domain.ChatOutbox) (SendReceipt, error) {
	unlock := b.lockSession(outbox.SessionID)
	defer unlock()

	// Another browser request can have waited on this session mutex. Reload the
	// idempotent record so stale in-memory status cannot invoke Codex twice.
	current, err := b.repo.Enqueue(ctx, outbox.SessionID, outbox.ClientKey, outbox.Content, outbox.Delivery)
	if err != nil {
		return SendReceipt{}, fmt.Errorf("refresh conversation outbox: %w", err)
	}
	outbox = current
	// An already-accepted duplicate must not start another App Server turn.
	if outbox.DeliveryStatus == domain.ChatOutboxAccepted {
		return receiptFor(outbox, "accepted"), nil
	}
	if outbox.DeliveryStatus == domain.ChatOutboxSending {
		return receiptFor(outbox, "sending"), nil
	}
	if outbox.DeliveryStatus == domain.ChatOutboxFailed {
		return receiptFor(outbox, "failed"), errors.New("the prior delivery for this client key failed")
	}
	return b.deliverLocked(ctx, outbox, false)
}

func (b *Broker) deliverLocked(ctx context.Context, outbox domain.ChatOutbox, fromQueue bool) (SendReceipt, error) {
	session, err := b.repo.GetSession(ctx, outbox.SessionID)
	if err != nil {
		return SendReceipt{}, fmt.Errorf("load conversation session: %w", err)
	}
	if session.CodexThreadID == nil || strings.TrimSpace(*session.CodexThreadID) == "" {
		return b.failWithoutSending(ctx, outbox, errors.New("conversation session has no Codex thread"))
	}
	threadID := *session.CodexThreadID

	release, err := b.acquireLease(ctx, threadID, session.ID)
	if err != nil {
		return SendReceipt{}, err
	}
	defer release()

	if outbox.DeliveryStatus == domain.ChatOutboxPending || outbox.DeliveryStatus == domain.ChatOutboxQueued {
		if err := b.repo.UpdateOutboxStatus(ctx, outbox.ID, outbox.DeliveryStatus, domain.ChatOutboxSending, nil, nil); err != nil {
			return SendReceipt{}, fmt.Errorf("mark conversation delivery sending: %w", err)
		}
		outbox.DeliveryStatus = domain.ChatOutboxSending
	}

	active, activeErr := b.repo.ActiveTurn(ctx, session.ID)
	hasActive := activeErr == nil && active.CodexTurnID != nil && *active.CodexTurnID != ""
	if activeErr != nil && !errors.Is(activeErr, sql.ErrNoRows) {
		return b.failSending(ctx, outbox, fmt.Errorf("read active Codex turn: %w", activeErr))
	}

	if outbox.Delivery == domain.DeliverySteer && !hasActive {
		return b.failSending(ctx, outbox, errors.New("cannot steer because this conversation has no active Codex turn"))
	}
	if outbox.Delivery == domain.DeliveryQueue && !fromQueue {
		if err := b.repo.UpdateOutboxStatus(ctx, outbox.ID, domain.ChatOutboxSending, domain.ChatOutboxQueued, nil, nil); err != nil {
			return SendReceipt{}, fmt.Errorf("queue conversation delivery: %w", err)
		}
		return receiptFor(outbox, "queued"), nil
	}

	if hasActive && outbox.Delivery == domain.DeliveryQueue {
		if outbox.DeliveryStatus == domain.ChatOutboxSending {
			if err := b.repo.UpdateOutboxStatus(ctx, outbox.ID, domain.ChatOutboxSending, domain.ChatOutboxQueued, nil, nil); err != nil {
				return SendReceipt{}, fmt.Errorf("queue exclusive conversation delivery: %w", err)
			}
		}
		return receiptFor(outbox, "queued"), nil
	}
	if hasActive {
		return b.steer(ctx, session, active, outbox, false)
	}
	return b.start(ctx, session, outbox)
}

func (b *Broker) steer(ctx context.Context, session domain.ChatSession, active domain.ChatTurn, outbox domain.ChatOutbox, refreshed bool) (SendReceipt, error) {
	threadID := *session.CodexThreadID
	expectedTurnID := *active.CodexTurnID
	params := map[string]any{
		"threadId":       threadID,
		"expectedTurnId": expectedTurnID,
		"input":          []map[string]string{{"type": "text", "text": outbox.Content}},
	}
	var result turnResult
	err := b.rpc.Call(ctx, "turn/steer", params, &result)
	if err != nil && isStaleTurn(err) && !refreshed {
		return b.refreshAfterStale(ctx, session, outbox)
	}
	if err != nil {
		return b.failSending(ctx, outbox, fmt.Errorf("steer Codex turn: %w", err))
	}
	turnID := result.id()
	if turnID == "" {
		turnID = expectedTurnID
	}
	if err := b.repo.UpdateOutboxStatus(ctx, outbox.ID, domain.ChatOutboxSending, domain.ChatOutboxAccepted, &turnID, nil); err != nil {
		return SendReceipt{}, fmt.Errorf("accept steered conversation delivery: %w", err)
	}
	if err := b.repo.UpdateSessionStatus(ctx, session.ID, domain.ChatRunning); err != nil {
		return SendReceipt{}, fmt.Errorf("mark conversation running: %w", err)
	}
	b.rememberTurn(turnID, session.ID)
	return SendReceipt{MessageID: outbox.MessageID, Method: "turn/steer", ThreadID: threadID, TurnID: turnID, State: "accepted"}, nil
}

func (b *Broker) refreshAfterStale(ctx context.Context, session domain.ChatSession, outbox domain.ChatOutbox) (SendReceipt, error) {
	active, err := b.repo.ActiveTurn(ctx, session.ID)
	if err == nil && active.CodexTurnID != nil && *active.CodexTurnID != "" {
		return b.steer(ctx, session, active, outbox, true)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return b.failSending(ctx, outbox, fmt.Errorf("refresh active Codex turn: %w", err))
	}
	if outbox.Delivery == domain.DeliveryAuto || outbox.Delivery == domain.DeliveryQueue {
		return b.start(ctx, session, outbox)
	}
	// Explicit steering is not silently converted into a new turn. The failed
	// outbox is durable and its text remains visible for an explicit retry.
	return b.failSending(ctx, outbox, errors.New(staleTurnText))
}

func (b *Broker) start(ctx context.Context, session domain.ChatSession, outbox domain.ChatOutbox) (SendReceipt, error) {
	return b.startWithReplacement(ctx, session, outbox, true)
}

func (b *Broker) startWithReplacement(ctx context.Context, session domain.ChatSession, outbox domain.ChatOutbox, allowReplacement bool) (SendReceipt, error) {
	threadID := *session.CodexThreadID
	turn := domain.ChatTurn{
		ID:             uuid.NewString(),
		SessionID:      session.ID,
		Status:         domain.ChatTurnIdle,
		Delivery:       outbox.Delivery,
		InputMessageID: &outbox.MessageID,
	}
	if err := b.repo.CreateTurn(ctx, turn); err != nil {
		return b.failSending(ctx, outbox, fmt.Errorf("record Codex turn start: %w", err))
	}
	params := map[string]any{
		"threadId":       threadID,
		"cwd":            session.WorkingDirectory,
		"approvalPolicy": "never",
		"sandboxPolicy":  managedTurnSandboxPolicy(session.WorkingDirectory),
		"input":          []map[string]string{{"type": "text", "text": outbox.Content}},
	}
	b.mu.Lock()
	completed := b.completed
	b.mu.Unlock()
	if strings.HasPrefix(outbox.ClientKey, taskClientKeyPrefix) {
		if provider, ok := completed.(FormalTaskExecutionConfigProvider); ok {
			model, effort, configErr := provider.TaskExecutionConfig(ctx, outbox.ClientKey)
			if configErr != nil {
				return b.failSending(ctx, outbox, fmt.Errorf("resolve formal task model: %w", configErr))
			}
			if strings.TrimSpace(model) != "" {
				params["model"] = strings.TrimSpace(model)
			}
			if strings.TrimSpace(effort) != "" {
				params["effort"] = strings.TrimSpace(effort)
			}
		}
	}
	var result turnResult
	err := b.rpc.Call(ctx, "turn/start", params, &result)
	if err != nil {
		_ = b.repo.UpdateTurnStatus(ctx, turn.ID, domain.ChatTurnIdle, domain.ChatTurnFailed, nil, nil, stringPtr(err.Error()))
		if allowReplacement && isMissingThread(err) {
			replacement, replacementErr := b.replaceMissingThread(ctx, session)
			if replacementErr == nil {
				return b.startWithReplacement(ctx, replacement, outbox, false)
			}
			return b.failSending(ctx, outbox, fmt.Errorf("replace missing Codex thread: %w", replacementErr))
		}
		return b.failSending(ctx, outbox, fmt.Errorf("start Codex turn: %w", err))
	}
	turnID := result.id()
	if turnID == "" {
		_ = b.repo.UpdateTurnStatus(ctx, turn.ID, domain.ChatTurnIdle, domain.ChatTurnFailed, nil, nil, stringPtr("App Server did not return a turn ID"))
		return b.failSending(ctx, outbox, errors.New("App Server did not return a turn ID"))
	}
	if err := b.repo.UpdateTurnStatus(ctx, turn.ID, domain.ChatTurnIdle, domain.ChatTurnRunning, &turnID, nil, nil); err != nil {
		return SendReceipt{}, fmt.Errorf("mark Codex turn running: %w", err)
	}
	if err := b.repo.UpdateOutboxStatus(ctx, outbox.ID, domain.ChatOutboxSending, domain.ChatOutboxAccepted, &turnID, nil); err != nil {
		return SendReceipt{}, fmt.Errorf("accept conversation delivery: %w", err)
	}
	if err := b.repo.UpdateSessionStatus(ctx, session.ID, domain.ChatRunning); err != nil {
		return SendReceipt{}, fmt.Errorf("mark conversation running: %w", err)
	}
	if completed != nil && strings.HasPrefix(outbox.ClientKey, taskClientKeyPrefix) {
		if err := completed.TaskTurnStarted(ctx, outbox.ClientKey, session.ID, threadID, turnID); err != nil {
			return SendReceipt{}, fmt.Errorf("bind formal task turn: %w", err)
		}
	}
	b.rememberTurn(turnID, session.ID)
	return SendReceipt{MessageID: outbox.MessageID, Method: "turn/start", ThreadID: threadID, TurnID: turnID, State: "accepted"}, nil
}

// replaceMissingThread keeps the durable conversation while replacing only the
// App Server thread that can no longer accept turns. The caller retries the
// already-persisted outbox message exactly once against the replacement.
func (b *Broker) replaceMissingThread(ctx context.Context, session domain.ChatSession) (domain.ChatSession, error) {
	result := struct {
		ThreadID string `json:"threadId"`
		Thread   struct {
			ID string `json:"id"`
		} `json:"thread"`
	}{}
	if err := b.rpc.Call(ctx, "thread/start", managedThreadStartParams(session.WorkingDirectory), &result); err != nil {
		return domain.ChatSession{}, err
	}
	threadID := strings.TrimSpace(result.ThreadID)
	if threadID == "" {
		threadID = strings.TrimSpace(result.Thread.ID)
	}
	if threadID == "" {
		return domain.ChatSession{}, errors.New("Codex did not return a replacement thread ID")
	}
	if err := b.repo.SetSessionThread(ctx, session.ID, threadID); err != nil {
		return domain.ChatSession{}, err
	}
	session.CodexThreadID = &threadID
	return session, nil
}

func (b *Broker) failWithoutSending(ctx context.Context, outbox domain.ChatOutbox, cause error) (SendReceipt, error) {
	if outbox.DeliveryStatus == domain.ChatOutboxPending || outbox.DeliveryStatus == domain.ChatOutboxQueued {
		if err := b.repo.UpdateOutboxStatus(ctx, outbox.ID, outbox.DeliveryStatus, domain.ChatOutboxFailed, nil, stringPtr(cause.Error())); err != nil {
			return SendReceipt{}, fmt.Errorf("persist conversation failure: %w", err)
		}
	}
	return SendReceipt{MessageID: outbox.MessageID, State: "failed"}, cause
}

func (b *Broker) failSending(ctx context.Context, outbox domain.ChatOutbox, cause error) (SendReceipt, error) {
	if err := b.repo.UpdateOutboxStatus(ctx, outbox.ID, domain.ChatOutboxSending, domain.ChatOutboxFailed, nil, stringPtr(cause.Error())); err != nil {
		return SendReceipt{}, fmt.Errorf("persist conversation delivery failure: %w", err)
	}
	return SendReceipt{MessageID: outbox.MessageID, State: "failed"}, cause
}

func (b *Broker) acquireLease(ctx context.Context, threadID, sessionID string) (func(), error) {
	ok, err := b.repo.AcquireThreadLease(ctx, domain.ThreadLease{
		ThreadID: threadID, SessionID: sessionID, OwnerID: b.ownerID,
		ExpiresAt: time.Now().UTC().Add(brokerLeaseTTL),
	})
	if err != nil {
		return nil, fmt.Errorf("acquire Codex thread lease: %w", err)
	}
	if !ok {
		return nil, errors.New("Codex conversation is being updated by another console process")
	}
	return func() { _ = b.repo.ReleaseThreadLease(context.Background(), threadID, b.ownerID) }, nil
}

func (b *Broker) lockSession(sessionID string) func() {
	b.mu.Lock()
	lock := b.sessions[sessionID]
	if lock == nil {
		lock = &sync.Mutex{}
		b.sessions[sessionID] = lock
	}
	b.mu.Unlock()
	lock.Lock()
	return lock.Unlock
}

func (b *Broker) rememberTurn(turnID, sessionID string) {
	if turnID == "" {
		return
	}
	b.mu.Lock()
	b.turns[turnID] = sessionID
	b.mu.Unlock()
}

func (b *Broker) consumeNotifications(notifications <-chan codexapp.Notification) {
	for notification := range notifications {
		turnID := notificationTurnID(notification.Params)
		sessionID := b.sessionForTurn(turnID)
		if turnID != "" {
			if err := b.projectTimingNotification(context.Background(), turnID, notification); err != nil && sessionID != "" {
				b.recordCompletionError(sessionID, turnID, "timing_projection_failed", err)
			}
		}
		if sessionID != "" {
			if err := b.persistAssistantNotification(context.Background(), sessionID, turnID, notification); err != nil {
				b.recordCompletionError(sessionID, turnID, "assistant_persist_failed", err)
			}
			if err := b.projectNotification(context.Background(), sessionID, notification); err != nil {
				b.recordCompletionError(sessionID, turnID, "semantic_projection_failed", err)
			}
		}
		if notification.Method == "turn/completed" && sessionID != "" {
			if failure := turnCompletionFailure(notification.Params); failure != nil {
				b.mu.Lock()
				completed := b.completed
				b.mu.Unlock()
				if completed != nil {
					_ = completed.FailTurn(context.Background(), sessionID, turnID, "codex_turn_failed", failure)
				}
			}
			b.persistCompletion(sessionID, turnID)
		}
	}
	_ = b.interruptActiveTimings(context.Background())
}

func (b *Broker) interruptActiveTimings(ctx context.Context) error {
	if b == nil || b.repo == nil {
		return nil
	}
	b.mu.Lock()
	turnIDs := make([]string, 0, len(b.turns))
	for turnID := range b.turns {
		turnIDs = append(turnIDs, turnID)
	}
	b.mu.Unlock()
	var result error
	for _, turnID := range turnIDs {
		task, err := store.NewTaskRepository(b.repo.DB()).GetByCodexTurn(ctx, turnID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			result = errors.Join(result, err)
			continue
		}
		classification := phasetiming.Classification{PhaseKey: "codex_execution", DisplayName: "Codex 执行", Boundary: phasetiming.BoundaryInterrupt, ExternalItemID: turnID, DetailJSON: `{"classification":"turn_boundary"}`}
		result = errors.Join(result, phasetiming.Record(ctx, store.NewTaskTimingRepository(b.repo.DB()), task.ID, domain.PhaseSourceAppServer, classification, time.Now().UTC()))
	}
	return result
}

func (b *Broker) projectTimingNotification(ctx context.Context, turnID string, notification codexapp.Notification) error {
	if b == nil || b.repo == nil || strings.TrimSpace(turnID) == "" {
		return nil
	}
	classification, ok := progress.ProjectTiming(progress.Input{Method: notification.Method, RawJSON: string(notification.Params)})
	if !ok {
		return nil
	}
	task, err := store.NewTaskRepository(b.repo.DB()).GetByCodexTurn(ctx, turnID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return phasetiming.Record(ctx, store.NewTaskTimingRepository(b.repo.DB()), task.ID, domain.PhaseSourceAppServer, classification, time.Now().UTC())
}

func turnCompletionFailure(raw json.RawMessage) error {
	type completionError struct {
		Message string `json:"message"`
	}
	var payload struct {
		Error *completionError `json:"error"`
		Turn  struct {
			Error *completionError `json:"error"`
		} `json:"turn"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return nil
	}
	completionErr := payload.Error
	if completionErr == nil {
		completionErr = payload.Turn.Error
	}
	if completionErr == nil || strings.TrimSpace(completionErr.Message) == "" {
		return nil
	}
	return errors.New(strings.TrimSpace(completionErr.Message))
}

func (b *Broker) projectNotification(ctx context.Context, sessionID string, notification codexapp.Notification) error {
	projected := progress.Project(progress.Input{Method: notification.Method, RawJSON: string(notification.Params)})
	if !projected.Visible {
		return nil
	}
	_, err := b.repo.AppendSemantic(ctx, store.SemanticWrite{
		SessionID: sessionID,
		Kind:      string(projected.Kind),
		Phase:     projected.Phase,
		Level:     "info",
		Title:     projected.DisplayText,
	})
	return err
}

func (b *Broker) consumeCompletions() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-b.completionWake:
		case <-ticker.C:
		}
		ctx, cancel := context.WithTimeout(context.Background(), completionTimeout)
		_ = b.drainCompletions(ctx)
		cancel()
	}
}

func (b *Broker) persistCompletion(sessionID, turnID string) {
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := b.repo.EnqueueCompletion(ctx, sessionID, turnID)
		cancel()
		if err == nil {
			select {
			case b.completionWake <- struct{}{}:
			default:
			}
			return
		}
		b.recordCompletionError(sessionID, turnID, "completion_inbox_persist_failed", err)
		time.Sleep(completionRetry)
	}
}

func (b *Broker) drainCompletions(ctx context.Context) error {
	var resultErr error
	for {
		if err := b.repo.RequeueStaleCompletions(ctx, time.Now().UTC().Add(-completionTimeout-time.Minute)); err != nil {
			return errors.Join(resultErr, err)
		}
		b.mu.Lock()
		includeFormal := b.completed != nil
		b.mu.Unlock()
		item, claimed, err := b.repo.ClaimCompletion(ctx, includeFormal)
		if err != nil {
			return errors.Join(resultErr, err)
		}
		if !claimed {
			return resultErr
		}
		err = b.processTurnCompleted(ctx, item.SessionID, item.CodexTurnID)
		if err != nil {
			b.recordCompletionError(item.SessionID, item.CodexTurnID, "completion_failed", err)
			persistCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			retryErr := b.repo.RetryCompletion(persistCtx, item.ID, err, time.Now().UTC().Add(completionRetry))
			cancel()
			resultErr = errors.Join(resultErr, err, retryErr)
			if retryErr != nil {
				return resultErr
			}
			continue
		}
		persistCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		completeErr := b.repo.CompleteCompletion(persistCtx, item.ID)
		cancel()
		if completeErr != nil {
			retryCtx, retryCancel := context.WithTimeout(context.Background(), 5*time.Second)
			retryErr := b.repo.RetryCompletion(retryCtx, item.ID, completeErr, time.Now().UTC().Add(completionRetry))
			retryCancel()
			return errors.Join(resultErr, completeErr, retryErr)
		}
		if err := b.deliverAfterCompletion(ctx, item.SessionID); err != nil {
			b.recordCompletionError(item.SessionID, item.CodexTurnID, "queued_delivery_failed", err)
			resultErr = errors.Join(resultErr, err)
		}
		b.mu.Lock()
		delete(b.turns, item.CodexTurnID)
		b.mu.Unlock()
	}
}

func (b *Broker) processTurnCompleted(ctx context.Context, sessionID, turnID string) error {
	b.mu.Lock()
	completed := b.completed
	b.mu.Unlock()
	if completed != nil {
		status, formal, statusErr := b.repo.FormalTaskStatusForTurn(ctx, turnID)
		if statusErr != nil {
			return statusErr
		}
		if !formal {
			if err := completed.CompleteTurn(ctx, sessionID, turnID, ""); err != nil {
				return errors.Join(err, completed.FailTurn(ctx, sessionID, turnID, "result_completion_failed", err))
			}
		}
		if formal && (status == domain.TaskRunning || status == domain.TaskResuming) {
			resultText, readErr := b.repo.LatestAssistantTextForTurn(ctx, sessionID, turnID)
			if errors.Is(readErr, sql.ErrNoRows) {
				resultText, readErr = b.readAssistantTextFromThread(ctx, sessionID, turnID)
			}
			if readErr != nil && !errors.Is(readErr, sql.ErrNoRows) {
				return errors.Join(readErr, completed.FailTurn(ctx, sessionID, turnID, "result_read_failed", readErr))
			}
			// App Server can emit turn/completed before the final assistant item
			// has been persisted, or a formal skill may only write output/result.json
			// and return a human summary. Let the task adapter perform its own
			// result-file fallback instead of failing on a missing chat message.
			if errors.Is(readErr, sql.ErrNoRows) {
				resultText = ""
			}
			if err := completed.CompleteTurn(ctx, sessionID, turnID, resultText); err != nil {
				if errors.Is(err, errCompletionInProgress) {
					return err
				}
				return errors.Join(err, completed.FailTurn(ctx, sessionID, turnID, "result_completion_failed", err))
			}
		}
	}
	if err := b.closeTurnCompleted(ctx, sessionID, turnID); err != nil {
		return err
	}
	return nil
}

// readAssistantTextFromThread recovers the terminal assistant item when an
// App Server sends turn/completed without first forwarding item/completed to
// the notification stream. The result still goes through the same strict task
// envelope validation in TaskAdapter; this method only restores transport data.
func (b *Broker) readAssistantTextFromThread(ctx context.Context, sessionID, turnID string) (string, error) {
	session, err := b.repo.GetSession(ctx, sessionID)
	if err != nil {
		return "", err
	}
	if session.CodexThreadID == nil || strings.TrimSpace(*session.CodexThreadID) == "" {
		return "", sql.ErrNoRows
	}
	var result any
	if err := b.rpc.Call(ctx, "thread/read", map[string]any{"threadId": *session.CodexThreadID, "includeTurns": true}, &result); err != nil {
		return "", err
	}
	if text := assistantTextFromThreadRead(result, turnID, ""); text != "" {
		return text, nil
	}
	return "", sql.ErrNoRows
}

func assistantTextFromThreadRead(value any, targetTurnID, inheritedTurnID string) string {
	switch node := value.(type) {
	case []any:
		for i := len(node) - 1; i >= 0; i-- {
			if text := assistantTextFromThreadRead(node[i], targetTurnID, inheritedTurnID); text != "" {
				return text
			}
		}
	case map[string]any:
		turnID := inheritedTurnID
		if value, ok := node["turnId"].(string); ok && strings.TrimSpace(value) != "" {
			turnID = strings.TrimSpace(value)
		}
		if turn, ok := node["turn"].(map[string]any); ok {
			if value, ok := turn["id"].(string); ok && strings.TrimSpace(value) != "" {
				turnID = strings.TrimSpace(value)
			}
		}
		kind, _ := node["type"].(string)
		if turnID == targetTurnID && isAssistantItemType(kind) {
			if text, ok := node["text"].(string); ok && strings.TrimSpace(text) != "" {
				return strings.TrimSpace(text)
			}
			if raw, err := json.Marshal(node["content"]); err == nil {
				if text := textFromContent(raw); text != "" {
					return text
				}
			}
		}
		for _, value := range node {
			if text := assistantTextFromThreadRead(value, targetTurnID, turnID); text != "" {
				return text
			}
		}
	}
	return ""
}

func (b *Broker) recordCompletionError(sessionID, turnID, kind string, cause error) {
	if b == nil || b.repo == nil || strings.TrimSpace(sessionID) == "" || cause == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = b.repo.AppendSemantic(ctx, store.SemanticWrite{
		SessionID: sessionID,
		Kind:      kind,
		Phase:     "completion",
		Level:     "error",
		Title:     "Codex turn completion requires replay",
		Detail:    fmt.Sprintf("turn %s: %v", turnID, cause),
	})
}

func (b *Broker) sessionForTurn(turnID string) string {
	if strings.TrimSpace(turnID) == "" {
		return ""
	}
	b.mu.Lock()
	sessionID := b.turns[turnID]
	b.mu.Unlock()
	if sessionID != "" {
		return sessionID
	}
	// The Broker may have restarted after starting this turn. The durable turn
	// record remains authoritative after that restart.
	sessionID, _ = b.repo.SessionIDForCodexTurn(context.Background(), turnID)
	return sessionID
}

func (b *Broker) persistAssistantNotification(ctx context.Context, sessionID, fallbackTurnID string, notification codexapp.Notification) error {
	if !isAssistantItemNotification(notification.Method) {
		return nil
	}
	item, ok := parseAssistantItem(notification.Params)
	if !ok {
		return nil
	}
	if item.TurnID == "" {
		item.TurnID = fallbackTurnID
	}
	_, err := b.repo.AppendAssistantItem(ctx, sessionID, item.ID, item.TurnID, item.Text)
	return err
}

func isAssistantItemNotification(method string) bool {
	method = strings.ToLower(strings.TrimSpace(method))
	return method == "item/completed" || method == "item/updated" || method == "thread/item/completed"
}

type assistantItemNotification struct {
	ID, TurnID, Text string
}

func parseAssistantItem(raw json.RawMessage) (assistantItemNotification, bool) {
	var payload struct {
		TurnID string `json:"turnId"`
		Turn   struct {
			ID string `json:"id"`
		} `json:"turn"`
		Item struct {
			ID      string          `json:"id"`
			Type    string          `json:"type"`
			Text    string          `json:"text"`
			Content json.RawMessage `json:"content"`
		} `json:"item"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return assistantItemNotification{}, false
	}
	if !isAssistantItemType(payload.Item.Type) {
		return assistantItemNotification{}, false
	}
	text := strings.TrimSpace(payload.Item.Text)
	if text == "" {
		text = textFromContent(payload.Item.Content)
	}
	if text == "" {
		return assistantItemNotification{}, false
	}
	turnID := strings.TrimSpace(payload.TurnID)
	if turnID == "" {
		turnID = strings.TrimSpace(payload.Turn.ID)
	}
	return assistantItemNotification{ID: strings.TrimSpace(payload.Item.ID), TurnID: turnID, Text: text}, true
}

func isAssistantItemType(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "agent_message", "assistant_message", "agentmessage", "assistantmessage":
		return true
	default:
		return false
	}
}

func textFromContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return strings.TrimSpace(text)
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part.Text) != "" && (part.Type == "" || strings.Contains(strings.ToLower(part.Type), "text")) {
			values = append(values, strings.TrimSpace(part.Text))
		}
	}
	return strings.Join(values, "\n")
}

func (b *Broker) closeTurnCompleted(ctx context.Context, sessionID, turnID string) error {
	unlock := b.lockSession(sessionID)
	defer unlock()
	active, err := b.repo.ActiveTurn(ctx, sessionID)
	if err == nil && active.CodexTurnID != nil && *active.CodexTurnID == turnID {
		if err := b.repo.UpdateTurnStatus(ctx, active.ID, active.Status, domain.ChatTurnCompleted, &turnID, nil, nil); err != nil {
			return err
		}
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err := b.repo.UpdateSessionStatus(ctx, sessionID, domain.ChatIdle); err != nil {
		return err
	}
	return nil
}

func (b *Broker) deliverAfterCompletion(ctx context.Context, sessionID string) error {
	unlock := b.lockSession(sessionID)
	defer unlock()
	pending, err := b.repo.PendingOutbox(ctx, sessionID)
	if err != nil {
		return err
	}
	for _, outbox := range pending {
		if outbox.DeliveryStatus != domain.ChatOutboxQueued {
			continue
		}
		deliverable, err := b.formalOutboxDeliverable(ctx, outbox)
		if err != nil {
			return err
		}
		if !deliverable {
			continue
		}
		// Holding the session mutex and claiming queued -> sending before RPC
		// makes duplicate completed notifications unable to start it twice.
		if _, deliveryErr := b.deliverLocked(ctx, outbox, true); deliveryErr != nil {
			outboxErr := b.repo.FailOutbox(ctx, outbox.ID, deliveryErr)
			var taskErr error
			b.mu.Lock()
			completed := b.completed
			b.mu.Unlock()
			if strings.HasPrefix(outbox.ClientKey, taskClientKeyPrefix) {
				if completed == nil {
					taskErr = errors.New("formal task delivery failure handler is not configured")
				} else {
					taskErr = completed.TaskDeliveryFailed(ctx, outbox.ClientKey, sessionID, deliveryErr)
				}
			}
			if outboxErr != nil || taskErr != nil {
				return errors.Join(deliveryErr, outboxErr, taskErr)
			}
			continue
		}
		return nil
	}
	failed, err := b.repo.FailedOutbox(ctx, sessionID)
	if err != nil {
		return err
	}
	for _, outbox := range failed {
		if !strings.HasPrefix(outbox.ClientKey, taskClientKeyPrefix) {
			continue
		}
		b.mu.Lock()
		completed := b.completed
		b.mu.Unlock()
		if completed == nil {
			return errors.New("formal task delivery failure handler is not configured")
		}
		cause := errors.New("queued formal task delivery failed")
		if outbox.LastError != nil && strings.TrimSpace(*outbox.LastError) != "" {
			cause = errors.New(*outbox.LastError)
		}
		if err := completed.TaskDeliveryFailed(ctx, outbox.ClientKey, sessionID, cause); err != nil {
			return err
		}
	}
	return nil
}

type turnResult struct {
	TurnID string `json:"turnId"`
	Turn   *struct {
		ID string `json:"id"`
	} `json:"turn"`
}

func (r turnResult) id() string {
	if strings.TrimSpace(r.TurnID) != "" {
		return r.TurnID
	}
	if r.Turn != nil {
		return strings.TrimSpace(r.Turn.ID)
	}
	return ""
}

func notificationTurnID(raw json.RawMessage) string {
	var result turnResult
	if json.Unmarshal(raw, &result) == nil && result.id() != "" {
		return result.id()
	}
	var nested struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if json.Unmarshal(raw, &nested) == nil {
		return strings.TrimSpace(nested.Turn.ID)
	}
	return ""
}

func isStaleTurn(err error) bool {
	if err == nil {
		return false
	}
	var rpcErr *codexapp.RPCError
	if errors.As(err, &rpcErr) {
		text := strings.ToLower(rpcErr.Message)
		return strings.Contains(text, "stale") || strings.Contains(text, "expectedturn") || strings.Contains(text, "expected turn") || strings.Contains(text, "no active turn")
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "stale turn") || strings.Contains(text, "expected turn")
}

func receiptFor(outbox domain.ChatOutbox, state string) SendReceipt {
	turnID := ""
	if outbox.CodexTurnID != nil {
		turnID = *outbox.CodexTurnID
	}
	return SendReceipt{MessageID: outbox.MessageID, TurnID: turnID, State: state}
}

func stringPtr(value string) *string { return &value }
