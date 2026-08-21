package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestClearStaleOptionalSettingsEmptiesBlockingPaths(t *testing.T) {
	db := openTempSettingsDB(t)
	if _, err := db.Exec(`INSERT INTO settings(key,value) VALUES('ffmpeg_path',?),('machine_profile_path',?),('listen_addr',?)`, `C:\missing\ffmpeg.exe`, `C:\missing\profile.json`, "127.0.0.1:2030"); err != nil {
		t.Fatal(err)
	}
	cleared, err := clearStaleOptionalSettings(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(cleared) != 2 {
		t.Fatalf("cleared=%v", cleared)
	}
	var ffmpeg, listen string
	if err := db.QueryRow(`SELECT value FROM settings WHERE key='ffmpeg_path'`).Scan(&ffmpeg); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT value FROM settings WHERE key='listen_addr'`).Scan(&listen); err != nil {
		t.Fatal(err)
	}
	if ffmpeg != "" || listen != "127.0.0.1:2030" {
		t.Fatalf("ffmpeg=%q listen=%q", ffmpeg, listen)
	}
}

func TestRunSkipsMissingDatabase(t *testing.T) {
	root := t.TempDir()
	if err := run([]string{root}); err != nil {
		t.Fatal(err)
	}
}

func openTempSettingsDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "console.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(filepath.Dir(path)), 0o755); err != nil {
		t.Fatal(err)
	}
	return db
}
