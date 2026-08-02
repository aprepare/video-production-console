package codex

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os/exec"
	"sync"

	"video-production-console/internal/domain"
	"video-production-console/internal/store"
)

type SchedulerSnapshot struct{ Limit, Running, Queued int }
type Scheduler interface {
	Enqueue(context.Context, domain.CodexTask) error
	Resume(context.Context, string, string) error
	Cancel(context.Context, string) error
	SetLimit(int) error
	Snapshot() SchedulerSnapshot
	Close()
}
type CommandFactory func(domain.CodexTask) (*exec.Cmd, string, error)
type ResumeCommandFactory func(domain.CodexTask, string) (*exec.Cmd, string, error)

type scheduled struct {
	cancel    context.CancelFunc
	cmd       *exec.Cmd
	cancelled bool
}
type TaskScheduler struct {
	tasks         *store.TaskRepository
	makeCommand   CommandFactory
	makeResume    ResumeCommandFactory
	broadcast     func(Event)
	broadcastTask func(string, Event)
	mu            sync.Mutex
	limit         int
	running       map[string]*scheduled
	projectLocks  map[string]string
	wake          chan struct{}
	stop          chan struct{}
	done          chan struct{}
}

func NewScheduler(tasks *store.TaskRepository, limit int, makeCommand CommandFactory, makeResume ResumeCommandFactory, broadcast func(Event)) (*TaskScheduler, error) {
	if tasks == nil || makeCommand == nil {
		return nil, fmt.Errorf("scheduler requires task repository and command factory")
	}
	if limit < 1 || limit > 4 {
		return nil, fmt.Errorf("concurrency limit must be between 1 and 4")
	}
	s := &TaskScheduler{tasks: tasks, makeCommand: makeCommand, makeResume: makeResume, broadcast: broadcast, limit: limit, running: map[string]*scheduled{}, projectLocks: map[string]string{}, wake: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{})}
	go s.loop()
	return s, nil
}
func (s *TaskScheduler) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *TaskScheduler) loop() {
	defer close(s.done)
	for {
		select {
		case <-s.wake:
			s.dispatch()
		case <-s.stop:
			return
		default:
			s.dispatch()
			select {
			case <-s.wake:
			case <-s.stop:
				return
			}
		}
	}
}
func (s *TaskScheduler) dispatch() {
	s.mu.Lock()
	slots := s.limit - len(s.running)
	s.mu.Unlock()
	if slots <= 0 {
		return
	}
	tasks, err := s.tasks.List(context.Background(), "", domain.TaskQueued)
	if err != nil {
		return
	}
	for _, t := range tasks {
		if slots <= 0 {
			break
		}
		key := ""
		if t.ProjectID != nil {
			key = *t.ProjectID
		}
		s.mu.Lock()
		_, busy := s.projectLocks[key]
		if busy {
			s.mu.Unlock()
			continue
		}
		s.projectLocks[key] = t.ID
		s.mu.Unlock()
		var cmd *exec.Cmd
		var root string
		var err error
		if t.CodexSessionID != nil && *t.CodexSessionID != "" && s.makeResume != nil {
			msgs, _ := s.tasks.Messages(context.Background(), t.ID)
			answer := ""
			for i := len(msgs) - 1; i >= 0; i-- {
				if msgs[i].Role == "user" {
					answer = msgs[i].Content
					break
				}
			}
			cmd, root, err = s.makeResume(t, answer)
		} else {
			cmd, root, err = s.makeCommand(t)
		}
		if err != nil {
			_ = s.tasks.UpdateStatus(context.Background(), t.ID, domain.TaskFailed, "", "command_build_failed", err.Error())
			s.mu.Lock()
			delete(s.projectLocks, key)
			s.mu.Unlock()
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		s.mu.Lock()
		s.running[t.ID] = &scheduled{cancel: cancel, cmd: cmd}
		s.mu.Unlock()
		slots--
		go s.run(ctx, t, cmd, root, key)
	}
}
func (s *TaskScheduler) run(ctx context.Context, t domain.CodexTask, cmd *exec.Cmd, root, key string) {
	broadcast := s.broadcast
	s.mu.Lock()
	taskBroadcast := s.broadcastTask
	s.mu.Unlock()
	if taskBroadcast != nil {
		broadcast = func(e Event) { taskBroadcast(t.ID, e) }
	}
	r := NewRunner(cmd, s.tasks, t.ID, root, broadcast)
	err := r.Run(ctx)
	s.mu.Lock()
	item := s.running[t.ID]
	cancelled := item != nil && item.cancelled
	delete(s.running, t.ID)
	delete(s.projectLocks, key)
	s.mu.Unlock()
	if cancelled {
		_ = s.tasks.UpdateStatus(context.Background(), t.ID, domain.TaskCancelled, "", "cancelled", "task cancelled")
	} else if err != nil { /* Runner persists failure details. */
	}
	s.signal()
}

// SetTaskBroadcast connects persisted runner events to a task-aware realtime hub.
func (s *TaskScheduler) SetTaskBroadcast(fn func(string, Event)) {
	s.mu.Lock()
	s.broadcastTask = fn
	s.mu.Unlock()
}
func (s *TaskScheduler) Enqueue(ctx context.Context, t domain.CodexTask) error {
	if t.ID == "" {
		return errors.New("task id is required")
	}
	if t.Status == "" {
		t.Status = domain.TaskQueued
	}
	if _, err := s.tasks.Get(ctx, t.ID); errors.Is(err, sql.ErrNoRows) {
		if err := s.tasks.Create(ctx, t); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if t.Status != domain.TaskQueued {
		return fmt.Errorf("task is not queued")
	}
	s.signal()
	return nil
}
func (s *TaskScheduler) Resume(ctx context.Context, id, answer string) error {
	t, err := s.tasks.Get(ctx, id)
	if err != nil {
		return err
	}
	if t.Status != domain.TaskWaitingInput {
		return fmt.Errorf("task is not waiting for input")
	}
	if t.CodexSessionID == nil || *t.CodexSessionID == "" {
		return fmt.Errorf("task has no codex session")
	}
	if s.makeResume == nil {
		return fmt.Errorf("resume is not configured")
	}
	if err := s.tasks.AddMessage(ctx, domain.TaskMessage{TaskID: id, Role: "user", Content: answer}); err != nil {
		return err
	}
	if err := s.tasks.UpdateStatus(ctx, id, domain.TaskQueued, "", "", ""); err != nil {
		return err
	}
	// The resume command is selected when dispatch sees the task's session.
	s.signal()
	return nil
}
func (s *TaskScheduler) Cancel(ctx context.Context, id string) error {
	t, err := s.tasks.Get(ctx, id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	item := s.running[id]
	if item != nil {
		item.cancelled = true
		item.cancel()
		if item.cmd != nil && item.cmd.Process != nil {
			terminateProcess(item.cmd)
		}
		s.mu.Unlock()
		_ = s.tasks.AppendEvent(ctx, id, domain.TaskEvent{Kind: "cancel_requested", Level: "warning", DisplayText: "Task cancellation requested"})
		return nil
	}
	s.mu.Unlock()
	if t.Status == domain.TaskQueued || t.Status == domain.TaskWaitingInput {
		_ = s.tasks.AppendEvent(ctx, id, domain.TaskEvent{Kind: "cancelled", Level: "warning", DisplayText: "Task cancelled"})
		return s.tasks.UpdateStatus(ctx, id, domain.TaskCancelled, "", "cancelled", "task cancelled")
	}
	return fmt.Errorf("task cannot be cancelled in status %s", t.Status)
}
func (s *TaskScheduler) SetLimit(limit int) error {
	if limit < 1 || limit > 4 {
		return fmt.Errorf("concurrency limit must be between 1 and 4")
	}
	s.mu.Lock()
	s.limit = limit
	s.mu.Unlock()
	s.signal()
	return nil
}
func (s *TaskScheduler) Snapshot() SchedulerSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	q, _ := s.tasks.List(context.Background(), "", domain.TaskQueued)
	return SchedulerSnapshot{Limit: s.limit, Running: len(s.running), Queued: len(q)}
}
func (s *TaskScheduler) Close() {
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
	<-s.done
	s.mu.Lock()
	for _, v := range s.running {
		v.cancel()
		if v.cmd != nil && v.cmd.Process != nil {
			terminateProcess(v.cmd)
		}
	}
	s.mu.Unlock()
}
