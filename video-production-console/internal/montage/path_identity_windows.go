//go:build windows

package montage

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// canonicalNoFollow binds a path to the final handle identity while rejecting
// reparse points, symlinks, junctions, and paths traversing an alias.
func canonicalNoFollow(path string, directory bool) (string, error) {
	// Registration receipts and Python-produced manifests may use the Windows
	// extended-length prefix. Normalize it before filepath.Abs; otherwise Go
	// treats the prefix as part of a relative path and rejects a valid target.
	path = stripWindowsExtendedPath(strings.TrimSpace(path))
	abs, err := filepath.Abs(path)
	if err != nil || strings.TrimSpace(path) == "" {
		return "", os.ErrInvalid
	}
	abs = filepath.Clean(abs)
	openPath := windowsExtendedPath(abs)
	name, err := windows.UTF16PtrFromString(openPath)
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
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return "", err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return "", os.ErrInvalid
	}
	final, err := montageFinalPath(handle)
	if err != nil || !strings.EqualFold(filepath.Clean(stripWindowsExtendedPath(final)), abs) {
		return "", os.ErrInvalid
	}
	isDirectory := info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0
	if isDirectory != directory {
		return "", os.ErrInvalid
	}
	return abs, nil
}

func windowsExtendedPath(path string) string {
	if strings.HasPrefix(path, `\\?\`) {
		return path
	}
	if strings.HasPrefix(path, `\\`) {
		return `\\?\UNC\` + strings.TrimPrefix(path, `\\`)
	}
	return `\\?\` + path
}

func stripWindowsExtendedPath(path string) string {
	// Jianying's root index serializes extended paths as //?/C:/... whereas
	// Windows APIs and registration receipts normally use \\?\C:\... .
	path = strings.ReplaceAll(path, "/", `\`)
	if strings.HasPrefix(path, `\\?\UNC\`) {
		return `\\` + strings.TrimPrefix(path, `\\?\UNC\`)
	}
	return strings.TrimPrefix(path, `\\?\`)
}

func montageFinalPath(handle windows.Handle) (string, error) {
	buffer := make([]uint16, 512)
	for {
		n, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
		if err != nil {
			return "", err
		}
		if n < uint32(len(buffer)) {
			path := strings.TrimPrefix(windows.UTF16ToString(buffer[:n]), `\\?\`)
			return filepath.Clean(path), nil
		}
		buffer = make([]uint16, n+1)
	}
}
