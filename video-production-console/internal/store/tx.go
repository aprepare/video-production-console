package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"time"
)

// commitHook lets a repository override how a transaction is committed. Tests
// inject it to simulate a commit whose acknowledgement is lost after SQLite
// already applied it.
type commitHook func(context.Context, *sql.Conn) error

// runImmediate executes fn inside a BEGIN IMMEDIATE transaction on a dedicated
// connection.
//
// Any connection whose transaction state is uncertain is discarded through
// driver.ErrBadConn rather than returned to the pool: a connection still inside
// BEGIN IMMEDIATE would make the next borrower fail with "cannot start a
// transaction within a transaction". A failed COMMIT is reported as
// CommitUnknown so callers can reconcile durable state, and no ROLLBACK is
// attempted afterwards because the commit may in fact have been applied.
func runImmediate(ctx context.Context, db *sql.DB, operation string, commit commitHook, fn func(assetDBTX, time.Time) error) (returnErr error) {
	conn, err := db.Conn(ctx)
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
	var commitErr error
	if commit != nil {
		commitErr = commit(ctx, conn)
	} else {
		_, commitErr = conn.ExecContext(ctx, `COMMIT`)
	}
	if commitErr != nil {
		discard = true
		ended = true
		return &CommitOutcomeError{Outcome: CommitUnknown, Err: fmt.Errorf("commit %s outcome unknown: %w", operation, commitErr)}
	}
	ended = true
	return nil
}
