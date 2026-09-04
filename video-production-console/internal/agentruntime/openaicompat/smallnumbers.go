package openaicompat

import (
	"regexp"
	"strconv"
	"strings"
)

// 修辞性小数字改汉字：口播里「20年」「第6次」「10个人里8个」念起来发飘，也像
// 机器写的。一到两位数跟着量词的一律转汉字；年份、金额、百分比、日期不碰。
// 「年」单独处理——只在时长语境（了/近/整整/前/后/过 之后）才转，「98年、08年」
// 这种年份缩写保留。

var (
	// 前面不能是数字、小数点或百分号（排除 2026年、0.95%、173万 里的片段）。
	smallNumberUnitRe = regexp.MustCompile(`(^|[^0-9.%])(\d{1,2})(个|次|倍|套|辆|种|件|条|轮|回|天|遍|家|位|步|道|门|口|句|段|张|份|块地|份文件)`)
	smallNumberYearRe = regexp.MustCompile(`(了|近|整整|前|后|过|这|那)(\d{1,2})年`)
)

var chineseDigits = []string{"零", "一", "二", "三", "四", "五", "六", "七", "八", "九"}

// chineseNumber 把 0～99 写成口播汉字；ordinal=false 时 2 读「两」（两个、两次），
// 「第2」这类序数仍用「二」。
func chineseNumber(n int, ordinal bool) string {
	if n < 0 || n > 99 {
		return strconv.Itoa(n)
	}
	if n < 10 {
		if n == 2 && !ordinal {
			return "两"
		}
		return chineseDigits[n]
	}
	tens, ones := n/10, n%10
	var b strings.Builder
	if tens > 1 {
		b.WriteString(chineseDigits[tens])
	}
	b.WriteString("十")
	if ones > 0 {
		b.WriteString(chineseDigits[ones])
	}
	return b.String()
}

func chineseSmallNumbers(script string) (string, bool) {
	orig := script
	script = smallNumberUnitRe.ReplaceAllStringFunc(script, func(m string) string {
		sub := smallNumberUnitRe.FindStringSubmatch(m)
		n, err := strconv.Atoi(sub[2])
		if err != nil {
			return m
		}
		ordinal := strings.HasSuffix(sub[1], "第")
		return sub[1] + chineseNumber(n, ordinal) + sub[3]
	})
	// 「第N」前缀：上面的正则把「第」算在前缀里，但序数要用「二」不用「两」，
	// 再单独处理一遍「第2次」这类漏网。
	script = regexp.MustCompile(`第(\d{1,2})(个|次|轮|回|步|道|波|批)`).ReplaceAllStringFunc(script, func(m string) string {
		digits := regexp.MustCompile(`\d{1,2}`).FindString(m)
		n, _ := strconv.Atoi(digits)
		return strings.Replace(m, digits, chineseNumber(n, true), 1)
	})
	script = smallNumberYearRe.ReplaceAllStringFunc(script, func(m string) string {
		sub := smallNumberYearRe.FindStringSubmatch(m)
		n, err := strconv.Atoi(sub[2])
		if err != nil {
			return m
		}
		return sub[1] + chineseNumber(n, false) + "年"
	})
	return script, script != orig
}
