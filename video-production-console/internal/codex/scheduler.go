package codex

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"video-production-console/internal/domain"
	"video-production-console/internal/store"
	"video-production-console/internal/taskcompletion"
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
	cancel context.CancelFunc
	cmd    *exec.Cmd
}
type TaskScheduler struct {
	tasks              *store.TaskRepository
	makeCommand        CommandFactory
	makeResume         ResumeCommandFactory
	broadcast          func(Event)
	broadcastTask      func(string, Event)
	completionGate     taskcompletion.Gate
	completionObserver taskcompletion.Observer
	mu                 sync.Mutex
	limit              int
	running            map[string]*scheduled
	projectLocks       map[string]string
	wake               chan struct{}
	stop               chan struct{}
	done               chan struct{}
	runWG              sync.WaitGroup
	closeOnce          sync.Once
	closeDone          chan struct{}
	closed             bool
}

func NewScheduler(tasks *store.TaskRepository, limit int, makeCommand CommandFactory, makeResume ResumeCommandFactory, broadcast func(Event)) (*TaskScheduler, error) {
	if tasks == nil || makeCommand == nil {
		return nil, fmt.Errorf("scheduler requires task repository and command factory")
	}
	if limit < 1 || limit > 4 {
		return nil, fmt.Errorf("concurrency limit must be between 1 and 4")
	}
	s := &TaskScheduler{tasks: tasks, makeCommand: makeCommand, makeResume: makeResume, broadcast: broadcast, limit: limit, running: map[string]*scheduled{}, projectLocks: map[string]string{}, wake: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{}), closeDone: make(chan struct{})}
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
		// Project-less planning tasks are independent. Giving all of them the
		// empty lock key accidentally serialized every account's topic work.
		// Use the task ID unless a real project needs exclusive asset writes.
		key := t.ID
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
			changed, failErr := s.tasks.FailQueuedTask(context.Background(), t.ID, "command_build_failed", err.Error(), time.Now().UTC())
			if failErr == nil && changed {
				_ = s.notifyTerminalObserver(context.Background(), t.ID)
			}
			s.mu.Lock()
			delete(s.projectLocks, key)
			s.mu.Unlock()
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		startedAt := time.Now().UTC()
		if _, err := s.tasks.ClaimLegacyStart(context.Background(), t.ID, startedAt); err != nil {
			cancel()
			changed, failErr := s.tasks.FailQueuedTask(context.Background(), t.ID, "task_start_failed", err.Error(), startedAt)
			if failErr == nil && changed {
				_ = s.notifyTerminalObserver(context.Background(), t.ID)
			}
			s.mu.Lock()
			delete(s.projectLocks, key)
			s.mu.Unlock()
			continue
		}
		s.mu.Lock()
		s.running[t.ID] = &scheduled{cancel: cancel, cmd: cmd}
		s.mu.Unlock()
		slots--
		s.runWG.Add(1)
		go func() {
			defer s.runWG.Done()
			s.run(ctx, t, cmd, root, key)
		}()
	}
}
func (s *TaskScheduler) run(ctx context.Context, t domain.CodexTask, cmd *exec.Cmd, root, key string) {
	broadcast := s.broadcast
	s.mu.Lock()
	taskBroadcast := s.broadcastTask
	completionGate := s.completionGate
	completionObserver := s.completionObserver
	s.mu.Unlock()
	if taskBroadcast != nil {
		broadcast = func(e Event) { taskBroadcast(t.ID, e) }
	}
	r := NewRunner(cmd, s.tasks, t.ID, root, broadcast)
	r.CompletionGate = completionGate
	r.CompletionObserver = completionObserver
	_ = r.Run(ctx) // Runner persists terminal failures and observer warnings.
	s.mu.Lock()
	delete(s.running, t.ID)
	delete(s.projectLocks, key)
	s.mu.Unlock()
	s.signal()
}

// SetTaskBroadcast connects persisted runner events to a task-aware realtime hub.
func (s *TaskScheduler) SetTaskBroadcast(fn func(string, Event)) {
	s.mu.Lock()
	s.broadcastTask = fn
	s.mu.Unlock()
}

func (s *TaskScheduler) SetCompletionGate(gate taskcompletion.Gate) {
	s.mu.Lock()
	s.completionGate = gate
	s.mu.Unlock()
}

func (s *TaskScheduler) SetCompletionObserver(observer taskcompletion.Observer) {
	s.mu.Lock()
	s.completionObserver = observer
	s.mu.Unlock()
}
func (s *TaskScheduler) Enqueue(ctx context.Context, t domain.CodexTask) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("scheduler is closed")
	}
	defer s.mu.Unlock()
	if t.ID == "" {
		return errors.New("task id is required")
	}
	if t.Status == "" {
		t.Status = domain.TaskQueued
	}
	action, resolved, err := ResolveTaskAction(t.Type, t.Action)
	if err != nil {
		return err
	}
	t.Action = action
	if t.SkillName == "" {
		t.SkillName = resolved.Skill
	}
	if t.SkillName != resolved.Skill {
		return fmt.Errorf("task skill %q does not match action %q", t.SkillName, t.Action)
	}
	if t.Status != domain.TaskQueued {
		return fmt.Errorf("task is not queued")
	}
	acceptedAt := time.Now().UTC()
	if _, err := s.tasks.AdmitQueuedTask(ctx, t, acceptedAt); err != nil {
		return err
	}
	s.signal()
	return nil
}

func (s *TaskScheduler) Resume(ctx context.Context, id, answer string) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("scheduler is closed")
	}
	defer s.mu.Unlock()
	t, err := s.tasks.Get(ctx, id)
	if err != nil {
		return err
	}
	if !t.Status.IsWaitingForInput() {
		return fmt.Errorf("task is not waiting for input")
	}
	if t.CodexSessionID == nil || *t.CodexSessionID == "" {
		return fmt.Errorf("task has no codex session")
	}
	if s.makeResume == nil {
		return fmt.Errorf("resume is not configured")
	}
	if err := s.tasks.BeginLegacyResume(ctx, id, answer, time.Now().UTC()); err != nil {
		return err
	}
	// The resume command is selected when dispatch sees the task's session.
	s.signal()
	return nil
}
func (s *TaskScheduler) Cancel(ctx context.Context, id string) error {
	persistCtx := context.WithoutCancel(ctx)
	changed, err := s.tasks.CancelTask(persistCtx, id, time.Now().UTC())
	if err != nil {
		return err
	}
	if !changed {
		task, getErr := s.tasks.Get(persistCtx, id)
		if getErr != nil {
			return getErr
		}
		return fmt.Errorf("task cannot be cancelled in status %s", task.Status)
	}
	s.mu.Lock()
	item := s.running[id]
	if item != nil {
		item.cancel()
		if item.cmd != nil && item.cmd.Process != nil {
			terminateProcess(item.cmd)
		}
	}
	s.mu.Unlock()
	_ = s.tasks.AppendEvent(persistCtx, id, domain.TaskEvent{Kind: "cancelled", Level: "warning", DisplayText: "Task cancelled"})
	return s.notifyTerminalObserver(persistCtx, id)
}

func (s *TaskScheduler) notifyTerminalObserver(ctx context.Context, taskID string) error {
	s.mu.Lock()
	observer := s.completionObserver
	s.mu.Unlock()
	if observer == nil {
		return nil
	}
	task, err := s.tasks.Get(ctx, taskID)
	if err != nil {
		return err
	}
	if err := observer.AfterTerminal(ctx, task); err != nil {
		warningErr := s.tasks.AppendEvent(ctx, taskID, domain.TaskEvent{Kind: taskcompletion.ObserverWarningEvent, Level: "warning", DisplayText: err.Error()})
		return errors.Join(err, warningErr)
	}
	return nil
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
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		close(s.stop)
		<-s.done

		s.mu.Lock()
		running := make([]*scheduled, 0, len(s.running))
		for _, item := range s.running {
			running = append(running, item)
		}
		s.mu.Unlock()
		for _, item := range running {
			item.cancel()
			if item.cmd != nil && item.cmd.Process != nil {
				terminateProcess(item.cmd)
			}
		}
		s.runWG.Wait()
		close(s.closeDone)
	})
	<-s.closeDone
}
