package partnergateway

import (
	"fmt"
	"sync"
	"time"
)

const (
	defaultRequestsPerMinute = 60
	defaultMaxConcurrent     = 3
	defaultIdleTTL           = 2 * time.Minute
	concurrencyRetryAfter    = time.Second
)

type LimiterOptions struct {
	RequestsPerMinute int
	MaxConcurrent     int
	Clock             func() time.Time
}

type LimitError struct {
	RetryAfter time.Duration
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("partner request limit reached; retry after %s", e.RetryAfter)
}

type Limiter struct {
	mu                sync.Mutex
	entries           map[string]*limiterEntry
	requestsPerMinute int
	maxConcurrent     int
	clock             func() time.Time
	idleTTL           time.Duration
}

type limiterEntry struct {
	bucket     time.Time
	requests   int
	concurrent int
	lastSeen   time.Time
}

func NewLimiter(options ...LimiterOptions) *Limiter {
	var config LimiterOptions
	if len(options) != 0 {
		config = options[0]
	}
	if config.RequestsPerMinute <= 0 {
		config.RequestsPerMinute = defaultRequestsPerMinute
	}
	if config.MaxConcurrent <= 0 {
		config.MaxConcurrent = defaultMaxConcurrent
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	return &Limiter{
		entries:           make(map[string]*limiterEntry),
		requestsPerMinute: config.RequestsPerMinute,
		maxConcurrent:     config.MaxConcurrent,
		clock:             config.Clock,
		idleTTL:           defaultIdleTTL,
	}
}

func (l *Limiter) Acquire(partnerID string) (release func(), err error) {
	now := l.clock().UTC()
	bucket := now.Truncate(time.Minute)

	l.mu.Lock()
	defer l.mu.Unlock()

	l.removeIdleEntries(now)
	entry := l.entries[partnerID]
	if entry == nil {
		entry = &limiterEntry{bucket: bucket}
		l.entries[partnerID] = entry
	}
	entry.lastSeen = now
	if !entry.bucket.Equal(bucket) {
		entry.bucket = bucket
		entry.requests = 0
	}
	if entry.concurrent >= l.maxConcurrent {
		return nil, &LimitError{RetryAfter: concurrencyRetryAfter}
	}
	if entry.requests >= l.requestsPerMinute {
		return nil, &LimitError{RetryAfter: bucket.Add(time.Minute).Sub(now)}
	}

	entry.requests++
	entry.concurrent++
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			if entry.concurrent > 0 {
				entry.concurrent--
			}
			entry.lastSeen = l.clock().UTC()
		})
	}, nil
}

func (l *Limiter) removeIdleEntries(now time.Time) {
	for partnerID, entry := range l.entries {
		if entry.concurrent == 0 && !entry.lastSeen.After(now.Add(-l.idleTTL)) {
			delete(l.entries, partnerID)
		}
	}
}
