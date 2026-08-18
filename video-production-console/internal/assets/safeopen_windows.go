//go:build windows

package assets

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func openPathNoFollow(path, allowedRoot string, directory bool) (*os.File, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, ErrAssetPathInvalid
	}
	name, err := windows.UTF16PtrFromString(abs)
	if err != nil {
		return nil, ErrAssetPathInvalid
	}
	flags := uint32(windows.FILE_FLAG_OPEN_REPARSE_POINT)
	if directory {
		flags |= windows.FILE_FLAG_BACKUP_SEMANTICS
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, flags, 0)
	if err != nil {
		return nil, err
	}
	closeHandle := true
	defer func() {
		if closeHandle {
			_ = windows.CloseHandle(handle)
		}
	}()
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return nil, err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return nil, ErrAssetPathInvalid
	}
	final, err := finalPathForHandle(handle)
	if err != nil || !strings.EqualFold(filepath.Clean(final), filepath.Clean(abs)) {
		return nil, ErrAssetPathInvalid
	}
	if allowedRoot != "" && !pathInside(allowedRoot, final) {
		return nil, ErrAssetPathInvalid
	}
	file := os.NewFile(uintptr(handle), abs)
	if file == nil {
		return nil, ErrAssetPathInvalid
	}
	stat, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if stat.IsDir() != directory || !directory && !stat.Mode().IsRegular() {
		_ = file.Close()
		return nil, ErrAssetPathInvalid
	}
	closeHandle = false
	return file, nil
}

func finalPathForHandle(handle windows.Handle) (string, error) {
	buffer := make([]uint16, 512)
	for {
		n, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
		if err != nil {
			return "", err
		}
		if n < uint32(len(buffer)) {
			path := windows.UTF16ToString(buffer[:n])
			path = strings.TrimPrefix(path, `\\?\`)
			return filepath.Clean(path), nil
		}
		buffer = make([]uint16, n+1)
	}
}
