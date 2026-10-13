package montageplan

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"video-production-console/internal/medialibrary"
)

func cityCategory(category string) bool {
	switch category {
	case "Housing_Residential", "City_Aerial", "Streets", "Architecture_Exterior":
		return true
	}
	return false
}

func cityOpeningCategory(category string) bool {
	return category == "Housing_Residential" || category == "City_Aerial"
}

func cityCatalogPath(root string) string {
	return filepath.Join(root, "00_INDEX", "production_catalog.sqlite3")
}

func cityLibraryMode(root string, p ScenicPolicy) bool {
	if strings.TrimSpace(root) == "" {
		return false
	}
	if p.Version == 2 && p.ContentScope == "architecture_city_only" {
		return true
	}
	info, err := os.Stat(cityCatalogPath(root))
	return err == nil && info.Mode().IsRegular()
}

// CityLibraryPath identifies the catalog without opening a legacy media index.
func CityLibraryPath(indexPath, mediaRoot string) (string, error) {
	return CityLibraryPathForPlan(indexPath, mediaRoot, "")
}

// CityLibraryPathForPlan honors the policy already frozen by an earlier attempt.
func CityLibraryPathForPlan(indexPath, mediaRoot, planPath string) (string, error) {
	if strings.TrimSpace(mediaRoot) == "" {
		return "", nil
	}
	p, err := cityLibraryPolicy(indexPath, mediaRoot, planPath)
	if err != nil {
		return "", err
	}
	if cityLibraryMode(mediaRoot, p) {
		return cityCatalogPath(mediaRoot), nil
	}
	return "", nil
}

func cityLibraryPolicy(indexPath, mediaRoot, planPath string) (ScenicPolicy, error) {
	p := defaultScenicPolicy()
	if planPath != "" {
		raw, err := os.ReadFile(filepath.Join(filepath.Dir(planPath), "scenic_policy_snapshot.json"))
		if err == nil {
			var snap scenicSnapshot
			if err := json.Unmarshal(raw, &snap); err != nil {
				return p, err
			}
			return snap.Policy, validateCityLibraryPolicy(mediaRoot, snap.Policy)
		}
		if !os.IsNotExist(err) {
			return p, err
		}
	}
	var paths []string
	// Preserve the China policy's original index precedence. A catalog root never
	// reads a policy beside an obsolete JSON index.
	if indexPath != "" && !cityLibraryMode(mediaRoot, p) {
		paths = append(paths, filepath.Join(filepath.Dir(indexPath), "scenic_policy.json"))
	}
	if mediaRoot != "" {
		paths = append(paths, filepath.Join(mediaRoot, "00_INDEX", "scenic_policy.json"))
	}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return p, err
		}
		if err := json.Unmarshal(stripBOM(raw), &p); err != nil {
			return p, fmt.Errorf("read scenic policy: %w", err)
		}
		break
	}
	return p, validateCityLibraryPolicy(mediaRoot, p)
}

func validateCityLibraryPolicy(root string, p ScenicPolicy) error {
	if p.Version == 2 && strings.EqualFold(filepath.Base(filepath.Clean(root)), "AI_Media_Library") {
		destination, err := medialibrary.MigrationDestination(root)
		if err != nil {
			return err
		}
		if destination != "" {
			return fmt.Errorf("素材库已迁移到 %s，请在设置 → 混剪 → 媒体素材目录 改成新库", destination)
		}
	}
	return p.validate()
}
