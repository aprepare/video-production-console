package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"video-production-console/internal/domain"
)

var (
	ErrAccountNameConflict = errors.New("active account name already exists")
	ErrAccountNotFound     = errors.New("account not found")
	ErrAccountActive       = errors.New("active account cannot be deleted")
)

type AccountRepository struct {
	db *sql.DB
}

type CommitState uint8

const (
	CommitNotCommitted CommitState = iota
	CommitCommitted
	CommitUnknown
)

type NewBackground struct {
	ID       string
	Path     string
	Filename string
	MIMEType string
	Size     int64
	SHA256   string
}

func NewAccountRepository(db *sql.DB) *AccountRepository {
	return &AccountRepository{db: db}
}

func (r *AccountRepository) CreateWithBackground(ctx context.Context, account domain.Account, background NewBackground) (state CommitState, err error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return CommitNotCommitted, fmt.Errorf("begin create account: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	_, err = tx.ExecContext(ctx, `INSERT INTO accounts
        (id, name, background_asset_id, color, status, created_at, updated_at)
        VALUES (?, ?, NULL, ?, 'active', ?, ?)`,
		account.ID, account.Name, account.Color, account.CreatedAt, account.UpdatedAt)
	if err != nil {
		return CommitNotCommitted, classifyAccountError(err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO assets
        (id, project_id, account_id, type, path, filename, mime_type, size, sha256, version, status, created_at)
        VALUES (?, NULL, ?, 'account_background', ?, ?, ?, ?, ?, 1, 'active', ?)`,
		background.ID, account.ID, background.Path, background.Filename, background.MIMEType,
		background.Size, background.SHA256, account.CreatedAt)
	if err != nil {
		return CommitNotCommitted, fmt.Errorf("insert background asset: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE accounts SET background_asset_id = ? WHERE id = ?`, background.ID, account.ID); err != nil {
		return CommitNotCommitted, fmt.Errorf("link background asset: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return CommitUnknown, fmt.Errorf("commit create account: %w", err)
	}
	return CommitCommitted, nil
}

func (r *AccountRepository) List(ctx context.Context) ([]domain.Account, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT a.id, a.name, a.background_asset_id, b.path, a.color, a.status, a.created_at, a.updated_at
        FROM accounts a LEFT JOIN assets b ON b.id = a.background_asset_id ORDER BY a.created_at, a.id`)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	defer rows.Close()
	accounts := make([]domain.Account, 0)
	for rows.Next() {
		account, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, account)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate accounts: %w", err)
	}
	return accounts, nil
}

func (r *AccountRepository) Get(ctx context.Context, id string) (domain.Account, error) {
	account, err := scanAccount(r.db.QueryRowContext(ctx, `SELECT a.id, a.name, a.background_asset_id, b.path, a.color, a.status, a.created_at, a.updated_at
        FROM accounts a LEFT JOIN assets b ON b.id = a.background_asset_id WHERE a.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Account{}, ErrAccountNotFound
	}
	return account, err
}

func (r *AccountRepository) Rename(ctx context.Context, id, name string, updatedAt time.Time) (domain.Account, error) {
	result, err := r.db.ExecContext(ctx, `UPDATE accounts SET name = ?, updated_at = ? WHERE id = ?`, name, updatedAt, id)
	if err != nil {
		return domain.Account{}, classifyAccountError(err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return domain.Account{}, fmt.Errorf("read rename result: %w", err)
	}
	if count == 0 {
		return domain.Account{}, ErrAccountNotFound
	}
	return r.Get(ctx, id)
}

func (r *AccountRepository) ReplaceBackground(ctx context.Context, accountID string, background NewBackground, updatedAt time.Time) (account domain.Account, state CommitState, err error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Account{}, CommitNotCommitted, fmt.Errorf("begin replace background: %w", err)
	}
	defer func() {
		if state != CommitCommitted {
			_ = tx.Rollback()
		}
	}()
	account, err = scanAccount(tx.QueryRowContext(ctx, `SELECT a.id, a.name, a.background_asset_id, b.path,
        a.color, a.status, a.created_at, a.updated_at
        FROM accounts a LEFT JOIN assets b ON b.id = a.background_asset_id WHERE a.id = ?`, accountID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Account{}, CommitNotCommitted, ErrAccountNotFound
	}
	if err != nil {
		return domain.Account{}, CommitNotCommitted, fmt.Errorf("read account for background replacement: %w", err)
	}
	var version int
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) + 1 FROM assets
		WHERE account_id = ? AND type = 'account_background'`, accountID).Scan(&version); err != nil {
		return domain.Account{}, CommitNotCommitted, fmt.Errorf("choose background version: %w", err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO assets
        (id, project_id, account_id, type, path, filename, mime_type, size, sha256, version, status, created_at)
        VALUES (?, NULL, ?, 'account_background', ?, ?, ?, ?, ?, ?, 'active', ?)`,
		background.ID, accountID, background.Path, background.Filename, background.MIMEType,
		background.Size, background.SHA256, version, updatedAt)
	if err != nil {
		return domain.Account{}, CommitNotCommitted, fmt.Errorf("insert replacement background: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE accounts SET background_asset_id = ?, updated_at = ? WHERE id = ?`, background.ID, updatedAt, accountID); err != nil {
		return domain.Account{}, CommitNotCommitted, fmt.Errorf("update background pointer: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return domain.Account{}, CommitUnknown, fmt.Errorf("commit replace background: %w", err)
	}
	state = CommitCommitted
	account.BackgroundAssetID = &background.ID
	account.BackgroundPath = &background.Path
	account.UpdatedAt = updatedAt
	return account, CommitCommitted, nil
}

func (r *AccountRepository) Deactivate(ctx context.Context, id string, updatedAt time.Time) error {
	result, err := r.db.ExecContext(ctx, `UPDATE accounts SET status = 'inactive', updated_at = ? WHERE id = ?`, updatedAt, id)
	if err != nil {
		return fmt.Errorf("deactivate account: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read deactivate result: %w", err)
	}
	if count == 0 {
		return ErrAccountNotFound
	}
	return nil
}

type accountScanner interface {
	Scan(dest ...any) error
}

func scanAccount(scanner accountScanner) (domain.Account, error) {
	var account domain.Account
	err := scanner.Scan(&account.ID, &account.Name, &account.BackgroundAssetID, &account.BackgroundPath, &account.Color,
		&account.Status, &account.CreatedAt, &account.UpdatedAt)
	if err != nil {
		return domain.Account{}, err
	}
	return account, nil
}

func classifyAccountError(err error) error {
	if strings.Contains(err.Error(), "UNIQUE constraint failed: accounts.name") {
		return ErrAccountNameConflict
	}
	return fmt.Errorf("store account: %w", err)
}
