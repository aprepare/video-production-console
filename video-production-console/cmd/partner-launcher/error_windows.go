//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func reportLaunchError(err error) {
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, err)
	if dir, dirErr := os.UserCacheDir(); dirErr == nil {
		logDir := filepath.Join(dir, productDirName)
		_ = os.MkdirAll(logDir, 0o700)
		_ = os.WriteFile(filepath.Join(logDir, "launcher-error.txt"), []byte(err.Error()+"\n"), 0o600)
	}
	_, _ = windows.MessageBox(0, windows.StringToUTF16Ptr(err.Error()), windows.StringToUTF16Ptr("伙伴控制台无法启动"), windows.MB_OK|windows.MB_ICONERROR)
}
