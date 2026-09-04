package spokenlines

import "strings"

// 切句只依赖局部上下文，所以长文案可以按句子边界切成几块并发交给模型，
// 再按原顺序拼回——比单次长生成快好几倍，且每块的断行质量不受影响。

const (
	// ChunkTargetRunes 是单块目标长度；短于 ChunkMinTotalRunes 的文案不切。
	ChunkTargetRunes   = 500
	ChunkMinTotalRunes = 700
	MaxChunks          = 6
)

// SplitForParallel cuts the script into at most MaxChunks pieces at sentence
// boundaries (。！？；and newlines). A script shorter than ChunkMinTotalRunes
// comes back as a single piece.
func SplitForParallel(script string) []string {
	text := strings.TrimSpace(strings.ReplaceAll(script, "\r\n", "\n"))
	if text == "" {
		return nil
	}
	runes := []rune(text)
	if len(runes) < ChunkMinTotalRunes {
		return []string{text}
	}
	chunks := (len(runes) + ChunkTargetRunes - 1) / ChunkTargetRunes
	if chunks > MaxChunks {
		chunks = MaxChunks
	}
	target := (len(runes) + chunks - 1) / chunks

	var out []string
	var current []rune
	flush := func() {
		if piece := strings.TrimSpace(string(current)); piece != "" {
			out = append(out, piece)
		}
		current = current[:0]
	}
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		current = append(current, r)
		if len(current) < target {
			continue
		}
		if isSentenceEnd(r) || r == '\n' {
			// Closing quotes / brackets stay with the sentence they close.
			for i+1 < len(runes) && isClosingPunct(runes[i+1]) {
				i++
				current = append(current, runes[i])
			}
			flush()
		}
	}
	flush()
	if len(out) == 0 {
		return []string{text}
	}
	return out
}

func isSentenceEnd(r rune) bool {
	switch r {
	case '。', '！', '？', '；', '!', '?', ';':
		return true
	}
	return false
}

func isClosingPunct(r rune) bool {
	switch r {
	case '”', '』', '」', '）', ')', '》', '"':
		return true
	}
	return false
}
