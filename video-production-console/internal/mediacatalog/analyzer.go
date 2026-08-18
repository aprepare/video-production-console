package mediacatalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

var (
	// ErrVisionNotConfigured is returned when vision endpoint/model settings
	// are missing.
	ErrVisionNotConfigured = errors.New("vision_not_configured: vision base URL and model must be provided via settings")
	// ErrEmbeddingNotConfigured is returned when embedding endpoint/model
	// settings are missing.
	ErrEmbeddingNotConfigured = errors.New("embedding_not_configured: embedding base URL and model must be provided via settings")
	// ErrEmbeddingDimensionChanged guards reproducibility: one embedding model
	// must keep one dimension across the catalog.
	ErrEmbeddingDimensionChanged = errors.New("embedding_dimension_changed: vector dimension differs from stored embeddings of the same model")
)

const (
	analyzerMaxRetries           = 3
	visionTagNamespace           = "vision"
	minShotKeyframes             = 2
	maxShotKeyframes             = 3
	DefaultAnalysisConcurrency   = 64
	MaxAnalysisConcurrency       = 1000
	analyzerHTTPTimeout          = 2 * time.Minute
)

func ClampAnalysisConcurrency(value int) int {
	if value < 1 {
		return DefaultAnalysisConcurrency
	}
	if value > MaxAnalysisConcurrency {
		return MaxAnalysisConcurrency
	}
	return value
}

func analyzerHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = MaxAnalysisConcurrency
	transport.MaxIdleConnsPerHost = MaxAnalysisConcurrency
	transport.MaxConnsPerHost = MaxAnalysisConcurrency
	return &http.Client{Timeout: analyzerHTTPTimeout, Transport: transport}
}

var analyzerBackoffs = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}

// KeyframeInput is one low-res JPEG keyframe handed to the vision model.
// Only these bytes (as data URLs) and fixed prompt text ever leave the
// machine; never movie paths, video bytes, or audio.
type KeyframeInput struct {
	JPEG   []byte
	Width  int
	Height int
}

// TagScore is one tag returned by the vision model.
type TagScore struct {
	Value      string  `json:"value"`
	Confidence float64 `json:"confidence"`
}

// ShotAnalysis is the strict JSON contract of the vision response.
type ShotAnalysis struct {
	Summary     string     `json:"summary"`
	Mood        string     `json:"mood"`
	Setting     string     `json:"setting"`
	PeopleCount int        `json:"people_count"`
	MotionLevel string     `json:"motion_level"`
	HasText     bool       `json:"has_text"`
	Tags        []TagScore `json:"tags"`
}

func (a ShotAnalysis) validate() error {
	if strings.TrimSpace(a.Summary) == "" {
		return fmt.Errorf("%w: analysis summary is required", ErrInvalidValue)
	}
	if a.PeopleCount < 0 {
		return fmt.Errorf("%w: people_count cannot be negative", ErrInvalidValue)
	}
	switch a.MotionLevel {
	case "static", "low", "medium", "high":
	default:
		return fmt.Errorf("%w: motion_level %q is not one of static/low/medium/high", ErrInvalidValue, a.MotionLevel)
	}
	for _, tag := range a.Tags {
		if strings.TrimSpace(tag.Value) == "" {
			return fmt.Errorf("%w: analysis tags need non-empty values", ErrInvalidValue)
		}
		if tag.Confidence < 0 || tag.Confidence > 1 {
			return fmt.Errorf("%w: tag confidence must stay within [0,1]", ErrInvalidValue)
		}
	}
	return nil
}

type VisionAnalyzer interface {
	Analyze(ctx context.Context, keyframes []KeyframeInput) (ShotAnalysis, error)
}

type Embedder interface {
	Embed(ctx context.Context, input string) ([]float32, error)
}

// VisionConfig configures the OpenAI-compatible multimodal client. The API
// key is injected by the caller (settings runtime) and is only ever placed in
// the Authorization header: never logged, never persisted.
type VisionConfig struct {
	BaseURL    string
	Model      string
	APIKey     string
	HTTPClient *http.Client
	Sleep      func(time.Duration) // injectable backoff clock; nil = time.Sleep
}

type HTTPVisionAnalyzer struct {
	baseURL string
	model   string
	apiKey  string
	client  retryingHTTPClient
}

func NewHTTPVisionAnalyzer(cfg VisionConfig) (*HTTPVisionAnalyzer, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" || strings.TrimSpace(cfg.Model) == "" {
		return nil, ErrVisionNotConfigured
	}
	return &HTTPVisionAnalyzer{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		model:   cfg.Model,
		apiKey:  cfg.APIKey,
		client:  newRetryingHTTPClient(cfg.HTTPClient, cfg.Sleep),
	}, nil
}

// Analyze submits exactly the shot's 2-3 keyframes as data URLs plus the
// fixed prompts, and strictly decodes the JSON response (unknown fields and
// malformed payloads are rejected).
func (v *HTTPVisionAnalyzer) Analyze(ctx context.Context, keyframes []KeyframeInput) (ShotAnalysis, error) {
	if len(keyframes) < minShotKeyframes || len(keyframes) > maxShotKeyframes {
		return ShotAnalysis{}, fmt.Errorf("%w: vision analysis takes %d-%d keyframes, got %d", ErrInvalidValue, minShotKeyframes, maxShotKeyframes, len(keyframes))
	}
	content := []map[string]any{{"type": "text", "text": visionUserInstruction}}
	for _, keyframe := range keyframes {
		if len(keyframe.JPEG) == 0 {
			return ShotAnalysis{}, fmt.Errorf("%w: keyframe bytes are required", ErrInvalidValue)
		}
		if keyframe.Width > KeyframeMaxEdge || keyframe.Height > KeyframeMaxEdge {
			return ShotAnalysis{}, fmt.Errorf("%w: keyframe %dx%d exceeds the %dpx edge cap", ErrInvalidValue, keyframe.Width, keyframe.Height, KeyframeMaxEdge)
		}
		dataURL := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(keyframe.JPEG)
		content = append(content, map[string]any{
			"type":      "image_url",
			"image_url": map[string]any{"url": dataURL},
		})
	}
	payload, err := json.Marshal(map[string]any{
		"model": v.model,
		"messages": []map[string]any{
			{"role": "system", "content": visionSystemPrompt},
			{"role": "user", "content": content},
		},
		"temperature":     0,
		"response_format": map[string]any{"type": "json_object"},
	})
	if err != nil {
		return ShotAnalysis{}, fmt.Errorf("encode vision request: %w", err)
	}
	body, err := v.client.postJSON(ctx, CompatEndpoint(v.baseURL, "chat/completions"), v.apiKey, payload)
	if err != nil {
		return ShotAnalysis{}, err
	}
	var envelope struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || len(envelope.Choices) == 0 {
		return ShotAnalysis{}, fmt.Errorf("%w: vision response envelope is malformed", ErrInvalidValue)
	}
	return decodeStrictShotAnalysis(envelope.Choices[0].Message.Content)
}

func decodeStrictShotAnalysis(content string) (ShotAnalysis, error) {
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.DisallowUnknownFields()
	var analysis ShotAnalysis
	if err := decoder.Decode(&analysis); err != nil {
		return ShotAnalysis{}, fmt.Errorf("%w: vision response is not strict ShotAnalysis JSON: %v", ErrInvalidValue, err)
	}
	if decoder.More() {
		return ShotAnalysis{}, fmt.Errorf("%w: vision response contains trailing data", ErrInvalidValue)
	}
	if err := analysis.validate(); err != nil {
		return ShotAnalysis{}, err
	}
	return analysis, nil
}

// EmbedderConfig configures the OpenAI-compatible embeddings client.
type EmbedderConfig struct {
	BaseURL    string
	Model      string
	APIKey     string
	HTTPClient *http.Client
	Sleep      func(time.Duration)
}

type HTTPEmbedder struct {
	baseURL string
	model   string
	apiKey  string
	client  retryingHTTPClient
}

func NewHTTPEmbedder(cfg EmbedderConfig) (*HTTPEmbedder, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" || strings.TrimSpace(cfg.Model) == "" {
		return nil, ErrEmbeddingNotConfigured
	}
	return &HTTPEmbedder{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		model:   cfg.Model,
		apiKey:  cfg.APIKey,
		client:  newRetryingHTTPClient(cfg.HTTPClient, cfg.Sleep),
	}, nil
}

// Model returns the configured embedding model name for persistence metadata.
func (e *HTTPEmbedder) Model() string { return e.model }

func (e *HTTPEmbedder) Embed(ctx context.Context, input string) ([]float32, error) {
	if strings.TrimSpace(input) == "" {
		return nil, fmt.Errorf("%w: embedding input is required", ErrInvalidValue)
	}
	payload, err := json.Marshal(map[string]any{"model": e.model, "input": input})
	if err != nil {
		return nil, fmt.Errorf("encode embedding request: %w", err)
	}
	body, err := e.client.postJSON(ctx, CompatEndpoint(e.baseURL, "embeddings"), e.apiKey, payload)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || len(envelope.Data) == 0 || len(envelope.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("%w: embedding response is malformed", ErrInvalidValue)
	}
	vector := make([]float32, len(envelope.Data[0].Embedding))
	for i, value := range envelope.Data[0].Embedding {
		vector[i] = float32(value)
	}
	return vector, nil
}

// retryingHTTPClient posts JSON with up to 3 retries on 429/5xx using a
// 1s/2s/4s backoff (clock injectable). Other 4xx contract errors never retry.
type retryingHTTPClient struct {
	client *http.Client
	sleep  func(time.Duration)
}

func newRetryingHTTPClient(client *http.Client, sleep func(time.Duration)) retryingHTTPClient {
	if client == nil {
		client = analyzerHTTPClient()
	}
	if sleep == nil {
		sleep = time.Sleep
	}
	return retryingHTTPClient{client: client, sleep: sleep}
}

func (c retryingHTTPClient) postJSON(ctx context.Context, url, apiKey string, payload []byte) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			return nil, fmt.Errorf("build analyzer request: %w", err)
		}
		request.Header.Set("Content-Type", "application/json")
		if apiKey != "" {
			request.Header.Set("Authorization", "Bearer "+apiKey)
		}
		response, err := c.client.Do(request)
		if err != nil {
			return nil, fmt.Errorf("analyzer request failed: %w", err)
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read analyzer response: %w", readErr)
		}
		if response.StatusCode == http.StatusOK {
			return body, nil
		}
		retryable := response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500
		if !retryable || attempt >= analyzerMaxRetries {
			return nil, fmt.Errorf("analyzer endpoint returned status %d", response.StatusCode)
		}
		c.sleep(analyzerBackoffs[attempt])
	}
}

// AnalysisRunner walks shots that still need analysis for AnalysisVersion,
// submits each shot independently (a failed shot can be retried alone), and
// persists analysis + tags + embedding. Re-running a fully analyzed catalog
// performs zero network requests.
type AnalysisRunner struct {
	repo           *Repository
	vision         VisionAnalyzer
	embedder       Embedder
	embeddingModel string
	concurrency    int
	persistMu      sync.Mutex
}

func NewAnalysisRunner(repo *Repository, vision VisionAnalyzer, embedder Embedder, embeddingModel string) (*AnalysisRunner, error) {
	return NewAnalysisRunnerWithConcurrency(repo, vision, embedder, embeddingModel, DefaultAnalysisConcurrency)
}

func NewAnalysisRunnerWithConcurrency(repo *Repository, vision VisionAnalyzer, embedder Embedder, embeddingModel string, concurrency int) (*AnalysisRunner, error) {
	if repo == nil || vision == nil || embedder == nil || strings.TrimSpace(embeddingModel) == "" {
		return nil, fmt.Errorf("%w: analysis runner requires repository, analyzer, embedder, and embedding model", ErrInvalidValue)
	}
	return &AnalysisRunner{
		repo: repo, vision: vision, embedder: embedder, embeddingModel: embeddingModel,
		concurrency: ClampAnalysisConcurrency(concurrency),
	}, nil
}

type AnalysisSummary struct {
	AnalyzedShots int
	SkippedShots  int
	FailedShots   int
}

func (a *AnalysisRunner) Run(ctx context.Context) (AnalysisSummary, error) {
	summary := AnalysisSummary{}
	shots, err := a.repo.ShotsPendingAnalysis(ctx, AnalysisVersion)
	if err != nil {
		return summary, err
	}
	workers := a.concurrency
	if workers < 1 {
		workers = DefaultAnalysisConcurrency
	}
	if len(shots) > 0 && workers > len(shots) {
		workers = len(shots)
	}
	jobs := make(chan Shot)
	var (
		mu         sync.Mutex
		firstFatal error
		wg         sync.WaitGroup
	)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for shot := range jobs {
				if ctx.Err() != nil {
					return
				}
				keyframes, err := a.repo.KeyframesByShot(ctx, shot.ID)
				if err != nil {
					mu.Lock()
					if firstFatal == nil {
						firstFatal = err
					}
					mu.Unlock()
					return
				}
				if len(keyframes) == 0 {
					mu.Lock()
					summary.SkippedShots++
					mu.Unlock()
					continue
				}
				if err := a.analyzeShot(ctx, shot, keyframes); err != nil {
					mu.Lock()
					summary.FailedShots++
					mu.Unlock()
					a.persistMu.Lock()
					markErr := a.repo.MarkShotAnalysisFailed(ctx, shot.ID)
					a.persistMu.Unlock()
					if markErr != nil {
						mu.Lock()
						if firstFatal == nil {
							firstFatal = errors.Join(err, markErr)
						}
						mu.Unlock()
						return
					}
					if ctx.Err() != nil {
						mu.Lock()
						if firstFatal == nil {
							firstFatal = err
						}
						mu.Unlock()
						return
					}
					continue
				}
				mu.Lock()
				summary.AnalyzedShots++
				mu.Unlock()
			}
		}()
	}
	for _, shot := range shots {
		if ctx.Err() != nil {
			break
		}
		jobs <- shot
	}
	close(jobs)
	wg.Wait()
	return summary, firstFatal
}

func (a *AnalysisRunner) analyzeShot(ctx context.Context, shot Shot, keyframes []Keyframe) error {
	if len(keyframes) > maxShotKeyframes {
		keyframes = keyframes[:maxShotKeyframes]
	}
	inputs := make([]KeyframeInput, 0, len(keyframes))
	for _, keyframe := range keyframes {
		data, err := a.loadVerifiedKeyframe(keyframe)
		if err != nil {
			return err
		}
		inputs = append(inputs, KeyframeInput{JPEG: data, Width: keyframe.Width, Height: keyframe.Height})
	}
	analysis, err := a.vision.Analyze(ctx, inputs)
	if err != nil {
		return err
	}
	vector, err := a.embedder.Embed(ctx, EmbeddingInput(analysis))
	if err != nil {
		return err
	}
	a.persistMu.Lock()
	defer a.persistMu.Unlock()
	storedDimension, err := a.repo.EmbeddingDimensionForModel(ctx, a.embeddingModel)
	if err != nil {
		return err
	}
	if storedDimension > 0 && storedDimension != len(vector) {
		return fmt.Errorf("%w: model %q stored %d, got %d", ErrEmbeddingDimensionChanged, a.embeddingModel, storedDimension, len(vector))
	}
	if err := a.repo.SetShotEmbedding(ctx, shot.ID, a.embeddingModel, vector, AnalysisVersion); err != nil {
		return err
	}
	tags := make([]Tag, 0, len(analysis.Tags))
	for _, tag := range analysis.Tags {
		tags = append(tags, Tag{ShotID: shot.ID, Namespace: visionTagNamespace, Value: tag.Value, Confidence: tag.Confidence})
	}
	if err := a.repo.UpsertTags(ctx, shot.ID, tags); err != nil {
		return err
	}
	// Marking completed comes last so an interruption re-runs this shot only.
	return a.repo.SetShotAnalysis(ctx, shot.ID, analysis, AnalysisVersion)
}

// loadVerifiedKeyframe re-resolves containment inside the media root and
// re-checks the stored SHA-256 so only the exact catalogued low-res JPEG can
// leave the machine.
func (a *AnalysisRunner) loadVerifiedKeyframe(keyframe Keyframe) ([]byte, error) {
	absolute, err := a.repo.ResolvePath(keyframe.RelativePath)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(absolute)
	if err != nil {
		return nil, fmt.Errorf("read keyframe %q: %w", keyframe.RelativePath, err)
	}
	digest := sha256Hex(data)
	if !strings.EqualFold(digest, keyframe.SHA256) {
		return nil, fmt.Errorf("%w: keyframe %q checksum mismatch", ErrInvalidValue, keyframe.RelativePath)
	}
	return data, nil
}

// ShotsPendingAnalysis lists shots whose analysis is not completed for the
// given version; a completed shot at the same version is never re-analyzed.
func (r *Repository) ShotsPendingAnalysis(ctx context.Context, analysisVersion string) ([]Shot, error) {
	rows, err := r.db.QueryContext(ctx, shotSelect+
		` WHERE NOT (analysis_status='completed' AND analysis_version=?) ORDER BY source_id, ordinal`, analysisVersion)
	if err != nil {
		return nil, fmt.Errorf("list shots pending analysis: %w", err)
	}
	defer rows.Close()
	shots := []Shot{}
	for rows.Next() {
		shot, err := scanShot(rows)
		if err != nil {
			return nil, fmt.Errorf("read pending analysis shot: %w", err)
		}
		shots = append(shots, shot)
	}
	return shots, rows.Err()
}

// SetShotAnalysis persists the vision result and marks the shot completed for
// the given analysis version.
func (r *Repository) SetShotAnalysis(ctx context.Context, shotID string, analysis ShotAnalysis, analysisVersion string) error {
	if err := analysis.validate(); err != nil {
		return err
	}
	if strings.TrimSpace(analysisVersion) == "" {
		return fmt.Errorf("%w: analysis version is required", ErrInvalidValue)
	}
	return r.withImmediateTx(ctx, func(conn *sql.Conn) error {
		result, err := conn.ExecContext(ctx, `UPDATE media_shots
			SET summary=?, mood=?, setting=?, people_count=?, motion_level=?, has_text=?, analysis_status='completed', analysis_version=?
			WHERE id=?`,
			analysis.Summary, analysis.Mood, analysis.Setting, analysis.PeopleCount,
			analysis.MotionLevel, analysis.HasText, analysisVersion, shotID)
		if err != nil {
			return fmt.Errorf("write shot analysis: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return ErrShotNotFound
		}
		return nil
	})
}

// MarkShotAnalysisFailed flags a shot as failed so it can be retried alone;
// completed shots are never downgraded.
func (r *Repository) MarkShotAnalysisFailed(ctx context.Context, shotID string) error {
	return r.withImmediateTx(ctx, func(conn *sql.Conn) error {
		if _, err := conn.ExecContext(ctx,
			`UPDATE media_shots SET analysis_status='failed' WHERE id=? AND analysis_status<>'completed'`, shotID); err != nil {
			return fmt.Errorf("mark shot analysis failed: %w", err)
		}
		return nil
	})
}

// EmbeddingDimensionForModel returns the dimension already stored for a
// model, or 0 when the model has no stored vectors yet.
func (r *Repository) EmbeddingDimensionForModel(ctx context.Context, model string) (int, error) {
	var dimension int
	err := r.db.QueryRowContext(ctx,
		`SELECT embedding_dimension FROM media_shots WHERE embedding_model=? AND embedding_dimension>0 LIMIT 1`, model).
		Scan(&dimension)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read stored embedding dimension: %w", err)
	}
	return dimension, nil
}
