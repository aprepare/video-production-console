//go:build !windows

package main

import "os/exec"

func hideChildConsole(*exec.Cmd) {}
