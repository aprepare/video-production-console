package montageplan

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode"
)

// CaptionKind classifies why a sentence deserves a highlight caption.
// The values match the skill parser's CAPTION_KINDS enum exactly.
type CaptionKind string

const (
	CaptionNumber       CaptionKind = "number"
	CaptionTurningPoint CaptionKind = "turning_point"
	CaptionConclusion   CaptionKind = "conclusion"
	CaptionSpokenLine   CaptionKind = "spoken"
)

// TimedSentence is one narration sentence with SRT-derived timing.
type TimedSentence struct {
	StartMS int64
	EndMS   int64
	Text    string
}

// visualSegment groups sentences into the 6-10s units the planner paces
// shots against. Highlight-worthy sentences stand alone and carry their kind.
type visualSegment struct {
	StartMS int64
	EndMS   int64
	Text    string
	Kind    CaptionKind
}

const (
	visualSegmentMinMS = int64(6000)
	visualSegmentMaxMS = int64(10000)

	captionItemMinMS = int64(2000)
	// The skill parser caps one highlight caption at 4s (CAPTION_ITEM_MAX_S),
	// stricter than the plan document's 5s; the parser wins.
	captionItemMaxMS = int64(4000)
	// Phrase-level SRT cues from the console TTS pipeline last ~2-4s and
	// usually have no 。！？. Treat those as finished sentences so they are
	// not glued into one 0s-start blob.
	phraseCueMinMS = int64(1800)

	captionCoverageMin = 0.15
	captionCoverageMax = 0.25
)

// sentenceBoundaryRunes close a sentence when a word-level SRT entry ends
// with one of them; sentence-level entries close themselves the same way.
const sentenceBoundaryRunes = "。！？!?；;"

var conclusionMarkers = []string{"所以", "因此", "记住", "结论", "最后"}
var turningPointMarkers = []string{"但是", "然而", "真正", "其实", "可惜"}

// parseSRTSentences reads word-level, phrase-level, or sentence-level SRT.
// Word-level cues stay glued until 。！？; cues longer than phraseCueMinMS
// are treated as finished phrases even without punctuation.
func parseSRTSentences(r io.Reader) ([]TimedSentence, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read srt: %w", err)
	}
	text := strings.ReplaceAll(string(stripBOM(raw)), "\r\n", "\n")
	lines := strings.Split(text, "\n")

	type srtEntry struct {
		startMS, endMS int64
		text           string
	}
	entries := make([]srtEntry, 0, 64)
	for i := 0; i < len(lines); {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			i++
			continue
		}
		if isAllDigits(line) && i+1 < len(lines) && strings.Contains(lines[i+1], "-->") {
			i++
			line = strings.TrimSpace(lines[i])
		}
		if !strings.Contains(line, "-->") {
			return nil, fmt.Errorf("srt entry %d has no timing line: %q", len(entries)+1, line)
		}
		startMS, endMS, err := parseSRTTimeRange(line)
		if err != nil {
			return nil, fmt.Errorf("srt entry %d: %w", len(entries)+1, err)
		}
		i++
		var parts []string
		for i < len(lines) {
			part := strings.TrimSpace(lines[i])
			if part == "" {
				break
			}
			parts = append(parts, part)
			i++
		}
		entries = append(entries, srtEntry{startMS: startMS, endMS: endMS, text: strings.Join(parts, "")})
	}

	sentences := make([]TimedSentence, 0, len(entries))
	var curStart, curEnd int64
	var builder strings.Builder
	open := false
	for _, entry := range entries {
		if entry.text == "" {
			continue
		}
		if !open {
			curStart = entry.startMS
			builder.Reset()
			open = true
		}
		curEnd = entry.endMS
		builder.WriteString(entry.text)
		if endsWithSentenceBoundary(entry.text) || entry.endMS-entry.startMS >= phraseCueMinMS {
			sentences = append(sentences, TimedSentence{StartMS: curStart, EndMS: curEnd, Text: builder.String()})
			open = false
		}
	}
	if open && strings.TrimSpace(builder.String()) != "" {
		sentences = append(sentences, TimedSentence{StartMS: curStart, EndMS: curEnd, Text: builder.String()})
	}
	return sentences, nil
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func parseSRTTimeRange(line string) (startMS, endMS int64, err error) {
	parts := strings.SplitN(line, "-->", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid timing line %q", line)
	}
	if startMS, err = parseSRTTimestamp(parts[0]); err != nil {
		return 0, 0, err
	}
	if endMS, err = parseSRTTimestamp(parts[1]); err != nil {
		return 0, 0, err
	}
	if endMS < startMS {
		return 0, 0, fmt.Errorf("timing line %q ends before it starts", line)
	}
	return startMS, endMS, nil
}

func parseSRTTimestamp(value string) (int64, error) {
	value = strings.ReplaceAll(strings.TrimSpace(value), ".", ",")
	var h, m, s, ms int64
	if _, err := fmt.Sscanf(value, "%d:%d:%d,%d", &h, &m, &s, &ms); err != nil {
		return 0, fmt.Errorf("invalid srt timestamp %q", value)
	}
	return ((h*60+m)*60+s)*1000 + ms, nil
}

func endsWithSentenceBoundary(text string) bool {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) == 0 {
		return false
	}
	return strings.ContainsRune(sentenceBoundaryRunes, runes[len(runes)-1])
}

// classifyCaptionKind tags a sentence for highlight captions. Conclusions win
// over turning points, which win over plain numbers, matching the selection
// weights below.
func classifyCaptionKind(text string) (CaptionKind, bool) {
	for _, marker := range conclusionMarkers {
		if strings.Contains(text, marker) {
			return CaptionConclusion, true
		}
	}
	for _, marker := range turningPointMarkers {
		if strings.Contains(text, marker) {
			return CaptionTurningPoint, true
		}
	}
	for _, r := range text {
		if unicode.IsDigit(r) {
			return CaptionNumber, true
		}
	}
	return "", false
}

func captionWeight(kind CaptionKind) int {
	switch kind {
	case CaptionConclusion:
		return 4
	case CaptionTurningPoint:
		return 3
	case CaptionNumber:
		return 2
	default:
		return 0
	}
}

// buildVisualSegments groups plain sentences into 6-10s pacing units and
// isolates every highlight-worthy sentence into its own tagged segment.
func buildVisualSegments(sentences []TimedSentence) []visualSegment {
	segments := make([]visualSegment, 0, len(sentences))
	var cur *visualSegment
	flush := func() {
		if cur != nil {
			segments = append(segments, *cur)
			cur = nil
		}
	}
	for _, sentence := range sentences {
		if kind, special := classifyCaptionKind(sentence.Text); special {
			flush()
			segments = append(segments, visualSegment{
				StartMS: sentence.StartMS, EndMS: sentence.EndMS,
				Text: sentence.Text, Kind: kind,
			})
			continue
		}
		if cur != nil {
			curDur := cur.EndMS - cur.StartMS
			newDur := sentence.EndMS - cur.StartMS
			if curDur >= visualSegmentMinMS || newDur > visualSegmentMaxMS {
				flush()
			}
		}
		if cur == nil {
			cur = &visualSegment{StartMS: sentence.StartMS, EndMS: sentence.EndMS, Text: sentence.Text}
			continue
		}
		cur.EndMS = sentence.EndMS
		cur.Text += sentence.Text
	}
	flush()
	return segments
}

// selectHighlightCaptions picks the sparse highlight captions for a plan:
// weight order conclusion > turning point > number, stable tie-breaks, one
// caption at a time, and a union coverage of 15%-25% of the narration.
// Insufficient candidates yield a caption_coverage_below_target warning
// instead of an error; CaptionOff always returns an empty list.
func selectHighlightCaptions(sentences []TimedSentence, narrationMS int64, mode CaptionMode) ([]CaptionItem, []string) {
	if mode == CaptionOff || narrationMS <= 0 {
		return []CaptionItem{}, nil
	}
	type candidate struct {
		weight         int
		startMS, endMS int64
		text, norm     string
		kind           CaptionKind
		order          int
	}
	candidates := make([]candidate, 0, len(sentences))
	for i, sentence := range sentences {
		kind, ok := classifyCaptionKind(sentence.Text)
		if !ok {
			continue
		}
		start := sentence.StartMS
		end := sentence.EndMS
		if end > start+captionItemMaxMS {
			end = start + captionItemMaxMS
		}
		if end < start+captionItemMinMS {
			end = start + captionItemMinMS
		}
		if end > narrationMS {
			end = narrationMS
		}
		if end-start < captionItemMinMS {
			continue
		}
		candidates = append(candidates, candidate{
			weight: captionWeight(kind), startMS: start, endMS: end,
			text: sentence.Text, norm: strings.Join(strings.Fields(sentence.Text), ""),
			kind: kind, order: i,
		})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.weight != b.weight {
			return a.weight > b.weight
		}
		if a.startMS != b.startMS {
			return a.startMS < b.startMS
		}
		if a.norm != b.norm {
			return a.norm < b.norm
		}
		return a.order < b.order
	})

	// Overlapping candidates resolve by weight before any coverage counting.
	kept := make([]candidate, 0, len(candidates))
	for _, c := range candidates {
		overlaps := false
		for _, k := range kept {
			if c.startMS < k.endMS && c.endMS > k.startMS {
				overlaps = true
				break
			}
		}
		if !overlaps {
			kept = append(kept, c)
		}
	}
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].startMS < kept[j].startMS })

	items := make([]CaptionItem, 0, len(kept))
	covered := int64(0)
	for _, c := range kept {
		if float64(covered)/float64(narrationMS) >= captionCoverageMin {
			break
		}
		duration := c.endMS - c.startMS
		if float64(covered+duration)/float64(narrationMS) > captionCoverageMax {
			continue
		}
		covered += duration
		items = append(items, CaptionItem{
			Text:   c.text,
			StartS: float64(c.startMS) / 1000,
			EndS:   float64(c.endMS) / 1000,
			Kind:   c.kind,
			Style:  "highlight_v1",
			Intro:  "none",
		})
	}
	var warnings []string
	if coverage := float64(covered) / float64(narrationMS); coverage < captionCoverageMin {
		warnings = append(warnings, fmt.Sprintf(
			"caption_coverage_below_target: coverage=%.4f target_min=%.4f", coverage, captionCoverageMin))
	}
	return items, warnings
}

// intervalCoverage reports the union length of the caption intervals as a
// fraction of the narration duration.
func intervalCoverage(items []CaptionItem, narrationMS int64) float64 {
	if narrationMS <= 0 || len(items) == 0 {
		return 0
	}
	intervals := make([][2]float64, 0, len(items))
	for _, item := range items {
		if item.EndS > item.StartS {
			intervals = append(intervals, [2]float64{item.StartS, item.EndS})
		}
	}
	sort.Slice(intervals, func(i, j int) bool {
		if intervals[i][0] != intervals[j][0] {
			return intervals[i][0] < intervals[j][0]
		}
		return intervals[i][1] < intervals[j][1]
	})
	total := 0.0
	cursorEnd := -1.0
	started := false
	for _, iv := range intervals {
		switch {
		case !started || iv[0] >= cursorEnd:
			total += iv[1] - iv[0]
			cursorEnd = iv[1]
			started = true
		case iv[1] > cursorEnd:
			total += iv[1] - cursorEnd
			cursorEnd = iv[1]
		}
	}
	return total / (float64(narrationMS) / 1000)
}
