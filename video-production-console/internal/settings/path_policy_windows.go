//go:build windows

package settings

import (
	"errors"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func validatePlatformLocalPath(value string) error {
	if strings.HasPrefix(value, `\\`) {
		return errors.New("UNC and device paths are not allowed")
	}
	volume := filepath.VolumeName(value)
	if len(volume) != 2 || volume[1] != ':' || !strings.ContainsAny(volume[:1], "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz") {
		return errors.New("path must use a local drive letter")
	}
	root, err := windows.UTF16PtrFromString(volume + `\`)
	if err != nil || windows.GetDriveType(root) != windows.DRIVE_FIXED {
		return errors.New("path must use a fixed local volume")
	}
	return nil
}
