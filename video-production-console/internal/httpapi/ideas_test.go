package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/codex"
	"video-production-console/internal/domain"
	"video-production-console/internal/store"
)

// persistedIdeaScheduler models the synchronous persistence step that the
// production scheduler performs before it signals its worker loop.
type persistedIdeaScheduler struct{ tasks *store.TaskRepository }

func (s persistedIdeaScheduler) Enqueue(ctx context.Context, task domain.CodexTask) error {
	return s.tasks.CreateV2(ctx, task)
}
func (persistedIdeaScheduler) Resume(context.Context, string, string) error { return nil }
func (persistedIdeaScheduler) Cancel(context.Context, string) error         { return nil }
func (persistedIdeaScheduler) SetLimit(int) error                           { return nil }
func (persistedIdeaScheduler) Snapshot() codex.SchedulerSnapshot            { return codex.SchedulerSnapshot{} }
func (persistedIdeaScheduler) Close()                                       {}

func TestIdeaMessagePersistsTaskBeforeForeignKeyReference(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/console.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	now := time.Now().UTC()
	accountID := uuid.NewString()
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,?,?,?,?)`, accountID, "test", "#000000", "active", now, now); err != nil {
		t.Fatalf("create account: %v", err)
	}
	ideas := store.NewIdeaRepository(db)
	sessionID := uuid.NewString()
	if err := ideas.CreateSession(context.Background(), domain.IdeaSession{ID: sessionID, AccountID: &accountID, Title: "test", Status: "planning", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	handler := NewIdeasHandler(db, persistedIdeaScheduler{tasks: store.NewTaskRepository(db)})
	request := httptest.NewRequest(http.MethodPost, "/api/ideas/"+sessionID+"/messages", strings.NewReader(`{"content":"测试选题"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	messages, err := ideas.Messages(context.Background(), sessionID)
	if err != nil || len(messages) != 1 || messages[0].TaskID == nil {
		t.Fatalf("messages=%+v err=%v", messages, err)
	}
	if _, err := store.NewTaskRepository(db).Get(context.Background(), *messages[0].TaskID); err != nil {
		t.Fatalf("referenced task was not persisted: %v", err)
	}
}
