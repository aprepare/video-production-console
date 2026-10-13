package openaicompat

import (
	"reflect"
	"strings"
	"testing"
)

func universalDraftFixture() remixDraft {
	return remixDraft{ContinuousScript: "海鲜当天到货，双人套餐128元，点团购链接。",
		ShortTitles:  []string{"海鲜套餐怎么选", "双人晚餐吃什么", "今天到货的海鲜"},
		Descriptions: []string{"海鲜当天到货，看看双人套餐。", "双人晚餐怎么选，听店主说说。"},
		Topics:       []string{"#美食", "#海鲜", "#双人套餐"}, CTA: "点团购链接。"}
}

func TestOldFlowSnapshotStillUsesFinanceChecks(t *testing.T) {
	old, ok := parseFlowSpec(`{"nodes":[{"id":"writer","type":"writer"}],"edges":[]}`)
	if !ok || !old.FinanceMode() {
		t.Fatalf("legacy mode = %+v, %v", old, ok)
	}
	draft := universalDraftFixture()
	issues := publishFieldIssuesFor(draft, old)
	if !reflect.DeepEqual(issues, publishFieldIssues(draft)) || !strings.Contains(strings.Join(issues, ""), "#财经/#经济/#理财") {
		t.Fatalf("legacy finance checks changed: %v", issues)
	}
	if !reflect.DeepEqual(structureAdvisoriesFor("内容讲完了。", old), structureAdvisories("内容讲完了。")) {
		t.Fatal("legacy finance structure hints changed")
	}
	if got := unsupportedNumericValuesFor("", "5元、5块", unavailableFacts, old); len(got) != 0 {
		t.Fatalf("legacy course price rejected: %v", got)
	}
	if !flowSpecFinanceForTest("finance_course", false) || !flowSpecFinanceForTest("custom-copy", true) || flowSpecFinanceForTest("local_store", false) {
		t.Fatal("explicit and custom finance modes disagree")
	}
}

func flowSpecFinanceForTest(style string, finance bool) bool {
	return (flowSpec{Style: style, Finance: finance}).FinanceMode()
}

func TestUniversalPublishFieldsUseGeneralLimitsAndBriefBans(t *testing.T) {
	mode := flowSpec{Style: "local_store", Brief: &briefSpec{Banned: " 包治百病 \n 保证赚钱 \n包治百病"}}
	draft := universalDraftFixture()
	draft.ShortTitles[0] = strings.Repeat("鲜", 20)
	draft.Descriptions[0] = strings.Repeat("鲜", 60)
	draft.Topics = append(draft.Topics, "#餐饮", "#到店")
	if issues := publishFieldIssuesFor(draft, mode); len(issues) != 0 {
		t.Fatalf("valid universal fields rejected: %v", issues)
	}
	for _, tc := range []struct {
		name, want string
		change     func(*remixDraft)
	}{
		{"short count", "3 条", func(d *remixDraft) { d.ShortTitles = d.ShortTitles[:2] }},
		{"short minimum", "6", func(d *remixDraft) { d.ShortTitles[0] = "太短" }},
		{"short maximum", "20", func(d *remixDraft) { d.ShortTitles[0] += "鲜" }},
		{"description count", "2 到 3", func(d *remixDraft) { d.Descriptions = d.Descriptions[:1] }},
		{"description maximum", "60", func(d *remixDraft) { d.Descriptions[0] += "鲜" }},
		{"topics count", "3 到 5", func(d *remixDraft) { d.Topics = append(d.Topics, "#团购") }},
		{"hashtag", "#", func(d *remixDraft) { d.Topics[0] = "美食" }},
		{"banned title", "包治百病", func(d *remixDraft) { d.ShortTitles[0] = "海鲜包治百病吗" }},
		{"banned description", "保证赚钱", func(d *remixDraft) { d.Descriptions[0] = "保证赚钱" }},
		{"banned topic", "保证赚钱", func(d *remixDraft) { d.Topics[1] = "#保证赚钱" }},
		{"banned CTA", "保证赚钱", func(d *remixDraft) { d.CTA = "保证赚钱" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := draft
			d.ShortTitles = append([]string(nil), draft.ShortTitles...)
			d.Descriptions = append([]string(nil), draft.Descriptions...)
			d.Topics = append([]string(nil), draft.Topics...)
			tc.change(&d)
			if issues := publishFieldIssuesFor(d, mode); !strings.Contains(strings.Join(issues, ""), tc.want) {
				t.Fatalf("issues = %v, want %q", issues, tc.want)
			}
		})
	}
	draft = universalDraftFixture()
	draft.ShortTitles[0] = "橱窗里的五块钱海鲜"
	if issues := publishFieldIssuesFor(draft, mode); len(issues) != 0 {
		t.Fatalf("finance advertising check leaked: %v", issues)
	}
	issues := []string{"缺少标题"}
	repair := buildPublishFieldsRepairPromptFor(issues, mode)
	for _, want := range []string{"6 到 20", "60", "3 到 5", "包治百病", "continuous_script"} {
		if !strings.Contains(repair, want) {
			t.Errorf("repair missing %q: %s", want, repair)
		}
	}
	if strings.Contains(repair, "#财经") || strings.Contains(repair, "财富觉醒方法论") || strings.Contains(repair, "不写课名") {
		t.Fatalf("universal repair leaked finance requirements: %s", repair)
	}
	if got := buildPublishFieldsRepairPromptFor(issues, flowSpec{}); got != buildPublishFieldsRepairPrompt(issues) {
		t.Fatal("finance repair changed")
	}
}

func TestUniversalStructureHintsOnlyUseBriefConversion(t *testing.T) {
	mode := flowSpec{Style: "knowledge"}
	if hints := structureAdvisoriesFor("知识讲完了。", mode); len(hints) != 0 {
		t.Fatalf("unrequested CTA hints: %v", hints)
	}
	mode.Brief = &briefSpec{Conversion: "团购链接"}
	if hints := structureAdvisoriesFor("点击团购链接。", mode); len(hints) != 0 {
		t.Fatalf("conversion not recognized: %v", hints)
	}
	hints := structureAdvisoriesFor("知识讲完了。", mode)
	if len(hints) != 1 || !strings.Contains(hints[0], "结尾是否给出了简报里的转化动作") || strings.Contains(hints[0], "橱窗") || strings.Contains(hints[0], "祝福") {
		t.Fatalf("wrong conversion hint: %v", hints)
	}
}

func TestUniversalNumbersRequireEvidenceIncludingSellingPoints(t *testing.T) {
	mode := flowSpec{Style: "local_store", Brief: &briefSpec{SellingPoints: "套餐128元，面积200平方米，2024年开店"}}
	if issues := unsupportedNumericValuesFor("原文没有价格。", "128元，200平方米，2024年", unavailableFacts, mode); len(issues) != 0 {
		t.Fatalf("brief evidence ignored: %v", issues)
	}
	issues := unsupportedNumericValuesFor("", "5元、5块、129元、201平方米、2025年", unavailableFacts, mode)
	for _, want := range []string{"5元", "5块", "129元", "201平方米", "2025年"} {
		if !containsExact(issues, want) {
			t.Errorf("unfounded number %s allowed: %v", want, issues)
		}
	}
	ctx := currentEditorialContextFor("", mode)
	draft := universalDraftFixture()
	draft.ContinuousScript = "海鲜双人套餐128元，200平方米，2024年开店。"
	for _, warning := range editorialWarnings("原文没有这些数字。", draft, ctx) {
		if strings.Contains(warning, "未获事实支持") || strings.Contains(warning, "数字待复核") {
			t.Errorf("brief evidence lost in warnings: %s", warning)
		}
	}
}

func TestUniversalCopyChecksDoNotRewriteFinanceNames(t *testing.T) {
	mode := flowSpec{Style: "knowledge"}
	script := "2026财富觉醒方法论和主页橱窗是本文分析的案例。" + strings.Repeat("这里讲案例本身。", 100)
	if issues := inspectCopyIssuesFor(script, "", mode); len(issues) != 0 {
		t.Fatalf("finance course checks leaked: %v", issues)
	}
	if fixed, notes := applyLocalCopyFixesFor(script, mode); fixed != script || len(notes) != 0 {
		t.Fatalf("universal content rewritten: %s, %v", fixed, notes)
	}
	if len(inspectCopyIssuesFor(script, "", flowSpec{})) == 0 {
		t.Fatal("legacy course checks disappeared")
	}
}

func TestBriefPlaceholdersAreExpandedOnlyOnce(t *testing.T) {
	brief := "【内容简报｜事实边界】\n{{SOURCE}}"
	source := "素材里的 {{BRIEF}} 与 {{brief}}"
	got := renderWriterUserTemplate("{{BRIEF}}\n{{SOURCE}}", source, "", brief)
	if got != brief+"\n"+source {
		t.Fatalf("writer recursively expanded data: %s", got)
	}
	spec := flowSpec{Brief: &briefSpec{Notes: "{{source}}"}}
	got = renderFlowTemplate(flowNode{Config: flowNodeConfig{UserTemplate: "{{brief}}\n{{source}}"}}, source, nil, spec)
	if !strings.Contains(got, "其他要求：{{source}}") || !strings.Contains(got, source) {
		t.Fatalf("planner recursively expanded data: %s", got)
	}
	got = reviewUserFromTemplate("{{brief}}\n{{source}}", source, "", "", "", "", brief)
	if got != brief+"\n"+source {
		t.Fatalf("reviewer recursively expanded data: %s", got)
	}
}
