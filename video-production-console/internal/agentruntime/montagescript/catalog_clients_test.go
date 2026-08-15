package montagescript

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"video-production-console/internal/agentruntime/montageplan"
)

func TestAttachCatalogClientsReadsManifestAndEnv(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "task_manifest.json")
	payload, _ := json.Marshal(map[string]any{
		"skill": "jianying-movie-montage",
		"non_secret_settings": map[string]any{
			"media_catalog_path": filepath.Join(dir, "catalog.db"),
			"embedding_base_url": "https://api.example.test/v1",
			"embedding_model":    "embed-x",
		},
	})
	if err := os.WriteFile(manifestPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envEmbeddingAPIKey, "embed-secret")
	opts := Options{ManifestPath: manifestPath}
	attachCatalogClients(&opts)
	if opts.CatalogPath != filepath.Join(dir, "catalog.db") {
		t.Fatalf("catalog path=%q", opts.CatalogPath)
	}
	if opts.Embedder == nil {
		t.Fatal("expected embedder from manifest + env")
	}
	if opts.Analyzer == nil {
		t.Fatal("expected analyzer")
	}
	if _, ok := opts.Analyzer.(montageplan.HTTPIntentAnalyzer); !ok {
		if _, ok := opts.Analyzer.(montageplan.LocalIntentAnalyzer); !ok {
			t.Fatalf("analyzer type %T", opts.Analyzer)
		}
	}
}
