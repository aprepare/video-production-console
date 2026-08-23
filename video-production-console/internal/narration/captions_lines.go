package narration

import (
	"fmt"
	"strings"
	"unicode"

	"video-production-console/internal/spokenlines"
)

// ComposeFromSpokenLines times the LLM 口播稿 lines against vendor word
// timings. It does not re-break or merge those lines.
func ComposeFromSpokenLines(script string, lines []string, words []Word, opts Options) ([]Caption, QCReport, error) {
	opts = opts.withDefaults()
	if len(words) == 0 {
		return nil, QCReport{}, fmt.Errorf("no word timings to build captions from")
	}
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(spokenlines.StripSpecialTokens(line))
		if line == "" {
			continue
		}
		cleaned = append(cleaned, line)
	}
	if len(cleaned) == 0 {
		return nil, QCReport{}, fmt.Errorf("spoken lines are empty")
	}
	report := QCReport{TextCoverage: textCoverage(script, words)}
	captions, err := alignSpokenLines(cleaned, words)
	if err != nil {
		return nil, QCReport{}, err
	}
	gradeSpokenLines(captions, opts, &report)
	if report.TextCoverage < 1 {
		report.Failures = append(report.Failures, fmt.Sprintf(
			"spoken text covers only %.1f%% of the script; the narration may have skipped content",
			report.TextCoverage*100))
	}
	report.Pass = len(report.Failures) == 0
	return captions, report, nil
}

type timedRune struct {
	r          rune
	start, end float64
}

// chineseDigits maps Chinese numerals to ASCII digits. The 口播稿 prompt asks
// the model to digitize numbers (八月 → 8月), but the TTS reads the continuous
// script whose word timings keep the original spelling, so both must align.
var chineseDigits = map[rune]rune{
	'零': '0', '〇': '0', '一': '1', '二': '2', '两': '2', '三': '3',
	'四': '4', '五': '5', '六': '6', '七': '7', '八': '8', '九': '9',
}

func runesAlign(a, b rune) bool {
	if a == b {
		return true
	}
	if d, ok := chineseDigits[a]; ok && d == b {
		return true
	}
	if d, ok := chineseDigits[b]; ok && d == a {
		return true
	}
	return false
}

// alignSpokenLines times each 口播稿 line against the narration's rune stream
// using a global longest-common-subsequence alignment. The 口播稿 and the
// spoken script are near-identical texts, so the LCS finds the true position
// of every line even when the model re-spelled numbers, dropped a heading, or
// rewrote a phrase — a greedy forward scan would drift past the whole tail on
// the first such divergence.
func alignSpokenLines(lines []string, words []Word) ([]Caption, error) {
	stream := expandContentRunes(words)
	if len(stream) == 0 {
		return nil, fmt.Errorf("word timings contain no readable units")
	}
	var wants []rune
	var wantLine []int
	for li, line := range lines {
		for _, r := range contentRunes(line) {
			wants = append(wants, r)
			wantLine = append(wantLine, li)
		}
	}
	if len(wants) == 0 {
		return nil, fmt.Errorf("no captions aligned from spoken lines")
	}
	matches := lcsMatches(wants, stream)
	// Collect each line's matched stream indices, in order.
	lineIdx := make(map[int][]int, len(lines))
	for wi, si := range matches {
		if si >= 0 {
			li := wantLine[wi]
			lineIdx[li] = append(lineIdx[li], si)
		}
	}
	captions := make([]Caption, 0, len(lines))
	lastEnd := stream[0].start
	for li, line := range lines {
		if len(contentRunes(line)) == 0 {
			continue
		}
		idxs := clusterIndexes(lineIdx[li])
		var start, end float64
		if len(idxs) == 0 {
			// The narration has no trace of this line (a heavy model rewrite).
			// Pin a zero-length caption instead of failing the whole task; QC
			// grading reports it with the exact line text.
			start, end = lastEnd, lastEnd
		} else {
			start = stream[idxs[0]].start
			end = stream[idxs[len(idxs)-1]].end
			if start < lastEnd {
				start = lastEnd
			}
			if end < start {
				end = start
			}
		}
		lastEnd = end
		captions = append(captions, Caption{Text: line, Start: start, End: end})
	}
	if len(captions) == 0 {
		return nil, fmt.Errorf("no captions aligned from spoken lines")
	}
	// The narration tail belongs to the last line even when its final runes
	// were spelled differently from the script.
	if tail := stream[len(stream)-1].end; tail > captions[len(captions)-1].End {
		captions[len(captions)-1].End = tail
	}
	return captions, nil
}

// clusterIndexes keeps the longest tight run of matched stream indexes. Very
// common runes (的、了、人) can be claimed far from a line's true position;
// the dominant cluster is where the line was actually spoken.
func clusterIndexes(idxs []int) []int {
	if len(idxs) <= 1 {
		return idxs
	}
	const maxGap = 12
	bestStart, bestLen := 0, 1
	runStart := 0
	for k := 1; k < len(idxs); k++ {
		if idxs[k]-idxs[k-1] > maxGap {
			runStart = k
		}
		if length := k - runStart + 1; length > bestLen {
			bestStart, bestLen = runStart, length
		}
	}
	return idxs[bestStart : bestStart+bestLen]
}

// lcsMatches returns, for every wanted rune, the stream index it aligns to
// (or -1). Standard LCS dynamic program over runesAlign equality; both texts
// are a few thousand runes, so the quadratic table stays small. Above the
// cell cap it degrades to a windowed greedy scan rather than allocating
// hundreds of megabytes.
func lcsMatches(wants []rune, stream []timedRune) []int {
	n, m := len(wants), len(stream)
	out := make([]int, n)
	for i := range out {
		out[i] = -1
	}
	const maxCells = 64 << 20
	if (n+1)*(m+1) > maxCells {
		cursor := 0
		for i, w := range wants {
			limit := cursor + 40
			if limit > m {
				limit = m
			}
			for j := cursor; j < limit; j++ {
				if runesAlign(w, stream[j].r) {
					out[i] = j
					cursor = j + 1
					break
				}
			}
		}
		return out
	}
	dp := make([]int32, (n+1)*(m+1))
	for i := n - 1; i >= 0; i-- {
		row := i * (m + 1)
		next := (i + 1) * (m + 1)
		for j := m - 1; j >= 0; j-- {
			best := dp[row+j+1]
			if v := dp[next+j]; v > best {
				best = v
			}
			if runesAlign(wants[i], stream[j].r) {
				if v := dp[next+j+1] + 1; v > best {
					best = v
				}
			}
			dp[row+j] = best
		}
	}
	i, j := 0, 0
	for i < n && j < m {
		if runesAlign(wants[i], stream[j].r) && dp[i*(m+1)+j] == dp[(i+1)*(m+1)+j+1]+1 {
			out[i] = j
			i++
			j++
			continue
		}
		if dp[(i+1)*(m+1)+j] >= dp[i*(m+1)+j+1] {
			i++
		} else {
			j++
		}
	}
	return out
}

func expandContentRunes(words []Word) []timedRune {
	var out []timedRune
	for _, w := range words {
		rs := contentRunes(w.Text)
		if len(rs) == 0 {
			continue
		}
		dur := w.EndTime - w.StartTime
		if dur < 0 {
			dur = 0
		}
		step := dur / float64(len(rs))
		for i, r := range rs {
			out = append(out, timedRune{
				r:     r,
				start: w.StartTime + step*float64(i),
				end:   w.StartTime + step*float64(i+1),
			})
		}
	}
	return out
}

func contentRunes(s string) []rune {
	out := make([]rune, 0, len([]rune(s)))
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			continue
		}
		out = append(out, r)
	}
	return out
}

func gradeSpokenLines(captions []Caption, opts Options, report *QCReport) {
	for i, c := range captions {
		duration := c.End - c.Start
		if duration <= 0 {
			report.Failures = append(report.Failures, fmt.Sprintf(
				"caption %d has no duration: %q", i+1, c.Text))
			continue
		}
		if i > 0 && c.Start < captions[i-1].End-0.0001 {
			report.Failures = append(report.Failures, fmt.Sprintf(
				"caption %d starts at %.3fs before caption %d ends at %.3fs", i+1, c.Start, i, captions[i-1].End))
		}
		units := readableUnits(c.Text)
		if units == 0 {
			continue
		}
		speed := float64(units) / duration
		if speed > opts.MaxReadingSpeed {
			report.Warnings = append(report.Warnings, fmt.Sprintf(
				"caption %d reads at %.1f units/s: %q", i+1, speed, c.Text))
		}
	}
}
