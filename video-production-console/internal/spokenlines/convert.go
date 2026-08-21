package spokenlines

import (
	"strings"
	"unicode"
)

var cnDigits = map[rune]int{
	'零': 0, '〇': 0, '○': 0, 'Ｏ': 0, '0': 0,
	'一': 1, '二': 2, '两': 2, '三': 3, '四': 4,
	'五': 5, '六': 6, '七': 7, '八': 8, '九': 9,
}

var cnSmallUnits = map[rune]int{
	'十': 10, '百': 100, '千': 1000,
}

func digitize(text string) string {
	text = convertYears(text)
	text = convertPercents(text)
	text = convertLargeNumbers(text)
	text = appendYearToBareYears(text)
	return text
}

// quantityUnitAfterDigits marks runes that make a 4-digit run a count rather
// than a year ("2000块", "2026套"), so no 年 is appended before them.
var quantityUnitAfterDigits = map[rune]struct{}{
	'万': {}, '亿': {}, '千': {}, '百': {}, '十': {}, '多': {}, '余': {},
	'元': {}, '块': {}, '人': {}, '名': {}, '套': {}, '户': {}, '次': {},
	'个': {}, '条': {}, '张': {}, '倍': {}, '斤': {}, '米': {}, '款': {},
	'台': {}, '辆': {}, '号': {}, '期': {}, '页': {}, '篇': {}, '集': {},
	'层': {}, '楼': {}, '分': {}, '秒': {}, '点': {}, '成': {}, '岁': {},
	'字': {}, '词': {}, '只': {}, '支': {}, '家': {}, '场': {}, '站': {},
}

// appendYearToBareYears turns a bare 1900–2099 four-digit run into "XXXX年" so
// the TTS reads 2026 as a year instead of 两千零二十六. Runs already followed
// by 年, part of a longer number, or followed by a quantity unit are left alone.
func appendYearToBareYears(text string) string {
	runes := []rune(text)
	var b strings.Builder
	for i := 0; i < len(runes); {
		r := runes[i]
		if r < '0' || r > '9' {
			b.WriteRune(r)
			i++
			continue
		}
		start := i
		for i < len(runes) && runes[i] >= '0' && runes[i] <= '9' {
			i++
		}
		run := string(runes[start:i])
		b.WriteString(run)
		if len(run) != 4 || (run[0] != '1' && run[0] != '2') {
			continue
		}
		if run[0] == '1' && run[1] != '9' {
			continue
		}
		if run[0] == '2' && run[1] != '0' {
			continue
		}
		if start > 0 {
			prev := runes[start-1]
			if prev == '.' || (prev >= 'a' && prev <= 'z') || (prev >= 'A' && prev <= 'Z') {
				continue
			}
		}
		if i < len(runes) {
			next := runes[i]
			if next == '.' || next == '%' || next == '％' || next == '年' {
				continue
			}
			if next >= 'a' && next <= 'z' || next >= 'A' && next <= 'Z' {
				continue
			}
			if _, quantity := quantityUnitAfterDigits[next]; quantity {
				continue
			}
		}
		b.WriteRune('年')
	}
	return b.String()
}

func convertYears(text string) string {
	runes := []rune(text)
	var b strings.Builder
	for i := 0; i < len(runes); {
		if year, n, ok := matchChineseYear(runes[i:]); ok {
			b.WriteString(year)
			i += n
			continue
		}
		b.WriteRune(runes[i])
		i++
	}
	return b.String()
}

func matchChineseYear(runes []rune) (string, int, bool) {
	if len(runes) < 5 {
		return "", 0, false
	}
	digits := make([]byte, 0, 4)
	for _, r := range runes[:4] {
		d, ok := cnYearDigit(r)
		if !ok {
			return "", 0, false
		}
		digits = append(digits, byte('0'+d))
	}
	if runes[4] != '年' {
		return "", 0, false
	}
	if digits[0] == '0' {
		return "", 0, false
	}
	return string(digits) + "年", 5, true
}

func cnYearDigit(r rune) (int, bool) {
	switch r {
	case '零', '〇', '○', 'Ｏ', '0':
		return 0, true
	case '一', '二', '三', '四', '五', '六', '七', '八', '九':
		return cnDigits[r], true
	default:
		if r >= '0' && r <= '9' {
			return int(r - '0'), true
		}
		return 0, false
	}
}

func convertPercents(text string) string {
	const prefix = "百分之"
	runes := []rune(text)
	prefixN := len([]rune(prefix))
	var b strings.Builder
	for i := 0; i < len(runes); {
		if i+prefixN <= len(runes) && string(runes[i:i+prefixN]) == prefix {
			if num, n, ok := parseChinesePercentNumber(runes[i+prefixN:]); ok {
				// 百分之十几 means "ten-something percent": the trailing 几
				// belongs to the number, so the phrase must stay Chinese.
				if rest := runes[i+prefixN+n:]; len(rest) == 0 || rest[0] != '几' {
					b.WriteString(num)
					b.WriteByte('%')
					i += prefixN + n
					continue
				}
			}
		}
		b.WriteRune(runes[i])
		i++
	}
	return b.String()
}

func parseChinesePercentNumber(runes []rune) (string, int, bool) {
	if len(runes) == 0 {
		return "", 0, false
	}
	end := 0
	for end < len(runes) && isChineseNumberRune(runes[end]) {
		end++
	}
	if end == 0 {
		return "", 0, false
	}
	body := string(runes[:end])
	if formatted, ok := formatChineseNumber(body); ok {
		return formatted, end, true
	}
	return "", 0, false
}

// approxQuantifiers precede a unit word to mean "several": 几十万亿、数百万.
// The coefficient after them is not a real number and must stay Chinese —
// converting 几十万亿 to 几10万亿 reads broken on screen and in TTS.
var approxQuantifiers = map[rune]struct{}{'几': {}, '数': {}}

func convertLargeNumbers(text string) string {
	units := []string{"万亿", "亿", "万"}
	runes := []rune(text)
	var b strings.Builder
	for i := 0; i < len(runes); {
		prevBlocks := false
		if i > 0 {
			if _, approx := approxQuantifiers[runes[i-1]]; approx || isChineseIntRune(runes[i-1]) {
				prevBlocks = true
			}
		}
		if !prevBlocks {
			matched := false
			for _, unit := range units {
				unitRunes := []rune(unit)
				if coeff, coeffN, ok := matchChineseCoeffBefore(runes, i, unitRunes); ok {
					b.WriteString(coeff)
					b.WriteString(unit)
					i += coeffN + len(unitRunes)
					matched = true
					break
				}
			}
			if matched {
				continue
			}
		}
		b.WriteRune(runes[i])
		i++
	}
	return b.String()
}

func matchChineseCoeffBefore(runes []rune, i int, unit []rune) (string, int, bool) {
	start := i
	for start < len(runes) && isChineseIntRune(runes[start]) {
		start++
	}
	if start == i {
		return "", 0, false
	}
	if start+len(unit) > len(runes) || string(runes[start:start+len(unit)]) != string(unit) {
		return "", 0, false
	}
	coeff := string(runes[i:start])
	n, ok := parseChineseInt(coeff)
	if !ok || n <= 0 {
		return "", 0, false
	}
	return itoa(n), start - i, true
}

func isChineseIntRune(r rune) bool {
	if _, ok := cnDigits[r]; ok {
		return true
	}
	_, ok := cnSmallUnits[r]
	return ok
}

func isChineseNumberRune(r rune) bool {
	if _, ok := cnDigits[r]; ok {
		return true
	}
	if _, ok := cnSmallUnits[r]; ok {
		return true
	}
	return r == '点'
}

func formatChineseNumber(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	if i := strings.IndexRune(s, '点'); i >= 0 {
		intPart, ok := parseChineseInt(s[:i])
		if !ok {
			return "", false
		}
		frac := []rune(s[i+len("点"):])
		if len(frac) == 0 {
			return "", false
		}
		var fb strings.Builder
		fb.WriteString(itoa(intPart))
		fb.WriteByte('.')
		for _, r := range frac {
			d, ok := cnDigits[r]
			if !ok {
				return "", false
			}
			fb.WriteByte(byte('0' + d))
		}
		return trimTrailingPercentZeros(fb.String()), true
	}
	n, ok := parseChineseInt(s)
	if !ok {
		return "", false
	}
	return itoa(n), true
}

func parseChineseInt(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	total, current := 0, 0
	seen := false
	for _, r := range s {
		if d, ok := cnDigits[r]; ok {
			current = d
			seen = true
			continue
		}
		if u, ok := cnSmallUnits[r]; ok {
			if current == 0 {
				current = 1
			}
			total += current * u
			current = 0
			seen = true
			continue
		}
		return 0, false
	}
	if !seen {
		return 0, false
	}
	return total + current, true
}

func trimTrailingPercentZeros(s string) string {
	if !strings.Contains(s, ".") {
		return s
	}
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	return s
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func isContentRune(r rune) bool {
	if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
		return false
	}
	switch r {
	case '，', '。', '！', '？', '、', '；', '：', '「', '」', '『', '』',
		'（', '）', '【', '】', '《', '》', '〈', '〉', '—', '…', '·',
		'“', '”', '‘', '’', '～', '%', '％':
		return false
	}
	return unicode.Is(unicode.Han, r) || unicode.IsDigit(r) || unicode.IsLetter(r)
}

func contentCount(s string) int {
	n := 0
	for _, r := range s {
		if isContentRune(r) {
			n++
		}
	}
	return n
}
