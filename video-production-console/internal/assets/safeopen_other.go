//go:build !windows

package assets

import (
	"os"
	"path/filepath"
)

func openPathNoFollow(path, allowedRoot string, directory bool) (*os.File, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 {
		return nil, ErrAssetPathInvalid
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	after, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !os.SameFile(before, after) || after.IsDir() != directory || !directory && !after.Mode().IsRegular() {
		_ = file.Close()
		return nil, ErrAssetPathInvalid
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		_ = file.Close()
		return nil, ErrAssetPathInvalid
	}
	abs, err := filepath.Abs(path)
	if err != nil || filepath.Clean(resolved) != filepath.Clean(abs) {
		_ = file.Close()
		return nil, ErrAssetPathInvalid
	}
	if allowedRoot != "" && !pathInside(allowedRoot, resolved) {
		_ = file.Close()
		return nil, ErrAssetPathInvalid
	}
	current, err := os.Stat(path)
	if err != nil || !os.SameFile(after, current) {
		_ = file.Close()
		return nil, ErrAssetPathInvalid
	}
	return file, nil
}
