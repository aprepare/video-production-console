package montageplan

import (
	"sort"
	"strings"
	"unicode"

	"video-production-console/internal/spokenlines"
)

// spokenWarningStyle paints model-marked warning terms red; numbers keep the
// gold spokenKeywordStyle. Both keys live in montage-style-policy.v2.json.
const spokenWarningStyle = "spoken_warning_v1"

// Line-level SRT detection. 口播稿 lines carry at most 9 content runes (Han +
// letters); Arabic digits, %, and book quotes are free, so a display line like
// "从1.45%直接降到0.95%" is still 8 content runes. Word-level SRT cues average
// one or two runes. A cue whose content-rune budget exceeds MaxContentRunes
// is an old sentence-composer cut and must go through the gluing pipeline.
const (
	minAvgLineCueRunes = 3.0
	lineCueSnapGapUS   = 400_000
	lineCueMinPieceUS  = 50_000
)

// spokenDisplayRunes strips the punctuation a caption never shows, matching
// what splitSpokenLine does through its clause machinery. A decimal point
// between digits (0.05%) is part of the number and stays on screen.
func spokenDisplayRunes(text string) []rune {
	src := []rune(strings.TrimSpace(spokenlines.StripSpecialTokens(text)))
	out := make([]rune, 0, len(src))
	for i, r := range src {
		if (unicode.IsSpace(r) || isSpokenBreakRune(r)) && !isDecimalPoint(src, i) {
			continue
		}
		out = append(out, r)
	}
	return out
}

// lineLevelSRT reports whether the cues came from the 口播稿 composer (one
// finished caption line per cue) rather than a word-level vendor SRT.
func lineLevelSRT(cues []TimedSentence) bool {
	if len(cues) == 0 {
		return false
	}
	total := 0
	counted := 0
	for _, cue := range cues {
		display := spokenDisplayRunes(cue.Text)
		if len(display) == 0 {
			continue
		}
		// Count Han/letters only, matching the 口播稿 9-character budget.
		// Digits must not trip the whole SRT onto the glue+rewrap path.
		n := spokenlines.ContentCount(string(display))
		if n == 0 {
			n = len(display)
		}
		if n > spokenlines.MaxContentRunes {
			return false
		}
		total += n
		counted++
	}
	if counted == 0 {
		return false
	}
	return float64(total)/float64(counted) >= minAvgLineCueRunes
}

// spokenCaptionsFromLineCues maps 口播稿 line-level SRT cues one-to-one onto
// caption items. The 口播稿 model already chose these line breaks, so nothing
// is glued or re-split here — that is the whole point: a keyword can never be
// torn across two captions again.
func spokenCaptionsFromLineCues(cues []TimedSentence, narrationMS int64, keywords *lineKeywordIndex) ([]CaptionItem, []string) {
	if narrationMS <= 0 || len(cues) == 0 {
		return []CaptionItem{}, []string{"spoken_captions_empty: subtitle timings produced no on-screen lines"}
	}
	narrationUS := narrationMS * 1000
	items := make([]CaptionItem, 0, len(cues))
	var prevEndUS int64
	for _, cue := range cues {
		runes := spokenDisplayRunes(cue.Text)
		if len(runes) == 0 {
			continue
		}
		startUS := cue.StartMS * 1000
		endUS := cue.EndMS * 1000
		if startUS < prevEndUS {
			startUS = prevEndUS
		}
		if endUS > narrationUS {
			endUS = narrationUS
		}
		if endUS-startUS < lineCueMinPieceUS {
			continue
		}
		items = append(items, CaptionItem{
			Text:   string(runes),
			StartS: float64(startUS) / 1_000_000,
			EndS:   float64(endUS) / 1_000_000,
			Kind:   CaptionSpokenLine,
			Style:  "spoken_v1",
			Intro:  "none",
			Spans:  keywords.spansFor(runes),
		})
		prevEndUS = endUS
	}
	if len(items) == 0 {
		return items, []string{"spoken_captions_empty: subtitle timings produced no on-screen lines"}
	}
	// Close sub-0.4s gaps so the caption track reads continuously, the way
	// the QC draft does.
	for i := 0; i+1 < len(items); i++ {
		gapUS := int64((items[i+1].StartS - items[i].EndS) * 1_000_000)
		if gapUS > 0 && gapUS < lineCueSnapGapUS {
			items[i].EndS = items[i+1].StartS
		}
	}
	return items, []string{"spoken_captions_line_srt: 口播稿行级SRT一对一上屏"}
}

// lineKeywordIndex resolves model-marked keywords for a caption line. Lines
// are matched by their display text (punctuation stripped on both sides);
// duplicate lines consume entries in order.
type lineKeywordIndex struct {
	queues map[string][][]spokenlines.Keyword
}

func buildLineKeywordIndex(doc *spokenlines.KeywordDoc) *lineKeywordIndex {
	if doc == nil {
		return nil
	}
	index := &lineKeywordIndex{queues: make(map[string][][]spokenlines.Keyword, len(doc.Lines))}
	for _, line := range doc.Lines {
		key := string(spokenDisplayRunes(line.Line))
		if key == "" {
			continue
		}
		index.queues[key] = append(index.queues[key], line.Keywords)
	}
	return index
}

// spansFor builds the styled spans for one caption line. Model keywords win
// the limited slots (warnings red, numbers gold); with no model entry the
// local numeric/term fallback keeps working exactly as before.
func (index *lineKeywordIndex) spansFor(runes []rune) []CaptionSpan {
	if index == nil {
		return spokenKeywordSpans(runes, nil)
	}
	key := string(runes)
	queue, ok := index.queues[key]
	if !ok || len(queue) == 0 {
		return spokenKeywordSpans(runes, nil)
	}
	keywords := queue[0]
	index.queues[key] = queue[1:]
	spans := modelKeywordSpans(runes, keywords)
	if len(spans) == 0 {
		// The model looked at this line and marked nothing: keep it plain
		// instead of falling back to the blanket local term list.
		return nil
	}
	return spans
}

func modelKeywordSpans(runes []rune, keywords []spokenlines.Keyword) []CaptionSpan {
	text := string(runes)
	spans := make([]CaptionSpan, 0, spokenKeywordSpanLimit)
	for _, kw := range keywords {
		if len(spans) >= spokenKeywordSpanLimit {
			break
		}
		term := strings.TrimSpace(kw.Text)
		if term == "" {
			continue
		}
		found := strings.Index(text, term)
		if found < 0 {
			continue
		}
		start := len([]rune(text[:found]))
		end := start + len([]rune(term))
		if spansOverlap(spans, start, end) {
			continue
		}
		style := spokenKeywordStyle
		if kw.Kind == spokenlines.KeywordKindWarning {
			style = spokenWarningStyle
		}
		spans = append(spans, CaptionSpan{Start: start, End: end, Style: style})
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].Start < spans[j].Start })
	if len(spans) == 0 {
		return nil
	}
	return spans
}
