package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/domain"
)

func TestListIdeaSessionsHidesEmptyShells(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := NewIdeaRepository(db)
	now := time.Now().UTC()
	emptyID := uuid.NewString()
	usedID := uuid.NewString()
	for _, session := range []domain.IdeaSession{
		{ID: emptyID, Title: "空会话", Status: "planning", CreatedAt: now, UpdatedAt: now},
		{ID: usedID, Title: "有效会话", Status: "planning", CreatedAt: now, UpdatedAt: now},
	} {
		if err := repo.CreateSession(context.Background(), session); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.AddMessage(context.Background(), domain.IdeaMessage{SessionID: usedID, Role: "user", Content: "给我选题"}); err != nil {
		t.Fatal(err)
	}

	sessions, err := repo.ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].ID != usedID {
		t.Fatalf("listed sessions = %+v, want only %s", sessions, usedID)
	}
}
