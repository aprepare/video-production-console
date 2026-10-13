package medialibrary

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MigrationDestination reads only the marker, never the destination library.
func MigrationDestination(root string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(root, "00_INDEX", "素材库已迁移.txt"))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	line, _, _ := strings.Cut(strings.TrimPrefix(string(raw), "\ufeff"), "\n")
	path, ok := strings.CutPrefix(strings.TrimSpace(line), "当前主素材库：")
	if !ok || strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("素材库已迁移，但素材库已迁移.txt 第一行未记录当前主素材库路径")
	}
	return strings.TrimSpace(path), nil
}
