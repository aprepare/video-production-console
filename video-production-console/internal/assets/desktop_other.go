//go:build !windows

package assets

import (
	"context"
	"errors"
)

type platformDesktopOpener struct{}

func (platformDesktopOpener) OpenDirectory(context.Context, string) error {
	return errors.New("desktop directory opening is unsupported on this platform")
}
