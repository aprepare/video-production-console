package montageplan

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLocalIntentAnalyzerExtractsFinanceCues(t *testing.T) {
	sentences := []TimedSentence{
		{StartMS: 0, EndMS: 4000, Text: "真正危险的是家庭现金流越来越薄。"},
		{StartMS: 4000, EndMS: 9000, Text: "所以要去银行把存款看清楚。"},
	}
	intents, err := LocalIntentAnalyzer{}.Analyze(context.Background(), sentences)
	if err != nil || len(intents) == 0 {
		t.Fatalf("intents=%v err=%v", intents, err)
	}
	joined := ""
	for _, intent := range intents {
		joined += intent.Text
		if len(intent.Entities) == 0 && len(intent.Metaphors) == 0 {
			t.Fatalf("expected entities or metaphors: %#v", intent)
		}
	}
	if !strings.Contains(joined, "家庭") && !strings.Contains(joined, "银行") {
		t.Fatalf("unexpected texts %q", joined)
	}
}

func TestHTTPIntentAnalyzerFallsBackOnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	analyzer := NewHTTPIntentAnalyzer(IntentAnalyzerConfig{
		BaseURL: server.URL, Model: "x", APIKey: "k", HTTPClient: server.Client(),
	})
	intents, err := analyzer.Analyze(context.Background(), []TimedSentence{
		{StartMS: 0, EndMS: 3000, Text: "但是银行的机会正在收紧。"},
	})
	if err != nil || len(intents) == 0 {
		t.Fatalf("fallback failed: %v %#v", err, intents)
	}
}

func TestHTTPIntentAnalyzerParsesStrictJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"[{\"segment_id\":\"seg-001\",\"start_ms\":0,\"end_ms\":1000,\"text\":\"银行\",\"entities\":[\"银行\"],\"topics\":[\"银行\"],\"mood\":\"warning\",\"visual_concepts\":[],\"metaphors\":[],\"importance\":0.8,\"caption_kind\":\"turning_point\"}]"}}]}`))
	}))
	defer server.Close()
	analyzer := NewHTTPIntentAnalyzer(IntentAnalyzerConfig{
		BaseURL: server.URL, Model: "x", APIKey: "k", HTTPClient: server.Client(),
	})
	intents, err := analyzer.Analyze(context.Background(), []TimedSentence{{Text: "银行", EndMS: 1000}})
	if err != nil || len(intents) != 1 || intents[0].SegmentID != "seg-001" {
		t.Fatalf("intents=%#v err=%v", intents, err)
	}
}
