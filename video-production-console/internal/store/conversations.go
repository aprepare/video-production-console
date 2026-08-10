package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/domain"
)

type ConversationRepository struct{ db *sql.DB }

type ThreadCleanupIntent struct {
	ThreadID, Reason, LastError string
	Attempts                    int
}

var (
	ErrConversationProtected = errors.New("conversation is protected")
	ErrConversationActive    = errors.New("conversation has active work")
)

func NewConversationRepository(db *sql.DB) *ConversationRepository {
	return &ConversationRepository{db: db}
}

// DB exposes the shared database to protocol adapters that must atomically
// project durable task state derived from conversation notifications.
func (r *ConversationRepository) DB() *sql.DB { return r.db }

func (r *ConversationRepository) CreateSession(ctx context.Context, session domain.ChatSession) error {
	if strings.TrimSpace(session.ID) == "" {
		return fmt.Errorf("session id is required")
	}
	if session.Kind == "" {
		session.Kind = domain.ChatGeneral
	}
	if session.Status == "" {
		session.Status = domain.ChatIdle
	}
	if strings.TrimSpace(session.Source) == "" {
		session.Source = "console"
	}
	now := time.Now().UTC()
	if session.CreatedAt.IsZero() {
		session.CreatedAt = now
	}
	if session.UpdatedAt.IsZero() {
		session.UpdatedAt = session.CreatedAt
	}
	skillNames, err := json.Marshal(session.SkillNames)
	if err != nil {
		return fmt.Errorf("encode session skill names: %w", err)
	}
	if session.SkillNames == nil {
		skillNames = []byte("[]")
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO chat_sessions(
        id,title,source,kind,status,project_id,idea_session_id,codex_thread_id,
        working_directory,model,reasoning_effort,skill_names_json,created_at,updated_at
    ) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		session.ID, session.Title, session.Source, session.Kind, session.Status,
		session.ProjectID, session.IdeaSessionID, session.CodexThreadID,
		session.WorkingDirectory, session.Model, session.ReasoningEffort, string(skillNames),
		session.CreatedAt, session.UpdatedAt)
	return err
}

func (r *ConversationRepository) GetSession(ctx context.Context, id string) (domain.ChatSession, error) {
	row := r.db.QueryRowContext(ctx, `SELECT id,title,source,kind,status,project_id,idea_session_id,codex_thread_id,
        working_directory,model,reasoning_effort,skill_names_json,created_at,updated_at
        FROM chat_sessions WHERE id=?`, id)
	session, err := scanChatSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return session, fmt.Errorf("chat session %q: %w", id, sql.ErrNoRows)
	}
	return session, err
}

// ResolveProjectMainSession returns the single console-owned main conversation
// protected by chat_sessions_project_main_uq.
func (r *ConversationRepository) ResolveProjectMainSession(ctx context.Context, projectID string) (domain.ChatSession, error) {
	row := r.db.QueryRowContext(ctx, `SELECT id,title,source,kind,status,project_id,idea_session_id,codex_thread_id,
        working_directory,model,reasoning_effort,skill_names_json,created_at,updated_at
        FROM chat_sessions
        WHERE project_id=? AND kind='project' AND source='console'
        LIMIT 1`, projectID)
	session, err := scanChatSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return session, fmt.Errorf("project %q main chat session: %w", projectID, sql.ErrNoRows)
	}
	return session, err
}

func (r *ConversationRepository) ProjectThreadLabel(ctx context.Context, projectID string) (string, string, error) {
	var accountName, projectTitle string
	err := r.db.QueryRowContext(ctx, `SELECT accounts.name,projects.title FROM projects JOIN accounts ON accounts.id=projects.account_id WHERE projects.id=?`, projectID).Scan(&accountName, &projectTitle)
	return accountName, projectTitle, err
}

func (r *ConversationRepository) SessionHasActiveWork(ctx context.Context, sessionID string) (bool, error) {
	var activeTurns, activeTasks int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM chat_turns WHERE session_id=? AND status IN ('running','awaiting_input')`, sessionID).Scan(&activeTurns); err != nil {
		return false, err
	}
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM codex_tasks WHERE chat_session_id=? AND status IN ('queued','running','awaiting_input','resuming','waiting_input')`, sessionID).Scan(&activeTasks); err != nil {
		return false, err
	}
	return activeTurns != 0 || activeTasks != 0, nil
}

// DemoteProjectMainSession preserves a superseded or unusable conversation,
// including its thread and messages, while releasing the main-session slot.
func (r *ConversationRepository) DemoteProjectMainSession(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE chat_sessions SET source='console_fork',updated_at=? WHERE id=? AND source='console' AND kind='project'`, time.Now().UTC(), id)
	return err
}

func (r *ConversationRepository) RecordThreadCleanup(ctx context.Context, threadID, reason string, cause error) error {
	message := "cleanup failed"
	if cause != nil {
		message = cause.Error()
	}
	now := time.Now().UTC()
	_, err := r.db.ExecContext(ctx, `INSERT INTO thread_cleanup_intents(thread_id,reason,attempts,last_error,created_at,updated_at) VALUES(?,?,1,?,?,?) ON CONFLICT(thread_id) DO UPDATE SET attempts=attempts+1,last_error=excluded.last_error,updated_at=excluded.updated_at`, threadID, reason, message, now, now)
	return err
}

func (r *ConversationRepository) PendingThreadCleanup(ctx context.Context, limit int) ([]ThreadCleanupIntent, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	rows, err := r.db.QueryContext(ctx, `SELECT thread_id,reason,attempts,last_error FROM thread_cleanup_intents ORDER BY updated_at,thread_id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ThreadCleanupIntent
	for rows.Next() {
		var item ThreadCleanupIntent
		if err := rows.Scan(&item.ThreadID, &item.Reason, &item.Attempts, &item.LastError); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *ConversationRepository) CompleteThreadCleanup(ctx context.Context, threadID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM thread_cleanup_intents WHERE thread_id=?`, threadID)
	return err
}

func (r *ConversationRepository) ListSessions(ctx context.Context) ([]domain.ChatSession, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,title,source,kind,status,project_id,idea_session_id,codex_thread_id,
        working_directory,model,reasoning_effort,skill_names_json,created_at,updated_at
        FROM chat_sessions ORDER BY updated_at DESC,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.ChatSession{}
	for rows.Next() {
		session, err := scanChatSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, session)
	}
	return out, rows.Err()
}

func (r *ConversationRepository) OwnedThreadIDs(ctx context.Context) (map[string]bool, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT codex_thread_id FROM chat_sessions WHERE codex_thread_id IS NOT NULL AND codex_thread_id<>''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	owned := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		owned[id] = true
	}
	return owned, rows.Err()
}

func (r *ConversationRepository) DeleteMapping(ctx context.Context, id string) error {
	return r.immediate(ctx, "delete chat session", func(q assetDBTX, _ time.Time) error {
		var kind domain.ChatKind
		var source string
		if err := q.QueryRowContext(ctx, `SELECT kind,source FROM chat_sessions WHERE id=?`, id).Scan(&kind, &source); err != nil {
			return fmt.Errorf("chat session %q: %w", id, err)
		}
		if kind == domain.ChatProject && source == "console" {
			return ErrConversationProtected
		}
		var activeTurns, activeTasks int
		if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM chat_turns WHERE session_id=? AND status IN ('running','awaiting_input')`, id).Scan(&activeTurns); err != nil {
			return err
		}
		if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM codex_tasks WHERE chat_session_id=? AND status IN ('queued','running','awaiting_input','resuming','waiting_input')`, id).Scan(&activeTasks); err != nil {
			return err
		}
		if activeTurns != 0 || activeTasks != 0 {
			return ErrConversationActive
		}
		result, err := q.ExecContext(ctx, `DELETE FROM chat_sessions WHERE id=?`, id)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil {
			return err
		} else if affected != 1 {
			return fmt.Errorf("chat session %q: %w", id, sql.ErrNoRows)
		}
		return nil
	})
}

func (r *ConversationRepository) Enqueue(ctx context.Context, sessionID, clientKey, content string, delivery domain.DeliveryMode) (domain.ChatOutbox, error) {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(clientKey) == "" {
		return domain.ChatOutbox{}, fmt.Errorf("session id and client key are required")
	}
	if delivery != domain.DeliveryAuto && delivery != domain.DeliverySteer && delivery != domain.DeliveryQueue {
		return domain.ChatOutbox{}, fmt.Errorf("unsupported delivery mode %q", delivery)
	}
	var out domain.ChatOutbox
	err := r.immediate(ctx, "enqueue chat message", func(q assetDBTX, now time.Time) error {
		existing, err := getChatOutbox(ctx, q, sessionID, clientKey)
		if err == nil {
			out = existing
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var sequence int64
		if err := q.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM chat_messages WHERE session_id=?`, sessionID).Scan(&sequence); err != nil {
			return err
		}
		messageID := uuid.NewString()
		deliveryStatus := domain.ChatOutboxPending
		if delivery == domain.DeliveryQueue {
			deliveryStatus = domain.ChatOutboxQueued
		}
		out = domain.ChatOutbox{
			ID: uuid.NewString(), SessionID: sessionID, MessageID: messageID, ClientKey: clientKey,
			Content: content, Delivery: delivery, DeliveryStatus: deliveryStatus, AvailableAt: now,
			CreatedAt: now, UpdatedAt: now,
		}
		if _, err := q.ExecContext(ctx, `INSERT INTO chat_messages(
            id,session_id,role,kind,content,delivery_status,client_key,sequence,created_at
        ) VALUES(?,?,?,?,?,?,?,?,?)`, messageID, sessionID, "user", "input", content, out.DeliveryStatus, clientKey, sequence, now); err != nil {
			return err
		}
		_, err = q.ExecContext(ctx, `INSERT INTO chat_outbox(
            id,session_id,message_id,client_key,content,delivery_mode,delivery_status,
            attempts,available_at,created_at,updated_at
        ) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, out.ID, sessionID, messageID, clientKey, content,
			delivery, out.DeliveryStatus, 0, now, now, now)
		return err
	})
	return out, err
}

func (r *ConversationRepository) ListMessages(ctx context.Context, sessionID string) ([]domain.ChatMessage, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,session_id,role,kind,content,delivery_status,client_key,
        codex_item_id,turn_id,sequence,created_at FROM chat_messages WHERE session_id=? ORDER BY sequence,id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.ChatMessage{}
	for rows.Next() {
		var message domain.ChatMessage
		if err := rows.Scan(&message.ID, &message.SessionID, &message.Role, &message.Kind, &message.Content,
			&message.DeliveryStatus, &message.ClientKey, &message.CodexItemID, &message.TurnID,
			&message.Sequence, &message.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, message)
	}
	return out, rows.Err()
}

// LatestAssistantTextForTurn returns the last durable assistant item emitted
// for one App Server turn. turn/completed is only a transport signal; callers
// must still validate this text as the task's formal result envelope.
func (r *ConversationRepository) LatestAssistantTextForTurn(ctx context.Context, sessionID, turnID string) (string, error) {
	sessionID = strings.TrimSpace(sessionID)
	turnID = strings.TrimSpace(turnID)
	if sessionID == "" || turnID == "" {
		return "", fmt.Errorf("assistant result session and turn ids are required")
	}
	var content string
	err := r.db.QueryRowContext(ctx, `SELECT content FROM chat_messages
        WHERE session_id=? AND turn_id=? AND role='assistant'
        ORDER BY sequence DESC,id DESC LIMIT 1`, sessionID, turnID).Scan(&content)
	if err != nil {
		return "", err
	}
	return content, nil
}

func (r *ConversationRepository) AppendMessage(ctx context.Context, message domain.ChatMessage) (domain.ChatMessage, error) {
	if strings.TrimSpace(message.SessionID) == "" || strings.TrimSpace(message.Role) == "" || strings.TrimSpace(message.Kind) == "" || strings.TrimSpace(message.Content) == "" {
		return message, fmt.Errorf("message session id, role, kind, and content are required")
	}
	if message.Sequence < 0 {
		return message, fmt.Errorf("message sequence cannot be negative")
	}
	if !validOutboxStatus(domain.ChatOutboxStatus(message.DeliveryStatus)) {
		return message, fmt.Errorf("invalid message delivery status %q", message.DeliveryStatus)
	}
	if strings.TrimSpace(message.ID) == "" {
		message.ID = uuid.NewString()
	}
	err := r.immediate(ctx, "append chat message", func(q assetDBTX, now time.Time) error {
		if message.Sequence == 0 {
			if err := q.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM chat_messages WHERE session_id=?`, message.SessionID).Scan(&message.Sequence); err != nil {
				return err
			}
		}
		if message.CreatedAt.IsZero() {
			message.CreatedAt = now
		}
		_, err := q.ExecContext(ctx, `INSERT INTO chat_messages(id,session_id,role,kind,content,delivery_status,
            client_key,codex_item_id,turn_id,sequence,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			message.ID, message.SessionID, message.Role, message.Kind, message.Content, message.DeliveryStatus,
			message.ClientKey, message.CodexItemID, message.TurnID, message.Sequence, message.CreatedAt)
		return err
	})
	return message, err
}

// AppendAssistantItem records one App Server item exactly once. Codex can
// replay notifications after a reconnect, so the server item ID is treated as
// the idempotency key instead of relying on arrival order. Updated item
// notifications refresh the same row so turn completion observes final text.
func (r *ConversationRepository) AppendAssistantItem(ctx context.Context, sessionID, itemID, turnID, content string) (bool, error) {
	sessionID = strings.TrimSpace(sessionID)
	itemID = strings.TrimSpace(itemID)
	turnID = strings.TrimSpace(turnID)
	content = strings.TrimSpace(content)
	if sessionID == "" || content == "" {
		return false, fmt.Errorf("assistant item session and content are required")
	}
	inserted := false
	err := r.immediate(ctx, "append Codex assistant item", func(q assetDBTX, now time.Time) error {
		if itemID != "" {
			var existingID, existingContent string
			err := q.QueryRowContext(ctx, `SELECT id,content FROM chat_messages WHERE session_id=? AND codex_item_id=? LIMIT 1`, sessionID, itemID).Scan(&existingID, &existingContent)
			if err == nil {
				if existingContent != content {
					_, err = q.ExecContext(ctx, `UPDATE chat_messages SET content=?,turn_id=COALESCE(NULLIF(?,''),turn_id) WHERE id=?`, content, turnID, existingID)
				}
				return err
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		var sequence int64
		if err := q.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM chat_messages WHERE session_id=?`, sessionID).Scan(&sequence); err != nil {
			return err
		}
		_, err := q.ExecContext(ctx, `INSERT INTO chat_messages(id,session_id,role,kind,content,delivery_status,client_key,codex_item_id,turn_id,sequence,created_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?)`, uuid.NewString(), sessionID, "assistant", "agent_message", content, domain.ChatOutboxAccepted, "", nullableString(itemID), nullableString(turnID), sequence, now)
		if err != nil {
			return err
		}
		inserted = true
		return nil
	})
	return inserted, err
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func (r *ConversationRepository) CreateTurn(ctx context.Context, turn domain.ChatTurn) error {
	if strings.TrimSpace(turn.ID) == "" || strings.TrimSpace(turn.SessionID) == "" {
		return fmt.Errorf("turn id and session id are required")
	}
	if !validTurnStatus(turn.Status) {
		return fmt.Errorf("invalid turn status %q", turn.Status)
	}
	if !validDeliveryMode(turn.Delivery) {
		return fmt.Errorf("invalid turn delivery mode %q", turn.Delivery)
	}
	if turn.InputMessageID != nil {
		var messageSessionID string
		if err := r.db.QueryRowContext(ctx, `SELECT session_id FROM chat_messages WHERE id=?`, *turn.InputMessageID).Scan(&messageSessionID); err != nil {
			return fmt.Errorf("read turn input message: %w", err)
		}
		if messageSessionID != turn.SessionID {
			return fmt.Errorf("turn input message belongs to another session")
		}
	}
	now := time.Now().UTC()
	if turn.CreatedAt.IsZero() {
		turn.CreatedAt = now
	}
	if turn.UpdatedAt.IsZero() {
		turn.UpdatedAt = turn.CreatedAt
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO chat_turns(id,session_id,codex_turn_id,status,delivery_mode,
        input_message_id,error_code,error_message,started_at,finished_at,created_at,updated_at)
        VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, turn.ID, turn.SessionID, turn.CodexTurnID, turn.Status, turn.Delivery,
		turn.InputMessageID, turn.ErrorCode, turn.ErrorMessage, turn.StartedAt, turn.FinishedAt, turn.CreatedAt, turn.UpdatedAt)
	return err
}

func (r *ConversationRepository) ActiveTurn(ctx context.Context, sessionID string) (domain.ChatTurn, error) {
	var turn domain.ChatTurn
	err := r.db.QueryRowContext(ctx, `SELECT id,session_id,codex_turn_id,status,delivery_mode,input_message_id,
        error_code,error_message,started_at,finished_at,created_at,updated_at FROM chat_turns
		WHERE session_id=? AND status IN ('running','awaiting_input') ORDER BY updated_at DESC,id LIMIT 1`, sessionID).Scan(
		&turn.ID, &turn.SessionID, &turn.CodexTurnID, &turn.Status, &turn.Delivery, &turn.InputMessageID,
		&turn.ErrorCode, &turn.ErrorMessage, &turn.StartedAt, &turn.FinishedAt, &turn.CreatedAt, &turn.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return turn, fmt.Errorf("active turn for chat session %q: %w", sessionID, sql.ErrNoRows)
	}
	return turn, err
}

// SessionIDForCodexTurn resolves a persisted App Server turn back to its
// console session. It lets a restarted Broker process completion notifications
// without depending on an in-memory turn map.
func (r *ConversationRepository) SessionIDForCodexTurn(ctx context.Context, codexTurnID string) (string, error) {
	codexTurnID = strings.TrimSpace(codexTurnID)
	if codexTurnID == "" {
		return "", fmt.Errorf("Codex turn id is required")
	}
	var sessionID string
	err := r.db.QueryRowContext(ctx, `SELECT session_id FROM chat_turns
		WHERE codex_turn_id=? ORDER BY updated_at DESC,id LIMIT 1`, codexTurnID).Scan(&sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("Codex turn %q: %w", codexTurnID, sql.ErrNoRows)
	}
	if err != nil {
		return "", err
	}
	return sessionID, nil
}

func (r *ConversationRepository) UpdateTurnStatus(ctx context.Context, id string, expected, next domain.ChatTurnStatus, codexTurnID *string, errorCode, errorMessage *string) error {
	if !validTurnTransition(expected, next) {
		return fmt.Errorf("invalid turn status transition %q -> %q", expected, next)
	}
	now := time.Now().UTC()
	result, err := r.db.ExecContext(ctx, `UPDATE chat_turns SET status=?,codex_turn_id=COALESCE(?,codex_turn_id),
        error_code=?,error_message=?,started_at=CASE WHEN ?='running' THEN COALESCE(started_at,?) ELSE started_at END,
		finished_at=CASE WHEN ? IN ('completed','failed') THEN ? ELSE finished_at END,updated_at=? WHERE id=? AND status=?`,
		next, codexTurnID, errorCode, errorMessage, next, now, next, now, now, id, expected)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return err
	} else if affected != 1 {
		return fmt.Errorf("chat turn %q did not have expected status %q: %w", id, expected, sql.ErrNoRows)
	}
	return nil
}

func (r *ConversationRepository) PendingOutbox(ctx context.Context, sessionID string) ([]domain.ChatOutbox, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,session_id,message_id,client_key,content,delivery_mode,
        delivery_status,expected_turn_id,codex_turn_id,attempts,available_at,claimed_at,last_error,created_at,updated_at
        FROM chat_outbox WHERE session_id=? AND delivery_status IN ('pending','queued') AND available_at<=?
        ORDER BY created_at,id`, sessionID, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.ChatOutbox{}
	for rows.Next() {
		item, err := scanChatOutbox(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// RecoveryOutbox returns every non-terminal delivery whose ownership may have
// been lost when the console process stopped. Callers must treat sending as an
// uncertain RPC outcome and must not send it again automatically.
func (r *ConversationRepository) RecoveryOutbox(ctx context.Context) ([]domain.ChatOutbox, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,session_id,message_id,client_key,content,delivery_mode,
        delivery_status,expected_turn_id,codex_turn_id,attempts,available_at,claimed_at,last_error,created_at,updated_at
        FROM chat_outbox WHERE delivery_status IN ('pending','queued','sending') AND available_at<=?
        ORDER BY session_id,created_at,id`, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.ChatOutbox{}
	for rows.Next() {
		item, err := scanChatOutbox(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// ActiveTurns returns durable turn ownership for Broker startup recovery.
func (r *ConversationRepository) ActiveTurns(ctx context.Context) ([]domain.ChatTurn, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,session_id,codex_turn_id,status,delivery_mode,input_message_id,
        error_code,error_message,started_at,finished_at,created_at,updated_at FROM chat_turns
        WHERE status IN ('running','awaiting_input') ORDER BY updated_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.ChatTurn{}
	for rows.Next() {
		var turn domain.ChatTurn
		if err := rows.Scan(&turn.ID, &turn.SessionID, &turn.CodexTurnID, &turn.Status, &turn.Delivery, &turn.InputMessageID,
			&turn.ErrorCode, &turn.ErrorMessage, &turn.StartedAt, &turn.FinishedAt, &turn.CreatedAt, &turn.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, turn)
	}
	return out, rows.Err()
}

// InterruptUnreconciledActiveTurns releases durable turns for which no
// completed notification was persisted before restart. App Server RPC state is
// not assumed to survive process ownership changes.
func (r *ConversationRepository) InterruptUnreconciledActiveTurns(ctx context.Context, before time.Time) (int, error) {
	if before.IsZero() {
		return 0, fmt.Errorf("conversation recovery cutoff is required")
	}
	interrupted := 0
	err := r.immediate(ctx, "interrupt unreconciled conversation turns", func(q assetDBTX, now time.Time) error {
		rows, err := q.QueryContext(ctx, `SELECT id,session_id FROM chat_turns t
			WHERE status IN ('running','awaiting_input') AND updated_at<?
			  AND (codex_turn_id IS NULL OR NOT EXISTS (
				SELECT 1 FROM chat_completion_inbox c
				WHERE c.codex_turn_id=t.codex_turn_id AND c.status IN ('pending','processing')
			  )) ORDER BY updated_at,id`, before)
		if err != nil {
			return err
		}
		type target struct{ id, sessionID string }
		var targets []target
		for rows.Next() {
			var item target
			if err := rows.Scan(&item.id, &item.sessionID); err != nil {
				_ = rows.Close()
				return err
			}
			targets = append(targets, item)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, item := range targets {
			result, err := q.ExecContext(ctx, `UPDATE chat_turns SET status=?,error_code=?,error_message=?,finished_at=?,updated_at=?
				WHERE id=? AND status IN ('running','awaiting_input')`, domain.ChatTurnFailed, "interrupted_on_restart", "Console restarted before the App Server turn was reconciled.", now, now, item.id)
			if err != nil {
				return err
			}
			if affected, err := result.RowsAffected(); err != nil || affected != 1 {
				if err != nil {
					return err
				}
				return fmt.Errorf("chat turn %q changed while recovering", item.id)
			}
			if _, err := q.ExecContext(ctx, `UPDATE chat_sessions SET status=?,updated_at=? WHERE id=?`, domain.ChatIdle, now, item.sessionID); err != nil {
				return err
			}
			interrupted++
		}
		return nil
	})
	return interrupted, err
}

func (r *ConversationRepository) FormalTaskStatus(ctx context.Context, taskID, sessionID string) (domain.TaskStatus, error) {
	taskID, sessionID = strings.TrimSpace(taskID), strings.TrimSpace(sessionID)
	if taskID == "" || sessionID == "" {
		return "", fmt.Errorf("formal task and session ids are required")
	}
	var status domain.TaskStatus
	err := r.db.QueryRowContext(ctx, `SELECT status FROM codex_tasks
		WHERE id=? AND chat_session_id=? AND transport='app_server'`, taskID, sessionID).Scan(&status)
	return status, err
}

func (r *ConversationRepository) FormalTaskStatusForTurn(ctx context.Context, turnID string) (domain.TaskStatus, bool, error) {
	var status domain.TaskStatus
	err := r.db.QueryRowContext(ctx, `SELECT status FROM codex_tasks
		WHERE codex_turn_id=? AND transport='app_server' ORDER BY created_at DESC,id DESC LIMIT 1`, strings.TrimSpace(turnID)).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return status, err == nil, err
}

func (r *ConversationRepository) EnqueueCompletion(ctx context.Context, sessionID, turnID string) error {
	sessionID, turnID = strings.TrimSpace(sessionID), strings.TrimSpace(turnID)
	if sessionID == "" || turnID == "" {
		return fmt.Errorf("completion session and turn ids are required")
	}
	return r.immediate(ctx, "enqueue turn completion", func(q assetDBTX, now time.Time) error {
		if _, err := q.ExecContext(ctx, `INSERT INTO chat_completion_inbox(
			id,session_id,codex_turn_id,status,attempts,available_at,created_at,updated_at)
			VALUES(?,?,?,?,0,?,?,?) ON CONFLICT(codex_turn_id) DO NOTHING`, uuid.NewString(), sessionID, turnID, domain.ChatCompletionPending, now, now, now); err != nil {
			return err
		}
		var durableSession string
		if err := q.QueryRowContext(ctx, `SELECT session_id FROM chat_completion_inbox WHERE codex_turn_id=?`, turnID).Scan(&durableSession); err != nil {
			return err
		}
		if durableSession != sessionID {
			return fmt.Errorf("completion turn %q belongs to another session", turnID)
		}
		return nil
	})
}

func (r *ConversationRepository) ResetProcessingCompletions(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, `UPDATE chat_completion_inbox
		SET status=?,available_at=?,claimed_at=NULL,last_error=?,updated_at=? WHERE status=?`,
		domain.ChatCompletionPending, time.Now().UTC(), "completion worker interrupted by restart", time.Now().UTC(), domain.ChatCompletionProcessing)
	return err
}

func (r *ConversationRepository) RequeueStaleCompletions(ctx context.Context, claimedBefore time.Time) error {
	if claimedBefore.IsZero() {
		return fmt.Errorf("stale completion cutoff is required")
	}
	now := time.Now().UTC()
	_, err := r.db.ExecContext(ctx, `UPDATE chat_completion_inbox
		SET status=?,available_at=?,claimed_at=NULL,last_error=?,updated_at=?
		WHERE status=? AND claimed_at<?`, domain.ChatCompletionPending, now, "completion worker lease expired", now, domain.ChatCompletionProcessing, claimedBefore)
	return err
}

func (r *ConversationRepository) ClaimCompletion(ctx context.Context, includeFormal bool) (domain.ChatCompletionInbox, bool, error) {
	var item domain.ChatCompletionInbox
	claimed := false
	err := r.immediate(ctx, "claim turn completion", func(q assetDBTX, now time.Time) error {
		err := q.QueryRowContext(ctx, `SELECT id,session_id,codex_turn_id,status,attempts,available_at,claimed_at,last_error,created_at,updated_at,completed_at
			FROM chat_completion_inbox c WHERE status=? AND available_at<=?
			  AND (? OR NOT EXISTS (
				SELECT 1 FROM codex_tasks t WHERE t.transport='app_server' AND t.codex_turn_id=c.codex_turn_id AND t.status IN ('running','resuming')
			  ))
			ORDER BY available_at,created_at,id LIMIT 1`, domain.ChatCompletionPending, now, includeFormal).Scan(
			&item.ID, &item.SessionID, &item.CodexTurnID, &item.Status, &item.Attempts, &item.AvailableAt, &item.ClaimedAt, &item.LastError, &item.CreatedAt, &item.UpdatedAt, &item.CompletedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		result, err := q.ExecContext(ctx, `UPDATE chat_completion_inbox SET status=?,attempts=attempts+1,claimed_at=?,updated_at=?
			WHERE id=? AND status=?`, domain.ChatCompletionProcessing, now, now, item.ID, domain.ChatCompletionPending)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		claimed = affected == 1
		if claimed {
			item.Status = domain.ChatCompletionProcessing
			item.Attempts++
			item.ClaimedAt = &now
			item.UpdatedAt = now
		}
		return nil
	})
	return item, claimed, err
}

func (r *ConversationRepository) CompleteCompletion(ctx context.Context, id string) error {
	now := time.Now().UTC()
	result, err := r.db.ExecContext(ctx, `UPDATE chat_completion_inbox SET status=?,completed_at=?,last_error=NULL,updated_at=?
		WHERE id=? AND status=?`, domain.ChatCompletionDone, now, now, id, domain.ChatCompletionProcessing)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return err
		}
		return fmt.Errorf("completion inbox %q is no longer processing", id)
	}
	return nil
}

func (r *ConversationRepository) RetryCompletion(ctx context.Context, id string, cause error, availableAt time.Time) error {
	if cause == nil {
		return fmt.Errorf("completion retry failure is required")
	}
	result, err := r.db.ExecContext(ctx, `UPDATE chat_completion_inbox SET status=?,available_at=?,claimed_at=NULL,last_error=?,updated_at=?
		WHERE id=? AND status=?`, domain.ChatCompletionPending, availableAt, cause.Error(), time.Now().UTC(), id, domain.ChatCompletionProcessing)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return err
		}
		return fmt.Errorf("completion inbox %q is no longer processing", id)
	}
	return nil
}

func (r *ConversationRepository) CompletionForTurn(ctx context.Context, turnID string) (domain.ChatCompletionInbox, error) {
	var item domain.ChatCompletionInbox
	err := r.db.QueryRowContext(ctx, `SELECT id,session_id,codex_turn_id,status,attempts,available_at,claimed_at,last_error,created_at,updated_at,completed_at
		FROM chat_completion_inbox WHERE codex_turn_id=?`, strings.TrimSpace(turnID)).Scan(
		&item.ID, &item.SessionID, &item.CodexTurnID, &item.Status, &item.Attempts, &item.AvailableAt,
		&item.ClaimedAt, &item.LastError, &item.CreatedAt, &item.UpdatedAt, &item.CompletedAt)
	return item, err
}

func (r *ConversationRepository) FailedOutbox(ctx context.Context, sessionID string) ([]domain.ChatOutbox, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,session_id,message_id,client_key,content,delivery_mode,
        delivery_status,expected_turn_id,codex_turn_id,attempts,available_at,claimed_at,last_error,created_at,updated_at
        FROM chat_outbox WHERE session_id=? AND delivery_status='failed' ORDER BY updated_at,id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.ChatOutbox{}
	for rows.Next() {
		item, err := scanChatOutbox(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *ConversationRepository) UpdateOutboxStatus(ctx context.Context, id string, expected, next domain.ChatOutboxStatus, codexTurnID *string, lastError *string) error {
	if !validOutboxTransition(expected, next) {
		return fmt.Errorf("invalid outbox status transition %q -> %q", expected, next)
	}
	return r.immediate(ctx, "update chat delivery", func(q assetDBTX, now time.Time) error {
		result, err := q.ExecContext(ctx, `UPDATE chat_outbox SET delivery_status=?,codex_turn_id=?,last_error=?,
			attempts=attempts+CASE WHEN ?='sending' THEN 1 ELSE 0 END,claimed_at=CASE WHEN ?='sending' THEN ? ELSE claimed_at END,updated_at=? WHERE id=? AND delivery_status=?`,
			next, codexTurnID, lastError, next, next, now, now, id, expected)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return fmt.Errorf("chat outbox %q did not have expected status %q: %w", id, expected, sql.ErrNoRows)
		}
		_, err = q.ExecContext(ctx, `UPDATE chat_messages SET delivery_status=?,turn_id=?
			WHERE id=(SELECT message_id FROM chat_outbox WHERE id=?)`, next, codexTurnID, id)
		return err
	})
}

// FailOutbox records an unrecoverable queued delivery even when the lower
// layer failed after changing queued -> sending or accepted.
func (r *ConversationRepository) FailOutbox(ctx context.Context, id string, cause error) error {
	if strings.TrimSpace(id) == "" || cause == nil {
		return fmt.Errorf("outbox id and failure are required")
	}
	return r.immediate(ctx, "fail queued conversation delivery", func(q assetDBTX, now time.Time) error {
		result, err := q.ExecContext(ctx, `UPDATE chat_outbox SET delivery_status=?,last_error=?,updated_at=? WHERE id=?`, domain.ChatOutboxFailed, cause.Error(), now, id)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			if err != nil {
				return err
			}
			return fmt.Errorf("chat outbox %q: %w", id, sql.ErrNoRows)
		}
		_, err = q.ExecContext(ctx, `UPDATE chat_messages SET delivery_status=? WHERE id=(SELECT message_id FROM chat_outbox WHERE id=?)`, domain.ChatOutboxFailed, id)
		return err
	})
}

// FailOutboxByClientKey closes uncertain synchronous delivery outcomes. A
// missing row is already terminal for this invariant; any live matching row
// and its visible message are forced to failed in one transaction.
func (r *ConversationRepository) FailOutboxByClientKey(ctx context.Context, sessionID, clientKey string, cause error) error {
	sessionID, clientKey = strings.TrimSpace(sessionID), strings.TrimSpace(clientKey)
	if sessionID == "" || clientKey == "" || cause == nil {
		return fmt.Errorf("session, client key, and failure are required")
	}
	return r.immediate(ctx, "fail task delivery by client key", func(q assetDBTX, now time.Time) error {
		var outboxID, messageID string
		err := q.QueryRowContext(ctx, `SELECT id,message_id FROM chat_outbox WHERE session_id=? AND client_key=?`, sessionID, clientKey).Scan(&outboxID, &messageID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `UPDATE chat_outbox SET delivery_status=?,last_error=?,updated_at=? WHERE id=?`, domain.ChatOutboxFailed, cause.Error(), now, outboxID); err != nil {
			return err
		}
		_, err = q.ExecContext(ctx, `UPDATE chat_messages SET delivery_status=? WHERE id=?`, domain.ChatOutboxFailed, messageID)
		return err
	})
}

// FailSendingOutbox resolves an uncertain startup delivery only while it is
// still unowned. A false result means another process already made the outcome
// terminal, and recovery must not overwrite it or invoke failure callbacks.
func (r *ConversationRepository) FailSendingOutbox(ctx context.Context, id string, cause error) (bool, error) {
	id = strings.TrimSpace(id)
	if id == "" || cause == nil {
		return false, fmt.Errorf("outbox id and failure are required")
	}
	changed := false
	err := r.immediate(ctx, "fail uncertain conversation delivery", func(q assetDBTX, now time.Time) error {
		result, err := q.ExecContext(ctx, `UPDATE chat_outbox SET delivery_status=?,last_error=?,updated_at=?
			WHERE id=? AND delivery_status=?`, domain.ChatOutboxFailed, cause.Error(), now, id, domain.ChatOutboxSending)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil || affected == 0 {
			return err
		}
		changed = true
		_, err = q.ExecContext(ctx, `UPDATE chat_messages SET delivery_status=?
			WHERE id=(SELECT message_id FROM chat_outbox WHERE id=?)`, domain.ChatOutboxFailed, id)
		return err
	})
	return changed, err
}

func (r *ConversationRepository) UpdateSessionStatus(ctx context.Context, id string, status domain.ChatStatus) error {
	result, err := r.db.ExecContext(ctx, `UPDATE chat_sessions SET status=?,updated_at=? WHERE id=?`, status, time.Now().UTC(), id)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return err
	} else if affected != 1 {
		return fmt.Errorf("chat session %q: %w", id, sql.ErrNoRows)
	}
	return nil
}

func (r *ConversationRepository) SetSessionThread(ctx context.Context, id, threadID string) error {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return fmt.Errorf("thread id is required")
	}
	result, err := r.db.ExecContext(ctx, `UPDATE chat_sessions SET codex_thread_id=?,updated_at=? WHERE id=?`, threadID, time.Now().UTC(), id)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return err
	} else if affected != 1 {
		return fmt.Errorf("chat session %q: %w", id, sql.ErrNoRows)
	}
	return nil
}

func (r *ConversationRepository) AcquireThreadLease(ctx context.Context, lease domain.ThreadLease) (bool, error) {
	now := time.Now().UTC()
	lease.ThreadID = strings.TrimSpace(lease.ThreadID)
	lease.SessionID = strings.TrimSpace(lease.SessionID)
	lease.OwnerID = strings.TrimSpace(lease.OwnerID)
	if lease.ThreadID == "" || lease.SessionID == "" || lease.OwnerID == "" {
		return false, fmt.Errorf("lease thread id, session id, and owner id are required")
	}
	if lease.ExpiresAt.IsZero() || !lease.ExpiresAt.After(now) {
		return false, fmt.Errorf("lease expiry must be in the future")
	}
	if lease.CreatedAt.IsZero() {
		lease.CreatedAt = now
	} else if lease.CreatedAt.After(now) {
		return false, fmt.Errorf("lease creation time cannot be in the future")
	}
	lease.UpdatedAt = now
	result, err := r.db.ExecContext(ctx, `INSERT INTO thread_leases(thread_id,session_id,owner_id,expires_at,created_at,updated_at)
        VALUES(?,?,?,?,?,?) ON CONFLICT(thread_id) DO UPDATE SET
        session_id=excluded.session_id,owner_id=excluded.owner_id,expires_at=excluded.expires_at,updated_at=excluded.updated_at
        WHERE thread_leases.expires_at<=? OR thread_leases.owner_id=excluded.owner_id`,
		lease.ThreadID, lease.SessionID, lease.OwnerID, lease.ExpiresAt, lease.CreatedAt, lease.UpdatedAt, now)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

func (r *ConversationRepository) ReleaseThreadLease(ctx context.Context, threadID, ownerID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM thread_leases WHERE thread_id=? AND owner_id=?`, threadID, ownerID)
	return err
}

func (r *ConversationRepository) immediate(ctx context.Context, operation string, fn func(assetDBTX, time.Time) error) (returnErr error) {
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return err
	}
	begun := false
	ended := false
	discard := false
	defer func() {
		if begun && !ended {
			if _, rollbackErr := conn.ExecContext(context.Background(), `ROLLBACK`); rollbackErr != nil {
				returnErr = errors.Join(returnErr, fmt.Errorf("rollback %s: %w", operation, rollbackErr))
				discard = true
			} else {
				ended = true
			}
		}
		if discard {
			if discardErr := conn.Raw(func(any) error { return driver.ErrBadConn }); discardErr != nil && !errors.Is(discardErr, driver.ErrBadConn) {
				returnErr = errors.Join(returnErr, fmt.Errorf("discard connection after uncertain %s transaction: %w", operation, discardErr))
			}
		}
		if closeErr := conn.Close(); closeErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("release connection after %s: %w", operation, closeErr))
		}
	}()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		discard = true
		return fmt.Errorf("begin %s: %w", operation, err)
	}
	begun = true
	if err := fn(conn, time.Now().UTC()); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		discard = true
		return fmt.Errorf("commit %s outcome unknown: %w", operation, err)
	}
	ended = true
	return nil
}

func validDeliveryMode(mode domain.DeliveryMode) bool {
	return mode == domain.DeliveryAuto || mode == domain.DeliverySteer || mode == domain.DeliveryQueue
}

func validTurnStatus(status domain.ChatTurnStatus) bool {
	return status == domain.ChatTurnIdle || status == domain.ChatTurnRunning || status == domain.ChatTurnAwaitingInput || status == domain.ChatTurnCompleted || status == domain.ChatTurnFailed
}

func validTurnTransition(expected, next domain.ChatTurnStatus) bool {
	if !validTurnStatus(expected) || !validTurnStatus(next) || expected == next {
		return false
	}
	switch expected {
	case domain.ChatTurnIdle:
		return next == domain.ChatTurnRunning || next == domain.ChatTurnFailed
	case domain.ChatTurnRunning:
		return next == domain.ChatTurnAwaitingInput || next == domain.ChatTurnCompleted || next == domain.ChatTurnFailed
	case domain.ChatTurnAwaitingInput:
		return next == domain.ChatTurnRunning || next == domain.ChatTurnCompleted || next == domain.ChatTurnFailed
	default:
		return false
	}
}

func validOutboxStatus(status domain.ChatOutboxStatus) bool {
	return status == domain.ChatOutboxPending || status == domain.ChatOutboxSending || status == domain.ChatOutboxAccepted || status == domain.ChatOutboxQueued || status == domain.ChatOutboxFailed
}

func validOutboxTransition(expected, next domain.ChatOutboxStatus) bool {
	if !validOutboxStatus(expected) || !validOutboxStatus(next) || expected == next {
		return false
	}
	switch expected {
	case domain.ChatOutboxPending:
		return next == domain.ChatOutboxSending || next == domain.ChatOutboxQueued || next == domain.ChatOutboxFailed
	case domain.ChatOutboxQueued:
		return next == domain.ChatOutboxSending || next == domain.ChatOutboxFailed
	case domain.ChatOutboxSending:
		return next == domain.ChatOutboxAccepted || next == domain.ChatOutboxQueued || next == domain.ChatOutboxFailed
	case domain.ChatOutboxFailed:
		return next == domain.ChatOutboxPending || next == domain.ChatOutboxQueued
	default:
		return false
	}
}

func scanChatSession(scanner rowScanner) (domain.ChatSession, error) {
	var session domain.ChatSession
	var skillNamesJSON string
	err := scanner.Scan(&session.ID, &session.Title, &session.Source, &session.Kind, &session.Status,
		&session.ProjectID, &session.IdeaSessionID, &session.CodexThreadID, &session.WorkingDirectory,
		&session.Model, &session.ReasoningEffort, &skillNamesJSON, &session.CreatedAt, &session.UpdatedAt)
	if err != nil {
		return session, err
	}
	if err := decodeSkillNames(skillNamesJSON, &session.SkillNames); err != nil {
		return domain.ChatSession{}, fmt.Errorf("chat session %q skill_names_json: %w", session.ID, err)
	}
	return session, nil
}

func decodeSkillNames(raw string, target *[]string) error {
	trimmed := strings.TrimSpace(raw)
	if len(trimmed) < 2 || trimmed[0] != '[' || trimmed[len(trimmed)-1] != ']' {
		return fmt.Errorf("must be a JSON string array")
	}
	if err := json.Unmarshal([]byte(trimmed), target); err != nil {
		return fmt.Errorf("must be a JSON string array: %w", err)
	}
	if *target == nil {
		*target = []string{}
	}
	return nil
}

func getChatOutbox(ctx context.Context, q assetDBTX, sessionID, clientKey string) (domain.ChatOutbox, error) {
	return scanChatOutbox(q.QueryRowContext(ctx, `SELECT id,session_id,message_id,client_key,content,delivery_mode,
        delivery_status,expected_turn_id,codex_turn_id,attempts,available_at,claimed_at,last_error,created_at,updated_at
        FROM chat_outbox WHERE session_id=? AND client_key=?`, sessionID, clientKey))
}

func scanChatOutbox(scanner rowScanner) (domain.ChatOutbox, error) {
	var item domain.ChatOutbox
	err := scanner.Scan(&item.ID, &item.SessionID, &item.MessageID, &item.ClientKey, &item.Content,
		&item.Delivery, &item.DeliveryStatus, &item.ExpectedTurnID, &item.CodexTurnID, &item.Attempts,
		&item.AvailableAt, &item.ClaimedAt, &item.LastError, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}
