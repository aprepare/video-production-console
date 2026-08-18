package montageplan

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestApplyShotSelectionsBoostsChosenShot(t *testing.T) {
	ranked := []rankedCandidate{
		{
			Item:  mediaItem{ID: "a", ShotID: "shot-a", Kind: mediaKindBroll, Summary: "coins"},
			Score: 0.2,
			Match: MatchEvidence{IntentID: "seg-001", Level: matchLevelNeutral, Reason: "neutral"},
		},
		{
			Item:  mediaItem{ID: "b", ShotID: "shot-b", Kind: mediaKindBroll, Summary: "city"},
			Score: 0.3,
			Match: MatchEvidence{IntentID: "seg-001", Level: matchLevelDirect, Reason: "direct"},
		},
	}
	got, notes := applyShotSelections(ranked, []ShotSelectPick{{
		IntentID: "seg-001", ShotID: "shot-a", Why: "月供用硬币",
	}})
	if len(notes) != 1 || !strings.Contains(notes[0], "picked 1") {
		t.Fatalf("notes=%v", notes)
	}
	if got[0].Item.ShotID != "shot-a" || got[0].Score < shotSelectBoost-1e-9 {
		t.Fatalf("want shot-a boosted first, got %#v", got[0])
	}
	if !strings.HasPrefix(got[0].Match.Reason, "llm:") {
		t.Fatalf("reason=%q", got[0].Match.Reason)
	}
}

func TestParseShotSelectJSONRejectsUnknownIDs(t *testing.T) {
	jobs := []ShotSelectJob{{
		Intent: NarrativeIntent{SegmentID: "seg-001"},
		Candidates: []ShotSelectCandidate{
			{ShotID: "shot-real"},
		},
	}}
	picks, err := parseShotSelectJSON(`[{"segment_id":"seg-001","shot_id":"shot-fake","why":"x"},{"segment_id":"seg-001","shot_id":"shot-real","why":"ok"}]`, jobs)
	if err != nil || len(picks) != 1 || picks[0].ShotID != "shot-real" {
		t.Fatalf("picks=%#v err=%v", picks, err)
	}
}

func TestHTTPIntentAnalyzerSelectsFromShortlist(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"[{\"segment_id\":\"seg-001\",\"shot_id\":\"shot-city\",\"why\":\"法拍对城市\"}]"}}]}`))
	}))
	defer server.Close()
	analyzer := NewHTTPIntentAnalyzer(IntentAnalyzerConfig{
		BaseURL: server.URL, Model: "x", APIKey: "k", HTTPClient: server.Client(),
	})
	selector, ok := analyzer.(ShotSelector)
	if !ok {
		t.Fatalf("analyzer %T is not ShotSelector", analyzer)
	}
	picks, err := selector.SelectShots(t.Context(), []ShotSelectJob{{
		Intent: NarrativeIntent{SegmentID: "seg-001", Text: "还要不要买房"},
		Candidates: []ShotSelectCandidate{
			{ShotID: "shot-city", Summary: "city skyline"},
			{ShotID: "shot-coin", Summary: "coins"},
		},
	}})
	if err != nil || len(picks) != 1 || picks[0].ShotID != "shot-city" {
		t.Fatalf("picks=%#v err=%v", picks, err)
	}
}

func TestIntentSystemPromptUsesCatalogTags(t *testing.T) {
	prompt := intentSystemPrompt(false)
	for _, want := range []string{"cityscape", "coins", "visual_query", "empty wallet"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q", want)
		}
	}
}

func TestHTTPIntentAnalyzerAnalyzeKeepsSentenceWindows(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"[{\"segment_id\":\"x\",\"start_ms\":9,\"end_ms\":9,\"text\":\"买房\",\"entities\":[],\"topics\":[],\"mood\":\"warning\",\"visual_concepts\":[\"cityscape\"],\"visual_query\":\"night city skyline\",\"metaphors\":[],\"importance\":0.8,\"caption_kind\":\"\"}]"}}]}`))
	}))
	defer server.Close()
	analyzer := NewHTTPIntentAnalyzer(IntentAnalyzerConfig{
		BaseURL: server.URL, Model: "x", APIKey: "k", HTTPClient: server.Client(),
	})
	intents, err := analyzer.Analyze(t.Context(), []TimedSentence{{StartMS: 1000, EndMS: 4000, Text: "买房"}})
	if err != nil || len(intents) != 1 {
		t.Fatalf("intents=%#v err=%v", intents, err)
	}
	if intents[0].SegmentID != "seg-001" || intents[0].StartMS != 1000 || intents[0].EndMS != 4000 {
		t.Fatalf("window not preserved: %#v", intents[0])
	}
	if intents[0].VisualQuery != "night city skyline" {
		raw, _ := json.Marshal(intents[0])
		t.Fatalf("visual_query missing: %s", raw)
	}
}
