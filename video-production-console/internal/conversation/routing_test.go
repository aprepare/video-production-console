package conversation

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/domain"
	"video-production-console/internal/store"
)

func TestFormalTaskSandboxIncludesThreadAndOutputDirectories(t *testing.T) {
	dataRoot := t.TempDir()
	db, err := store.Open(filepath.Join(t.TempDir(), "routing.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := time.Now().UTC()
	accountID, projectID, taskID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,?,?,?,?)`, accountID, "账号", "#fff", "active", now, now); err != nil {
		t.Fatal(err)
	}
	if err := store.NewProjectRepository(db).CreateProject(t.Context(), domain.Project{ID: projectID, AccountID: accountID, Title: "项目", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.NewTaskRepository(db).CreateV2(t.Context(), domain.CodexTask{ID: taskID, ProjectID: &projectID, AccountID: accountID, Type: "remix", SkillName: "finance-viral-remix", Action: domain.ActionRemixStandard, Status: domain.TaskQueued, PromptSnapshot: "prompt", ModelName: "gpt-test", ReasoningEffort: "high", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	adapter := NewTaskAdapter(store.NewTaskRepository(db), nil, nil, TaskCompletionConfig{DataRoot: dataRoot})
	config, err := adapter.TaskExecutionConfig(t.Context(), taskClientKeyPrefix+taskID+":initial")
	if err != nil {
		t.Fatal(err)
	}
	outputDirectory := filepath.Join(dataRoot, "projects", projectID, "tasks", taskID, "output")
	if config.Model != "gpt-test" || config.Effort != "high" || !reflect.DeepEqual(config.WritableRoots, []string{outputDirectory}) {
		t.Fatalf("config=%+v", config)
	}
	threadDirectory := filepath.Join(t.TempDir(), "video-console-tasks", projectID)
	policy := managedTurnSandboxPolicy(threadDirectory, config.WritableRoots[0], threadDirectory)
	if got := policy["writableRoots"]; !reflect.DeepEqual(got, []string{threadDirectory, outputDirectory}) {
		t.Fatalf("writable roots=%v", got)
	}
}

func TestEnsureProjectMainSessionSkipsActiveBinding(t *testing.T) {
	root := t.TempDir()
	db, err := store.Open(filepath.Join(t.TempDir(), "active-routing.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := time.Now().UTC()
	accountID, projectID, sessionID, taskID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,?,?,?,?)`, accountID, "账号", "#fff", "active", now, now); err != nil {
		t.Fatal(err)
	}
	if err := store.NewProjectRepository(db).CreateProject(t.Context(), domain.Project{ID: projectID, AccountID: accountID, Title: "项目", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	threadID := "active-thread"
	repo := store.NewConversationRepository(db)
	if err := repo.CreateSession(t.Context(), domain.ChatSession{ID: sessionID, Title: "旧绑定", Source: "console", Kind: domain.ChatProject, Status: domain.ChatRunning, ProjectID: &projectID, CodexThreadID: &threadID, WorkingDirectory: filepath.Join(root, "old")}); err != nil {
		t.Fatal(err)
	}
	if err := store.NewTaskRepository(db).CreateV2(t.Context(), domain.CodexTask{ID: taskID, ProjectID: &projectID, AccountID: accountID, ChatSessionID: &sessionID, Type: "remix", SkillName: "finance-viral-remix", Action: domain.ActionRemixStandard, Status: domain.TaskQueued, PromptSnapshot: "prompt", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	service := NewService(repo, nil, &serviceRPC{}, []string{root}, nil, ServiceOptions{TaskProjectRoot: root})
	if _, err := service.EnsureProjectMainSession(t.Context(), projectID); !errors.Is(err, store.ErrConversationActive) {
		t.Fatalf("err=%v", err)
	}
	persisted, err := repo.ResolveProjectMainSession(t.Context(), projectID)
	if err != nil || persisted.ID != sessionID || persisted.CodexThreadID == nil || *persisted.CodexThreadID != threadID {
		t.Fatalf("binding=%+v err=%v", persisted, err)
	}
}
