package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/codexapp"
	"video-production-console/internal/domain"
	"video-production-console/internal/store"
)

type serviceRPC struct {
	calls           atomic.Int32
	lastStartParams map[string]any
}

func (r *serviceRPC) Call(_ context.Context, method string, params any, result any) error {
	if method == "thread/start" {
		r.lastStartParams, _ = params.(map[string]any)
		sequence := r.calls.Add(1)
		data, _ := json.Marshal(map[string]string{"threadId": "thread-" + string(rune('0'+sequence))})
		return json.Unmarshal(data, result)
	}
	return nil
}

type cleanupRPC struct{}

func (cleanupRPC) Call(_ context.Context, method string, _ any, result any) error {
	if method == "thread/archive" {
		return errors.New("archive unavailable")
	}
	data, _ := json.Marshal(map[string]string{"threadId": "duplicate-thread"})
	return json.Unmarshal(data, result)
}

func TestPersistFailureRecordsDurableOrphanCleanup(t *testing.T) {
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "cleanup.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	threadID := "duplicate-thread"
	repo := store.NewConversationRepository(db)
	accountID, projectID := uuid.NewString(), uuid.NewString()
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,?,?,?,?)`, accountID, "account", "#fff", "active", now, now); err != nil {
		t.Fatal(err)
	}
	if err := store.NewProjectRepository(db).CreateProject(t.Context(), domain.Project{ID: projectID, AccountID: accountID, Title: "project", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	winnerThread := "winner-thread"
	if err := repo.CreateSession(t.Context(), domain.ChatSession{ID: uuid.NewString(), Title: "existing", Source: "console", Kind: domain.ChatProject, Status: domain.ChatIdle, ProjectID: &projectID, CodexThreadID: &winnerThread, WorkingDirectory: filepath.Join(root, "projects", projectID)}); err != nil {
		t.Fatal(err)
	}
	service := NewService(repo, nil, cleanupRPC{}, []string{root}, nil, ServiceOptions{DataRoot: root})
	if _, err := service.createSession(t.Context(), CreateSessionInput{Kind: domain.ChatProject, ProjectID: &projectID}); err == nil {
		t.Fatal("duplicate project main persistence unexpectedly succeeded")
	}
	intents, err := repo.PendingThreadCleanup(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(intents) != 1 || intents[0].ThreadID != threadID {
		t.Fatalf("cleanup intents=%+v", intents)
	}
}

func TestProjectCreateReusesEnsuredMainSession(t *testing.T) {
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	accountID, projectID := uuid.NewString(), uuid.NewString()
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,?,?,?,?)`, accountID, "account", "#fff", "active", now, now); err != nil {
		t.Fatal(err)
	}
	if err := store.NewProjectRepository(db).CreateProject(t.Context(), domain.Project{ID: projectID, AccountID: accountID, Title: "project", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	rpc := &serviceRPC{}
	service := NewService(store.NewConversationRepository(db), nil, rpc, []string{root}, nil, ServiceOptions{DataRoot: root})
	first, err := service.Create(t.Context(), CreateSessionInput{Kind: domain.ChatProject, ProjectID: &projectID, Title: "ignored"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Create(t.Context(), CreateSessionInput{Kind: domain.ChatProject, ProjectID: &projectID, Title: "also ignored"})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || rpc.calls.Load() != 1 {
		t.Fatalf("sessions=%s/%s thread starts=%d", first.ID, second.ID, rpc.calls.Load())
	}
	wantDirectory := filepath.Join(root, "projects", projectID)
	if first.WorkingDirectory != wantDirectory {
		t.Fatalf("working directory=%q want %q", first.WorkingDirectory, wantDirectory)
	}
	if got := rpc.lastStartParams["sandbox"]; got != "workspace-write" {
		t.Fatalf("thread sandbox=%v, want workspace-write", got)
	}
	if got := rpc.lastStartParams["approvalPolicy"]; got != "never" {
		t.Fatalf("thread approval policy=%v, want never", got)
	}
}

type staleProjectThreadRPC struct{ starts atomic.Int32 }

func (r *staleProjectThreadRPC) Call(_ context.Context, method string, _ any, result any) error {
	switch method {
	case "thread/read":
		return &codexapp.RPCError{Code: -32600, Message: "thread not found: stale-thread"}
	case "thread/start":
		r.starts.Add(1)
		data, _ := json.Marshal(map[string]string{"threadId": "replacement-thread"})
		return json.Unmarshal(data, result)
	default:
		return nil
	}
}

func TestEnsureProjectMainSessionReplacesMissingAppServerThread(t *testing.T) {
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	accountID, projectID := uuid.NewString(), uuid.NewString()
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,?,?,?,?)`, accountID, "account", "#fff", "active", now, now); err != nil {
		t.Fatal(err)
	}
	if err := store.NewProjectRepository(db).CreateProject(t.Context(), domain.Project{ID: projectID, AccountID: accountID, Title: "project", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	staleThread := "stale-thread"
	if err := store.NewConversationRepository(db).CreateSession(t.Context(), domain.ChatSession{ID: uuid.NewString(), Title: "stale", Source: "console", Kind: domain.ChatProject, Status: domain.ChatIdle, ProjectID: &projectID, CodexThreadID: &staleThread, WorkingDirectory: filepath.Join(root, "projects", projectID)}); err != nil {
		t.Fatal(err)
	}
	rpc := &staleProjectThreadRPC{}
	service := NewService(store.NewConversationRepository(db), nil, rpc, []string{root}, nil, ServiceOptions{DataRoot: root})

	session, err := service.EnsureProjectMainSession(t.Context(), projectID)
	if err != nil {
		t.Fatal(err)
	}
	if session.CodexThreadID == nil || *session.CodexThreadID != "replacement-thread" {
		t.Fatalf("thread=%v, want replacement-thread", session.CodexThreadID)
	}
	if rpc.starts.Load() != 1 {
		t.Fatalf("thread starts=%d, want 1", rpc.starts.Load())
	}
}
