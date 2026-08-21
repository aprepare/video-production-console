package montageplan

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// CaptionLLMLineBreakerEnabled turns the HTTP caption breaker back on.
//
// 2026-08-18：大模型的字幕分段和关键词挑选效果不好，先屏蔽。
// 用户自己生成配音和 SRT；混剪只用 SRT 时间轴 + 本地断行，不再为字幕调大模型。
// 实现和单测留着，改回 true 即可恢复。
const CaptionLLMLineBreakerEnabled = false

// LineBreaker splits narration sentences into caption lines. Chinese word
// boundaries are a language judgement, so a model makes the cuts and this
// package only verifies them; a missing or wrong answer falls back to the
// deterministic splitter instead of failing the plan.
//
// Production montage currently leaves this nil. See CaptionLLMLineBreakerEnabled.
type LineBreaker interface {
	BreakLines(ctx context.Context, sentences []string) (CaptionPack, error)
}

// CaptionLine is one on-screen row plus the keywords the model wants enlarged.
type CaptionLine struct {
	Text     string   `json:"text"`
	Keywords []string `json:"keywords,omitempty"`
}

// CaptionPack is one model answer for spoken captions and the board-title
// fallback used when the publishing package is missing.
type CaptionPack struct {
	BoardTitle    string          `json:"board_title,omitempty"`
	BoardSubtitle string          `json:"board_subtitle,omitempty"`
	Groups        [][]CaptionLine `json:"sentences,omitempty"`
}

type LineBreakerConfig struct {
	BaseURL         string
	Model           string
	APIKey          string
	ReasoningEffort string
	HTTPClient      *http.Client
}

const (
	lineBreakerHTTPTimeout            = 12 * time.Minute
	lineBreakerTotalTimeout           = 20 * time.Minute
	lineBreakerBatchSize              = 8
	lineBreakerMaxResponseBytes       = 16 << 20
	lineBreakerMaxTokens              = 8192
	defaultLineBreakerReasoningEffort = "high"
)

// NewHTTPLineBreaker returns nil when the endpoint is not configured, which
// keeps the planner on its offline path. Production montage does not wire this
// while CaptionLLMLineBreakerEnabled is false.
func NewHTTPLineBreaker(cfg LineBreakerConfig) LineBreaker {
	if strings.TrimSpace(cfg.BaseURL) == "" || strings.TrimSpace(cfg.Model) == "" {
		return nil
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: lineBreakerHTTPTimeout}
	}
	return httpLineBreaker{
		baseURL:         strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/"),
		model:           strings.TrimSpace(cfg.Model),
		apiKey:          strings.TrimSpace(cfg.APIKey),
		reasoningEffort: normalizeLineBreakerEffort(cfg.ReasoningEffort),
		client:          client,
	}
}

// HTTPLineBreakerEndpoint reports the chat endpoint a live line breaker will call.
func HTTPLineBreakerEndpoint(breaker LineBreaker) (baseURL, model string, ok bool) {
	httpBreaker, ok := breaker.(httpLineBreaker)
	if !ok {
		return "", "", false
	}
	return httpBreaker.baseURL, httpBreaker.model, true
}

// HTTPLineBreakerReasoningEffort reports the effort a live line breaker will send.
func HTTPLineBreakerReasoningEffort(breaker LineBreaker) (string, bool) {
	httpBreaker, ok := breaker.(httpLineBreaker)
	if !ok {
		return "", false
	}
	return httpBreaker.reasoningEffort, true
}

type httpLineBreaker struct {
	baseURL         string
	model           string
	apiKey          string
	reasoningEffort string
	client          *http.Client
}

func normalizeLineBreakerEffort(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "low", "medium", "high", "xhigh", "max", "ultra":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return defaultLineBreakerReasoningEffort
	}
}

const captionPackCacheVersion = "caption_pack_v2"

const lineBreakerSystemPrompt = `你给竖屏财经口播同时做三件事：断行、选关键词、写板上主副标题。只输出一个 JSON 对象，不要解释，不要 Markdown，不要长篇推理。

格式：
{"board_title":"最多15字","board_subtitle":"最多15字","sentences":[[{"text":"一行","keywords":["词"]},...],...]}

sentences 第 i 项对应输入第 i 句。

断行：
1) 每行 4 到 9 个字，一句的最后一行可以少于 4 个字。
2) 只在词语边界断开，不许拆开词语、数字和量词、书名号里的书名。
3) 每一行必须是听得懂的意群：完整主谓、完整动宾、或「数字+量词」。
4) 禁止把主语孤零零切出去。错误：「三年前大家」。正确：「三年前大家挤破头」。
5) 行尾不要停在：往、把、给、从、对、被、让、和、跟、与、在、为、向、的。
6) 行首不要是：里、中、上、下、了、着、过、率。
7) 不要拆开：法拍房、百分之、城镇化率、房产税、房贷、接盘、四十万套。
8) 删掉全部标点。把一句的行按顺序拼起来，必须和原句去掉标点后逐字相同，不许增删改写。

关键词：
1) 每行最多 2 个。
2) 只标三类：数字和量词（四十万套、百分之十九）、财经名词（法拍房、投资、房贷、房价）、强动词（砸钱、接盘、腰斩、断供、脱手）。
3) keywords 必须是该行里连续出现的原文，不许改写，不许把整行都标成关键词。
4) 虚词、程度副词不要标。没有就不给空数组。

标题：
1) 根据全部口播写板上主标题和副标题，各 6 到 15 字，不要标点，不要 #。
2) 主标题抓冲突或后果，副标题抓数据或第二钩子。
3) 禁止栏目套话：时代观察笔记、家庭财务提醒、生活成本提醒。

例子，原句「三年前大家挤破头往房子里砸钱还叫投资，三年后想脱手才发现自己成了接盘的那一个。」
正确：[{"text":"三年前大家挤破头","keywords":["挤破头"]},{"text":"往房子里砸钱","keywords":["砸钱"]},{"text":"还叫投资","keywords":["投资"]},{"text":"三年后想脱手","keywords":["脱手"]},{"text":"才发现自己成了","keywords":[]},{"text":"接盘的那一个","keywords":["接盘"]}]
错误：["三年前大家","挤破头往房子里","砸钱还叫投资"]`

func (b httpLineBreaker) BreakLines(ctx context.Context, sentences []string) (CaptionPack, error) {
	if len(sentences) == 0 {
		return CaptionPack{}, fmt.Errorf("caption line breaker received no sentences")
	}
	merged := CaptionPack{Groups: make([][]CaptionLine, 0, len(sentences))}
	for start := 0; start < len(sentences); start += lineBreakerBatchSize {
		end := start + lineBreakerBatchSize
		if end > len(sentences) {
			end = len(sentences)
		}
		pack, err := b.breakBatch(ctx, sentences, start, end)
		if err != nil {
			return CaptionPack{}, err
		}
		if len(pack.Groups) != end-start {
			return CaptionPack{}, fmt.Errorf("caption line breaker model %s returned %d groups for batch %d-%d", b.model, len(pack.Groups), start+1, end)
		}
		if merged.BoardTitle == "" {
			merged.BoardTitle = pack.BoardTitle
			merged.BoardSubtitle = pack.BoardSubtitle
		}
		merged.Groups = append(merged.Groups, pack.Groups...)
	}
	return merged, nil
}

func (b httpLineBreaker) breakBatch(ctx context.Context, sentences []string, start, end int) (CaptionPack, error) {
	batch, err := json.Marshal(sentences[start:end])
	if err != nil {
		return CaptionPack{}, err
	}
	all, err := json.Marshal(sentences)
	if err != nil {
		return CaptionPack{}, err
	}
	user := "batch=" + string(batch)
	if start == 0 {
		user = "all_sentences=" + string(all) + "\n" + user + "\n只切 batch 里的句子，sentences 数组长度必须等于 batch。标题根据 all_sentences 写。"
	} else {
		user += "\n只切本批，标题可空。sentences 数组长度必须等于 batch。"
	}
	payload, err := json.Marshal(map[string]any{
		"model": b.model,
		"messages": []map[string]string{
			{"role": "system", "content": lineBreakerSystemPrompt},
			{"role": "user", "content": user},
		},
		"temperature":      0,
		"stream":           true,
		"max_tokens":       lineBreakerMaxTokens,
		"reasoning_effort": b.reasoningEffort,
	})
	if err != nil {
		return CaptionPack{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, intentChatURL(b.baseURL), bytes.NewReader(payload))
	if err != nil {
		return CaptionPack{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	if b.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+b.apiKey)
	}
	response, err := b.client.Do(request)
	if err != nil {
		return CaptionPack{}, fmt.Errorf("caption line breaker model %s at %s: %w", b.model, intentChatURL(b.baseURL), err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return CaptionPack{}, fmt.Errorf("caption line breaker model %s at %s: status %d", b.model, intentChatURL(b.baseURL), response.StatusCode)
	}
	content, err := readCaptionModelContent(response)
	if err != nil {
		return CaptionPack{}, fmt.Errorf("caption line breaker model %s at %s: %w", b.model, intentChatURL(b.baseURL), err)
	}
	return parseCaptionPack(content)
}

func readCaptionModelContent(response *http.Response) (string, error) {
	if strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
		return readCaptionSSE(response.Body)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, lineBreakerMaxResponseBytes))
	if err != nil {
		return "", err
	}
	var envelope struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(body, &envelope) == nil && len(envelope.Choices) > 0 {
		return envelope.Choices[0].Message.Content, nil
	}
	return readCaptionSSE(bytes.NewReader(body))
}

func readCaptionSSE(r io.Reader) (string, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), lineBreakerMaxResponseBytes)
	var content strings.Builder
	var lastMessage string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") || !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil || len(chunk.Choices) == 0 {
			continue
		}
		if delta := chunk.Choices[0].Delta.Content; delta != "" {
			content.WriteString(delta)
		}
		if msg := chunk.Choices[0].Message.Content; msg != "" {
			lastMessage = msg
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if content.Len() > 0 {
		return content.String(), nil
	}
	if lastMessage != "" {
		return lastMessage, nil
	}
	return "", fmt.Errorf("empty caption model stream")
}

func parseCaptionPack(content string) (CaptionPack, error) {
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)
	var obj struct {
		BoardTitle    string          `json:"board_title"`
		BoardSubtitle string          `json:"board_subtitle"`
		Sentences     json.RawMessage `json:"sentences"`
		Groups        json.RawMessage `json:"groups"`
		Lines         json.RawMessage `json:"lines"`
	}
	if json.Unmarshal([]byte(content), &obj) == nil {
		raw := firstRawJSON(obj.Sentences, obj.Groups, obj.Lines)
		if len(raw) > 0 {
			groups, err := parseCaptionGroups(raw)
			if err != nil {
				return CaptionPack{}, err
			}
			return CaptionPack{
				BoardTitle:    strings.TrimSpace(obj.BoardTitle),
				BoardSubtitle: strings.TrimSpace(obj.BoardSubtitle),
				Groups:        groups,
			}, nil
		}
	}
	groups, err := parseCaptionGroups([]byte(content))
	if err != nil {
		return CaptionPack{}, err
	}
	return CaptionPack{Groups: groups}, nil
}

func firstRawJSON(values ...json.RawMessage) json.RawMessage {
	for _, value := range values {
		if len(bytes.TrimSpace(value)) > 0 && string(bytes.TrimSpace(value)) != "null" {
			return value
		}
	}
	return nil
}

func parseCaptionGroups(raw []byte) ([][]CaptionLine, error) {
	var objects [][]CaptionLine
	if json.Unmarshal(raw, &objects) == nil && captionGroupsHaveText(objects) {
		return objects, nil
	}
	var plain [][]string
	if json.Unmarshal(raw, &plain) == nil && len(plain) > 0 {
		return captionPackFromPlainLines(plain).Groups, nil
	}
	return nil, fmt.Errorf("caption line breaker returned no groups")
}

func captionGroupsHaveText(groups [][]CaptionLine) bool {
	if len(groups) == 0 {
		return false
	}
	for _, group := range groups {
		for _, line := range group {
			if strings.TrimSpace(line.Text) != "" {
				return true
			}
		}
	}
	return false
}

func captionPackFromPlainLines(groups [][]string) CaptionPack {
	out := CaptionPack{Groups: make([][]CaptionLine, len(groups))}
	for i, group := range groups {
		lines := make([]CaptionLine, 0, len(group))
		for _, line := range group {
			lines = append(lines, CaptionLine{Text: line})
		}
		out.Groups[i] = lines
	}
	return out
}

const captionLineCacheVersion = captionPackCacheVersion

type captionLineCache struct {
	Version        string          `json:"version"`
	SubtitleDigest string          `json:"subtitle_digest"`
	BoardTitle     string          `json:"board_title,omitempty"`
	BoardSubtitle  string          `json:"board_subtitle,omitempty"`
	Lines          [][]CaptionLine `json:"lines"`
}

func readCaptionLineCache(path, digest string, sentences int) CaptionPack {
	if strings.TrimSpace(path) == "" {
		return CaptionPack{}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return CaptionPack{}
	}
	var cache captionLineCache
	if json.Unmarshal(raw, &cache) != nil {
		return CaptionPack{}
	}
	if cache.Version != captionLineCacheVersion || cache.SubtitleDigest != digest || len(cache.Lines) != sentences {
		return CaptionPack{}
	}
	return CaptionPack{BoardTitle: cache.BoardTitle, BoardSubtitle: cache.BoardSubtitle, Groups: cache.Lines}
}

func writeCaptionLineCache(path, digest string, pack CaptionPack) {
	if strings.TrimSpace(path) == "" {
		return
	}
	raw, err := json.MarshalIndent(captionLineCache{
		Version:        captionLineCacheVersion,
		SubtitleDigest: digest,
		BoardTitle:     pack.BoardTitle,
		BoardSubtitle:  pack.BoardSubtitle,
		Lines:          pack.Groups,
	}, "", "  ")
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	_ = os.WriteFile(path, append(raw, '\n'), 0o600)
}

func sentenceDigest(sentences []TimedSentence) string {
	hash := sha256.New()
	for _, sentence := range sentences {
		hash.Write([]byte(sentence.Text))
		hash.Write([]byte{'\n'})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// captionLinesCachePath keeps the accepted lines beside the project so every
// remake of the same narration reuses them.
func captionLinesCachePath(ctx *planContext) string {
	if ctx == nil || ctx.manifest.Project == nil {
		return ""
	}
	root := strings.TrimSpace(ctx.manifest.NonSecretSettings.DataRoot)
	projectID := strings.TrimSpace(ctx.manifest.Project.ID)
	if root == "" || projectID == "" {
		return ""
	}
	return filepath.Join(root, "projects", projectID, "caption_lines.json")
}

// resolveModelLines returns per-sentence caption lines, or nil for any sentence
// the model failed to break correctly.
func resolveModelLines(breaker LineBreaker, sentences []TimedSentence, cachePath string) (CaptionPack, []string) {
	// Skip the live HTTP breaker and its cache. Injected test fakes still run.
	if !CaptionLLMLineBreakerEnabled {
		if breaker == nil {
			return CaptionPack{}, nil
		}
		if _, isHTTP := breaker.(httpLineBreaker); isHTTP {
			return CaptionPack{}, nil
		}
	}
	digest := sentenceDigest(sentences)
	if CaptionLLMLineBreakerEnabled {
		if cached := readCaptionLineCache(cachePath, digest, len(sentences)); len(cached.Groups) == len(sentences) {
			return cached, nil
		}
	}
	if breaker == nil {
		return CaptionPack{}, nil
	}
	texts := make([]string, 0, len(sentences))
	for _, sentence := range sentences {
		texts = append(texts, sentence.Text)
	}
	ctx, cancel := context.WithTimeout(context.Background(), lineBreakerTotalTimeout)
	defer cancel()
	pack, err := breaker.BreakLines(ctx, texts)
	if err != nil {
		return CaptionPack{}, []string{"caption_lines_unavailable: " + err.Error()}
	}
	if len(pack.Groups) != len(sentences) {
		return CaptionPack{BoardTitle: pack.BoardTitle, BoardSubtitle: pack.BoardSubtitle}, []string{
			fmt.Sprintf("caption_lines_rejected: model returned %d groups for %d sentences", len(pack.Groups), len(sentences)),
		}
	}
	var (
		notes    []string
		rejected int
		accepted int
	)
	cleaned := CaptionPack{
		BoardTitle:    fitModelBoardTitle(pack.BoardTitle),
		BoardSubtitle: fitModelBoardTitle(pack.BoardSubtitle),
		Groups:        make([][]CaptionLine, len(sentences)),
	}
	for i, group := range pack.Groups {
		lines, err := acceptedLines(group, sentences[i].Text)
		if err != nil {
			rejected++
			if rejected <= 3 {
				notes = append(notes, fmt.Sprintf("caption_lines_rejected: sentence %d %v", i+1, err))
			}
			continue
		}
		cleaned.Groups[i] = lines
		accepted++
	}
	if rejected > 3 {
		notes = append(notes, fmt.Sprintf("caption_lines_rejected: %d sentences fell back to the deterministic splitter", rejected))
	}
	if accepted == 0 {
		return CaptionPack{BoardTitle: cleaned.BoardTitle, BoardSubtitle: cleaned.BoardSubtitle}, notes
	}
	if rejected == 0 && CaptionLLMLineBreakerEnabled {
		writeCaptionLineCache(cachePath, digest, cleaned)
	}
	return cleaned, notes
}

// acceptedLines enforces the contract the prompt states: readable line lengths
// and a concatenation that reproduces the narration character for character.
func acceptedLines(group []CaptionLine, sentence string) ([]CaptionLine, error) {
	cleaned := make([]CaptionLine, 0, len(group))
	for _, line := range group {
		text := strings.TrimSpace(line.Text)
		if text == "" {
			continue
		}
		if n := len([]rune(text)); n > spokenLineModelMaxRunes {
			return nil, fmt.Errorf("line %q is %d runes", text, n)
		}
		if incompleteSpokenLine(text) {
			return nil, fmt.Errorf("line %q is an incomplete cut", text)
		}
		cleaned = append(cleaned, CaptionLine{
			Text:     text,
			Keywords: sanitizeModelKeywords(text, line.Keywords),
		})
	}
	if len(cleaned) == 0 {
		return nil, fmt.Errorf("no usable lines")
	}
	joined := make([]string, 0, len(cleaned))
	for _, line := range cleaned {
		joined = append(joined, line.Text)
	}
	if want, got := strippedSpeech(sentence), strings.Join(joined, ""); want != got {
		return nil, fmt.Errorf("lines do not rebuild the sentence")
	}
	return cleaned, nil
}

var spokenIncompleteEndings = []string{"大家", "有人", "人们", "还有"}
var spokenModelTrailingForbidden = []rune("往把给从对被让和跟与在为向")

func incompleteSpokenLine(line string) bool {
	runes := []rune(line)
	if len(runes) == 0 {
		return true
	}
	if containsRune(spokenModelTrailingForbidden, runes[len(runes)-1]) {
		return true
	}
	if containsRune(spokenLeadingForbidden, runes[0]) {
		return true
	}
	for _, ending := range spokenIncompleteEndings {
		if strings.HasSuffix(line, ending) && len(runes) <= 6 {
			return true
		}
	}
	return false
}

func sanitizeModelKeywords(line string, keywords []string) []string {
	if len(keywords) == 0 {
		return nil
	}
	lineRunes := []rune(line)
	seen := map[string]bool{}
	out := make([]string, 0, spokenKeywordSpanLimit)
	for _, keyword := range keywords {
		keyword = strings.TrimSpace(keyword)
		if keyword == "" || seen[keyword] {
			continue
		}
		runes := []rune(keyword)
		if len(runes) < 2 || len(runes) >= len(lineRunes) {
			continue
		}
		if strings.Index(line, keyword) < 0 {
			continue
		}
		seen[keyword] = true
		out = append(out, keyword)
		if len(out) >= spokenKeywordSpanLimit {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func fitModelBoardTitle(src string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(src) {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) || r == '#' {
			continue
		}
		b.WriteRune(r)
	}
	cleaned := b.String()
	runes := []rune(cleaned)
	if len(runes) < boardTitleMinRunes || len(runes) > boardTitleMaxRunes {
		return ""
	}
	switch cleaned {
	case "时代观察笔记", "家庭财务提醒", "生活成本提醒":
		return ""
	}
	return cleaned
}

// spokenLineModelMaxRunes is the widest line that still fits one on-screen row
// at the QC caption size.
const spokenLineModelMaxRunes = 10

// strippedSpeech removes everything a caption never shows, so model output can
// be compared with the narration text it came from.
func strippedSpeech(text string) string {
	var b strings.Builder
	for _, r := range text {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
