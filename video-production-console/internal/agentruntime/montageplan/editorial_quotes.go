package montageplan

import (
	"os"
	"sort"
	"strings"
	"unicode"
)

// quoteCandidates uses punctuation in the approved prose to avoid highlighting
// subtitle fragments such as "一家人先把账". Quotes must also match the caption
// stream exactly; narration/transcription disagreements are never guessed.
func quoteCandidates(ctx *planContext, full []highlightCandidate) []highlightCandidate {
	raw, err := os.ReadFile(ctx.scriptPath)
	if err != nil {
		return nil
	}
	clauses := strings.FieldsFunc(string(raw), func(r rune) bool { return strings.ContainsRune("，,。！？!?；;：:\n\r—", r) })
	seen := map[string]bool{}
	var out []highlightCandidate
	for _, clause := range clauses {
		text := strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) || strings.ContainsRune("“”\"‘’", r) {
				return -1
			}
			return r
		}, clause)
		if len([]rune(text)) < 5 || len([]rune(text)) > 18 || seen[text] {
			continue
		}
		score := editorialQuoteScore(text)
		if score == 0 {
			continue
		}
		for _, candidate := range full {
			if strings.Contains(candidate.Text, text) {
				out = append(out, highlightCandidate{candidate.CueIndex, text, candidate.StartS})
				seen[text] = true
				break
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return editorialQuoteScore(out[i].Text) > editorialQuoteScore(out[j].Text) })
	if len(out) > 72 {
		out = out[:72]
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartS < out[j].StartS })
	return out
}
func editorialQuoteScore(s string) int {
	score := 0
	for _, term := range []string{"面子", "日子", "金贵", "后悔", "苦说不出", "钱", "账", "身子", "风险", "别把", "看得懂", "风向", "存得", "花得", "盖房", "盖楼", "撑住", "青山"} {
		if strings.Contains(s, term) {
			score++
		}
	}
	for _, end := range []string{"的时候", "的东西", "的话", "那笔", "的事", "一句话", "是什么", "说到这儿", "这时候"} {
		if strings.HasSuffix(s, end) {
			return 0
		}
	}
	return score
}
func localQuotePicks(candidates []highlightCandidate) []highlightPick {
	// Pick the strongest complete clause per half-minute before enforcing gaps.
	best := map[int]highlightCandidate{}
	for _, c := range candidates {
		k := int(c.StartS / 30)
		old, ok := best[k]
		if !ok || editorialQuoteScore(c.Text) > editorialQuoteScore(old.Text) {
			best[k] = c
		}
	}
	var out []highlightPick
	for _, c := range best {
		out = append(out, highlightPick{c.CueIndex, c.Text, "keypoint"})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CueIndex < out[j].CueIndex })
	return out
}
