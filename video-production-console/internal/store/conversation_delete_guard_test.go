package store

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/domain"
)

func TestDeleteMappingRejectsActiveTurnAndProjectMain(t *testing.T) {
	db, err := Open(t.TempDir() + "/delete-guards.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := NewConversationRepository(db)
	now := time.Now().UTC()
	active := domain.ChatSession{ID: uuid.NewString(), Title: "active", Kind: domain.ChatGeneral, Status: domain.ChatRunning}
	if err := repo.CreateSession(t.Context(), active); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateTurn(t.Context(), domain.ChatTurn{ID: uuid.NewString(), SessionID: active.ID, Status: domain.ChatTurnRunning, Delivery: domain.DeliveryQueue}); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteMapping(t.Context(), active.ID); !errors.Is(err, ErrConversationActive) {
		t.Fatalf("active delete err=%v", err)
	}
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('account','A','#fff','active',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	taskSession := domain.ChatSession{ID: uuid.NewString(), Title: "task", Kind: domain.ChatGeneral, Status: domain.ChatIdle}
	if err := repo.CreateSession(t.Context(), taskSession); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO codex_tasks(id,account_id,type,skill_name,status,prompt_snapshot,chat_session_id,created_at) VALUES('active-task','account','chat','skill','queued','prompt',?,?)`, taskSession.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteMapping(t.Context(), taskSession.ID); !errors.Is(err, ErrConversationActive) {
		t.Fatalf("task delete err=%v", err)
	}
	if _, err := db.Exec(`INSERT INTO projects(id,account_id,title,stage,created_at,updated_at) VALUES('project','account','P','topic',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	projectID := "project"
	main := domain.ChatSession{ID: uuid.NewString(), Title: "main", Source: "console", Kind: domain.ChatProject, Status: domain.ChatIdle, ProjectID: &projectID}
	if err := repo.CreateSession(t.Context(), main); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteMapping(t.Context(), main.ID); !errors.Is(err, ErrConversationProtected) {
		t.Fatalf("main delete err=%v", err)
	}
}
