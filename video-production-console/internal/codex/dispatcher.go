package codex

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"video-production-console/internal/domain"
	"video-production-console/internal/store"
)

const (
	TransportLegacyExec = "legacy_exec"
	TransportAppServer  = "app_server"
)

// TaskDispatcher is the durable task dispatch contract. CompositeDispatcher
// resolves transport once at first enqueue; resumes and cancellations always
// use the persisted transport.
type TaskDispatcher interface {
	Enqueue(context.Context, domain.CodexTask) error
	Resume(context.Context, string, string) error
	Cancel(context.Context, string) error
}

// AppServerTaskDispatcher owns only task turns. It must never terminate the
// shared App Server process as part of Cancel.
type AppServerTaskDispatcher interface {
	Enqueue(context.Context, domain.CodexTask) error
	Resume(context.Context, string, string) error
	Cancel(context.Context, domain.CodexTask) error
}

type ProjectSessionResolver interface {
	ResolveProjectMainSession(context.Context, string) (domain.ChatSession, error)
}

// CompositeDispatcher keeps the legacy scheduler intact while moving new
// tasks to the console-owned App Server transport.
type CompositeDispatcher struct {
	tasks    *store.TaskRepository
	legacy   TaskDispatcher
	app      AppServerTaskDispatcher
	projects ProjectSessionResolver
}

// CompositeScheduler preserves the existing Scheduler-facing HTTP and settings
// contracts. Queue metrics and concurrency settings remain owned by the
// legacy scheduler until App Server task scheduling has its own persisted
// queue implementation.
type CompositeScheduler struct {
	*CompositeDispatcher
	legacyScheduler Scheduler
}

func NewCompositeScheduler(tasks *store.TaskRepository, legacy Scheduler, app AppServerTaskDispatcher, resolvers ...ProjectSessionResolver) *CompositeScheduler {
	return &CompositeScheduler{CompositeDispatcher: NewCompositeDispatcher(tasks, legacy, app, resolvers...), legacyScheduler: legacy}
}

func (d *CompositeScheduler) SetLimit(limit int) error {
	if d == nil || d.legacyScheduler == nil {
		return errors.New("legacy Codex scheduler is not configured")
	}
	return d.legacyScheduler.SetLimit(limit)
}

func (d *CompositeScheduler) Snapshot() SchedulerSnapshot {
	if d == nil || d.legacyScheduler == nil {
		return SchedulerSnapshot{}
	}
	return d.legacyScheduler.Snapshot()
}

func (d *CompositeScheduler) Close() {
	if d != nil && d.legacyScheduler != nil {
		d.legacyScheduler.Close()
	}
}

func NewCompositeDispatcher(tasks *store.TaskRepository, legacy TaskDispatcher, app AppServerTaskDispatcher, resolvers ...ProjectSessionResolver) *CompositeDispatcher {
	var resolver ProjectSessionResolver
	if len(resolvers) > 0 {
		resolver = resolvers[0]
	}
	return &CompositeDispatcher{tasks: tasks, legacy: legacy, app: app, projects: resolver}
}

func (d *CompositeDispatcher) Enqueue(ctx context.Context, task domain.CodexTask) error {
	if d == nil || d.tasks == nil {
		return errors.New("Codex task dispatcher is not configured")
	}
	if persisted, err := d.tasks.Get(ctx, task.ID); err == nil {
		task = persisted
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err := d.bindPreparedProjectTask(ctx, &task); err != nil {
		return err
	}
	switch taskTransport(task.Transport) {
	case TransportLegacyExec:
		if d.legacy == nil {
			return errors.New("legacy Codex scheduler is not configured")
		}
		return d.legacy.Enqueue(ctx, task)
	case TransportAppServer:
		if d.app == nil {
			return errors.New("Codex App Server transport is not configured")
		}
		return d.app.Enqueue(ctx, task)
	default:
		return fmt.Errorf("unsupported Codex transport %q", task.Transport)
	}
}

// bindPreparedProjectTask keeps manifest-backed project work on the process
// runner. A shared App Server cannot receive a distinct per-turn environment,
// while the Skills contract requires VIDEO_CONSOLE_TASK_MANIFEST to be injected
// for every formal task. Project chat remains App Server-owned; formal Skill
// execution remains legacy_exec-owned.
func (d *CompositeDispatcher) bindPreparedProjectTask(ctx context.Context, task *domain.CodexTask) error {
	if task == nil || task.ProjectID == nil || strings.TrimSpace(*task.ProjectID) == "" {
		return nil
	}
	if task.Status != domain.TaskQueued {
		return nil
	}
	task.ChatSessionID, task.CodexThreadID, task.CodexTurnID = nil, nil, nil
	task.Transport, task.CompletionPhase = TransportLegacyExec, string(domain.CompletionAgentRunning)
	if _, err := d.tasks.Get(ctx, task.ID); errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	if err := d.tasks.SetTransportMetadata(ctx, task.ID, nil, nil, nil, task.CompletionPhase, task.Transport); err != nil {
		return fmt.Errorf("bind prepared project task transport: %w", err)
	}
	return nil
}

func hasAppServerBinding(task domain.CodexTask) bool {
	return taskTransport(task.Transport) == TransportAppServer &&
		task.ChatSessionID != nil && strings.TrimSpace(*task.ChatSessionID) != "" &&
		task.CodexThreadID != nil && strings.TrimSpace(*task.CodexThreadID) != ""
}

func (d *CompositeDispatcher) Resume(ctx context.Context, taskID, answer string) error {
	task, err := d.task(ctx, taskID)
	if err != nil {
		return err
	}
	switch taskTransport(task.Transport) {
	case TransportLegacyExec:
		if d.legacy == nil {
			return errors.New("legacy Codex scheduler is not configured")
		}
		return d.legacy.Resume(ctx, taskID, answer)
	case TransportAppServer:
		if d.app == nil {
			return errors.New("Codex App Server transport is not configured")
		}
		return d.app.Resume(ctx, taskID, answer)
	default:
		return fmt.Errorf("unsupported persisted Codex transport %q", task.Transport)
	}
}

func (d *CompositeDispatcher) Cancel(ctx context.Context, taskID string) error {
	task, err := d.task(ctx, taskID)
	if err != nil {
		return err
	}
	switch taskTransport(task.Transport) {
	case TransportLegacyExec:
		if d.legacy == nil {
			return errors.New("legacy Codex scheduler is not configured")
		}
		return d.legacy.Cancel(ctx, taskID)
	case TransportAppServer:
		if d.app == nil {
			return errors.New("Codex App Server transport is not configured")
		}
		return d.app.Cancel(ctx, task)
	default:
		return fmt.Errorf("unsupported persisted Codex transport %q", task.Transport)
	}
}

func (d *CompositeDispatcher) task(ctx context.Context, id string) (domain.CodexTask, error) {
	if d == nil || d.tasks == nil {
		return domain.CodexTask{}, errors.New("task repository is not configured")
	}
	return d.tasks.Get(ctx, id)
}

func taskTransport(value string) string {
	if value == "" {
		return TransportLegacyExec
	}
	return value
}
