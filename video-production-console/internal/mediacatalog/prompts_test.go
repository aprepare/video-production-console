package mediacatalog

import (
	"strings"
	"testing"
)

func TestAnalysisVersionIsPinned(t *testing.T) {
	if AnalysisVersion != "vision-v1" {
		t.Fatalf("analysis version must stay pinned to vision-v1, got %q", AnalysisVersion)
	}
}

func TestVisionPromptDemandsVisibleFactsAndForbidsSensitiveInference(t *testing.T) {
	required := []string{
		"only facts that are visible",
		"Never identify",
		"sensitive attributes",
		"Never transcribe",
		"has_text",
	}
	for _, fragment := range required {
		if !strings.Contains(visionSystemPrompt, fragment) {
			t.Fatalf("vision prompt must contain %q", fragment)
		}
	}
	for _, field := range []string{"summary", "mood", "setting", "people_count", "motion_level", "has_text", "tags"} {
		if !strings.Contains(visionSystemPrompt, field) {
			t.Fatalf("vision prompt must pin the %q response field", field)
		}
	}
}

func TestEmbeddingInputUsesFixedTemplate(t *testing.T) {
	analysis := ShotAnalysis{
		Summary: "a trader watches falling charts",
		Mood:    "tense",
		Setting: "trading floor",
		Tags:    []TagScore{{Value: "chart", Confidence: 0.9}, {Value: "screen", Confidence: 0.8}},
	}
	want := "summary: a trader watches falling charts\ntags: chart, screen\nmood: tense\nsetting: trading floor"
	if got := EmbeddingInput(analysis); got != want {
		t.Fatalf("embedding input mismatch:\n got %q\nwant %q", got, want)
	}
	if EmbeddingInput(analysis) != EmbeddingInput(analysis) {
		t.Fatal("embedding input must be deterministic")
	}
	empty := ShotAnalysis{Summary: "s", Mood: "m", Setting: "st"}
	if got := EmbeddingInput(empty); got != "summary: s\ntags: \nmood: m\nsetting: st" {
		t.Fatalf("empty-tag template mismatch: %q", got)
	}
}
