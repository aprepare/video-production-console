package montageplan

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

var packIndexCandidates = []string{
	filepath.Join("00_INDEX", "media_index.json"),
	"media_index.json",
	filepath.Join("originals", "media_index.json"),
}

var imageIndexExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".gif": true,
}

var videoIndexExts = map[string]bool{
	".mp4": true, ".mov": true, ".mkv": true, ".webm": true, ".m4v": true, ".avi": true,
}

// WriteScenicMediaIndex writes the scenic planner index used by montage
// preflight. A pack-shipped index is reused when it already lists usable
// files; otherwise originals/{broll,movies,images} are scanned.
func WriteScenicMediaIndex(mediaRoot, destPath, ffprobePath string) error {
	mediaRoot = strings.TrimSpace(mediaRoot)
	destPath = strings.TrimSpace(destPath)
	if mediaRoot == "" || destPath == "" {
		return fmt.Errorf("media_root and media_index_path are required")
	}
	items, err := collectScenicIndex(mediaRoot, ffprobePath)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return fmt.Errorf("media index produced no usable clips")
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(items)
	if err != nil {
		return err
	}
	temp := destPath + ".tmp"
	if err := os.WriteFile(temp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(temp, destPath); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}

func collectScenicIndex(mediaRoot, ffprobePath string) ([]mediaItem, error) {
	for _, rel := range packIndexCandidates {
		path := filepath.Join(mediaRoot, rel)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		items, err := usableItemsFromIndex(path, mediaRoot)
		if err != nil {
			return nil, err
		}
		if len(items) > 0 {
			return items, nil
		}
	}
	return scanPackMediaForIndex(mediaRoot, ffprobePath)
}

// PruneScenicMediaIndex drops city/finance/other non-landscape rows from an
// already written planner index so an older partner setup does not keep
// feeding those clips into the next montage.
func PruneScenicMediaIndex(indexPath string) error {
	indexPath = strings.TrimSpace(indexPath)
	if indexPath == "" {
		return nil
	}
	raw, err := os.ReadFile(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var items []mediaItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return err
	}
	kept := make([]mediaItem, 0, len(items))
	for _, item := range items {
		if err := normalizeMediaItem(&item); err != nil {
			continue
		}
		if isLandscapeItem(item) {
			kept = append(kept, item)
		}
	}
	if len(kept) == len(items) {
		return nil
	}
	if len(kept) == 0 {
		return fmt.Errorf("media index produced no usable clips")
	}
	out, err := json.Marshal(kept)
	if err != nil {
		return err
	}
	temp := indexPath + ".tmp"
	if err := os.WriteFile(temp, append(out, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(temp, indexPath); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}

func usableItemsFromIndex(indexPath, mediaRoot string) ([]mediaItem, error) {
	scan, err := scanMediaIndex(indexPath, mediaRoot, false)
	if err != nil {
		return nil, err
	}
	if scan.err != nil {
		return nil, scan.err
	}
	out := make([]mediaItem, 0, len(scan.rows))
	for _, row := range scan.rows {
		if !row.usable || !isLandscapeItem(row.item) {
			continue
		}
		item := row.item
		item.AbsPath = ""
		out = append(out, item)
	}
	return out, nil
}

func scanPackMediaForIndex(mediaRoot, ffprobePath string) ([]mediaItem, error) {
	info, err := os.Stat(mediaRoot)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("media root is invalid")
	}
	skipDirs := map[string]bool{
		"derived": true, "00_index": true, ".git": true,
	}
	var items []mediaItem
	walkErr := filepath.WalkDir(mediaRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != mediaRoot && skipDirs[strings.ToLower(entry.Name())] {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		item, ok, itemErr := scenicItemFromFile(mediaRoot, path, ffprobePath)
		if itemErr != nil {
			return itemErr
		}
		if ok {
			items = append(items, item)
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return items, nil
}

func scenicItemFromFile(mediaRoot, absPath, ffprobePath string) (mediaItem, bool, error) {
	rel, err := filepath.Rel(mediaRoot, absPath)
	if err != nil {
		return mediaItem{}, false, err
	}
	rel = filepath.ToSlash(rel)
	if strings.HasPrefix(rel, "../") {
		return mediaItem{}, false, nil
	}
	ext := strings.ToLower(filepath.Ext(absPath))
	item := mediaItem{
		ID:           scenicIndexID(rel),
		Category:     scenicCategoryFromPath(rel),
		RelativePath: rel,
	}
	if !isLandscapeItem(item) {
		return mediaItem{}, false, nil
	}
	switch {
	case imageIndexExts[ext]:
		item.Kind = mediaKindImage
		item.DurationSeconds = 0
		return item, true, nil
	case videoIndexExts[ext]:
		seconds, err := probeDurationWith(ffprobePath, absPath)
		if err != nil || seconds < minVideoSourceSeconds {
			return mediaItem{}, false, nil
		}
		if strings.Contains(strings.ToLower(rel), "/movies/") {
			item.Kind = mediaKindMovie
		} else {
			item.Kind = mediaKindBroll
		}
		item.DurationSeconds = seconds
		return item, true, nil
	default:
		return mediaItem{}, false, nil
	}
}

func scenicIndexID(relative string) string {
	id := strings.TrimSuffix(filepath.ToSlash(relative), filepath.Ext(relative))
	id = strings.ReplaceAll(id, "/", "-")
	id = strings.Trim(id, "-")
	if id == "" {
		return "clip"
	}
	return id
}

func scenicCategoryFromPath(relative string) string {
	slash := filepath.ToSlash(relative)
	lower := strings.ToLower(slash)
	if pathHasFolder(lower, "movies") || pathHasFolder(lower, "city_traffic") ||
		pathHasFolder(lower, "finance_business") || pathHasFolder(lower, "space_cosmos") {
		return filepath.Base(filepath.Dir(filepath.FromSlash(slash)))
	}
	parts := strings.Split(slash, "/")
	for i := len(parts) - 2; i >= 0; i-- {
		name := strings.TrimSpace(parts[i])
		if name != "" && name != "originals" && name != "broll" && name != "movies" && name != "images" {
			return name
		}
	}
	if pathHasFolder(lower, "originals") {
		return "Nature_Landscape"
	}
	return ""
}

func pathHasFolder(slashPath, name string) bool {
	return strings.HasPrefix(slashPath, name+"/") || strings.Contains(slashPath, "/"+name+"/")
}
