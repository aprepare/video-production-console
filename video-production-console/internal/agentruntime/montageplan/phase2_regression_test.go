package montageplan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"video-production-console/internal/narration"
)

func phase2Doc(t *testing.T, duration float64, words ...narration.Word) *narration.WordTimingDocument {
	t.Helper()
	doc, err := narration.NewWordTimingDocument("phase2", "test", "", words)
	if err != nil {
		t.Fatal(err)
	}
	// Preserve the fixture's explicit duration for tests that exercise bounds
	// independently of the final word's end time.
	doc.Duration = duration
	return &doc
}

func TestPhase2TimingHighlightsSingleCharacterTokens(t *testing.T) {
	doc := phase2Doc(t, 20,
		narration.Word{Text: "但", StartTime: 0, EndTime: .2}, narration.Word{Text: "是", StartTime: .2, EndTime: .4},
		narration.Word{Text: "人工智能", StartTime: 4, EndTime: 4.4},
		narration.Word{Text: "数字", StartTime: 8, EndTime: 8.3}, narration.Word{Text: "50%", StartTime: 8.3, EndTime: 8.6},
	)
	items := timingHighlightCaptions(doc)
	if len(items) == 0 {
		t.Fatal("expected highlights")
	}
	foundButIs := false
	for i, it := range items {
		if it.EndS-it.StartS < 2 || it.EndS-it.StartS > 4 || it.EndS > doc.Duration {
			t.Fatalf("item %d bounds: %#v", i, it)
		}
		if i > 0 && it.StartS < items[i-1].EndS {
			t.Fatalf("overlap: %#v then %#v", items[i-1], it)
		}
		if it.Text == "但" || it.Text == "是" {
			t.Fatalf("single-character split token: %#v", it)
		}
		if strings.Contains(it.Text, "但是") {
			foundButIs = true
		}
	}
	if !foundButIs {
		t.Fatal("expected complete 但是 highlight")
	}
	covered := 0.0
	for _, it := range items {
		covered += it.EndS - it.StartS
	}
	if covered > doc.Duration*.25 {
		t.Fatalf("coverage %.2f exceeds 25%%", covered)
	}
	if got := timingHighlightCaptions(phase2Doc(t, 20, narration.Word{Text: "普通", StartTime: 1, EndTime: 1.2})); len(got) != 0 {
		t.Fatalf("no candidates should return empty: %#v", got)
	}
}

func TestPhase2TimingHighlightsUsesNarrationDurationAndReachesCoverageWindow(t *testing.T) {
	words := make([]narration.Word, 0, 20)
	for i := 0; i < 20; i++ {
		start := float64(i * 5)
		words = append(words, narration.Word{Text: "50%", StartTime: start, EndTime: start + .2})
	}
	doc := phase2Doc(t, 20, words...)
	items := timingHighlightCaptions(doc, 100)
	covered := 0.0
	for _, it := range items {
		if it.EndS > 100 || it.StartS < 0 || it.EndS <= it.StartS {
			t.Fatalf("out-of-bounds item: %#v", it)
		}
		covered += it.EndS - it.StartS
	}
	coverage := covered / 100
	if coverage < 0.15 || coverage > 0.25 {
		t.Fatalf("coverage %.3f, want [0.15, 0.25]", coverage)
	}
}

func TestPhase2SemanticSFXPlacementsWordTokensAndFallback(t *testing.T) {
	lib := []verifiedSFX{{"open", "open", "r1", "sfx_opening_hit"}, {"drop", "drop", "r2", "sfx_water_drop"}, {"whoosh", "whoosh", "r3", "sfx_whoosh"}, {"conclusion", "conclusion", "r4", "sfx_conclusion_hit"}}
	doc := phase2Doc(t, 300, narration.Word{Text: "但", StartTime: 60, EndTime: 60.2}, narration.Word{Text: "是", StartTime: 60.2, EndTime: 60.4}, narration.Word{Text: "结论", StartTime: 150, EndTime: 150.4})
	placements := buildSemanticSFXPlacements(300, lib, doc)
	if len(placements) < 3 || len(placements) > 12 {
		t.Fatalf("placements=%d", len(placements))
	}
	if placements[0]["cache_key"] != "sfx_opening_hit" || placements[0]["start_s"] != 0.0 {
		t.Fatalf("opening=%#v", placements[0])
	}
	found60 := false
	allowed := map[string]bool{"sfx_opening_hit": true, "sfx_water_drop": true, "sfx_whoosh": true, "sfx_conclusion_hit": true}
	last := -12.0
	for _, p := range placements {
		if !allowed[p["cache_key"].(string)] || p["db"] == nil {
			t.Fatalf("invalid placement %#v", p)
		}
		start := p["start_s"].(float64)
		if start == 60 {
			found60 = true
		}
		if start-last < 12 && start != 0 {
			t.Fatalf("gap <12: %.2f", start-last)
		}
		last = start
	}
	if !found60 {
		t.Fatal("expected semantic hit at 60s from single-character tokens")
	}
	if got := buildSemanticSFXPlacements(300, lib, phase2Doc(t, 300, narration.Word{Text: "普通", StartTime: 1, EndTime: 1.2})); len(got) < 3 || len(got) > 12 {
		t.Fatalf("fallback placements=%d", len(got))
	}
}

func TestPhase2SemanticSFXMapsCueKinds(t *testing.T) {
	lib := []verifiedSFX{{"open", "open", "r1", "sfx_opening_hit"}, {"drop", "drop", "r2", "sfx_water_drop"}, {"whoosh", "whoosh", "r3", "sfx_whoosh"}, {"conclusion", "conclusion", "r4", "sfx_conclusion_hit"}}
	doc := phase2Doc(t, 300,
		narration.Word{Text: "浣?", StartTime: 60, EndTime: 60.2}, narration.Word{Text: "鏄?", StartTime: 60.2, EndTime: 60.4},
		narration.Word{Text: "5", StartTime: 100, EndTime: 100.1}, narration.Word{Text: "0%", StartTime: 100.1, EndTime: 100.3},
		narration.Word{Text: "\u7ed3\u8bba", StartTime: 150, EndTime: 150.3},
		narration.Word{Text: "普通", StartTime: 200, EndTime: 200.2},
	)
	placements := buildSemanticSFXPlacements(300, lib, doc)
	allowed := map[string]bool{"sfx_opening_hit": true, "sfx_water_drop": true, "sfx_whoosh": true, "sfx_conclusion_hit": true}
	found := map[string]bool{}
	prev := -12.0
	for _, p := range placements {
		key := p["cache_key"].(string)
		if !allowed[key] || p["db"] == nil {
			t.Fatalf("invalid placement %#v", p)
		}
		start := p["start_s"].(float64)
		if start != 0 && start-prev < 12 {
			t.Fatalf("gap <12: %.2f", start-prev)
		}
		prev = start
		found[key] = true
		if start == 200 {
			t.Fatalf("ordinary short word produced cue: %#v", p)
		}
	}
	for _, key := range []string{"sfx_opening_hit", "sfx_whoosh", "sfx_water_drop", "sfx_conclusion_hit"} {
		if !found[key] {
			t.Fatalf("missing semantic cache key %s: %#v", key, placements)
		}
	}
}

func TestPhase2SemanticSFXSpreadsAcrossFullRuntime(t *testing.T) {
	lib := []verifiedSFX{{"open", "open", "r1", "sfx_opening_hit"}, {"drop", "drop", "r2", "sfx_water_drop"}, {"whoosh", "whoosh", "r3", "sfx_whoosh"}, {"conclusion", "conclusion", "r4", "sfx_conclusion_hit"}}
	var words []narration.Word
	for s := 15.0; s < 380; s += 15 {
		words = append(words, narration.Word{Text: "50%", StartTime: s, EndTime: s + .2})
	}
	doc := phase2Doc(t, 380, words...)
	placements := buildSemanticSFXPlacements(380, lib, doc)
	if len(placements) < 8 || len(placements) > 12 {
		t.Fatalf("placements=%d, want the ~1-per-45s budget", len(placements))
	}
	last := placements[len(placements)-1]["start_s"].(float64)
	if last < 380*0.6 {
		t.Fatalf("last cue at %.1fs: hits must follow the script to the end, not cluster in the opening", last)
	}
}

func TestPhase2SemanticSFXTurnBeatsNumberInSameClause(t *testing.T) {
	// 但是到2026年 is a narrative turn first and a statistic second.
	if got := semanticSFXKind("但是到2026年", "但"); got != "sfx_whoosh" {
		t.Fatalf("kind=%q, want sfx_whoosh", got)
	}
	if got := semanticSFXKind("结果房价跌了67%", "结果"); got != "sfx_whoosh" {
		t.Fatalf("kind=%q, want sfx_whoosh", got)
	}
	if got := semanticSFXKind("记住这三点", "记住"); got != "sfx_conclusion_hit" {
		t.Fatalf("kind=%q, want sfx_conclusion_hit", got)
	}
	if got := semanticSFXKind("40万套", "40"); got != "sfx_water_drop" {
		t.Fatalf("kind=%q, want sfx_water_drop", got)
	}
}

func TestPhase2DecodeWordTimingRejectsNegativeAndOverlap(t *testing.T) {
	if _, err := narration.NewWordTimingDocument("x", "test", "", []narration.Word{{Text: "a", StartTime: -1, EndTime: 1}}); err == nil {
		t.Fatal("negative timing must fail")
	}
	if _, err := narration.NewWordTimingDocument("x", "test", "", []narration.Word{{Text: "a", StartTime: 0, EndTime: 1}, {Text: "b", StartTime: .5, EndTime: 2}}); err == nil {
		t.Fatal("overlap must fail")
	}
}

func TestPhase2DecodeWordTimingRequiresScriptHash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "timing.json")
	payload := `{"schema_version":1,"script":"hello","provider":"test","words":[{"text":"hello","start_time":0,"end_time":1}]}`
	if err := os.WriteFile(path, []byte(payload), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := decodeWordTiming(path); err == nil {
		t.Fatal("missing script_hash must fail")
	}
}

func TestPhase2BuildV2DefaultPlannerNotesFinanceTension(t *testing.T) {
	manifest, plan := v2Fixture(t)
	// Inject valid word-timing evidence into the fixture manifest.
	raw, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	timingPath := filepath.Join(filepath.Dir(manifest), "timing.json")
	doc := phase2Doc(t, 120, narration.Word{Text: "但是", StartTime: 1, EndTime: 2}, narration.Word{Text: "关键", StartTime: 20, EndTime: 21})
	b, _ := json.Marshal(doc)
	if err := os.WriteFile(timingPath, b, 0644); err != nil {
		t.Fatal(err)
	}
	inputs := m["inputs"].([]any)
	inputs = append(inputs, map[string]any{"role": "word_timing", "path": timingPath})
	m["inputs"] = inputs
	b, _ = json.Marshal(m)
	if err := os.WriteFile(manifest, b, 0644); err != nil {
		t.Fatal(err)
	}
	planRaw := buildV2PlanJSON(t, v2Options(manifest, plan))
	graphics := planRaw["graphics"].(map[string]any)
	if graphics["captions"].(map[string]any)["mode"] != string(CaptionSpoken) {
		t.Fatalf("default caption mode=%#v", graphics["captions"])
	}
	notes, _ := planRaw["planner_notes"].([]any)
	for _, n := range notes {
		if strings.Contains(n.(string), "style_preset=finance_tension") {
			return
		}
	}
	t.Fatalf("planner_notes missing style_preset=finance_tension: %#v", notes)
}

func TestPhase2LegacyPlannerNotesFinanceCalm(t *testing.T) {
	manifest, plan := v2Fixture(t)
	raw := buildV2PlanJSON(t, v2Options(manifest, plan))
	notes := raw["planner_notes"].([]any)
	for _, n := range notes {
		if strings.Contains(n.(string), "style_preset=finance_calm") {
			return
		}
	}
	t.Fatalf("missing calm note: %#v", notes)
}
