package codexapp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeProcessFactory struct {
	mu        sync.Mutex
	starts    int
	processes []*fakeManagedProcess
	startGate <-chan struct{}
}

func (f *fakeProcessFactory) Start(ctx context.Context) (ManagedProcess, error) {
	if f.startGate != nil {
		select {
		case <-f.startGate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	process := newFakeManagedProcess()
	f.mu.Lock()
	f.starts++
	f.processes = append(f.processes, process)
	f.mu.Unlock()
	return process, nil
}

func (f *fakeProcessFactory) snapshot() (int, []*fakeManagedProcess) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.starts, append([]*fakeManagedProcess(nil), f.processes...)
}

func (f *fakeProcessFactory) waitForProcess(t *testing.T) *fakeManagedProcess {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		_, processes := f.snapshot()
		if len(processes) > 0 {
			return processes[len(processes)-1]
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for process start")
		case <-time.After(time.Millisecond):
		}
	}
}

type fakeManagedProcess struct {
	stdin        *io.PipeWriter
	serverInput  *bufio.Reader
	stdout       *io.PipeReader
	serverOutput *io.PipeWriter
	stderr       *io.PipeReader
	stderrWriter *io.PipeWriter
	waitDone     chan struct{}
	waitOnce     sync.Once
	terminate    atomic.Int32
}

func newFakeManagedProcess() *fakeManagedProcess {
	serverInput, stdin := io.Pipe()
	stdout, serverOutput := io.Pipe()
	stderr, stderrWriter := io.Pipe()
	return &fakeManagedProcess{
		stdin: stdin, serverInput: bufio.NewReader(serverInput),
		stdout: stdout, serverOutput: serverOutput,
		stderr: stderr, stderrWriter: stderrWriter,
		waitDone: make(chan struct{}),
	}
}

func (p *fakeManagedProcess) Stdin() io.WriteCloser { return p.stdin }
func (p *fakeManagedProcess) Stdout() io.ReadCloser { return p.stdout }
func (p *fakeManagedProcess) Stderr() io.ReadCloser { return p.stderr }
func (p *fakeManagedProcess) PID() int              { return 4242 }
func (p *fakeManagedProcess) Wait() error {
	<-p.waitDone
	return errors.New("fake process exited")
}
func (p *fakeManagedProcess) Terminate() error {
	p.terminate.Add(1)
	p.exit()
	return nil
}
func (p *fakeManagedProcess) exit() {
	p.waitOnce.Do(func() {
		close(p.waitDone)
		_ = p.serverOutput.Close()
		_ = p.stderrWriter.Close()
		_ = p.stdin.Close()
	})
}
func (p *fakeManagedProcess) readEnvelope(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	line, err := p.serverInput.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read client frame: %v", err)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(bytes.TrimSpace(line), &envelope); err != nil {
		t.Fatalf("decode client frame: %v", err)
	}
	return envelope
}
func (p *fakeManagedProcess) respond(t *testing.T, envelope map[string]json.RawMessage, result any, rpcErr any) {
	t.Helper()
	response := map[string]any{"id": json.RawMessage(envelope["id"])}
	if rpcErr != nil {
		response["error"] = rpcErr
	} else {
		response["result"] = result
	}
	if err := json.NewEncoder(p.serverOutput).Encode(response); err != nil {
		t.Fatalf("write server response: %v", err)
	}
}

type ensureResult struct {
	client *Client
	err    error
}

func beginEnsure(manager *Manager) <-chan ensureResult {
	done := make(chan ensureResult, 1)
	go func() {
		client, err := manager.Ensure(context.Background())
		done <- ensureResult{client: client, err: err}
	}()
	return done
}

func completeInitialization(t *testing.T, process *fakeManagedProcess) {
	t.Helper()
	initialize := process.readEnvelope(t)
	var method string
	if err := json.Unmarshal(initialize["method"], &method); err != nil || method != "initialize" {
		t.Fatalf("first method = %q (%v), want initialize", method, err)
	}
	var params struct {
		ClientInfo struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"clientInfo"`
	}
	if err := json.Unmarshal(initialize["params"], &params); err != nil {
		t.Fatalf("decode initialize params: %v", err)
	}
	if params.ClientInfo.Name != "video-production-console" || params.ClientInfo.Version == "" {
		t.Fatalf("clientInfo = %+v", params.ClientInfo)
	}
	process.respond(t, initialize, map[string]any{}, nil)
	initialized := process.readEnvelope(t)
	if err := json.Unmarshal(initialized["method"], &method); err != nil || method != "initialized" {
		t.Fatalf("second method = %q (%v), want initialized", method, err)
	}
	if _, ok := initialized["id"]; ok {
		t.Fatal("initialized notification contains id")
	}
}

func receiveEnsure(t *testing.T, done <-chan ensureResult) ensureResult {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Ensure")
		return ensureResult{}
	}
}

func eventually(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition was not satisfied")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestManagerConcurrentEnsureStartsOneProcessAndReturnsSameClient(t *testing.T) {
	factory := &fakeProcessFactory{}
	manager := NewManager(factory)
	t.Cleanup(func() { _ = manager.Close() })

	first := beginEnsure(manager)
	second := beginEnsure(manager)
	process := factory.waitForProcess(t)
	completeInitialization(t, process)
	firstResult := receiveEnsure(t, first)
	secondResult := receiveEnsure(t, second)
	if firstResult.err != nil || secondResult.err != nil {
		t.Fatalf("Ensure errors = (%v, %v)", firstResult.err, secondResult.err)
	}
	if firstResult.client == nil || firstResult.client != secondResult.client {
		t.Fatalf("clients = (%p, %p), want same non-nil client", firstResult.client, secondResult.client)
	}
	if starts, _ := factory.snapshot(); starts != 1 {
		t.Fatalf("Start calls = %d, want 1", starts)
	}
}

func TestManagerBecomesHealthyOnlyAfterInitializeAndInitialized(t *testing.T) {
	factory := &fakeProcessFactory{}
	manager := NewManager(factory)
	t.Cleanup(func() { _ = manager.Close() })

	done := beginEnsure(manager)
	process := factory.waitForProcess(t)
	initialize := process.readEnvelope(t)
	if got := manager.Health().Status; got != StatusStarting {
		t.Fatalf("status before initialize response = %q, want %q", got, StatusStarting)
	}
	process.respond(t, initialize, map[string]any{}, nil)
	initialized := process.readEnvelope(t)
	var method string
	_ = json.Unmarshal(initialized["method"], &method)
	if method != "initialized" {
		t.Fatalf("method = %q, want initialized", method)
	}
	result := receiveEnsure(t, done)
	if result.err != nil {
		t.Fatalf("Ensure: %v", result.err)
	}
	health := manager.Health()
	if health.Status != StatusHealthy || health.PID != 4242 || health.LastError != "" {
		t.Fatalf("health = %+v", health)
	}
}

func TestManagerInitializeFailureCleansUpProcess(t *testing.T) {
	factory := &fakeProcessFactory{}
	manager := NewManager(factory)
	done := beginEnsure(manager)
	process := factory.waitForProcess(t)
	initialize := process.readEnvelope(t)
	process.respond(t, initialize, nil, map[string]any{"code": -32000, "message": "rejected"})

	if result := receiveEnsure(t, done); result.err == nil || result.client != nil {
		t.Fatalf("Ensure = (%p, %v), want failure", result.client, result.err)
	}
	eventually(t, func() bool { return process.terminate.Load() == 1 })
	if got := manager.Health().Status; got != StatusFailed {
		t.Fatalf("status = %q, want %q", got, StatusFailed)
	}
}

func TestManagerCanceledInitializationFailsAllWaitersAndCleansUp(t *testing.T) {
	factory := &fakeProcessFactory{}
	manager := NewManager(factory)
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan ensureResult, 1)
	go func() {
		client, err := manager.Ensure(ctx)
		first <- ensureResult{client: client, err: err}
	}()
	process := factory.waitForProcess(t)
	_ = process.readEnvelope(t)
	second := beginEnsure(manager)
	cancel()

	firstResult := receiveEnsure(t, first)
	secondResult := receiveEnsure(t, second)
	if firstResult.client != nil || secondResult.client != nil {
		t.Fatalf("canceled clients = (%p, %p), want nil", firstResult.client, secondResult.client)
	}
	if !errors.Is(firstResult.err, context.Canceled) || !errors.Is(secondResult.err, context.Canceled) {
		t.Fatalf("canceled errors = (%v, %v), want context.Canceled", firstResult.err, secondResult.err)
	}
	eventually(t, func() bool { return process.terminate.Load() == 1 })
}

func TestManagerUnexpectedExitFailsWithoutAutomaticRestart(t *testing.T) {
	factory := &fakeProcessFactory{}
	manager := NewManager(factory)
	done := beginEnsure(manager)
	process := factory.waitForProcess(t)
	completeInitialization(t, process)
	if result := receiveEnsure(t, done); result.err != nil {
		t.Fatalf("Ensure: %v", result.err)
	}

	process.exit()
	eventually(t, func() bool { return manager.Health().Status == StatusFailed })
	time.Sleep(10 * time.Millisecond)
	if starts, _ := factory.snapshot(); starts != 1 {
		t.Fatalf("Start calls after unexpected exit = %d, want 1", starts)
	}
}

func TestManagerCloseTerminatesStoredProcessOnce(t *testing.T) {
	factory := &fakeProcessFactory{}
	manager := NewManager(factory)
	done := beginEnsure(manager)
	process := factory.waitForProcess(t)
	completeInitialization(t, process)
	if result := receiveEnsure(t, done); result.err != nil {
		t.Fatalf("Ensure: %v", result.err)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if got := process.terminate.Load(); got != 1 {
		t.Fatalf("Terminate calls = %d, want 1", got)
	}
	if client, err := manager.Ensure(context.Background()); !errors.Is(err, ErrManagerClosed) || client != nil {
		t.Fatalf("Ensure after Close = (%p, %v), want ErrManagerClosed", client, err)
	}
}

func TestManagerStderrTailIsBounded(t *testing.T) {
	factory := &fakeProcessFactory{}
	manager := NewManagerWithOptions(factory, ManagerOptions{StderrLimit: 8, ClientVersion: "test"})
	t.Cleanup(func() { _ = manager.Close() })
	done := beginEnsure(manager)
	process := factory.waitForProcess(t)
	if _, err := io.WriteString(process.stderrWriter, "0123456789abcdef"); err != nil {
		t.Fatalf("write stderr: %v", err)
	}
	completeInitialization(t, process)
	if result := receiveEnsure(t, done); result.err != nil {
		t.Fatalf("Ensure: %v", result.err)
	}
	eventually(t, func() bool { return manager.Health().StderrTail == "89abcdef" })
	if got := len(manager.Health().StderrTail); got > 8 {
		t.Fatalf("stderr tail length = %d, want <= 8", got)
	}
}

func TestManagerCloseRacingEnsureNeverReturnsHalfInitializedClient(t *testing.T) {
	factory := &fakeProcessFactory{}
	manager := NewManager(factory)
	done := beginEnsure(manager)
	process := factory.waitForProcess(t)
	_ = process.readEnvelope(t)

	if err := manager.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	result := receiveEnsure(t, done)
	if result.client != nil || result.err == nil {
		t.Fatalf("racing Ensure = (%p, %v), want nil client and error", result.client, result.err)
	}
	if got := process.terminate.Load(); got != 1 {
		t.Fatalf("Terminate calls = %d, want 1", got)
	}
}

func TestManagerRetriesOnlyOnNextExplicitEnsure(t *testing.T) {
	factory := &fakeProcessFactory{}
	manager := NewManager(factory)
	t.Cleanup(func() { _ = manager.Close() })

	first := beginEnsure(manager)
	firstProcess := factory.waitForProcess(t)
	request := firstProcess.readEnvelope(t)
	firstProcess.respond(t, request, nil, map[string]any{"code": -1, "message": "no"})
	if result := receiveEnsure(t, first); result.err == nil {
		t.Fatal("first Ensure unexpectedly succeeded")
	}
	if starts, _ := factory.snapshot(); starts != 1 {
		t.Fatalf("Start calls before retry = %d, want 1", starts)
	}

	second := beginEnsure(manager)
	eventually(t, func() bool { starts, _ := factory.snapshot(); return starts == 2 })
	_, processes := factory.snapshot()
	completeInitialization(t, processes[1])
	if result := receiveEnsure(t, second); result.err != nil || result.client == nil {
		t.Fatalf("second Ensure = (%p, %v)", result.client, result.err)
	}
}

func TestManagerHealthDoesNotExposeFactoryConfiguration(t *testing.T) {
	healthJSON, err := json.Marshal(Health{Status: StatusFailed, PID: 1, LastError: "failed", StderrTail: "tail"})
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"endpoint", "token", "environment", "binary", "working"} {
		if strings.Contains(strings.ToLower(string(healthJSON)), forbidden) {
			t.Fatalf("health JSON exposes forbidden field %q: %s", forbidden, healthJSON)
		}
	}
}
