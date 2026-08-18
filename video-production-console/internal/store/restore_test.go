package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestManualBackupRestoreRoundTripAndRejectsOverwrite(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "console.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE backup_fixture(value TEXT); INSERT INTO backup_fixture(value) VALUES('preserved')`); err != nil {
		t.Fatal(err)
	}
	backupDir := filepath.Join(t.TempDir(), "backups")
	now := time.Date(2026, time.August, 9, 1, 2, 3, 0, time.UTC)
	if err := CreateBackup(db, root, backupDir, now); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(backupDir, "console-20260809-010203.000000000.db")
	if err := CheckIntegrity(context.Background(), backup); err != nil {
		t.Fatal(err)
	}
	restoredPath := filepath.Join(t.TempDir(), "restored", "console.db")
	if err := RestoreBackup(context.Background(), backup, restoredPath); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(restoredPath)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var value string
	if err := restored.QueryRow(`SELECT value FROM backup_fixture`).Scan(&value); err != nil || value != "preserved" {
		t.Fatalf("restored value=%q err=%v", value, err)
	}
	if err := RestoreBackup(context.Background(), backup, restoredPath); err == nil {
		t.Fatal("restore overwrote an existing database")
	}
}

func TestCreateBackupRejectsOverlappingDirectories(t *testing.T) {
	root := t.TempDir()
	db, err := Open(filepath.Join(root, "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	for _, backupDir := range []string{
		filepath.Join(root, "backups"),
		filepath.Dir(root),
	} {
		if err := CreateBackup(db, root, backupDir, time.Now()); err == nil {
			t.Fatalf("CreateBackup accepted overlapping directory %s", backupDir)
		}
	}
}

func TestRestoreBackupRejectsCorruptSQLite(t *testing.T) {
	corrupt := filepath.Join(t.TempDir(), "corrupt.db")
	if err := os.WriteFile(corrupt, []byte("not sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "console.db")
	if err := RestoreBackup(context.Background(), corrupt, destination); err == nil {
		t.Fatal("corrupt backup was restored")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("corrupt restore created destination: %v", err)
	}
}
