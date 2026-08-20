//go:build windows

package main

import (
	"os/exec"

	"golang.org/x/sys/windows"
)

func hideChildConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &windows.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW,
	}
}
