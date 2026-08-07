package httpapi

import (
	"context"
	"errors"
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

func TestDeleteIdeaSessionRemovesConversation(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/console.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	repo := store.NewIdeaRepository(db)
	sessionID := uuid.NewString()
	now := time.Now().UTC()
	if err := repo.CreateSession(context.Background(), domain.IdeaSession{ID: sessionID, Title: "待删除", Status: "planning", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}

	handler := NewIdeasHandler(db, nil, nil, nil)
	request := httptest.NewRequest(http.MethodDelete, "/api/ideas/"+sessionID, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if _, err := repo.GetSession(context.Background(), sessionID); !errors.Is(err, store.ErrIdeaSessionNotFound) {
		t.Fatalf("deleted session lookup error = %v", err)
	}
}

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

	handler := NewIdeasHandler(db, persistedIdeaScheduler{tasks: store.NewTaskRepository(db)}, nil, nil)
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

func TestIdeaMessageResolvesModelOnlyWhenEnqueuing(t *testing.T) {
	for _, tt := range []struct {
		name, extra, model, effort string
		status                     int
	}{
		{"defaults", "", "gpt-5.6-sol", "medium", 202},
		{"override", `,"model":"openai/custom","reasoning_effort":"high"`, "openai/custom", "high", 202},
		{"invalid", `,"reasoning_effort":"impossible"`, "", "", 400},
	} {
		t.Run(tt.name, func(t *testing.T) {
			db, err := store.Open(t.TempDir() + "/console.db")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			now := time.Now().UTC()
			accountID := uuid.NewString()
			if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,?,?,?,?)`, accountID, "test", "#000", "active", now, now); err != nil {
				t.Fatal(err)
			}
			repo := store.NewIdeaRepository(db)
			sessionID := uuid.NewString()
			if err := repo.CreateSession(context.Background(), domain.IdeaSession{ID: sessionID, AccountID: &accountID, Title: "test", Status: "planning", CreatedAt: now, UpdatedAt: now}); err != nil {
				t.Fatal(err)
			}
			h := NewIdeasHandler(db, persistedIdeaScheduler{tasks: store.NewTaskRepository(db)}, nil, nil)
			req := httptest.NewRequest(http.MethodPost, "/api/ideas/"+sessionID+"/messages", strings.NewReader(`{"content":"plan"`+tt.extra+`}`))
			req.Header.Set("Content-Type", "application/json")
			res := httptest.NewRecorder()
			h.ServeHTTP(res, req)
			if res.Code != tt.status {
				t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
			}
			messages, err := repo.Messages(context.Background(), sessionID)
			if err != nil {
				t.Fatal(err)
			}
			if tt.status == 400 {
				if len(messages) != 0 {
					t.Fatalf("invalid request persisted messages=%+v", messages)
				}
				return
			}
			if len(messages) != 1 || messages[0].TaskID == nil {
				t.Fatalf("messages=%+v", messages)
			}
			task, err := store.NewTaskRepository(db).Get(context.Background(), *messages[0].TaskID)
			if err != nil {
				t.Fatal(err)
			}
			if task.ModelName != tt.model || task.ReasoningEffort != tt.effort {
				t.Fatalf("task=%+v", task)
			}
		})
	}
}

func TestIdeaMessageWithoutSchedulerDoesNotValidateModel(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/console.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	repo := store.NewIdeaRepository(db)
	sessionID := uuid.NewString()
	if err := repo.CreateSession(context.Background(), domain.IdeaSession{ID: sessionID, Title: "test", Status: "planning", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	h := NewIdeasHandler(db, nil, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/ideas/"+sessionID+"/messages", strings.NewReader(`{"content":"save","model":"bad model"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != 202 {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestIdeaMessageExplicitAccountUpdatesSessionAndTask(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/console.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	firstAccount := uuid.NewString()
	secondAccount := uuid.NewString()
	for _, accountID := range []string{firstAccount, secondAccount} {
		if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,?,?,?,?)`, accountID, accountID, "#000", "active", now, now); err != nil {
			t.Fatal(err)
		}
	}
	repo := store.NewIdeaRepository(db)
	sessionID := uuid.NewString()
	if err := repo.CreateSession(t.Context(), domain.IdeaSession{ID: sessionID, AccountID: &firstAccount, Title: "test", Status: "planning", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	h := NewIdeasHandler(db, persistedIdeaScheduler{tasks: store.NewTaskRepository(db)}, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/ideas/"+sessionID+"/messages", strings.NewReader(`{"content":"plan","account_id":"`+secondAccount+`"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	session, err := repo.GetSession(t.Context(), sessionID)
	if err != nil || session.AccountID == nil || *session.AccountID != secondAccount {
		t.Fatalf("session=%+v err=%v", session, err)
	}
	messages, err := repo.Messages(t.Context(), sessionID)
	if err != nil || len(messages) != 1 || messages[0].TaskID == nil {
		t.Fatalf("messages=%+v err=%v", messages, err)
	}
	task, err := store.NewTaskRepository(db).Get(t.Context(), *messages[0].TaskID)
	if err != nil || task.AccountID != secondAccount {
		t.Fatalf("task=%+v err=%v", task, err)
	}
}

func TestSelectIdeaCandidateExplicitAccountOverridesSessionAccount(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/console.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	firstAccount := uuid.NewString()
	secondAccount := uuid.NewString()
	for _, accountID := range []string{firstAccount, secondAccount} {
		if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,?,?,?,?)`, accountID, accountID, "#000", "active", now, now); err != nil {
			t.Fatal(err)
		}
	}
	sessionID := uuid.NewString()
	candidateID := uuid.NewString()
	repo := store.NewIdeaRepository(db)
	if err := repo.CreateSession(t.Context(), domain.IdeaSession{ID: sessionID, AccountID: &firstAccount, Title: "test", Status: "planning", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO idea_candidates(id,session_id,position,title,summary,score,source,selected,created_at) VALUES(?,?,?,?,?,?,?,0,?)`, candidateID, sessionID, 1, "选题", "摘要", 90, "test", now); err != nil {
		t.Fatal(err)
	}
	h := NewIdeasHandler(db, nil, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/ideas/"+sessionID+"/select", strings.NewReader(`{"candidate_id":"`+candidateID+`","account_id":"`+secondAccount+`"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	var projectAccount string
	if err := db.QueryRow(`SELECT account_id FROM projects WHERE title='选题'`).Scan(&projectAccount); err != nil {
		t.Fatal(err)
	}
	if projectAccount != secondAccount {
		t.Fatalf("project account=%q, want explicit %q", projectAccount, secondAccount)
	}
}
