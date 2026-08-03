package store

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type OpenOptions struct {
	Path      string
	DataRoot  string
	BackupDir string
	Now       func() time.Time

	beforeDatabaseBackupPublish func(string) error
}

type migrationPathLock struct {
	mu   sync.Mutex
	refs int
}

var migrationPathLocks = struct {
	sync.Mutex
	entries map[string]*migrationPathLock
}{entries: make(map[string]*migrationPathLock)}

func Open(path string) (*sql.DB, error) {
	return OpenWithOptions(OpenOptions{
		Path:      path,
		DataRoot:  filepath.Dir(path),
		BackupDir: filepath.Join(filepath.Dir(path), "backups"),
		Now:       time.Now,
	})
}

func OpenWithOptions(options OpenOptions) (*sql.DB, error) {
	if options.Path == "" {
		return nil, fmt.Errorf("database path is required")
	}
	absPath, err := filepath.Abs(options.Path)
	if err != nil {
		return nil, fmt.Errorf("resolve database path: %w", err)
	}
	dataRoot := options.DataRoot
	if dataRoot == "" {
		dataRoot = filepath.Dir(absPath)
	}
	dataRoot, err = filepath.Abs(dataRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve data root: %w", err)
	}
	backupDir := options.BackupDir
	if backupDir == "" {
		backupDir = filepath.Join(filepath.Dir(absPath), "backups")
	}
	backupDir, err = filepath.Abs(backupDir)
	if err != nil {
		return nil, fmt.Errorf("resolve backup directory: %w", err)
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	absPath = canonicalPath(absPath)
	dataRoot = canonicalPath(dataRoot)
	backupDir = canonicalPath(backupDir)
	options.Path = absPath
	options.DataRoot = dataRoot
	options.BackupDir = backupDir
	if pathWithin(dataRoot, backupDir) {
		return nil, fmt.Errorf("backup directory must not contain data root: backup=%s data=%s", backupDir, dataRoot)
	}

	unlock := lockMigrationPath(normalizedPathKey(absPath))
	defer unlock()

	preExisting := false
	info, statErr := os.Stat(absPath)
	if statErr == nil {
		preExisting = info.Mode().IsRegular() && info.Size() > 0
	} else if !os.IsNotExist(statErr) {
		return nil, fmt.Errorf("inspect database path: %w", statErr)
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}

	query := url.Values{}
	query.Add("_pragma", "foreign_keys(ON)")
	query.Add("_pragma", "busy_timeout(5000)")
	dsnPath := filepath.ToSlash(absPath)
	if !strings.HasPrefix(dsnPath, "/") {
		dsnPath = "/" + dsnPath
	}
	dsn := (&url.URL{
		Scheme:   "file",
		Path:     dsnPath,
		RawQuery: query.Encode(),
	}).String()

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect sqlite database: %w", err)
	}
	version, err := validatedSchemaVersion(db)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if preExisting && version < len(migrations) {
		if err := backupDatabase(db, options); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func validatedSchemaVersion(db *sql.DB) (int, error) {
	var exists int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'`).Scan(&exists); err != nil {
		return 0, fmt.Errorf("inspect schema migrations table: %w", err)
	}
	if exists == 0 {
		return 0, nil
	}
	rows, err := db.Query(`SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		return 0, fmt.Errorf("read schema migration history: %w", err)
	}
	defer rows.Close()

	version := 0
	for rows.Next() {
		var applied int
		if err := rows.Scan(&applied); err != nil {
			return 0, fmt.Errorf("read schema migration version: %w", err)
		}
		expected := version + 1
		if applied != expected || applied > len(migrations) {
			return 0, fmt.Errorf("invalid schema migration history: got version %d, want contiguous version %d through at most %d", applied, expected, len(migrations))
		}
		version = applied
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate schema migration history: %w", err)
	}
	return version, nil
}

func lockMigrationPath(path string) func() {
	migrationPathLocks.Lock()
	entry := migrationPathLocks.entries[path]
	if entry == nil {
		entry = &migrationPathLock{}
		migrationPathLocks.entries[path] = entry
	}
	entry.refs++
	migrationPathLocks.Unlock()

	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		migrationPathLocks.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(migrationPathLocks.entries, path)
		}
		migrationPathLocks.Unlock()
	}
}

func canonicalDatabasePath(path string) string {
	return normalizedPathKey(canonicalPath(path))
}

func normalizedPathKey(path string) string {
	path = filepath.Clean(path)
	if filepath.Separator == '\\' {
		path = strings.ToLower(path)
	}
	return path
}

func canonicalPath(path string) string {
	return canonicalPathWithResolver(path, filepath.EvalSymlinks)
}

func canonicalPathWithResolver(path string, resolve func(string) (string, error)) string {
	path = filepath.Clean(path)
	remaining := make([]string, 0)
	for {
		resolved, err := resolve(path)
		if err == nil {
			for i := len(remaining) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, remaining[i])
			}
			path = resolved
			break
		}
		parent := filepath.Dir(path)
		if parent == path {
			break
		}
		remaining = append(remaining, filepath.Base(path))
		path = parent
	}
	return filepath.Clean(path)
}
