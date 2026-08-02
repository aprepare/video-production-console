//go:build windows

package codex

import (
	"os"
	"path/filepath"
)

func openVerifiedArtifact(path, root string) (*os.File, string, os.FileInfo, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, "", nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 {
		return nil, "", nil, os.ErrInvalid
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, "", nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, "", nil, err
	}
	after, err := os.Lstat(path)
	if err != nil || after.Mode()&os.ModeSymlink != 0 {
		_ = f.Close()
		return nil, "", nil, os.ErrInvalid
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		_ = f.Close()
		return nil, "", nil, err
	}
	return f, resolved, info, nil
}
