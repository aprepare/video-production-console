package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOpenCreatesInitialSchema(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "data", "console.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	wantTables := []string{
		"settings",
		"accounts",
		"projects",
		"assets",
		"codex_tasks",
		"task_events",
		"task_messages",
	}
	for _, table := range wantTables {
		var name string
		err := db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`,
			table,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %q does not exist: %v", table, err)
		}
	}

	var concurrency string
	if err := db.QueryRow(
		`SELECT value FROM settings WHERE key = 'max_codex_concurrency'`,
	).Scan(&concurrency); err != nil {
		t.Fatalf("read max_codex_concurrency: %v", err)
	}
	if concurrency != "2" {
		t.Errorf("max_codex_concurrency = %q, want %q", concurrency, "2")
	}
}

func TestOpenConfiguresEveryReplacementConnection(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	db.SetMaxIdleConns(0)
	assertPragmas := func() {
		t.Helper()
		conn, err := db.Conn(t.Context())
		if err != nil {
			t.Fatalf("db.Conn() error = %v", err)
		}
		defer conn.Close()

		var foreignKeys, busyTimeout int
		if err := conn.QueryRowContext(t.Context(), `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
			t.Fatalf("read foreign_keys pragma: %v", err)
		}
		if err := conn.QueryRowContext(t.Context(), `PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
			t.Fatalf("read busy_timeout pragma: %v", err)
		}
		if foreignKeys != 1 || busyTimeout != 5000 {
			t.Errorf("pragmas = foreign_keys:%d busy_timeout:%d, want 1 and 5000", foreignKeys, busyTimeout)
		}
	}

	assertPragmas()
	assertPragmas()

	_, err = db.Exec(`INSERT INTO projects (
        id, account_id, title, stage, created_at, updated_at
    ) VALUES (?, ?, ?, ?, ?, ?)`,
		"project-1", "missing-account", "test", "topic", time.Now(), time.Now())
	if err == nil {
		t.Fatal("insert project with missing account succeeded, want foreign key error")
	}
}

func TestOpenCreatesRequiredIndexes(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	tests := []struct {
		name string
		want string
	}{
		{name: "accounts_name_uq", want: "WHERE status='active'"},
		{name: "assets_project_type_idx", want: "project_id, type, version DESC"},
		{name: "assets_project_type_version_uq", want: "project_id, type, version"},
	}
	for _, tt := range tests {
		var definition string
		if err := db.QueryRow(
			`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ?`,
			tt.name,
		).Scan(&definition); err != nil {
			t.Fatalf("read index %q: %v", tt.name, err)
		}
		if !strings.Contains(definition, tt.want) {
			t.Errorf("index %q definition = %q, want it to contain %q", tt.name, definition, tt.want)
		}
	}
}

func TestConcurrentOpenAppliesMigrationOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "console.db")
	const openers = 8

	start := make(chan struct{})
	errs := make(chan error, openers)
	var wg sync.WaitGroup
	for range openers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			db, err := Open(path)
			if err == nil {
				err = db.Close()
			}
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Errorf("concurrent Open() error = %v", err)
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open migrated database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	var versions int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = 1`).Scan(&versions); err != nil {
		t.Fatalf("count migration versions: %v", err)
	}
	if versions != 1 {
		t.Errorf("migration version 1 rows = %d, want 1", versions)
	}
}
