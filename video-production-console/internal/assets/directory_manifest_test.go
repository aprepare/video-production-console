package assets

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildDirectoryManifestReturnsOnlyRelativeSortedMetadata(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "draft")
	if err := os.MkdirAll(filepath.Join(target, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "z.txt"), []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "nested", "a.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	canonical, entries, err := BuildDirectoryManifest(root, target)
	if err != nil {
		t.Fatal(err)
	}
	if canonical != filepath.Clean(target) {
		t.Fatalf("canonical=%q", canonical)
	}
	if len(entries) != 3 || entries[0].Path != "nested" || entries[1].Path != "nested/a.txt" || entries[2].Path != "z.txt" {
		t.Fatalf("entries=%+v", entries)
	}
	if entries[0].Kind != "directory" || entries[0].Size != 0 || entries[2].Kind != "file" || entries[2].Size != 3 {
		t.Fatalf("metadata=%+v", entries)
	}
}

func TestBuildDirectoryManifestRejectsEscape(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	_, _, err := BuildDirectoryManifest(root, target)
	if !errors.Is(err, ErrDirectoryPathInvalid) {
		t.Fatalf("err=%v", err)
	}
}

func TestBuildDirectoryManifestRejectsExcessiveDepth(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "draft")
	deep := target
	for i := 0; i <= MaxDirectoryManifestDepth; i++ {
		deep = filepath.Join(deep, "d")
	}
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	_, _, err := BuildDirectoryManifest(root, target)
	if !errors.Is(err, ErrDirectoryTooDeep) {
		t.Fatalf("err=%v", err)
	}
}
