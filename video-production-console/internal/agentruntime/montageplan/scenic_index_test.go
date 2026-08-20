package montageplan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteScenicMediaIndexReusesPackIndex(t *testing.T) {
	root := t.TempDir()
	clip := filepath.Join(root, "originals", "broll", "lake.mp4")
	if err := os.MkdirAll(filepath.Dir(clip), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(clip, []byte("mp4"), 0o600); err != nil {
		t.Fatal(err)
	}
	packIndex := filepath.Join(root, "00_INDEX", "media_index.json")
	if err := os.MkdirAll(filepath.Dir(packIndex), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(packIndex, []byte(`[{"id":"lake","category":"Nature_Landscape","relative_path":"originals/broll/lake.mp4","duration_seconds":20}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "index.json")
	if err := WriteScenicMediaIndex(root, dest, ""); err != nil {
		t.Fatal(err)
	}
	if err := ValidateMediaLibrary(dest, root, ""); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	var items []mediaItem
	if err := json.Unmarshal(raw, &items); err != nil || len(items) != 1 || items[0].ID != "lake" {
		t.Fatalf("items=%s", raw)
	}
}

func TestWriteScenicMediaIndexScansImagesWithoutProbe(t *testing.T) {
	root := t.TempDir()
	image := filepath.Join(root, "originals", "images", "sky.jpg")
	if err := os.MkdirAll(filepath.Dir(image), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(image, []byte("jpg"), 0o600); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "index.json")
	if err := WriteScenicMediaIndex(root, dest, ""); err != nil {
		t.Fatal(err)
	}
	if err := ValidateMediaLibrary(dest, root, ""); err != nil {
		t.Fatal(err)
	}
}

func TestWriteScenicMediaIndexScansCategoryFolders(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "originals", "broll"), 0o755); err != nil {
		t.Fatal(err)
	}
	image := filepath.Join(root, "01_Nature_Landscape", "lake.jpg")
	if err := os.MkdirAll(filepath.Dir(image), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(image, []byte("jpg"), 0o600); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "index.json")
	if err := WriteScenicMediaIndex(root, dest, ""); err != nil {
		t.Fatal(err)
	}
	if err := ValidateMediaLibrary(dest, root, ""); err != nil {
		t.Fatal(err)
	}
}

func TestWriteScenicMediaIndexDropsCityAndFinanceFromPackIndex(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{
		"01_Nature_Landscape/lake.mp4",
		"02_City_Traffic/skyline.mp4",
		"04_Finance_Business/office.mp4",
	} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("mp4"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	packIndex := filepath.Join(root, "00_INDEX", "media_index.json")
	if err := os.MkdirAll(filepath.Dir(packIndex), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := `[
	  {"id":"lake","category":"Nature_Landscape","relative_path":"01_Nature_Landscape/lake.mp4","duration_seconds":20},
	  {"id":"city","category":"City_Traffic","relative_path":"02_City_Traffic/skyline.mp4","duration_seconds":20},
	  {"id":"office","category":"Finance_Business","relative_path":"04_Finance_Business/office.mp4","duration_seconds":20}
	]`
	if err := os.WriteFile(packIndex, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "index.json")
	if err := WriteScenicMediaIndex(root, dest, ""); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	var items []mediaItem
	if err := json.Unmarshal(raw, &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "lake" {
		t.Fatalf("items=%s", raw)
	}
}

func TestPruneScenicMediaIndexDropsNonLandscapeRows(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "index.json")
	payload := `[
	  {"id":"lake","category":"Nature_Landscape","relative_path":"lake.mp4","duration_seconds":20},
	  {"id":"city","category":"City_Traffic","relative_path":"city.mp4","duration_seconds":20}
	]`
	if err := os.WriteFile(dest, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := PruneScenicMediaIndex(dest); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	var items []mediaItem
	if err := json.Unmarshal(raw, &items); err != nil || len(items) != 1 || items[0].ID != "lake" {
		t.Fatalf("items=%s", raw)
	}
}

func TestWriteScenicMediaIndexRejectsEmptyPack(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "originals", "broll"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteScenicMediaIndex(root, filepath.Join(t.TempDir(), "index.json"), ""); err == nil {
		t.Fatal("empty originals must fail")
	}
}
