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

func TestAttachCatalogClientsScenicFollowsNarration(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "task_manifest.json")
	payload, _ := json.Marshal(map[string]any{
		"skill": "jianying-montage-draft",
		"non_secret_settings": map[string]any{
			"media_catalog_path": filepath.Join(dir, "catalog.db"),
		},
	})
	if err := os.WriteFile(manifestPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	opts := Options{ManifestPath: manifestPath}
	attachCatalogClients(&opts)
	local, ok := opts.Analyzer.(montageplan.LocalIntentAnalyzer)
	if !ok {
		t.Fatalf("analyzer type %T", opts.Analyzer)
	}
	if local.RestrictToLandscape {
		t.Fatal("scenic montage must follow narration, not restrict to landscape")
	}
}

func TestAttachCatalogClientsLeavesLineBreakerNilWhileLLMDisabled(t *testing.T) {
	if montageplan.CaptionLLMLineBreakerEnabled {
		t.Skip("caption LLM line breaker is on")
	}
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "task_manifest.json")
	payload, _ := json.Marshal(map[string]any{
		"skill": "jianying-montage-draft",
		"non_secret_settings": map[string]any{
			"remix_base_url":         "http://127.0.0.1:4100/v1",
			"remix_model":            "grok-4.6",
			"remix_reasoning_effort": "medium",
			"grok_base_url":          "http://23.138.12.112:2001/v1",
			"grok_model":             "gpt-5.6-sol",
		},
	})
	if err := os.WriteFile(manifestPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envIntentBaseURL, "")
	t.Setenv(envIntentModel, "")
	t.Setenv(envIntentAPIKey, "")
	t.Setenv(envIntentReasoningEffort, "")
	opts := Options{ManifestPath: manifestPath}
	attachCatalogClients(&opts)
	if opts.LineBreaker != nil {
		t.Fatalf("line breaker must stay nil while caption LLM is disabled, got %T", opts.LineBreaker)
	}
}

func TestAttachCatalogClientsPrefersEnvReasoningEffort(t *testing.T) {
	if !montageplan.CaptionLLMLineBreakerEnabled {
		t.Skip("caption LLM line breaker is disabled")
	}
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "task_manifest.json")
	payload, _ := json.Marshal(map[string]any{
		"skill": "jianying-montage-draft",
		"non_secret_settings": map[string]any{
			"remix_base_url":         "http://127.0.0.1:4100/v1",
			"remix_model":            "grok-4.6",
			"remix_reasoning_effort": "medium",
		},
	})
	if err := os.WriteFile(manifestPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envIntentReasoningEffort, "low")
	opts := Options{ManifestPath: manifestPath}
	attachCatalogClients(&opts)
	effort, ok := montageplan.HTTPLineBreakerReasoningEffort(opts.LineBreaker)
	if !ok || effort != "low" {
		t.Fatalf("effort = %q ok=%v", effort, ok)
	}
}
