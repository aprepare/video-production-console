package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/codex"
	"video-production-console/internal/domain"
	"video-production-console/internal/store"
	"video-production-console/internal/taskmodel"
	"video-production-console/internal/workflow"
)

type workflowTaskLauncher struct {
	db        *sql.DB
	scheduler codex.Scheduler
	preparer  TaskManifestPreparer
	models    TaskModelResolver
	mu        sync.Mutex
}

func NewWorkflowTaskLauncher(db *sql.DB, scheduler codex.Scheduler, preparer TaskManifestPreparer, models TaskModelResolver) workflow.TaskLauncher {
	return &workflowTaskLauncher{db: db, scheduler: scheduler, preparer: preparer, models: models}
}

func (l *workflowTaskLauncher) LaunchTopicCommit(ctx context.Context, in workflow.LaunchTask) (domain.CodexTask, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.validate(in); err != nil {
		return domain.CodexTask{}, err
	}
	selection, err := findProjectTopicSelection(ctx, l.db, in.Project)
	if err != nil {
		return domain.CodexTask{}, err
	}
	return enqueueTopicCommit(ctx, l.db, l.scheduler, l.preparer, l.models, in.Project, selection, topicCommitLaunch{model: taskmodel.Selection{Model: in.ModelName, ReasoningEffort: in.ReasoningEffort}, now: in.Now, taskID: workflowStepTaskID(in.WorkflowID, "topic")})
}

func (l *workflowTaskLauncher) LaunchRemixFromTopicCard(ctx context.Context, in workflow.LaunchTask) (domain.CodexTask, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.validate(in); err != nil {
		return domain.CodexTask{}, err
	}
	if in.TopicCard == nil || in.TopicCard.Type != domain.AssetTopicCard || in.TopicCard.State != domain.AssetReady || in.TopicCard.ProjectID == nil || *in.TopicCard.ProjectID != in.Project.ID || in.TopicCard.AccountID != in.Project.AccountID {
		return domain.CodexTask{}, errors.New("ready project topic card is required")
	}
	tasks := store.NewTaskRepository(l.db)
	model, err := resolveTaskModel(ctx, l.models, taskmodel.Selection{Model: in.ModelName, ReasoningEffort: in.ReasoningEffort})
	if err != nil {
		return domain.CodexTask{}, err
	}
	taskID := workflowStepTaskID(in.WorkflowID, "remix")
	if existing, readErr := tasks.Get(ctx, taskID); readErr == nil {
		if existing.Action != domain.ActionRemixFromTopic || existing.ProjectID == nil || *existing.ProjectID != in.Project.ID || existing.AccountID != in.Project.AccountID || existing.ModelName != model.Model || existing.ReasoningEffort != model.ReasoningEffort {
			return domain.CodexTask{}, errors.New("workflow remix task identity conflict")
		}
		return existing, nil
	} else if !errors.Is(readErr, sql.ErrNoRows) {
		return domain.CodexTask{}, readErr
	}
	now := in.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	projectID := in.Project.ID
	task := domain.CodexTask{
		ID: taskID, ProjectID: &projectID, AccountID: in.Project.AccountID,
		Type: "remix", SkillName: "finance-viral-remix", Action: domain.ActionRemixFromTopic,
		Status: domain.TaskQueued, PromptSnapshot: "Create a remix from the approved topic card.",
		ModelName: model.Model, ReasoningEffort: model.ReasoningEffort, CreatedAt: now,
	}
	if err := l.preparer.Prepare(ctx, task, TaskManifestRequest{TopicCardPath: in.TopicCard.Path}); err != nil {
		return domain.CodexTask{}, err
	}
	if err := l.scheduler.Enqueue(ctx, task); err != nil {
		return domain.CodexTask{}, err
	}
	return task, nil
}

func workflowStepTaskID(workflowID, step string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("video-production-console/workflow/"+workflowID+"/"+step)).String()
}

func (l *workflowTaskLauncher) validate(in workflow.LaunchTask) error {
	if l == nil || l.db == nil || l.scheduler == nil || l.preparer == nil {
		return errors.New("workflow task launcher is unavailable")
	}
	if strings.TrimSpace(in.WorkflowID) == "" || strings.TrimSpace(in.Project.ID) == "" || strings.TrimSpace(in.Project.AccountID) == "" || strings.TrimSpace(in.ModelName) == "" || strings.TrimSpace(in.ReasoningEffort) == "" {
		return fmt.Errorf("workflow launch identity and model are required")
	}
	return nil
}
