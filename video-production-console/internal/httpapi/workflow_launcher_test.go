package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/codex"
	"video-production-console/internal/domain"
	"video-production-console/internal/store"
	"video-production-console/internal/taskmodel"
	"video-production-console/internal/workflow"
)

type workflowLauncherScheduler struct {
	mu           sync.Mutex
	tasks        []domain.CodexTask
	err          error
	beforeReturn func()
}

func (s *workflowLauncherScheduler) Enqueue(_ context.Context, task domain.CodexTask) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks = append(s.tasks, task)
	if s.beforeReturn != nil {
		s.beforeReturn()
	}
	return s.err
}
func (*workflowLauncherScheduler) Resume(context.Context, string, string) error { return nil }
func (*workflowLauncherScheduler) Cancel(context.Context, string) error         { return nil }
func (*workflowLauncherScheduler) SetLimit(int) error                           { return nil }
func (*workflowLauncherScheduler) Snapshot() codex.SchedulerSnapshot {
	return codex.SchedulerSnapshot{}
}
func (*workflowLauncherScheduler) Close() {}

type workflowLauncherPreparer struct {
	mu       sync.Mutex
	repo     *store.TaskRepository
	requests []TaskManifestRequest
}

func (p *workflowLauncherPreparer) Prepare(ctx context.Context, task domain.CodexTask, req TaskManifestRequest) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, req)
	_, err := p.repo.EnsurePreparedTask(ctx, task, "workflow-snapshot", filepath.Join("manifests", task.ID+".json"))
	return err
}

type workflowModelResolver struct{ got taskmodel.Selection }

func (r *workflowModelResolver) ResolveTaskModel(_ context.Context, in taskmodel.Selection) (taskmodel.Selection, error) {
	r.got = in
	return taskmodel.Selection{Model: "normalized-model", ReasoningEffort: "xhigh"}, nil
}

func TestWorkflowTaskLauncherCreatesRemixWithWorkflowIdentityModelAndManifest(t *testing.T) {
	db, project, card := workflowLauncherFixture(t)
	scheduler := &workflowLauncherScheduler{}
	preparer := &workflowLauncherPreparer{repo: store.NewTaskRepository(db)}
	launcher := NewWorkflowTaskLauncher(db, scheduler, preparer, nil)
	now := time.Date(2026, 8, 8, 3, 4, 5, 0, time.UTC)
	task, err := launcher.LaunchRemixFromTopicCard(context.Background(), workflow.LaunchTask{WorkflowID: uuid.NewString(), Project: project, TopicCard: &card, ModelName: "gpt-5.4", ReasoningEffort: "high", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if task.Action != domain.ActionRemixFromTopic || task.Type != "remix" || task.SkillName != "finance-viral-remix" || task.ProjectID == nil || *task.ProjectID != project.ID || task.AccountID != project.AccountID || task.ModelName != "gpt-5.4" || task.ReasoningEffort != "high" {
		t.Fatalf("task=%+v", task)
	}
	if len(preparer.requests) != 1 || preparer.requests[0].TopicCardPath != card.Path {
		t.Fatalf("requests=%+v", preparer.requests)
	}
	if len(scheduler.tasks) != 1 || scheduler.tasks[0].ID != task.ID {
		t.Fatalf("scheduled=%+v", scheduler.tasks)
	}
	persisted, err := store.NewTaskRepository(db).Get(context.Background(), task.ID)
	if err != nil || persisted.Action != domain.ActionRemixFromTopic {
		t.Fatalf("persisted=%+v err=%v", persisted, err)
	}
}

func TestWorkflowTaskLauncherReusesExistingActiveRemix(t *testing.T) {
	db, project, card := workflowLauncherFixture(t)
	scheduler := &workflowLauncherScheduler{}
	preparer := &workflowLauncherPreparer{repo: store.NewTaskRepository(db)}
	launcher := NewWorkflowTaskLauncher(db, scheduler, preparer, nil)
	in := workflow.LaunchTask{WorkflowID: uuid.NewString(), Project: project, TopicCard: &card, ModelName: "gpt-5.4", ReasoningEffort: "high", Now: time.Now().UTC()}
	first, err := launcher.LaunchRemixFromTopicCard(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	second, err := launcher.LaunchRemixFromTopicCard(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || len(scheduler.tasks) != 1 {
		t.Fatalf("first=%s second=%s enqueues=%d", first.ID, second.ID, len(scheduler.tasks))
	}
}

func TestWorkflowTaskLauncherReadyCardUsesResolverAndDoesNotAbsorbUnrelatedRemix(t *testing.T) {
	db, project, card := workflowLauncherFixture(t)
	repo := store.NewTaskRepository(db)
	projectID := project.ID
	unrelated := domain.CodexTask{ID: uuid.NewString(), ProjectID: &projectID, AccountID: project.AccountID, Type: "remix", SkillName: "finance-viral-remix", Action: domain.ActionRemixFromTopic, Status: domain.TaskQueued, PromptSnapshot: "manual", ModelName: "normalized-model", ReasoningEffort: "xhigh", CreatedAt: time.Now().UTC()}
	if err := repo.CreateV2(context.Background(), unrelated); err != nil {
		t.Fatal(err)
	}
	scheduler := &workflowLauncherScheduler{}
	preparer := &workflowLauncherPreparer{repo: repo}
	resolver := &workflowModelResolver{}
	launcher := NewWorkflowTaskLauncher(db, scheduler, preparer, resolver)
	in := workflow.LaunchTask{WorkflowID: uuid.NewString(), Project: project, TopicCard: &card, ModelName: " raw-model ", ReasoningEffort: " HIGH ", Now: time.Now().UTC()}
	first, err := launcher.LaunchRemixFromTopicCard(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == unrelated.ID || first.ModelName != "normalized-model" || first.ReasoningEffort != "xhigh" || resolver.got.Model != " raw-model " {
		t.Fatalf("task=%+v resolver=%+v", first, resolver.got)
	}
	const n = 8
	var wg sync.WaitGroup
	ids := make(chan string, n)
	errs := make(chan error, n)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			task, e := launcher.LaunchRemixFromTopicCard(context.Background(), in)
			if e == nil {
				ids <- task.ID
			}
			errs <- e
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	for id := range ids {
		if id != first.ID {
			t.Fatalf("duplicate identity %s != %s", id, first.ID)
		}
	}
	if len(scheduler.tasks) != 1 {
		t.Fatalf("enqueues=%d", len(scheduler.tasks))
	}
}

func TestWorkflowTaskLauncherTopicCommitPreservesWorkflowModel(t *testing.T) {
	db, project, _ := workflowLauncherFixture(t)
	addWorkflowTopicSelection(t, db, project)
	scheduler := &workflowLauncherScheduler{}
	preparer := &workflowLauncherPreparer{repo: store.NewTaskRepository(db)}
	launcher := NewWorkflowTaskLauncher(db, scheduler, preparer, nil)
	now := time.Date(2026, 8, 8, 6, 7, 8, 0, time.UTC)
	task, err := launcher.LaunchTopicCommit(context.Background(), workflow.LaunchTask{WorkflowID: uuid.NewString(), Project: project, ModelName: "gpt-5.4", ReasoningEffort: "high", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if task.Action != domain.ActionTopicCommit || task.ModelName != "gpt-5.4" || task.ReasoningEffort != "high" || !task.CreatedAt.Equal(now) || len(preparer.requests) != 1 || preparer.requests[0].CandidateID == "" || len(scheduler.tasks) != 1 {
		t.Fatalf("task=%+v requests=%+v scheduled=%d", task, preparer.requests, len(scheduler.tasks))
	}
}

func TestWorkflowTaskLauncherTreatsPreparedTaskAsDurableWhenSchedulerNotifyFails(t *testing.T) {
	for _, step := range []string{"topic", "remix"} {
		t.Run(step, func(t *testing.T) {
			db, project, card := workflowLauncherFixture(t)
			if step == "topic" {
				addWorkflowTopicSelection(t, db, project)
			}
			scheduler := &workflowLauncherScheduler{err: errors.New("wake failed")}
			preparer := &workflowLauncherPreparer{repo: store.NewTaskRepository(db)}
			launcher := NewWorkflowTaskLauncher(db, scheduler, preparer, nil)
			in := workflow.LaunchTask{WorkflowID: uuid.NewString(), Project: project, TopicCard: &card, ModelName: "m", ReasoningEffort: "high", Now: time.Now().UTC()}
			var task domain.CodexTask
			var err error
			if step == "topic" {
				task, err = launcher.LaunchTopicCommit(context.Background(), in)
			} else {
				task, err = launcher.LaunchRemixFromTopicCard(context.Background(), in)
			}
			if err != nil {
				t.Fatal(err)
			}
			persisted, readErr := store.NewTaskRepository(db).Get(context.Background(), task.ID)
			if readErr != nil || persisted.Status != domain.TaskQueued {
				t.Fatalf("task=%+v err=%v", persisted, readErr)
			}
			events, _ := store.NewTaskRepository(db).Events(context.Background(), task.ID)
			if len(events) == 0 || events[len(events)-1].Kind != "workflow_scheduler_notify_warning" {
				t.Fatalf("events=%+v", events)
			}
		})
	}
}

func TestWorkflowTaskLauncherRejectsDeterministicIdentityConflict(t *testing.T) {
	db, project, card := workflowLauncherFixture(t)
	repo := store.NewTaskRepository(db)
	workflowID := uuid.NewString()
	projectID := project.ID
	conflict := domain.CodexTask{ID: workflowStepTaskID(workflowID, "remix"), ProjectID: &projectID, AccountID: project.AccountID, Type: "remix", SkillName: "finance-viral-remix", Action: domain.ActionRemixEnhanced, Status: domain.TaskQueued, PromptSnapshot: "conflict", ModelName: "m", ReasoningEffort: "high", CreatedAt: time.Now().UTC()}
	if _, err := repo.EnsurePreparedTask(context.Background(), conflict, "workflow-snapshot", "conflict.json"); err != nil {
		t.Fatal(err)
	}
	launcher := NewWorkflowTaskLauncher(db, &workflowLauncherScheduler{}, &workflowLauncherPreparer{repo: repo}, nil)
	if _, err := launcher.LaunchRemixFromTopicCard(context.Background(), workflow.LaunchTask{WorkflowID: workflowID, Project: project, TopicCard: &card, ModelName: "m", ReasoningEffort: "high", Now: time.Now().UTC()}); err == nil {
		t.Fatal("expected identity conflict")
	}
}

func TestWorkflowTaskLauncherNotifyFailureDoesNotFailWorkflow(t *testing.T) {
	db, project, _ := workflowLauncherFixture(t)
	scheduler := &workflowLauncherScheduler{err: errors.New("wake failed")}
	repo := store.NewTaskRepository(db)
	launcher := NewWorkflowTaskLauncher(db, scheduler, &workflowLauncherPreparer{repo: repo}, nil)
	coordinator := workflow.NewRemixCoordinator(store.NewWorkflowRepository(db), store.NewProjectRepository(db), store.NewAssetRepository(db), launcher)
	run, err := coordinator.Start(context.Background(), workflow.StartRemix{ProjectID: project.ID, AccountID: project.AccountID, ModelName: "m", ReasoningEffort: "high", Now: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if run.State != domain.WorkflowRunning || run.CurrentStep != domain.WorkflowStepRemix || run.RemixTaskID == nil {
		t.Fatalf("run=%+v", run)
	}
}

func TestWorkflowTaskLauncherConfirmsAndBindsDurableTaskAfterRequestCancellation(t *testing.T) {
	for _, step := range []string{"topic", "remix"} {
		t.Run(step, func(t *testing.T) {
			db, project, _ := workflowLauncherFixture(t)
			if step == "topic" {
				if _, err := db.Exec(`UPDATE asset_versions SET state='stale' WHERE project_id=?`, project.ID); err != nil {
					t.Fatal(err)
				}
				addWorkflowTopicSelection(t, db, project)
			}
			ctx, cancel := context.WithCancel(context.Background())
			scheduler := &workflowLauncherScheduler{err: errors.New("wake failed"), beforeReturn: cancel}
			repo := store.NewTaskRepository(db)
			launcher := NewWorkflowTaskLauncher(db, scheduler, &workflowLauncherPreparer{repo: repo}, nil)
			coordinator := workflow.NewRemixCoordinator(store.NewWorkflowRepository(db), store.NewProjectRepository(db), store.NewAssetRepository(db), launcher)
			run, err := coordinator.Start(ctx, workflow.StartRemix{ProjectID: project.ID, AccountID: project.AccountID, ModelName: "m", ReasoningEffort: "high", Now: time.Now().UTC()})
			if err != nil {
				t.Fatal(err)
			}
			var taskID *string
			if step == "topic" {
				taskID = run.TopicTaskID
			} else {
				taskID = run.RemixTaskID
			}
			if run.State != domain.WorkflowRunning || taskID == nil {
				t.Fatalf("run=%+v", run)
			}
			if _, _, err := repo.PreparedManifest(context.Background(), *taskID); err != nil {
				t.Fatalf("prepared task %s: %v", *taskID, err)
			}
			persisted, err := store.NewWorkflowRepository(db).ActiveForProject(context.Background(), project.ID, domain.WorkflowRemix)
			if err != nil || (step == "topic" && persisted.TopicTaskID == nil) || (step == "remix" && persisted.RemixTaskID == nil) {
				t.Fatalf("persisted=%+v err=%v", persisted, err)
			}
		})
	}
}

func TestWorkflowTaskLauncherRepairsQueuedDeterministicTaskWithoutManifest(t *testing.T) {
	for _, step := range []string{"topic", "remix"} {
		t.Run(step, func(t *testing.T) {
			db, project, card := workflowLauncherFixture(t)
			if step == "topic" {
				addWorkflowTopicSelection(t, db, project)
			}
			workflowID := uuid.NewString()
			task := workflowLauncherDeterministicTask(workflowID, step, project, time.Now().UTC())
			if err := store.NewTaskRepository(db).CreateV2(context.Background(), task); err != nil {
				t.Fatal(err)
			}
			scheduler := &workflowLauncherScheduler{}
			preparer := &workflowLauncherPreparer{repo: store.NewTaskRepository(db)}
			launcher := NewWorkflowTaskLauncher(db, scheduler, preparer, nil)
			in := workflow.LaunchTask{WorkflowID: workflowID, Project: project, TopicCard: &card, ModelName: "m", ReasoningEffort: "high", Now: task.CreatedAt}
			var got domain.CodexTask
			var err error
			if step == "topic" {
				got, err = launcher.LaunchTopicCommit(context.Background(), in)
			} else {
				got, err = launcher.LaunchRemixFromTopicCard(context.Background(), in)
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.ID != task.ID || len(preparer.requests) != 1 || len(scheduler.tasks) != 1 {
				t.Fatalf("got=%+v prepares=%d enqueues=%d", got, len(preparer.requests), len(scheduler.tasks))
			}
			if _, _, err := store.NewTaskRepository(db).PreparedManifest(context.Background(), task.ID); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestWorkflowTaskLauncherDoesNotPrepareStartedOrTerminalDeterministicTask(t *testing.T) {
	for _, step := range []string{"topic", "remix"} {
		for _, status := range []domain.TaskStatus{domain.TaskRunning, domain.TaskCompleted} {
			t.Run(step+"/"+string(status), func(t *testing.T) {
				db, project, card := workflowLauncherFixture(t)
				if step == "topic" {
					addWorkflowTopicSelection(t, db, project)
				}
				workflowID := uuid.NewString()
				task := workflowLauncherDeterministicTask(workflowID, step, project, time.Now().UTC())
				if err := store.NewTaskRepository(db).CreateV2(context.Background(), task); err != nil {
					t.Fatal(err)
				}
				if err := store.NewTaskRepository(db).UpdateStatus(context.Background(), task.ID, status, "", "", ""); err != nil {
					t.Fatal(err)
				}
				scheduler := &workflowLauncherScheduler{}
				preparer := &workflowLauncherPreparer{repo: store.NewTaskRepository(db)}
				launcher := NewWorkflowTaskLauncher(db, scheduler, preparer, nil)
				in := workflow.LaunchTask{WorkflowID: workflowID, Project: project, TopicCard: &card, ModelName: "m", ReasoningEffort: "high", Now: task.CreatedAt}
				var got domain.CodexTask
				var err error
				if step == "topic" {
					got, err = launcher.LaunchTopicCommit(context.Background(), in)
				} else {
					got, err = launcher.LaunchRemixFromTopicCard(context.Background(), in)
				}
				if err != nil || got.Status != status || len(preparer.requests) != 0 || len(scheduler.tasks) != 0 {
					t.Fatalf("got=%+v err=%v prepares=%d enqueues=%d", got, err, len(preparer.requests), len(scheduler.tasks))
				}
				if _, _, err := store.NewTaskRepository(db).PreparedManifest(context.Background(), task.ID); !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("manifest err=%v", err)
				}
			})
		}
	}
}

func workflowLauncherDeterministicTask(workflowID, step string, project domain.Project, now time.Time) domain.CodexTask {
	projectID := project.ID
	task := domain.CodexTask{ID: workflowStepTaskID(workflowID, step), ProjectID: &projectID, AccountID: project.AccountID, Status: domain.TaskQueued, ModelName: "m", ReasoningEffort: "high", CreatedAt: now}
	if step == "topic" {
		task.Type, task.SkillName, task.Action, task.PromptSnapshot = "topic_commit", "finance-topic-selector", domain.ActionTopicCommit, "topic"
	} else {
		task.Type, task.SkillName, task.Action, task.PromptSnapshot = "remix", "finance-viral-remix", domain.ActionRemixFromTopic, "remix"
	}
	return task
}

func workflowLauncherFixture(t *testing.T) (*sql.DB, domain.Project, domain.AssetVersion) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "launcher.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	now := time.Now().UTC()
	accountID, projectID := uuid.NewString(), uuid.NewString()
	_, err = db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,'#fff','active',?,?)`, accountID, "a", now, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO skill_snapshots(id,name,path,sha256,files_json,modified_at,created_at) VALUES('workflow-snapshot','finance-viral-remix','skill','abc','[]',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	project := domain.Project{ID: projectID, AccountID: accountID, Title: "p", CreatedAt: now, UpdatedAt: now}
	if err := store.NewProjectRepository(db).CreateProject(context.Background(), project); err != nil {
		t.Fatal(err)
	}
	card, err := store.NewAssetRepository(db).AddVersion(context.Background(), store.AddAssetVersion{ProjectID: &projectID, AccountID: accountID, Type: domain.AssetTopicCard, Path: filepath.Join(t.TempDir(), "topic.md"), Filename: "topic.md", MIMEType: "text/markdown", SHA256: "abc"})
	if err != nil {
		t.Fatal(err)
	}
	return db, project, card
}

func addWorkflowTopicSelection(t *testing.T, db *sql.DB, project domain.Project) {
	t.Helper()
	now := time.Now().UTC()
	sourceTaskID, sessionID, candidateID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	source := domain.CodexTask{ID: sourceTaskID, AccountID: project.AccountID, Type: "topic_select", SkillName: "finance-topic-selector", Action: domain.ActionTopicBrainstorm, Status: domain.TaskCompleted, PromptSnapshot: "prompt", ModelName: "source", ReasoningEffort: "low", CreatedAt: now}
	if err := store.NewTaskRepository(db).CreateV2(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "candidates.json")
	if _, err := db.Exec(`INSERT INTO task_artifacts(id,task_id,kind,path,filename,mime_type,size,sha256,created_at) VALUES(?,?, 'topic_candidates',?,'candidates.json','application/json',0,'abc',?)`, uuid.NewString(), sourceTaskID, path, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO idea_sessions(id,account_id,title,status,created_at,updated_at) VALUES(?,?,?,'planning',?,?)`, sessionID, project.AccountID, project.Title, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO idea_candidates(id,session_id,task_id,position,title,summary,score,source,selected,created_at) VALUES(?,?,?,1,?, '',0,'test',1,?)`, candidateID, sessionID, sourceTaskID, project.Title, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE idea_sessions SET status='selected',selected_id=?,project_id=? WHERE id=?`, candidateID, project.ID, sessionID); err != nil {
		t.Fatal(err)
	}
}
