//go:build windows

package codexapp

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"

	"video-production-console/internal/codex"
)

type commandManagedProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser

	terminateOnce sync.Once
	terminateErr  error
}

func startCommandProcess(ctx context.Context, config ProcessConfig) (ManagedProcess, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Do not force strict config validation here. The desktop Codex config can
	// contain feature flags from another CLI version; strict mode would make the
	// app server exit before the JSON-RPC initialize handshake.
	// The manager cancels its startup context after the handshake. Keep the
	// child lifetime under explicit Manager.Close/Terminate ownership.
	launch, err := codex.ResolveCommandLaunch(config.CodexBinary)
	if err != nil {
		return nil, fmt.Errorf("resolve Codex app process command: %w", err)
	}
	args := append(append([]string(nil), launch.PrefixArgs...), "app-server", "--stdio", "--disable", "plugins")
	cmd := exec.Command(launch.Executable, args...)
	cmd.Dir = config.WorkingDirectory
	cmd.Env = make([]string, len(config.Environment))
	copy(cmd.Env, config.Environment)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		closeProcessPipes(stdin, stdout, stderr)
		return nil, err
	}

	return &commandManagedProcess{
		cmd: cmd, stdin: stdin, stdout: stdout, stderr: stderr,
	}, nil
}

func (p *commandManagedProcess) Stdin() io.WriteCloser { return p.stdin }
func (p *commandManagedProcess) Stdout() io.ReadCloser { return p.stdout }
func (p *commandManagedProcess) Stderr() io.ReadCloser { return p.stderr }
func (p *commandManagedProcess) PID() int              { return p.cmd.Process.Pid }

func (p *commandManagedProcess) Wait() error {
	return p.cmd.Wait()
}

func (p *commandManagedProcess) Terminate() error {
	p.terminateOnce.Do(func() {
		if p.cmd.Process != nil {
			p.terminateErr = p.cmd.Process.Kill()
		}
	})
	return p.terminateErr
}

func closeProcessPipes(pipes ...io.Closer) {
	for _, pipe := range pipes {
		if pipe != nil {
			_ = pipe.Close()
		}
	}
}
