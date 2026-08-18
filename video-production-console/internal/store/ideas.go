package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/domain"
)

var ErrIdeaSessionNotFound = errors.New("idea session not found")
var ErrIdeaCandidateNotFound = errors.New("idea candidate not found")
var ErrIdeaSessionBusy = errors.New("idea session has an active task")

type IdeaRepository struct{ db *sql.DB }

func NewIdeaRepository(db *sql.DB) *IdeaRepository { return &IdeaRepository{db: db} }
func (r *IdeaRepository) DB() *sql.DB              { return r.db }

func (r *IdeaRepository) CreateSession(ctx context.Context, session domain.IdeaSession) error {
	if _, err := uuid.Parse(session.ID); err != nil {
		return fmt.Errorf("invalid idea session id: %w", err)
	}
	if session.CreatedAt.IsZero() {
		session.CreatedAt = time.Now().UTC()
	}
	if session.UpdatedAt.IsZero() {
		session.UpdatedAt = session.CreatedAt
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO idea_sessions(id,account_id,title,status,created_at,updated_at) VALUES(?,?,?,?,?,?)`, session.ID, nullableStringPtr(session.AccountID), session.Title, session.Status, session.CreatedAt, session.UpdatedAt)
	return err
}

func (r *IdeaRepository) ListSessions(ctx context.Context) ([]domain.IdeaSession, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id,account_id,title,status,selected_id,topic_card_path,topic_card_state,topic_card_sha256,project_id,created_at,updated_at
		FROM idea_sessions
		WHERE selected_id IS NOT NULL OR project_id IS NOT NULL
		   OR EXISTS (SELECT 1 FROM idea_messages m WHERE m.session_id=idea_sessions.id)
		   OR EXISTS (SELECT 1 FROM idea_candidates c WHERE c.session_id=idea_sessions.id)
		ORDER BY updated_at DESC,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.IdeaSession, 0)
	for rows.Next() {
		session, err := scanIdeaSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, session)
	}
	return out, rows.Err()
}

func (r *IdeaRepository) GetSession(ctx context.Context, id string) (domain.IdeaSession, error) {
	session, err := scanIdeaSession(r.db.QueryRowContext(ctx, `SELECT id,account_id,title,status,selected_id,topic_card_path,topic_card_state,topic_card_sha256,project_id,created_at,updated_at FROM idea_sessions WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.IdeaSession{}, ErrIdeaSessionNotFound
	}
	return session, err
}

func (r *IdeaRepository) DeleteSession(ctx context.Context, id string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM idea_sessions WHERE id=?`, id).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return ErrIdeaSessionNotFound
	} else if err != nil {
		return err
	}
	var active int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM idea_messages m
		JOIN codex_tasks t ON t.id=m.task_id
		WHERE m.session_id=? AND t.status IN ('queued','running','awaiting_input','resuming','waiting_input')`, id).Scan(&active); err != nil {
		return err
	}
	if active > 0 {
		return ErrIdeaSessionBusy
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM idea_sessions WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *IdeaRepository) AddMessage(ctx context.Context, message domain.IdeaMessage) error {
	if message.ID == "" {
		message.ID = uuid.NewString()
	}
	if message.CreatedAt.IsZero() {
		message.CreatedAt = time.Now().UTC()
	}
	result, err := r.db.ExecContext(ctx, `INSERT INTO idea_messages(id,session_id,task_id,role,content,created_at) VALUES(?,?,?,?,?,?)`, message.ID, message.SessionID, nullableStringPtr(message.TaskID), message.Role, message.Content, message.CreatedAt)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrIdeaSessionNotFound
	}
	_, err = r.db.ExecContext(ctx, `UPDATE idea_sessions SET updated_at=? WHERE id=?`, message.CreatedAt, message.SessionID)
	return err
}

func (r *IdeaRepository) Messages(ctx context.Context, id string) ([]domain.IdeaMessage, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,session_id,task_id,role,content,created_at FROM idea_messages WHERE session_id=? ORDER BY created_at,id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.IdeaMessage
	for rows.Next() {
		var m domain.IdeaMessage
		var task sql.NullString
		if err := rows.Scan(&m.ID, &m.SessionID, &task, &m.Role, &m.Content, &m.CreatedAt); err != nil {
			return nil, err
		}
		if task.Valid {
			m.TaskID = &task.String
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *IdeaRepository) Candidates(ctx context.Context, id string) ([]domain.IdeaCandidate, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,session_id,task_id,position,title,summary,score,source,selected,created_at FROM idea_candidates WHERE session_id=? ORDER BY position,id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.IdeaCandidate
	for rows.Next() {
		var c domain.IdeaCandidate
		var task sql.NullString
		var selected int
		if err := rows.Scan(&c.ID, &c.SessionID, &task, &c.Position, &c.Title, &c.Summary, &c.Score, &c.Source, &selected, &c.CreatedAt); err != nil {
			return nil, err
		}
		if task.Valid {
			c.TaskID = &task.String
		}
		c.Selected = selected != 0
		out = append(out, c)
	}
	return out, rows.Err()
}

// ReplaceCandidates replaces the brainstorm candidates for a session. The
// task_id/session_id pair is checked by the foreign keys in the same
// transaction, so stale task output cannot populate an unrelated session.
func (r *IdeaRepository) ReplaceCandidates(ctx context.Context, sessionID, taskID string, candidates []domain.IdeaCandidate) error {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(taskID) == "" {
		return fmt.Errorf("session and task ids are required")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM idea_sessions WHERE id=? AND EXISTS (SELECT 1 FROM idea_messages WHERE session_id=idea_sessions.id AND task_id=?)`, sessionID, taskID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return ErrIdeaSessionNotFound
	} else if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM idea_candidates WHERE session_id=?`, sessionID); err != nil {
		return err
	}
	now := time.Now().UTC()
	for i, candidate := range candidates {
		created := candidate.CreatedAt
		if created.IsZero() {
			created = now
		}
		if strings.TrimSpace(candidate.ID) == "" || strings.TrimSpace(candidate.Title) == "" {
			return fmt.Errorf("candidate %d is missing id or title", i)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO idea_candidates(id,session_id,task_id,position,title,summary,score,source,selected,created_at) VALUES(?,?,?,?,?,?,?,?,0,?)`, candidate.ID, sessionID, taskID, i+1, candidate.Title, candidate.Summary, candidate.Score, candidate.Source, created); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE idea_sessions SET status='planning',updated_at=? WHERE id=?`, now, sessionID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (r *IdeaRepository) SelectCandidate(ctx context.Context, sessionID, candidateID string) (domain.IdeaCandidate, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.IdeaCandidate{}, err
	}
	defer tx.Rollback()
	var c domain.IdeaCandidate
	var task sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT id,session_id,task_id,position,title,summary,score,source,created_at FROM idea_candidates WHERE id=? AND session_id=?`, candidateID, sessionID).Scan(&c.ID, &c.SessionID, &task, &c.Position, &c.Title, &c.Summary, &c.Score, &c.Source, &c.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.IdeaCandidate{}, ErrIdeaCandidateNotFound
	}
	if err != nil {
		return domain.IdeaCandidate{}, err
	}
	if task.Valid {
		c.TaskID = &task.String
	}
	if _, err = tx.ExecContext(ctx, `UPDATE idea_candidates SET selected=CASE WHEN id=? THEN 1 ELSE 0 END WHERE session_id=?`, candidateID, sessionID); err != nil {
		return domain.IdeaCandidate{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE idea_sessions SET selected_id=?,status='selected',updated_at=? WHERE id=?`, candidateID, time.Now().UTC(), sessionID); err != nil {
		return domain.IdeaCandidate{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.IdeaCandidate{}, err
	}
	c.Selected = true
	return c, nil
}

// LinkProject records the concrete video project created from a confirmed
// candidate. The explicit account is also persisted so later confirmations
// use the account selected at confirmation time rather than a stale planner
// filter.
func (r *IdeaRepository) LinkProject(ctx context.Context, sessionID, projectID, accountID string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE idea_sessions SET project_id=?,account_id=?,updated_at=? WHERE id=?`, projectID, accountID, time.Now().UTC(), sessionID)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return err
	} else if affected != 1 {
		return ErrIdeaSessionNotFound
	}
	return nil
}

// UpdateAccount persists the account explicitly chosen in the topic planner.
// This keeps later messages and candidate confirmation on the same account
// even when the global sidebar filter changes or the session is refreshed.
func (r *IdeaRepository) UpdateAccount(ctx context.Context, sessionID, accountID string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE idea_sessions SET account_id=?,updated_at=? WHERE id=?`, accountID, time.Now().UTC(), sessionID)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return err
	} else if affected != 1 {
		return ErrIdeaSessionNotFound
	}
	return nil
}

func scanIdeaSession(row interface{ Scan(...any) error }) (domain.IdeaSession, error) {
	var s domain.IdeaSession
	var account, selected, card, state, sha, project sql.NullString
	err := row.Scan(&s.ID, &account, &s.Title, &s.Status, &selected, &card, &state, &sha, &project, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return s, err
	}
	if account.Valid {
		s.AccountID = &account.String
	}
	if selected.Valid {
		s.SelectedID = &selected.String
	}
	if card.Valid {
		s.TopicCardPath = &card.String
	}
	if state.Valid {
		s.TopicCardState = &state.String
	}
	if sha.Valid {
		s.TopicCardSHA256 = &sha.String
	}
	if project.Valid {
		s.ProjectID = &project.String
	}
	return s, nil
}

func nullableStringPtr(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}
