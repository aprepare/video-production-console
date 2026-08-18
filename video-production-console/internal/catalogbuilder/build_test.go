package catalogbuilder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"video-production-console/internal/mediacatalog"
)

func TestBuildFailsWhenNoMediaFound(t *testing.T) {
	root := t.TempDir()
	_, err := Build(context.Background(), Config{MediaRoot: root})
	if !errors.Is(err, errNoMedia) {
		t.Fatalf("got %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "originals") {
		t.Fatalf("error should mention the movie directory: %v", err)
	}
}

func TestBuildIndexesImagesWithoutFFmpegOrVision(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "originals", "images"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "originals", "images", "still.png"), []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	summary, err := Build(context.Background(), Config{MediaRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Phase != "ready" || summary.Index.NewSources != 1 {
		t.Fatalf("summary=%+v", summary)
	}
}

func TestBuildRequiresFFmpegWhenVideosNeedProbe(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "originals", "movies"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "originals", "movies", "clip.mp4"), []byte("movie-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Build(context.Background(), Config{MediaRoot: root})
	if !errors.Is(err, errFFmpegRequired) {
		t.Fatalf("got %v", err)
	}
}

func TestBuildRequiresVisionWhenKeyframesAwaitAnalysis(t *testing.T) {
	root := t.TempDir()
	seedPendingMovie(t, root, "need-vision")
	_, err := Build(context.Background(), Config{MediaRoot: root})
	if !errors.Is(err, errVisionRequired) {
		t.Fatalf("got %v", err)
	}
	if err != nil && strings.Contains(err.Error(), "sk-") {
		t.Fatalf("error leaked a key-like value: %v", err)
	}
}

func TestBuildAnalyzesPendingKeyframesThroughCompatEndpoints(t *testing.T) {
	root := t.TempDir()
	seedPendingMovie(t, root, "analyze-me")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/chat/completions"):
			payload, _ := json.Marshal(map[string]any{
				"choices": []map[string]any{{"message": map[string]any{"content": `{"summary":"a person reviews documents at a desk","mood":"calm","setting":"office","people_count":1,"motion_level":"low","has_text":false,"tags":[{"value":"desk","confidence":0.9}]}`}}},
			})
			_, _ = w.Write(payload)
		case strings.HasSuffix(r.URL.Path, "/embeddings"):
			payload, _ := json.Marshal(map[string]any{"data": []map[string]any{{"embedding": []float64{0.1, 0.2, 0.3}}}})
			_, _ = w.Write(payload)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	summary, err := Build(context.Background(), Config{
		MediaRoot:        root,
		VisionBaseURL:    server.URL,
		VisionModel:      "vision-test",
		EmbeddingBaseURL: server.URL,
		EmbeddingModel:   "embed-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Phase != "ready" || summary.Analysis.AnalyzedShots != 1 {
		t.Fatalf("summary=%+v", summary)
	}
	repo, err := mediacatalog.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	sources, err := repo.ListSources(context.Background())
	if err != nil || len(sources) != 1 {
		t.Fatalf("sources=%d err=%v", len(sources), err)
	}
	shots, err := repo.ShotsBySource(context.Background(), sources[0].ID)
	if err != nil || len(shots) != 1 || shots[0].AnalysisStatus != mediacatalog.AnalysisCompleted {
		t.Fatalf("shots=%+v err=%v", shots, err)
	}
	if !bytes.Contains([]byte(shots[0].Summary), []byte("desk")) {
		t.Fatalf("summary=%q", shots[0].Summary)
	}
}

func TestBuildDoesNotReportReadyWhenEveryShotFailsAnalysis(t *testing.T) {
	root := t.TempDir()
	seedPendingMovie(t, root, "all-fail")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	summary, err := Build(context.Background(), Config{
		MediaRoot:        root,
		VisionBaseURL:    server.URL,
		VisionModel:      "vision-test",
		EmbeddingBaseURL: server.URL,
		EmbeddingModel:   "embed-test",
	})
	if !errors.Is(err, errBuildFailed) {
		t.Fatalf("got %v", err)
	}
	if summary.Phase == "ready" {
		t.Fatalf("phase=%q, do not report ready after total analysis failure", summary.Phase)
	}
}
