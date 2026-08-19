package partnergateway

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestLimiterDefaultsToSixtyRequestsAndThreeConcurrent(t *testing.T) {
	clock := newLimiterTestClock(time.Date(2035, time.August, 19, 12, 0, 0, 0, time.UTC))
	limiter := NewLimiter(LimiterOptions{Clock: clock.Now})

	releases := make([]func(), 0, 3)
	for range 3 {
		release, err := limiter.Acquire("partner-a")
		if err != nil {
			t.Fatalf("default concurrency rejected early: %v", err)
		}
		releases = append(releases, release)
	}
	if _, err := limiter.Acquire("partner-a"); err == nil {
		t.Fatal("default concurrency allowed a fourth request")
	}
	for _, release := range releases {
		release()
	}

	for range 57 {
		release, err := limiter.Acquire("partner-a")
		if err != nil {
			t.Fatalf("default minute quota rejected early: %v", err)
		}
		release()
	}
	if _, err := limiter.Acquire("partner-a"); err == nil {
		t.Fatal("default minute quota allowed a sixty-first request")
	}
}

func TestLimiterKeepsPartnerQuotasIsolated(t *testing.T) {
	clock := newLimiterTestClock(time.Date(2035, time.August, 19, 12, 0, 0, 0, time.UTC))
	limiter := NewLimiter(LimiterOptions{
		Clock:             clock.Now,
		RequestsPerMinute: 2,
		MaxConcurrent:     2,
	})

	for range 2 {
		release, err := limiter.Acquire("partner-a")
		if err != nil {
			t.Fatal(err)
		}
		release()
	}
	if _, err := limiter.Acquire("partner-a"); err == nil {
		t.Fatal("partner A exceeded its quota")
	}

	release, err := limiter.Acquire("partner-b")
	if err != nil {
		t.Fatalf("partner A consumed partner B's quota: %v", err)
	}
	release()
}

func TestLimiterReleasesConcurrencyAfterCancellationRelease(t *testing.T) {
	clock := newLimiterTestClock(time.Date(2035, time.August, 19, 12, 0, 0, 0, time.UTC))
	limiter := NewLimiter(LimiterOptions{
		Clock:             clock.Now,
		RequestsPerMinute: 10,
		MaxConcurrent:     1,
	})

	release, err := limiter.Acquire("partner-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := limiter.Acquire("partner-a"); err == nil {
		t.Fatal("concurrency limit was not enforced")
	}

	// A cancelled handler calls its release function while unwinding.
	release()
	release() // release must be safe when cancellation and defer both call it.

	nextRelease, err := limiter.Acquire("partner-a")
	if err != nil {
		t.Fatalf("cancelled request retained its concurrency slot: %v", err)
	}
	nextRelease()
}

func TestLimiterUsesUTCMinuteBucketAndReportsRetryAfter(t *testing.T) {
	local := time.FixedZone("UTC+8", 8*60*60)
	clock := newLimiterTestClock(time.Date(2035, time.August, 19, 12, 0, 30, 0, local))
	limiter := NewLimiter(LimiterOptions{
		Clock:             clock.Now,
		RequestsPerMinute: 1,
		MaxConcurrent:     1,
	})

	release, err := limiter.Acquire("partner-a")
	if err != nil {
		t.Fatal(err)
	}
	release()

	_, err = limiter.Acquire("partner-a")
	var limitErr *LimitError
	if !errors.As(err, &limitErr) {
		t.Fatalf("rate limit error=%v", err)
	}
	if limitErr.RetryAfter != 30*time.Second {
		t.Fatalf("retry after=%s", limitErr.RetryAfter)
	}

	limiter.mu.Lock()
	bucket := limiter.entries["partner-a"].bucket
	limiter.mu.Unlock()
	if bucket.Location() != time.UTC {
		t.Fatalf("bucket location=%v", bucket.Location())
	}
	if want := clock.Now().UTC().Truncate(time.Minute); !bucket.Equal(want) {
		t.Fatalf("bucket=%s want=%s", bucket, want)
	}

	clock.Advance(30 * time.Second)
	release, err = limiter.Acquire("partner-a")
	if err != nil {
		t.Fatalf("new UTC minute did not reset quota: %v", err)
	}
	release()
}

func TestLimiterRemovesEntriesIdleForTwoMinutes(t *testing.T) {
	clock := newLimiterTestClock(time.Date(2035, time.August, 19, 12, 0, 0, 0, time.UTC))
	limiter := NewLimiter(LimiterOptions{
		Clock:             clock.Now,
		RequestsPerMinute: 10,
		MaxConcurrent:     1,
	})

	release, err := limiter.Acquire("partner-a")
	if err != nil {
		t.Fatal(err)
	}
	release()
	clock.Advance(2 * time.Minute)

	release, err = limiter.Acquire("partner-b")
	if err != nil {
		t.Fatal(err)
	}
	release()

	limiter.mu.Lock()
	_, remains := limiter.entries["partner-a"]
	limiter.mu.Unlock()
	if remains {
		t.Fatal("idle partner entry was not removed")
	}
}

type limiterTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func newLimiterTestClock(now time.Time) *limiterTestClock {
	return &limiterTestClock{now: now}
}

func (c *limiterTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *limiterTestClock) Advance(duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(duration)
}
