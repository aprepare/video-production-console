package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
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

// persistedIdeaScheduler models the synchronous queue admission step that the
// production scheduler performs before it signals its worker loop.
type persistedIdeaScheduler struct {
	tasks *store.TaskRepository
	err   error
}

func (s persistedIdeaScheduler) Enqueue(ctx context.Context, task domain.CodexTask) error {
	if s.err != nil {
		return s.err
	}
	_, err := s.tasks.AdmitQueuedTask(ctx, task, time.Now().UTC())
	return err
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
	if task, err := store.NewTaskRepository(db).Get(context.Background(), *messages[0].TaskID); err != nil {
		t.Fatalf("referenced task was not persisted: %v", err)
	} else if task.QueuedAt == nil {
		t.Fatalf("referenced task was not admitted: %+v", task)
	}
}

type failingIdeaPreparer struct{ err error }

func (p failingIdeaPreparer) Prepare(context.Context, domain.CodexTask, TaskManifestRequest) error {
	return p.err
}

type capturingIdeaPreparer struct {
	request TaskManifestRequest
}

func (p *capturingIdeaPreparer) Prepare(_ context.Context, _ domain.CodexTask, request TaskManifestRequest) error {
	p.request = request
	return nil
}

func TestIdeaMessagePassesExplicitBaokuanSourcesToManifestPreparation(t *testing.T) {
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
	if err := repo.CreateSession(t.Context(), domain.IdeaSession{ID: sessionID, AccountID: &accountID, Title: "test", Status: "planning", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	preparer := &capturingIdeaPreparer{}
	h := NewIdeasHandler(db, persistedIdeaScheduler{tasks: store.NewTaskRepository(db)}, preparer, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/ideas/"+sessionID+"/messages", strings.NewReader(`{"content":"plan","source_feed_ids":["14986230628414069221"]}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "prepared task manifest was not persisted") {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	if !slices.Equal(preparer.request.SourceFeedIDs, []string{"14986230628414069221"}) {
		t.Fatalf("source feed IDs=%v", preparer.request.SourceFeedIDs)
	}
}

func TestIdeaMessageDoesNotPublishPreparationOrEnqueueFailure(t *testing.T) {
	for _, test := range []struct {
		name       string
		preparer   TaskManifestPreparer
		scheduler  persistedIdeaScheduler
		wantStatus int
	}{
		{name: "preparation", preparer: failingIdeaPreparer{err: errors.New("manifest invalid")}, wantStatus: http.StatusConflict},
		{name: "enqueue", scheduler: persistedIdeaScheduler{err: errors.New("queue unavailable")}, wantStatus: http.StatusInternalServerError},
	} {
		t.Run(test.name, func(t *testing.T) {
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
			ideas := store.NewIdeaRepository(db)
			sessionID := uuid.NewString()
			if err := ideas.CreateSession(context.Background(), domain.IdeaSession{ID: sessionID, AccountID: &accountID, Title: "test", Status: "planning", CreatedAt: now, UpdatedAt: now}); err != nil {
				t.Fatal(err)
			}
			test.scheduler.tasks = store.NewTaskRepository(db)
			handler := NewIdeasHandler(db, test.scheduler, test.preparer, nil)
			request := httptest.NewRequest(http.MethodPost, "/api/ideas/"+sessionID+"/messages", strings.NewReader(`{"content":"测试选题"}`))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			messages, err := ideas.Messages(context.Background(), sessionID)
			if err != nil || len(messages) != 0 {
				t.Fatalf("messages=%+v err=%v", messages, err)
			}
		})
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
	var selected struct {
		Project struct {
			Stage domain.ProjectStage `json:"stage"`
		} `json:"project"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &selected); err != nil {
		t.Fatal(err)
	}
	if selected.Project.Stage != domain.StageScript {
		t.Fatalf("response project stage=%q, want %q", selected.Project.Stage, domain.StageScript)
	}
	// Decoding cannot catch a casing regression here: encoding/json matches
	// object keys case-insensitively, so a bare domain.Project (PascalCase
	// keys) still satisfies the assertion above while the browser reads
	// project.id and project.account_id as undefined. Assert the raw keys.
	var rawSelection struct {
		Project map[string]json.RawMessage `json:"project"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &rawSelection); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"id", "account_id", "title", "stage", "publication_status", "created_at"} {
		if _, ok := rawSelection.Project[key]; !ok {
			t.Fatalf("selection project is missing key %q; keys=%v", key, slices.Sorted(maps.Keys(rawSelection.Project)))
		}
	}
	for _, key := range []string{"ID", "AccountID", "Title", "Stage", "Status"} {
		if _, ok := rawSelection.Project[key]; ok {
			t.Fatalf("selection project still exposes Go field name %q", key)
		}
	}
	var projectAccount string
	var projectStage domain.ProjectStage
	if err := db.QueryRow(`SELECT account_id,stage FROM projects WHERE title='选题'`).Scan(&projectAccount, &projectStage); err != nil {
		t.Fatal(err)
	}
	if projectAccount != secondAccount {
		t.Fatalf("project account=%q, want explicit %q", projectAccount, secondAccount)
	}
	if projectStage != domain.StageScript {
		t.Fatalf("project stage=%q, want %q", projectStage, domain.StageScript)
	}
}
