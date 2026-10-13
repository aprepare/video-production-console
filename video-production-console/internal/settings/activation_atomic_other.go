//go:build !windows

package settings

import "os"

func replaceFileAtomic(source, destination string) error {
	return os.Rename(source, destination)
}
