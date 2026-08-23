package spokenlines

import (
	"fmt"
	"strings"
	"unicode"
)

const (
	MaxContentRunes = 9
	lineSuffix      = " \n"
)

var protectedPhrases = []string{
	"《财富觉醒方法论》",
	"财富觉醒方法论",
}

// Format turns model output (or a pasted script) into the console 口播稿:
// one spoken unit per line, at most 9 content characters, no blank lines,
// each line ending with a trailing space before the newline.
func Format(raw string) (string, error) {
	text := strings.TrimSpace(stripCodeFence(raw))
	text = StripSpecialTokens(text)
	if text == "" {
		return "", fmt.Errorf("spoken lines are empty")
	}
	if script, ok := extractSpokenJSON(text); ok {
		text = StripSpecialTokens(script)
	}
	text = digitize(text)
	var lines []string
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(StripSpecialTokens(line))
		if line == "" {
			continue
		}
		lines = append(lines, wrapLine(line)...)
	}
	if len(lines) == 0 {
		return "", fmt.Errorf("spoken lines are empty")
	}
	lines = rebalanceLines(lines)
	var b strings.Builder
	for _, line := range lines {
		b.WriteString(strings.TrimSpace(line))
		b.WriteString(lineSuffix)
	}
	return b.String(), nil
}

// Lines returns the spoken units without trailing spaces.
func Lines(formatted string) []string {
	out := make([]string, 0)
	for _, line := range strings.Split(strings.ReplaceAll(formatted, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}

// SpeechText joins spoken lines for TTS. CJK lines concatenate with no extra
// space; the trailing spaces from Format are stripped first.
func SpeechText(formatted string) string {
	return strings.Join(Lines(formatted), "")
}

// SpeechFromScript prepares the continuous script for TTS. Years, percents,
// and large numbers become digits so the engine reads them right, but the
// writer's punctuation and paragraphing stay: TTS prosody follows the original
// sentences instead of the LLM's re-broken 口播稿 lines.
func SpeechFromScript(raw string) string {
	return digitize(strings.TrimSpace(raw))
}

// attachingParticles must not begin a subtitle line: they attach to the word
// on the previous line (的人、的那天、了、吗…) and read broken on screen.
// 地 and 得 are deliberately absent — at a line start they are usually real
// words (土地、地段、得卖给国家), not particles.
var attachingParticles = map[rune]bool{
	'的': true, '了': true, '着': true,
	'吗': true, '吧': true, '呢': true, '嘛': true, '啊': true, '呀': true,
}

func startsWithParticle(line string) bool {
	for _, r := range line {
		return attachingParticles[r]
	}
	return false
}

// rebalanceLines repairs the model's worst break choices deterministically,
// but only when the merged line still fits the 9-rune cap: a line must not
// start with an attaching particle, a single content rune must not stand
// alone, and a line ending in 的 pulls its noun back up. Offenders that would
// exceed the cap are left for the prompt's break-quality rules — merging them
// correctly would need real word segmentation.
func rebalanceLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if len(out) > 0 {
			prev := out[len(out)-1]
			merged := prev + line
			// A dangling 的 only pulls back a short noun fragment (东西、零件):
			// a longer next line is usually a brand-new sentence after a
			// sentence-final 的 (…发家的 / 你说他们脑子) and must stay split.
			badBreak := startsWithParticle(line) || contentCount(line) <= 1 ||
				(strings.HasSuffix(prev, "的") && contentCount(line) <= 2)
			if badBreak && contentCount(merged) <= MaxContentRunes {
				out[len(out)-1] = merged
				continue
			}
		}
		out = append(out, line)
	}
	return out
}

func wrapLine(line string) []string {
	if contentCount(line) <= MaxContentRunes {
		return []string{line}
	}
	tokens := tokenize(line)
	var (
		out     []string
		current strings.Builder
		count   int
	)
	flush := func() {
		if current.Len() == 0 {
			return
		}
		out = append(out, current.String())
		current.Reset()
		count = 0
	}
	for _, token := range tokens {
		n := contentCount(token)
		if current.Len() > 0 && count+n > MaxContentRunes {
			flush()
		}
		current.WriteString(token)
		count += n
	}
	flush()
	if len(out) == 0 {
		return []string{line}
	}
	return out
}

func tokenize(line string) []string {
	runes := []rune(line)
	var tokens []string
	for i := 0; i < len(runes); {
		if phrase, n := matchProtected(runes[i:]); n > 0 {
			tokens = append(tokens, phrase)
			i += n
			continue
		}
		if n := digitAtomLen(runes[i:]); n > 0 {
			tokens = append(tokens, string(runes[i:i+n]))
			i += n
			continue
		}
		start := i
		i++
		for i < len(runes) && isPunctuationRune(runes[i]) {
			i++
		}
		tokens = append(tokens, string(runes[start:i]))
	}
	return tokens
}

func matchProtected(runes []rune) (string, int) {
	rest := string(runes)
	for _, phrase := range protectedPhrases {
		if strings.HasPrefix(rest, phrase) {
			return phrase, len([]rune(phrase))
		}
	}
	return "", 0
}

func digitAtomLen(runes []rune) int {
	if len(runes) == 0 || !unicode.IsDigit(runes[0]) {
		return 0
	}
	n := 0
	for n < len(runes) && (unicode.IsDigit(runes[n]) || runes[n] == '.') {
		n++
	}
	if n < len(runes) {
		switch runes[n] {
		case '年', '%', '％', '万', '亿':
			n++
			if n < len(runes) && runes[n-1] == '万' && runes[n] == '亿' {
				n++
			}
		}
	}
	return n
}

func isPunctuationRune(r rune) bool {
	if unicode.IsSpace(r) {
		return false
	}
	if unicode.IsPunct(r) || unicode.IsSymbol(r) {
		return true
	}
	switch r {
	case '，', '。', '！', '？', '、', '；', '：', '—', '…', '·', '%', '％':
		return true
	default:
		return false
	}
}

func stripCodeFence(raw string) string {
	text := strings.TrimSpace(raw)
	if !strings.HasPrefix(text, "```") {
		return text
	}
	text = strings.TrimPrefix(text, "```")
	if nl := strings.Index(text, "\n"); nl >= 0 {
		text = text[nl+1:]
	}
	text = strings.TrimSuffix(text, "```")
	return strings.TrimSpace(text)
}

func extractSpokenJSON(text string) (string, bool) {
	if !strings.HasPrefix(strings.TrimSpace(text), "{") {
		return "", false
	}
	// Minimal extraction so a model that wraps the sheet in JSON still lands.
	for _, key := range []string{`"spoken_script"`, `"spoken_lines"`} {
		idx := strings.Index(text, key)
		if idx < 0 {
			continue
		}
		rest := text[idx+len(key):]
		colon := strings.Index(rest, ":")
		if colon < 0 {
			continue
		}
		rest = strings.TrimSpace(rest[colon+1:])
		if !strings.HasPrefix(rest, `"`) {
			continue
		}
		var b strings.Builder
		escaped := false
		for _, r := range rest[1:] {
			if escaped {
				if r == 'n' {
					b.WriteByte('\n')
				} else {
					b.WriteRune(r)
				}
				escaped = false
				continue
			}
			if r == '\\' {
				escaped = true
				continue
			}
			if r == '"' {
				return strings.TrimSpace(b.String()), true
			}
			b.WriteRune(r)
		}
	}
	return "", false
}
