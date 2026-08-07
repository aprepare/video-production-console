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

func (c *RemixCoordinator) ReconcileTerminalWorkflows(ctx context.Context) error {
	runs, err := c.workflows.Running(ctx, domain.WorkflowRemix)
	if err != nil {
		return err
	}
	var reconcileErr error
	for _, run := range runs {
		c.mu.Lock()
		_, taskID, runErr := c.resumeRunLocked(ctx, run, time.Now().UTC())
		c.mu.Unlock()
		if runErr == nil {
			continue
		}
		_, failErr := c.workflows.Fail(ctx, run.ID, "reconcile_failed", runErr.Error(), time.Now().UTC())
		runErr = errors.Join(runErr, failErr)
		if taskID != "" {
			warningErr := store.NewTaskRepository(c.workflows.DB()).AppendEvent(ctx, taskID, domain.TaskEvent{Kind: "workflow_observer_warning", Level: "warning", DisplayText: runErr.Error()})
			runErr = errors.Join(runErr, warningErr)
		}
		reconcileErr = errors.Join(reconcileErr, runErr)
	}
	return reconcileErr
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
	resumed, _, resumeErr := c.resumeRunLocked(ctx, run, now)
	return resumed, resumeErr
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
	_, _, err = c.resumeRunLocked(ctx, run, time.Now().UTC())
	return err
}

func (c *RemixCoordinator) resumeRunLocked(ctx context.Context, run domain.ProjectWorkflowRun, now time.Time) (domain.ProjectWorkflowRun, string, error) {
	tasks := store.NewTaskRepository(c.workflows.DB())
	for run.State == domain.WorkflowRunning {
		project, err := c.projects.GetProject(ctx, run.ProjectID)
		if err != nil {
			failed, failErr := c.fail(ctx, run, "project_lookup_failed", err, now)
			return failed, "", failErr
		}
		if run.CurrentStep == domain.WorkflowStepTopicCard {
			card, cardErr := c.readyTopicCard(ctx, run.ProjectID)
			if cardErr != nil {
				failed, failErr := c.fail(ctx, run, "asset_lookup_failed", cardErr, now)
				return failed, "", failErr
			}
			if run.TopicTaskID == nil && card != nil {
				remix, launchErr := c.launcher.LaunchRemixFromTopicCard(ctx, LaunchTask{WorkflowID: run.ID, Project: project, TopicCard: card, ModelName: run.ModelName, ReasoningEffort: run.ReasoningEffort, Now: now})
				if launchErr != nil {
					failed, failErr := c.fail(ctx, run, "remix_launch_failed", launchErr, now)
					return failed, "", failErr
				}
				advanced, bindErr := c.workflows.AdvanceExistingTopicCardToRemix(ctx, run.ID, remix.ID, now)
				if bindErr != nil {
					failed, failErr := c.fail(ctx, run, "remix_bind_failed", bindErr, now)
					return failed, remix.ID, failErr
				}
				run = advanced
				continue
			}
			if run.TopicTaskID == nil {
				topic, launchErr := c.launcher.LaunchTopicCommit(ctx, LaunchTask{WorkflowID: run.ID, Project: project, ModelName: run.ModelName, ReasoningEffort: run.ReasoningEffort, Now: now})
				if launchErr != nil {
					failed, failErr := c.fail(ctx, run, "topic_launch_failed", launchErr, now)
					return failed, "", failErr
				}
				bound, bindErr := c.workflows.BindTopicTask(ctx, run.ID, topic.ID, now)
				if bindErr != nil {
					failed, failErr := c.fail(ctx, run, "topic_bind_failed", bindErr, now)
					return failed, topic.ID, failErr
				}
				run = bound
			}
			task, readErr := tasks.Get(ctx, *run.TopicTaskID)
			if readErr != nil {
				failed, failErr := c.fail(ctx, run, "topic_task_lookup_failed", readErr, now)
				return failed, *run.TopicTaskID, failErr
			}
			if !terminal(task.Status) {
				return run, task.ID, nil
			}
			if task.Status != domain.TaskCompleted {
				failed, failErr := c.workflows.Fail(ctx, run.ID, string(task.Status), terminalMessage(task), now)
				return failed, task.ID, failErr
			}
			if card == nil {
				cause := errors.New("completed topic task did not persist a ready topic_card")
				failed, failErr := c.fail(ctx, run, "topic_card_missing", cause, now)
				return failed, task.ID, failErr
			}
			remix, launchErr := c.launcher.LaunchRemixFromTopicCard(ctx, LaunchTask{WorkflowID: run.ID, Project: project, TopicCard: card, ModelName: run.ModelName, ReasoningEffort: run.ReasoningEffort, Now: now})
			if launchErr != nil {
				failed, failErr := c.fail(ctx, run, "remix_launch_failed", launchErr, now)
				return failed, task.ID, failErr
			}
			advanced, bindErr := c.workflows.AdvanceToRemix(ctx, run.ID, remix.ID, now)
			if bindErr != nil {
				failed, failErr := c.fail(ctx, run, "remix_bind_failed", bindErr, now)
				return failed, remix.ID, failErr
			}
			run = advanced
			continue
		}
		if run.CurrentStep == domain.WorkflowStepRemix && run.RemixTaskID != nil {
			task, readErr := tasks.Get(ctx, *run.RemixTaskID)
			if readErr != nil {
				failed, failErr := c.fail(ctx, run, "remix_task_lookup_failed", readErr, now)
				return failed, *run.RemixTaskID, failErr
			}
			if !terminal(task.Status) {
				return run, task.ID, nil
			}
			if task.Status == domain.TaskCompleted {
				completed, completeErr := c.workflows.Complete(ctx, run.ID, now)
				return completed, task.ID, completeErr
			}
			failed, failErr := c.workflows.Fail(ctx, run.ID, string(task.Status), terminalMessage(task), now)
			return failed, task.ID, failErr
		}
		cause := errors.New("workflow current step has no bound task")
		failed, failErr := c.fail(ctx, run, "workflow_task_missing", cause, now)
		return failed, "", failErr
	}
	return run, "", nil
}

func (c *RemixCoordinator) readyTopicCard(ctx context.Context, projectID string) (*domain.AssetVersion, error) {
	versions, err := c.assets.CurrentByProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	for i := range versions {
		if versions[i].Type == domain.AssetTopicCard && versions[i].State == domain.AssetReady {
			card := versions[i]
			return &card, nil
		}
	}
	return nil, nil
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
