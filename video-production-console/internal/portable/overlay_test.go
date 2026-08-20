package portable

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"os"
	"strings"
	"testing"
)

func TestReadOverlayFindsAppendedPayload(t *testing.T) {
	exe := append([]byte("MZ-fake-stub"), buildTestOverlay(t, "0.1.0", map[string][]byte{"bin/app.exe": []byte("app")})...)
	overlay, err := ReadOverlay(bytes.NewReader(exe), int64(len(exe)))
	if err != nil {
		t.Fatal(err)
	}
	if overlay.Manifest.AppVersion != "0.1.0" {
		t.Fatalf("manifest=%+v", overlay.Manifest)
	}
	if overlay.PayloadSize == 0 {
		t.Fatal("missing payload")
	}
	found := false
	for _, file := range overlay.Zip.File {
		if file.Name == "bin/app.exe" {
			found = true
			rc, openErr := file.Open()
			if openErr != nil {
				t.Fatal(openErr)
			}
			body, readErr := io.ReadAll(rc)
			_ = rc.Close()
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(body) != "app" {
				t.Fatalf("entry=%q", body)
			}
		}
	}
	if !found {
		t.Fatal("zip entry missing")
	}
}

func TestOverlayRejectsBadMagic(t *testing.T) {
	raw := append([]byte("MZ"), buildTestOverlay(t, "0.1.0", map[string][]byte{"bin/app.exe": []byte("app")})...)
	copy(raw[len(raw)-trailerSize:], []byte("BADMAGIC!!!!!!!"))
	if _, err := ReadOverlay(bytes.NewReader(raw), int64(len(raw))); err == nil {
		t.Fatal("accepted bad magic")
	}
}

func TestOverlayRejectsTruncatedTrailer(t *testing.T) {
	raw := []byte("MZ-too-small")
	if _, err := ReadOverlay(bytes.NewReader(raw), int64(len(raw))); err == nil {
		t.Fatal("accepted truncated overlay")
	}
}

func TestOverlayRejectsZeroLengths(t *testing.T) {
	raw := append([]byte("MZ"), make([]byte, trailerSize)...)
	copy(raw[len(raw)-trailerSize:], []byte(trailerMagic))
	if _, err := ReadOverlay(bytes.NewReader(raw), int64(len(raw))); err == nil {
		t.Fatal("accepted zero zip/manifest lengths")
	}
}

func TestOverlayRejectsOverflowLengths(t *testing.T) {
	raw := append([]byte("MZ"), make([]byte, trailerSize)...)
	copy(raw[len(raw)-trailerSize:], []byte(trailerMagic))
	binary.LittleEndian.PutUint64(raw[len(raw)-16:], math.MaxUint64)
	binary.LittleEndian.PutUint64(raw[len(raw)-8:], math.MaxUint64)
	if _, err := ReadOverlay(bytes.NewReader(raw), int64(len(raw))); err == nil {
		t.Fatal("accepted overflowing lengths")
	}
}

func TestOverlayRejectsOutOfBoundsLengths(t *testing.T) {
	raw := append([]byte("MZ"), make([]byte, trailerSize)...)
	copy(raw[len(raw)-trailerSize:], []byte(trailerMagic))
	binary.LittleEndian.PutUint64(raw[len(raw)-16:], 1<<20)
	binary.LittleEndian.PutUint64(raw[len(raw)-8:], 32)
	if _, err := ReadOverlay(bytes.NewReader(raw), int64(len(raw))); err == nil {
		t.Fatal("accepted out-of-bounds lengths")
	}
}

func TestOverlayRejectsUnknownJSONFields(t *testing.T) {
	files := map[string][]byte{"bin/app.exe": []byte("app")}
	zipBytes := mustZip(t, files)
	manifest := validManifest("0.1.0", files, zipBytes)
	rawJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(rawJSON, &object); err != nil {
		t.Fatal(err)
	}
	object["extra"] = "nope"
	badJSON, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	raw := appendOverlayBytes(t, []byte("MZ"), zipBytes, badJSON)
	if _, err := ReadOverlay(bytes.NewReader(raw), int64(len(raw))); err == nil {
		t.Fatal("accepted unknown JSON field")
	}
}

func TestOverlayRejectsBadSchema(t *testing.T) {
	files := map[string][]byte{"bin/app.exe": []byte("app")}
	zipBytes := mustZip(t, files)
	manifest := validManifest("0.1.0", files, zipBytes)
	manifest.SchemaVersion = 99
	raw := appendOverlay(t, []byte("MZ"), zipBytes, manifest)
	if _, err := ReadOverlay(bytes.NewReader(raw), int64(len(raw))); err == nil {
		t.Fatal("accepted bad schema")
	}
}

func TestOverlayRejectsBadVersion(t *testing.T) {
	files := map[string][]byte{"bin/app.exe": []byte("app")}
	zipBytes := mustZip(t, files)
	manifest := validManifest("not-a-version", files, zipBytes)
	raw := appendOverlay(t, []byte("MZ"), zipBytes, manifest)
	if _, err := ReadOverlay(bytes.NewReader(raw), int64(len(raw))); err == nil {
		t.Fatal("accepted bad version")
	}
}

func TestOverlayRejectsUppercasePayloadHash(t *testing.T) {
	files := map[string][]byte{"bin/app.exe": []byte("app")}
	zipBytes := mustZip(t, files)
	manifest := validManifest("0.1.0", files, zipBytes)
	manifest.PayloadSHA256 = strings.ToUpper(manifest.PayloadSHA256)
	raw := appendOverlay(t, []byte("MZ"), zipBytes, manifest)
	if _, err := ReadOverlay(bytes.NewReader(raw), int64(len(raw))); err == nil {
		t.Fatal("accepted uppercase payload hash")
	}
}

func TestOverlayRejectsPayloadHashMismatch(t *testing.T) {
	files := map[string][]byte{"bin/app.exe": []byte("app")}
	zipBytes := mustZip(t, files)
	manifest := validManifest("0.1.0", files, zipBytes)
	manifest.PayloadSHA256 = strings.Repeat("ab", 32)
	raw := appendOverlay(t, []byte("MZ"), zipBytes, manifest)
	if _, err := ReadOverlay(bytes.NewReader(raw), int64(len(raw))); err == nil {
		t.Fatal("accepted payload hash mismatch")
	}
}

func TestOverlayRejectsUnsortedEntries(t *testing.T) {
	files := map[string][]byte{"z.bin": []byte("z"), "a.bin": []byte("a")}
	zipBytes := mustZip(t, files)
	manifest := validManifest("0.1.0", files, zipBytes)
	manifest.Entries = []Entry{
		{Path: "z.bin", Size: 1, SHA256: sha256Hex([]byte("z"))},
		{Path: "a.bin", Size: 1, SHA256: sha256Hex([]byte("a"))},
	}
	raw := appendOverlay(t, []byte("MZ"), zipBytes, manifest)
	if _, err := ReadOverlay(bytes.NewReader(raw), int64(len(raw))); err == nil {
		t.Fatal("accepted unsorted entries")
	}
}

func TestOverlayRejectsDuplicateEntries(t *testing.T) {
	files := map[string][]byte{"a.bin": []byte("a")}
	zipBytes := mustZip(t, files)
	manifest := validManifest("0.1.0", files, zipBytes)
	manifest.Entries = []Entry{
		{Path: "a.bin", Size: 1, SHA256: sha256Hex([]byte("a"))},
		{Path: "a.bin", Size: 1, SHA256: sha256Hex([]byte("a"))},
	}
	raw := appendOverlay(t, []byte("MZ"), zipBytes, manifest)
	if _, err := ReadOverlay(bytes.NewReader(raw), int64(len(raw))); err == nil {
		t.Fatal("accepted duplicate entries")
	}
}

func TestOverlayRejectsSymlinkZipEntry(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	header := &zip.FileHeader{Name: "bin/link"}
	header.SetMode(os.ModeSymlink | 0o777)
	writer, err := zw.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("target")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zipBytes := buf.Bytes()
	manifest := Manifest{
		SchemaVersion: 1,
		AppVersion:    "0.1.0",
		PayloadSHA256: sha256Hex(zipBytes),
		Entries:       []Entry{{Path: "bin/link", Size: 6, SHA256: sha256Hex([]byte("target"))}},
	}
	raw := appendOverlay(t, []byte("MZ"), zipBytes, manifest)
	if _, err := ReadOverlay(bytes.NewReader(raw), int64(len(raw))); err == nil {
		t.Fatal("accepted symlink zip entry")
	}
}

func TestOverlayRejectsZipPathEscape(t *testing.T) {
	zipBytes := mustZip(t, map[string][]byte{"../escape": []byte("x")})
	manifest := Manifest{
		SchemaVersion: 1,
		AppVersion:    "0.1.0",
		PayloadSHA256: sha256Hex(zipBytes),
		Entries:       []Entry{{Path: "bin/app.exe", Size: 1, SHA256: sha256Hex([]byte("x"))}},
	}
	raw := appendOverlay(t, []byte("MZ"), zipBytes, manifest)
	if _, err := ReadOverlay(bytes.NewReader(raw), int64(len(raw))); err == nil {
		t.Fatal("accepted zip path escape")
	}
}

func buildTestOverlay(t *testing.T, version string, files map[string][]byte) []byte {
	t.Helper()
	zipBytes := mustZip(t, files)
	manifest := validManifest(version, files, zipBytes)
	return appendOverlay(t, nil, zipBytes, manifest)
}

func validManifest(version string, files map[string][]byte, zipBytes []byte) Manifest {
	entries := make([]Entry, 0, len(files))
	for path, body := range files {
		entries = append(entries, Entry{Path: path, Size: int64(len(body)), SHA256: sha256Hex(body)})
	}
	sortEntries(entries)
	return Manifest{
		SchemaVersion: SchemaVersion,
		AppVersion:    version,
		PayloadSHA256: sha256Hex(zipBytes),
		Entries:       entries,
	}
}

func appendOverlay(t *testing.T, pe, zipBytes []byte, manifest Manifest) []byte {
	t.Helper()
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return appendOverlayBytes(t, pe, zipBytes, raw)
}

func appendOverlayBytes(t *testing.T, pe, zipBytes, manifestJSON []byte) []byte {
	t.Helper()
	out := append([]byte{}, pe...)
	out = append(out, zipBytes...)
	out = append(out, manifestJSON...)
	trailer := make([]byte, trailerSize)
	copy(trailer[:16], []byte(trailerMagic))
	binary.LittleEndian.PutUint64(trailer[16:24], uint64(len(zipBytes)))
	binary.LittleEndian.PutUint64(trailer[24:32], uint64(len(manifestJSON)))
	return append(out, trailer...)
}

func mustZip(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sortEntriesByName(names)
	for _, name := range names {
		writer, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(files[name]); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sortEntries(entries []Entry) {
	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			if entries[j].Path < entries[i].Path {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}
}

func sortEntriesByName(names []string) {
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			if names[j] < names[i] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
}
