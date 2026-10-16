package runninghub

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

var ErrNoKeys = errors.New("RunningHub keys are not configured")
var ErrKeySlotUnavailable = errors.New("RunningHub key slot is unavailable")

type poolKey struct {
	key           string
	slot          int
	inUse         int
	disabledUntil time.Time
}

type KeyPool struct {
	mu    sync.Mutex
	keys  []*poolKey
	byKey map[string]*poolKey
	limit int
	next  int
	wake  chan struct{}
	now   func() time.Time
}

func NewKeyPool() *KeyPool {
	return &KeyPool{limit: 1, wake: make(chan struct{}), now: time.Now, byKey: make(map[string]*poolKey)}
}

func (p *KeyPool) ensure() {
	if p.wake == nil {
		p.wake = make(chan struct{})
	}
	if p.now == nil {
		p.now = time.Now
	}
	if p.limit < 1 {
		p.limit = 1
	}
	if p.byKey == nil {
		p.byKey = make(map[string]*poolKey)
		for _, entry := range p.keys {
			p.byKey[entry.key] = entry
		}
	}
}

func (p *KeyPool) Configure(input []string, limit int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensure()
	if limit < 1 {
		limit = 1
	}
	if limit > 5 {
		limit = 5
	}
	seen := map[string]bool{}
	keys := make([]*poolKey, 0, len(input))
	for _, raw := range input {
		key := strings.TrimSpace(raw)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		entry := p.byKey[key]
		if entry == nil {
			entry = &poolKey{key: key}
			p.byKey[key] = entry
		}
		entry.slot = len(keys)
		keys = append(keys, entry)
	}
	p.keys, p.limit = keys, limit
	if len(keys) == 0 {
		p.next = 0
	} else {
		p.next %= len(keys)
	}
	p.signalLocked()
}

func (p *KeyPool) KeyCount() int { p.mu.Lock(); defer p.mu.Unlock(); return len(p.keys) }

func (p *KeyPool) Acquire(ctx context.Context) (*Lease, error) {
	for {
		p.mu.Lock()
		p.ensure()
		if len(p.keys) == 0 {
			p.mu.Unlock()
			return nil, ErrNoKeys
		}
		now := p.now()
		for offset := 0; offset < len(p.keys); offset++ {
			index := (p.next + offset) % len(p.keys)
			entry := p.keys[index]
			if entry.inUse < p.limit && !entry.disabledUntil.After(now) {
				entry.inUse++
				p.next = (index + 1) % len(p.keys)
				lease := &Lease{pool: p, entry: entry, key: entry.key, slot: entry.slot}
				p.mu.Unlock()
				return lease, nil
			}
		}
		wake := p.wake
		wait := p.nextEnableLocked(now)
		p.mu.Unlock()
		if err := waitForPool(ctx, wake, wait); err != nil {
			return nil, err
		}
	}
}

func (p *KeyPool) AcquireSlot(ctx context.Context, slot int) (*Lease, error) {
	for {
		p.mu.Lock()
		p.ensure()
		if slot < 0 || slot >= len(p.keys) {
			p.mu.Unlock()
			return nil, ErrKeySlotUnavailable
		}
		entry := p.keys[slot]
		now := p.now()
		if entry.inUse < p.limit && !entry.disabledUntil.After(now) {
			entry.inUse++
			lease := &Lease{pool: p, entry: entry, key: entry.key, slot: slot}
			p.mu.Unlock()
			return lease, nil
		}
		wake := p.wake
		wait := time.Duration(0)
		if entry.disabledUntil.After(now) {
			wait = entry.disabledUntil.Sub(now)
		}
		p.mu.Unlock()
		if err := waitForPool(ctx, wake, wait); err != nil {
			return nil, err
		}
	}
}

func waitForPool(ctx context.Context, wake <-chan struct{}, wait time.Duration) error {
	if wait <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wake:
			return nil
		}
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-wake:
		return nil
	case <-timer.C:
		return nil
	}
}

func (p *KeyPool) nextEnableLocked(now time.Time) time.Duration {
	var earliest time.Time
	for _, entry := range p.keys {
		if entry.disabledUntil.After(now) && (earliest.IsZero() || entry.disabledUntil.Before(earliest)) {
			earliest = entry.disabledUntil
		}
	}
	if earliest.IsZero() {
		return 0
	}
	return earliest.Sub(now)
}

func (p *KeyPool) signalLocked() { close(p.wake); p.wake = make(chan struct{}) }

type Lease struct {
	pool  *KeyPool
	entry *poolKey
	key   string
	slot  int
	once  sync.Once
}

func (l *Lease) Key() string {
	if l == nil {
		return ""
	}
	return l.key
}
func (l *Lease) Slot() int {
	if l == nil {
		return -1
	}
	return l.slot
}
func (l *Lease) Release() {
	if l == nil || l.pool == nil {
		return
	}
	l.once.Do(func() {
		l.pool.mu.Lock()
		if l.entry.inUse > 0 {
			l.entry.inUse--
		}
		l.pool.signalLocked()
		l.pool.mu.Unlock()
	})
}
func (l *Lease) Disable() {
	if l == nil || l.pool == nil {
		return
	}
	l.pool.mu.Lock()
	until := l.pool.now().Add(30 * time.Minute)
	if until.After(l.entry.disabledUntil) {
		l.entry.disabledUntil = until
	}
	l.pool.signalLocked()
	l.pool.mu.Unlock()
}
