//go:build windows

package codex

import (
	"fmt"
	"os/exec"
)

func terminateProcess(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	// taskkill terminates the shell and its descendants; fall back to the direct handle.
	if err := exec.Command("taskkill", "/PID", fmt.Sprint(cmd.Process.Pid), "/T", "/F").Run(); err != nil {
		_ = cmd.Process.Kill()
	}
}
