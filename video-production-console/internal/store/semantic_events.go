package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/domain"
)

type SemanticWrite struct{ SessionID, TaskID, Kind, Phase, Level, Title, Detail string }

func (r *ConversationRepository) AppendSemantic(ctx context.Context, write SemanticWrite) (domain.SemanticEvent, error) {
	if (strings.TrimSpace(write.SessionID) == "") == (strings.TrimSpace(write.TaskID) == "") {
		return domain.SemanticEvent{}, fmt.Errorf("semantic event requires exactly one session or task")
	}
	if strings.TrimSpace(write.Kind) == "" || strings.TrimSpace(write.Phase) == "" || strings.TrimSpace(write.Title) == "" {
		return domain.SemanticEvent{}, fmt.Errorf("semantic event kind, phase, and title are required")
	}
	if write.Level == "" {
		write.Level = "info"
	}
	event := domain.SemanticEvent{ID: uuid.NewString(), Sequence: 0, Kind: write.Kind, Phase: write.Phase, Level: write.Level, Title: write.Title, Detail: write.Detail, RawJSON: ""}
	err := r.immediate(ctx, "append semantic event", func(q assetDBTX, now time.Time) error {
		event.CreatedAt = now
		if write.SessionID != "" {
			event.SessionID = &write.SessionID
			if err := q.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM semantic_events WHERE session_id=?`, write.SessionID).Scan(&event.Sequence); err != nil {
				return err
			}
		} else {
			event.TaskID = &write.TaskID
			if err := q.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM semantic_events WHERE task_id=?`, write.TaskID).Scan(&event.Sequence); err != nil {
				return err
			}
		}
		_, err := q.ExecContext(ctx, `INSERT INTO semantic_events(id,session_id,task_id,sequence,kind,phase,level,title,detail,raw_json,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, event.ID, event.SessionID, event.TaskID, event.Sequence, event.Kind, event.Phase, event.Level, event.Title, event.Detail, event.RawJSON, event.CreatedAt)
		return err
	})
	return event, err
}
func (r *ConversationRepository) SemanticForTask(ctx context.Context, taskID string, after int64, limit int) ([]domain.SemanticEvent, error) {
	if limit < 1 {
		limit = 100
	}
	if limit > 100 {
		limit = 100
	}
	return r.semantic(ctx, `task_id=?`, taskID, after, limit)
}
func (r *ConversationRepository) SemanticForSession(ctx context.Context, sessionID string, after int64, limit int) ([]domain.SemanticEvent, error) {
	if limit < 1 {
		limit = 100
	}
	if limit > 100 {
		limit = 100
	}
	return r.semantic(ctx, `session_id=?`, sessionID, after, limit)
}
func (r *ConversationRepository) semantic(ctx context.Context, where, id string, after int64, limit int) ([]domain.SemanticEvent, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,session_id,task_id,sequence,kind,phase,level,title,detail,raw_json,created_at FROM semantic_events WHERE `+where+` AND sequence>? ORDER BY sequence LIMIT ?`, id, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.SemanticEvent{}
	for rows.Next() {
		var event domain.SemanticEvent
		if err := rows.Scan(&event.ID, &event.SessionID, &event.TaskID, &event.Sequence, &event.Kind, &event.Phase, &event.Level, &event.Title, &event.Detail, &event.RawJSON, &event.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	return out, rows.Err()
}

var _ = sql.ErrNoRows
