package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func TestVideoQCMigrationUpgrades48WithoutChangingReports(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`PRAGMA foreign_keys=OFF; CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	for i, migration := range migrations[:48] {
		if _, err := db.Exec(migration); err != nil {
			t.Fatalf("migration %d: %v", i+1, err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations(version) VALUES(?)`, i+1); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO reference_posts(id,platform,external_id,created_at,updated_at) VALUES('legacy','douyin','legacy',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP);
	INSERT INTO reference_analyses(id,reference_id,status,model,focus,report_json,created_at,updated_at) VALUES('analysis','legacy','completed','old','', '{"summary":"旧报告"}',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	var product, report string
	if err := db.QueryRow(`SELECT product,report_json FROM reference_analyses WHERE id='analysis'`).Scan(&product, &report); err != nil || product != "" || report != `{"summary":"旧报告"}` {
		t.Fatalf("legacy product=%q report=%q err=%v", product, report, err)
	}
	if err := migrate(db); err != nil {
		t.Fatalf("reopen migration: %v", err)
	}
}

func TestVideoQCMigration49(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=49`).Scan(&version); err != nil || version != 1 {
		t.Fatalf("migration 49 missing: %d %v", version, err)
	}
	if _, err := db.Exec(`SELECT product FROM reference_analyses`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`SELECT * FROM video_qc_checks`); err != nil {
		t.Fatal(err)
	}
}

func TestVideoQCRepositoryLifecycle(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := NewVideoQCRepository(db)
	ctx := context.Background()
	for _, id := range []string{"one", "two", "three"} {
		a, err := r.CreateQueued(ctx, VideoQCCheck{ID: id, Source: "upload", Title: "海鲜.mp4", Product: "一盒海鲜", Model: "vision"})
		if err != nil || a.Status != "queued" {
			t.Fatalf("create: %+v %v", a, err)
		}
	}
	if err := r.MarkRunning(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	if err := r.Complete(ctx, "two", VideoQCCheck{}); !errors.Is(err, ErrVideoQCNotFound) {
		t.Fatalf("queued cannot complete: %v", err)
	}
	if err := r.Complete(ctx, "one", VideoQCCheck{DurationS: 2.1, Width: 320, Height: 240, FrameCount: 3, Verdict: "warn", Score: 72, ReportJSON: `{"summary":"检查"}`}); err != nil {
		t.Fatal(err)
	}
	a, err := r.Get(ctx, "one")
	if err != nil || a.Status != "completed" || a.Product != "一盒海鲜" || a.FrameCount != 3 || a.Score != 72 || a.CreatedAt.IsZero() {
		t.Fatalf("get: %+v %v", a, err)
	}
	if err := r.MarkRunning(ctx, "two"); err != nil {
		t.Fatal(err)
	}
	if err := r.FailInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"two", "three"} {
		a, err := r.Get(ctx, id)
		if err != nil || a.Status != "failed" || a.Error != "控制台重启中断" {
			t.Fatalf("recovery: %+v %v", a, err)
		}
	}
	items, err := r.List(ctx, 2)
	if err != nil || len(items) != 2 || items[0].ID != "three" || items[1].ID != "two" {
		t.Fatalf("list: %+v %v", items, err)
	}
	if err := r.Delete(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Get(ctx, "one"); !errors.Is(err, ErrVideoQCNotFound) {
		t.Fatal(err)
	}
	if err := r.Delete(ctx, "one"); !errors.Is(err, ErrVideoQCNotFound) {
		t.Fatal(err)
	}
}
