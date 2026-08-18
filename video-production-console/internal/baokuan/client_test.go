package baokuan

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestClientSearchBundleAndSSEReconnect(t *testing.T) {
	var events int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/channels/library/materials/search":
			if r.URL.Query().Get("limit") != "3" {
				t.Errorf("limit=%s", r.URL.Query().Get("limit"))
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"materials":[{"id":"a"}]}`)
		case "/api/channels/library/materials/bundle":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"code":0,"msg":"成功","data":{"videos":[{"record":{"feed_id":"a"},"transcript":{"text":"完整正文"}}]}}`)
		case "/api/channels/library/events":
			events++
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			fmt.Fprintf(w, "data: {\"id\":\"%d\",\"type\":\"update\"}\n\n", events)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	c.HTTPClient = &http.Client{Timeout: time.Second}
	m, err := c.SearchMaterials(context.Background(), url.Values{"limit": {"3"}})
	if err != nil || len(m) != 1 {
		t.Fatalf("search: %v %#v", err, m)
	}
	b, err := c.GetMaterialBundle(context.Background(), BundleRequest{FeedIDs: []string{"a"}})
	if err != nil || len(b.Videos) != 1 {
		t.Fatalf("bundle: %v %#v", err, b)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := c.Subscribe(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-ch:
		if ev.Type != "update" {
			t.Fatalf("event=%#v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("SSE timeout")
	}
}

func TestBundleLimit(t *testing.T) {
	c := NewClient("http://127.0.0.1:1")
	_, err := c.GetMaterialBundle(context.Background(), BundleRequest{FeedIDs: make([]string, 21)})
	if err == nil {
		t.Fatal("expected limit error")
	}
}
