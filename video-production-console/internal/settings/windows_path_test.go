//go:build windows

package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsSettingsPathsRejectUNCDeviceAndNonLocalVolumes(t *testing.T) {
	localVolume := filepath.VolumeName(os.TempDir())
	foreignVolume := "Z:"
	if strings.EqualFold(localVolume, foreignVolume) {
		foreignVolume = "Y:"
	}
	for _, path := range []string{
		`\\server\share\skill`,
		`\\?\C:\skill`,
		`\\.\PIPE\console`,
		foreignVolume + `\skill`,
	} {
		if err := validateCanonicalAbsolutePath(path); err == nil {
			t.Errorf("validateCanonicalAbsolutePath(%q) accepted unsafe Windows path", path)
		}
	}
}
