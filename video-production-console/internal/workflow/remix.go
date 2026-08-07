package workflow

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/domain"
	"video-production-console/internal/store"
)

type LaunchTask struct {
	WorkflowID      string
	Project         domain.Project
	TopicCard       *domain.AssetVersion
	ModelName       string
	ReasoningEffort string
	Now             time.Time
}

type TaskLauncher interface {
	LaunchTopicCommit(context.Context, LaunchTask) (domain.CodexTask, error)
	LaunchRemixFromTopicCard(context.Context, LaunchTask) (domain.CodexTask, error)
}

type StartRemix struct {
	ProjectID, AccountID       string
	ModelName, ReasoningEffort string
	Now                        time.Time
}

type RemixCoordinator struct {
	workflows *store.WorkflowRepository
	projects  *store.ProjectRepository
	assets    *store.AssetRepository
	launcher  TaskLauncher
	mu        sync.Mutex
}

func NewRemixCoordinator(workflows *store.WorkflowRepository, projects *store.ProjectRepository, assets *store.AssetRepository, launcher TaskLauncher) *RemixCoordinator {
	return &RemixCoordinator{workflows: workflows, projects: projects, assets: assets, launcher: launcher}
}

func (c *RemixCoordinator) Start(ctx context.Context, in StartRemix) (domain.ProjectWorkflowRun, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.workflows == nil || c.projects == nil || c.assets == nil || c.launcher == nil {
		return domain.ProjectWorkflowRun{}, errors.New("remix coordinator is not configured")
	}
	now := in.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	project, err := c.projects.GetProject(ctx, in.ProjectID)
	if err != nil {
		return domain.ProjectWorkflowRun{}, err
	}
	if project.AccountID != in.AccountID {
		return domain.ProjectWorkflowRun{}, errors.New("remix project account mismatch")
	}
	run, err := c.workflows.BeginRemix(ctx, domain.ProjectWorkflowRun{
		ID: uuid.NewString(), ProjectID: project.ID, AccountID: project.AccountID,
		Kind: domain.WorkflowRemix, State: domain.WorkflowRunning, CurrentStep: domain.WorkflowStepTopicCard,
		ModelName: in.ModelName, ReasoningEffort: in.ReasoningEffort, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		return run, err
	}
	if run.TopicTaskID != nil || run.RemixTaskID != nil || run.State != domain.WorkflowRunning {
		return run, nil
	}
	versions, err := c.assets.CurrentByProject(ctx, project.ID)
	if err != nil {
		return c.fail(ctx, run, "asset_lookup_failed", err, now)
	}
	var topicCard *domain.AssetVersion
	for i := range versions {
		if versions[i].Type == domain.AssetTopicCard && versions[i].State == domain.AssetReady {
			card := versions[i]
			topicCard = &card
			break
		}
	}
	launch := LaunchTask{WorkflowID: run.ID, Project: project, TopicCard: topicCard, ModelName: run.ModelName, ReasoningEffort: run.ReasoningEffort, Now: now}
	if topicCard != nil {
		task, launchErr := c.launcher.LaunchRemixFromTopicCard(ctx, launch)
		if launchErr != nil {
			return c.fail(ctx, run, "remix_launch_failed", launchErr, now)
		}
		advanced, advanceErr := c.workflows.AdvanceExistingTopicCardToRemix(ctx, run.ID, task.ID, now)
		if advanceErr != nil {
			return c.fail(ctx, run, "remix_bind_failed", advanceErr, now)
		}
		return advanced, nil
	}
	task, launchErr := c.launcher.LaunchTopicCommit(ctx, launch)
	if launchErr != nil {
		return c.fail(ctx, run, "topic_launch_failed", launchErr, now)
	}
	bound, bindErr := c.workflows.BindTopicTask(ctx, run.ID, task.ID, now)
	if bindErr != nil {
		return c.fail(ctx, run, "topic_bind_failed", bindErr, now)
	}
	return bound, nil
}

func (c *RemixCoordinator) AfterTerminal(ctx context.Context, task domain.CodexTask) error {
	if !terminal(task.Status) {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	run, err := c.workflows.ByTask(ctx, task.ID)
	if errors.Is(err, store.ErrWorkflowNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if run.State != domain.WorkflowRunning {
		return nil
	}
	now := time.Now().UTC()
	if task.Status != domain.TaskCompleted {
		_, err = c.workflows.Fail(ctx, run.ID, string(task.Status), terminalMessage(task), now)
		return err
	}
	if run.TopicTaskID != nil && *run.TopicTaskID == task.ID {
		if run.CurrentStep != domain.WorkflowStepTopicCard {
			return nil
		}
		versions, readErr := c.assets.CurrentByProject(ctx, run.ProjectID)
		if readErr != nil {
			_, _ = c.workflows.Fail(ctx, run.ID, "asset_lookup_failed", readErr.Error(), now)
			return readErr
		}
		var card *domain.AssetVersion
		for i := range versions {
			if versions[i].Type == domain.AssetTopicCard && versions[i].State == domain.AssetReady {
				value := versions[i]
				card = &value
				break
			}
		}
		if card == nil {
			cause := errors.New("completed topic task did not persist a ready topic_card")
			_, _ = c.workflows.Fail(ctx, run.ID, "topic_card_missing", cause.Error(), now)
			return cause
		}
		project, readErr := c.projects.GetProject(ctx, run.ProjectID)
		if readErr != nil {
			_, _ = c.workflows.Fail(ctx, run.ID, "project_lookup_failed", readErr.Error(), now)
			return readErr
		}
		remix, launchErr := c.launcher.LaunchRemixFromTopicCard(ctx, LaunchTask{WorkflowID: run.ID, Project: project, TopicCard: card, ModelName: run.ModelName, ReasoningEffort: run.ReasoningEffort, Now: now})
		if launchErr != nil {
			_, _ = c.workflows.Fail(ctx, run.ID, "remix_launch_failed", launchErr.Error(), now)
			return launchErr
		}
		_, err = c.workflows.AdvanceToRemix(ctx, run.ID, remix.ID, now)
		if err != nil {
			_, failErr := c.workflows.Fail(ctx, run.ID, "remix_bind_failed", err.Error(), now)
			return errors.Join(err, failErr)
		}
		return nil
	}
	if run.RemixTaskID != nil && *run.RemixTaskID == task.ID && run.CurrentStep == domain.WorkflowStepRemix {
		_, err = c.workflows.Complete(ctx, run.ID, now)
	}
	return err
}

func (c *RemixCoordinator) fail(ctx context.Context, run domain.ProjectWorkflowRun, code string, cause error, now time.Time) (domain.ProjectWorkflowRun, error) {
	failed, failErr := c.workflows.Fail(ctx, run.ID, code, cause.Error(), now)
	return failed, errors.Join(cause, failErr)
}

func terminal(status domain.TaskStatus) bool {
	switch status {
	case domain.TaskCompleted, domain.TaskFailed, domain.TaskCanceled, domain.TaskCancelled, domain.TaskInterrupted:
		return true
	}
	return false
}

func terminalMessage(task domain.CodexTask) string {
	if task.ErrorMessage != nil && *task.ErrorMessage != "" {
		return *task.ErrorMessage
	}
	return fmt.Sprintf("task ended with status %s", task.Status)
}
