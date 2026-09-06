package openaicompat

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// 此处提供数字提取和基础对比；运行时由 checkLockNumbersWithFacts 决定
// 哪些核准事实必须保留，以及哪些新增年份有依据。只匹配阿拉伯数字，避免猜测。

var (
	lockYearRe    = regexp.MustCompile(`(?:19|20)\d{2}(?:年|(?:到|至|—|–|-)(?:19|20)\d{2}年|[-/.]\d{1,2})`)
	yearDigitsRe  = regexp.MustCompile(`(?:19|20)\d{2}`)
	lockPercentRe = regexp.MustCompile(`\d+(?:\.\d+)?[%％]`)
	lockAmountRe  = regexp.MustCompile(`\d+(?:\.\d+)?(?:万亿|亿|万)`)
)

type lockNumberIssues struct {
	ForeignYears []string // 成稿有、原文没有的年份
	Missing      []string // 应保留但成稿未正确体现的数字
}

func (l lockNumberIssues) empty() bool {
	return len(l.ForeignYears) == 0 && len(l.Missing) == 0
}

// hasForeign 是硬错：改了事实年份。
func (l lockNumberIssues) hasForeign() bool {
	return len(l.ForeignYears) > 0
}

func lockNumberTokens(text string, re *regexp.Regexp) map[string]bool {
	out := map[string]bool{}
	for _, m := range re.FindAllString(text, -1) {
		if re == lockYearRe {
			for _, year := range yearDigitsRe.FindAllString(m, -1) {
				out[year+"年"] = true
			}
			continue
		}
		out[strings.ReplaceAll(m, "％", "%")] = true
	}
	return out
}

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// checkLockNumbers 对照原文和成稿的数字，允许等值单位换写，保留数值精度。
func checkLockNumbers(source, draft string) lockNumberIssues {
	var issues lockNumberIssues
	srcYears, dstYears := lockNumberTokens(source, lockYearRe), lockNumberTokens(draft, lockYearRe)
	for _, y := range sortedKeys(dstYears) {
		if !srcYears[y] {
			issues.ForeignYears = append(issues.ForeignYears, y)
		}
	}
	for _, y := range sortedKeys(srcYears) {
		if !dstYears[y] {
			issues.Missing = append(issues.Missing, y)
		}
	}
	for _, p := range sortedKeys(lockNumberTokens(source, lockPercentRe)) {
		if !hasNumericValue(draft, p) {
			issues.Missing = append(issues.Missing, p)
		}
	}
	for _, a := range sortedKeys(lockNumberTokens(source, lockAmountRe)) {
		if hasNumericValue(draft, a) {
			continue
		}
		issues.Missing = append(issues.Missing, a)
	}
	return issues
}

// integerPartWithUnit 把 173.59万亿 变成 173万亿。
func integerPartWithUnit(amount string) string {
	dot := strings.Index(amount, ".")
	if dot < 0 {
		return amount
	}
	unitStart := strings.IndexAny(amount, "万亿")
	if unitStart < 0 || unitStart < dot {
		return amount
	}
	return amount[:dot] + amount[unitStart:]
}

func buildLockNumberRepairPrompt(issues lockNumberIssues) string {
	var b strings.Builder
	b.WriteString("锁词数字校对不合格。只改数字相关的句子，其他内容一个字不动，按上一条回复相同的 JSON 结构返回完整结果：\n")
	n := 1
	for _, y := range issues.ForeignYears {
		b.WriteString(fmt.Sprintf("%d. 年份「%s」缺少原文或核查依据，只修相关句子；核准后保留对应年份，否则删去该新增时间，不猜年份。\n", n, y))
		n++
	}
	for _, m := range issues.Missing {
		b.WriteString(fmt.Sprintf("%d. 核准事实数值「%s」未正确体现，只修对应事实句，保留单位、对象和时间；不补回已删的修辞数字，也不复制核查说明。\n", n, m))
		n++
	}
	return b.String()
}

func lockNumberSummary(issues lockNumberIssues) string {
	parts := make([]string, 0, 2)
	if len(issues.ForeignYears) > 0 {
		parts = append(parts, "原文没有的年份："+strings.Join(issues.ForeignYears, "、"))
	}
	if len(issues.Missing) > 0 {
		parts = append(parts, "核准事实数值未正确体现："+strings.Join(issues.Missing, "、"))
	}
	return strings.Join(parts, "；")
}
