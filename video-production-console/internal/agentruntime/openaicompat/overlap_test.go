package openaicompat

import (
	"encoding/json"
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

const overlapHighCopySource = "问一个让你后背发凉的问题，如果全国老百姓存在银行里的钱突然少了整整2万亿，而且不是买了房，不是炒个股，连最火的黄金都没接住这笔钱，那它到底变成了什么？这不是假设，这是刚刚发生的白纸黑字写在央行月度报表上的实时数据。两个月2万亿蒸发，这是近十年来最大规模的无声迁徙。"

const overlapHighCopyDraft = `{"continuous_script":"问一个让你后背发凉的问题，如果全国老百姓存在银行里的钱突然少了整整2万亿，而且不是买了房，不是炒个股，连最火的黄金都没接住这笔钱，那它到底变成了什么？这不是假设，这是刚刚发生的白纸黑字写在央行月度报表上的实时数据。两个月2万亿蒸发，这是近十年来最大规模的无声迁徙。点开主页橱窗看《财富觉醒方法论》。","titles":[],"short_titles":[],"descriptions":[],"topics":[],"cta":""}`

const overlapHighCopyFixed = `{"continuous_script":"存折上一下子少了2万亿，不是买房，不是进股市，连黄金都没接住。央行刚公布的月报把这件事写死了。两个月里这笔钱蒸发掉，近十年没见过这么大的搬家。点开主页橱窗看《财富觉醒方法论》。","titles":[],"short_titles":[],"descriptions":[],"topics":[],"cta":""}`

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
	content, warnings, note, err := repairSourceOverlap(client, "test-model", "check-model", "", "system", "user", overlapSourceText, overlapCopiedDraft)
	if err != nil {
		t.Fatal(err)
	}
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
	if !strings.Contains(repair.Messages[3].Content, "40%") || !strings.Contains(repair.Messages[3].Content, "央行就对应印8块多人民币") {
		t.Fatalf("返工消息必须列出照搬证据: %q", repair.Messages[3].Content)
	}
	if content != overlapFixedDraft {
		t.Fatalf("必须采用返工后的稿子")
	}
	if len(warnings) != 0 {
		t.Fatalf("修复后不应有警告: %v", warnings)
	}
	if !strings.Contains(note, "check-model") || !strings.Contains(note, "降到") {
		t.Fatalf("质检结论必须写明模型和返工结果: %q", note)
	}
}

func TestRepairSourceOverlapFailsWhenRetryStillCopies(t *testing.T) {
	client := &sequenceClient{responses: []string{overlapHighCopyDraft}}
	_, warnings, note, err := repairSourceOverlap(client, "test-model", "check-model", "", "system", "user", overlapHighCopySource, overlapHighCopyDraft)
	if err == nil || !strings.Contains(err.Error(), "40%") {
		t.Fatalf("超过 40%% 必须失败: %v", err)
	}
	if len(client.requests) != 1 || client.requests[0].Model != "check-model" {
		t.Fatalf("返工必须用配置的质检模型: %#v", client.requests)
	}
	if len(warnings) == 0 || !strings.Contains(warnings[0], "与原文重合未修复") {
		t.Fatalf("残留重合必须写成警告: %v", warnings)
	}
	if !strings.Contains(note, "超过 40%") {
		t.Fatalf("质检结论必须说明仍超标: %q", note)
	}
}

func TestRepairSourceOverlapSkipsCleanDrafts(t *testing.T) {
	client := &sequenceClient{responses: []string{overlapFixedDraft}}
	content, warnings, note, err := repairSourceOverlap(client, "test-model", "check-model", "", "system", "user", overlapSourceText, overlapFixedDraft)
	if err != nil {
		t.Fatal(err)
	}
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

func TestRepairSourceOverlapUsesWriterWhenCheckModelEmpty(t *testing.T) {
	client := &sequenceClient{responses: []string{overlapHighCopyFixed}}
	content, _, note, err := repairSourceOverlap(client, "writer-model", "", "", "system", "user", overlapHighCopySource, overlapHighCopyDraft)
	if err != nil {
		t.Fatal(err)
	}
	if len(client.requests) != 1 || client.requests[0].Model != "writer-model" {
		t.Fatalf("质检留空时必须用写稿模型返工: %#v", client.requests)
	}
	if content != overlapHighCopyFixed {
		t.Fatal("必须采用返工后的稿子")
	}
	if !strings.Contains(note, "降到") {
		t.Fatalf("note=%q", note)
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
	issues := inspectCopyIssues(got, "")
	for _, issue := range issues {
		if strings.Contains(issue, "两次") || strings.Contains(issue, "年份") {
			t.Fatalf("本地改完不应再报课名/重复: %v", issues)
		}
	}
}

func TestRepairRemixDraftSavesLocalCourseFixesWithoutModel(t *testing.T) {
	raw := `{"continuous_script":"两个月少了2万亿，钱去了哪儿？不是买房。第三，打开主页橱窗里的《2026财富觉醒方法论》。虽然才五块钱。主页橱窗里的《2026财富觉醒方法论》已经放好。","titles":[],"short_titles":[],"descriptions":[],"topics":[],"cta":""}`
	client := &sequenceClient{}
	content, _, note, err := repairRemixDraft(client, "writer", "", "", "system", "user", "source", raw)
	if err != nil {
		t.Fatal(err)
	}
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

func TestInspectCopyIssuesRejectsSameOpeningAndDepositRange(t *testing.T) {
	source := "问一个让你后背发凉的问题，如果全国老百姓存在银行里的钱突然少了整整2万亿，而且不是买了房，不是炒个股，连最火的黄金都没接住这笔钱，那它到底变成了什么？"
	script := "两个月，整整20500亿，从全国老百姓的存折上悄无声息地蒸发了。这笔钱没流进楼市，没被股市收走，连近两年涨势最猛的黄金都没接住它——那它究竟去了哪儿？答案只有两个字：到期。华泰测算逼近50到77万亿。这种搬家只出现过三次。98年、08年、15年。现在是第四次。去我主页橱窗找《财富觉醒方法论》。"
	issues := inspectCopyIssues(script, source)
	if !containsIssue(issues, "开场切口") {
		t.Fatalf("必须抓住同一切口: %v", issues)
	}
	if !containsIssue(issues, "50到77") {
		t.Fatalf("必须抓住定存区间: %v", issues)
	}
	if containsIssue(issues, "流水线") || containsIssue(issues, "过软") {
		t.Fatalf("流水线和过软共情不再当硬闸: %v", issues)
	}
	got, notes := applyLocalCopyFixes(script)
	if strings.Contains(got, "50到77") || !containsString(notes, "去掉50到77万亿定存区间") {
		t.Fatalf("本地必须去掉定存区间: %s notes=%v", got, notes)
	}
	client := &sequenceClient{}
	raw := `{"continuous_script":"` + script + `","titles":[],"short_titles":[],"descriptions":[],"topics":[],"cta":""}`
	_, _, _, err := repairRemixDraft(client, "writer", "", "", "system", "user", source, raw)
	if !strings.Contains(err.Error(), "切口") && !strings.Contains(err.Error(), "50到77") {
		t.Fatalf("同一切口或定存区间必须让任务失败: %v", err)
	}
}

func TestInspectCopyIssuesAllowsSamePipelineAndSoftEmpathy(t *testing.T) {
	source := "三年定存利率从2.6%砍到1.25%。四月份居民存款少了近2万亿。"
	script := "三年定存利率从2.6%直接砍到1.25%，一年期只剩0.95%。这不是吓你。答案只有两个字：到期。这种搬家只出现过三次。98年、08年、15年。现在是第四次。不要觉得手头存款少就没资格。去我主页橱窗找《财富觉醒方法论》。"
	issues := inspectCopyIssues(script, source)
	if containsIssue(issues, "流水线") || containsIssue(issues, "过软") {
		t.Fatalf("同一流水线和过软共情必须放行: %v", issues)
	}
	client := &sequenceClient{}
	raw := `{"continuous_script":` + mustJSONString(script) + `,"titles":[],"short_titles":[],"descriptions":[],"topics":[],"cta":""}`
	if _, _, _, err := repairRemixDraft(client, "writer", "", "", "system", "user", source, raw); err != nil {
		t.Fatalf("按对标顺序写、留下共情句不应失败: %v", err)
	}
}

func mustJSONString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func containsIssue(issues []string, needle string) bool {
	for _, issue := range issues {
		if strings.Contains(issue, needle) {
			return true
		}
	}
	return false
}
