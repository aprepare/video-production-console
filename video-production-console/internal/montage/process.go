package montage

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
)

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, spec CommandSpec, limit int64) (CommandResult, error) {
	if limit < 1 {
		return CommandResult{}, fmt.Errorf("command output limit must be positive")
	}
	command := exec.CommandContext(ctx, spec.Program, spec.Args...)
	command.Dir = spec.Dir
	stdout, stderr := &boundedBuffer{limit: limit}, &boundedBuffer{limit: limit}
	command.Stdout, command.Stderr = stdout, stderr
	err := command.Run()
	result := CommandResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	if command.ProcessState != nil {
		result.ExitCode = command.ProcessState.ExitCode()
	}
	if stdout.overflow || stderr.overflow {
		return result, fmt.Errorf("registration command output exceeded limit")
	}
	if _, ok := err.(*exec.ExitError); ok {
		return result, nil
	}
	return result, err
}

type boundedBuffer struct {
	bytes.Buffer
	limit    int64
	overflow bool
}

func (b *boundedBuffer) Write(value []byte) (int, error) {
	want := len(value)
	remaining := b.limit - int64(b.Len())
	if remaining <= 0 {
		b.overflow = true
		return want, nil
	}
	if int64(len(value)) > remaining {
		value = value[:remaining]
		b.overflow = true
	}
	_, _ = b.Buffer.Write(value)
	return want, nil
}
