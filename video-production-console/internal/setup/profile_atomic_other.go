//go:build !windows

package setup

import "os"

func replaceProfileFile(source, target string) error { return os.Rename(source, target) }
