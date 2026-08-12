package narration

import (
	"fmt"
	"strings"
	"unicode"
)

// Caption is one subtitle cue. Times are seconds from the start of the audio.
type Caption struct {
	Text  string  `json:"text"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// Options tunes segmentation and the quality gate. Zero values fall back to the
// production defaults.
type Options struct {
	MaxLineRunes     int
	MinDuration      float64
	MaxReadingSpeed  float64
	WarnReadingSpeed float64
	// MinCaptionRunes is the shortest caption kept on its own; anything shorter
	// is merged into a neighbour rather than left as an isolated fragment.
	MinCaptionRunes int
}

func (o Options) withDefaults() Options {
	if o.MaxLineRunes <= 0 {
		o.MaxLineRunes = 16
	}
	if o.MinDuration <= 0 {
		o.MinDuration = 0.5
	}
	if o.MaxReadingSpeed <= 0 {
		o.MaxReadingSpeed = 12
	}
	if o.WarnReadingSpeed <= 0 {
		o.WarnReadingSpeed = 9
	}
	if o.MinCaptionRunes <= 0 {
		o.MinCaptionRunes = 4
	}
	return o
}

// QCReport records why a caption set is or is not fit for delivery.
type QCReport struct {
	Pass bool `json:"pass"`
	// TextCoverage is the share of the script covered by the returned timings,
	// measured from the matching head and tail. A dropped sentence in the middle
	// shows up as a proportional shortfall.
	TextCoverage float64  `json:"text_coverage"`
	Failures     []string `json:"failures,omitempty"`
	Warnings     []string `json:"warnings,omitempty"`
}

var (
	strongTerminators = []rune{'。', '！', '？', '…', '!', '?', '.'}
	softSeparators    = []rune{'，', '、', '；', '：', ',', ';', ':'}
	// connectors read as the opening of the next thought, so a caption must not
	// end on one.
	connectors = []string{"比如说", "比如", "但是", "只是", "而且", "所以", "因为", "如果", "虽然", "不过", "然后", "于是", "并且", "也就是说"}
)

// Compose turns vendor word timings into delivery captions and grades them
// against the script. The script is the source of truth for caption text; the
// timings only decide where each cue starts and ends.
func Compose(script string, words []Word, opts Options) ([]Caption, QCReport, error) {
	opts = opts.withDefaults()
	if len(words) == 0 {
		return nil, QCReport{}, fmt.Errorf("no word timings to build captions from")
	}
	report := QCReport{TextCoverage: textCoverage(script, words)}
	groups := groupWords(words, opts)
	groups = mergeFragments(groups, opts)
	captions := make([]Caption, 0, len(groups))
	for _, group := range groups {
		captions = append(captions, Caption{
			Text:  strings.TrimSpace(joinWords(group)),
			Start: group[0].StartTime,
			End:   group[len(group)-1].EndTime,
		})
	}
	grade(captions, opts, &report)
	if report.TextCoverage < 1 {
		report.Failures = append(report.Failures, fmt.Sprintf(
			"spoken text covers only %.1f%% of the script; the narration may have skipped content",
			report.TextCoverage*100))
	}
	report.Pass = len(report.Failures) == 0
	return captions, report, nil
}

// groupWords cuts the stream at sentence boundaries first, then divides any
// sentence that is still too long into evenly sized cues. Splitting evenly
// rather than greedily filling each line avoids leaving a stranded tail such as
// a two-character final cue.
func groupWords(words []Word, opts Options) [][]Word {
	var groups [][]Word
	for _, segment := range splitSentences(words, opts) {
		groups = append(groups, balance(segment, opts.MaxLineRunes)...)
	}
	groups = pullLeadingPunctuation(groups)
	return pushTrailingConnectors(groups)
}

// splitSentences breaks after terminal punctuation, and after a comma once the
// line is long enough that the comma is a better break point than a mid-phrase
// one.
func splitSentences(words []Word, opts Options) [][]Word {
	softMin := opts.MaxLineRunes * 3 / 5
	var (
		segments [][]Word
		current  []Word
		length   int
	)
	for i, w := range words {
		current = append(current, w)
		length += len([]rune(w.Text))
		if i+1 >= len(words) || joinsAcrossASCII(w.Text, words[i+1].Text) {
			continue
		}
		if endsWithAny(w.Text, strongTerminators) ||
			(length >= softMin && endsWithAny(w.Text, softSeparators)) {
			segments = append(segments, current)
			current, length = nil, 0
		}
	}
	if len(current) > 0 {
		segments = append(segments, current)
	}
	return segments
}

// balance divides one sentence into the fewest cues that all fit the line limit,
// keeping them close to the same length. A cue may exceed the limit only when
// breaking would split a latin word or number.
func balance(segment []Word, maxRunes int) [][]Word {
	total := len([]rune(joinWords(segment)))
	if total <= maxRunes || len(segment) < 2 {
		return [][]Word{segment}
	}
	parts := (total + maxRunes - 1) / maxRunes
	target := (total + parts - 1) / parts
	var (
		groups  [][]Word
		current []Word
		length  int
	)
	for i, w := range segment {
		current = append(current, w)
		length += len([]rune(w.Text))
		if length < target || i+1 >= len(segment) {
			continue
		}
		if joinsAcrossASCII(w.Text, segment[i+1].Text) {
			continue
		}
		groups = append(groups, current)
		current, length = nil, 0
	}
	if len(current) > 0 {
		groups = append(groups, current)
	}
	return groups
}

// pullLeadingPunctuation keeps closing punctuation off the start of a line,
// which is the one hard rule of CJK line breaking.
func pullLeadingPunctuation(groups [][]Word) [][]Word {
	for i := 1; i < len(groups); i++ {
		for len(groups[i]) > 1 && isPunctuationOnly(groups[i][0].Text) {
			groups[i-1] = append(groups[i-1], groups[i][0])
			groups[i] = groups[i][1:]
		}
	}
	return groups
}

// pushTrailingConnectors moves a trailing connector onto the caption it
// introduces, so a cue never ends on "但是" or "比如说".
func pushTrailingConnectors(groups [][]Word) [][]Word {
	for i := 0; i+1 < len(groups); i++ {
		for len(groups[i]) > 1 {
			tail := groups[i][len(groups[i])-1]
			if !isConnector(tail.Text) {
				break
			}
			groups[i] = groups[i][:len(groups[i])-1]
			groups[i+1] = append([]Word{tail}, groups[i+1]...)
		}
	}
	return groups
}

// mergeFragments folds captions that are too short to read into the neighbour
// they belong with. A fragment that cannot be absorbed without pushing a line
// past the limit is left alone: an over-long cue is worse than a short one.
func mergeFragments(groups [][]Word, opts Options) [][]Word {
	for changed := true; changed; {
		changed = false
		for i := 0; i < len(groups) && len(groups) > 1; i++ {
			runes := len([]rune(joinWords(groups[i])))
			duration := groups[i][len(groups[i])-1].EndTime - groups[i][0].StartTime
			if runes >= opts.MinCaptionRunes && duration >= opts.MinDuration {
				continue
			}
			target := mergeTarget(groups, i, runes, opts.MaxLineRunes)
			if target < 0 {
				continue
			}
			if target > i {
				groups[target] = append(append([]Word{}, groups[i]...), groups[target]...)
			} else {
				groups[target] = append(groups[target], groups[i]...)
			}
			groups = append(groups[:i], groups[i+1:]...)
			changed = true
			break
		}
	}
	return groups
}

// mergeTarget picks the neighbour that can absorb a fragment, preferring the
// shorter one so cue lengths stay even. It returns -1 when neither fits.
func mergeTarget(groups [][]Word, i, runes, maxRunes int) int {
	best, bestLen := -1, 0
	for _, candidate := range []int{i - 1, i + 1} {
		if candidate < 0 || candidate >= len(groups) {
			continue
		}
		length := len([]rune(joinWords(groups[candidate])))
		if length+runes > maxRunes {
			continue
		}
		if best < 0 || length < bestLen {
			best, bestLen = candidate, length
		}
	}
	return best
}

// grade applies the delivery gates: readable pace, a floor on how briefly a cue
// may flash, and no overlapping cues.
func grade(captions []Caption, opts Options, report *QCReport) {
	for i, c := range captions {
		duration := c.End - c.Start
		units := readableUnits(c.Text)
		if duration < opts.MinDuration {
			report.Failures = append(report.Failures, fmt.Sprintf(
				"caption %d lasts %.2fs, below the %.2fs floor: %q", i+1, duration, opts.MinDuration, c.Text))
			continue
		}
		speed := float64(units) / duration
		switch {
		case speed > opts.MaxReadingSpeed:
			report.Failures = append(report.Failures, fmt.Sprintf(
				"caption %d reads at %.1f units/s, above the %.1f limit: %q", i+1, speed, opts.MaxReadingSpeed, c.Text))
		case speed > opts.WarnReadingSpeed:
			report.Warnings = append(report.Warnings, fmt.Sprintf(
				"caption %d reads at %.1f units/s: %q", i+1, speed, c.Text))
		}
		if i > 0 && c.Start < captions[i-1].End {
			report.Failures = append(report.Failures, fmt.Sprintf(
				"caption %d starts at %.3fs before caption %d ends at %.3fs", i+1, c.Start, i, captions[i-1].End))
		}
	}
}

// textCoverage compares the spoken tokens against the script. Because the vendor
// reports the original script rather than a normalized transcript, an exact match
// is the expected outcome and any shortfall means content was dropped.
func textCoverage(script string, words []Word) float64 {
	want := []rune(stripSpace(script))
	got := []rune(stripSpace(joinWords(words)))
	if len(want) == 0 {
		return 0
	}
	prefix := 0
	for prefix < len(want) && prefix < len(got) && want[prefix] == got[prefix] {
		prefix++
	}
	if prefix == len(want) && len(want) == len(got) {
		return 1
	}
	suffix := 0
	for suffix < len(want)-prefix && suffix < len(got)-prefix &&
		want[len(want)-1-suffix] == got[len(got)-1-suffix] {
		suffix++
	}
	matched := prefix + suffix
	if matched > len(want) {
		matched = len(want)
	}
	return float64(matched) / float64(len(want))
}

// joinWords rebuilds display text from tokens. The vendor reports latin words as
// separate tokens without the space between them, so the space is restored where
// two tokens meet on latin characters and "Claude Max" survives intact.
func joinWords(words []Word) string {
	var b strings.Builder
	for i, w := range words {
		if i > 0 && joinsAcrossASCII(words[i-1].Text, w.Text) {
			b.WriteByte(' ')
		}
		b.WriteString(w.Text)
	}
	return b.String()
}

func stripSpace(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

// readableUnits counts what a viewer actually reads, ignoring punctuation.
func readableUnits(s string) int {
	count := 0
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			continue
		}
		count++
	}
	return count
}

func endsWithAny(s string, set []rune) bool {
	runes := []rune(strings.TrimSpace(s))
	if len(runes) == 0 {
		return false
	}
	last := runes[len(runes)-1]
	for _, r := range set {
		if last == r {
			return true
		}
	}
	return false
}

func isPunctuationOnly(s string) bool {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return false
	}
	for _, r := range trimmed {
		if !unicode.IsPunct(r) && !unicode.IsSymbol(r) {
			return false
		}
	}
	return true
}

func isConnector(s string) bool {
	trimmed := strings.TrimSpace(s)
	for _, c := range connectors {
		if trimmed == c {
			return true
		}
	}
	return false
}

// joinsAcrossASCII reports whether breaking between two tokens would split a
// latin word or number such as "Claude Max" or a model name.
func joinsAcrossASCII(left, right string) bool {
	l := []rune(strings.TrimSpace(left))
	r := []rune(strings.TrimSpace(right))
	if len(l) == 0 || len(r) == 0 {
		return false
	}
	return isASCIIAlnum(l[len(l)-1]) && isASCIIAlnum(r[0])
}

func isASCIIAlnum(r rune) bool {
	return r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r))
}
