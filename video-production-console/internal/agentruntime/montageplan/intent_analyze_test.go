package montageplan

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLocalIntentAnalyzerMovieModeUsesNarrationConcepts(t *testing.T) {
	intents, err := LocalIntentAnalyzer{}.Analyze(context.Background(), []TimedSentence{
		{StartMS: 0, EndMS: 4000, Text: "真正危险的是家庭现金流越来越薄。"},
	})
	if err != nil || len(intents) == 0 {
		t.Fatalf("intents=%v err=%v", intents, err)
	}
	joined := strings.Join(intents[0].VisualConcepts, " ")
	if !strings.Contains(joined, "家庭") && !strings.Contains(joined, "现金流") {
		t.Fatalf("movie concepts=%q", joined)
	}
}

func TestLocalIntentAnalyzerLandscapeModeStaysScenic(t *testing.T) {
	intents, err := LocalIntentAnalyzer{RestrictToLandscape: true}.Analyze(context.Background(), []TimedSentence{
		{StartMS: 0, EndMS: 4000, Text: "真正危险的是家庭现金流越来越薄。"},
	})
	if err != nil || len(intents) == 0 {
		t.Fatalf("intents=%v err=%v", intents, err)
	}
	joined := strings.Join(intents[0].VisualConcepts, " ")
	if joined != "风景 景观" {
		t.Fatalf("landscape concepts=%q", joined)
	}
}

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

func TestLocalIntentAnalyzerExtractsHousingCuesAndCatalogTags(t *testing.T) {
	intents, err := LocalIntentAnalyzer{}.Analyze(context.Background(), []TimedSentence{
		{StartMS: 0, EndMS: 5000, Text: "法拍房五个月跌了四十万，现在还要不要买房？"},
		{StartMS: 5000, EndMS: 9000, Text: "月供和首付压着，房贷比存款还吓人。"},
	})
	if err != nil || len(intents) == 0 {
		t.Fatalf("intents=%v err=%v", intents, err)
	}
	joined := ""
	concepts := ""
	for _, intent := range intents {
		joined += strings.Join(intent.Entities, " ") + " " + strings.Join(intent.Topics, " ") + " "
		concepts += strings.Join(intent.VisualConcepts, " ") + " "
	}
	for _, want := range []string{"法拍", "买房", "房贷", "月供", "首付"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("housing cues missing %q in %q", want, joined)
		}
	}
	for _, want := range []string{"cityscape", "money", "financial_data"} {
		if !strings.Contains(concepts, want) {
			t.Fatalf("catalog tag bridge missing %q in %q", want, concepts)
		}
	}
}

func TestEnrichIntentsMergesSpokenCatalogTags(t *testing.T) {
	intents := enrichIntentsWithSpokenCues([]NarrativeIntent{{
		SegmentID: "seg-001", Text: "现在还要不要买房", VisualConcepts: []string{"房子"},
	}}, false)
	if len(intents) != 1 {
		t.Fatalf("intents=%#v", intents)
	}
	joined := strings.Join(intents[0].VisualConcepts, " ")
	if !strings.Contains(joined, "cityscape") || !strings.Contains(joined, "skyscrapers") {
		t.Fatalf("enriched concepts=%q", joined)
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
