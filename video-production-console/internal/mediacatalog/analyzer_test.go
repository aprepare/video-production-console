package mediacatalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testAnalysisContent = `{"summary":"a person reviews documents at a desk","mood":"calm","setting":"office","people_count":1,"motion_level":"low","has_text":false,"tags":[{"value":"desk","confidence":0.9},{"value":"paper","confidence":0.7}]}`

func makeTestJPEGBytes(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for x := 0; x < width; x++ {
		for y := 0; y < height; y++ {
			img.Set(x, y, color.RGBA{R: uint8((x + y) % 256), G: 120, B: 40, A: 255})
		}
	}
	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// analyzerServer is one httptest server for /chat/completions and /embeddings
// with scripted statuses and full request capture.
type analyzerServer struct {
	mu             sync.Mutex
	visionBodies   []string
	visionAuth     []string
	visionStatuses []int // consumed per request; empty means always 200
	visionContent  string
	embedBodies    []string
	embedVectors   [][]float64
	server         *httptest.Server
}

func newAnalyzerServer(t *testing.T) *analyzerServer {
	t.Helper()
	s := &analyzerServer{visionContent: testAnalysisContent, embedVectors: [][]float64{{0.1, 0.2, 0.3}}}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := new(bytes.Buffer)
		_, _ = body.ReadFrom(r.Body)
		s.mu.Lock()
		defer s.mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/chat/completions"):
			s.visionBodies = append(s.visionBodies, body.String())
			s.visionAuth = append(s.visionAuth, r.Header.Get("Authorization"))
			if len(s.visionStatuses) > 0 {
				status := s.visionStatuses[0]
				s.visionStatuses = s.visionStatuses[1:]
				if status != http.StatusOK {
					w.WriteHeader(status)
					return
				}
			}
			payload, _ := json.Marshal(map[string]any{
				"choices": []map[string]any{{"message": map[string]any{"content": s.visionContent}}},
			})
			_, _ = w.Write(payload)
		case strings.HasSuffix(r.URL.Path, "/embeddings"):
			s.embedBodies = append(s.embedBodies, body.String())
			vector := s.embedVectors[0]
			if len(s.embedVectors) > 1 {
				s.embedVectors = s.embedVectors[1:]
			}
			payload, _ := json.Marshal(map[string]any{"data": []map[string]any{{"embedding": vector}}})
			_, _ = w.Write(payload)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(s.server.Close)
	return s
}

func (s *analyzerServer) visionRequestCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.visionBodies)
}

func (s *analyzerServer) embedRequestCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.embedBodies)
}

func newTestVisionAnalyzer(t *testing.T, s *analyzerServer, sleeps *[]time.Duration) *HTTPVisionAnalyzer {
	t.Helper()
	analyzer, err := NewHTTPVisionAnalyzer(VisionConfig{
		BaseURL: s.server.URL, Model: "test-vision-model", APIKey: "test-vision-key",
		Sleep: func(d time.Duration) {
			if sleeps != nil {
				*sleeps = append(*sleeps, d)
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return analyzer
}

func TestVisionRequestContainsOnlyKeyframeDataURLsAndFixedPrompt(t *testing.T) {
	server := newAnalyzerServer(t)
	analyzer := newTestVisionAnalyzer(t, server, nil)

	keyframes := []KeyframeInput{
		{JPEG: makeTestJPEGBytes(t, 320, 180), Width: 320, Height: 180},
		{JPEG: makeTestJPEGBytes(t, 512, 288), Width: 512, Height: 288},
	}
	analysis, err := analyzer.Analyze(context.Background(), keyframes)
	if err != nil {
		t.Fatal(err)
	}
	if analysis.Summary != "a person reviews documents at a desk" || analysis.PeopleCount != 1 || analysis.MotionLevel != "low" || len(analysis.Tags) != 2 {
		t.Fatalf("unexpected analysis: %+v", analysis)
	}

	if server.visionRequestCount() != 1 {
		t.Fatalf("expected one request, got %d", server.visionRequestCount())
	}
	body := server.visionBodies[0]
	if got := strings.Count(body, "data:image/jpeg;base64,"); got != 2 {
		t.Fatalf("request must contain exactly the 2 keyframe data URLs, found %d", got)
	}
	for _, forbidden := range []string{".mp4", "C:\\\\", "originals/movies"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("request body leaked %q", forbidden)
		}
	}
	if !strings.Contains(body, "Never identify") || !strings.Contains(body, "test-vision-model") {
		t.Fatal("request must carry the fixed prompt and the configured model")
	}
	if server.visionAuth[0] != "Bearer test-vision-key" {
		t.Fatalf("API key must travel only in the Authorization header, got %q", server.visionAuth[0])
	}
}

func TestVisionRejectsWrongKeyframeCountAndOversizedFrames(t *testing.T) {
	server := newAnalyzerServer(t)
	analyzer := newTestVisionAnalyzer(t, server, nil)
	frame := KeyframeInput{JPEG: makeTestJPEGBytes(t, 100, 60), Width: 100, Height: 60}

	if _, err := analyzer.Analyze(context.Background(), []KeyframeInput{frame}); err == nil {
		t.Fatal("one keyframe must be rejected (contract is 2-3)")
	}
	if _, err := analyzer.Analyze(context.Background(), []KeyframeInput{frame, frame, frame, frame}); err == nil {
		t.Fatal("four keyframes must be rejected (contract is 2-3)")
	}
	oversized := KeyframeInput{JPEG: makeTestJPEGBytes(t, 600, 400), Width: 600, Height: 400}
	if _, err := analyzer.Analyze(context.Background(), []KeyframeInput{frame, oversized}); err == nil {
		t.Fatal("keyframes above the 512px edge cap must be rejected")
	}
	if server.visionRequestCount() != 0 {
		t.Fatalf("invalid inputs must never reach the network, got %d requests", server.visionRequestCount())
	}
}

func TestVisionStrictJSONRejectsUnknownFieldsAndMalformedPayloads(t *testing.T) {
	server := newAnalyzerServer(t)
	analyzer := newTestVisionAnalyzer(t, server, nil)
	frames := []KeyframeInput{
		{JPEG: makeTestJPEGBytes(t, 100, 60), Width: 100, Height: 60},
		{JPEG: makeTestJPEGBytes(t, 100, 60), Width: 100, Height: 60},
	}

	server.visionContent = strings.Replace(testAnalysisContent, `"summary"`, `"unknown_field":1,"summary"`, 1)
	if _, err := analyzer.Analyze(context.Background(), frames); err == nil {
		t.Fatal("unknown response fields must be rejected")
	}
	server.visionContent = "not json at all"
	if _, err := analyzer.Analyze(context.Background(), frames); err == nil {
		t.Fatal("malformed response JSON must be rejected")
	}
	server.visionContent = strings.Replace(testAnalysisContent, `"low"`, `"warp-speed"`, 1)
	if _, err := analyzer.Analyze(context.Background(), frames); err == nil {
		t.Fatal("invalid motion_level must be rejected")
	}
}

func TestVisionRetriesOn429ThenSucceedsWithInjectedBackoff(t *testing.T) {
	server := newAnalyzerServer(t)
	server.visionStatuses = []int{429, 429, 200}
	var sleeps []time.Duration
	analyzer := newTestVisionAnalyzer(t, server, &sleeps)
	frames := []KeyframeInput{
		{JPEG: makeTestJPEGBytes(t, 100, 60), Width: 100, Height: 60},
		{JPEG: makeTestJPEGBytes(t, 100, 60), Width: 100, Height: 60},
	}

	if _, err := analyzer.Analyze(context.Background(), frames); err != nil {
		t.Fatal(err)
	}
	if server.visionRequestCount() != 3 {
		t.Fatalf("expected 3 requests (2 retries), got %d", server.visionRequestCount())
	}
	want := []time.Duration{time.Second, 2 * time.Second}
	if len(sleeps) != len(want) || sleeps[0] != want[0] || sleeps[1] != want[1] {
		t.Fatalf("backoff mismatch: got %v want %v", sleeps, want)
	}
}

func TestVisionExhaustsRetriesOnServerErrors(t *testing.T) {
	server := newAnalyzerServer(t)
	server.visionStatuses = []int{500, 502, 503, 500, 500}
	var sleeps []time.Duration
	analyzer := newTestVisionAnalyzer(t, server, &sleeps)
	frames := []KeyframeInput{
		{JPEG: makeTestJPEGBytes(t, 100, 60), Width: 100, Height: 60},
		{JPEG: makeTestJPEGBytes(t, 100, 60), Width: 100, Height: 60},
	}

	_, err := analyzer.Analyze(context.Background(), frames)
	if err == nil || !strings.Contains(err.Error(), "status 500") {
		t.Fatalf("expected exhaustion error carrying the status, got %v", err)
	}
	if server.visionRequestCount() != 4 {
		t.Fatalf("expected 1 attempt + 3 retries, got %d requests", server.visionRequestCount())
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
	if fmt.Sprint(sleeps) != fmt.Sprint(want) {
		t.Fatalf("backoff mismatch: got %v want %v", sleeps, want)
	}
}

func TestVisionDoesNotRetryContractErrors(t *testing.T) {
	server := newAnalyzerServer(t)
	server.visionStatuses = []int{400}
	var sleeps []time.Duration
	analyzer := newTestVisionAnalyzer(t, server, &sleeps)
	frames := []KeyframeInput{
		{JPEG: makeTestJPEGBytes(t, 100, 60), Width: 100, Height: 60},
		{JPEG: makeTestJPEGBytes(t, 100, 60), Width: 100, Height: 60},
	}

	if _, err := analyzer.Analyze(context.Background(), frames); err == nil {
		t.Fatal("4xx contract errors must fail")
	}
	if server.visionRequestCount() != 1 || len(sleeps) != 0 {
		t.Fatalf("4xx must not retry: %d requests, %v sleeps", server.visionRequestCount(), sleeps)
	}
}

func TestEmbedderParsesVectorAndRequiresConfiguration(t *testing.T) {
	server := newAnalyzerServer(t)
	embedder, err := NewHTTPEmbedder(EmbedderConfig{BaseURL: server.server.URL, Model: "test-embed-model", APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	vector, err := embedder.Embed(context.Background(), "summary: x")
	if err != nil {
		t.Fatal(err)
	}
	if len(vector) != 3 || vector[0] != float32(0.1) {
		t.Fatalf("unexpected vector %v", vector)
	}
	if _, err := NewHTTPEmbedder(EmbedderConfig{}); !errors.Is(err, ErrEmbeddingNotConfigured) {
		t.Fatalf("expected ErrEmbeddingNotConfigured, got %v", err)
	}
	if _, err := NewHTTPVisionAnalyzer(VisionConfig{}); !errors.Is(err, ErrVisionNotConfigured) {
		t.Fatalf("expected ErrVisionNotConfigured, got %v", err)
	}
}

// seedAnalyzableShot creates a ready video source with one shot and two real
// keyframe files/rows inside the media root.
func seedAnalyzableShot(t *testing.T, repo *Repository, seed string, window SceneBoundary) Shot {
	t.Helper()
	ctx := context.Background()
	source := testVideoSource(seed)
	source.Status = SourceStatusReady
	stored, _, err := repo.UpsertSource(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	shot, err := repo.InsertShot(ctx, Shot{SourceID: stored.ID, Ordinal: 0, SourceInMS: window.InMS, SourceOutMS: window.OutMS})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		relative := fmt.Sprintf("derived/keyframes/%s/%s/%02d.jpg", stored.SHA256, shot.ID, i+1)
		absolute := filepath.Join(repo.Root(), filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			t.Fatal(err)
		}
		data := makeTestJPEGBytes(t, 100+i, 60)
		if err := os.WriteFile(absolute, data, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.InsertKeyframe(ctx, Keyframe{
			ShotID: shot.ID, Ordinal: i, RelativePath: relative,
			AtMS: shot.SourceInMS + int64(i)*1000, Width: 100 + i, Height: 60, SHA256: sha256Hex(data),
		}); err != nil {
			t.Fatal(err)
		}
	}
	return shot
}

func TestAnalysisRunnerPersistsAnalysisTagsAndEmbeddingIdempotently(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepository(t)
	shot := seedAnalyzableShot(t, repo, "runner-source", SceneBoundary{InMS: 0, OutMS: 4000})
	server := newAnalyzerServer(t)
	analyzer := newTestVisionAnalyzer(t, server, nil)
	embedder, err := NewHTTPEmbedder(EmbedderConfig{BaseURL: server.server.URL, Model: "test-embed-model"})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewAnalysisRunner(repo, analyzer, embedder, "test-embed-model")
	if err != nil {
		t.Fatal(err)
	}

	summary, err := runner.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if summary.AnalyzedShots != 1 || summary.FailedShots != 0 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	shots, err := repo.ShotsBySource(ctx, shot.SourceID)
	if err != nil {
		t.Fatal(err)
	}
	stored := shots[0]
	if stored.AnalysisStatus != AnalysisCompleted || stored.AnalysisVersion != AnalysisVersion {
		t.Fatalf("shot must be completed at %s, got %+v", AnalysisVersion, stored)
	}
	if stored.Summary != "a person reviews documents at a desk" || stored.Mood != "calm" || stored.Setting != "office" || stored.MotionLevel != "low" || stored.PeopleCount != 1 || stored.HasText {
		t.Fatalf("analysis fields were not persisted: %+v", stored)
	}
	tags, err := repo.TagsByShot(ctx, shot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 2 || tags[0].Namespace != visionTagNamespace {
		t.Fatalf("expected 2 vision tags, got %+v", tags)
	}
	embedding, err := repo.ShotEmbedding(ctx, shot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if embedding.Model != "test-embed-model" || embedding.Dimension != 3 || embedding.AnalysisVersion != AnalysisVersion {
		t.Fatalf("embedding metadata mismatch: %+v", embedding)
	}
	if embedding.Vector[1] != float32(0.2) {
		t.Fatalf("embedding vector mismatch: %v", embedding.Vector)
	}
	// The embedding request must use the fixed template text.
	if !strings.Contains(server.embedBodies[0], "summary: a person reviews documents at a desk") {
		t.Fatalf("embedding input must come from the fixed template, got %s", server.embedBodies[0])
	}

	// Re-running the same source hash + analysis version issues zero requests.
	visionBefore, embedBefore := server.visionRequestCount(), server.embedRequestCount()
	summary, err = runner.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if summary.AnalyzedShots != 0 || summary.FailedShots != 0 || summary.SkippedShots != 0 {
		t.Fatalf("idempotent rerun must do nothing, got %+v", summary)
	}
	if server.visionRequestCount() != visionBefore || server.embedRequestCount() != embedBefore {
		t.Fatal("idempotent rerun must perform zero network requests")
	}
}

func TestAnalysisRunnerRejectsEmbeddingDimensionChanges(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepository(t)
	first := seedAnalyzableShot(t, repo, "dim-source-a", SceneBoundary{InMS: 0, OutMS: 4000})
	second := seedAnalyzableShot(t, repo, "dim-source-b", SceneBoundary{InMS: 0, OutMS: 4000})
	server := newAnalyzerServer(t)
	server.embedVectors = [][]float64{{0.1, 0.2, 0.3}, {0.1, 0.2, 0.3, 0.4}}
	analyzer := newTestVisionAnalyzer(t, server, nil)
	embedder, err := NewHTTPEmbedder(EmbedderConfig{BaseURL: server.server.URL, Model: "test-embed-model"})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewAnalysisRunner(repo, analyzer, embedder, "test-embed-model")
	if err != nil {
		t.Fatal(err)
	}

	summary, err := runner.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if summary.AnalyzedShots != 1 || summary.FailedShots != 1 {
		t.Fatalf("dimension change must fail exactly one shot, got %+v", summary)
	}
	var failed Shot
	for _, sourceShot := range []Shot{first, second} {
		shots, err := repo.ShotsBySource(ctx, sourceShot.SourceID)
		if err != nil {
			t.Fatal(err)
		}
		if shots[0].AnalysisStatus == AnalysisFailed {
			failed = shots[0]
		}
	}
	if failed.ID == "" {
		t.Fatal("the mismatching shot must be marked failed for standalone retry")
	}
	embedding, err := repo.ShotEmbedding(ctx, failed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if embedding.Dimension != 0 {
		t.Fatalf("no vector may be stored for the failed shot, got %+v", embedding)
	}
}

func TestAnalysisRunnerSkipsShotsWithoutKeyframes(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepository(t)
	source, _, err := repo.UpsertSource(ctx, testImageSource("image-no-frames"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.EnsureImageShot(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	server := newAnalyzerServer(t)
	analyzer := newTestVisionAnalyzer(t, server, nil)
	embedder, err := NewHTTPEmbedder(EmbedderConfig{BaseURL: server.server.URL, Model: "test-embed-model"})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewAnalysisRunner(repo, analyzer, embedder, "test-embed-model")
	if err != nil {
		t.Fatal(err)
	}

	summary, err := runner.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if summary.SkippedShots != 1 || summary.AnalyzedShots != 0 || summary.FailedShots != 0 {
		t.Fatalf("keyframe-less shots must be skipped, got %+v", summary)
	}
	if server.visionRequestCount() != 0 || server.embedRequestCount() != 0 {
		t.Fatal("skipped shots must not reach the network")
	}
}

func TestAnalysisRunnerRejectsTamperedKeyframes(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepository(t)
	shot := seedAnalyzableShot(t, repo, "tamper-source", SceneBoundary{InMS: 0, OutMS: 4000})
	keyframes, err := repo.KeyframesByShot(ctx, shot.ID)
	if err != nil {
		t.Fatal(err)
	}
	absolute, err := repo.ResolvePath(keyframes[0].RelativePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolute, makeTestJPEGBytes(t, 90, 50), 0o644); err != nil {
		t.Fatal(err)
	}
	server := newAnalyzerServer(t)
	analyzer := newTestVisionAnalyzer(t, server, nil)
	embedder, err := NewHTTPEmbedder(EmbedderConfig{BaseURL: server.server.URL, Model: "test-embed-model"})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewAnalysisRunner(repo, analyzer, embedder, "test-embed-model")
	if err != nil {
		t.Fatal(err)
	}

	summary, err := runner.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if summary.FailedShots != 1 {
		t.Fatalf("checksum mismatch must fail the shot, got %+v", summary)
	}
	if server.visionRequestCount() != 0 {
		t.Fatal("tampered keyframes must never be uploaded")
	}
}

type concurrentVision struct {
	inflight atomic.Int32
	peak     atomic.Int32
}

func (v *concurrentVision) Analyze(ctx context.Context, keyframes []KeyframeInput) (ShotAnalysis, error) {
	current := v.inflight.Add(1)
	for {
		peak := v.peak.Load()
		if current <= peak || v.peak.CompareAndSwap(peak, current) {
			break
		}
	}
	time.Sleep(80 * time.Millisecond)
	v.inflight.Add(-1)
	return ShotAnalysis{
		Summary: "a person reviews documents at a desk", Mood: "calm", Setting: "office",
		PeopleCount: 1, MotionLevel: "low",
	}, nil
}

type staticEmbedder struct{}

func (staticEmbedder) Embed(context.Context, string) ([]float32, error) {
	return []float32{0.1, 0.2, 0.3}, nil
}

func TestClampAnalysisConcurrency(t *testing.T) {
	if got := ClampAnalysisConcurrency(0); got != DefaultAnalysisConcurrency {
		t.Fatalf("zero=%d", got)
	}
	if got := ClampAnalysisConcurrency(1000); got != 1000 {
		t.Fatalf("1000=%d", got)
	}
	if got := ClampAnalysisConcurrency(1001); got != MaxAnalysisConcurrency {
		t.Fatalf("over max=%d", got)
	}
}

func TestAnalysisRunnerRunsShotsConcurrently(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepository(t)
	for i := 0; i < 4; i++ {
		seedAnalyzableShot(t, repo, fmt.Sprintf("parallel-%d", i), SceneBoundary{InMS: 0, OutMS: 4000})
	}
	vision := &concurrentVision{}
	runner, err := NewAnalysisRunnerWithConcurrency(repo, vision, staticEmbedder{}, "test-embed-model", 4)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	summary, err := runner.Run(ctx)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatal(err)
	}
	if summary.AnalyzedShots != 4 || summary.FailedShots != 0 {
		t.Fatalf("summary=%+v", summary)
	}
	if vision.peak.Load() < 3 {
		t.Fatalf("peak concurrency=%d, want at least 3", vision.peak.Load())
	}
	if elapsed > 300*time.Millisecond {
		t.Fatalf("elapsed %s, concurrent analysis should finish near one shot latency", elapsed)
	}
}
