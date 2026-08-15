package catalogbuilder

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportPackOmitsOriginalsAndSecrets(t *testing.T) {
	root := t.TempDir()
	seedAnalyzedMovie(t, root, "pack-movie", "embed-a")
	zipPath := filepath.Join(t.TempDir(), "out", "catalog-pack.zip")
	if err := ExportPack(context.Background(), root, zipPath, "vision-x"); err != nil {
		t.Fatal(err)
	}
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	names := map[string]bool{}
	for _, file := range reader.File {
		name := filepath.ToSlash(file.Name)
		names[name] = true
		if strings.Contains(name, "originals/") {
			t.Fatalf("pack contains original %q", name)
		}
		if strings.Contains(strings.ToLower(name), "api_key") {
			t.Fatalf("pack contains secret-looking name %q", name)
		}
	}
	if !names[packManifestName] || !names[packCatalogName] {
		t.Fatalf("pack names=%v", names)
	}
	foundFrame := false
	for name := range names {
		if strings.HasPrefix(name, packKeyframePrefix) && strings.HasSuffix(name, ".jpg") {
			foundFrame = true
		}
	}
	if !foundFrame {
		t.Fatalf("pack missing keyframes: %v", names)
	}
	extract := t.TempDir()
	manifest, err := extractPack(zipPath, extract)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Format != packFormatName || manifest.EmbeddingModel != "embed-a" || manifest.EmbeddingDimension != 3 {
		t.Fatalf("manifest=%+v", manifest)
	}
	if manifest.VisionModel != "vision-x" || manifest.AnalysisVersion == "" {
		t.Fatalf("manifest=%+v", manifest)
	}
}

func TestExtractPackRejectsUnsafeEntries(t *testing.T) {
	for _, name := range []string{"../escape.db", "originals/movies/x.mp4", "C:/windows/catalog.db", "secrets.json"} {
		zipPath := writeMaliciousZip(t, name)
		if _, err := extractPack(zipPath, t.TempDir()); err == nil {
			t.Fatalf("accepted unsafe pack entry %q", name)
		}
	}
}

func writeMaliciousZip(t *testing.T, name string) string {
	t.Helper()
	zipPath := filepath.Join(t.TempDir(), "bad.zip")
	file, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for _, entry := range []string{packManifestName, packCatalogName, name} {
		part, err := writer.Create(entry)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte("{}")); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return zipPath
}
