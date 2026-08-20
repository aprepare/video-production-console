package portable

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractVerifiesEveryEntryBeforeCommit(t *testing.T) {
	overlay := testOverlay(t, Manifest{SchemaVersion: 1, AppVersion: "0.1.0", Entries: []Entry{{Path: "bin/app.exe", Size: 3, SHA256: sha256Hex([]byte("app"))}}}, map[string][]byte{"bin/app.exe": []byte("bad")})
	root := filepath.Join(t.TempDir(), "app")
	err := ExtractToStaging(context.Background(), overlay, root)
	if !errors.Is(err, ErrEntryHash) {
		t.Fatalf("err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "0.1.0")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("committed bad version: %v", err)
	}
}

func TestExtractCommitsVerifiedTree(t *testing.T) {
	overlay := testOverlay(t, Manifest{}, map[string][]byte{
		"bin/app.exe": []byte("app"),
		"share/note":  []byte("ok"),
	})
	root := filepath.Join(t.TempDir(), "app")
	if err := ExtractToStaging(context.Background(), overlay, root); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, overlay.Manifest.AppVersion, "bin", "app.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "app" {
		t.Fatalf("got=%q", got)
	}
	if entries, err := os.ReadDir(filepath.Join(root, ".staging")); err == nil && len(entries) != 0 {
		t.Fatalf("staging leftover=%v", names(entries))
	}
}

func TestExtractReusesExistingVerifiedVersion(t *testing.T) {
	overlay := testOverlay(t, Manifest{}, map[string][]byte{"bin/app.exe": []byte("app")})
	root := filepath.Join(t.TempDir(), "app")
	if err := ExtractToStaging(context.Background(), overlay, root); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, overlay.Manifest.AppVersion, "keep.txt")
	if err := os.WriteFile(marker, []byte("reuse"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ExtractToStaging(context.Background(), overlay, root); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "reuse" {
		t.Fatalf("existing version was overwritten: %q", got)
	}
}

func TestExtractRejectsZipPathEscape(t *testing.T) {
	root := filepath.Join(t.TempDir(), "app")
	overlay := overlayFromZip(t, Manifest{
		SchemaVersion: 1,
		AppVersion:    "0.1.0",
		Entries:       []Entry{{Path: "bin/app.exe", Size: 1, SHA256: sha256Hex([]byte("x"))}},
	}, mustZip(t, map[string][]byte{"../escape": []byte("x")}))
	if err := ExtractToStaging(context.Background(), overlay, root); err == nil || !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "0.1.0")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("committed escaped version: %v", err)
	}
}

func TestExtractRejectsAbsoluteZipPath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "app")
	overlay := overlayFromZip(t, Manifest{
		SchemaVersion: 1,
		AppVersion:    "0.1.0",
		Entries:       []Entry{{Path: "bin/app.exe", Size: 1, SHA256: sha256Hex([]byte("x"))}},
	}, mustZip(t, map[string][]byte{"/absolute": []byte("x")}))
	if err := ExtractToStaging(context.Background(), overlay, root); err == nil || !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("err=%v", err)
	}
}

func TestExtractRejectsSymlinkMetadata(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	header := &zip.FileHeader{Name: "bin/app.exe"}
	header.SetMode(os.ModeSymlink | 0o777)
	writer, err := zw.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("app")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "app")
	overlay := overlayFromZip(t, Manifest{
		SchemaVersion: 1,
		AppVersion:    "0.1.0",
		Entries:       []Entry{{Path: "bin/app.exe", Size: 3, SHA256: sha256Hex([]byte("app"))}},
	}, buf.Bytes())
	if err := ExtractToStaging(context.Background(), overlay, root); !errors.Is(err, ErrSymlink) {
		t.Fatalf("err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "0.1.0")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("committed symlink version: %v", err)
	}
}

func TestExtractRejectsUnexpectedMissingAndDuplicateEntries(t *testing.T) {
	root := filepath.Join(t.TempDir(), "app")
	t.Run("unexpected", func(t *testing.T) {
		overlay := overlayFromZip(t, Manifest{
			SchemaVersion: 1,
			AppVersion:    "0.1.0",
			Entries:       []Entry{{Path: "bin/app.exe", Size: 3, SHA256: sha256Hex([]byte("app"))}},
		}, mustZip(t, map[string][]byte{"bin/app.exe": []byte("app"), "extra.bin": []byte("x")}))
		if err := ExtractToStaging(context.Background(), overlay, root); !errors.Is(err, ErrUnexpectedEntry) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("missing", func(t *testing.T) {
		overlay := overlayFromZip(t, Manifest{
			SchemaVersion: 1,
			AppVersion:    "0.1.0",
			Entries: []Entry{
				{Path: "a.bin", Size: 1, SHA256: sha256Hex([]byte("a"))},
				{Path: "b.bin", Size: 1, SHA256: sha256Hex([]byte("b"))},
			},
		}, mustZip(t, map[string][]byte{"a.bin": []byte("a")}))
		if err := ExtractToStaging(context.Background(), overlay, root); !errors.Is(err, ErrMissingEntry) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("duplicate", func(t *testing.T) {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for range 2 {
			writer, err := zw.Create("bin/app.exe")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := writer.Write([]byte("app")); err != nil {
				t.Fatal(err)
			}
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		overlay := overlayFromZip(t, Manifest{
			SchemaVersion: 1,
			AppVersion:    "0.1.0",
			Entries:       []Entry{{Path: "bin/app.exe", Size: 3, SHA256: sha256Hex([]byte("app"))}},
		}, buf.Bytes())
		if err := ExtractToStaging(context.Background(), overlay, root); !errors.Is(err, ErrDuplicateEntry) {
			t.Fatalf("err=%v", err)
		}
	})
}

func TestExtractRespectsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	overlay := testOverlay(t, Manifest{}, map[string][]byte{"bin/app.exe": []byte("app")})
	root := filepath.Join(t.TempDir(), "app")
	if err := ExtractToStaging(ctx, overlay, root); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, overlay.Manifest.AppVersion)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("committed canceled version: %v", err)
	}
}

func TestExtractRejectsWhenDiskSpaceCheckFails(t *testing.T) {
	overlay := testOverlay(t, Manifest{}, map[string][]byte{"bin/app.exe": []byte("app")})
	root := filepath.Join(t.TempDir(), "app")
	extractor := Extractor{SpaceCheck: func(string, int64) error { return ErrInsufficientSpace }}
	if err := extractor.ExtractToStaging(context.Background(), overlay, root); !errors.Is(err, ErrInsufficientSpace) {
		t.Fatalf("err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, overlay.Manifest.AppVersion)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("committed without space: %v", err)
	}
}

func testOverlay(t *testing.T, manifest Manifest, files map[string][]byte) *Overlay {
	t.Helper()
	if manifest.AppVersion == "" {
		manifest.AppVersion = "0.1.0"
	}
	if manifest.SchemaVersion == 0 {
		manifest.SchemaVersion = SchemaVersion
	}
	if len(manifest.Entries) == 0 {
		for path, body := range files {
			manifest.Entries = append(manifest.Entries, Entry{Path: path, Size: int64(len(body)), SHA256: sha256Hex(body)})
		}
		sortEntries(manifest.Entries)
	}
	zipBytes := mustZip(t, files)
	if manifest.PayloadSHA256 == "" {
		manifest.PayloadSHA256 = sha256Hex(zipBytes)
	}
	raw := appendOverlay(t, []byte("MZ"), zipBytes, manifest)
	overlay, err := ReadOverlay(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return overlayFromZip(t, manifest, zipBytes)
	}
	return overlay
}

func overlayFromZip(t *testing.T, manifest Manifest, zipBytes []byte) *Overlay {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.PayloadSHA256 == "" {
		manifest.PayloadSHA256 = sha256Hex(zipBytes)
	}
	return &Overlay{Manifest: manifest, PayloadSize: int64(len(zipBytes)), Zip: reader}
}

func names(entries []os.DirEntry) []string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.Name())
	}
	return out
}

func mustWrite(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
}
