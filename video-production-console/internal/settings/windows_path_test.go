//go:build windows

package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsSettingsPathsAllowAnotherFixedLocalVolume(t *testing.T) {
	root, err := windows.UTF16PtrFromString(`E:\`)
	if err != nil {
		t.Fatal(err)
	}
	if windows.GetDriveType(root) != windows.DRIVE_FIXED {
		t.Skip("E: is not a fixed local volume on this machine")
	}
	if err := validateCanonicalAbsolutePath(`E:\media\index.json`); err != nil {
		t.Fatalf("fixed local volume was rejected: %v", err)
	}
}

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
