//go:build windows

package assets

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func canonicalNoFollow(path string, directory bool) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil || strings.TrimSpace(path) == "" {
		return "", os.ErrInvalid
	}
	abs = filepath.Clean(abs)
	name, err := windows.UTF16PtrFromString(abs)
	if err != nil {
		return "", os.ErrInvalid
	}
	flags := uint32(windows.FILE_FLAG_OPEN_REPARSE_POINT)
	if directory {
		flags |= windows.FILE_FLAG_BACKUP_SEMANTICS
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, flags, 0)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)
	var info windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(handle, &info) != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return "", os.ErrInvalid
	}
	final, err := finalPath(handle)
	if err != nil || !strings.EqualFold(final, abs) || (info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0) != directory {
		return "", os.ErrInvalid
	}
	return abs, nil
}

func finalPath(handle windows.Handle) (string, error) {
	buffer := make([]uint16, 512)
	for {
		n, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
		if err != nil {
			return "", err
		}
		if n < uint32(len(buffer)) {
			return filepath.Clean(strings.TrimPrefix(windows.UTF16ToString(buffer[:n]), `\\?\`)), nil
		}
		buffer = make([]uint16, n+1)
	}
}
