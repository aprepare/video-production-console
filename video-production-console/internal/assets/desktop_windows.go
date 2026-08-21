//go:build windows

package assets

import (
	"context"
	"os/exec"
)

type platformDesktopOpener struct{}

func (platformDesktopOpener) OpenDirectory(_ context.Context, directory string) error {
	// Deliberately NOT CommandContext: the HTTP request context is canceled the
	// moment the handler returns, which kills explorer.exe before it can show
	// the window — the button then "does nothing" despite a 204.
	return exec.Command("explorer.exe", directory).Start()
}
