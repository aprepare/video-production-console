package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

const settingsVersionKey = "settings_version"

var (
	ErrReservedSettingKey = errors.New("reserved setting key")
	ErrUnknownSettingKey  = errors.New("unknown setting key")
	ErrInvalidCiphertext  = errors.New("encrypted secret must be base64 ciphertext")
	ErrSecretNotFound     = errors.New("encrypted secret not found")
)

var publicSettingKeys = map[string]struct{}{
	"listen_addr": {}, "data_root": {}, "max_codex_concurrency": {},
	"codex_default_model": {}, "codex_default_reasoning_effort": {},
	"baokuan_base_url": {}, "baokuan_mcp_executable": {},
	"obsidian_vault": {}, "topic_cards_dir": {},
	"grok_base_url": {}, "grok_model": {}, "codex_binary_path": {},
	"image_base_url": {}, "image_model": {}, "max_image_concurrency": {},
	"default_image_ratio": {}, "default_image_style": {},
	"media_index_path": {}, "media_root": {}, "jianying_root": {},
	"machine_profile_path": {}, "app_server_enabled": {}, "codex_workspace_roots": {}, "codex_task_project_root": {},
	"volc_speech_speaker_id": {}, "volc_speech_resource_id": {},
}

type EncryptedSecret struct {
	Key        string
	Ciphertext string
	Version    int64
	UpdatedAt  time.Time
}

type SettingsRepository struct{ db *sql.DB }

func NewSettingsRepository(db *sql.DB) *SettingsRepository {
	return &SettingsRepository{db: db}
}

// InitializeBoot inserts machine-derived settings without replacing values a
// user has already stored. The legacy migration seeded the unresolved command
// name "codex"; before the first versioned settings update only, it is safe to
// replace that bootstrap placeholder with the resolved absolute binary path.
func (r *SettingsRepository) InitializeBoot(ctx context.Context, values map[string]string) (returnErr error) {
	keys := make([]string, 0, len(values))
	for key := range values {
		if _, ok := publicSettingKeys[key]; !ok || key == settingsVersionKey {
			return ErrUnknownSettingKey
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire boot settings connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return fmt.Errorf("begin boot settings initialization: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			if _, err := conn.ExecContext(context.Background(), `ROLLBACK`); err != nil {
				returnErr = errors.Join(returnErr, fmt.Errorf("roll back boot settings: %w", err))
			}
		}
	}()
	for _, key := range keys {
		if _, err := conn.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO NOTHING`, key, values[key]); err != nil {
			return fmt.Errorf("initialize boot setting: %w", err)
		}
	}
	if value := values["codex_binary_path"]; value != "" {
		if _, err := conn.ExecContext(ctx, `UPDATE settings SET value=?
			WHERE key='codex_binary_path' AND value='codex'
			AND NOT EXISTS(SELECT 1 FROM settings WHERE key=?)`, value, settingsVersionKey); err != nil {
			return fmt.Errorf("resolve legacy Codex bootstrap path: %w", err)
		}
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("commit boot settings initialization: %w", err)
	}
	committed = true
	return nil
}

func (r *SettingsRepository) Public(ctx context.Context) (map[string]string, int64, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT key,value FROM settings ORDER BY key`)
	if err != nil {
		return nil, 0, fmt.Errorf("read public settings: %w", err)
	}
	defer rows.Close()
	values := make(map[string]string)
	var version int64
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, 0, fmt.Errorf("read public setting: %w", err)
		}
		if key == settingsVersionKey {
			version, err = strconv.ParseInt(value, 10, 64)
			if err != nil || version < 0 {
				return nil, 0, errors.New("stored settings version is invalid")
			}
			continue
		}
		values[key] = value
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate public settings: %w", err)
	}
	return values, version, nil
}

// UpdatePublic writes the whole provided batch and the next settings version
// under the same SQLite reserved lock.
func (r *SettingsRepository) UpdatePublic(ctx context.Context, values map[string]string) (version int64, returnErr error) {
	keys := make([]string, 0, len(values))
	for key := range values {
		if key == settingsVersionKey {
			return 0, ErrReservedSettingKey
		}
		if _, ok := publicSettingKeys[key]; !ok {
			return 0, ErrUnknownSettingKey
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return 0, fmt.Errorf("acquire settings connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return 0, fmt.Errorf("begin public settings update: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			if _, err := conn.ExecContext(context.Background(), `ROLLBACK`); err != nil {
				returnErr = errors.Join(returnErr, fmt.Errorf("roll back public settings: %w", err))
			}
		}
	}()
	var rawVersion string
	err = conn.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, settingsVersionKey).Scan(&rawVersion)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		version = 0
	case err != nil:
		return 0, fmt.Errorf("read settings version: %w", err)
	default:
		version, err = strconv.ParseInt(rawVersion, 10, 64)
		if err != nil || version < 0 {
			return 0, errors.New("stored settings version is invalid")
		}
	}
	for _, key := range keys {
		if _, err := conn.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, values[key]); err != nil {
			return 0, fmt.Errorf("write public setting: %w", err)
		}
	}
	version++
	if _, err := conn.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, settingsVersionKey, strconv.FormatInt(version, 10)); err != nil {
		return 0, fmt.Errorf("write settings version: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return 0, fmt.Errorf("commit public settings: %w", err)
	}
	committed = true
	return version, nil
}

// UpdateAtomic commits one public-settings generation and all changed
// encrypted secrets under a single reserved SQLite write lock.
func (r *SettingsRepository) UpdateAtomic(ctx context.Context, values, ciphertexts map[string]string, updatedAt time.Time) (version int64, returnErr error) {
	keys := make([]string, 0, len(values))
	for key := range values {
		if key == settingsVersionKey {
			return 0, ErrReservedSettingKey
		}
		if _, ok := publicSettingKeys[key]; !ok {
			return 0, ErrUnknownSettingKey
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	secretKeys := make([]string, 0, len(ciphertexts))
	for key, ciphertext := range ciphertexts {
		decoded, err := base64.StdEncoding.DecodeString(ciphertext)
		if strings.TrimSpace(key) == "" || err != nil || len(decoded) == 0 {
			clear(decoded)
			return 0, ErrInvalidCiphertext
		}
		clear(decoded)
		secretKeys = append(secretKeys, key)
	}
	sort.Strings(secretKeys)

	conn, err := r.db.Conn(ctx)
	if err != nil {
		return 0, fmt.Errorf("acquire atomic settings connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return 0, fmt.Errorf("begin atomic settings update: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			if _, err := conn.ExecContext(context.Background(), `ROLLBACK`); err != nil {
				returnErr = errors.Join(returnErr, fmt.Errorf("roll back atomic settings update: %w", err))
			}
		}
	}()
	version, err = readSettingsVersion(ctx, conn)
	if err != nil {
		return 0, err
	}
	for _, key := range keys {
		if _, err := conn.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, values[key]); err != nil {
			return 0, fmt.Errorf("write public setting: %w", err)
		}
	}
	version++
	if _, err := conn.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, settingsVersionKey, strconv.FormatInt(version, 10)); err != nil {
		return 0, fmt.Errorf("write settings version: %w", err)
	}
	for _, key := range secretKeys {
		var secretVersion int64
		err := conn.QueryRowContext(ctx, `SELECT version FROM encrypted_secrets WHERE key=?`, key).Scan(&secretVersion)
		if errors.Is(err, sql.ErrNoRows) {
			secretVersion = 0
		} else if err != nil {
			return 0, errors.New("read encrypted secret version")
		}
		secretVersion++
		if _, err := conn.ExecContext(ctx, `INSERT INTO encrypted_secrets(key,ciphertext,version,updated_at) VALUES(?,?,?,?) ON CONFLICT(key) DO UPDATE SET ciphertext=excluded.ciphertext,version=excluded.version,updated_at=excluded.updated_at`, key, ciphertexts[key], secretVersion, updatedAt); err != nil {
			return 0, errors.New("write encrypted secret")
		}
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return 0, errors.New("commit atomic settings update")
	}
	committed = true
	return version, nil
}

type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readSettingsVersion(ctx context.Context, queryer queryRower) (int64, error) {
	var rawVersion string
	err := queryer.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, settingsVersionKey).Scan(&rawVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read settings version: %w", err)
	}
	version, err := strconv.ParseInt(rawVersion, 10, 64)
	if err != nil || version < 0 {
		return 0, errors.New("stored settings version is invalid")
	}
	return version, nil
}

func (r *SettingsRepository) PutSecret(ctx context.Context, key, ciphertext string, updatedAt time.Time) (version int64, returnErr error) {
	key = strings.TrimSpace(key)
	decoded, err := base64.StdEncoding.DecodeString(ciphertext)
	if key == "" || err != nil || len(decoded) == 0 {
		return 0, ErrInvalidCiphertext
	}
	clear(decoded)
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return 0, fmt.Errorf("acquire encrypted secret connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return 0, fmt.Errorf("begin encrypted secret update: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			if _, err := conn.ExecContext(context.Background(), `ROLLBACK`); err != nil {
				returnErr = errors.Join(returnErr, fmt.Errorf("roll back encrypted secret: %w", err))
			}
		}
	}()
	err = conn.QueryRowContext(ctx, `SELECT version FROM encrypted_secrets WHERE key=?`, key).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		version = 0
	} else if err != nil {
		return 0, errors.New("read encrypted secret version")
	}
	version++
	if _, err := conn.ExecContext(ctx, `INSERT INTO encrypted_secrets(key,ciphertext,version,updated_at) VALUES(?,?,?,?) ON CONFLICT(key) DO UPDATE SET ciphertext=excluded.ciphertext,version=excluded.version,updated_at=excluded.updated_at`, key, ciphertext, version, updatedAt); err != nil {
		return 0, errors.New("write encrypted secret")
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return 0, errors.New("commit encrypted secret")
	}
	committed = true
	return version, nil
}

func (r *SettingsRepository) Secret(ctx context.Context, key string) (EncryptedSecret, error) {
	var secret EncryptedSecret
	err := r.db.QueryRowContext(ctx, `SELECT key,ciphertext,version,updated_at FROM encrypted_secrets WHERE key=?`, key).
		Scan(&secret.Key, &secret.Ciphertext, &secret.Version, &secret.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return EncryptedSecret{}, ErrSecretNotFound
	}
	if err != nil {
		return EncryptedSecret{}, errors.New("read encrypted secret")
	}
	return secret, nil
}
