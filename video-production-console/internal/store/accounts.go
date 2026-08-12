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

type CommitOutcomeError struct {
	Outcome CommitState
	Err     error
}

func (e *CommitOutcomeError) Error() string { return e.Err.Error() }
func (e *CommitOutcomeError) Unwrap() error { return e.Err }

func commitOutcome(err error) CommitState {
	var outcome *CommitOutcomeError
	if errors.As(err, &outcome) {
		return outcome.Outcome
	}
	return CommitNotCommitted
}

func CommitOutcomeOf(err error) CommitState { return commitOutcome(err) }

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
	if _, parseErr := uuid.Parse(account.ID); parseErr != nil {
		return CommitNotCommitted, ErrInvalidAssetInput
	}
	if _, parseErr := uuid.Parse(background.ID); parseErr != nil {
		return CommitNotCommitted, ErrInvalidAssetInput
	}
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return CommitNotCommitted, fmt.Errorf("begin create account: %w", err)
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return CommitNotCommitted, fmt.Errorf("begin create account: %w", err)
	}
	defer func() {
		if err != nil {
			_, _ = conn.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()
	_, err = conn.ExecContext(ctx, `INSERT INTO accounts
        (id, name, background_asset_id, color, status, created_at, updated_at)
        VALUES (?, ?, NULL, ?, 'active', ?, ?)`,
		account.ID, account.Name, account.Color, account.CreatedAt, account.UpdatedAt)
	if err != nil {
		return CommitNotCommitted, classifyAccountError(err)
	}
	version, err := NewAssetRepository(r.db).addVersion(ctx, conn, AddAssetVersion{AccountID: account.ID, Type: domain.AssetAccountBackground, StorageKind: domain.StorageFile, Path: background.Path, Filename: background.Filename, MIMEType: background.MIMEType, Size: background.Size, SHA256: background.SHA256}, account.CreatedAt)
	if err != nil {
		return CommitNotCommitted, fmt.Errorf("insert background version: %w", err)
	}
	if _, err = conn.ExecContext(ctx, `UPDATE accounts SET background_asset_item_id = ? WHERE id = ?`, version.AssetID, account.ID); err != nil {
		return CommitNotCommitted, fmt.Errorf("link background asset: %w", err)
	}
	if _, err = conn.ExecContext(ctx, `COMMIT`); err != nil {
		return CommitUnknown, fmt.Errorf("commit create account: %w", err)
	}
	return CommitCommitted, nil
}

func (r *AccountRepository) List(ctx context.Context) ([]domain.Account, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT a.id, a.name, a.background_asset_item_id, v.path, a.color, a.status, a.created_at, a.updated_at
		FROM accounts a LEFT JOIN asset_items i ON i.id=a.background_asset_item_id LEFT JOIN asset_versions v ON v.id=i.current_version_id ORDER BY a.created_at, a.id`)
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
	account, err := scanAccount(r.db.QueryRowContext(ctx, `SELECT a.id, a.name, a.background_asset_item_id, v.path, a.color, a.status, a.created_at, a.updated_at
		FROM accounts a LEFT JOIN asset_items i ON i.id=a.background_asset_item_id LEFT JOIN asset_versions v ON v.id=i.current_version_id WHERE a.id = ?`, id))
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
	if _, parseErr := uuid.Parse(accountID); parseErr != nil {
		return domain.Account{}, CommitNotCommitted, ErrInvalidAssetInput
	}
	if _, parseErr := uuid.Parse(background.ID); parseErr != nil {
		return domain.Account{}, CommitNotCommitted, ErrInvalidAssetInput
	}
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return domain.Account{}, CommitNotCommitted, fmt.Errorf("begin replace background: %w", err)
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return domain.Account{}, CommitNotCommitted, fmt.Errorf("begin replace background: %w", err)
	}
	defer func() {
		if state != CommitCommitted {
			_, _ = conn.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()
	account, err = scanAccount(conn.QueryRowContext(ctx, `SELECT a.id, a.name, a.background_asset_item_id, v.path,
        a.color, a.status, a.created_at, a.updated_at
		FROM accounts a LEFT JOIN asset_items i ON i.id=a.background_asset_item_id LEFT JOIN asset_versions v ON v.id=i.current_version_id WHERE a.id = ?`, accountID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Account{}, CommitNotCommitted, ErrAccountNotFound
	}
	if err != nil {
		return domain.Account{}, CommitNotCommitted, fmt.Errorf("read account for background replacement: %w", err)
	}
	if account.BackgroundAssetID == nil {
		return domain.Account{}, CommitNotCommitted, ErrAssetVersionNotFound
	}
	var parent string
	if err = conn.QueryRowContext(ctx, `SELECT current_version_id FROM asset_items WHERE id=?`, *account.BackgroundAssetID).Scan(&parent); err != nil {
		return domain.Account{}, CommitNotCommitted, err
	}
	version, err := NewAssetRepository(r.db).addVersion(ctx, conn, AddAssetVersion{LogicalAssetID: *account.BackgroundAssetID, AccountID: accountID, Type: domain.AssetAccountBackground, StorageKind: domain.StorageFile, Path: background.Path, Filename: background.Filename, MIMEType: background.MIMEType, Size: background.Size, SHA256: background.SHA256, ParentVersionID: &parent}, updatedAt)
	if err != nil {
		return domain.Account{}, CommitNotCommitted, fmt.Errorf("insert replacement background: %w", err)
	}
	if _, err = conn.ExecContext(ctx, `UPDATE accounts SET updated_at = ? WHERE id = ?`, updatedAt, accountID); err != nil {
		return domain.Account{}, CommitNotCommitted, fmt.Errorf("update background pointer: %w", err)
	}
	if _, err = conn.ExecContext(ctx, `COMMIT`); err != nil {
		return domain.Account{}, CommitUnknown, fmt.Errorf("commit replace background: %w", err)
	}
	state = CommitCommitted
	account.BackgroundAssetID = &version.AssetID
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
