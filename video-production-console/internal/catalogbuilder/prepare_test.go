package catalogbuilder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareMediaRootCreatesLayoutAndStripsSuffix(t *testing.T) {
	parent := t.TempDir()
	nested := filepath.Join(parent, "data", "media", "originals", "movies")
	got, err := PrepareMediaRoot(nested)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(parent, "data", "media")
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
	for _, dir := range []string{"originals/movies", "originals/broll", "originals/images"} {
		if _, err := os.Stat(filepath.Join(got, filepath.FromSlash(dir))); err != nil {
			t.Fatalf("missing %s: %v", dir, err)
		}
	}
}

func TestPrepareMediaRootUsesDefaultWhenEmpty(t *testing.T) {
	root := filepath.Join(t.TempDir(), "e-media")
	old := defaultMediaRoot
	defaultMediaRoot = root
	t.Cleanup(func() { defaultMediaRoot = old })
	got, err := PrepareMediaRoot("")
	if err != nil {
		t.Fatal(err)
	}
	if got != root {
		t.Fatalf("got %s want %s", got, root)
	}
	if _, err := os.Stat(filepath.Join(root, "originals", "movies")); err != nil {
		t.Fatal(err)
	}
}

func TestResolveToolPathAcceptsDirectory(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "ffmpeg.exe")
	if err := os.WriteFile(exe, []byte("x"), 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveToolPath(dir, "ffmpeg.exe")
	if err != nil || got != exe {
		t.Fatalf("got %s err=%v", got, err)
	}
}

func TestResolveToolPathSearchesBesideBuilder(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "ffprobe.exe")
	if err := os.WriteFile(exe, []byte("x"), 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveToolPath("", "ffprobe.exe", dir)
	if err != nil || got != exe {
		t.Fatalf("got %s err=%v", got, err)
	}
}

func TestSaveConfigCreatesMissingMediaRoot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog-builder.config.json")
	root := filepath.Join(t.TempDir(), "missing", "media")
	if err := SaveConfig(path, Config{MediaRoot: root}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "originals", "movies")); err != nil {
		t.Fatal(err)
	}
	saved, err := LoadConfig(path)
	if err != nil || saved.MediaRoot != root {
		t.Fatalf("saved=%+v err=%v", saved, err)
	}
}

func TestDefaultMediaRootIsEDrive(t *testing.T) {
	if DefaultMediaRoot != `E:\data\media` {
		t.Fatalf("DefaultMediaRoot=%q", DefaultMediaRoot)
	}
	if !strings.HasPrefix(strings.ToUpper(DefaultMediaRoot), "E:") {
		t.Fatalf("default should stay on E: got %q", DefaultMediaRoot)
	}
}
