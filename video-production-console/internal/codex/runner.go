package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
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
	var wg sync.WaitGroup
	var finalMu sync.Mutex
	var final *Result
	wg.Add(2)
	go func() { defer wg.Done(); r.readStdout(ctx, stdout, &finalMu, &final) }()
	go func() { defer wg.Done(); r.readStderr(ctx, stderr) }()
	waitErr := r.Command.Wait()
	wg.Wait()
	finalMu.Lock()
	result := finalMuResult(final)
	finalMu.Unlock()
	artifactErr := error(nil)
	if result != nil {
		artifactErr = r.registerArtifacts(ctx, result.Artifacts)
	}
	status := domain.TaskFailed
	summary, code, message := "", "process_failed", ""
	if result != nil {
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
	if waitErr != nil && code == "" && status != domain.TaskWaitingInput {
		message = waitErr.Error()
	}
	if err := r.Tasks.UpdateStatus(ctx, r.TaskID, status, summary, code, message); err != nil {
		return err
	}
	if waitErr != nil && status == domain.TaskFailed {
		return waitErr
	}
	return nil
}
func finalMuResult(v *Result) *Result { return v }
func (r *Runner) readStdout(ctx context.Context, reader io.Reader, mu *sync.Mutex, final **Result) {
	s := bufio.NewScanner(reader)
	for s.Scan() {
		event, _ := ParseLine(s.Bytes())
		if event.SessionID != "" {
			_ = r.Tasks.SetSession(ctx, r.TaskID, event.SessionID)
		}
		if event.FinalResult != nil {
			mu.Lock()
			*final = event.FinalResult
			mu.Unlock()
		}
		raw, _ := json.Marshal(event.RawJSON)
		_ = raw
		_ = r.Tasks.AppendEvent(ctx, r.TaskID, domain.TaskEvent{Kind: event.Kind, Level: event.Level, DisplayText: event.DisplayText, RawJSON: string(event.RawJSON)})
		if r.Broadcast != nil {
			r.Broadcast(event)
		}
	}
}
func (r *Runner) readStderr(ctx context.Context, reader io.Reader) {
	s := bufio.NewScanner(reader)
	for s.Scan() {
		text := s.Text()
		_ = r.Tasks.AppendEvent(ctx, r.TaskID, domain.TaskEvent{Kind: "technical_log", Level: "error", DisplayText: text, RawJSON: fmt.Sprintf("%q", text)})
	}
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
		root, err := filepath.Abs(r.AssetRoot)
		if err != nil {
			return err
		}
		if !withinRoot(root, abs) {
			return fmt.Errorf("artifact path escapes asset root: %s", artifact.Path)
		}
		info, err := os.Stat(abs)
		if err != nil {
			return err
		}
		if task.ProjectID == nil {
			continue
		}
		_, err = r.Tasks.DB().ExecContext(ctx, `INSERT INTO assets(id,project_id,account_id,type,path,filename,mime_type,size,sha256,version,status,created_at,source_task_id) VALUES(lower(hex(randomblob(16))),?,?,?,?,?,?,?,?,?,?,?,?)`, *task.ProjectID, task.AccountID, artifact.Type, abs, filepath.Base(abs), "application/octet-stream", info.Size(), "", 1, "ready", timeNow(), r.TaskID)
		if err != nil {
			return err
		}
	}
	return nil
}
func withinRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
func timeNow() time.Time { return time.Now().UTC() }
