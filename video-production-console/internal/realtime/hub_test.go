package realtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"video-production-console/internal/domain"
)

func TestLocalOrigin(t *testing.T) {
	for _, tc := range []struct {
		origin string
		want   bool
	}{
		{"", true}, {"http://localhost:2030", true}, {"http://127.0.0.1:2030", true},
		{"https://evil.example", false}, {"not a url", false},
	} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Origin", tc.origin)
		if got := localOrigin(r); got != tc.want {
			t.Errorf("origin %q = %v, want %v", tc.origin, got, tc.want)
		}
	}
}

func TestHubCloseIsIdempotentAndRejectsNewClients(t *testing.T) {
	hub := NewHub(nil)
	hub.Close()
	hub.Close()

	if hub.add("task-1", &client{}) {
		t.Fatal("closed hub accepted a new client")
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if !hub.closed {
		t.Fatal("hub was not marked closed")
	}
	if len(hub.byTask) != 0 {
		t.Fatalf("closed hub retained clients: %d", len(hub.byTask))
	}
}

func TestClientSequenceCursorOrdersReplayBeforeConcurrentPublishAndDeduplicates(t *testing.T) {
	hub := NewHub(nil)
	written := make([]int64, 0, 2)
	client := &client{writeEvent: func(_ context.Context, event domain.TaskEvent) error {
		written = append(written, event.Sequence)
		return nil
	}}

	client.mu.Lock()
	if !hub.add("task-1", client) {
		t.Fatal("open hub rejected client")
	}
	published := make(chan struct{})
	go func() {
		hub.Publish(context.Background(), "task-1", domain.TaskEvent{Sequence: 2})
		close(published)
	}()
	if err := client.writeLocked(context.Background(), domain.TaskEvent{Sequence: 1}); err != nil {
		t.Fatal(err)
	}
	if err := client.writeLocked(context.Background(), domain.TaskEvent{Sequence: 2}); err != nil {
		t.Fatal(err)
	}
	client.mu.Unlock()
	<-published

	if !reflect.DeepEqual(written, []int64{1, 2}) {
		t.Fatalf("event order=%v, want [1 2]", written)
	}
}

func TestParseAfter(t *testing.T) {
	if got, _ := parseAfter(""); got != 0 {
		t.Fatal(got)
	}
	if got, _ := parseAfter("12"); got != 12 {
		t.Fatal(got)
	}
	if _, err := parseAfter("-1"); err == nil {
		t.Fatal("expected error")
	}
	if _, err := parseAfter("x"); err == nil {
		t.Fatal("expected error")
	}
}
