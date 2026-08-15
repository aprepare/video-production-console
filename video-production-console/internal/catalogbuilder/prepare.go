package catalogbuilder

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const DefaultMediaRoot = `E:\data\media`

var defaultMediaRoot = DefaultMediaRoot

func toolFileName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}

func NormalizeMediaRoot(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		root = defaultMediaRoot
	}
	root = filepath.Clean(root)
	for {
		base := strings.ToLower(filepath.Base(root))
		switch base {
		case "movies", "broll", "images":
			if strings.EqualFold(filepath.Base(filepath.Dir(root)), "originals") {
				root = filepath.Dir(filepath.Dir(root))
				continue
			}
		case "originals":
			root = filepath.Dir(root)
			continue
		}
		return root
	}
}

func PrepareMediaRoot(root string) (string, error) {
	root = NormalizeMediaRoot(root)
	if root == "" || !filepath.IsAbs(root) {
		return "", fmt.Errorf("%w: media_root must be an absolute path", errInvalidConfig)
	}
	layout := []string{
		root,
		filepath.Join(root, "originals"),
		filepath.Join(root, "originals", "movies"),
		filepath.Join(root, "originals", "broll"),
		filepath.Join(root, "originals", "images"),
	}
	for _, dir := range layout {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("%w: create %s: %v", errInvalidConfig, dir, err)
		}
	}
	return root, nil
}

func ResolveToolPath(given, toolName string, searchDirs ...string) (string, error) {
	given = strings.TrimSpace(given)
	var candidates []string
	if given != "" {
		candidates = append(candidates, given)
		if info, err := os.Stat(given); err == nil && info.IsDir() {
			candidates = append(candidates, filepath.Join(given, toolName))
		}
	}
	for _, dir := range searchDirs {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		candidates = append(candidates, filepath.Join(dir, toolName))
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && info.Mode().IsRegular() {
			abs, absErr := filepath.Abs(candidate)
			if absErr != nil {
				return candidate, nil
			}
			return abs, nil
		}
	}
	if given == "" {
		return "", fmt.Errorf("%w: %s not found next to catalog-builder", errFFmpegRequired, toolName)
	}
	return "", fmt.Errorf("%w: %s not found at %s", errFFmpegRequired, toolName, given)
}

func (c Config) Prepare(searchDirs ...string) (Config, error) {
	root, err := PrepareMediaRoot(c.MediaRoot)
	if err != nil {
		return c, err
	}
	c.MediaRoot = root
	if resolved, err := ResolveToolPath(c.FFmpegPath, toolFileName("ffmpeg"), searchDirs...); err == nil {
		c.FFmpegPath = resolved
	}
	if resolved, err := ResolveToolPath(c.FFprobePath, toolFileName("ffprobe"), searchDirs...); err == nil {
		c.FFprobePath = resolved
	}
	return c, nil
}

func movieDir(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	return filepath.Join(root, "originals", "movies")
}
