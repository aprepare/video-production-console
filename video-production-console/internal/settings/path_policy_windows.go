//go:build windows

package settings

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func validatePlatformLocalPath(value string) error {
	if strings.HasPrefix(value, `\\`) {
		return errors.New("UNC and device paths are not allowed")
	}
	volume := filepath.VolumeName(value)
	localVolume := filepath.VolumeName(os.TempDir())
	if volume == "" || localVolume == "" || !strings.EqualFold(volume, localVolume) {
		return errors.New("path must use the local application volume")
	}
	return nil
}
