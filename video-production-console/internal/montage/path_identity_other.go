//go:build !windows

package montage

import (
	"os"
	"path/filepath"
	"strings"
)

func canonicalNoFollow(path string, directory bool) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil || strings.TrimSpace(path) == "" {
		return "", os.ErrInvalid
	}
	abs = filepath.Clean(abs)
	before, err := os.Lstat(abs)
	if err != nil || before.Mode()&os.ModeSymlink != 0 {
		return "", os.ErrInvalid
	}
	file, err := os.Open(abs)
	if err != nil {
		return "", err
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || after.IsDir() != directory || (!directory && !after.Mode().IsRegular()) {
		return "", os.ErrInvalid
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil || !samePath(resolved, abs) {
		return "", os.ErrInvalid
	}
	return abs, nil
}
