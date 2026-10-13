// Package citylib provides isolated fixtures matching the production library schema.
package citylib

import (
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// Schema is the table/index contract from multi-account-video-production/library.py.
const Schema = `
CREATE TABLE assets(asset_id TEXT PRIMARY KEY, path TEXT NOT NULL, category TEXT NOT NULL,
 title TEXT, origin TEXT, duration REAL, usable_in REAL, usable_out REAL,
 width INTEGER, height INTEGER, status TEXT, family TEXT, metadata TEXT, review TEXT);
CREATE TABLE aliases(path TEXT PRIMARY KEY, asset_id TEXT NOT NULL REFERENCES assets(asset_id));
CREATE TABLE projects(project_id TEXT PRIMARY KEY, account TEXT, status TEXT, seed INTEGER, plan TEXT);
CREATE TABLE uses(project_id TEXT NOT NULL REFERENCES projects(project_id),
 asset_id TEXT NOT NULL REFERENCES assets(asset_id), PRIMARY KEY(project_id,asset_id));
CREATE TABLE reservations(project_id TEXT NOT NULL REFERENCES projects(project_id),
 asset_id TEXT NOT NULL REFERENCES assets(asset_id), PRIMARY KEY(project_id,asset_id));
CREATE INDEX uses_asset ON uses(asset_id);
CREATE INDEX reservations_asset ON reservations(asset_id);`

type Fixture struct {
	Root string
	Path string
	DB   *sql.DB
}

func New(t testing.TB) *Fixture {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "00_INDEX", "production_catalog.sqlite3")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	f := &Fixture{root, path, db}
	f.Exec(t, "PRAGMA foreign_keys=ON")
	f.Exec(t, "PRAGMA journal_mode=WAL")
	f.Exec(t, Schema)
	return f
}

func (f *Fixture) Exec(t testing.TB, query string, args ...any) {
	t.Helper()
	if _, err := f.DB.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func (f *Fixture) Count(t testing.TB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := f.DB.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (f *Fixture) Add(t testing.TB, id, category string) string {
	t.Helper()
	path := filepath.Join(f.Root, id+".mp4")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	f.Exec(t, `INSERT INTO assets VALUES(?,?,?,?,'real',10,0,10,1920,1080,'eligible',?,'{}','{}')`, id, path, category, id, id)
	return path
}

func (f *Fixture) History(t testing.TB, id, account string, uses, reservations int) {
	t.Helper()
	for i := 0; i < uses+reservations; i++ {
		project := fmt.Sprintf("history-%s-%s-%d", id, account, i)
		status, table := "completed", "uses"
		if i >= uses {
			status, table = "reserved", "reservations"
		}
		f.Exec(t, "INSERT INTO projects(project_id,account,status) VALUES(?,?,?)", project, account, status)
		f.Exec(t, "INSERT INTO "+table+" VALUES(?,?)", project, id)
	}
}

func Video(t testing.TB, filter string) (path, ffmpeg string) {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not on PATH")
	}
	path = filepath.Join(t.TempDir(), "sample.mp4")
	out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", filter, "-t", "12", "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", path).CombinedOutput()
	if err != nil {
		t.Fatalf("create video: %v: %s", err, out)
	}
	return path, ffmpeg
}
