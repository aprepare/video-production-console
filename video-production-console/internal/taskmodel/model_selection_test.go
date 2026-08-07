package taskmodel

import (
	"strings"
	"testing"
)

func TestSelectionNormalize(t *testing.T) {
	got, err := Normalize(Selection{Model: "  openai/gpt-5.6-sol:preview  ", ReasoningEffort: " XHIGH "})
	if err != nil {
		t.Fatal(err)
	}
	want := Selection{Model: "openai/gpt-5.6-sol:preview", ReasoningEffort: "xhigh"}
	if got != want {
		t.Fatalf("Normalize()=%+v, want %+v", got, want)
	}
}

func TestSelectionNormalizeRejectsInvalidValuesWithoutEcho(t *testing.T) {
	tests := []Selection{
		{Model: "", ReasoningEffort: "medium"},
		{Model: "-gpt-5.6-sol", ReasoningEffort: "medium"},
		{Model: "gpt-5.6-sol;rm", ReasoningEffort: "medium"},
		{Model: "dangerous value\nsecret", ReasoningEffort: "medium"},
		{Model: "gpt-5.6-sol", ReasoningEffort: "secret-effort"},
		{Model: strings.Repeat("a", 129), ReasoningEffort: "medium"},
	}
	for _, selection := range tests {
		_, err := Normalize(selection)
		if err == nil {
			t.Fatalf("Normalize(%+v) succeeded", selection)
		}
		if (selection.Model != "" && strings.Contains(err.Error(), selection.Model)) || (selection.ReasoningEffort != "" && strings.Contains(err.Error(), selection.ReasoningEffort)) {
			t.Fatalf("error echoed rejected input: %q", err)
		}
	}
}

func TestSelectionNormalizeAcceptsEveryReasoningEffort(t *testing.T) {
	for _, effort := range []string{"low", "medium", "high", "xhigh", "max", "ultra"} {
		got, err := Normalize(Selection{Model: DefaultModel, ReasoningEffort: effort})
		if err != nil {
			t.Fatalf("Normalize(%q) error=%v", effort, err)
		}
		if got.ReasoningEffort != effort {
			t.Fatalf("Normalize(%q) effort=%q", effort, got.ReasoningEffort)
		}
	}
}

func TestSelectionResolveUsesNonEmptyOverrides(t *testing.T) {
	defaults := Selection{Model: " gpt-5.6-sol ", ReasoningEffort: " MEDIUM "}
	got, err := Resolve(defaults, Selection{ReasoningEffort: " HIGH "})
	if err != nil {
		t.Fatal(err)
	}
	want := Selection{Model: "gpt-5.6-sol", ReasoningEffort: "high"}
	if got != want {
		t.Fatalf("Resolve()=%+v, want %+v", got, want)
	}
}

func TestSelectionResolveIgnoresWhitespaceOnlyOverrides(t *testing.T) {
	defaults := Selection{Model: DefaultModel, ReasoningEffort: DefaultReasoningEffort}
	got, err := Resolve(defaults, Selection{Model: " \t ", ReasoningEffort: "\r\n"})
	if err != nil {
		t.Fatal(err)
	}
	if got != defaults {
		t.Fatalf("Resolve()=%+v, want defaults %+v", got, defaults)
	}
}

func TestSelectionConstants(t *testing.T) {
	if DefaultModel != "gpt-5.6-sol" || DefaultReasoningEffort != "medium" {
		t.Fatalf("defaults=%q/%q", DefaultModel, DefaultReasoningEffort)
	}
}
