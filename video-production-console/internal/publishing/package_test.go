package publishing

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublishingPackageReaderVerifiesAndDecodesPackage(t *testing.T) {
	data := []byte(`{"titles":["长标题"],"short_titles":["第一短标题","第二短标题"],"description":"简介"}`)
	path := filepath.Join(t.TempDir(), "publishing_package.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)

	got, err := (Reader{}).Read(path, hex.EncodeToString(digest[:]))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.ShortTitles) != 2 || got.ShortTitles[0] != "第一短标题" || got.Description != "简介" {
		t.Fatalf("package=%+v", got)
	}
}

func TestPublishingPackageReaderRejectsUntrustedInput(t *testing.T) {
	root := t.TempDir()
	valid := filepath.Join(root, "valid.json")
	if err := os.WriteFile(valid, []byte(`{"short_titles":["标题"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	invalid := filepath.Join(root, "invalid.json")
	if err := os.WriteFile(invalid, []byte(`not-json`), 0o600); err != nil {
		t.Fatal(err)
	}
	oversized := filepath.Join(root, "oversized.json")
	if err := os.WriteFile(oversized, []byte(`{"description":"`+strings.Repeat("x", maxPackageBytes)+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name, path, sha string
	}{
		{name: "directory", path: root},
		{name: "digest mismatch", path: valid, sha: strings.Repeat("0", 64)},
		{name: "invalid JSON", path: invalid},
		{name: "oversized", path: oversized},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := (Reader{}).Read(tt.path, tt.sha); err == nil {
				t.Fatal("expected untrusted publishing package to fail")
			}
		})
	}
}
