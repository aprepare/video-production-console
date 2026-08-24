package openaicompat

import (
	"strings"
	"testing"
)

func TestOverlapFragmentsCatchesRepunctuatedCopies(t *testing.T) {
	source := "这一压就是十三年。2008年全国均价3900块一平，到2021年一万多，北上广深直接翻了十来倍。"
	draft := "这一压，就是十三年！2008年全国均价3900块一平。到2021年一万多。北上广深直接翻了十来倍。"
	fragments := overlapFragments(source, draft)
	if len(fragments) == 0 {
		t.Fatal("只改标点的照搬必须被抓住")
	}
	if !strings.Contains(fragments[0], "十三年") {
		t.Fatalf("fragments=%v", fragments)
	}
}

func TestOverlapFragmentsFlagsCopiedDataSentences(t *testing.T) {
	// 带数字的句子照搬同样要抓：数字原词是规则，句子照搬不是。
	source := "2001年外汇储备2100多亿美元，到2014年最猛的时候3.99万亿，翻了快二十倍。"
	draft := "回头看，2001年外汇储备2100多亿美元，到2014年最猛的时候3.99万亿，翻了快二十倍，全流进了工厂。"
	if fragments := overlapFragments(source, draft); len(fragments) == 0 {
		t.Fatal("照搬的数据句必须被抓住")
	}
}

func TestOverlapFragmentsIgnoresFixedElementsAndShortEchoes(t *testing.T) {
	source := "去我主页橱窗看《财富觉醒方法论》。推广期间只要五块钱。窗口正在收紧，现在上车还来得及。"
	draft := "去我主页橱窗看财富觉醒方法论。推广期间只要五块钱。这几个月越来越窄，现在上车还来得及。"
	if fragments := overlapFragments(source, draft); len(fragments) != 0 {
		t.Fatalf("固定要素和短口头禅不算照搬: %v", fragments)
	}
}

func TestOverlapFragmentsSkipsNumberOnlySpans(t *testing.T) {
	source := "外汇储备2100多亿美元。"
	draft := "储备额是2100多亿美元没错。"
	if fragments := overlapFragments(source, draft); len(fragments) != 0 {
		t.Fatalf("纯数据短语不算照搬: %v", fragments)
	}
}

const overlapSourceText = "回头看第一桌。2001年中国刚加入世贸，人民币背后撑腰的是美元。你每挣一美元，央行就对应印8块多人民币，央行手里美元越攒越多，发牌越有底气。"

const overlapCopiedDraft = `{"continuous_script":"回头看第一回。那年入了世贸，你每挣一美元，央行就对应印8块多人民币，央行手里美元越攒越多，钱就这样进了外贸老板的口袋，流水线工人只分到零头。","titles":[],"short_titles":[],"descriptions":[],"topics":[],"cta":"现在上车还来得及。"}`

const overlapFixedDraft = `{"continuous_script":"回头看第一回。那年入了世贸，厂里每收一美元货款，银行柜台就按八块多的价换给你人民币，钱就这样进了外贸老板的口袋，流水线工人只分到零头。","titles":[],"short_titles":[],"descriptions":[],"topics":[],"cta":"现在上车还来得及。"}`

type sequenceClient struct {
	responses []string
	requests  []ChatRequest
}

func (c *sequenceClient) Chat(req ChatRequest) (ChatResponse, error) {
	c.requests = append(c.requests, req)
	idx := len(c.requests) - 1
	if idx >= len(c.responses) {
		idx = len(c.responses) - 1
	}
	return textResponse(c.responses[idx]), nil
}

func TestRepairSourceOverlapRewritesCopiedSentences(t *testing.T) {
	client := &sequenceClient{responses: []string{overlapFixedDraft}}
	content, warnings, note := repairSourceOverlap(client, "test-model", "check-model", "", "system", "user", overlapSourceText, overlapCopiedDraft)
	if len(client.requests) != 1 {
		t.Fatalf("返工必须只追加一轮请求, got %d", len(client.requests))
	}
	repair := client.requests[0]
	if repair.Model != "check-model" {
		t.Fatalf("返工请求必须用质检模型, got %q", repair.Model)
	}
	if len(repair.Messages) != 4 || repair.Messages[3].Role != "user" {
		t.Fatalf("返工请求必须带完整会话: %#v", repair.Messages)
	}
	if !strings.Contains(repair.Messages[3].Content, "一字不差") || !strings.Contains(repair.Messages[3].Content, "央行就对应印8块多人民币") {
		t.Fatalf("返工消息必须列出照搬证据: %q", repair.Messages[3].Content)
	}
	if content != overlapFixedDraft {
		t.Fatalf("必须采用返工后的稿子")
	}
	if len(warnings) != 0 {
		t.Fatalf("修复后不应有警告: %v", warnings)
	}
	if !strings.Contains(note, "check-model") || !strings.Contains(note, "已全部改写") {
		t.Fatalf("质检结论必须写明模型和返工结果: %q", note)
	}
}

func TestRepairSourceOverlapKeepsOriginalWhenRetryStillCopies(t *testing.T) {
	client := &sequenceClient{responses: []string{overlapCopiedDraft}}
	content, warnings, note := repairSourceOverlap(client, "test-model", "check-model", "", "system", "user", overlapSourceText, overlapCopiedDraft)
	if content != overlapCopiedDraft {
		t.Fatal("返工无改善时保留首稿")
	}
	if len(client.requests) != 1 || client.requests[0].Model != "check-model" {
		t.Fatalf("返工必须用配置的质检模型: %#v", client.requests)
	}
	if len(warnings) == 0 || !strings.Contains(warnings[0], "与原文重合未修复") {
		t.Fatalf("残留重合必须写成警告: %v", warnings)
	}
	if !strings.Contains(note, "check-model") || !strings.Contains(note, "保留首稿") {
		t.Fatalf("质检结论必须说明保留首稿: %q", note)
	}
}

func TestRepairSourceOverlapSkipsCleanDrafts(t *testing.T) {
	client := &sequenceClient{responses: []string{overlapFixedDraft}}
	content, warnings, note := repairSourceOverlap(client, "test-model", "check-model", "", "system", "user", overlapSourceText, overlapFixedDraft)
	if len(client.requests) != 0 {
		t.Fatal("干净稿子不应触发返工请求")
	}
	if content != overlapFixedDraft || warnings != nil {
		t.Fatalf("干净稿子原样通过: warnings=%v", warnings)
	}
	if !strings.Contains(note, "质检通过") || !strings.Contains(note, "check-model") {
		t.Fatalf("干净稿子也要写质检结论: %q", note)
	}
}

func TestRepairSourceOverlapIsDisabledWithoutCheckModel(t *testing.T) {
	client := &sequenceClient{responses: []string{overlapFixedDraft}}
	content, warnings, note := repairSourceOverlap(client, "test-model", "", "", "system", "user", overlapSourceText, overlapCopiedDraft)
	if len(client.requests) != 0 {
		t.Fatal("质检模型留空时不得发出任何质检请求")
	}
	if content != overlapCopiedDraft {
		t.Fatalf("质检关闭时仍交付原始输出")
	}
	if len(warnings) == 0 || !strings.Contains(warnings[0], "与原文重合未修复") {
		t.Fatalf("质检关闭也要把重合写成警告: %v", warnings)
	}
	if !strings.Contains(note, "质检未启用") || !strings.Contains(note, "与原文重合") {
		t.Fatalf("质检关闭也要在结论里说明重合: %q", note)
	}
}

func TestStripCourseYearAndCollapseDuplicateMentions(t *testing.T) {
	script := "两个月少了2万亿。第三，花2秒钟打开主页橱窗里的《2026财富觉醒方法论》。虽然才五块钱。主页橱窗里的《2026财富觉醒方法论》已经放好。"
	got, notes := applyLocalCopyFixes(script)
	if strings.Contains(got, "2026财富") {
		t.Fatalf("课名年份必须去掉: %s", got)
	}
	if strings.Count(got, canonicalCourse) != 1 {
		t.Fatalf("卖课只留一次, got=%s notes=%v", got, notes)
	}
	if !containsString(notes, "课名去掉年份") || !containsString(notes, "删掉重复的卖课收口") {
		t.Fatalf("notes=%v", notes)
	}
	issues := inspectCopyIssues(got)
	for _, issue := range issues {
		if strings.Contains(issue, "两次") || strings.Contains(issue, "年份") {
			t.Fatalf("本地改完不应再报课名/重复: %v", issues)
		}
	}
}

func TestRepairRemixDraftSavesLocalCourseFixesWithoutModel(t *testing.T) {
	raw := `{"continuous_script":"两个月少了2万亿，钱去了哪儿？不是买房。第三，打开主页橱窗里的《2026财富觉醒方法论》。虽然才五块钱。主页橱窗里的《2026财富觉醒方法论》已经放好。","titles":[],"short_titles":[],"descriptions":[],"topics":[],"cta":""}`
	client := &sequenceClient{}
	content, _, note := repairRemixDraft(client, "writer", "", "", "system", "user", "source", raw)
	if len(client.requests) != 0 {
		t.Fatalf("本地能改时不应打模型: %d", len(client.requests))
	}
	draft, err := parseRemixDraft(content)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(draft.ContinuousScript, "2026财富") {
		t.Fatalf("必须直接改稿保存: %s", draft.ContinuousScript)
	}
	if strings.Count(draft.ContinuousScript, canonicalCourse) != 1 {
		t.Fatalf("重复收口必须删掉: %s", draft.ContinuousScript)
	}
	if !strings.Contains(note, "本地已改") {
		t.Fatalf("note=%q", note)
	}
}
