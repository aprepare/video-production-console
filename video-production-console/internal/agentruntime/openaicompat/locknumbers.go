package openaicompat

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// 锁词数字校对：原文里的年份、百分比、带单位的金额规模是锁词，成稿必须一个
// 不少；成稿里冒出原文没有的年份（2026 写成 2025 这类）是硬错。只认阿拉伯
// 数字写法，避免对汉字数字做猜测式匹配带来的误报。

var (
	lockYearRe    = regexp.MustCompile(`(19|20)\d{2}年`)
	lockPercentRe = regexp.MustCompile(`\d+(?:\.\d+)?[%％]`)
	lockAmountRe  = regexp.MustCompile(`\d+(?:\.\d+)?(?:万亿|亿|万)`)
)

type lockNumberIssues struct {
	ForeignYears []string // 成稿有、原文没有的年份
	Missing      []string // 原文有、成稿没有的锁词数字
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

// checkLockNumbers 对照原文和成稿的锁词数字。金额允许小数位被口播化省略
// （173.59万亿 → 173万亿 算出现），年份和百分比要求原样出现。
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
	dstNorm := strings.ReplaceAll(draft, "％", "%")
	for _, p := range sortedKeys(lockNumberTokens(source, lockPercentRe)) {
		if !strings.Contains(dstNorm, p) {
			issues.Missing = append(issues.Missing, p)
		}
	}
	for _, a := range sortedKeys(lockNumberTokens(source, lockAmountRe)) {
		if strings.Contains(draft, a) || strings.Contains(draft, integerPartWithUnit(a)) {
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
		b.WriteString(fmt.Sprintf("%d. 成稿写了原文没有的年份「%s」，这是事实错误，改回原文对应的年份。\n", n, y))
		n++
	}
	for _, m := range issues.Missing {
		b.WriteString(fmt.Sprintf("%d. 原文的锁词数字「%s」在成稿里没有出现，把它补回对应的那一句（原样写，不换说法）。\n", n, m))
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
		parts = append(parts, "原文数字未出现："+strings.Join(issues.Missing, "、"))
	}
	return strings.Join(parts, "；")
}
