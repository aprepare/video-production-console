package openaicompat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFastRequestPreservesModelAndEffort(t *testing.T) {
	for _, tier := range []string{"", "priority", "default"} {
		t.Run(tier, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["model"] != "chosen-model" || body["reasoning_effort"] != "high" {
					t.Errorf("model/effort changed: %v", body)
				}
				if tier == "" {
					if _, present := body["service_tier"]; present {
						t.Errorf("legacy request changed: %v", body)
					}
				} else if body["service_tier"] != tier {
					t.Errorf("wrong tier: %v", body)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
			}))
			defer server.Close()
			client := withServiceTier(&HTTPChatClient{BaseURL: server.URL, APIKey: "test"}, tier)
			_, err := client.Chat(ChatRequest{Model: "chosen-model", ReasoningEffort: "high"})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNormalizeServiceTier(t *testing.T) {
	for in, want := range map[string]string{"": "", "default": "default", "priority": "priority", " Fast ": "priority"} {
		got, err := NormalizeServiceTier(in)
		if err != nil || got != want {
			t.Fatalf("%q: %q %v", in, got, err)
		}
	}
	if _, err := NormalizeServiceTier("turbo"); err == nil {
		t.Fatal("invalid tier accepted")
	}
}

func TestFastNodesRemainIndependent(t *testing.T) {
	client := &flowFakeClient{}
	spec, ok := parseFlowSpec(`{"nodes":[
	{"id":"source","type":"input"},
	{"id":"fast","type":"agent","config":{"model":"agent-fast","reasoning_effort":"high","service_tier":"priority","system_prompt":"fast"}},
	{"id":"normal","type":"agent","config":{"model":"agent-normal","reasoning_effort":"medium","system_prompt":"normal"}},
	{"id":"writer","type":"writer"},
	{"id":"review","type":"reviewer","config":{"service_tier":"default"}}
	],"edges":[["source","fast"],["source","normal"],["fast","writer"],["normal","writer"]]}`)
	if !ok {
		t.Fatal("invalid fixture")
	}
	opts := Options{Model: "writer", ServiceTier: "priority", ReasoningEffort: "xhigh"}
	runFlowAgents(client, opts, "source", t.TempDir(), spec)
	if len(client.requests) != 2 {
		t.Fatalf("requests=%d", len(client.requests))
	}
	for _, req := range client.requests {
		if req.Model == "agent-fast" && (req.ServiceTier != "priority" || req.ReasoningEffort != "high") {
			t.Fatalf("fast config lost: %+v", req)
		}
		if req.Model == "agent-normal" && (req.ServiceTier != "" || req.ReasoningEffort != "medium") {
			t.Fatalf("writer config leaked: %+v", req)
		}
	}
	if reviewerServiceTier(opts, &spec) != "default" {
		t.Fatal("reviewer inherited writer Fast")
	}
	if reviewerServiceTier(opts, nil) != "priority" {
		t.Fatal("legacy reviewer lost slot Fast")
	}
}

func TestFastReviewerRequestPreservesEffort(t *testing.T) {
	client := &reviewerFakeClient{reply: `{"verdict":"pass","summary":"ok"}`}
	outcome := ReviewRemixDraft(ReviewOptions{
		Client: client, Model: "review-model", ReasoningEffort: "high", ServiceTier: "priority",
		Source: "source", DraftJSON: reviewerTestDraft(), OutputDir: t.TempDir(),
	})
	if outcome.Record.Error != "" {
		t.Fatal(outcome.Record.Error)
	}
	if len(client.requests) != 1 {
		t.Fatalf("requests=%d", len(client.requests))
	}
	req := client.requests[0]
	if req.ServiceTier != "priority" || req.Model != "review-model" || req.ReasoningEffort != "high" {
		t.Fatalf("request=%+v", req)
	}
}
