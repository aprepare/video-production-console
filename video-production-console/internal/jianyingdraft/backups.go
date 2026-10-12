package jianyingdraft

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// BackupFile is one root_meta_info registration backup sitting in the draft root.
type BackupFile struct {
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
}

// PruneResult is what a backup prune removed and what it left in place.
type PruneResult struct {
	Deleted    []BackupFile `json:"deleted"`
	Kept       int          `json:"kept"`
	FreedBytes int64        `json:"freed_bytes"`
}

// ListRegistrationBackups returns registration backups directly inside jianyingRoot.
func ListRegistrationBackups(jianyingRoot string) ([]BackupFile, error) {
	root := strings.TrimSpace(jianyingRoot)
	if root == "" {
		return nil, fmt.Errorf("jianying root is empty")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	out := make([]BackupFile, 0)
	for _, entry := range entries {
		name := entry.Name()
		path, ok := directBackupPath(root, name)
		if !ok {
			continue
		}
		info, err := os.Lstat(path)
		if err != nil || !isRegularFile(info) {
			continue
		}
		out = append(out, BackupFile{Name: name, Size: info.Size(), ModTime: info.ModTime()})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].ModTime.Equal(out[j].ModTime) {
			return out[i].ModTime.After(out[j].ModTime)
		}
		return out[i].Name > out[j].Name
	})
	return out, nil
}

// PruneRegistrationBackups keeps the newest backups and deletes the rest when they are old enough.
func PruneRegistrationBackups(jianyingRoot string, keepLatest int, olderThan time.Duration, dryRun bool) (PruneResult, error) {
	if keepLatest < 0 {
		keepLatest = 0
	}
	files, err := ListRegistrationBackups(jianyingRoot)
	if err != nil {
		return PruneResult{}, err
	}
	root := strings.TrimSpace(jianyingRoot)
	result := PruneResult{Deleted: []BackupFile{}}
	cutoff := time.Now().Add(-olderThan)
	for i, file := range files {
		if i < keepLatest || !file.ModTime.Before(cutoff) {
			result.Kept++
			continue
		}
		path, ok := directBackupPath(root, file.Name)
		if !ok {
			result.Kept++
			continue
		}
		info, err := os.Lstat(path)
		if err != nil || !isRegularFile(info) {
			result.Kept++
			continue
		}
		if dryRun {
			result.Deleted = append(result.Deleted, file)
			result.FreedBytes += info.Size()
			continue
		}
		if err := os.Remove(path); err != nil {
			return result, err
		}
		result.Deleted = append(result.Deleted, BackupFile{Name: file.Name, Size: info.Size(), ModTime: file.ModTime})
		result.FreedBytes += info.Size()
	}
	return result, nil
}

func directBackupPath(root, name string) (string, bool) {
	if !isRegistrationBackupName(name) {
		return "", false
	}
	root = filepath.Clean(root)
	path := filepath.Join(root, name)
	if filepath.Base(path) != name || filepath.Clean(filepath.Dir(path)) != root {
		return "", false
	}
	return path, true
}

func isRegistrationBackupName(name string) bool {
	if name == "" || name != filepath.Base(name) || strings.ContainsAny(name, `/\:`) {
		return false
	}
	if !strings.HasPrefix(name, "root_meta_info.") || name == "root_meta_info.json" {
		return false
	}
	return strings.HasSuffix(name, ".backup.json") || strings.HasSuffix(name, ".bak.json")
}

func isRegularFile(info os.FileInfo) bool {
	mode := info.Mode()
	return mode.IsRegular() && mode&os.ModeSymlink == 0
}
