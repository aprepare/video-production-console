package codex

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const projectLockName = ".video-production-console.lock"

// ProjectDirGuard owns the canonical project directory and its command-lifetime lock.
type ProjectDirGuard struct {
	mu         sync.Mutex
	lock       *os.File
	normalized TaskContext
}

// OpenProjectDirGuard canonicalizes the project context once and acquires an
// exclusive lock file inside the canonical project directory.
func OpenProjectDirGuard(ctx TaskContext) (*ProjectDirGuard, error) {
	normalized, err := normalizeContext(ctx)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(normalized.ProjectDir)
	if err != nil {
		return nil, fmt.Errorf("inspect project directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("project directory %q is not a directory", normalized.ProjectDir)
	}
	lockPath := filepath.Join(normalized.ProjectDir, projectLockName)
	lock, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, fmt.Errorf("lock project directory: %w", err)
	}
	return &ProjectDirGuard{lock: lock, normalized: normalized}, nil
}

// CanonicalProjectDir returns the path captured when the guard was opened.
func (g *ProjectDirGuard) CanonicalProjectDir() string {
	if g == nil {
		return ""
	}
	return g.normalized.ProjectDir
}

func (g *ProjectDirGuard) open() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.lock != nil
}

// Close releases the project lock. It is safe to call more than once.
func (g *ProjectDirGuard) Close() error {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.lock == nil {
		return nil
	}
	lock := g.lock
	g.lock = nil
	closeErr := lock.Close()
	removeErr := os.Remove(lock.Name())
	if closeErr != nil {
		return closeErr
	}
	return removeErr
}
