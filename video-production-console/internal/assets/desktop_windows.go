//go:build windows

package assets

import (
	"context"
	"os/exec"
)

type platformDesktopOpener struct{}

func (platformDesktopOpener) OpenDirectory(ctx context.Context, directory string) error {
	return exec.CommandContext(ctx, "explorer.exe", directory).Start()
}
