package partnergateway

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
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

	return &Store{db: db}, nil
}

func createSchema(db *sql.DB) error {
	const schema = `
CREATE TABLE IF NOT EXISTS schema_migrations (
	version INTEGER PRIMARY KEY,
	applied_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS partners (
	id TEXT PRIMARY KEY,
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
	token_hash BLOB PRIMARY KEY,
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
	var partner Partner
	var createdAt, updatedAt, lastVerifiedAt string
	err := s.db.QueryRowContext(ctx, `
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
`, id).Scan(
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
		return Partner{}, fmt.Errorf("find partner by ID: %w", err)
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

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, value)
}
