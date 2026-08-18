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
	VisualQuery    string      `json:"visual_query,omitempty"`
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

var financeEntities = []string{
	"银行", "房子", "家庭", "股市", "存款", "现金流", "房价", "贷款", "柜台", "机会",
	"买房", "法拍", "房贷", "首付", "月供", "接盘", "房产", "楼市", "业主", "中介", "合同",
}

// spokenVisualBridge maps Chinese narration cues onto the English catalog
// tags written during ingest (cityscape, money, stock_market, ...).
var spokenVisualBridge = []struct {
	cues []string
	tags []string
}{
	{[]string{"房子", "买房", "房价", "房产", "楼市", "法拍", "接盘", "业主"}, []string{"cityscape", "nighttime cityscape", "city skyline", "skyscrapers", "building_facade", "urban", "urban_environment", "indoor"}},
	{[]string{"贷款", "房贷", "月供", "首付", "银行", "存款", "柜台"}, []string{"coins", "coin", "money", "currency", "calculator", "piggy_bank", "banknote", "wallet", "credit_card", "financial_data", "financial data"}},
	{[]string{"股市", "行情"}, []string{"stock_market", "stock market", "graph", "line graph", "line_graph", "data visualization", "data_visualization", "financial_data", "digital_display", "digital display"}},
	{[]string{"合同", "中介"}, []string{"pen", "tablet", "hands", "handshake", "business attire", "business meeting", "office", "office environment"}},
	{[]string{"家庭", "现金流"}, []string{"money", "coins", "wallet", "piggy_bank", "empty wallet"}},
}

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
type LocalIntentAnalyzer struct {
	// RestrictToLandscape keeps scenic montage from asking the catalog for
	// offices, ledgers, or portraits. Movie montage leaves this false so
	// recall can follow the narration.
	RestrictToLandscape bool
}

func (a LocalIntentAnalyzer) Analyze(_ context.Context, sentences []TimedSentence) ([]NarrativeIntent, error) {
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
			VisualConcepts: inferVisualConcepts(segment.Text, a.RestrictToLandscape),
			Metaphors:      inferMetaphors(segment.Text),
			Importance:     importanceFor(kind),
			CaptionKind:    kind,
		}
		intents = append(intents, intent)
	}
	return intents, nil
}

func enrichIntentsWithSpokenCues(intents []NarrativeIntent, landscapeOnly bool) []NarrativeIntent {
	if landscapeOnly || len(intents) == 0 {
		return intents
	}
	out := append([]NarrativeIntent(nil), intents...)
	for i := range out {
		text := out[i].Text
		out[i].Entities = uniqueStrings(append(out[i].Entities, extractEntities(text)...))
		out[i].Topics = uniqueStrings(append(out[i].Topics, inferTopics(text)...))
		out[i].VisualConcepts = uniqueStrings(append(out[i].VisualConcepts, inferVisualConcepts(text, false)...))
		if strings.TrimSpace(out[i].Mood) == "" {
			out[i].Mood = inferMood(text, out[i].CaptionKind)
		}
	}
	return out
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
	topics := make([]string, 0, 4)
	switch {
	case strings.Contains(text, "法拍") || strings.Contains(text, "买房") || strings.Contains(text, "房产") || strings.Contains(text, "楼市"):
		topics = append(topics, "买房")
	case strings.Contains(text, "房贷") || strings.Contains(text, "月供") || strings.Contains(text, "首付") || strings.Contains(text, "贷款"):
		topics = append(topics, "房贷")
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

func inferVisualConcepts(text string, landscapeOnly bool) []string {
	if landscapeOnly {
		return []string{"风景", "景观"}
	}
	concepts := append([]string{}, extractEntities(text)...)
	for _, entry := range metaphorLexicon {
		if strings.Contains(text, entry.cue) {
			concepts = append(concepts, entry.concept)
		}
	}
	concepts = append(concepts, inferTopics(text)...)
	return uniqueStrings(expandSpokenVisualTags(text, concepts))
}

func expandSpokenVisualTags(text string, concepts []string) []string {
	haystack := text + " " + strings.Join(concepts, " ")
	out := append([]string{}, concepts...)
	for _, rule := range spokenVisualBridge {
		hit := false
		for _, cue := range rule.cues {
			if strings.Contains(haystack, cue) {
				hit = true
				break
			}
		}
		if !hit {
			continue
		}
		out = append(out, rule.tags...)
	}
	return out
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

func alignedLocalIntents(sentences []TimedSentence, landscapeOnly bool) []NarrativeIntent {
	analyzer := LocalIntentAnalyzer{RestrictToLandscape: landscapeOnly}
	out := make([]NarrativeIntent, 0, len(sentences))
	for _, sentence := range sentences {
		intents, err := analyzer.Analyze(context.Background(), []TimedSentence{sentence})
		if err != nil || len(intents) == 0 {
			out = append(out, NarrativeIntent{
				Text: sentence.Text, StartMS: sentence.StartMS, EndMS: sentence.EndMS,
				Mood: "neutral", Importance: 0.45,
			})
			continue
		}
		out = append(out, intents[0])
	}
	return out
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
	BaseURL             string
	Model               string
	APIKey              string
	ReasoningEffort     string
	HTTPClient          *http.Client
	Fallback            IntentAnalyzer
	RestrictToLandscape bool
}

// HTTPIntentAnalyzer calls a chat endpoint for strict JSON intents and
// falls back to LocalIntentAnalyzer on any failure.
type HTTPIntentAnalyzer struct {
	baseURL             string
	model               string
	apiKey              string
	reasoningEffort     string
	client              *http.Client
	fallback            IntentAnalyzer
	restrictToLandscape bool
}

func NewHTTPIntentAnalyzer(cfg IntentAnalyzerConfig) IntentAnalyzer {
	fallback := cfg.Fallback
	if fallback == nil {
		fallback = LocalIntentAnalyzer{RestrictToLandscape: cfg.RestrictToLandscape}
	}
	if strings.TrimSpace(cfg.BaseURL) == "" || strings.TrimSpace(cfg.Model) == "" {
		return fallback
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 180 * time.Second}
	}
	return HTTPIntentAnalyzer{
		baseURL:             strings.TrimRight(cfg.BaseURL, "/"),
		model:               cfg.Model,
		apiKey:              cfg.APIKey,
		reasoningEffort:     strings.TrimSpace(cfg.ReasoningEffort),
		client:              client,
		fallback:            fallback,
		restrictToLandscape: cfg.RestrictToLandscape,
	}
}

const intentAnalyzeBatchSize = 16

func (a HTTPIntentAnalyzer) Analyze(ctx context.Context, sentences []TimedSentence) ([]NarrativeIntent, error) {
	if len(sentences) == 0 {
		return nil, nil
	}
	out := make([]NarrativeIntent, 0, len(sentences))
	for i := 0; i < len(sentences); i += intentAnalyzeBatchSize {
		end := i + intentAnalyzeBatchSize
		if end > len(sentences) {
			end = len(sentences)
		}
		chunk := sentences[i:end]
		remote, err := a.analyzeRemote(ctx, chunk)
		if err != nil || len(remote) != len(chunk) {
			remote = alignedLocalIntents(chunk, a.restrictToLandscape)
		}
		for j := range remote {
			remote[j].StartMS = chunk[j].StartMS
			remote[j].EndMS = chunk[j].EndMS
			remote[j].SegmentID = fmt.Sprintf("seg-%03d", i+j+1)
			if strings.TrimSpace(remote[j].Text) == "" {
				remote[j].Text = chunk[j].Text
			}
		}
		out = append(out, remote...)
	}
	return out, nil
}

func (a HTTPIntentAnalyzer) postChat(ctx context.Context, system, user string) (string, error) {
	body := map[string]any{
		"model": a.model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"temperature": 0,
	}
	if a.reasoningEffort != "" {
		body["reasoning_effort"] = a.reasoningEffort
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, intentChatURL(a.baseURL), bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/json")
	if a.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+a.apiKey)
	}
	response, err := a.client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return "", err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("intent analyzer status %d", response.StatusCode)
	}
	var envelope struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || len(envelope.Choices) == 0 {
		return "", fmt.Errorf("intent analyzer response is malformed")
	}
	return envelope.Choices[0].Message.Content, nil
}

func (a HTTPIntentAnalyzer) analyzeRemote(ctx context.Context, sentences []TimedSentence) ([]NarrativeIntent, error) {
	content, err := a.postChat(ctx, intentSystemPrompt(a.restrictToLandscape), intentUserPrompt(sentences))
	if err != nil {
		return nil, err
	}
	return parseIntentJSON(content)
}

func intentChatURL(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if strings.HasSuffix(base, "/chat/completions") {
		return base
	}
	if strings.HasSuffix(base, "/v1") {
		return base + "/chat/completions"
	}
	return base + "/v1/chat/completions"
}

func intentSystemPrompt(landscapeOnly bool) string {
	if landscapeOnly {
		return "你把旁白句子转成 NarrativeIntent JSON 数组。只输出 JSON。visual_concepts 只能填风景或景观。"
	}
	return strings.TrimSpace(`你是财经短视频的画面检索员。素材库几乎没有真实住宅、小区、法拍现场，高频画面是城市天际线、办公、硬币、钱包、计算器、K线、握手、室内。
把每一句旁白转成检索条件，一句对应一个对象，顺序与输入完全一致。只输出 JSON 数组。
每个对象字段：segment_id,start_ms,end_ms,text,entities,topics,mood,visual_concepts,visual_query,metaphors,importance,caption_kind。
规则：
- start_ms/end_ms/text 必须原样抄输入，不要改时间。
- visual_concepts 只能从下面英文标签里选 3 到 6 个，不要自造中文画面词：cityscape, nighttime cityscape, city skyline, skyscrapers, building_facade, urban, indoor, office, office environment, business meeting, business attire, handshake, coins, coin, money, currency, banknote, wallet, empty wallet, piggy_bank, credit_card, calculator, financial_data, stock_market, graph, line graph, data visualization, digital_display, pen, tablet, hands, desk, laptop
- 住房/法拍/楼市/房价 → cityscape, city skyline, skyscrapers, indoor, building_facade
- 月供/首付/房贷/贷款/存款 → coins, money, calculator, piggy_bank, wallet, banknote
- 危险/没人接/接盘/跌 → empty wallet, coins, cityscape, indoor
- 合同/中介/签约 → pen, handshake, tablet, business meeting
- 股市/行情 → stock_market, graph, financial_data, digital_display
- visual_query 用一句英文描述想要的画面，供向量检索，例如 "night city skyline with empty streets" 或 "hands counting coins beside a calculator"。
- mood 只能是 warning, anxious, resolute, neutral。
- caption_kind 只能是 number、turning_point、conclusion 或空字符串。
- importance 0 到 1。转折和结论更高。`)
}

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
