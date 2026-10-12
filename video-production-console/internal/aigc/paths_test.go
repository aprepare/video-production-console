package aigc

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestPathsAndRedaction(t *testing.T) {
	root := t.TempDir()
	if _, err := safePath(root, filepath.Join(root, "..", "outside")); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("escape allowed: %v", err)
	}
	if _, err := safePath(root, filepath.Join(root, "child", "image.png")); err != nil {
		t.Fatal(err)
	}
	key := "sk-test-secret"
	if got := redact("error "+key+" "+strings.Repeat("a", 40), key); strings.Contains(got, key) || strings.Contains(got, strings.Repeat("a", 40)) {
		t.Fatal(got)
	}
	for _, base := range []string{"https://example.test/v1", "https://example.test/v1/images/edits", "https://example.test/v1/images/generations"} {
		got, err := editsURL(base)
		if err != nil || got != "https://example.test/v1/images/edits" {
			t.Fatalf("%s => %s %v", base, got, err)
		}
	}
}
