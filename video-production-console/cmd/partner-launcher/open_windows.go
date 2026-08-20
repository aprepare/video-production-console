//go:build windows

package main

import "os/exec"

func defaultOpenUI(rawURL string) error {
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL).Start()
}
