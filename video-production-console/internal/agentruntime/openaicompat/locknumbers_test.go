package openaicompat

import (
	"strings"
	"testing"
)

func TestCheckLockNumbersFlagsForeignYearAndMissing(t *testing.T) {
	source := "三份文件2026年9月1号同一天生效。居民存款173.59万亿，一年期利率0.95%，2008年降息那一轮。"
	draft := "三份文件2025年9月1号同一天生效。居民存款173万亿，一年期利率0.95%。"
	issues := checkLockNumbers(source, draft)
	if len(issues.ForeignYears) != 1 || issues.ForeignYears[0] != "2025年" {
		t.Fatalf("foreign years = %v", issues.ForeignYears)
	}
	joined := strings.Join(issues.Missing, "|")
	if !strings.Contains(joined, "2026年") || !strings.Contains(joined, "2008年") {
		t.Fatalf("missing years not reported: %v", issues.Missing)
	}
	// 金额精度不得丢失；0.95% 原样出现不算缺失。
	if !strings.Contains(joined, "173.59万亿") || strings.Contains(joined, "0.95%") {
		t.Fatalf("false positives: %v", issues.Missing)
	}
}

func TestCheckLockNumbersCleanDraft(t *testing.T) {
	source := "2026年9月1号起效，规模75万亿，利率1.25%。"
	draft := "从2026年9月1号起就执行，光到期的就有75万亿，续存只剩1.25%。"
	if issues := checkLockNumbers(source, draft); !issues.empty() {
		t.Fatalf("clean draft flagged: %+v", issues)
	}
}

func TestIntegerPartWithUnit(t *testing.T) {
	if got := integerPartWithUnit("173.59万亿"); got != "173万亿" {
		t.Fatalf("got %q", got)
	}
	if got := integerPartWithUnit("2400亿"); got != "2400亿" {
		t.Fatalf("got %q", got)
	}
}

// 年份写错：一轮定向返工修好就采用；模型不改则任务失败。
func TestSelfCheckRepairsForeignYearOrFails(t *testing.T) {
	source := strings.Repeat("这一段是原文正文，讲的是钱往哪里走、规则怎么变、普通人该怎么看。", 12) + "三份文件2026年9月1号同一天生效。"
	wrong := `{"continuous_script":"` + strings.Repeat("新稿换了说法讲钱的去向和规矩的变化，普通人先把脑子转过来。", 12) + `三份文件2025年9月1号同一天生效。","titles":[],"short_titles":[],"descriptions":[],"topics":[],"cta":""}`
	fixed := strings.Replace(wrong, "2025年", "2026年", 1)

	client := &sequenceClient{responses: []string{fixed}}
	content, warnings, note, err := selfCheckRemixRewrite(client, "m", "", "sys", "user", source, wrong, t.TempDir(), defaultSelfCheckLimits())
	if err != nil {
		t.Fatalf("repaired year must pass: %v", err)
	}
	if !strings.Contains(content, "2026年") || strings.Contains(content, "2025年") {
		t.Fatalf("repair not adopted: %s", content)
	}
	if len(warnings) != 0 || !strings.Contains(note, "锁词数字已定向返工") {
		t.Fatalf("warnings=%v note=%q", warnings, note)
	}
	if len(client.requests) != 1 || !strings.Contains(client.requests[0].Messages[3].Content, "2025年") {
		t.Fatalf("repair prompt must name the foreign year: %+v", client.requests)
	}

	stubborn := &sequenceClient{responses: []string{wrong}}
	_, warnings, _, err = selfCheckRemixRewrite(stubborn, "m", "", "sys", "user", source, wrong, t.TempDir(), defaultSelfCheckLimits())
	if err == nil || !strings.Contains(err.Error(), "2025年") {
		t.Fatalf("unfixed foreign year must fail the run: %v", err)
	}
	if len(warnings) == 0 || !strings.Contains(warnings[0], "锁词数字错误") {
		t.Fatalf("warnings=%v", warnings)
	}
}
