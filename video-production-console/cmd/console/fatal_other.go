//go:build !windows

package main

func showFatalDialog(title, message string) {
	_, _ = title, message
}
