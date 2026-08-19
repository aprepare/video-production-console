package partnergateway

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"video-production-console/internal/security"

	_ "modernc.org/sqlite"
)

type Store struct {
	db    *sql.DB
	clock func() time.Time
}

func OpenStore(path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("database path is required")
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve database path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}

	query := url.Values{}
	query.Add("_pragma", "foreign_keys(ON)")
	query.Add("_pragma", "busy_timeout(5000)")
	dsnPath := filepath.ToSlash(absPath)
	if !strings.HasPrefix(dsnPath, "/") {
		dsnPath = "/" + dsnPath
	}
	dsn := (&url.URL{
		Scheme:   "file",
		Path:     dsnPath,
		RawQuery: query.Encode(),
	}).String()

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	db.SetMaxOpenConns(1)
	closeWithError := func(err error) (*Store, error) {
		_ = db.Close()
		return nil, err
	}

	if err := db.Ping(); err != nil {
		return closeWithError(fmt.Errorf("connect sqlite database: %w", err))
	}
	var journalMode string
	if err := db.QueryRow(`PRAGMA journal_mode=WAL`).Scan(&journalMode); err != nil {
		return closeWithError(fmt.Errorf("enable sqlite WAL mode: %w", err))
	}
	if !strings.EqualFold(journalMode, "wal") {
		return closeWithError(fmt.Errorf("enable sqlite WAL mode: got %q", journalMode))
	}
	if err := createSchema(db); err != nil {
		return closeWithError(err)
	}

	return &Store{db: db, clock: time.Now}, nil
}

func createSchema(db *sql.DB) error {
	const schema = `
CREATE TABLE IF NOT EXISTS schema_migrations (
	version INTEGER PRIMARY KEY,
	applied_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS partners (
	id TEXT PRIMARY KEY NOT NULL,
	display_name TEXT NOT NULL UNIQUE,
	key_prefix TEXT NOT NULL UNIQUE,
	key_hash BLOB NOT NULL,
	status TEXT NOT NULL CHECK (status IN ('active', 'disabled')),
	device_hash TEXT NOT NULL,
	device_secret_hash BLOB,
	session_version INTEGER NOT NULL,
	text_calls INTEGER NOT NULL,
	image_calls INTEGER NOT NULL,
	verify_failures INTEGER NOT NULL,
	rate_limited INTEGER NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	last_verified_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
	token_hash BLOB PRIMARY KEY NOT NULL,
	partner_id TEXT NOT NULL,
	session_version INTEGER NOT NULL,
	expires_at TEXT NOT NULL,
	created_at TEXT NOT NULL,
	FOREIGN KEY (partner_id) REFERENCES partners(id) ON DELETE CASCADE
);
`
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("create partner gateway schema: %w", err)
	}
	if _, err := db.Exec(
		`INSERT OR IGNORE INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
		1,
		time.Now().UTC().Format(time.RFC3339Nano),
	); err != nil {
		return fmt.Errorf("record partner gateway schema: %w", err)
	}
	return nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) CreatePartner(ctx context.Context, partner Partner) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO partners (
	id,
	display_name,
	key_prefix,
	key_hash,
	status,
	device_hash,
	device_secret_hash,
	session_version,
	text_calls,
	image_calls,
	verify_failures,
	rate_limited,
	created_at,
	updated_at,
	last_verified_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`,
		partner.ID,
		partner.DisplayName,
		partner.KeyPrefix,
		partner.KeyHash,
		partner.Status,
		partner.DeviceHash,
		partner.DeviceSecretHash,
		partner.SessionVersion,
		partner.TextCalls,
		partner.ImageCalls,
		partner.VerifyFailures,
		partner.RateLimited,
		formatTime(partner.CreatedAt),
		formatTime(partner.UpdatedAt),
		formatTime(partner.LastVerifiedAt),
	)
	if err != nil {
		return fmt.Errorf("create partner: %w", err)
	}
	return nil
}

func (s *Store) PartnerByID(ctx context.Context, id string) (Partner, error) {
	partner, err := scanPartner(s.db.QueryRowContext(ctx, `
SELECT
	id,
	display_name,
	key_prefix,
	key_hash,
	status,
	device_hash,
	device_secret_hash,
	session_version,
	text_calls,
	image_calls,
	verify_failures,
	rate_limited,
	created_at,
	updated_at,
	last_verified_at
FROM partners
WHERE id = ?
`, id))
	if err != nil {
		return Partner{}, fmt.Errorf("find partner by ID: %w", err)
	}
	return partner, nil
}

func (s *Store) activatePartner(
	ctx context.Context,
	keyPrefix string,
	deviceHash string,
	deviceSecretHash []byte,
	session Session,
	now time.Time,
	keyMatches func([]byte) bool,
) (Partner, error) {
	var partner Partner
	err := s.withImmediateTransaction(ctx, func(conn *sql.Conn) error {
		var err error
		partner, err = scanPartner(conn.QueryRowContext(ctx, `
SELECT
	id,
	display_name,
	key_prefix,
	key_hash,
	status,
	device_hash,
	device_secret_hash,
	session_version,
	text_calls,
	image_calls,
	verify_failures,
	rate_limited,
	created_at,
	updated_at,
	last_verified_at
FROM partners
WHERE key_prefix = ?
`, keyPrefix))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvalidCredential
		}
		if err != nil {
			return fmt.Errorf("find partner by activation key prefix: %w", err)
		}
		if !keyMatches(partner.KeyHash) {
			return ErrInvalidCredential
		}
		if partner.Status == PartnerDisabled {
			return ErrPartnerDisabled
		}
		if partner.DeviceHash != "" && partner.DeviceHash != deviceHash {
			return ErrDeviceMismatch
		}

		if _, err := conn.ExecContext(ctx, `
UPDATE partners
SET device_hash = ?, device_secret_hash = ?, updated_at = ?, last_verified_at = ?
WHERE id = ?
`, deviceHash, deviceSecretHash, formatTime(now), formatTime(now), partner.ID); err != nil {
			return fmt.Errorf("bind partner device: %w", err)
		}
		session.PartnerID = partner.ID
		session.SessionVersion = partner.SessionVersion
		if err := insertSession(ctx, conn, session); err != nil {
			return err
		}
		partner.DeviceHash = deviceHash
		partner.DeviceSecretHash = append([]byte(nil), deviceSecretHash...)
		partner.UpdatedAt = now
		partner.LastVerifiedAt = now
		return nil
	})
	if err != nil {
		return Partner{}, err
	}
	return partner, nil
}

func (s *Store) verifyPartner(
	ctx context.Context,
	partnerID string,
	deviceHash string,
	session Session,
	now time.Time,
	deviceSecretMatches func([]byte) bool,
) (Partner, error) {
	var partner Partner
	err := s.withImmediateTransaction(ctx, func(conn *sql.Conn) error {
		var err error
		partner, err = scanPartner(conn.QueryRowContext(ctx, `
SELECT
	id,
	display_name,
	key_prefix,
	key_hash,
	status,
	device_hash,
	device_secret_hash,
	session_version,
	text_calls,
	image_calls,
	verify_failures,
	rate_limited,
	created_at,
	updated_at,
	last_verified_at
FROM partners
WHERE id = ?
`, partnerID))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvalidCredential
		}
		if err != nil {
			return fmt.Errorf("find partner for verification: %w", err)
		}
		if partner.Status == PartnerDisabled {
			return ErrPartnerDisabled
		}
		if partner.DeviceHash == "" ||
			partner.DeviceHash != deviceHash ||
			!deviceSecretMatches(partner.DeviceSecretHash) {
			return ErrInvalidCredential
		}

		if _, err := conn.ExecContext(ctx, `
UPDATE partners
SET updated_at = ?, last_verified_at = ?
WHERE id = ?
`, formatTime(now), formatTime(now), partner.ID); err != nil {
			return fmt.Errorf("record partner verification: %w", err)
		}
		session.PartnerID = partner.ID
		session.SessionVersion = partner.SessionVersion
		if err := insertSession(ctx, conn, session); err != nil {
			return err
		}
		partner.UpdatedAt = now
		partner.LastVerifiedAt = now
		return nil
	})
	if err != nil {
		return Partner{}, err
	}
	return partner, nil
}

func (s *Store) SessionPartner(ctx context.Context, plaintextToken string) (Partner, error) {
	if plaintextToken == "" {
		return Partner{}, ErrInvalidCredential
	}
	var partnerID, expiresAt string
	var sessionVersion int64
	err := s.db.QueryRowContext(ctx, `
SELECT partner_id, session_version, expires_at
FROM sessions
WHERE token_hash = ?
`, []byte(security.HashSecret(plaintextToken))).Scan(&partnerID, &sessionVersion, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Partner{}, ErrInvalidCredential
	}
	if err != nil {
		return Partner{}, fmt.Errorf("find session: %w", err)
	}
	expiration, err := parseTime(expiresAt)
	if err != nil {
		return Partner{}, fmt.Errorf("parse session expires_at: %w", err)
	}
	clock := s.clock
	if clock == nil {
		clock = time.Now
	}
	if !expiration.After(clock().UTC()) {
		return Partner{}, ErrSessionExpired
	}
	partner, err := s.PartnerByID(ctx, partnerID)
	if errors.Is(err, sql.ErrNoRows) {
		return Partner{}, ErrInvalidCredential
	}
	if err != nil {
		return Partner{}, err
	}
	if partner.Status == PartnerDisabled {
		return Partner{}, ErrPartnerDisabled
	}
	if partner.SessionVersion != sessionVersion {
		return Partner{}, ErrInvalidCredential
	}
	return partner, nil
}

func (s *Store) setPartnerStatus(ctx context.Context, partnerID string, status PartnerStatus, now time.Time) error {
	return s.withImmediateTransaction(ctx, func(conn *sql.Conn) error {
		result, err := conn.ExecContext(ctx, `
UPDATE partners
SET status = ?, session_version = session_version + 1, updated_at = ?
WHERE id = ?
`, status, formatTime(now), partnerID)
		if err != nil {
			return fmt.Errorf("set partner status: %w", err)
		}
		if err := requireAffectedPartner(result); err != nil {
			return err
		}
		return deletePartnerSessions(ctx, conn, partnerID)
	})
}

func (s *Store) rotatePartnerKey(
	ctx context.Context,
	partnerID string,
	keyPrefix string,
	keyHash []byte,
	now time.Time,
) error {
	return s.withImmediateTransaction(ctx, func(conn *sql.Conn) error {
		result, err := conn.ExecContext(ctx, `
UPDATE partners
SET key_prefix = ?, key_hash = ?, session_version = session_version + 1, updated_at = ?
WHERE id = ?
`, keyPrefix, keyHash, formatTime(now), partnerID)
		if err != nil {
			return fmt.Errorf("rotate partner key: %w", err)
		}
		if err := requireAffectedPartner(result); err != nil {
			return err
		}
		return deletePartnerSessions(ctx, conn, partnerID)
	})
}

func (s *Store) unbindDevice(ctx context.Context, partnerID string, now time.Time) error {
	return s.withImmediateTransaction(ctx, func(conn *sql.Conn) error {
		result, err := conn.ExecContext(ctx, `
UPDATE partners
SET device_hash = '', device_secret_hash = NULL, session_version = session_version + 1, updated_at = ?
WHERE id = ?
`, formatTime(now), partnerID)
		if err != nil {
			return fmt.Errorf("unbind partner device: %w", err)
		}
		if err := requireAffectedPartner(result); err != nil {
			return err
		}
		return deletePartnerSessions(ctx, conn, partnerID)
	})
}

func (s *Store) ListPartners(ctx context.Context) ([]Partner, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT
	id,
	display_name,
	key_prefix,
	key_hash,
	status,
	device_hash,
	device_secret_hash,
	session_version,
	text_calls,
	image_calls,
	verify_failures,
	rate_limited,
	created_at,
	updated_at,
	last_verified_at
FROM partners
ORDER BY created_at, id
`)
	if err != nil {
		return nil, fmt.Errorf("list partners: %w", err)
	}
	defer rows.Close()

	partners := make([]Partner, 0)
	for rows.Next() {
		partner, err := scanPartner(rows)
		if err != nil {
			return nil, fmt.Errorf("scan listed partner: %w", err)
		}
		partners = append(partners, partner)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate partners: %w", err)
	}
	return partners, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanPartner(row rowScanner) (Partner, error) {
	var partner Partner
	var createdAt, updatedAt, lastVerifiedAt string
	err := row.Scan(
		&partner.ID,
		&partner.DisplayName,
		&partner.KeyPrefix,
		&partner.KeyHash,
		&partner.Status,
		&partner.DeviceHash,
		&partner.DeviceSecretHash,
		&partner.SessionVersion,
		&partner.TextCalls,
		&partner.ImageCalls,
		&partner.VerifyFailures,
		&partner.RateLimited,
		&createdAt,
		&updatedAt,
		&lastVerifiedAt,
	)
	if err != nil {
		return Partner{}, err
	}

	partner.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return Partner{}, fmt.Errorf("parse partner created_at: %w", err)
	}
	partner.UpdatedAt, err = parseTime(updatedAt)
	if err != nil {
		return Partner{}, fmt.Errorf("parse partner updated_at: %w", err)
	}
	partner.LastVerifiedAt, err = parseTime(lastVerifiedAt)
	if err != nil {
		return Partner{}, fmt.Errorf("parse partner last_verified_at: %w", err)
	}
	return partner, nil
}

func insertSession(ctx context.Context, conn *sql.Conn, session Session) error {
	_, err := conn.ExecContext(ctx, `
INSERT INTO sessions (token_hash, partner_id, session_version, expires_at, created_at)
VALUES (?, ?, ?, ?, ?)
`,
		session.TokenHash,
		session.PartnerID,
		session.SessionVersion,
		formatTime(session.ExpiresAt),
		formatTime(session.CreatedAt),
	)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

func deletePartnerSessions(ctx context.Context, conn *sql.Conn, partnerID string) error {
	if _, err := conn.ExecContext(ctx, `DELETE FROM sessions WHERE partner_id = ?`, partnerID); err != nil {
		return fmt.Errorf("delete partner sessions: %w", err)
	}
	return nil
}

func requireAffectedPartner(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read affected partner count: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("partner not found: %w", sql.ErrNoRows)
	}
	return nil
}

func (s *Store) withImmediateTransaction(ctx context.Context, operation func(*sql.Conn) error) (err error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire transaction connection: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return fmt.Errorf("begin immediate transaction: %w", err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		if _, rollbackErr := conn.ExecContext(context.Background(), `ROLLBACK`); rollbackErr != nil && err == nil {
			err = fmt.Errorf("rollback immediate transaction: %w", rollbackErr)
		}
	}()

	if err := operation(conn); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("commit immediate transaction: %w", err)
	}
	committed = true
	return nil
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, value)
}
