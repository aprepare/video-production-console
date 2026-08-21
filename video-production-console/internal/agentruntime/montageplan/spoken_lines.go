package montageplan

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// Spoken caption lines follow the user-approved QC draft: no punctuation and one
// on-screen row per caption. At the QC size a rune costs about 6.1px per size
// unit, so 10 runes at size 17 is the widest line the 1080 canvas holds; longer
// captions would let Jianying pick the wrap point and break a word in half.
const (
	spokenLineMaxRunes = spokenLineModelMaxRunes
	spokenLineMinRunes = 4
	spokenKeywordStyle = "spoken_keyword_v1"
)

const (
	boardTitleSize    = 16.0
	boardSubtitleSize = 9.2
	// A board title shorter than this reads as a fragment ("房子", "短");
	// too-short copy falls back to the default board pair instead.
	boardTitleMinRunes = 4
	boardTitleMaxRunes = 15
	// In the QC draft 8 runes at size 16 fill about 0.78 of the 1080 canvas, so
	// one rune costs ~0.0061 of the width per size unit. 0.86 is the widest a
	// board line may be before it touches the frame margin, which leaves a
	// rune*size budget of 0.86/0.0061.
	boardWidthBudget = 141.0
)

// boardTextSize keeps the QC size for copy that already fits and otherwise
// shrinks just enough to keep a longer title on one line.
func boardTextSize(runes int, base float64) float64 {
	if runes <= 0 {
		return base
	}
	limit := math.Round(boardWidthBudget/float64(runes)*10) / 10
	if limit < base {
		return limit
	}
	return base
}

var (
	spokenSoftBreaks   = []rune{'，', '、', '；', '：', ',', ';', ':'}
	spokenStrongBreaks = []rune{'。', '！', '？', '…', '!', '?', '.'}
	// A line must not end on a character that only introduces what follows.
	spokenTrailingForbidden = []rune("往把给从对被让和跟与在为向朝由自到的地得之比每再更还就也很太都要能会可将是有老新大小多少好高低长短那这哪")
	// A line must not start on a suffix, a measure word, or a dangling particle.
	spokenLeadingForbidden = []rune("率性度额量费税价员者们化感力型式的了着过吗呢吧啊么呀》〉）)]】里内中上下间外到约些市")
	spokenNumerals         = []rune("零一二三四五六七八九十百千万亿两半点")
	spokenUnits            = []rune("套户个人年月日成倍米元块次条家座平方％%")
	spokenNumeralPrefixes  = []rune("第之")
	// Terms that a line break must never cut in half. Domain nouns first, then
	// the connectives and adverbs that read as one word.
	spokenInseparable = []string{
		"百分之", "法拍房", "城镇化率", "租售比", "房产税", "消化周期", "评估价", "中指研究院", "国家统计局",
		"财富觉醒方法论", "主页橱窗", "橱窗", "城市", "接触", "大约", "接近", "超过", "差不多", "已经", "正在",
		"想法", "首付", "月供", "断供", "库存", "挂牌", "成交", "同比", "杠杆",
		"也就是说", "比如说", "而且", "所以", "因为", "如果", "虽然", "不过", "然后", "于是", "并且", "但是",
		"只是", "就是", "可是", "以及", "包括",
	}
	// Keyword vocabulary for the enlarged spans: finance nouns plus the verbs
	// and states that carry the pressure of the script.
	spokenKeywordTerms = []string{
		"法拍房", "房产税", "城镇化率", "租售比", "房贷", "断供", "停供", "库存", "存量", "腰斩", "接盘",
		"首付", "月供", "挂牌", "成交", "杠杆", "拍卖台", "刚改盘", "租售", "财政", "债务", "评估价",
		"喘不过气", "扛不住", "压垮", "要命", "缩水", "甩卖", "止损", "反弹", "预亏", "洗牌", "断崖",
		"没人接手", "走不通", "趴下", "睡踏实", "重新洗牌", "来不及", "来得及", "收紧", "划算", "失衡",
	}
)

type runeSpan struct{ start, end int }

// spokenClause is one punctuation-delimited run of speech. It keeps source rune
// offsets so caption timings stay tied to the original sentence window even
// after the punctuation is dropped from the on-screen text.
type spokenClause struct {
	spans        []runeSpan
	strongBefore bool
}

func (c spokenClause) runes(src []rune) []rune {
	out := make([]rune, 0, spokenLineMaxRunes)
	for _, span := range c.spans {
		out = append(out, src[span.start:span.end]...)
	}
	return out
}

func (c spokenClause) length() int {
	total := 0
	for _, span := range c.spans {
		total += span.end - span.start
	}
	return total
}

func (c spokenClause) firstStart() int { return c.spans[0].start }
func (c spokenClause) lastEnd() int    { return c.spans[len(c.spans)-1].end }

// splitSpokenLine turns one SRT sentence into on-screen caption lines. Cuts land
// on punctuation first; a clause that is still too long is divided at the
// position closest to the middle that does not break a word, a number with its
// measure word, a book title, or a connective.
func splitSpokenLine(text string, maxRunes int) []spokenPiece {
	src := []rune(strings.TrimSpace(text))
	if len(src) == 0 {
		return nil
	}
	if maxRunes <= 0 {
		maxRunes = spokenLineMaxRunes
	}
	clauses := mergeSpokenClauses(spokenClauses(src), maxRunes)
	pieces := make([]spokenPiece, 0, len(clauses))
	total := float64(len(src))
	for _, clause := range clauses {
		for _, part := range splitLongClause(clause, src, maxRunes) {
			runes := part.runes(src)
			pieces = append(pieces, spokenPiece{
				text:      string(runes),
				startFrac: float64(part.firstStart()) / total,
				endFrac:   float64(part.lastEnd()) / total,
				spans:     spokenKeywordSpans(runes, nil),
			})
		}
	}
	// Keep the lines back to back so the caption track has no gaps, the way the
	// QC draft reads.
	for i := range pieces {
		if i == 0 {
			pieces[i].startFrac = 0
		} else {
			pieces[i-1].endFrac = pieces[i].startFrac
		}
	}
	if len(pieces) > 0 {
		pieces[len(pieces)-1].endFrac = 1
	}
	return pieces
}

func spokenClauses(src []rune) []spokenClause {
	var (
		clauses []spokenClause
		start   = -1
		strong  bool
	)
	flush := func(end int) {
		if start < 0 {
			return
		}
		clauses = append(clauses, spokenClause{spans: []runeSpan{{start: start, end: end}}, strongBefore: strong})
		start, strong = -1, false
	}
	for i, r := range src {
		if (isSpokenBreakRune(r) || unicode.IsSpace(r)) && !isDecimalPoint(src, i) {
			flush(i)
			if containsRune(spokenStrongBreaks, r) {
				strong = true
			}
			continue
		}
		if start < 0 {
			start = i
		}
	}
	flush(len(src))
	return clauses
}

// isDecimalPoint reports whether the '.' at src[i] joins two ASCII digits
// (0.05%): that dot is part of a number, never a sentence-ending period, and
// must survive punctuation stripping so captions keep reading 0.05%.
func isDecimalPoint(src []rune, i int) bool {
	if i < 0 || i >= len(src) || src[i] != '.' {
		return false
	}
	if i == 0 || i+1 >= len(src) {
		return false
	}
	return src[i-1] >= '0' && src[i-1] <= '9' && src[i+1] >= '0' && src[i+1] <= '9'
}

// mergeSpokenClauses folds a clause that is too short to read into its
// neighbour. It never merges across a sentence end, so one caption never mixes
// the tail of one sentence with the head of the next.
func mergeSpokenClauses(clauses []spokenClause, maxRunes int) []spokenClause {
	out := make([]spokenClause, 0, len(clauses))
	for _, clause := range clauses {
		if len(out) > 0 && !clause.strongBefore {
			prev := &out[len(out)-1]
			short := clause.length() < spokenLineMinRunes || prev.length() < spokenLineMinRunes
			if short && prev.length()+clause.length() <= maxRunes {
				prev.spans = append(prev.spans, clause.spans...)
				continue
			}
		}
		out = append(out, clause)
	}
	return out
}

func splitLongClause(clause spokenClause, src []rune, maxRunes int) []spokenClause {
	if clause.length() <= maxRunes {
		return []spokenClause{clause}
	}
	runes := clause.runes(src)
	var parts []spokenClause
	base := 0
	for len(runes)-base > maxRunes {
		remaining := len(runes) - base
		chunks := (remaining + maxRunes - 1) / maxRunes
		target := base + int(math.Round(float64(remaining)/float64(chunks)))
		lower := base + spokenLineMinRunes
		upper := base + maxRunes
		if limit := len(runes) - spokenLineMinRunes; upper > limit {
			upper = limit
		}
		cut := nearestSafeCut(runes, target, lower, upper)
		if cut <= base {
			cut = base + maxRunes
			if cut > len(runes) {
				cut = len(runes)
			}
		}
		parts = append(parts, sliceClause(clause, base, cut))
		base = cut
	}
	return append(parts, sliceClause(clause, base, len(runes)))
}

// nearestSafeCut walks outward from the ideal midpoint and returns the first
// break that keeps every word intact, or 0 when the clause has none.
func nearestSafeCut(runes []rune, target, lower, upper int) int {
	if lower > upper {
		return 0
	}
	for delta := 0; delta <= upper-lower; delta++ {
		for _, candidate := range [2]int{target - delta, target + delta} {
			if candidate < lower || candidate > upper {
				continue
			}
			if !breaksSpokenWord(runes, candidate) {
				return candidate
			}
		}
	}
	return 0
}

func breaksSpokenWord(runes []rune, at int) bool {
	if at <= 0 || at >= len(runes) {
		return true
	}
	prev, next := runes[at-1], runes[at]
	prevNumeric := containsRune(spokenNumerals, prev) || containsRune(spokenUnits, prev)
	nextNumeric := containsRune(spokenNumerals, next) || containsRune(spokenUnits, next) || containsRune(spokenNumeralPrefixes, next)
	if prevNumeric && nextNumeric {
		return true
	}
	if containsRune(spokenNumeralPrefixes, prev) && containsRune(spokenNumerals, next) {
		return true
	}
	if containsRune(spokenTrailingForbidden, prev) || containsRune(spokenLeadingForbidden, next) {
		return true
	}
	if isASCIIWordRune(prev) && isASCIIWordRune(next) {
		return true
	}
	if insideBookTitle(runes, at) {
		return true
	}
	text := string(runes)
	head := len(string(runes[:at]))
	for _, term := range spokenInseparable {
		for offset := 0; ; {
			found := strings.Index(text[offset:], term)
			if found < 0 {
				break
			}
			found += offset
			if found < head && head < found+len(term) {
				return true
			}
			offset = found + len(term)
		}
	}
	return false
}

func insideBookTitle(runes []rune, at int) bool {
	depth := 0
	for _, r := range runes[:at] {
		switch r {
		case '《', '〈':
			depth++
		case '》', '〉':
			if depth > 0 {
				depth--
			}
		}
	}
	return depth > 0
}

func sliceClause(clause spokenClause, from, to int) spokenClause {
	var (
		spans  []runeSpan
		cursor int
	)
	for _, span := range clause.spans {
		length := span.end - span.start
		start, end := cursor, cursor+length
		cursor = end
		if end <= from || start >= to {
			continue
		}
		lo := span.start
		if from > start {
			lo += from - start
		}
		hi := span.end
		if to < end {
			hi -= end - to
		}
		if hi > lo {
			spans = append(spans, runeSpan{start: lo, end: hi})
		}
	}
	return spokenClause{spans: spans, strongBefore: clause.strongBefore}
}

// spokenKeywordSpans marks the quantities and finance terms that carry the line.
// Quantities win the limited slots, then the longest matching term, so a caption
// never turns into a wall of gold.
const spokenKeywordSpanLimit = 2

func spokenKeywordSpans(runes []rune, modelKeywords []string) []CaptionSpan {
	spans := numericKeywordSpans(runes)
	if len(spans) > spokenKeywordSpanLimit {
		spans = spans[:spokenKeywordSpanLimit]
	}
	text := string(runes)
	terms := make([]CaptionSpan, 0, 2)
	search := spokenKeywordTerms
	if modelKeywords != nil {
		search = modelKeywords
	}
	for _, term := range search {
		found := strings.Index(text, term)
		if found < 0 {
			continue
		}
		start := len([]rune(text[:found]))
		end := start + len([]rune(term))
		if !spansOverlap(spans, start, end) && !spansOverlap(terms, start, end) {
			terms = append(terms, CaptionSpan{Start: start, End: end, Style: spokenKeywordStyle})
		}
	}
	sort.Slice(terms, func(i, j int) bool {
		left, right := terms[i].End-terms[i].Start, terms[j].End-terms[j].Start
		if left != right {
			return left > right
		}
		return terms[i].Start < terms[j].Start
	})
	for _, term := range terms {
		if len(spans) >= spokenKeywordSpanLimit {
			break
		}
		spans = append(spans, term)
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].Start < spans[j].Start })
	if len(spans) == 0 {
		return nil
	}
	return spans
}

func numericKeywordSpans(runes []rune) []CaptionSpan {
	var spans []CaptionSpan
	for i := 0; i < len(runes); {
		start := i
		digits := 0
		if strings.HasPrefix(string(runes[i:]), "百分之") {
			i += 3
		} else if !containsRune(spokenNumerals, runes[i]) && !unicode.IsDigit(runes[i]) {
			i++
			continue
		}
		numeralStart := i
		for i < len(runes) && (containsRune(spokenNumerals, runes[i]) || unicode.IsDigit(runes[i]) || isDecimalPoint(runes, i)) {
			if unicode.IsDigit(runes[i]) {
				digits++
			}
			i++
		}
		numerals := i - numeralStart
		for i < len(runes) && containsRune(spokenUnits, runes[i]) {
			i++
		}
		if numerals == 0 {
			continue
		}
		// A lone "一个" or "两样" is not a fact; require a real quantity.
		if numerals < 2 && digits == 0 && start == numeralStart {
			continue
		}
		spans = append(spans, CaptionSpan{Start: start, End: i, Style: spokenKeywordStyle})
	}
	return spans
}

func spansOverlap(spans []CaptionSpan, start, end int) bool {
	for _, span := range spans {
		if start < span.End && span.Start < end {
			return true
		}
	}
	return false
}

func isSpokenBreakRune(r rune) bool {
	return containsRune(spokenSoftBreaks, r) || containsRune(spokenStrongBreaks, r)
}

func containsRune(set []rune, r rune) bool {
	for _, candidate := range set {
		if candidate == r {
			return true
		}
	}
	return false
}

func isASCIIWordRune(r rune) bool {
	return r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r))
}
