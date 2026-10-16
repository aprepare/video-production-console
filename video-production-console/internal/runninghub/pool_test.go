package runninghub

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestKeyPoolRoundRobinConcurrencyAndQueue(t *testing.T) {
	pool := NewKeyPool()
	pool.Configure([]string{"a", "b"}, 1)
	one, err := pool.Acquire(t.Context())
	if err != nil || one.Key() != "a" || one.Slot() != 0 {
		t.Fatalf("first=%v %v", one, err)
	}
	two, err := pool.Acquire(t.Context())
	if err != nil || two.Key() != "b" || two.Slot() != 1 {
		t.Fatalf("second=%v %v", two, err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if _, err := pool.Acquire(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("busy acquire=%v", err)
	}
	one.Release()
	three, err := pool.Acquire(t.Context())
	if err != nil || three.Key() != "a" {
		t.Fatalf("third=%v %v", three, err)
	}
	three.Release()
	two.Release()

	pool.Configure([]string{"a"}, 2)
	first, _ := pool.Acquire(t.Context())
	second, err := pool.Acquire(t.Context())
	if err != nil || first.Key() != "a" || second.Key() != "a" {
		t.Fatalf("limit 2: %v", err)
	}
	first.Release()
	second.Release()
}

func TestKeyPoolDisableAndAcquireSlot(t *testing.T) {
	now := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	pool := NewKeyPool()
	pool.now = func() time.Time { return now }
	pool.Configure([]string{"a", "b"}, 1)
	one, _ := pool.Acquire(t.Context())
	one.Disable()
	one.Release()
	two, err := pool.Acquire(t.Context())
	if err != nil || two.Key() != "b" {
		t.Fatalf("fallback=%v %v", two, err)
	}
	two.Release()

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := pool.AcquireSlot(ctx, 0); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("disabled slot=%v", err)
	}
	now = now.Add(30 * time.Minute)
	recovered, err := pool.AcquireSlot(t.Context(), 0)
	if err != nil || recovered.Key() != "a" || recovered.Slot() != 0 {
		t.Fatalf("recovered=%v %v", recovered, err)
	}
	recovered.Release()
	pool.Configure([]string{"b"}, 1)
	if _, err := pool.AcquireSlot(t.Context(), 1); !errors.Is(err, ErrKeySlotUnavailable) {
		t.Fatalf("removed slot=%v", err)
	}
}

func TestKeyPoolConfigureCopiesAndNormalizesKeys(t *testing.T) {
	keys := []string{" a ", "", "a", "b"}
	pool := NewKeyPool()
	pool.Configure(keys, 0)
	keys[0] = "changed"
	if pool.KeyCount() != 2 {
		t.Fatalf("count=%d", pool.KeyCount())
	}
	lease, err := pool.Acquire(t.Context())
	if err != nil || lease.Key() != "a" {
		t.Fatalf("lease=%v err=%v", lease, err)
	}
	lease.Release()
	pool.Configure(nil, 1)
	if _, err := pool.Acquire(t.Context()); !errors.Is(err, ErrNoKeys) {
		t.Fatalf("no keys=%v", err)
	}
}

func TestKeyPoolReaddingKeyPreservesBusyAndDisabledLedger(t *testing.T) {
	now := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	pool := NewKeyPool()
	pool.now = func() time.Time { return now }
	pool.Configure([]string{"a", "b"}, 1)
	busy, err := pool.AcquireSlot(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	pool.Configure([]string{"b"}, 1)
	pool.Configure([]string{"a", "b"}, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := pool.AcquireSlot(ctx, 0); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("re-added busy key acquired: %v", err)
	}
	busy.Disable()
	busy.Release()
	pool.Configure([]string{"b"}, 1)
	pool.Configure([]string{"a", "b"}, 1)
	ctx2, cancel2 := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel2()
	if _, err := pool.AcquireSlot(ctx2, 0); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("re-added disabled key acquired: %v", err)
	}
	now = now.Add(30 * time.Minute)
	lease, err := pool.AcquireSlot(t.Context(), 0)
	if err != nil || lease.Key() != "a" {
		t.Fatalf("re-enabled lease=%v err=%v", lease, err)
	}
	lease.Release()
}
