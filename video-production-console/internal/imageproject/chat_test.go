package imageproject

import "testing"

func TestChatCompletionsURLRejectsUnsafeOrInvalidBases(t *testing.T) {
	for _, base := range []string{
		"file:///tmp/model",
		"javascript:alert(1)",
		"https://user:password@example.com/v1",
		"http://",
	} {
		if _, err := chatCompletionsURL(base); err == nil {
			t.Fatalf("chatCompletionsURL(%q) accepted unsafe base", base)
		}
	}
}

func TestChatCompletionsURLNormalizesOpenAICompatibleBases(t *testing.T) {
	for base, want := range map[string]string{
		"https://example.com":                  "https://example.com/v1/chat/completions",
		"https://example.com/v1/":              "https://example.com/v1/chat/completions",
		"https://example.com/chat/completions": "https://example.com/chat/completions",
	} {
		got, err := chatCompletionsURL(base)
		if err != nil {
			t.Fatalf("chatCompletionsURL(%q): %v", base, err)
		}
		if got != want {
			t.Fatalf("chatCompletionsURL(%q)=%q, want %q", base, got, want)
		}
	}
}
