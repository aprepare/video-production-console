package spokenlines

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Keyword kinds. Warning terms paint red on screen, numbers paint gold.
const (
	KeywordKindWarning = "warning"
	KeywordKindNumber  = "number"
)

// KeywordSchemaVersion versions the caption_keywords.json asset.
const KeywordSchemaVersion = 1

// MaxKeywordsPerLine caps how many spans one caption line may carry so the
// screen never turns into a wall of emphasis.
const MaxKeywordsPerLine = 2

// KeywordDoc is the caption_keywords asset: one entry per 口播稿 line, in the
// same order as the spoken script.
type KeywordDoc struct {
	SchemaVersion int           `json:"schema_version"`
	Lines         []KeywordLine `json:"lines"`
}

// KeywordLine ties the keywords back to the exact spoken line text.
type KeywordLine struct {
	Line     string    `json:"line"`
	Keywords []Keyword `json:"keywords"`
}

// Keyword is one term the model marked for on-screen emphasis.
type Keyword struct {
	Text string `json:"text"`
	Kind string `json:"kind"`
}

// ParseKeywordDoc decodes and validates a stored caption_keywords asset.
func ParseKeywordDoc(raw []byte) (KeywordDoc, error) {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	var doc KeywordDoc
	if err := dec.Decode(&doc); err != nil {
		return KeywordDoc{}, fmt.Errorf("decode caption keywords: %w", err)
	}
	if doc.SchemaVersion != KeywordSchemaVersion {
		return KeywordDoc{}, fmt.Errorf("unsupported caption keywords schema_version %d", doc.SchemaVersion)
	}
	for i, line := range doc.Lines {
		if strings.TrimSpace(line.Line) == "" {
			return KeywordDoc{}, fmt.Errorf("caption keywords line %d is empty", i+1)
		}
		for _, kw := range line.Keywords {
			if kw.Kind != KeywordKindWarning && kw.Kind != KeywordKindNumber {
				return KeywordDoc{}, fmt.Errorf("caption keywords line %d has unknown kind %q", i+1, kw.Kind)
			}
			if !strings.Contains(line.Line, kw.Text) {
				return KeywordDoc{}, fmt.Errorf("caption keywords line %d: %q not found in line", i+1, kw.Text)
			}
		}
	}
	return doc, nil
}

// BuildKeywordDoc turns raw model output into a validated KeywordDoc for the
// given spoken lines. Keywords the model hallucinated (not a substring of the
// line), duplicate terms, and anything past MaxKeywordsPerLine are dropped
// instead of failing the task; an unusable payload still errors.
func BuildKeywordDoc(raw string, lines []string) (KeywordDoc, error) {
	if len(lines) == 0 {
		return KeywordDoc{}, fmt.Errorf("no spoken lines to annotate")
	}
	payload := strings.TrimSpace(stripCodeFence(raw))
	start := strings.IndexAny(payload, "[{")
	if start < 0 {
		return KeywordDoc{}, fmt.Errorf("model returned no JSON payload")
	}
	payload = payload[start:]

	parsed, err := decodeKeywordPayload(payload)
	if err != nil {
		return KeywordDoc{}, err
	}

	doc := KeywordDoc{SchemaVersion: KeywordSchemaVersion, Lines: make([]KeywordLine, len(lines))}
	for i, line := range lines {
		doc.Lines[i] = KeywordLine{Line: line, Keywords: []Keyword{}}
	}
	for i, entry := range parsed {
		index := matchKeywordLine(entry.Line, lines, i)
		if index < 0 {
			continue
		}
		target := &doc.Lines[index]
		for _, kw := range entry.Keywords {
			text := strings.TrimSpace(kw.Text)
			if text == "" || !strings.Contains(lines[index], text) {
				continue
			}
			if keywordListed(target.Keywords, text) {
				continue
			}
			if len(target.Keywords) >= MaxKeywordsPerLine {
				break
			}
			target.Keywords = append(target.Keywords, Keyword{Text: text, Kind: normalizeKeywordKind(kw.Kind, text)})
		}
	}
	return doc, nil
}

// MarshalKeywordDoc renders the asset file.
func MarshalKeywordDoc(doc KeywordDoc) ([]byte, error) {
	return json.MarshalIndent(doc, "", "  ")
}

func decodeKeywordPayload(payload string) ([]KeywordLine, error) {
	// Accept either the full doc shape or a bare array of line entries.
	var doc KeywordDoc
	if err := json.Unmarshal([]byte(payload), &doc); err == nil && len(doc.Lines) > 0 {
		return doc.Lines, nil
	}
	var entries []KeywordLine
	if err := json.Unmarshal([]byte(payload), &entries); err == nil && len(entries) > 0 {
		return entries, nil
	}
	return nil, fmt.Errorf("model keyword payload is not usable JSON")
}

// matchKeywordLine finds which spoken line an entry belongs to: exact text
// first, then positional when the model kept the order but reworded slightly.
func matchKeywordLine(text string, lines []string, position int) int {
	trimmed := strings.TrimSpace(text)
	for i, line := range lines {
		if line == trimmed {
			return i
		}
	}
	if position >= 0 && position < len(lines) {
		return position
	}
	return -1
}

func keywordListed(keywords []Keyword, text string) bool {
	for _, kw := range keywords {
		if kw.Text == text {
			return true
		}
	}
	return false
}

// normalizeKeywordKind repairs sloppy kinds: anything containing a digit or a
// percent sign counts as a number, everything else warns.
func normalizeKeywordKind(kind, text string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case KeywordKindWarning:
		return KeywordKindWarning
	case KeywordKindNumber:
		return KeywordKindNumber
	}
	for _, r := range text {
		if r >= '0' && r <= '9' || r == '%' || r == '％' {
			return KeywordKindNumber
		}
	}
	return KeywordKindWarning
}

// KeywordSystemPrompt drives the caption keyword model. The console validates
// and trims the reply, so a sloppy model cannot break the montage plan.
const KeywordSystemPrompt = `你是财经视频号字幕关键词标注助手。输入是已经定稿的口播稿（一句一行），你为每一行挑出值得在屏幕上放大强调的词。不要改写口播稿，不要解释，不要调用工具。

只返回一个 JSON 对象，不要 Markdown。格式：
{"lines":[{"line":"原样的一行","keywords":[{"text":"词","kind":"warning"}]}]}

规则：
1. 行序、行数、行文本必须与输入完全一致，不增删改任何一行。
2. 标注密度：每行最多标 2 个词，全篇一半以上的行要有标注，不允许连续 4 行都没有标注。
3. **开头 12 行是黄金钩子区**：只要行里有情绪词、动作词或数字就必须标，比如"发财""运气""彩票""又要有一批""翻身""机会"这类勾住观众的词，一个都不能放过。
4. kind 只有两种：
   - "warning"：能让观众心里一紧或眼前一亮的词。包括警示损失类（腰斩、断供、亏、跌、没人接手、压垮），钩子情绪类（发财、暴富、翻身、机会、错过、运气），关键动作类（换筹码、搬出来、洗牌、接盘）。
   - "number"：数字、金额、比例、年份，如 67%、0.05%、40万套、2026年。
5. text 必须原样出现在该行里，一个字都不能差。优先标 2-4 个字的实词，确实需要时可以标单字（如"又"）。
6. 不要标纯虚词和连接词（的、了、就是、所以、但是）；一行里不要把整行都标掉。`

// KeywordUserPrompt wraps the spoken lines for the keyword model.
func KeywordUserPrompt(lines []string) string {
	var b strings.Builder
	b.WriteString("下面是口播稿，每行一句。按规则输出关键词 JSON。\n\n# 口播稿\n")
	for _, line := range lines {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}
