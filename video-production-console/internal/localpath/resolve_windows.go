//go:build windows

package localpath

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// Resolve 同时解析符号链接和目录联接。Go 的 EvalSymlinks 在部分版本中
// 将 junction 识别成不规则文件，需由文件句柄取得实际访问的位置。
func Resolve(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		resolved = path
	}
	name, err := windows.UTF16PtrFromString(resolved)
	if err != nil {
		return "", &os.PathError{Op: "resolve", Path: path, Err: err}
	}
	handle, err := windows.CreateFile(name, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", &os.PathError{Op: "resolve", Path: path, Err: err}
	}
	defer windows.CloseHandle(handle)
	buffer := make([]uint16, 260)
	for {
		n, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
		if err != nil {
			return "", &os.PathError{Op: "resolve", Path: path, Err: err}
		}
		if n >= uint32(len(buffer)) {
			buffer = make([]uint16, n+1)
			continue
		}
		result := windows.UTF16ToString(buffer[:n])
		if strings.HasPrefix(result, `\\?\UNC\`) {
			result = `\\` + strings.TrimPrefix(result, `\\?\UNC\`)
		} else {
			result = strings.TrimPrefix(result, `\\?\`)
		}
		return filepath.Clean(result), nil
	}
}
