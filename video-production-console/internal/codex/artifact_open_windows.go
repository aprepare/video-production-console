//go:build windows

package codex

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func openVerifiedArtifact(path, root string) (*os.File, string, os.FileInfo, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, "", nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 {
		return nil, "", nil, os.ErrInvalid
	}
	h, err := windows.CreateFile(windows.StringToUTF16Ptr(path), windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, "", nil, err
	}
	f := os.NewFile(uintptr(h), path)
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
	finalPath, err := finalPathByHandle(windows.Handle(h))
	if err != nil {
		_ = f.Close()
		return nil, "", nil, err
	}
	finalPath = strings.TrimPrefix(finalPath, `\\?\`)
	finalAbs, err := filepath.Abs(finalPath)
	if err != nil || !samePath(finalAbs, resolved) {
		_ = f.Close()
		return nil, "", nil, fmt.Errorf("artifact path changed during validation")
	}
	return f, resolved, info, nil
}

func finalPathByHandle(h windows.Handle) (string, error) {
	buf := make([]uint16, 512)
	for {
		n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), 0)
		if err != nil {
			return "", err
		}
		if n < uint32(len(buf))-1 {
			return windows.UTF16ToString(buf[:n]), nil
		}
		buf = make([]uint16, len(buf)*2)
	}
}

func samePath(a, b string) bool {
	a = normalizeWindowsExtendedPath(a)
	b = normalizeWindowsExtendedPath(b)
	a, _ = filepath.Abs(a)
	b, _ = filepath.Abs(b)
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}
