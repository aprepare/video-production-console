package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOpenBacksUpBeforeV2Migration(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "console.db")
	backupDir := filepath.Join(root, "migration-backups")
	createV2Predecessor(t, dbPath)
	if err := os.WriteFile(filepath.Join(root, "media.txt"), []byte("media"), 0o600); err != nil {
		t.Fatalf("write manifest fixture: %v", err)
	}
	mediaModTime := time.Date(2026, time.August, 1, 2, 3, 4, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(root, "media.txt"), mediaModTime, mediaModTime); err != nil {
		t.Fatalf("set manifest fixture mod time: %v", err)
	}

	opts := OpenOptions{
		Path:      dbPath,
		DataRoot:  root,
		BackupDir: backupDir,
		Now: func() time.Time {
			return time.Date(2026, time.August, 3, 4, 5, 6, 0, time.UTC)
		},
	}
	db, err := OpenWithOptions(opts)
	if err != nil {
		t.Fatalf("OpenWithOptions() error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close migrated database: %v", err)
	}

	dbBackups := mustGlob(t, filepath.Join(backupDir, "console-*.db"))
	if len(dbBackups) != 1 {
		t.Fatalf("sqlite backups=%v, want one", dbBackups)
	}
	manifests := mustGlob(t, filepath.Join(backupDir, "data-manifest-*.json"))
	if len(manifests) != 1 {
		t.Fatalf("data manifests=%v, want one", manifests)
	}
	assertBackupIsPreMigration(t, dbBackups[0])

	raw, err := os.ReadFile(manifests[0])
	if err != nil {
		t.Fatalf("read data manifest: %v", err)
	}
	var entries []struct {
		Path    string    `json:"path"`
		Size    int64     `json:"size"`
		ModTime time.Time `json:"mod_time"`
	}
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("decode data manifest: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("manifest entries=%v, want exactly console.db and media.txt", entries)
	}
	if entries[0].Path != "console.db" || entries[1].Path != "media.txt" {
		t.Fatalf("manifest order=%v, want [console.db media.txt]", []string{entries[0].Path, entries[1].Path})
	}
	paths := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if filepath.IsAbs(entry.Path) || strings.Contains(entry.Path, `\`) {
			t.Errorf("manifest path %q is not portable and relative", entry.Path)
		}
		if strings.HasPrefix(entry.Path, "migration-backups/") {
			t.Errorf("manifest contains backup path %q", entry.Path)
		}
		if entry.Size <= 0 || entry.ModTime.IsZero() {
			t.Errorf("manifest entry has invalid metadata: %#v", entry)
		}
		paths[entry.Path] = true
	}
	if !paths["console.db"] || !paths["media.txt"] {
		t.Fatalf("manifest paths=%v, want console.db and media.txt", paths)
	}
	if entries[1].Size != int64(len("media")) || !entries[1].ModTime.Equal(mediaModTime) {
		t.Fatalf("media manifest entry=%#v, want size=5 mod_time=%s", entries[1], mediaModTime)
	}

	reopened, err := OpenWithOptions(opts)
	if err != nil {
		t.Fatalf("second OpenWithOptions() error = %v", err)
	}
	defer reopened.Close()
	if got := tableCount(t, reopened, "asset_versions"); got != 1 {
		t.Fatalf("asset_versions=%d", got)
	}
	if got := tableCount(t, reopened, "assets"); got != 1 {
		t.Fatalf("legacy assets=%d", got)
	}
	if got := len(mustGlob(t, filepath.Join(backupDir, "console-*.db"))); got != 1 {
		t.Fatalf("sqlite backups after reopen=%d, want one", got)
	}
	if got := len(mustGlob(t, filepath.Join(backupDir, "data-manifest-*.json"))); got != 1 {
		t.Fatalf("data manifests after reopen=%d, want one", got)
	}
}

func TestOpenBacksUpAndUpgradesCurrentV3Database(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "console.db")
	legacy := createV2PredecessorSchema(t, dbPath)
	if _, err := legacy.Exec(migrations[2]); err != nil {
		t.Fatalf("apply migration 3 fixture: %v", err)
	}
	if _, err := legacy.Exec(`INSERT INTO schema_migrations(version) VALUES(3)`); err != nil {
		t.Fatalf("record migration 3 fixture: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close migration 3 fixture: %v", err)
	}

	backupDir := filepath.Join(root, "backups")
	db, err := OpenWithOptions(OpenOptions{Path: dbPath, DataRoot: root, BackupDir: backupDir, Now: time.Now})
	if err != nil {
		t.Fatalf("OpenWithOptions() error = %v", err)
	}
	defer db.Close()
	if got := len(mustGlob(t, filepath.Join(backupDir, "console-*.db"))); got != 1 {
		t.Fatalf("v3 upgrade backups=%d, want one", got)
	}
	if got := scalar(t, db, `SELECT MAX(version) FROM schema_migrations`); got != "6" {
		t.Fatalf("schema version=%s, want 6", got)
	}
	var triggers int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name LIKE 'asset_%'`).Scan(&triggers); err != nil {
		t.Fatalf("count asset integrity triggers: %v", err)
	}
	if triggers != 5 {
		t.Fatalf("asset integrity triggers=%d, want 5", triggers)
	}
}

func TestOpenCleansIncompleteBackupBeforeSameTimestampRetry(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "console.db")
	backupDir := filepath.Join(root, "backups")
	createV2Predecessor(t, dbPath)
	fixedNow := time.Date(2026, time.August, 3, 4, 5, 6, 0, time.UTC)
	manifestPath := filepath.Join(backupDir, "data-manifest-20260803-040506.000000000.json")
	if err := os.MkdirAll(manifestPath, 0o755); err != nil {
		t.Fatalf("create manifest rename blocker: %v", err)
	}
	opts := OpenOptions{Path: dbPath, DataRoot: root, BackupDir: backupDir, Now: func() time.Time { return fixedNow }}

	if db, err := OpenWithOptions(opts); err == nil {
		_ = db.Close()
		t.Fatal("OpenWithOptions() succeeded with blocked manifest destination")
	}
	if got := mustGlob(t, filepath.Join(backupDir, "console-*.db")); len(got) != 0 {
		t.Fatalf("failed backup left sqlite snapshots=%v", got)
	}
	if got := mustGlob(t, filepath.Join(backupDir, ".data-manifest-*.tmp")); len(got) != 0 {
		t.Fatalf("failed backup left manifest temp files=%v", got)
	}
	if err := os.Remove(manifestPath); err != nil {
		t.Fatalf("remove manifest rename blocker: %v", err)
	}

	db, err := OpenWithOptions(opts)
	if err != nil {
		t.Fatalf("same-timestamp retry error = %v", err)
	}
	defer db.Close()
	if got := len(mustGlob(t, filepath.Join(backupDir, "console-*.db"))); got != 1 {
		t.Fatalf("sqlite backups after retry=%d, want one", got)
	}
	if got := len(mustGlob(t, filepath.Join(backupDir, "data-manifest-*.json"))); got != 1 {
		t.Fatalf("data manifests after retry=%d, want one", got)
	}
}

func TestOpenDoesNotClobberExistingManifest(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "console.db")
	backupDir := filepath.Join(root, "backups")
	createV2Predecessor(t, dbPath)
	fixedNow := time.Date(2026, time.August, 3, 4, 5, 6, 0, time.UTC)
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		t.Fatalf("create backup directory: %v", err)
	}
	manifestPath := filepath.Join(backupDir, "data-manifest-20260803-040506.000000000.json")
	wantBlocker := []byte("existing manifest must survive")
	if err := os.WriteFile(manifestPath, wantBlocker, 0o600); err != nil {
		t.Fatalf("write manifest blocker: %v", err)
	}

	db, err := OpenWithOptions(OpenOptions{
		Path: dbPath, DataRoot: root, BackupDir: backupDir,
		Now: func() time.Time { return fixedNow },
	})
	if db != nil {
		_ = db.Close()
		t.Fatal("OpenWithOptions() returned a database with an existing manifest target")
	}
	if err == nil || !strings.Contains(err.Error(), "backup already exists") {
		t.Fatalf("OpenWithOptions() error = %v, want backup collision error", err)
	}
	if got, readErr := os.ReadFile(manifestPath); readErr != nil {
		t.Fatalf("read manifest blocker: %v", readErr)
	} else if string(got) != string(wantBlocker) {
		t.Fatalf("manifest blocker = %q, want %q", got, wantBlocker)
	}
	if got := mustGlob(t, filepath.Join(backupDir, "console-*.db")); len(got) != 0 {
		t.Fatalf("manifest collision left sqlite snapshots=%v", got)
	}
	if got := mustGlob(t, filepath.Join(backupDir, ".data-manifest-*.tmp")); len(got) != 0 {
		t.Fatalf("manifest collision left temporary manifests=%v", got)
	}
	if got := mustGlob(t, filepath.Join(backupDir, ".backup-*.reserve")); len(got) != 0 {
		t.Fatalf("manifest collision left reservations=%v", got)
	}
}

func TestOpenDoesNotDeleteDatabaseBackupThatWinsPublishRace(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "console.db")
	backupDir := filepath.Join(root, "backups")
	createV2Predecessor(t, dbPath)
	fixedNow := time.Date(2026, time.August, 3, 4, 5, 6, 0, time.UTC)
	finalPath := filepath.Join(backupDir, "console-20260803-040506.000000000.db")
	wantBlocker := []byte("external winner must survive")
	publishHook := func(path string) error {
		if !strings.EqualFold(path, finalPath) {
			return fmt.Errorf("publish hook path=%q, want %q", path, finalPath)
		}
		return os.WriteFile(path, wantBlocker, 0o600)
	}

	db, err := OpenWithOptions(OpenOptions{
		Path: dbPath, DataRoot: root, BackupDir: backupDir,
		Now:                         func() time.Time { return fixedNow },
		beforeDatabaseBackupPublish: publishHook,
	})
	if db != nil {
		_ = db.Close()
		t.Fatal("OpenWithOptions() returned a database after losing the backup publish race")
	}
	if err == nil {
		t.Fatal("OpenWithOptions() succeeded after losing the database publish race")
	}
	if got, readErr := os.ReadFile(finalPath); readErr != nil {
		t.Fatalf("read external database blocker: %v", readErr)
	} else if string(got) != string(wantBlocker) {
		t.Fatalf("database blocker=%q, want %q", got, wantBlocker)
	}
	if !strings.Contains(err.Error(), "publish sqlite migration backup") {
		t.Fatalf("OpenWithOptions() error = %v, want database publish collision", err)
	}
	if got := mustGlob(t, filepath.Join(backupDir, ".console-*.staging")); len(got) != 0 {
		t.Fatalf("publish collision left database staging files=%v", got)
	}
	if got := mustGlob(t, filepath.Join(backupDir, ".data-manifest-*.tmp")); len(got) != 0 {
		t.Fatalf("publish collision left manifest staging files=%v", got)
	}
	if got := mustGlob(t, filepath.Join(backupDir, ".backup-*.reserve")); len(got) != 0 {
		t.Fatalf("publish collision left reservations=%v", got)
	}
}

func TestConcurrentSameTimestampBackupsDoNotClobber(t *testing.T) {
	root := t.TempDir()
	backupDir := filepath.Join(root, "shared-backups")
	fixedNow := time.Date(2026, time.August, 3, 4, 5, 6, 0, time.UTC)
	options := make([]OpenOptions, 2)
	for i := range options {
		dataRoot := filepath.Join(root, fmt.Sprintf("data-%d", i))
		dbPath := filepath.Join(dataRoot, "console.db")
		createV2Predecessor(t, dbPath)
		options[i] = OpenOptions{
			Path: dbPath, DataRoot: dataRoot, BackupDir: backupDir,
			Now: func() time.Time { return fixedNow },
		}
	}

	start := make(chan struct{})
	errs := make(chan error, len(options))
	var wg sync.WaitGroup
	for _, option := range options {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			db, err := OpenWithOptions(option)
			if db != nil {
				err = errors.Join(err, db.Close())
			}
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	successes := 0
	for err := range errs {
		if err == nil {
			successes++
		} else if !strings.Contains(err.Error(), "backup already exists or is in progress") &&
			!strings.Contains(err.Error(), "backup already exists") {
			t.Errorf("concurrent OpenWithOptions() unexpected error: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent successful backups=%d, want exactly one", successes)
	}
	databaseBackups := mustGlob(t, filepath.Join(backupDir, "console-*.db"))
	if len(databaseBackups) != 1 {
		t.Fatalf("concurrent sqlite backups=%v, want one", databaseBackups)
	}
	assertBackupIsPreMigration(t, databaseBackups[0])
	if got := mustGlob(t, filepath.Join(backupDir, "data-manifest-*.json")); len(got) != 1 {
		t.Fatalf("concurrent manifests=%v, want one", got)
	}
	if got := mustGlob(t, filepath.Join(backupDir, ".console-*.staging")); len(got) != 0 {
		t.Fatalf("concurrent backup left database staging=%v", got)
	}
	if got := mustGlob(t, filepath.Join(backupDir, ".data-manifest-*.tmp")); len(got) != 0 {
		t.Fatalf("concurrent backup left manifest staging=%v", got)
	}
	if got := mustGlob(t, filepath.Join(backupDir, ".backup-*.reserve")); len(got) != 0 {
		t.Fatalf("concurrent backup left reservations=%v", got)
	}
}

func TestOpenAlwaysNamesSQLiteBackupConsole(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "renamed-source.db")
	backupDir := filepath.Join(root, "backups")
	createV2Predecessor(t, dbPath)
	db, err := OpenWithOptions(OpenOptions{Path: dbPath, DataRoot: root, BackupDir: backupDir, Now: time.Now})
	if err != nil {
		t.Fatalf("OpenWithOptions() error = %v", err)
	}
	defer db.Close()
	if got := len(mustGlob(t, filepath.Join(backupDir, "console-*.db"))); got != 1 {
		t.Fatalf("console backups=%d, want one", got)
	}
	if got := mustGlob(t, filepath.Join(backupDir, "renamed-source-*.db")); len(got) != 0 {
		t.Fatalf("source-basename backups=%v, want none", got)
	}
}

func TestOpenDoesNotBackUpBrandNewDatabase(t *testing.T) {
	root := t.TempDir()
	backupDir := filepath.Join(root, "backups")
	db, err := OpenWithOptions(OpenOptions{
		Path:      filepath.Join(root, "console.db"),
		DataRoot:  root,
		BackupDir: backupDir,
		Now:       time.Now,
	})
	if err != nil {
		t.Fatalf("OpenWithOptions() error = %v", err)
	}
	defer db.Close()
	if got := mustGlob(t, filepath.Join(backupDir, "*")); len(got) != 0 {
		t.Fatalf("new database backups=%v, want none", got)
	}
}

func TestOpenRejectsBackupDirectoryContainingDataRoot(t *testing.T) {
	root := t.TempDir()
	for _, tt := range []struct {
		name, dataRoot, backupDir string
	}{
		{name: "equal", dataRoot: filepath.Join(root, "data"), backupDir: filepath.Join(root, "data")},
		{name: "ancestor", dataRoot: filepath.Join(root, "data", "nested"), backupDir: filepath.Join(root, "data")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			db, err := OpenWithOptions(OpenOptions{
				Path:      filepath.Join(tt.dataRoot, "console.db"),
				DataRoot:  tt.dataRoot,
				BackupDir: tt.backupDir,
				Now:       time.Now,
			})
			if db != nil {
				_ = db.Close()
				t.Fatal("OpenWithOptions() returned a database for unsafe backup layout")
			}
			if err == nil || !strings.Contains(err.Error(), "backup directory must not contain data root") {
				t.Fatalf("OpenWithOptions() error = %v, want unsafe backup layout error", err)
			}
		})
	}
}

func TestOpenRejectsBackupDirectoryAliasingDataRoot(t *testing.T) {
	root := t.TempDir()
	dataRoot := filepath.Join(root, "data")
	if err := os.MkdirAll(dataRoot, 0o755); err != nil {
		t.Fatalf("create data root: %v", err)
	}
	alias := filepath.Join(root, "data-alias")
	if err := os.Symlink(dataRoot, alias); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	db, err := OpenWithOptions(OpenOptions{
		Path:      filepath.Join(dataRoot, "console.db"),
		DataRoot:  dataRoot,
		BackupDir: alias,
		Now:       time.Now,
	})
	if db != nil {
		_ = db.Close()
		t.Fatal("OpenWithOptions() returned a database for aliased unsafe backup layout")
	}
	if err == nil || !strings.Contains(err.Error(), "backup directory must not contain data root") {
		t.Fatalf("OpenWithOptions() error = %v, want unsafe backup layout error", err)
	}
}

func TestOpenWithAliasedDataRootBuildsCompleteManifest(t *testing.T) {
	root := t.TempDir()
	realDataRoot := filepath.Join(root, "data")
	if err := os.MkdirAll(realDataRoot, 0o755); err != nil {
		t.Fatalf("create data root: %v", err)
	}
	dataRootAlias := filepath.Join(root, "data-alias")
	if err := os.Symlink(realDataRoot, dataRootAlias); err != nil {
		t.Skipf("directory symlink integration requires symlink permission: %v", err)
	}
	dbPath := filepath.Join(realDataRoot, "console.db")
	createV2Predecessor(t, dbPath)
	if err := os.WriteFile(filepath.Join(realDataRoot, "media.txt"), []byte("media"), 0o600); err != nil {
		t.Fatalf("write media fixture: %v", err)
	}
	backupDir := filepath.Join(root, "backups")
	db, err := OpenWithOptions(OpenOptions{Path: dbPath, DataRoot: dataRootAlias, BackupDir: backupDir, Now: time.Now})
	if err != nil {
		t.Fatalf("OpenWithOptions() error = %v", err)
	}
	defer db.Close()
	entries := readManifestEntries(t, mustGlob(t, filepath.Join(backupDir, "data-manifest-*.json"))[0])
	if got := manifestPaths(entries); !reflect.DeepEqual(got, []string{"console.db", "media.txt"}) {
		t.Fatalf("manifest paths=%v, want complete aliased-root manifest", got)
	}
}

func TestOpenExcludesBackupDirectoryAliasingDescendant(t *testing.T) {
	root := t.TempDir()
	dataRoot := filepath.Join(root, "data")
	realBackupDir := filepath.Join(dataRoot, "migration-backups")
	if err := os.MkdirAll(realBackupDir, 0o755); err != nil {
		t.Fatalf("create backup directory: %v", err)
	}
	backupAlias := filepath.Join(root, "backup-alias")
	if err := os.Symlink(realBackupDir, backupAlias); err != nil {
		t.Skipf("directory symlink integration requires symlink permission: %v", err)
	}
	dbPath := filepath.Join(dataRoot, "console.db")
	createV2Predecessor(t, dbPath)
	if err := os.WriteFile(filepath.Join(dataRoot, "media.txt"), []byte("media"), 0o600); err != nil {
		t.Fatalf("write media fixture: %v", err)
	}
	db, err := OpenWithOptions(OpenOptions{Path: dbPath, DataRoot: dataRoot, BackupDir: backupAlias, Now: time.Now})
	if err != nil {
		t.Fatalf("OpenWithOptions() error = %v", err)
	}
	defer db.Close()
	entries := readManifestEntries(t, mustGlob(t, filepath.Join(realBackupDir, "data-manifest-*.json"))[0])
	if got := manifestPaths(entries); !reflect.DeepEqual(got, []string{"console.db", "media.txt"}) {
		t.Fatalf("manifest paths=%v, want aliased backup descendant excluded", got)
	}
}

func TestCanonicalPathResolvesExistingAncestorWithoutSymlinkPrivilege(t *testing.T) {
	root := t.TempDir()
	realRoot := filepath.Join(root, "real-data")
	aliasRoot := filepath.Join(root, "data-alias")
	want := filepath.Join(realRoot, "future", "console.db")
	resolver := func(path string) (string, error) {
		if filepath.Clean(path) == aliasRoot {
			return realRoot, nil
		}
		return "", os.ErrNotExist
	}
	got := canonicalPathWithResolver(filepath.Join(aliasRoot, "future", "console.db"), resolver)
	if got != canonicalPathWithResolver(want, func(path string) (string, error) { return path, nil }) {
		t.Fatalf("canonical path=%q, want resolved existing ancestor path %q", got, want)
	}
}

func TestOpenAllowsBackupDirectoryWithSiblingPrefix(t *testing.T) {
	root := t.TempDir()
	dataRoot := filepath.Join(root, "data")
	db, err := OpenWithOptions(OpenOptions{
		Path:      filepath.Join(dataRoot, "console.db"),
		DataRoot:  dataRoot,
		BackupDir: filepath.Join(root, "data-backups"),
		Now:       time.Now,
	})
	if err != nil {
		t.Fatalf("OpenWithOptions() error = %v", err)
	}
	defer db.Close()
}

func mustGlob(t *testing.T, pattern string) []string {
	t.Helper()
	matches, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("glob %q: %v", pattern, err)
	}
	return matches
}

func readManifestEntries(t *testing.T, path string) []backupManifestEntry {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read data manifest: %v", err)
	}
	var entries []backupManifestEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("decode data manifest: %v", err)
	}
	return entries
}

func manifestPaths(entries []backupManifestEntry) []string {
	paths := make([]string, len(entries))
	for i := range entries {
		paths[i] = entries[i].Path
	}
	return paths
}
