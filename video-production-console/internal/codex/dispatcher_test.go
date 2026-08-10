package codex

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/domain"
	"video-production-console/internal/store"
)

type dispatcherLegacyStub struct{ enqueued []domain.CodexTask }

func (s *dispatcherLegacyStub) Enqueue(_ context.Context, task domain.CodexTask) error {
	s.enqueued = append(s.enqueued, task)
	return nil
}
func (s *dispatcherLegacyStub) Resume(context.Context, string, string) error { return nil }
func (s *dispatcherLegacyStub) Cancel(context.Context, string) error         { return nil }

type dispatcherAppStub struct{ enqueued []domain.CodexTask }

func (s *dispatcherAppStub) Enqueue(_ context.Context, task domain.CodexTask) error {
	s.enqueued = append(s.enqueued, task)
	return nil
}
func (s *dispatcherAppStub) Resume(context.Context, string, string) error { return nil }
func (s *dispatcherAppStub) Cancel(context.Context, domain.CodexTask) error {
	return nil
}

type dispatcherProjectResolverStub struct {
	session domain.ChatSession
	calls   int
}

func (s *dispatcherProjectResolverStub) ResolveProjectMainSession(_ context.Context, _ string) (domain.ChatSession, error) {
	s.calls++
	return s.session, nil
}

func TestCompositeDispatcherKeepsPreparedProjectTaskOnLegacyExec(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/console.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	now := time.Now().UTC()
	accountID, projectID, taskID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,'#fff','active',?,?)`, accountID, "account", now, now); err != nil {
		t.Fatal(err)
	}
	if err := store.NewProjectRepository(db).CreateProject(context.Background(), domain.Project{ID: projectID, AccountID: accountID, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	threadID := "thread-123"
	projectSession := domain.ChatSession{
		ID: uuid.NewString(), Title: "Project " + projectID, Source: "console",
		Kind: domain.ChatProject, Status: domain.ChatIdle, ProjectID: &projectID,
		CodexThreadID: &threadID, WorkingDirectory: t.TempDir(), CreatedAt: now, UpdatedAt: now,
	}
	if err := store.NewConversationRepository(db).CreateSession(context.Background(), projectSession); err != nil {
		t.Fatal(err)
	}
	repo := store.NewTaskRepository(db)
	task := domain.CodexTask{
		ID: taskID, ProjectID: &projectID, AccountID: accountID,
		Type: "montage", SkillName: "jianying-montage-draft", Action: domain.ActionMontageExecute,
		Status: domain.TaskQueued, PromptSnapshot: "prompt", CreatedAt: now,
	}
	if err := repo.CreateV2(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO skill_snapshots(id,name,path,sha256,files_json,modified_at,created_at) VALUES(?,?,?,?,?,?,?)`, "snapshot-"+taskID, "skill", t.TempDir(), "sha", "[]", now, now); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetPreparedManifest(context.Background(), taskID, "snapshot-"+taskID, "manifest.json"); err != nil {
		t.Fatal(err)
	}

	resolver := &dispatcherProjectResolverStub{session: projectSession}
	legacy, app := &dispatcherLegacyStub{}, &dispatcherAppStub{}
	dispatcher := NewCompositeDispatcher(repo, legacy, app, resolver)
	if err := dispatcher.Enqueue(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if resolver.calls != 0 {
		t.Fatalf("resolver calls=%d, want 0", resolver.calls)
	}
	if len(legacy.enqueued) != 1 {
		t.Fatalf("legacy enqueues=%d, want 1", len(legacy.enqueued))
	}
	if len(app.enqueued) != 0 {
		t.Fatalf("App Server enqueues=%d, want 0", len(app.enqueued))
	}
	got, err := repo.Get(context.Background(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Transport != TransportLegacyExec || got.ChatSessionID != nil || got.CodexThreadID != nil {
		t.Fatalf("persisted task transport binding = %#v", got)
	}
}

func TestCompositeDispatcherMovesQueuedPreparedProjectTaskOffAppServer(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/console.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	now := time.Now().UTC()
	accountID, projectID, taskID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,'#fff','active',?,?)`, accountID, "account", now, now); err != nil {
		t.Fatal(err)
	}
	if err := store.NewProjectRepository(db).CreateProject(context.Background(), domain.Project{ID: projectID, AccountID: accountID, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	oldThread, replacementThread := "thread-old", "thread-replacement"
	oldSession := domain.ChatSession{ID: uuid.NewString(), Title: "old", Source: "console_fork", Kind: domain.ChatGeneral, Status: domain.ChatIdle, CodexThreadID: &oldThread, WorkingDirectory: t.TempDir(), CreatedAt: now, UpdatedAt: now}
	projectSession := domain.ChatSession{ID: uuid.NewString(), Title: "Project " + projectID, Source: "console", Kind: domain.ChatProject, Status: domain.ChatIdle, ProjectID: &projectID, CodexThreadID: &replacementThread, WorkingDirectory: t.TempDir(), CreatedAt: now, UpdatedAt: now}
	conversations := store.NewConversationRepository(db)
	if err := conversations.CreateSession(context.Background(), oldSession); err != nil {
		t.Fatal(err)
	}
	if err := conversations.CreateSession(context.Background(), projectSession); err != nil {
		t.Fatal(err)
	}
	repo := store.NewTaskRepository(db)
	task := domain.CodexTask{ID: taskID, ProjectID: &projectID, AccountID: accountID, Type: "montage", SkillName: "jianying-montage-draft", Action: domain.ActionMontageExecute, Status: domain.TaskQueued, PromptSnapshot: "prompt", ChatSessionID: &oldSession.ID, CodexThreadID: &oldThread, CompletionPhase: string(domain.CompletionAgentRunning), Transport: TransportAppServer, CreatedAt: now}
	if err := repo.CreateV2(context.Background(), task); err != nil {
		t.Fatal(err)
	}

	resolver := &dispatcherProjectResolverStub{session: projectSession}
	legacy, app := &dispatcherLegacyStub{}, &dispatcherAppStub{}
	dispatcher := NewCompositeDispatcher(repo, legacy, app, resolver)
	if err := dispatcher.Enqueue(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if resolver.calls != 1 {
		t.Fatalf("resolver calls=%d, want 1", resolver.calls)
	}
	if len(app.enqueued) != 1 || len(legacy.enqueued) != 0 {
		t.Fatalf("legacy=%d app=%d, want App Server only", len(legacy.enqueued), len(app.enqueued))
	}
	if app.enqueued[0].ChatSessionID == nil || *app.enqueued[0].ChatSessionID != projectSession.ID || app.enqueued[0].CodexThreadID == nil || *app.enqueued[0].CodexThreadID != replacementThread {
		t.Fatalf("App Server task binding=%#v", app.enqueued[0])
	}
	persisted, err := repo.Get(context.Background(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.ChatSessionID == nil || *persisted.ChatSessionID != projectSession.ID || persisted.CodexThreadID == nil || *persisted.CodexThreadID != replacementThread {
		t.Fatalf("persisted task binding=%#v", persisted)
	}
	if err := dispatcher.Enqueue(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if resolver.calls != 2 {
		t.Fatalf("resolver calls after idempotent enqueue=%d, want 2", resolver.calls)
	}
}
