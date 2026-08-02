package codex

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"video-production-console/internal/domain"
	"video-production-console/internal/store"
)

type Runner struct {
	Command   *exec.Cmd
	Tasks     *store.TaskRepository
	TaskID    string
	AssetRoot string
	Broadcast func(Event)
}

type persistedEvent struct{ event Event }

func NewRunner(command *exec.Cmd, tasks *store.TaskRepository, taskID, assetRoot string, broadcast func(Event)) *Runner {
	return &Runner{Command: command, Tasks: tasks, TaskID: taskID, AssetRoot: assetRoot, Broadcast: broadcast}
}

func (r *Runner) Run(ctx context.Context) error {
	if r.Command == nil || r.Tasks == nil {
		return fmt.Errorf("runner requires command and task repository")
	}
	stdout, err := r.Command.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := r.Command.StderrPipe()
	if err != nil {
		return err
	}
	if err := r.Tasks.UpdateStatus(ctx, r.TaskID, domain.TaskRunning, "", "", ""); err != nil {
		return err
	}
	if err := r.Command.Start(); err != nil {
		_ = r.Tasks.UpdateStatus(ctx, r.TaskID, domain.TaskFailed, "", "start_failed", err.Error())
		return err
	}
	stop := make(chan struct{})
	var stopOnce sync.Once
	var stopErr error
	terminate := func(err error) {
		stopOnce.Do(func() {
			stopErr = err
			close(stop)
			_ = stdout.Close()
			_ = stderr.Close()
			terminateProcess(r.Command)
		})
	}
	var wg sync.WaitGroup
	var finalMu sync.Mutex
	var final *Result
	events := make(chan persistedEvent)
	writerErr := make(chan error, 1)
	go func() {
		var firstErr error
		for item := range events {
			if firstErr != nil {
				continue
			}
			e := item.event
			if e.SessionID != "" {
				if err := r.Tasks.SetSession(ctx, r.TaskID, e.SessionID); err != nil {
					if firstErr == nil {
						firstErr = err
						terminate(err)
					}
					continue
				}
			}
			if err := r.Tasks.AppendEvent(ctx, r.TaskID, domain.TaskEvent{Kind: e.Kind, Level: e.Level, DisplayText: e.DisplayText, RawJSON: string(e.RawJSON)}); err != nil {
				if firstErr == nil {
					firstErr = err
					terminate(err)
				}
				continue
			}
			if r.Broadcast != nil {
				r.Broadcast(e)
			}
		}
		writerErr <- firstErr
	}()
	wg.Add(2)
	readerErrs := make(chan error, 2)
	go func() {
		defer wg.Done()
		readerErrs <- r.readStdout(ctx, stdout, events, stop, terminate, &finalMu, &final)
	}()
	go func() { defer wg.Done(); readerErrs <- r.readStderr(ctx, stderr, events, stop, terminate) }()
	wg.Wait()
	waitErr := r.Command.Wait()
	close(events)
	var streamErr error
	for i := 0; i < 2; i++ {
		if err := <-readerErrs; err != nil && streamErr == nil {
			streamErr = err
		}
	}
	persistenceErr := <-writerErr
	finalMu.Lock()
	result := finalMuResult(final)
	finalMu.Unlock()
	artifactErr := error(nil)
	if result != nil {
		artifactErr = r.registerArtifacts(ctx, result.Artifacts)
	}
	status := domain.TaskFailed
	summary, code, message := "", "process_failed", ""
	if result != nil && waitErr == nil {
		summary = result.Summary
		switch result.Status {
		case "needs_input":
			status, code = domain.TaskWaitingInput, ""
		case "completed":
			status, code = domain.TaskCompleted, ""
		case "failed":
			status = domain.TaskFailed
		}
	}
	if artifactErr != nil {
		status, code, message = domain.TaskFailed, "artifact_registration_failed", artifactErr.Error()
	}
	if waitErr != nil {
		message = waitErr.Error()
	}
	if streamErr != nil {
		status, code, message = domain.TaskFailed, "stream_failed", streamErr.Error()
	}
	if persistenceErr != nil {
		status, code, message = domain.TaskFailed, "event_persistence_failed", persistenceErr.Error()
	}
	if stopErr != nil && streamErr == nil && persistenceErr == nil {
		status, code, message = domain.TaskFailed, "stream_failed", stopErr.Error()
	}
	if err := r.Tasks.UpdateStatus(ctx, r.TaskID, status, summary, code, message); err != nil {
		return err
	}
	if waitErr != nil && status == domain.TaskFailed {
		return waitErr
	}
	if streamErr != nil {
		return streamErr
	}
	if persistenceErr != nil {
		return persistenceErr
	}
	if status == domain.TaskFailed {
		if message != "" {
			return fmt.Errorf("task failed: %s", message)
		}
		if summary != "" {
			return fmt.Errorf("task failed: %s", summary)
		}
		return fmt.Errorf("task failed")
	}
	return nil
}
func finalMuResult(v *Result) *Result { return v }
func (r *Runner) readStdout(ctx context.Context, reader io.Reader, events chan<- persistedEvent, stop <-chan struct{}, terminate func(error), mu *sync.Mutex, final **Result) error {
	s := bufio.NewScanner(reader)
	s.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for s.Scan() {
		event, err := ParseLine(s.Bytes())
		if err != nil {
			terminate(err)
			return err
		}
		if event.FinalResult != nil {
			mu.Lock()
			*final = event.FinalResult
			mu.Unlock()
		}
		select {
		case events <- persistedEvent{event: event}:
		case <-stop:
			return fmt.Errorf("runner stopped")
		}
	}
	if err := s.Err(); err != nil {
		terminate(err)
		return err
	}
	return nil
}
func (r *Runner) readStderr(ctx context.Context, reader io.Reader, events chan<- persistedEvent, stop <-chan struct{}, terminate func(error)) error {
	s := bufio.NewScanner(reader)
	s.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for s.Scan() {
		text := s.Text()
		select {
		case events <- persistedEvent{event: Event{Kind: "technical_log", Level: "error", DisplayText: text, RawJSON: []byte(fmt.Sprintf("%q", text))}}:
		case <-stop:
			return fmt.Errorf("runner stopped")
		}
	}
	if err := s.Err(); err != nil {
		terminate(err)
		return err
	}
	return nil
}

func (r *Runner) registerArtifacts(ctx context.Context, artifacts []Artifact) error {
	task, err := r.Tasks.Get(ctx, r.TaskID)
	if err != nil {
		return err
	}
	for _, artifact := range artifacts {
		path := artifact.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(r.AssetRoot, path)
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		root, err := filepath.EvalSymlinks(r.AssetRoot)
		if err != nil {
			return err
		}
		file, resolved, info, err := openVerifiedArtifact(abs, root)
		if err != nil {
			return err
		}
		if !withinRoot(root, resolved) {
			_ = file.Close()
			return fmt.Errorf("artifact path escapes asset root: %s", artifact.Path)
		}
		if task.ProjectID == nil {
			_ = file.Close()
			continue
		}
		_, err = r.Tasks.DB().ExecContext(ctx, `INSERT INTO assets(id,project_id,account_id,type,path,filename,mime_type,size,sha256,version,status,created_at,source_task_id) VALUES(lower(hex(randomblob(16))),?,?,?,?,?,?,?,?,?,?,?,?)`, *task.ProjectID, task.AccountID, artifact.Type, resolved, filepath.Base(resolved), "application/octet-stream", info.Size(), "", 1, "ready", timeNow(), r.TaskID)
		if err != nil {
			_ = file.Close()
			return err
		}
		_ = file.Close()
	}
	return nil
}
func withinRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
func timeNow() time.Time { return time.Now().UTC() }
