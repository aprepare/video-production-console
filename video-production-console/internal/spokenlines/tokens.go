package spokenlines

import (
	"regexp"
	"strings"
)

// specialTokenPattern matches chat/LLM end-of-sequence markers that some
// models leak into the completion as literal text. <|eos|> is the one that
// has already reached 剪映 captions.
var specialTokenPattern = regexp.MustCompile(`(?i)<\|[^|]{1,32}\|>|</s>|<s>|</eos>|<eos>|\[/?EOS\]`)

// StripSpecialTokens removes model end-of-sequence markers from spoken copy
// so they cannot land in 口播稿, SRT, or on-screen captions.
func StripSpecialTokens(text string) string {
	if text == "" {
		return text
	}
	cleaned := specialTokenPattern.ReplaceAllString(text, "")
	return strings.TrimSpace(cleaned)
}
