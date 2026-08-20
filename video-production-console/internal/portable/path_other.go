//go:build !windows

package portable

import (
	"fmt"
	"os"
)

func isReparse(info os.FileInfo) bool {
	return false
}

func openRegularNoFollow(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if isSymlinkOrReparse(info) {
		return nil, fmt.Errorf("%w: %q", ErrSymlink, path)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %q", ErrUnsafePath, path)
	}
	return os.Open(path)
}
