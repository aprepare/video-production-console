package store

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// RestoreBackup copies a verified SQLite backup to a new path.
func RestoreBackup(ctx context.Context, backupPath, destination string) (returnErr error) {
	if err := CheckIntegrity(ctx, backupPath); err != nil {
		return fmt.Errorf("verify source backup: %w", err)
	}
	destination = canonicalPath(destination)
	if _, err := os.Lstat(destination); err == nil {
		return fmt.Errorf("restore destination already exists: %s", destination)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	input, err := os.Open(backupPath)
	if err != nil {
		return err
	}
	defer input.Close()
	staging, err := os.CreateTemp(filepath.Dir(destination), ".console-restore-*.db")
	if err != nil {
		return err
	}
	stagingPath := staging.Name()
	defer func() {
		_ = staging.Close()
		if returnErr != nil {
			_ = os.Remove(stagingPath)
		}
	}()
	if _, err := io.Copy(staging, input); err != nil {
		return err
	}
	if err := staging.Sync(); err != nil {
		return err
	}
	if err := staging.Close(); err != nil {
		return err
	}
	if err := CheckIntegrity(ctx, stagingPath); err != nil {
		return fmt.Errorf("verify restored database: %w", err)
	}
	return os.Rename(stagingPath, destination)
}

func CheckIntegrity(ctx context.Context, path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	path = filepath.ToSlash(absolute)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	dsn := (&url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	var result string
	if err := db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("sqlite integrity check returned %q", result)
	}
	return nil
}
