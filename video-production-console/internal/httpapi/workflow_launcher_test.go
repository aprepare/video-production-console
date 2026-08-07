package httpapi

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/codex"
	"video-production-console/internal/domain"
	"video-production-console/internal/store"
	"video-production-console/internal/workflow"
)

type workflowLauncherScheduler struct{ tasks []domain.CodexTask }

func (s *workflowLauncherScheduler) Enqueue(_ context.Context, task domain.CodexTask) error {
	s.tasks = append(s.tasks, task)
	return nil
}
func (*workflowLauncherScheduler) Resume(context.Context, string, string) error { return nil }
func (*workflowLauncherScheduler) Cancel(context.Context, string) error         { return nil }
func (*workflowLauncherScheduler) SetLimit(int) error                           { return nil }
func (*workflowLauncherScheduler) Snapshot() codex.SchedulerSnapshot {
	return codex.SchedulerSnapshot{}
}
func (*workflowLauncherScheduler) Close() {}

type workflowLauncherPreparer struct {
	repo     *store.TaskRepository
	requests []TaskManifestRequest
}

func (p *workflowLauncherPreparer) Prepare(ctx context.Context, task domain.CodexTask, req TaskManifestRequest) error {
	p.requests = append(p.requests, req)
	return p.repo.CreateV2(ctx, task)
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
