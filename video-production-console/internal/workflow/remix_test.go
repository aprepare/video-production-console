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
	byStep          map[string]domain.CodexTask
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
	key := in.WorkflowID + ":" + string(action)
	if existing, ok := l.byStep[key]; ok {
		return existing, nil
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
	if l.byStep == nil {
		l.byStep = map[string]domain.CodexTask{}
	}
	l.byStep[key] = task
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

func TestRemixWorkflowReconcileInterruptedBoundTasksAndAllowsRestart(t *testing.T) {
	for _, step := range []string{"topic", "remix"} {
		t.Run(step, func(t *testing.T) {
			db, accountID, projectID, now := remixFixture(t)
			if step == "remix" {
				addTopicCard(t, db, projectID, accountID)
			}
			launcher := &recordingLauncher{tasks: store.NewTaskRepository(db)}
			coordinator := NewRemixCoordinator(store.NewWorkflowRepository(db), store.NewProjectRepository(db), store.NewAssetRepository(db), launcher)
			run, err := coordinator.Start(context.Background(), StartRemix{ProjectID: projectID, AccountID: accountID, ModelName: "m", ReasoningEffort: "high", Now: now})
			if err != nil {
				t.Fatal(err)
			}
			taskID := run.TopicTaskID
			if step == "remix" {
				taskID = run.RemixTaskID
			}
			if err := store.NewTaskRepository(db).UpdateStatus(context.Background(), *taskID, domain.TaskInterrupted, "", "restart", "interrupted"); err != nil {
				t.Fatal(err)
			}
			if err := coordinator.ReconcileTerminalWorkflows(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := coordinator.ReconcileTerminalWorkflows(context.Background()); err != nil {
				t.Fatal(err)
			}
			failed, _ := store.NewWorkflowRepository(db).ByTask(context.Background(), *taskID)
			if failed.State != domain.WorkflowFailed {
				t.Fatalf("run=%+v", failed)
			}
			next, err := coordinator.Start(context.Background(), StartRemix{ProjectID: projectID, AccountID: accountID, ModelName: "m", ReasoningEffort: "high", Now: now.Add(time.Minute)})
			if err != nil {
				t.Fatal(err)
			}
			if next.ID == run.ID {
				t.Fatalf("restart reused failed run %s", run.ID)
			}
		})
	}
}

func TestRemixWorkflowObserverNoOpsForUnrelatedTerminalAndFailsRemixTerminal(t *testing.T) {
	db, accountID, projectID, now := remixFixture(t)
	launcher := &recordingLauncher{tasks: store.NewTaskRepository(db)}
	coordinator := NewRemixCoordinator(store.NewWorkflowRepository(db), store.NewProjectRepository(db), store.NewAssetRepository(db), launcher)
	unrelated := domain.CodexTask{ID: uuid.NewString(), Status: domain.TaskFailed}
	if err := coordinator.AfterTerminal(context.Background(), unrelated); err != nil {
		t.Fatal(err)
	}
	addTopicCard(t, db, projectID, accountID)
	run, err := coordinator.Start(context.Background(), StartRemix{ProjectID: projectID, AccountID: accountID, ModelName: "m", ReasoningEffort: "high", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.NewTaskRepository(db).UpdateStatus(context.Background(), *run.RemixTaskID, domain.TaskFailed, "", "failed", "failed")
	task, _ := store.NewTaskRepository(db).Get(context.Background(), *run.RemixTaskID)
	if err := coordinator.AfterTerminal(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	failed, _ := store.NewWorkflowRepository(db).ByTask(context.Background(), task.ID)
	if failed.State != domain.WorkflowFailed {
		t.Fatalf("run=%+v", failed)
	}
}

func TestRemixWorkflowConcurrentStartCreatesOneEffectiveTask(t *testing.T) {
	db, accountID, projectID, now := remixFixture(t)
	launcher := &recordingLauncher{tasks: store.NewTaskRepository(db)}
	coordinator := NewRemixCoordinator(store.NewWorkflowRepository(db), store.NewProjectRepository(db), store.NewAssetRepository(db), launcher)
	var wg sync.WaitGroup
	runs := make(chan domain.ProjectWorkflowRun, 8)
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			run, err := coordinator.Start(context.Background(), StartRemix{ProjectID: projectID, AccountID: accountID, ModelName: "m", ReasoningEffort: "high", Now: now})
			runs <- run
			errs <- err
		}()
	}
	wg.Wait()
	close(runs)
	close(errs)
	var id string
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for run := range runs {
		if id == "" {
			id = run.ID
		}
		if run.ID != id {
			t.Fatalf("run ids %s %s", id, run.ID)
		}
	}
	if len(launcher.topics) != 1 {
		t.Fatalf("topic launches=%d", len(launcher.topics))
	}
}

func TestRemixWorkflowReconcileBindsPersistedUnboundTopicAndReplaysTerminal(t *testing.T) {
	db, accountID, projectID, now := remixFixture(t)
	launcher := &recordingLauncher{tasks: store.NewTaskRepository(db)}
	coordinator := NewRemixCoordinator(store.NewWorkflowRepository(db), store.NewProjectRepository(db), store.NewAssetRepository(db), launcher)
	run, err := store.NewWorkflowRepository(db).BeginRemix(context.Background(), domain.ProjectWorkflowRun{ID: uuid.NewString(), ProjectID: projectID, AccountID: accountID, Kind: domain.WorkflowRemix, State: domain.WorkflowRunning, CurrentStep: domain.WorkflowStepTopicCard, ModelName: "m", ReasoningEffort: "high", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	project, _ := store.NewProjectRepository(db).GetProject(context.Background(), projectID)
	task, err := launcher.LaunchTopicCommit(context.Background(), LaunchTask{WorkflowID: run.ID, Project: project, ModelName: run.ModelName, ReasoningEffort: run.ReasoningEffort, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.NewTaskRepository(db).UpdateStatus(context.Background(), task.ID, domain.TaskInterrupted, "", "restart", "interrupted")
	if err := coordinator.ReconcileTerminalWorkflows(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ := store.NewWorkflowRepository(db).ByTask(context.Background(), task.ID)
	if got.State != domain.WorkflowFailed {
		t.Fatalf("run=%+v", got)
	}
	if len(launcher.topics) != 1 {
		t.Fatalf("topic launches=%d", len(launcher.topics))
	}
}

func TestRemixWorkflowReconcileCompletesTerminalTopicAndUnadvancedTerminalRemixInOnePass(t *testing.T) {
	db, accountID, projectID, now := remixFixture(t)
	launcher := &recordingLauncher{tasks: store.NewTaskRepository(db)}
	coordinator := NewRemixCoordinator(store.NewWorkflowRepository(db), store.NewProjectRepository(db), store.NewAssetRepository(db), launcher)
	run, err := coordinator.Start(context.Background(), StartRemix{ProjectID: projectID, AccountID: accountID, ModelName: "m", ReasoningEffort: "high", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.NewTaskRepository(db).UpdateStatus(context.Background(), *run.TopicTaskID, domain.TaskCompleted, "ok", "", "")
	addTopicCard(t, db, projectID, accountID)
	project, _ := store.NewProjectRepository(db).GetProject(context.Background(), projectID)
	card := currentTopicCard(t, db, projectID)
	remix, err := launcher.LaunchRemixFromTopicCard(context.Background(), LaunchTask{WorkflowID: run.ID, Project: project, TopicCard: &card, ModelName: run.ModelName, ReasoningEffort: run.ReasoningEffort, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.NewTaskRepository(db).UpdateStatus(context.Background(), remix.ID, domain.TaskCompleted, "ok", "", "")
	if err := coordinator.ReconcileTerminalWorkflows(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ := store.NewWorkflowRepository(db).ByTask(context.Background(), remix.ID)
	if got.State != domain.WorkflowCompleted {
		t.Fatalf("run=%+v", got)
	}
	if err := coordinator.ReconcileTerminalWorkflows(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestRemixWorkflowReconcileErrorWarnsTaskFailsWorkflowAndKeepsTerminalTask(t *testing.T) {
	db, accountID, projectID, now := remixFixture(t)
	launcher := &recordingLauncher{tasks: store.NewTaskRepository(db)}
	coordinator := NewRemixCoordinator(store.NewWorkflowRepository(db), store.NewProjectRepository(db), store.NewAssetRepository(db), launcher)
	run, err := coordinator.Start(context.Background(), StartRemix{ProjectID: projectID, AccountID: accountID, ModelName: "m", ReasoningEffort: "high", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.NewTaskRepository(db).UpdateStatus(context.Background(), *run.TopicTaskID, domain.TaskCompleted, "ok", "", "")
	if err := coordinator.ReconcileTerminalWorkflows(context.Background()); err == nil {
		t.Fatal("expected reconcile error")
	}
	task, _ := store.NewTaskRepository(db).Get(context.Background(), *run.TopicTaskID)
	if task.Status != domain.TaskCompleted {
		t.Fatalf("task=%+v", task)
	}
	failed, _ := store.NewWorkflowRepository(db).ByTask(context.Background(), task.ID)
	if failed.State != domain.WorkflowFailed {
		t.Fatalf("run=%+v", failed)
	}
	events, _ := store.NewTaskRepository(db).Events(context.Background(), task.ID)
	if len(events) == 0 || events[len(events)-1].Kind != "workflow_observer_warning" {
		t.Fatalf("events=%+v", events)
	}
}

func currentTopicCard(t *testing.T, db *sql.DB, projectID string) domain.AssetVersion {
	t.Helper()
	versions, err := store.NewAssetRepository(db).CurrentByProject(context.Background(), projectID)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range versions {
		if v.Type == domain.AssetTopicCard && v.State == domain.AssetReady {
			return v
		}
	}
	t.Fatal("topic card missing")
	return domain.AssetVersion{}
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
