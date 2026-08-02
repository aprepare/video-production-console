package store

import (
	"path/filepath"
	"strings"
	"testing"
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
