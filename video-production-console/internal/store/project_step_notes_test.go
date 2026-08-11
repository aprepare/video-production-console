package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"video-production-console/internal/domain"
)

func TestProjectStepNotesUpsertAndGet(t *testing.T) {
	db, err := Open(t.TempDir() + "/step-notes.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	accountID := uuid.NewString()
	projectID := uuid.NewString()
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,?,?,?,?)`, accountID, "notes-account", "#111", "active", now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO projects(id,account_id,title,stage,created_at,updated_at) VALUES(?,?,?,?,?,?)`, projectID, accountID, "notes", domain.StageScript, now, now); err != nil {
		t.Fatal(err)
	}
	repo := NewProjectStepNotesRepository(db)
	if _, err := repo.Get(context.Background(), projectID, "remix"); err != ErrStepNotesNotFound {
		t.Fatalf("expected missing notes, got %v", err)
	}
	saved, err := repo.Upsert(context.Background(), projectID, "remix", "  语气更口语  ", now)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Notes != "语气更口语" || saved.Step != "remix" {
		t.Fatalf("unexpected saved notes: %+v", saved)
	}
	got, err := repo.Get(context.Background(), projectID, "remix")
	if err != nil {
		t.Fatal(err)
	}
	if got.Notes != "语气更口语" {
		t.Fatalf("got notes %q", got.Notes)
	}
	later := now.Add(time.Minute)
	if _, err := repo.Upsert(context.Background(), projectID, "remix", "缩短开场", later); err != nil {
		t.Fatal(err)
	}
	got, err = repo.Get(context.Background(), projectID, "remix")
	if err != nil {
		t.Fatal(err)
	}
	if got.Notes != "缩短开场" || !got.UpdatedAt.Equal(later) {
		t.Fatalf("unexpected updated notes: %+v", got)
	}
	if _, err := repo.Upsert(context.Background(), projectID, "montage", "nope", now); err != ErrInvalidStepNotes {
		t.Fatalf("expected invalid step, got %v", err)
	}
}
