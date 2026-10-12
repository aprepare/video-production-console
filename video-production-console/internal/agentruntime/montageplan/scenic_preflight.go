package montageplan

import (
	"fmt"
	"strings"
)

// ValidateMediaLibraryForPreset uses the same pool as the selected planner.
// An unused missing legacy clip must not block a curated scenic job.
func ValidateMediaLibraryForPreset(preset, indexPath, mediaRoot, profilePath string) error {
	if preset != mixPresetScenicSemantic {
		return ValidateMediaLibrary(indexPath, mediaRoot, profilePath)
	}
	if strings.TrimSpace(profilePath) != "" && (strings.TrimSpace(indexPath) == "" || strings.TrimSpace(mediaRoot) == "") {
		root, index, err := readProfilePaths(profilePath)
		if err != nil {
			return err
		}
		if strings.TrimSpace(mediaRoot) == "" {
			mediaRoot = root
		}
		if strings.TrimSpace(indexPath) == "" {
			indexPath = index
		}
	}
	if strings.TrimSpace(mediaRoot) == "" || strings.TrimSpace(indexPath) == "" {
		return fmt.Errorf("media_root and media_index_path are required")
	}
	_, err := scenicPool(&planContext{mediaRoot: mediaRoot, mediaIndex: indexPath})
	return err
}
