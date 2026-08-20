package portable

import (
	"strings"
	"testing"
)

func TestValidateManifestRejectsUnsafeAndDuplicatePaths(t *testing.T) {
	base := Manifest{SchemaVersion: 1, AppVersion: "0.1.0", Entries: []Entry{{Path: "bin/video-production-console.exe", Size: 3, SHA256: strings.Repeat("a", 64)}}}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../escape", "/absolute", `C:\\escape`, `bin\\..\\escape`, "", "bin//x"} {
		bad := base
		bad.Entries = []Entry{{Path: path, Size: 1, SHA256: strings.Repeat("b", 64)}}
		if err := bad.Validate(); err == nil {
			t.Fatalf("accepted %q", path)
		}
	}

	for _, path := range []string{"bin/./x", "./bin/x", "bin/foo/", "bin/\x00x", "..", ".", "C:/escape", "bin/../escape"} {
		bad := base
		bad.Entries = []Entry{{Path: path, Size: 1, SHA256: strings.Repeat("b", 64)}}
		if err := bad.Validate(); err == nil {
			t.Fatalf("accepted %q", path)
		}
	}

	dup := base
	dup.Entries = []Entry{
		{Path: "bin/a.exe", Size: 1, SHA256: strings.Repeat("c", 64)},
		{Path: "bin/a.exe", Size: 1, SHA256: strings.Repeat("c", 64)},
	}
	if err := dup.Validate(); err == nil {
		t.Fatal("accepted duplicate paths")
	}

	caseDup := base
	caseDup.Entries = []Entry{
		{Path: "bin/App.exe", Size: 1, SHA256: strings.Repeat("c", 64)},
		{Path: "bin/app.exe", Size: 1, SHA256: strings.Repeat("d", 64)},
	}
	if err := caseDup.Validate(); err == nil {
		t.Fatal("accepted case-colliding paths")
	}

	unsorted := base
	unsorted.Entries = []Entry{
		{Path: "z/file.bin", Size: 1, SHA256: strings.Repeat("c", 64)},
		{Path: "a/file.bin", Size: 1, SHA256: strings.Repeat("d", 64)},
	}
	if err := unsorted.Validate(); err == nil {
		t.Fatal("accepted unsorted entries")
	}

	cultureSorted := base
	cultureSorted.Entries = []Entry{
		{Path: "runtime/python/Lib/site-packages/comtypes/git.py", Size: 1, SHA256: strings.Repeat("c", 64)},
		{Path: "runtime/python/Lib/site-packages/comtypes/GUID.py", Size: 1, SHA256: strings.Repeat("d", 64)},
	}
	if err := cultureSorted.Validate(); err == nil {
		t.Fatal("accepted PowerShell culture-sorted entries")
	}

	upper := base
	upper.Entries = []Entry{{Path: "bin/app.exe", Size: 1, SHA256: strings.Repeat("A", 64)}}
	if err := upper.Validate(); err == nil {
		t.Fatal("accepted uppercase SHA-256")
	}

	shortHash := base
	shortHash.Entries = []Entry{{Path: "bin/app.exe", Size: 1, SHA256: strings.Repeat("a", 63)}}
	if err := shortHash.Validate(); err == nil {
		t.Fatal("accepted short SHA-256")
	}

	badSchema := base
	badSchema.SchemaVersion = 2
	if err := badSchema.Validate(); err == nil {
		t.Fatal("accepted unsupported schema")
	}

	badVersion := base
	badVersion.AppVersion = "v0.1.0"
	if err := badVersion.Validate(); err == nil {
		t.Fatal("accepted non-semver app version")
	}

	neg := base
	neg.Entries = []Entry{{Path: "bin/app.exe", Size: -1, SHA256: strings.Repeat("a", 64)}}
	if err := neg.Validate(); err == nil {
		t.Fatal("accepted negative size")
	}
}
