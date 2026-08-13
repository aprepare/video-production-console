package montageplan

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// NarrativeIntent is one timed visual segment with structured recall cues.
// Field names match plan §5.1.
type NarrativeIntent struct {
	SegmentID      string      `json:"segment_id"`
	StartMS        int64       `json:"start_ms"`
	EndMS          int64       `json:"end_ms"`
	Text           string      `json:"text"`
	Entities       []string    `json:"entities"`
	Topics         []string    `json:"topics"`
	Mood           string      `json:"mood"`
	VisualConcepts []string    `json:"visual_concepts"`
	Metaphors      []string    `json:"metaphors"`
	Importance     float64     `json:"importance"`
	CaptionKind    CaptionKind `json:"caption_kind"`
}

// IntentAnalyzer turns timed sentences into narrative intents.
type IntentAnalyzer interface {
	Analyze(ctx context.Context, sentences []TimedSentence) ([]NarrativeIntent, error)
}

// Embedder is the injected text-embedding surface. The catalog HTTP
// embedder satisfies it; tests use fakes. Missing embeddings never fail a plan.
type Embedder interface {
	Embed(ctx context.Context, input string) ([]float32, error)
}

var financeEntities = []string{"银行", "房子", "家庭", "股市", "存款", "现金流", "房价", "贷款", "柜台", "机会"}

var metaphorLexicon = []struct {
	cue, metaphor, concept string
}{
	{cue: "流动性", metaphor: "水位下降", concept: "流动性"},
	{cue: "收紧", metaphor: "关门", concept: "机会收紧"},
	{cue: "机会", metaphor: "关门", concept: "机会收紧"},
	{cue: "变薄", metaphor: "水位下降", concept: "现金流变薄"},
	{cue: "绳索", metaphor: "绳索绷紧", concept: "压力"},
}

// LocalIntentAnalyzer extracts entities, turning points and moods with
// offline rules so a missing model never blocks a task.
type LocalIntentAnalyzer struct{}

func (LocalIntentAnalyzer) Analyze(_ context.Context, sentences []TimedSentence) ([]NarrativeIntent, error) {
	segments := buildVisualSegments(sentences)
	intents := make([]NarrativeIntent, 0, len(segments))
	for i, segment := range segments {
		kind := segment.Kind
		if kind == "" {
			if detected, ok := classifyCaptionKind(segment.Text); ok {
				kind = detected
			}
		}
		intent := NarrativeIntent{
			SegmentID:      fmt.Sprintf("seg-%03d", i+1),
			StartMS:        segment.StartMS,
			EndMS:          segment.EndMS,
			Text:           segment.Text,
			Entities:       extractEntities(segment.Text),
			Topics:         inferTopics(segment.Text),
			Mood:           inferMood(segment.Text, kind),
			VisualConcepts: inferVisualConcepts(segment.Text),
			Metaphors:      inferMetaphors(segment.Text),
			Importance:     importanceFor(kind),
			CaptionKind:    kind,
		}
		intents = append(intents, intent)
	}
	return intents, nil
}

func extractEntities(text string) []string {
	found := make([]string, 0, 4)
	for _, entity := range financeEntities {
		if strings.Contains(text, entity) {
			found = append(found, entity)
		}
	}
	return found
}

func inferTopics(text string) []string {
	topics := make([]string, 0, 3)
	switch {
	case strings.Contains(text, "家庭") || strings.Contains(text, "现金流"):
		topics = append(topics, "家庭财务")
	case strings.Contains(text, "银行") || strings.Contains(text, "存款"):
		topics = append(topics, "银行")
	case strings.Contains(text, "股市") || strings.Contains(text, "房价"):
		topics = append(topics, "资产价格")
	}
	if strings.Contains(text, "危险") || strings.Contains(text, "风险") {
		topics = append(topics, "风险")
	}
	return uniqueStrings(topics)
}

func inferMood(text string, kind CaptionKind) string {
	switch {
	case kind == CaptionTurningPoint || strings.Contains(text, "危险") || strings.Contains(text, "收紧"):
		return "warning"
	case kind == CaptionConclusion:
		return "resolute"
	case kind == CaptionNumber:
		return "neutral"
	case strings.Contains(text, "深夜") || strings.Contains(text, "焦虑"):
		return "anxious"
	default:
		return "neutral"
	}
}

func inferVisualConcepts(text string) []string {
	concepts := make([]string, 0, 3)
	if strings.Contains(text, "家庭") || strings.Contains(text, "现金流") {
		concepts = append(concepts, "账本", "空钱包")
	}
	if strings.Contains(text, "银行") {
		concepts = append(concepts, "银行柜台")
	}
	if strings.Contains(text, "深夜") {
		concepts = append(concepts, "深夜家庭")
	}
	return concepts
}

func inferMetaphors(text string) []string {
	found := make([]string, 0, 2)
	for _, entry := range metaphorLexicon {
		if strings.Contains(text, entry.cue) {
			found = append(found, entry.metaphor)
		}
	}
	return uniqueStrings(found)
}

func importanceFor(kind CaptionKind) float64 {
	switch kind {
	case CaptionConclusion:
		return 0.92
	case CaptionTurningPoint:
		return 0.84
	case CaptionNumber:
		return 0.72
	default:
		return 0.45
	}
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

// IntentAnalyzerConfig configures the OpenAI-compatible chat analyzer.
// Endpoint, model and key are injected; nothing is read from the environment.
type IntentAnalyzerConfig struct {
	BaseURL    string
	Model      string
	APIKey     string
	HTTPClient *http.Client
	Fallback   IntentAnalyzer
}

// HTTPIntentAnalyzer calls a chat endpoint for strict JSON intents and
// falls back to LocalIntentAnalyzer on any failure.
type HTTPIntentAnalyzer struct {
	baseURL  string
	model    string
	apiKey   string
	client   *http.Client
	fallback IntentAnalyzer
}

func NewHTTPIntentAnalyzer(cfg IntentAnalyzerConfig) IntentAnalyzer {
	fallback := cfg.Fallback
	if fallback == nil {
		fallback = LocalIntentAnalyzer{}
	}
	if strings.TrimSpace(cfg.BaseURL) == "" || strings.TrimSpace(cfg.Model) == "" {
		return fallback
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	return HTTPIntentAnalyzer{
		baseURL:  strings.TrimRight(cfg.BaseURL, "/"),
		model:    cfg.Model,
		apiKey:   cfg.APIKey,
		client:   client,
		fallback: fallback,
	}
}

func (a HTTPIntentAnalyzer) Analyze(ctx context.Context, sentences []TimedSentence) ([]NarrativeIntent, error) {
	intents, err := a.analyzeRemote(ctx, sentences)
	if err != nil || len(intents) == 0 {
		return a.fallback.Analyze(ctx, sentences)
	}
	return intents, nil
}

func (a HTTPIntentAnalyzer) analyzeRemote(ctx context.Context, sentences []TimedSentence) ([]NarrativeIntent, error) {
	payload, err := json.Marshal(map[string]any{
		"model": a.model,
		"messages": []map[string]string{
			{"role": "system", "content": intentSystemPrompt},
			{"role": "user", "content": intentUserPrompt(sentences)},
		},
		"temperature": 0,
	})
	if err != nil {
		return nil, err
	}
	endpoint := a.baseURL + "/chat/completions"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	if a.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+a.apiKey)
	}
	response, err := a.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("intent analyzer status %d", response.StatusCode)
	}
	var envelope struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || len(envelope.Choices) == 0 {
		return nil, fmt.Errorf("intent analyzer response is malformed")
	}
	return parseIntentJSON(envelope.Choices[0].Message.Content)
}

const intentSystemPrompt = `你把旁白句子转成 NarrativeIntent JSON 数组。只输出 JSON，不要解释。每个对象必须含 segment_id,start_ms,end_ms,text,entities,topics,mood,visual_concepts,metaphors,importance,caption_kind。caption_kind 只能是 number、turning_point、conclusion 或空字符串。`

func intentUserPrompt(sentences []TimedSentence) string {
	raw, _ := json.Marshal(sentences)
	return "sentences=" + string(raw)
}

func parseIntentJSON(content string) ([]NarrativeIntent, error) {
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)
	var intents []NarrativeIntent
	if err := json.Unmarshal([]byte(content), &intents); err != nil {
		return nil, err
	}
	cleaned := make([]NarrativeIntent, 0, len(intents))
	for _, intent := range intents {
		if strings.TrimSpace(intent.SegmentID) == "" || strings.TrimSpace(intent.Text) == "" {
			return nil, fmt.Errorf("intent missing segment_id or text")
		}
		if intent.Importance < 0 || intent.Importance > 1 {
			return nil, fmt.Errorf("intent importance out of range")
		}
		cleaned = append(cleaned, intent)
	}
	if len(cleaned) == 0 {
		return nil, fmt.Errorf("intent list is empty")
	}
	return cleaned, nil
}
