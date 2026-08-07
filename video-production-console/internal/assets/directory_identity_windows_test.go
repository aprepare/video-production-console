//go:build windows

package assets

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalNoFollowRejectsWindowsReparsePoint(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	link := filepath.Join(root, "link")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("creating Windows symlink requires permission: %v", err)
	}
	if _, err := canonicalNoFollow(link, true); !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("err=%v", err)
	}
}
