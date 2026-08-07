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
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"video-production-console/internal/domain"
	"video-production-console/internal/progress"
	"video-production-console/internal/security"
	"video-production-console/internal/store"
	"video-production-console/internal/taskcompletion"
)

const (
	defaultMaxJSONLBytes             = 16 * 1024 * 1024
	defaultMaxOutputLastMessageBytes = 16 * 1024 * 1024
)

type Runner struct {
	Command               *exec.Cmd
	Tasks                 *store.TaskRepository
	TaskID                string
	AssetRoot             string
	OutputDir             string
	OutputLastMessagePath string
	Snapshot              *CommandSnapshot
	Redactor              *security.Redactor
	Broadcast             func(Event)
	Cleanup               func() error
	Action                domain.TaskAction
	CompletionGate        taskcompletion.Gate
	ExpectedTurnID        *string

	maxJSONLBytes             int
	maxOutputLastMessageBytes int64
	terminate                 func(*exec.Cmd)
}

type persistedEvent struct{ event Event }

func NewRunner(command *exec.Cmd, tasks *store.TaskRepository, taskID, assetRoot string, broadcast func(Event)) *Runner {
	return &Runner{
		Command: command, Tasks: tasks, TaskID: taskID, AssetRoot: assetRoot, Broadcast: broadcast,
		Redactor: security.NewRedactor(), maxJSONLBytes: defaultMaxJSONLBytes,
		maxOutputLastMessageBytes: defaultMaxOutputLastMessageBytes, terminate: terminateProcess,
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
	r.registerCommandSecrets()
	expectedAction, err := r.Tasks.ExpectedAction(ctx, r.TaskID)
	if err != nil {
		return err
	}
	r.Action = expectedAction
	stdout, err := r.Command.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := r.Command.StderrPipe()
	if err != nil {
		return err
	}
	snapshot := runnerCommandSnapshot(r.Command, r.Snapshot, r.Redactor)
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("encode command snapshot: %w", err)
	}
	if err := r.Tasks.Start(ctx, r.TaskID, string(snapshotJSON)); err != nil {
		return err
	}
	if err := r.Command.Start(); err != nil {
		return r.persistFailure(context.WithoutCancel(ctx), "start_failed", err)
	}

	stop := make(chan struct{})
	processDone := make(chan struct{})
	var stopOnce sync.Once
	var stopMu sync.Mutex
	var stopErr error
	terminate := func(err error) {
		stopOnce.Do(func() {
			stopMu.Lock()
			stopErr = err
			stopMu.Unlock()
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
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
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
	<-watcherDone
	close(events)
	var streamErr error
	for i := 0; i < 2; i++ {
		if err := <-readerErrs; err != nil && streamErr == nil {
			streamErr = err
		}
	}
	persistenceErr := <-writerErr
	persistCtx := context.WithoutCancel(ctx)

	// A user-requested cancellation closes the process pipes, which can surface
	// as ordinary read errors on Windows. Cancellation is authoritative here:
	// let the scheduler persist the cancelled state instead of misclassifying
	// the closed pipe as a task failure.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if persistenceErr != nil {
		return r.persistFailure(persistCtx, "event_persistence_failed", persistenceErr)
	}
	if streamErr != nil {
		return r.persistFailure(persistCtx, "stream_failed", streamErr)
	}
	stopMu.Lock()
	stoppedWith := stopErr
	stopMu.Unlock()
	if stoppedWith != nil && waitErr == nil {
		return r.persistFailure(persistCtx, "stream_failed", stoppedWith)
	}
	if waitErr != nil {
		return r.persistFailure(persistCtx, "process_failed", r.processFailureCause(persistCtx, waitErr))
	}

	latestMu.Lock()
	agentText := latestAgentMessage
	latestMu.Unlock()
	lastPath, pathErr := r.outputLastMessagePath()
	var result ResultEnvelope
	var rawResult, rawLast []byte
	if pathErr == nil {
		result, rawResult, rawLast, err = r.resolveFinalResult(agentText, lastPath, expectedAction)
	} else {
		err = pathErr
	}
	if err != nil {
		raw := rawLast
		if len(raw) == 0 {
			raw = []byte(agentText)
		}
		return r.persistOutputInvalid(persistCtx, lastPath, raw, err)
	}
	return r.persistValidatedResult(persistCtx, result, rawResult, lastPath, rawLast)
}

func (r *Runner) persistValidatedResult(persistCtx context.Context, result ResultEnvelope, rawResult []byte, lastPath string, rawLast []byte) error {
	rawJSON := r.redact(string(rawResult))
	switch result.Status {
	case "awaiting_input":
		questionJSON, marshalErr := json.Marshal(result.Questions)
		if marshalErr != nil {
			return marshalErr
		}
		questionSchema := r.redact(string(questionJSON))
		summary := r.redact(result.Summary)
		assistantContent := summary
		for _, question := range result.Questions {
			assistantContent += "\n\nQuestion: " + r.redact(question.Text)
		}
		write := store.TaskResultWrite{
			Status: domain.TaskAwaitingInput, Summary: summary, AssistantContent: assistantContent,
			QuestionSchema: &questionSchema, EventKind: "result_awaiting_input", RawJSON: rawJSON,
			ExpectedTurnID: r.ExpectedTurnID,
		}
		if err := r.Tasks.AwaitInput(persistCtx, r.TaskID, write); err != nil {
			return r.persistFailure(persistCtx, "result_persistence_failed", err)
		}
		return nil
	case "failed":
		artifacts, artifactErr := r.engineeringArtifacts(result.Action, result.Artifacts)
		if artifactErr != nil {
			return r.persistOutputInvalid(persistCtx, lastPath, rawLast, artifactErr)
		}
		write := store.TaskResultWrite{
			Status: domain.TaskFailed, Summary: r.redact(result.Summary), AssistantContent: r.redact(result.Summary),
			EventKind: "result_failed", RawJSON: rawJSON, ErrorCode: "result_failed", ErrorMessage: r.redact(result.Summary),
			ExpectedTurnID: r.ExpectedTurnID,
		}
		if err := r.Tasks.CompleteWithResult(persistCtx, r.TaskID, write, artifacts, nil); err != nil {
			return r.persistFailure(persistCtx, "result_persistence_failed", err)
		}
		return fmt.Errorf("task failed: %s", result.Summary)
	case "completed":
		artifacts, artifactErr := r.engineeringArtifacts(result.Action, result.Artifacts)
		if artifactErr != nil {
			return r.persistOutputInvalid(persistCtx, lastPath, rawLast, artifactErr)
		}
		var ideaSessionID string
		var ideaCandidates []domain.IdeaCandidate
		if result.Action == domain.ActionTopicBrainstorm {
			var candidatesPath string
			for _, output := range result.Artifacts {
				if output.Type == "topic_candidates" {
					candidatesPath = output.Path
					break
				}
			}
			sessionID, candidates, parseErr := loadTopicCandidates(candidatesPath, r.TaskID)
			if parseErr != nil {
				return r.persistOutputInvalid(persistCtx, lastPath, rawLast, parseErr)
			}
			ideaSessionID, ideaCandidates = sessionID, candidates
		}
		assetOutputs, assetErr := r.verifiedFormalAssets(result.AssetOutputs)
		if assetErr != nil {
			return r.persistOutputInvalid(persistCtx, lastPath, rawLast, assetErr)
		}
		task, taskErr := r.Tasks.Get(persistCtx, r.TaskID)
		if taskErr != nil {
			return r.persistFailure(persistCtx, "result_persistence_failed", taskErr)
		}
		if result.Action == domain.ActionMontageExecute {
			if r.CompletionGate == nil {
				return r.persistFailure(persistCtx, "registration_coordinator_unavailable", errors.New("montage registration coordinator is unavailable"))
			}
			handled, gateErr := r.CompletionGate.HandleCompleted(persistCtx, taskcompletion.CompletedInput{
				Task: task, ManifestPath: filepath.Join(r.AssetRoot, "tasks", r.TaskID, "task_manifest.json"),
				Action: result.Action, Summary: r.redact(result.Summary), RawJSON: rawJSON, Artifacts: artifacts, ExpectedTurnID: r.ExpectedTurnID,
			})
			if gateErr != nil {
				return r.persistFailure(persistCtx, "completion_gate_failed", gateErr)
			}
			if handled {
				return nil
			}
		}
		assets := make([]store.AddAssetVersion, 0, len(assetOutputs))
		for _, output := range assetOutputs {
			assets = append(assets, store.AddAssetVersion{
				ProjectID: task.ProjectID, AccountID: task.AccountID, Type: output.Type, StorageKind: output.StorageKind,
				Path: output.Path, Filename: output.Filename, MIMEType: output.MIME, Size: output.Size, SHA256: output.SHA256,
				SourceTaskID: &r.TaskID,
			})
		}
		// The topic Skill is required to return the Obsidian card as an
		// artifact, not as asset_outputs. The console owns project registration,
		// so it promotes that verified receipt into the project's locked
		// topic_card asset here.
		if (result.Action == domain.ActionTopicCommit || result.Action == domain.ActionTopicDeepen) && task.ProjectID != nil {
			for _, artifact := range artifacts {
				if artifact.Kind != "topic_card" {
					continue
				}
				assets = append(assets, store.AddAssetVersion{
					ProjectID: task.ProjectID, AccountID: task.AccountID, Type: domain.AssetTopicCard,
					StorageKind: domain.StorageFile, Path: artifact.Path, Filename: artifact.Filename,
					MIMEType: artifact.MIMEType, Size: artifact.Size, SHA256: artifact.SHA256,
					SourceTaskID: &r.TaskID,
				})
				break
			}
		}
		write := store.TaskResultWrite{
			Status: domain.TaskCompleted, Summary: r.redact(result.Summary), AssistantContent: r.redact(result.Summary),
			EventKind: "result_completed", RawJSON: rawJSON,
			ExpectedTurnID: r.ExpectedTurnID, IdeaSessionID: ideaSessionID, IdeaCandidates: ideaCandidates,
		}
		if err := r.Tasks.CompleteWithResult(persistCtx, r.TaskID, write, artifacts, assets); err != nil {
			return r.persistFailure(persistCtx, "result_persistence_failed", err)
		}
		if task.ProjectID != nil {
			_, _ = store.NewProjectRepository(r.Tasks.DB()).SyncStageFromAssets(persistCtx, *task.ProjectID, time.Now().UTC())
		}
		return nil
	default:
		return fmt.Errorf("unsupported validated result status %q", result.Status)
	}
}

// CompleteAgentResult applies the same V2 envelope validation, artifact
// verification, asset registration, and CompletionGate used by the legacy
// exec Runner to a final App Server assistant item.
func (r *Runner) CompleteAgentResult(ctx context.Context, agentText string) error {
	if r == nil || r.Tasks == nil || strings.TrimSpace(r.TaskID) == "" {
		return errors.New("result completion requires a task repository and task id")
	}
	action, err := r.Tasks.ExpectedAction(ctx, r.TaskID)
	if err != nil {
		return err
	}
	r.Action = action
	roots, err := r.resultManifestRoots(action)
	if err != nil {
		return r.persistOutputInvalid(ctx, "", []byte(agentText), err)
	}
	result, raw, usedResultFile, err := r.resolveAppServerResult(agentText, action, roots)
	if err != nil {
		return r.persistOutputInvalid(ctx, "", raw, err)
	}
	if usedResultFile {
		// The Skill's structured receipt is authoritative only after the same
		// strict validation as an assistant response. This makes App Server task
		// completion resilient when the final chat message is a human summary.
		_ = r.Tasks.AppendEvent(ctx, r.TaskID, domain.TaskEvent{Kind: "result_file_fallback", Level: "info", DisplayText: "Used validated output/result.json as the formal task result"})
	}
	return r.persistValidatedResult(ctx, result, raw, "", raw)
}

func (r *Runner) resolveAppServerResult(agentText string, action domain.TaskAction, roots ManifestRoots) (ResultEnvelope, []byte, bool, error) {
	agentRaw := []byte(agentText)
	if result, err := ParseResultEnvelopeWithRoots(agentRaw, r.TaskID, action, r.outputDir(), roots); err == nil {
		return result, agentRaw, false, nil
	} else if strings.TrimSpace(agentText) == "" {
		return r.resolveOutputResultFile(action, roots, "assistant returned no final result")
	} else {
		result, raw, usedFile, fileErr := r.resolveOutputResultFile(action, roots, "")
		if fileErr == nil {
			return result, raw, usedFile, nil
		}
		return ResultEnvelope{}, agentRaw, false, fmt.Errorf("assistant result is not a valid envelope: %w; output/result.json fallback failed: %v", err, fileErr)
	}
}

func (r *Runner) resolveOutputResultFile(action domain.TaskAction, roots ManifestRoots, agentFailure string) (ResultEnvelope, []byte, bool, error) {
	resultPath := filepath.Join(r.outputDir(), "result.json")
	raw, err := r.readOutputLastMessage(resultPath)
	if err != nil {
		if agentFailure != "" {
			return ResultEnvelope{}, nil, false, fmt.Errorf("%s; read output/result.json: %w", agentFailure, err)
		}
		return ResultEnvelope{}, nil, false, err
	}
	result, err := ParseResultEnvelopeWithRoots(raw, r.TaskID, action, r.outputDir(), roots)
	if err != nil {
		return ResultEnvelope{}, raw, false, err
	}
	return result, raw, true, nil
}

func runnerCommandSnapshot(cmd *exec.Cmd, provided *CommandSnapshot, redactor *security.Redactor) CommandSnapshot {
	if provided != nil {
		copy := *provided
		copy.Args = append([]string(nil), provided.Args...)
		copy.EnvironmentKeys = append([]string(nil), provided.EnvironmentKeys...)
		if redactor != nil {
			copy.Binary = redactor.Redact(copy.Binary)
			copy.WorkingDirectory = redactor.Redact(copy.WorkingDirectory)
			for i := range copy.Args {
				copy.Args[i] = redactor.Redact(copy.Args[i])
			}
		}
		return copy
	}
	snapshot := SnapshotCommand(cmd, redactor)
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
		e := r.redactedEvent(item.event)
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
		r.persistSemanticEvent(ctx, e)
		if r.Broadcast != nil {
			r.Broadcast(e)
		}
	}
	result <- firstErr
}

// persistSemanticEvent keeps the UI-facing timeline separate from raw JSONL
// diagnostics. Failure to add a presentation event must never interrupt a
// production task whose authoritative task event has already been persisted.
func (r *Runner) persistSemanticEvent(ctx context.Context, e Event) {
	if r == nil || r.Tasks == nil {
		return
	}
	projected := progress.Project(progress.Input{
		TaskID:     r.TaskID,
		Action:     r.Action,
		Method:     e.Kind,
		RawJSON:    string(e.RawJSON),
		LegacyKind: e.Kind,
	})
	if !projected.Visible || projected.Kind == "" || projected.DisplayText == "" {
		return
	}
	_, _ = store.NewConversationRepository(r.Tasks.DB()).AppendSemantic(ctx, store.SemanticWrite{
		TaskID: r.TaskID,
		Kind:   string(projected.Kind),
		Phase:  projected.Phase,
		Level:  e.Level,
		Title:  projected.DisplayText,
	})
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

func (r *Runner) resolveFinalResult(agentText, lastMessagePath string, action domain.TaskAction) (ResultEnvelope, []byte, []byte, error) {
	roots, rootsErr := r.resultManifestRoots(action)
	if rootsErr != nil {
		return ResultEnvelope{}, nil, nil, rootsErr
	}
	if strings.TrimSpace(agentText) != "" {
		raw := []byte(agentText)
		if result, err := ParseResultEnvelopeWithRoots(raw, r.TaskID, action, r.outputDir(), roots); err == nil {
			return result, raw, nil, nil
		}
	}
	rawLast, err := r.readOutputLastMessage(lastMessagePath)
	if err != nil {
		return ResultEnvelope{}, nil, nil, fmt.Errorf("output_last_message_missing: %w", err)
	}
	result, err := ParseResultEnvelopeWithRoots(rawLast, r.TaskID, action, r.outputDir(), roots)
	if err != nil {
		return ResultEnvelope{}, nil, rawLast, err
	}
	return result, rawLast, rawLast, nil
}

func (r *Runner) resultManifestRoots(action domain.TaskAction) (ManifestRoots, error) {
	if action != domain.ActionTopicCommit && action != domain.ActionTopicDeepen {
		return ManifestRoots{}, nil
	}
	path := filepath.Join(r.AssetRoot, "tasks", r.TaskID, "task_manifest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return ManifestRoots{}, fmt.Errorf("read result validation manifest: %w", err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var manifest TaskManifest
	if err := decoder.Decode(&manifest); err != nil {
		return ManifestRoots{}, fmt.Errorf("decode result validation manifest: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return ManifestRoots{}, fmt.Errorf("decode result validation manifest: %w", err)
	}
	if manifest.TaskID != r.TaskID || manifest.Action != action {
		return ManifestRoots{}, fmt.Errorf("result validation manifest identity mismatch")
	}
	return ManifestRoots{
		Obsidian:   manifest.NonSecretSettings.ObsidianVault,
		TopicCards: manifest.NonSecretSettings.TopicCardsDir,
	}, nil
}

func (r *Runner) outputLastMessagePath() (string, error) {
	path := ""
	if strings.TrimSpace(r.OutputLastMessagePath) != "" {
		path = r.OutputLastMessagePath
	} else {
		for i, arg := range r.Command.Args {
			if arg == "--output-last-message" && i+1 < len(r.Command.Args) {
				path = r.Command.Args[i+1]
				break
			}
		}
	}
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("output_last_message_missing: command has no --output-last-message path")
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("output_last_message path must be absolute")
	}
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil || !canonicalSamePath(abs, path) {
		return "", fmt.Errorf("output_last_message path must be canonical")
	}
	root, err := resolvePath(r.outputDir())
	if err != nil {
		return "", fmt.Errorf("canonicalize output directory: %w", err)
	}
	parent, err := resolvePath(filepath.Dir(abs))
	if err != nil {
		return "", fmt.Errorf("canonicalize output_last_message parent: %w", err)
	}
	if !withinRoot(root, parent) {
		return "", fmt.Errorf("output_last_message path escapes output directory")
	}
	if info, err := os.Lstat(abs); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return "", fmt.Errorf("output_last_message must be a no-follow regular file")
		}
		resolved, err := resolvePath(abs)
		if err != nil || !withinRoot(root, resolved) || !canonicalSamePath(abs, resolved) {
			return "", fmt.Errorf("output_last_message path is not safely contained")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return abs, nil
}

func (r *Runner) readOutputLastMessage(path string) ([]byte, error) {
	root, err := resolvePath(r.outputDir())
	if err != nil {
		return nil, err
	}
	file, resolved, info, err := openVerifiedArtifact(path, root)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if !info.Mode().IsRegular() || !withinRoot(root, resolved) || !canonicalSamePath(path, resolved) {
		return nil, fmt.Errorf("output_last_message must be a contained regular file")
	}
	limit := r.maxOutputLastMessageBytes
	if limit <= 0 {
		limit = defaultMaxOutputLastMessageBytes
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("output_last_message exceeds %d bytes", limit)
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit || int64(len(data)) != info.Size() {
		return nil, fmt.Errorf("output_last_message size changed during read")
	}
	return data, nil
}

func (r *Runner) outputDir() string {
	if strings.TrimSpace(r.OutputDir) != "" {
		return r.OutputDir
	}
	return r.AssetRoot
}

func (r *Runner) engineeringArtifacts(action domain.TaskAction, outputs []ArtifactOutput) ([]store.TaskArtifact, error) {
	roots := ManifestRoots{}
	if len(outputs) > 0 && outputs[0].Type == "topic_card" {
		var err error
		roots, err = r.resultManifestRoots(action)
		if err != nil {
			return nil, err
		}
	}
	artifacts := make([]store.TaskArtifact, 0, len(outputs))
	for _, output := range outputs {
		var artifact store.TaskArtifact
		var err error
		if action == domain.ActionMontageExecute && output.Type == "plaintext_workspace" {
			artifact, err = r.taskDirectoryArtifact(output.Type, output.Path)
		} else if output.Type == "topic_card" {
			artifact, err = r.taskArtifactWithinRoot(output.Type, output.Path, "text/markdown", roots.TopicCards)
		} else {
			artifact, err = r.taskArtifact(output.Type, output.Path, "")
		}
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, artifact)
	}
	return artifacts, nil
}

func (r *Runner) taskDirectoryArtifact(kind, path string) (store.TaskArtifact, error) {
	root, err := resolvePath(r.outputDir())
	if err != nil {
		return store.TaskArtifact{}, err
	}
	resolved, err := validateResultPath("task directory artifact", path, root)
	if err != nil {
		return store.TaskArtifact{}, err
	}
	info, err := os.Lstat(resolved)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return store.TaskArtifact{}, fmt.Errorf("task directory artifact must be a contained no-follow directory")
	}
	digest, err := HashResultDirectory(resolved)
	if err != nil {
		return store.TaskArtifact{}, err
	}
	return store.TaskArtifact{Kind: kind, Path: resolved, Filename: filepath.Base(resolved), MIMEType: "inode/directory", SHA256: digest}, nil
}

func (r *Runner) verifiedFormalAssets(outputs []AssetOutput) ([]AssetOutput, error) {
	root, err := resolvePath(r.outputDir())
	if err != nil {
		return nil, err
	}
	verified := make([]AssetOutput, 0, len(outputs))
	for _, output := range outputs {
		if output.StorageKind == domain.StorageDirectory {
			verified = append(verified, output)
			continue
		}
		file, path, info, err := openVerifiedArtifact(output.Path, root)
		if err != nil {
			return nil, fmt.Errorf("open formal asset %q: %w", output.Path, err)
		}
		if !info.Mode().IsRegular() || !withinRoot(root, path) || !canonicalSamePath(output.Path, path) {
			_ = file.Close()
			return nil, fmt.Errorf("formal asset must be a contained no-follow regular file")
		}
		if output.Size != info.Size() {
			_ = file.Close()
			return nil, fmt.Errorf("formal asset size does not match file")
		}
		probe := make([]byte, 512)
		n, readErr := io.ReadFull(file, probe)
		if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) && !errors.Is(readErr, io.EOF) {
			_ = file.Close()
			return nil, readErr
		}
		hash := sha256.New()
		if _, err := hash.Write(probe[:n]); err != nil {
			_ = file.Close()
			return nil, err
		}
		if _, err := io.Copy(hash, file); err != nil {
			_ = file.Close()
			return nil, err
		}
		if err := file.Close(); err != nil {
			return nil, err
		}
		actualSHA := hex.EncodeToString(hash.Sum(nil))
		if !strings.EqualFold(output.SHA256, actualSHA) {
			return nil, fmt.Errorf("formal asset sha256 does not match file")
		}
		declaredMIME, _, err := mime.ParseMediaType(output.MIME)
		if err != nil {
			return nil, fmt.Errorf("formal asset MIME is invalid: %w", err)
		}
		recognizedMIME := recognizedMIMEForPath(path)
		if recognizedMIME == "" {
			recognizedMIME = http.DetectContentType(probe[:n])
		}
		recognizedMIME, _, err = mime.ParseMediaType(recognizedMIME)
		if err != nil || !strings.EqualFold(declaredMIME, recognizedMIME) {
			return nil, fmt.Errorf("formal asset MIME does not match recognized file type")
		}
		output.Path = path
		output.Filename = filepath.Base(path)
		output.Size = info.Size()
		output.SHA256 = actualSHA
		output.MIME = recognizedMIME
		verified = append(verified, output)
	}
	return verified, nil
}

func recognizedMIMEForPath(path string) string {
	if strings.EqualFold(filepath.Ext(path), ".md") {
		return "text/markdown"
	}
	return mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
}

func (r *Runner) taskArtifact(kind, path, mimeType string) (store.TaskArtifact, error) {
	return r.taskArtifactWithinRoot(kind, path, mimeType, r.outputDir())
}

func (r *Runner) taskArtifactWithinRoot(kind, path, mimeType, allowedRoot string) (store.TaskArtifact, error) {
	root, err := filepath.Abs(filepath.Clean(allowedRoot))
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
		RawJSON: r.redact(string(rawLast)), ErrorCode: "output_invalid", ErrorMessage: r.redact(cause.Error()),
		ExpectedTurnID: r.ExpectedTurnID,
	}
	if err := r.Tasks.CompleteWithResult(ctx, r.TaskID, write, artifacts, nil); err != nil {
		return r.persistFailure(ctx, "result_persistence_failed", err)
	}
	return fmt.Errorf("output_invalid: %w", cause)
}

func (r *Runner) persistFailure(ctx context.Context, code string, cause error) error {
	message := r.redact(cause.Error())
	var statusErr error
	if r.ExpectedTurnID != nil {
		_, statusErr = r.Tasks.FailAppServerTurn(ctx, r.TaskID, *r.ExpectedTurnID, code, message)
	} else {
		statusErr = r.Tasks.UpdateStatus(ctx, r.TaskID, domain.TaskFailed, "", code, message)
	}
	if statusErr != nil {
		return errors.Join(cause, fmt.Errorf("persist failure status: %w", statusErr))
	}
	_, _ = store.NewConversationRepository(r.Tasks.DB()).AppendSemantic(ctx, store.SemanticWrite{
		TaskID: r.TaskID,
		Kind:   string(domain.SemanticFailure),
		Phase:  "failed",
		Level:  "error",
		Title:  message,
	})
	return cause
}

// processFailureCause preserves the useful terminal Codex error instead of
// replacing it with the operating system's unhelpful "exit status 1".
func (r *Runner) processFailureCause(ctx context.Context, fallback error) error {
	events, err := r.Tasks.Events(ctx, r.TaskID)
	if err != nil {
		return fallback
	}
	for i := len(events) - 1; i >= 0; i-- {
		var envelope struct {
			Type    string `json:"type"`
			Message string `json:"message"`
			Error   struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(events[i].RawJSON), &envelope) != nil {
			continue
		}
		message := strings.TrimSpace(envelope.Error.Message)
		if message == "" {
			message = strings.TrimSpace(envelope.Message)
		}
		if message == "" || (envelope.Type != "turn.failed" && envelope.Type != "error") {
			continue
		}
		return errors.New(friendlyCodexFailure(message))
	}
	return fallback
}

func friendlyCodexFailure(message string) string {
	lower := strings.ToLower(message)
	if strings.Contains(lower, "no available channel for model") {
		model := "当前模型"
		if marker := strings.Index(lower, "no available channel for model "); marker >= 0 {
			rest := message[marker+len("no available channel for model "):]
			if end := strings.IndexAny(rest, " ;,)(\r\n"); end > 0 {
				model = rest[:end]
			} else if strings.TrimSpace(rest) != "" {
				model = strings.TrimSpace(rest)
			}
		}
		return fmt.Sprintf("模型通道不可用：%s 当前没有可用线路，请在设置中更换模型后重试", model)
	}
	if strings.Contains(lower, "service unavailable") {
		return "模型服务暂时不可用，请稍后重试或在设置中更换模型"
	}
	return message
}

func (r *Runner) redact(value string) string {
	if r.Redactor == nil {
		return value
	}
	return r.Redactor.Redact(value)
}

func (r *Runner) redactedEvent(event Event) Event {
	event.DisplayText = r.redact(event.DisplayText)
	event.AgentMessageText = r.redact(event.AgentMessageText)
	event.RawJSON = json.RawMessage(r.redact(string(event.RawJSON)))
	return event
}

func (r *Runner) registerCommandSecrets() {
	if r.Redactor == nil {
		r.Redactor = security.NewRedactor()
	}
	secretKeys := make(map[string]struct{}, len(secretEnvironmentKeys))
	for _, key := range secretEnvironmentKeys {
		secretKeys[strings.ToUpper(key)] = struct{}{}
	}
	for _, entry := range r.Command.Env {
		key, value, ok := strings.Cut(entry, "=")
		if _, sensitive := secretKeys[strings.ToUpper(key)]; ok && sensitive {
			r.Redactor.Register(value)
		}
	}
}

func withinRoot(root, path string) bool {
	return pathInside(root, path)
}
