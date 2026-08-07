package workflow

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/domain"
	"video-production-console/internal/store"
)

type recordingLauncher struct {
	mu              sync.Mutex
	topics, remixes []LaunchTask
	tasks           *store.TaskRepository
	err             error
	taskProjectID   string
}

func (l *recordingLauncher) LaunchTopicCommit(ctx context.Context, in LaunchTask) (domain.CodexTask, error) {
	return l.launch(ctx, in, domain.ActionTopicCommit, &l.topics)
}
func (l *recordingLauncher) LaunchRemixFromTopicCard(ctx context.Context, in LaunchTask) (domain.CodexTask, error) {
	return l.launch(ctx, in, domain.ActionRemixFromTopic, &l.remixes)
}
func (l *recordingLauncher) launch(ctx context.Context, in LaunchTask, action domain.TaskAction, calls *[]LaunchTask) (domain.CodexTask, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return domain.CodexTask{}, l.err
	}
	*calls = append(*calls, in)
	projectID := in.Project.ID
	if l.taskProjectID != "" {
		projectID = l.taskProjectID
	}
	task := domain.CodexTask{ID: uuid.NewString(), ProjectID: &projectID, AccountID: in.Project.AccountID, Type: "workflow", SkillName: "test", PromptSnapshot: "prompt", Action: action, Status: domain.TaskQueued, ModelName: in.ModelName, ReasoningEffort: in.ReasoningEffort, CreatedAt: in.Now}
	if err := l.tasks.CreateV2(ctx, task); err != nil {
		return domain.CodexTask{}, err
	}
	return task, nil
}

func TestRemixWorkflowStartLaunchesTopicOrReadyCardDirectly(t *testing.T) {
	for _, tc := range []struct {
		name  string
		ready bool
	}{
		{name: "missing topic card"}, {name: "ready topic card", ready: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, accountID, projectID, now := remixFixture(t)
			if tc.ready {
				addTopicCard(t, db, projectID, accountID)
			}
			launcher := &recordingLauncher{tasks: store.NewTaskRepository(db)}
			coordinator := NewRemixCoordinator(store.NewWorkflowRepository(db), store.NewProjectRepository(db), store.NewAssetRepository(db), launcher)
			run, err := coordinator.Start(context.Background(), StartRemix{ProjectID: projectID, AccountID: accountID, ModelName: "gpt-5.4", ReasoningEffort: "high", Now: now})
			if err != nil {
				t.Fatal(err)
			}
			if tc.ready {
				if len(launcher.topics) != 0 || len(launcher.remixes) != 1 || run.CurrentStep != domain.WorkflowStepRemix || run.RemixTaskID == nil {
					t.Fatalf("run=%+v topics=%d remixes=%d", run, len(launcher.topics), len(launcher.remixes))
				}
			} else if len(launcher.topics) != 1 || len(launcher.remixes) != 0 || run.TopicTaskID == nil {
				t.Fatalf("run=%+v topics=%d remixes=%d", run, len(launcher.topics), len(launcher.remixes))
			}
			if got := append(launcher.topics, launcher.remixes...)[0]; got.ModelName != "gpt-5.4" || got.ReasoningEffort != "high" {
				t.Fatalf("launch identity=%+v", got)
			}
		})
	}
}

func TestRemixWorkflowRepeatedStartAndTerminalNotificationsAreIdempotent(t *testing.T) {
	db, accountID, projectID, now := remixFixture(t)
	launcher := &recordingLauncher{tasks: store.NewTaskRepository(db)}
	coordinator := NewRemixCoordinator(store.NewWorkflowRepository(db), store.NewProjectRepository(db), store.NewAssetRepository(db), launcher)
	first, err := coordinator.Start(context.Background(), StartRemix{ProjectID: projectID, AccountID: accountID, ModelName: "gpt-5.4", ReasoningEffort: "high", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	second, err := coordinator.Start(context.Background(), StartRemix{ProjectID: projectID, AccountID: accountID, ModelName: "other", ReasoningEffort: "low", Now: now.Add(time.Second)})
	if err != nil || second.ID != first.ID || len(launcher.topics) != 1 {
		t.Fatalf("second=%+v err=%v launches=%d", second, err, len(launcher.topics))
	}
	addTopicCard(t, db, projectID, accountID)
	topic, _ := store.NewTaskRepository(db).Get(context.Background(), *first.TopicTaskID)
	if err := store.NewTaskRepository(db).UpdateStatus(context.Background(), topic.ID, domain.TaskCompleted, "ok", "", ""); err != nil {
		t.Fatal(err)
	}
	topic, _ = store.NewTaskRepository(db).Get(context.Background(), topic.ID)
	if err := coordinator.AfterTerminal(context.Background(), topic); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.AfterTerminal(context.Background(), topic); err != nil {
		t.Fatal(err)
	}
	if len(launcher.remixes) != 1 {
		t.Fatalf("remix launches=%d", len(launcher.remixes))
	}
	run, _ := store.NewWorkflowRepository(db).ByTask(context.Background(), topic.ID)
	remix, _ := store.NewTaskRepository(db).Get(context.Background(), *run.RemixTaskID)
	_ = store.NewTaskRepository(db).UpdateStatus(context.Background(), remix.ID, domain.TaskCompleted, "ok", "", "")
	remix, _ = store.NewTaskRepository(db).Get(context.Background(), remix.ID)
	if err := coordinator.AfterTerminal(context.Background(), remix); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.AfterTerminal(context.Background(), remix); err != nil {
		t.Fatal(err)
	}
	completed, _ := store.NewWorkflowRepository(db).ByTask(context.Background(), remix.ID)
	if completed.State != domain.WorkflowCompleted {
		t.Fatalf("run=%+v", completed)
	}
}

func TestRemixWorkflowTerminalFailureFailsWithoutLaunchingNextStep(t *testing.T) {
	for _, status := range []domain.TaskStatus{domain.TaskFailed, domain.TaskCanceled, domain.TaskInterrupted} {
		t.Run(string(status), func(t *testing.T) {
			db, accountID, projectID, now := remixFixture(t)
			launcher := &recordingLauncher{tasks: store.NewTaskRepository(db)}
			coordinator := NewRemixCoordinator(store.NewWorkflowRepository(db), store.NewProjectRepository(db), store.NewAssetRepository(db), launcher)
			run, _ := coordinator.Start(context.Background(), StartRemix{ProjectID: projectID, AccountID: accountID, ModelName: "m", ReasoningEffort: "high", Now: now})
			_ = store.NewTaskRepository(db).UpdateStatus(context.Background(), *run.TopicTaskID, status, "", "terminal", "stopped")
			task, _ := store.NewTaskRepository(db).Get(context.Background(), *run.TopicTaskID)
			if err := coordinator.AfterTerminal(context.Background(), task); err != nil {
				t.Fatal(err)
			}
			failed, _ := store.NewWorkflowRepository(db).ByTask(context.Background(), task.ID)
			if failed.State != domain.WorkflowFailed || len(launcher.remixes) != 0 {
				t.Fatalf("run=%+v remixes=%d", failed, len(launcher.remixes))
			}
		})
	}
}

func TestRemixWorkflowObserverFailurePersistsFailedRun(t *testing.T) {
	db, accountID, projectID, now := remixFixture(t)
	launcher := &recordingLauncher{tasks: store.NewTaskRepository(db)}
	coordinator := NewRemixCoordinator(store.NewWorkflowRepository(db), store.NewProjectRepository(db), store.NewAssetRepository(db), launcher)
	run, _ := coordinator.Start(context.Background(), StartRemix{ProjectID: projectID, AccountID: accountID, ModelName: "m", ReasoningEffort: "high", Now: now})
	addTopicCard(t, db, projectID, accountID)
	launcher.err = errors.New("scheduler unavailable")
	_ = store.NewTaskRepository(db).UpdateStatus(context.Background(), *run.TopicTaskID, domain.TaskCompleted, "", "", "")
	task, _ := store.NewTaskRepository(db).Get(context.Background(), *run.TopicTaskID)
	if err := coordinator.AfterTerminal(context.Background(), task); err == nil {
		t.Fatal("expected observer error")
	}
	failed, _ := store.NewWorkflowRepository(db).ByTask(context.Background(), task.ID)
	if failed.State != domain.WorkflowFailed {
		t.Fatalf("run=%+v", failed)
	}
}

func TestRemixWorkflowBindingFailurePersistsFailedRun(t *testing.T) {
	db, accountID, projectID, now := remixFixture(t)
	otherProjectID := uuid.NewString()
	if err := store.NewProjectRepository(db).CreateProject(context.Background(), domain.Project{ID: otherProjectID, AccountID: accountID, Title: "other", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	launcher := &recordingLauncher{tasks: store.NewTaskRepository(db)}
	coordinator := NewRemixCoordinator(store.NewWorkflowRepository(db), store.NewProjectRepository(db), store.NewAssetRepository(db), launcher)
	run, _ := coordinator.Start(context.Background(), StartRemix{ProjectID: projectID, AccountID: accountID, ModelName: "m", ReasoningEffort: "high", Now: now})
	addTopicCard(t, db, projectID, accountID)
	launcher.taskProjectID = otherProjectID
	_ = store.NewTaskRepository(db).UpdateStatus(context.Background(), *run.TopicTaskID, domain.TaskCompleted, "", "", "")
	task, _ := store.NewTaskRepository(db).Get(context.Background(), *run.TopicTaskID)
	if err := coordinator.AfterTerminal(context.Background(), task); err == nil {
		t.Fatal("expected binding error")
	}
	failed, _ := store.NewWorkflowRepository(db).ByTask(context.Background(), task.ID)
	if failed.State != domain.WorkflowFailed {
		t.Fatalf("run=%+v", failed)
	}
}

func remixFixture(t *testing.T) (*sql.DB, string, string, time.Time) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	now := time.Date(2026, 8, 8, 1, 2, 3, 0, time.UTC)
	accountID, projectID := uuid.NewString(), uuid.NewString()
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,'#000','active',?,?)`, accountID, "account", now, now); err != nil {
		t.Fatal(err)
	}
	if err := store.NewProjectRepository(db).CreateProject(context.Background(), domain.Project{ID: projectID, AccountID: accountID, Title: "project", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	return db, accountID, projectID, now
}

func addTopicCard(t *testing.T, db *sql.DB, projectID, accountID string) {
	t.Helper()
	_, err := store.NewAssetRepository(db).AddVersion(context.Background(), store.AddAssetVersion{ProjectID: &projectID, AccountID: accountID, Type: domain.AssetTopicCard, StorageKind: domain.StorageFile, Path: filepath.Join(t.TempDir(), "topic.md"), Filename: "topic.md", MIMEType: "text/markdown", SHA256: "abc"})
	if err != nil {
		t.Fatal(err)
	}
}
