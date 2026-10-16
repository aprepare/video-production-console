package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestReplicateMigration50PreservesVideoQCAndExpandsSources(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`PRAGMA foreign_keys=OFF; CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	for i, migration := range migrations[:49] {
		if _, err := db.Exec(migration); err != nil {
			t.Fatalf("migration %d: %v", i+1, err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations(version) VALUES(?)`, i+1); err != nil {
			t.Fatal(err)
		}
	}
	const report = `{"summary":"旧审核不应丢失"}`
	if _, err := db.Exec(`INSERT INTO video_qc_checks
		(id,source,source_id,title,product,model,status,duration_s,width,height,frame_count,verdict,score,report_json,error,created_at,updated_at)
		VALUES ('legacy','upload','source-1','旧视频','海鲜','vision','completed',2.5,720,1280,12,'pass',91,?,'','2026-10-15T01:02:03Z','2026-10-15T01:03:04Z')`, report); err != nil {
		t.Fatal(err)
	}

	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	var source, gotReport, created string
	var score int
	if err := db.QueryRow(`SELECT source,report_json,score,created_at FROM video_qc_checks WHERE id='legacy'`).Scan(&source, &gotReport, &score, &created); err != nil {
		t.Fatal(err)
	}
	if source != "upload" || gotReport != report || score != 91 || created != "2026-10-15T01:02:03Z" {
		t.Fatalf("legacy QC changed: source=%q report=%q score=%d created=%q", source, gotReport, score, created)
	}
	for _, allowed := range []string{"replicate", "replicate_take"} {
		if _, err := db.Exec(`INSERT INTO video_qc_checks (id,source,status,created_at,updated_at) VALUES (?,?,'queued','now','now')`, allowed, allowed); err != nil {
			t.Fatalf("new QC source %q rejected: %v", allowed, err)
		}
	}
	for _, table := range []string{"replicate_projects", "replicate_shots", "replicate_takes"} {
		var exists int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&exists); err != nil || exists != 1 {
			t.Fatalf("table %s missing: exists=%d err=%v", table, exists, err)
		}
	}
	if err := migrate(db); err != nil {
		t.Fatalf("migration 50 is not idempotent: %v", err)
	}
}

func TestReplicateMigration50FreshDatabase(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "fresh.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var versions int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&versions); err != nil || versions != 50 {
		t.Fatalf("versions=%d err=%v", versions, err)
	}
	if _, err := db.Exec(`INSERT INTO replicate_projects(id,title,created_at,updated_at) VALUES('p','项目','now','now')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO replicate_shots(id,project_id,position,created_at,updated_at) VALUES('s','p',1,'now','now')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO replicate_takes(id,shot_id,provider,prompt,status,created_at,updated_at) VALUES('t','s','grok','动起来','queued','now','now')`); err != nil {
		t.Fatal(err)
	}
}
