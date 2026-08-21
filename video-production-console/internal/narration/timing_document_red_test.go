package narration

import (
	"encoding/json"
	"strings"
	"testing"
)

// RED contract: the sidecar must preserve vendor order and timing fields,
// together with provenance metadata needed to audit a delivery.
func TestWordTimingDocumentJSONPreservesWordsAndMetadata(t *testing.T) {
	words := []Word{
		{Text: "人工", StartTime: 0.1, EndTime: 0.4, Confidence: 0.91},
		{Text: "智能", StartTime: 0.4, EndTime: 0.9, Confidence: 0.87},
	}
	doc, err := NewWordTimingDocument("人工智能", "volcengine", "sha256:abc", words)
	if err != nil {
		t.Fatalf("NewWordTimingDocument: %v", err)
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got WordTimingDocument
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Script != "人工智能" || got.Provider != "volcengine" || got.Hash != "sha256:abc" {
		t.Fatalf("metadata lost: %+v", got)
	}
	if len(got.Words) != 2 || got.Words[0] != words[0] || got.Words[1] != words[1] {
		t.Fatalf("word order/timing lost: %+v", got.Words)
	}
}

func TestWordTimingDocumentRejectsInvalidTimings(t *testing.T) {
	cases := []struct {
		name  string
		words []Word
	}{
		{"empty text", []Word{{Text: "", StartTime: 0, EndTime: 1}}},
		{"negative duration", []Word{{Text: "x", StartTime: 1, EndTime: 0}}},
		{"overlap", []Word{{Text: "a", StartTime: 0, EndTime: 1}, {Text: "b", StartTime: .5, EndTime: 2}}},
		{"reverse order", []Word{{Text: "a", StartTime: 1, EndTime: 2}, {Text: "b", StartTime: 0, EndTime: .5}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewWordTimingDocument("ab", "vendor", "hash", tc.words); err == nil {
				t.Fatal("expected invalid timings to be rejected")
			}
		})
	}
}

func TestComposeDoesNotSplitHanCompoundWords(t *testing.T) {
	words := wordsFrom([]string{"我", "喜欢", "人", "工", "智", "能", "和", "房", "地", "产"}, .6)
	captions, _, err := Compose("我喜欢人工智能和房地产", words, Options{MaxLineRunes: 6})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	joined := strings.Join(captionTexts(captions), "|")
	if strings.Contains(joined, "人|工") || strings.Contains(joined, "智|能") || strings.Contains(joined, "房|地") || strings.Contains(joined, "地|产") {
		t.Fatalf("compound Han word split across cues: %q", joined)
	}
}
