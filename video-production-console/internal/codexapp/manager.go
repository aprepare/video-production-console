package codexapp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
)

const (
	defaultStderrLimit   = 64 << 10
	defaultClientVersion = "1.0.0"
)

type ProcessFactory interface {
	Start(context.Context) (ManagedProcess, error)
}

type ManagedProcess interface {
	Stdin() io.WriteCloser
	Stdout() io.ReadCloser
	Stderr() io.ReadCloser
	Wait() error
	Terminate() error
}

type ProcessConfig struct {
	CodexBinary      string
	WorkingDirectory string
	Environment      []string
}

type CommandProcessFactory struct {
	config ProcessConfig
}

func NewCommandProcessFactory(config ProcessConfig) *CommandProcessFactory {
	config.Environment = append([]string(nil), config.Environment...)
	return &CommandProcessFactory{config: config}
}

func (f *CommandProcessFactory) Start(ctx context.Context) (ManagedProcess, error) {
	if strings.TrimSpace(f.config.CodexBinary) == "" {
		return nil, errors.New("codex app process binary is empty")
	}
	if strings.TrimSpace(f.config.WorkingDirectory) == "" {
		return nil, errors.New("codex app process working directory is empty")
	}
	return startCommandProcess(ctx, f.config)
}

type Status string

const (
	StatusIdle     Status = "idle"
	StatusStarting Status = "starting"
	StatusHealthy  Status = "healthy"
	StatusFailed   Status = "failed"
	StatusClosed   Status = "closed"
)

var ErrManagerClosed = errors.New("codex app manager closed")

type Health struct {
	Status     Status `json:"status"`
	PID        int    `json:"pid,omitempty"`
	LastError  string `json:"last_error,omitempty"`
	StderrTail string `json:"stderr_tail,omitempty"`
}

type ManagerOptions struct {
	StderrLimit   int
	ClientVersion string
}

type Manager struct {
	factory ProcessFactory
	options ManagerOptions

	mu         sync.Mutex
	closed     bool
	status     Status
	lastError  string
	current    *processInstance
	active     *processInstance
	starting   *startAttempt
	lastStderr *byteRing
	closeOnce  sync.Once
	closeErr   error
}

type startAttempt struct {
	done   chan struct{}
	cancel context.CancelFunc
	client *Client
	err    error
	// joined counts callers that attached to this in-flight attempt instead of
	// starting their own. It only ever grows, so observing it is a reliable
	// "the caller is now parked" signal for tests that must cancel an attempt
	// after a second waiter has joined it.
	joined atomic.Int64
}

type processInstance struct {
	process ManagedProcess
	client  *Client
	owned   *ownedProcess
	stderr  *byteRing
	pid     int
	exited  bool
}

type ownedProcess struct {
	process  ManagedProcess
	stdin    io.WriteCloser
	stdout   io.ReadCloser
	stderr   io.ReadCloser
	expected atomic.Bool
	once     sync.Once
	err      error
}

func (p *ownedProcess) close() error {
	p.once.Do(func() {
		var errs []error
		if p.stdin != nil {
			errs = appendError(errs, p.stdin.Close())
		}
		if p.stdout != nil {
			errs = appendError(errs, p.stdout.Close())
		}
		if p.stderr != nil {
			errs = appendError(errs, p.stderr.Close())
		}
		errs = appendError(errs, p.process.Terminate())
		p.err = errors.Join(errs...)
	})
	return p.err
}

func appendError(errs []error, err error) []error {
	if err != nil && !errors.Is(err, io.ErrClosedPipe) {
		return append(errs, err)
	}
	return errs
}

func NewManager(factory ProcessFactory) *Manager {
	return NewManagerWithOptions(factory, ManagerOptions{})
}

func NewManagerWithOptions(factory ProcessFactory, options ManagerOptions) *Manager {
	if options.StderrLimit <= 0 {
		options.StderrLimit = defaultStderrLimit
	}
	if strings.TrimSpace(options.ClientVersion) == "" {
		options.ClientVersion = defaultClientVersion
	}
	return &Manager{factory: factory, options: options, status: StatusIdle}
}

// Ensure returns the single initialized client owned by the Manager. Concurrent
// callers join the same startup attempt. A failed process is retried only by a
// later explicit call to Ensure.
func (m *Manager) Ensure(ctx context.Context) (*Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, ErrManagerClosed
	}
	if m.active != nil {
		client := m.active.client
		m.mu.Unlock()
		return client, nil
	}
	if attempt := m.starting; attempt != nil {
		m.mu.Unlock()
		return waitForAttempt(ctx, attempt)
	}
	attemptCtx, cancel := context.WithCancel(ctx)
	attempt := &startAttempt{done: make(chan struct{}), cancel: cancel}
	m.starting = attempt
	m.status = StatusStarting
	m.lastError = ""
	m.mu.Unlock()

	client, err := m.start(attemptCtx, attempt)
	cancel()
	m.finishAttempt(attempt, client, err)
	return client, err
}

func waitForAttempt(ctx context.Context, attempt *startAttempt) (*Client, error) {
	attempt.joined.Add(1)
	select {
	case <-attempt.done:
		return attempt.client, attempt.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (m *Manager) start(ctx context.Context, attempt *startAttempt) (*Client, error) {
	if m.factory == nil {
		return nil, errors.New("start codex app process: process factory is nil")
	}
	process, err := m.factory.Start(ctx)
	if err != nil {
		return nil, fmt.Errorf("start codex app process: %w", err)
	}
	if process == nil {
		return nil, errors.New("start codex app process: factory returned a nil process")
	}
	stdin := process.Stdin()
	stdout := process.Stdout()
	stderr := process.Stderr()
	if stdin == nil || stdout == nil || stderr == nil {
		closeProcessPipes(stdin, stdout, stderr)
		if process != nil {
			_ = process.Terminate()
		}
		return nil, errors.New("start codex app process: process returned nil stdio")
	}

	owned := &ownedProcess{
		process: process,
		stdin:   stdin,
		stdout:  stdout,
		stderr:  stderr,
	}
	instance := &processInstance{
		process: process,
		owned:   owned,
		stderr:  newByteRing(m.options.StderrLimit),
	}
	if processWithPID, ok := process.(interface{ PID() int }); ok {
		instance.pid = processWithPID.PID()
	}
	client := NewClientWithClose(owned.stdout, owned.stdin, owned.close)
	instance.client = client

	m.mu.Lock()
	if m.closed || m.starting != attempt {
		m.mu.Unlock()
		owned.expected.Store(true)
		_ = client.Close()
		return nil, ErrManagerClosed
	}
	m.current = instance
	m.lastStderr = instance.stderr
	m.mu.Unlock()

	go m.captureStderr(instance)
	go m.waitProcess(instance)

	var initializeResult any
	err = client.Call(ctx, "initialize", map[string]any{
		"clientInfo": map[string]string{
			"name":    "video-production-console",
			"version": m.options.ClientVersion,
		},
	}, &initializeResult)
	if err == nil {
		err = client.Notify(ctx, "initialized", map[string]any{})
	}
	if err != nil {
		m.failStartingInstance(instance, err)
		if stderrTail := strings.TrimSpace(instance.stderr.String()); stderrTail != "" {
			return nil, fmt.Errorf("initialize codex app process: %w; codex diagnostics: %s", err, stderrTail)
		}
		return nil, fmt.Errorf("initialize codex app process: %w", err)
	}

	m.mu.Lock()
	if m.closed || m.current != instance || instance.exited {
		closed := m.closed
		m.mu.Unlock()
		m.failStartingInstance(instance, ErrManagerClosed)
		if closed {
			return nil, ErrManagerClosed
		}
		return nil, errors.New("codex app process exited during initialization")
	}
	m.active = instance
	m.status = StatusHealthy
	m.lastError = ""
	m.mu.Unlock()
	return client, nil
}

func (m *Manager) failStartingInstance(instance *processInstance, cause error) {
	instance.owned.expected.Store(true)
	_ = instance.client.Close()
	m.mu.Lock()
	if m.current == instance {
		m.current = nil
	}
	if m.active == instance {
		m.active = nil
	}
	if !m.closed {
		m.status = StatusFailed
		m.lastError = cause.Error()
	}
	m.mu.Unlock()
}

func (m *Manager) finishAttempt(attempt *startAttempt, client *Client, err error) {
	m.mu.Lock()
	attempt.client = client
	attempt.err = err
	if m.starting == attempt {
		m.starting = nil
	}
	if err != nil && !m.closed {
		m.status = StatusFailed
		m.lastError = err.Error()
	}
	close(attempt.done)
	m.mu.Unlock()
}

func (m *Manager) captureStderr(instance *processInstance) {
	_, _ = io.Copy(instance.stderr, instance.owned.stderr)
}

func (m *Manager) waitProcess(instance *processInstance) {
	err := instance.process.Wait()
	m.mu.Lock()
	instance.exited = true
	if m.current == instance && !instance.owned.expected.Load() && !m.closed {
		m.current = nil
		m.active = nil
		m.status = StatusFailed
		transportErr := instance.client.getTerminalError()
		if err != nil && transportErr != nil && !errors.Is(transportErr, ErrClosed) {
			m.lastError = fmt.Sprintf("codex app process exited: %v; transport: %v", err, transportErr)
		} else if err != nil {
			m.lastError = fmt.Sprintf("codex app process exited: %v", err)
		} else {
			m.lastError = "codex app process exited unexpectedly"
		}
	}
	m.mu.Unlock()
}

func (m *Manager) Health() Health {
	m.mu.Lock()
	health := Health{Status: m.status, LastError: m.lastError}
	if m.current != nil {
		health.PID = m.current.pid
	}
	stderr := m.lastStderr
	m.mu.Unlock()
	if stderr != nil {
		health.StderrTail = stderr.String()
	}
	return health
}

func (m *Manager) Close() error {
	m.closeOnce.Do(func() {
		m.mu.Lock()
		m.closed = true
		m.status = StatusClosed
		attempt := m.starting
		instance := m.current
		m.current = nil
		m.active = nil
		if attempt != nil {
			attempt.cancel()
		}
		if instance != nil {
			instance.owned.expected.Store(true)
		}
		m.mu.Unlock()

		if instance != nil {
			m.closeErr = instance.client.Close()
		}
		if attempt != nil {
			<-attempt.done
		}
	})
	return m.closeErr
}

type byteRing struct {
	mu    sync.Mutex
	limit int
	data  []byte
}

func newByteRing(limit int) *byteRing {
	return &byteRing{limit: limit, data: make([]byte, 0, limit)}
}

func (r *byteRing) Write(payload []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	written := len(payload)
	if len(payload) >= r.limit {
		r.data = append(r.data[:0], payload[len(payload)-r.limit:]...)
		return written, nil
	}
	overflow := len(r.data) + len(payload) - r.limit
	if overflow > 0 {
		copy(r.data, r.data[overflow:])
		r.data = r.data[:len(r.data)-overflow]
	}
	r.data = append(r.data, payload...)
	return written, nil
}

func (r *byteRing) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return string(append([]byte(nil), r.data...))
}
