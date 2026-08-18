package conversation

import (
	"fmt"
	"testing"
	"time"

	"video-production-console/internal/domain"
)

// The completion workers no longer own a ticker each, so an item that arrives
// without a wake signal (retry backoff, another process' leftovers) depends
// entirely on the shared sweep to be claimed.
func TestIdleSweepClaimsCompletionWithoutWakeSignal(t *testing.T) {
	ctx, repo, session := brokerFixture(t)
	broker := NewBroker(repo, &fakeRPC{})
	t.Cleanup(broker.Close)
	if err := repo.EnqueueCompletion(ctx, session.ID, "turn-sweep-only"); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * completionSweepInterval)
	for {
		item, err := repo.CompletionForTurn(ctx, "turn-sweep-only")
		if err == nil && item.Status == domain.ChatCompletionDone {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("shared sweep never claimed the completion: %+v err=%v", item, err)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// A claim fans the backlog out by waking a peer worker. That signal must never
// block the draining worker, even with more items than the wake buffer holds
// and nobody consuming it.
func TestDrainCompletesBacklogLargerThanWakeBufferWithoutConsumers(t *testing.T) {
	ctx, repo, session := brokerFixture(t)
	broker := NewBroker(repo, nil)
	t.Cleanup(broker.Close)
	backlog := completionQueueSize + 16
	for i := range backlog {
		if err := repo.EnqueueCompletion(ctx, session.ID, fmt.Sprintf("turn-backlog-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	drained := make(chan error, 1)
	go func() { drained <- broker.drainCompletions(ctx) }()
	select {
	case err := <-drained:
		if err != nil {
			t.Fatalf("drain backlog: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("drain blocked on the completion wake fan-out")
	}
	for i := range backlog {
		turnID := fmt.Sprintf("turn-backlog-%d", i)
		item, err := repo.CompletionForTurn(ctx, turnID)
		if err != nil || item.Status != domain.ChatCompletionDone {
			t.Fatalf("completion %q was left behind: %+v err=%v", turnID, item, err)
		}
	}
}

// Close must account for every goroutine NewBroker started, sweeper included,
// or it waits on a WaitGroup counter that nobody will decrement.
func TestCloseStopsCompletionWorkersAndSweeper(t *testing.T) {
	_, repo, _ := brokerFixture(t)
	broker := NewBroker(repo, &fakeRPC{})
	closed := make(chan struct{})
	go func() {
		broker.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("Close did not stop every completion goroutine")
	}
	broker.Close()
}
