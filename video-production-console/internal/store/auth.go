package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

var ErrUnauthenticated = errors.New("unauthenticated")

type Admin struct {
	ID                string
	PasswordHash      string
	PasswordChangedAt time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type AuthSession struct {
	ID                     string
	AdminID                string
	TokenHash              string
	CSRFHash               string
	RemoteAddr             string
	ExpiresAt              time.Time
	LastSeenAt             time.Time
	CreatedAt              time.Time
	InitialPasswordWarning bool
}

type AuthStore struct{ db *sql.DB }

func NewAuthStore(db *sql.DB) *AuthStore { return &AuthStore{db: db} }

func (s *AuthStore) CreateAdminIfNone(ctx context.Context, passwordHash string, now time.Time) (Admin, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Admin{}, false, fmt.Errorf("begin admin bootstrap: %w", err)
	}
	defer tx.Rollback()
	admin, err := scanAdmin(tx.QueryRowContext(ctx, `SELECT id,password_hash,password_changed_at,created_at,updated_at FROM admins LIMIT 1`))
	if err == nil {
		if err := tx.Commit(); err != nil {
			return Admin{}, false, fmt.Errorf("commit admin bootstrap: %w", err)
		}
		return admin, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Admin{}, false, fmt.Errorf("read administrator: %w", err)
	}
	admin = Admin{ID: uuid.NewString(), PasswordHash: passwordHash, PasswordChangedAt: now, CreatedAt: now, UpdatedAt: now}
	_, err = tx.ExecContext(ctx, `INSERT INTO admins(id,password_hash,password_changed_at,created_at,updated_at) VALUES(?,?,?,?,?)`, admin.ID, admin.PasswordHash, now, now, now)
	if err != nil {
		return Admin{}, false, fmt.Errorf("create administrator: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Admin{}, false, fmt.Errorf("commit administrator: %w", err)
	}
	return admin, true, nil
}

func (s *AuthStore) Admin(ctx context.Context) (Admin, error) {
	admin, err := scanAdmin(s.db.QueryRowContext(ctx, `SELECT id,password_hash,password_changed_at,created_at,updated_at FROM admins LIMIT 1`))
	if errors.Is(err, sql.ErrNoRows) {
		return Admin{}, ErrUnauthenticated
	}
	return admin, err
}

type rowScanner interface{ Scan(...any) error }

func scanAdmin(row rowScanner) (Admin, error) {
	var a Admin
	err := row.Scan(&a.ID, &a.PasswordHash, &a.PasswordChangedAt, &a.CreatedAt, &a.UpdatedAt)
	return a, err
}

func (s *AuthStore) CreateSession(ctx context.Context, session AuthSession) error {
	if _, err := uuid.Parse(session.ID); err != nil {
		return fmt.Errorf("invalid session UUID: %w", err)
	}
	if _, err := uuid.Parse(session.AdminID); err != nil {
		return fmt.Errorf("invalid admin UUID: %w", err)
	}
	if !validSHA256Hash(session.TokenHash) || !validSHA256Hash(session.CSRFHash) {
		return errors.New("session hashes must encode SHA-256 values")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO auth_sessions(id,admin_id,token_hash,csrf_hash,remote_addr,expires_at,last_seen_at,created_at) VALUES(?,?,?,?,?,?,?,?)`, session.ID, session.AdminID, session.TokenHash, session.CSRFHash, session.RemoteAddr, session.ExpiresAt, session.LastSeenAt, session.CreatedAt)
	if err != nil {
		return fmt.Errorf("create auth session: %w", err)
	}
	return nil
}

func validSHA256Hash(value string) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(decoded) == 32
}

func (s *AuthStore) FindValidSession(ctx context.Context, tokenHash string, now time.Time) (AuthSession, error) {
	var session AuthSession
	err := s.db.QueryRowContext(ctx, `SELECT s.id,s.admin_id,s.token_hash,s.csrf_hash,s.remote_addr,s.expires_at,s.last_seen_at,s.created_at,a.updated_at=a.created_at FROM auth_sessions s JOIN admins a ON a.id=s.admin_id WHERE s.token_hash=? AND s.expires_at>? AND s.created_at>=a.password_changed_at`, tokenHash, now).Scan(&session.ID, &session.AdminID, &session.TokenHash, &session.CSRFHash, &session.RemoteAddr, &session.ExpiresAt, &session.LastSeenAt, &session.CreatedAt, &session.InitialPasswordWarning)
	if errors.Is(err, sql.ErrNoRows) {
		return AuthSession{}, ErrUnauthenticated
	}
	if err != nil {
		return AuthSession{}, fmt.Errorf("find auth session: %w", err)
	}
	return session, nil
}

func (s *AuthStore) RotateCSRF(ctx context.Context, sessionID, currentHash, csrfHash string) error {
	if _, err := uuid.Parse(sessionID); err != nil {
		return fmt.Errorf("invalid session UUID: %w", err)
	}
	if !validSHA256Hash(currentHash) || !validSHA256Hash(csrfHash) {
		return errors.New("CSRF hash must encode a SHA-256 value")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE auth_sessions SET csrf_hash=? WHERE id=? AND csrf_hash=?`, csrfHash, sessionID, currentHash)
	if err != nil {
		return fmt.Errorf("rotate session CSRF: %w", err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return ErrUnauthenticated
	}
	return nil
}

func (s *AuthStore) RevokeSession(ctx context.Context, sessionID string) error {
	if _, err := uuid.Parse(sessionID); err != nil {
		return fmt.Errorf("invalid session UUID: %w", err)
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM auth_sessions WHERE id=?`, sessionID)
	if err != nil {
		return fmt.Errorf("revoke auth session: %w", err)
	}
	return nil
}

func (s *AuthStore) RevokeAll(ctx context.Context, adminID string) error {
	if _, err := uuid.Parse(adminID); err != nil {
		return fmt.Errorf("invalid admin UUID: %w", err)
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM auth_sessions WHERE admin_id=?`, adminID)
	if err != nil {
		return fmt.Errorf("revoke auth sessions: %w", err)
	}
	return nil
}

func (s *AuthStore) ChangePasswordAndRevokeAll(ctx context.Context, adminID, currentHash, passwordHash string, changedAt time.Time) error {
	if _, err := uuid.Parse(adminID); err != nil {
		return fmt.Errorf("invalid admin UUID: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin password change: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE admins SET password_hash=?,password_changed_at=?,updated_at=? WHERE id=? AND password_hash=?`, passwordHash, changedAt, changedAt.Add(time.Nanosecond), adminID, currentHash)
	if err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return ErrUnauthenticated
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM auth_sessions WHERE admin_id=?`, adminID); err != nil {
		return fmt.Errorf("revoke sessions after password change: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit password change: %w", err)
	}
	return nil
}
