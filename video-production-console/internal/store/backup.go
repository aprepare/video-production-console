package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type backupManifestEntry struct {
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
}

// CreateBackup writes a consistent SQLite snapshot and data-root manifest.
// The destination directory must not contain the data root.
func CreateBackup(db *sql.DB, dataRoot, backupDir string, now time.Time) error {
	if db == nil {
		return fmt.Errorf("database is required")
	}
	dataRoot = canonicalPath(dataRoot)
	backupDir = canonicalPath(backupDir)
	if pathWithin(dataRoot, backupDir) || pathWithin(backupDir, dataRoot) {
		return fmt.Errorf("backup directory and data root must not contain each other: backup=%s data=%s", backupDir, dataRoot)
	}
	if now.IsZero() {
		now = time.Now()
	}
	return backupDatabase(db, OpenOptions{
		DataRoot:  dataRoot,
		BackupDir: backupDir,
		Now:       func() time.Time { return now },
	})
}

func backupDatabase(db *sql.DB, options OpenOptions) (returnErr error) {
	if err := os.MkdirAll(options.BackupDir, 0o755); err != nil {
		return fmt.Errorf("create migration backup directory: %w", err)
	}
	timestamp := options.Now().Format("20060102-150405.000000000")
	databaseBackup := filepath.Join(options.BackupDir, "console-"+timestamp+".db")
	manifestPath := filepath.Join(options.BackupDir, "data-manifest-"+timestamp+".json")
	reservationPath := filepath.Join(options.BackupDir, ".backup-"+timestamp+".reserve")
	if err := ensureBackupTargetsAvailable(databaseBackup, manifestPath); err != nil {
		return err
	}
	reservation, err := os.OpenFile(reservationPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("migration backup already exists or is in progress: %s", reservationPath)
		}
		return fmt.Errorf("reserve migration backup targets: %w", err)
	}
	if err := reservation.Close(); err != nil {
		return cleanupBackupArtifacts(fmt.Errorf("close migration backup reservation: %w", err), reservationPath)
	}
	databasePublished := false
	manifestPublished := false
	backupComplete := false
	var databaseStagingDir, databaseStagingPath, manifestStagingPath string
	defer func() {
		if !backupComplete && manifestPublished {
			if err := removeOwnedPublishedFile(manifestStagingPath, manifestPath); err != nil {
				returnErr = errors.Join(returnErr, fmt.Errorf("remove incomplete migration data manifest: %w", err))
			}
		}
		if !backupComplete && databasePublished {
			if err := removeOwnedPublishedFile(databaseStagingPath, databaseBackup); err != nil {
				returnErr = errors.Join(returnErr, fmt.Errorf("remove incomplete sqlite migration backup: %w", err))
			}
		}
		for _, path := range []string{manifestStagingPath, databaseStagingPath, databaseStagingDir, reservationPath} {
			if path == "" {
				continue
			}
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				returnErr = errors.Join(returnErr, fmt.Errorf("remove migration backup staging path %s: %w", path, err))
			}
		}
	}()
	if err := ensureBackupTargetsAvailable(databaseBackup, manifestPath); err != nil {
		return err
	}
	databaseStagingDir, err = os.MkdirTemp(options.BackupDir, ".console-*.staging")
	if err != nil {
		return fmt.Errorf("create sqlite migration backup staging directory: %w", err)
	}
	if err := os.Chmod(databaseStagingDir, 0o700); err != nil {
		return fmt.Errorf("secure sqlite migration backup staging directory: %w", err)
	}
	databaseStagingPath = filepath.Join(databaseStagingDir, "console.db")
	quotedStaging := strings.ReplaceAll(databaseStagingPath, "'", "''")
	if _, err := db.Exec(`VACUUM INTO '` + quotedStaging + `'`); err != nil {
		return fmt.Errorf("create sqlite migration backup staging file: %w", err)
	}
	if options.beforeDatabaseBackupPublish != nil {
		if err := options.beforeDatabaseBackupPublish(databaseBackup); err != nil {
			return fmt.Errorf("run sqlite migration backup publish hook: %w", err)
		}
	}
	if err := os.Link(databaseStagingPath, databaseBackup); err != nil {
		return fmt.Errorf("publish sqlite migration backup: %w", err)
	}
	databasePublished = true

	entries, err := buildDataManifest(options.DataRoot, options.BackupDir)
	if err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("encode migration data manifest: %w", err)
	}
	temporary, err := os.CreateTemp(options.BackupDir, ".data-manifest-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary migration data manifest: %w", err)
	}
	manifestStagingPath = temporary.Name()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("secure temporary migration data manifest: %w", err)
	}
	if _, err := temporary.Write(append(encoded, '\n')); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write temporary migration data manifest: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync temporary migration data manifest: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary migration data manifest: %w", err)
	}
	if err := os.Link(manifestStagingPath, manifestPath); err != nil {
		return fmt.Errorf("publish migration data manifest: %w", err)
	}
	manifestPublished = true
	backupComplete = true
	return nil
}

func removeOwnedPublishedFile(stagingPath, publishedPath string) error {
	stagingInfo, err := os.Stat(stagingPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("inspect owned staging file: %w", err)
	}
	publishedInfo, err := os.Lstat(publishedPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("inspect published file: %w", err)
	}
	if !os.SameFile(stagingInfo, publishedInfo) {
		return nil
	}
	if err := os.Remove(publishedPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func ensureBackupTargetsAvailable(paths ...string) error {
	for _, path := range paths {
		if _, err := os.Lstat(path); err == nil {
			return fmt.Errorf("migration backup already exists: %s", path)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("inspect migration backup target %s: %w", path, err)
		}
	}
	return nil
}

func cleanupBackupArtifacts(cause error, paths ...string) error {
	cleanupErrors := make([]string, 0)
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			cleanupErrors = append(cleanupErrors, fmt.Sprintf("remove %s: %v", path, err))
		}
	}
	if len(cleanupErrors) != 0 {
		return fmt.Errorf("%w; backup cleanup failed: %s", cause, strings.Join(cleanupErrors, "; "))
	}
	return cause
}

func buildDataManifest(dataRoot, backupDir string) ([]backupManifestEntry, error) {
	entries := make([]backupManifestEntry, 0)
	err := filepath.WalkDir(dataRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if pathWithin(path, backupDir) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		relative, err := filepath.Rel(dataRoot, path)
		if err != nil {
			return err
		}
		entries = append(entries, backupManifestEntry{
			Path:    filepath.ToSlash(relative),
			Size:    info.Size(),
			ModTime: info.ModTime(),
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan migration data manifest: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, nil
}

func pathWithin(path, root string) bool {
	path = normalizedPathKey(path)
	root = normalizedPathKey(root)
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}
