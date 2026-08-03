package codex

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"video-production-console/internal/domain"
	"video-production-console/internal/store"
)

const defaultMaxJSONLBytes = 16 * 1024 * 1024

type Runner struct {
	Command               *exec.Cmd
	Tasks                 *store.TaskRepository
	TaskID                string
	AssetRoot             string
	OutputDir             string
	OutputLastMessagePath string
	Snapshot              *CommandSnapshot
	Broadcast             func(Event)
	Cleanup               func() error

	maxJSONLBytes int
	terminate     func(*exec.Cmd)
}

type persistedEvent struct{ event Event }

func NewRunner(command *exec.Cmd, tasks *store.TaskRepository, taskID, assetRoot string, broadcast func(Event)) *Runner {
	return &Runner{
		Command: command, Tasks: tasks, TaskID: taskID, AssetRoot: assetRoot, Broadcast: broadcast,
		maxJSONLBytes: defaultMaxJSONLBytes, terminate: terminateProcess,
	}
}

func (r *Runner) Run(ctx context.Context) (returnErr error) {
	if r.Cleanup != nil {
		var once sync.Once
		defer func() {
			var cleanupErr error
			once.Do(func() { cleanupErr = r.Cleanup() })
			returnErr = errors.Join(returnErr, cleanupErr)
		}()
	}
	if r.Command == nil || r.Tasks == nil {
		return fmt.Errorf("runner requires command and task repository")
	}
	expectedAction, err := r.Tasks.ExpectedAction(ctx, r.TaskID)
	if err != nil {
		return err
	}
	stdout, err := r.Command.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := r.Command.StderrPipe()
	if err != nil {
		return err
	}
	snapshot := runnerCommandSnapshot(r.Command, r.Snapshot)
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("encode command snapshot: %w", err)
	}
	if err := r.Tasks.Start(ctx, r.TaskID, string(snapshotJSON)); err != nil {
		return err
	}
	if err := r.Command.Start(); err != nil {
		_ = r.Tasks.UpdateStatus(context.WithoutCancel(ctx), r.TaskID, domain.TaskFailed, "", "start_failed", err.Error())
		return err
	}

	stop := make(chan struct{})
	processDone := make(chan struct{})
	var stopOnce sync.Once
	var stopErr error
	terminate := func(err error) {
		stopOnce.Do(func() {
			stopErr = err
			close(stop)
			terminator := r.terminate
			if terminator == nil {
				terminator = terminateProcess
			}
			terminator(r.Command)
			_ = stdout.Close()
			_ = stderr.Close()
		})
	}
	go func() {
		select {
		case <-ctx.Done():
			terminate(ctx.Err())
		case <-processDone:
		}
	}()

	events := make(chan persistedEvent)
	writerErr := make(chan error, 1)
	go r.persistEvents(ctx, events, terminate, writerErr)

	var latestMu sync.Mutex
	latestAgentMessage := ""
	var readers sync.WaitGroup
	readerErrs := make(chan error, 2)
	readers.Add(2)
	go func() {
		defer readers.Done()
		readerErrs <- r.readStdout(ctx, stdout, events, stop, terminate, &latestMu, &latestAgentMessage)
	}()
	go func() {
		defer readers.Done()
		readerErrs <- r.readStderr(ctx, stderr, events, stop, terminate)
	}()

	readers.Wait()
	waitErr := r.Command.Wait()
	close(processDone)
	close(events)
	var streamErr error
	for i := 0; i < 2; i++ {
		if err := <-readerErrs; err != nil && streamErr == nil {
			streamErr = err
		}
	}
	persistenceErr := <-writerErr
	persistCtx := context.WithoutCancel(ctx)

	if persistenceErr != nil {
		_ = r.Tasks.UpdateStatus(persistCtx, r.TaskID, domain.TaskFailed, "", "event_persistence_failed", persistenceErr.Error())
		return persistenceErr
	}
	if streamErr != nil {
		_ = r.Tasks.UpdateStatus(persistCtx, r.TaskID, domain.TaskFailed, "", "stream_failed", streamErr.Error())
		return streamErr
	}
	if stopErr != nil && waitErr == nil {
		_ = r.Tasks.UpdateStatus(persistCtx, r.TaskID, domain.TaskFailed, "", "stream_failed", stopErr.Error())
		return stopErr
	}
	if waitErr != nil {
		_ = r.Tasks.UpdateStatus(persistCtx, r.TaskID, domain.TaskFailed, "", "process_failed", waitErr.Error())
		return waitErr
	}

	latestMu.Lock()
	agentText := latestAgentMessage
	latestMu.Unlock()
	lastPath, pathErr := r.outputLastMessagePath()
	var result ResultEnvelope
	var rawResult, rawLast []byte
	if pathErr == nil {
		result, rawResult, rawLast, err = resolveFinalResult(agentText, lastPath, r.TaskID, expectedAction, r.outputDir())
	} else {
		err = pathErr
	}
	if err != nil {
		artifacts := []store.TaskArtifact{}
		if len(rawLast) > 0 {
			if artifact, artifactErr := r.taskArtifact("raw_output_last_message", lastPath, "application/json"); artifactErr == nil {
				artifacts = append(artifacts, artifact)
			}
		}
		raw := rawLast
		if len(raw) == 0 {
			raw = []byte(agentText)
		}
		write := store.TaskResultWrite{
			Status: domain.TaskFailed, Summary: "Codex output did not match the result contract",
			EventKind: "result_invalid", RawJSON: string(raw), ErrorCode: "output_invalid", ErrorMessage: err.Error(),
		}
		if persistErr := r.Tasks.CompleteWithResult(persistCtx, r.TaskID, write, artifacts, nil); persistErr != nil {
			return persistErr
		}
		return fmt.Errorf("output_invalid: %w", err)
	}

	rawJSON := string(rawResult)
	switch result.Status {
	case "awaiting_input":
		questionJSON, marshalErr := json.Marshal(result.Questions)
		if marshalErr != nil {
			return marshalErr
		}
		questionSchema := string(questionJSON)
		write := store.TaskResultWrite{
			Status: domain.TaskAwaitingInput, Summary: result.Summary, AssistantContent: result.Summary,
			QuestionSchema: &questionSchema, EventKind: "result_awaiting_input", RawJSON: rawJSON,
		}
		return r.Tasks.AwaitInput(persistCtx, r.TaskID, write)
	case "failed":
		artifacts, artifactErr := r.engineeringArtifacts(result.Artifacts)
		if artifactErr != nil {
			return r.persistOutputInvalid(persistCtx, lastPath, rawLast, artifactErr)
		}
		write := store.TaskResultWrite{
			Status: domain.TaskFailed, Summary: result.Summary, AssistantContent: result.Summary,
			EventKind: "result_failed", RawJSON: rawJSON, ErrorCode: "result_failed", ErrorMessage: result.Summary,
		}
		if err := r.Tasks.CompleteWithResult(persistCtx, r.TaskID, write, artifacts, nil); err != nil {
			return err
		}
		return fmt.Errorf("task failed: %s", result.Summary)
	case "completed":
		artifacts, artifactErr := r.engineeringArtifacts(result.Artifacts)
		if artifactErr != nil {
			return r.persistOutputInvalid(persistCtx, lastPath, rawLast, artifactErr)
		}
		task, taskErr := r.Tasks.Get(persistCtx, r.TaskID)
		if taskErr != nil {
			return taskErr
		}
		assets := make([]store.AddAssetVersion, 0, len(result.AssetOutputs))
		for _, output := range result.AssetOutputs {
			assets = append(assets, store.AddAssetVersion{
				ProjectID: task.ProjectID, AccountID: task.AccountID, Type: output.Type, StorageKind: output.StorageKind,
				Path: output.Path, Filename: output.Filename, MIMEType: output.MIME, Size: output.Size, SHA256: output.SHA256,
				SourceTaskID: &r.TaskID,
			})
		}
		write := store.TaskResultWrite{
			Status: domain.TaskCompleted, Summary: result.Summary, AssistantContent: result.Summary,
			EventKind: "result_completed", RawJSON: rawJSON,
		}
		return r.Tasks.CompleteWithResult(persistCtx, r.TaskID, write, artifacts, assets)
	default:
		return fmt.Errorf("unsupported validated result status %q", result.Status)
	}
}

func runnerCommandSnapshot(cmd *exec.Cmd, provided *CommandSnapshot) CommandSnapshot {
	if provided != nil {
		copy := *provided
		copy.Args = append([]string(nil), provided.Args...)
		copy.EnvironmentKeys = append([]string(nil), provided.EnvironmentKeys...)
		return copy
	}
	snapshot := SnapshotCommand(cmd, nil)
	if len(snapshot.Args) >= 4 && snapshot.Args[0] == "exec" && snapshot.Args[1] == "resume" && snapshot.Args[len(snapshot.Args)-1] == "-" {
		snapshot.Args[len(snapshot.Args)-2] = "[REDACTED]"
	}
	return snapshot
}

func (r *Runner) persistEvents(ctx context.Context, events <-chan persistedEvent, terminate func(error), result chan<- error) {
	var firstErr error
	for item := range events {
		if firstErr != nil {
			continue
		}
		e := item.event
		if e.SessionID != "" {
			if err := r.Tasks.SetSession(ctx, r.TaskID, e.SessionID); err != nil {
				firstErr = err
				terminate(err)
				continue
			}
		}
		if err := r.Tasks.AppendEvent(ctx, r.TaskID, domain.TaskEvent{Kind: e.Kind, Level: e.Level, DisplayText: e.DisplayText, RawJSON: string(e.RawJSON)}); err != nil {
			firstErr = err
			terminate(err)
			continue
		}
		if r.Broadcast != nil {
			r.Broadcast(e)
		}
	}
	result <- firstErr
}

func (r *Runner) readStdout(_ context.Context, reader io.Reader, events chan<- persistedEvent, stop <-chan struct{}, terminate func(error), mu *sync.Mutex, latestAgentMessage *string) error {
	scanner := bufio.NewScanner(reader)
	limit := r.maxJSONLBytes
	if limit <= 0 {
		limit = defaultMaxJSONLBytes
	}
	scanner.Buffer(make([]byte, min(64*1024, limit)), limit)
	for scanner.Scan() {
		event, err := ParseLine(scanner.Bytes())
		if err != nil {
			terminate(err)
			return err
		}
		if event.AgentMessageText != "" {
			mu.Lock()
			*latestAgentMessage = event.AgentMessageText
			mu.Unlock()
		}
		select {
		case events <- persistedEvent{event: event}:
		case <-stop:
			return fmt.Errorf("runner stopped")
		}
	}
	if err := scanner.Err(); err != nil {
		terminate(err)
		return err
	}
	return nil
}

func (r *Runner) readStderr(_ context.Context, reader io.Reader, events chan<- persistedEvent, stop <-chan struct{}, terminate func(error)) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), defaultMaxJSONLBytes)
	for scanner.Scan() {
		text := scanner.Text()
		raw, _ := json.Marshal(text)
		select {
		case events <- persistedEvent{event: Event{Kind: "technical_log", Level: "error", DisplayText: text, RawJSON: raw}}:
		case <-stop:
			return fmt.Errorf("runner stopped")
		}
	}
	if err := scanner.Err(); err != nil {
		terminate(err)
		return err
	}
	return nil
}

func resolveFinalResult(agentText, lastMessagePath, taskID string, action domain.TaskAction, outputDir string) (ResultEnvelope, []byte, []byte, error) {
	if strings.TrimSpace(agentText) != "" {
		raw := []byte(agentText)
		if result, err := ParseResultEnvelope(raw, taskID, action, outputDir); err == nil {
			return result, raw, nil, nil
		}
	}
	rawLast, err := os.ReadFile(lastMessagePath)
	if err != nil {
		return ResultEnvelope{}, nil, nil, fmt.Errorf("output_last_message_missing: %w", err)
	}
	result, err := ParseResultEnvelope(rawLast, taskID, action, outputDir)
	if err != nil {
		return ResultEnvelope{}, nil, rawLast, err
	}
	return result, rawLast, rawLast, nil
}

func (r *Runner) outputLastMessagePath() (string, error) {
	if strings.TrimSpace(r.OutputLastMessagePath) != "" {
		return filepath.Abs(filepath.Clean(r.OutputLastMessagePath))
	}
	for i, arg := range r.Command.Args {
		if arg == "--output-last-message" && i+1 < len(r.Command.Args) {
			return filepath.Abs(filepath.Clean(r.Command.Args[i+1]))
		}
	}
	return "", fmt.Errorf("output_last_message_missing: command has no --output-last-message path")
}

func (r *Runner) outputDir() string {
	if strings.TrimSpace(r.OutputDir) != "" {
		return r.OutputDir
	}
	return r.AssetRoot
}

func (r *Runner) engineeringArtifacts(outputs []ArtifactOutput) ([]store.TaskArtifact, error) {
	artifacts := make([]store.TaskArtifact, 0, len(outputs))
	for _, output := range outputs {
		artifact, err := r.taskArtifact(output.Type, output.Path, "")
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, artifact)
	}
	return artifacts, nil
}

func (r *Runner) taskArtifact(kind, path, mimeType string) (store.TaskArtifact, error) {
	root, err := filepath.Abs(filepath.Clean(r.outputDir()))
	if err != nil {
		return store.TaskArtifact{}, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return store.TaskArtifact{}, err
	}
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return store.TaskArtifact{}, err
	}
	before, err := os.Lstat(abs)
	if err != nil {
		return store.TaskArtifact{}, err
	}
	if before.Mode()&os.ModeSymlink != 0 {
		return store.TaskArtifact{}, fmt.Errorf("task artifact cannot be a symlink")
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return store.TaskArtifact{}, err
	}
	if !withinRoot(root, resolved) {
		return store.TaskArtifact{}, fmt.Errorf("task artifact path escapes output directory: %s", path)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return store.TaskArtifact{}, err
	}
	artifact := store.TaskArtifact{Kind: kind, Path: resolved, Filename: filepath.Base(resolved)}
	if info.IsDir() {
		artifact.MIMEType = "inode/directory"
		return artifact, nil
	}
	if !info.Mode().IsRegular() {
		return store.TaskArtifact{}, fmt.Errorf("task artifact is not a regular file or directory")
	}
	file, verifiedPath, verifiedInfo, err := openVerifiedArtifact(resolved, root)
	if err != nil {
		return store.TaskArtifact{}, err
	}
	defer file.Close()
	if !withinRoot(root, verifiedPath) {
		return store.TaskArtifact{}, fmt.Errorf("task artifact path escapes output directory")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return store.TaskArtifact{}, err
	}
	artifact.Path = verifiedPath
	artifact.Filename = filepath.Base(verifiedPath)
	artifact.Size = verifiedInfo.Size()
	artifact.SHA256 = hex.EncodeToString(hash.Sum(nil))
	artifact.MIMEType = mimeType
	if artifact.MIMEType == "" {
		artifact.MIMEType = mime.TypeByExtension(strings.ToLower(filepath.Ext(verifiedPath)))
	}
	if artifact.MIMEType == "" {
		artifact.MIMEType = "application/octet-stream"
	}
	return artifact, nil
}

func (r *Runner) persistOutputInvalid(ctx context.Context, lastPath string, rawLast []byte, cause error) error {
	artifacts := []store.TaskArtifact{}
	if len(rawLast) > 0 {
		if artifact, err := r.taskArtifact("raw_output_last_message", lastPath, "application/json"); err == nil {
			artifacts = append(artifacts, artifact)
		}
	}
	write := store.TaskResultWrite{
		Status: domain.TaskFailed, Summary: "Codex output did not match the result contract", EventKind: "result_invalid",
		RawJSON: string(rawLast), ErrorCode: "output_invalid", ErrorMessage: cause.Error(),
	}
	if err := r.Tasks.CompleteWithResult(ctx, r.TaskID, write, artifacts, nil); err != nil {
		return err
	}
	return fmt.Errorf("output_invalid: %w", cause)
}

func withinRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
