package aishorts

import (
	"context"
	"sync"
)

// Shared by bulk and single-shot generation across all shorts.
// Each run snapshots its settings; active requests finish normally on edits.
type generationLimiter struct {
	mu      sync.Mutex
	active  int
	changed chan struct{}
}

func (l *generationLimiter) acquire(ctx context.Context, limit int) (func(), error) {
	for {
		l.mu.Lock()
		if l.changed == nil {
			l.changed = make(chan struct{})
		}
		if l.active < limit {
			l.active++
			l.mu.Unlock()
			return func() { l.mu.Lock(); l.active--; close(l.changed); l.changed = make(chan struct{}); l.mu.Unlock() }, nil
		}
		changed := l.changed
		l.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
	}
}
