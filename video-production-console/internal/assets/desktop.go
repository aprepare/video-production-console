package assets

import "context"

type DesktopOpener interface {
	OpenDirectory(context.Context, string) error
}

func NewDesktopOpener() DesktopOpener { return platformDesktopOpener{} }
