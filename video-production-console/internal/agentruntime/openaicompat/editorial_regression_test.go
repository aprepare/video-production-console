package openaicompat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditorialFactsSelectWholeConclusion(t *testing.T) {
	raw := `搜索过程 {"query":"利息"}
{"source_facts":[{"claim":"利息","value":"200元","status":"纠错","latest":"100元","source":"算术复核"}],"risk_notes":{"unit":"元"}}
最后一个无关对象 {"usage":12}`
	got := capIntelSection(raw)
	if !strings.Contains(got, `"source_facts"`) || !strings.Contains(got, "100元") || strings.Contains(got, "usage") {
		t.Fatalf("lost whole facts: %s", got)
	}
}

func TestEditorialFlowFactsReachReviewer(t *testing.T) {
	dir := t.TempDir()
	facts := `{"source_facts":[{"claim":"利息","value":"200元","status":"纠错","latest":"100元","source":"算术复核"}],"fresh_ammo":[]}`
	if err := os.WriteFile(filepath.Join(dir, "node_output_facts.json"), []byte(facts), 0600); err != nil {
		t.Fatal(err)
	}
	client := &reviewerFakeClient{reply: `{"verdict":"pass"}`}
	ReviewRemixDraft(ReviewOptions{Client: client, OutputDir: dir, DraftJSON: reviewerTestDraft()})
	if len(client.requests) != 1 || !strings.Contains(client.requests[0].Messages[1].Content, "100元") {
		t.Fatal("workflow facts missing from reviewer")
	}
}

func TestEditorialReworkKeepsFactsAndUsesCurrentRules(t *testing.T) {
	dir := t.TempDir()
	ctx := currentEditorialContext(dir)
	ctx.Policy = "SNAPSHOT POLICY"
	ctx.ReviewerPrompt = "SNAPSHOT REVIEW"
	ctx.Limits.MaxOverlap = 0.18
	ctx.Facts = json.RawMessage(`{"source_facts":[{"value":"100元","status":"成立","source":"算式"}]}`)
	if err := saveEditorialContext(dir, ctx); err != nil {
		t.Fatal(err)
	}
	client := &reviewerFakeClient{reply: `{"verdict":"pass"}`}
	ReviewRemixDraft(ReviewOptions{Client: client, OutputDir: dir, Round: 2, DraftJSON: reviewerTestDraft(), SystemPrompt: "NEW POLICY", FactsJSON: `{"source_facts":[]}`})
	system, user := client.requests[0].Messages[0].Content, client.requests[0].Messages[1].Content
	if !strings.Contains(system, "SNAPSHOT REVIEW") || !strings.Contains(system, SharedEditorialPolicy) || strings.Contains(system, "NEW POLICY") || !strings.Contains(user, "100元") || strings.Contains(user, "18%") {
		t.Fatal("rework lost original context")
	}
}

func TestEditorialApprovedCorrectionAndOptionalFreshYear(t *testing.T) {
	facts := `{"source_facts":[{"claim":"年利息","value":"200元","status":"纠错","latest":"100元","source":"200000*0.0005=100"}],"fresh_ammo":[{"fact":"2026年新资料","value":"","source":{"url":"https://www.pbc.gov.cn/example","date":"2026-09-01"}}]}`
	if got := checkLockNumbersWithFacts("本金20万，利息200元。", "本金20万，利息100元。", facts); !got.empty() {
		t.Fatalf("corrected value rejected: %+v", got)
	}
	if got := checkLockNumbersWithFacts("本金20万，利息200元。", "2026年，本金20万，利息100元。", facts); !got.empty() {
		t.Fatalf("sourced year rejected: %+v", got)
	}
	if got := checkLockNumbersWithFacts("本金20万，利息200元。", "本金20万，利息200元。", facts); got.empty() {
		t.Fatal("wrong arithmetic retained")
	}
}

func TestEditorialReviewAllowsWithinThresholdIncrease(t *testing.T) {
	source := strings.Repeat("原始材料记载这件事情涉及大家每个月的家庭收支安排。", 20)
	before := strings.Repeat("工作赚来的收入怎么分配，咱们今天就把这笔账说清楚。", 20)
	after := "原始材料记载这件事情涉及大家每个月的家庭收支安排。" + before
	ctx := currentEditorialContext("")
	if problem := reviewRegression(source, remixDraft{ContinuousScript: before}, remixDraft{ContinuousScript: after}, ctx); problem != "" {
		t.Fatal(problem)
	}
}

func TestEditorialReviewRejectsAddedRateEvenIfOldRateRemains(t *testing.T) {
	before := remixDraft{ContinuousScript: "原先利率0.95%。"}
	after := remixDraft{ContinuousScript: "原先利率0.95%，还有1.25%的选择。"}
	if reviewRegression(before.ContinuousScript, before, after, currentEditorialContext("")) == "" {
		t.Fatal("new rate not rejected")
	}
}

func TestEditorialArchivedDrafts(t *testing.T) {
	root := filepath.Join("..", "..", "..", "video-console-data", "remix-lab")
	dir := filepath.Join(root, "babb99f5-cbb0-46c7-8d77-1b960cf4b5e6", "115170b4-231a-4053-ba76-88363d3c2b32")
	source, err := os.ReadFile(filepath.Join(dir, "source.txt"))
	if os.IsNotExist(err) {
		t.Skip("local archive not present")
	}
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "draft_v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	draft, err := parseRemixDraft(string(b))
	if err != nil {
		t.Fatal(err)
	}
	r, err := os.ReadFile(filepath.Join(dir, "review.json"))
	if err != nil {
		t.Fatal(err)
	}
	var record ReviewRecord
	if err := json.Unmarshal(r, &record); err != nil || record.Revised == nil {
		t.Fatalf("review archive: %v", err)
	}
	before, after := selfCheckMeasure(string(source), draft.ContinuousScript), selfCheckMeasure(string(source), record.Revised.ContinuousScript)
	t.Logf("archived source overlap: %.2f%% -> %.2f%%", before.overlap*100, after.overlap*100)
	if reviewRegression(string(source), draft, *record.Revised, currentEditorialContext("")) == "" {
		t.Fatal("archived review regression was accepted")
	}
	dir = filepath.Join(root, "21d4969b-e124-4ff9-9e94-ca51303a11cb", "c731ffea-d99d-45d0-baaa-8d33cda99563")
	facts, err := os.ReadFile(filepath.Join(dir, "node_output_facts.json"))
	if err != nil {
		t.Fatal(err)
	}
	normalized := normalizeFacts(string(facts))
	if normalized == unavailableFacts || !strings.Contains(normalized, "100") {
		t.Fatal("archived arithmetic conclusion lost")
	}
	t.Logf("archived fact result: %d -> %d bytes of structured conclusion", len(facts), len(normalized))
}

func TestEditorialReviewLeavesNumericJudgmentToHuman(t *testing.T) {
	before := strings.Repeat("这件事发生在2026年，先把具体的变化弄清楚。", 15)
	after := strings.ReplaceAll(before, "2026年", "2027年")
	draft, _ := json.Marshal(remixDraft{ContinuousScript: before})
	reply, _ := json.Marshal(map[string]any{"verdict": "fixed", "issues": []ReviewIssue{{Where: "2026年", Problem: "年份", Fix: "2027年"}}, "revised": remixDraft{ContinuousScript: after}})
	out := ReviewRemixDraft(ReviewOptions{Client: &reviewerFakeClient{reply: string(reply)}, Source: "2026年" + strings.Repeat("来源的其他信息。", 50), DraftJSON: string(draft)})
	if out.Record.Verdict != "fixed" {
		t.Fatal("mechanical year check rejected revision")
	}
}

func TestEditorialWrappedDraftPreservesPublishingFields(t *testing.T) {
	raw := `The{"continuous_script":"七秒到账。","short_titles":["工资到账先看哪里","每笔开支算清楚了","家里管钱的人来听"],"descriptions":["工资到账先看收支。","把每笔账记清楚。"],"topics":["#财经","#工资","#收支"],"cta":"课在主页橱窗里"}`
	got, err := parseRemixDraft(replaceDraftScript(raw, "七秒到账，先看收支。"))
	if err != nil || len(got.ShortTitles) != 3 || len(got.Descriptions) != 2 || got.CTA != "课在主页橱窗里" {
		t.Fatalf("publishing fields lost: %+v err=%v", got, err)
	}
}

func TestEditorialFactsConclusionSurvivesLongSearchPreamble(t *testing.T) {
	facts := `{"source_facts":[{"claim":"20万按0.05%算一年利息200元","value":"200元","status":"纠错","latest":"100元","source":"算术复核"}],"fresh_ammo":[],"risk_notes":"本金乘年利率"}`
	got := capIntelSection(strings.Repeat("搜索过程。", 1200) + "\n```json\n" + facts + "\n```")
	if !json.Valid([]byte(got)) || !strings.Contains(got, "100元") || strings.Contains(got, "搜索过程") {
		t.Fatalf("final facts lost or polluted: %.160s", got)
	}
}

func TestEditorialInvalidReviewVerdictIsNotPass(t *testing.T) {
	client := &reviewerFakeClient{reply: `{"verdict":"wat","revised":{}}`}
	out := ReviewRemixDraft(ReviewOptions{Client: client, DraftJSON: `{"continuous_script":"这是一篇待审的完整文案。"}`})
	if out.Record.Verdict != "error" {
		t.Fatalf("invalid verdict accepted: %+v", out.Record)
	}
}

func TestEditorialReviewLeavesOverlapJudgmentToHuman(t *testing.T) {
	source := strings.Repeat("政策发生变化之前先盘清楚家庭每个月收入和开支。", 12)
	before := strings.Repeat("别光盯着外面的消息，你手里的账也得理顺再做决定。", 12)
	raw, _ := json.Marshal(remixDraft{ContinuousScript: before})
	reply, _ := json.Marshal(map[string]any{"verdict": "fixed", "issues": []ReviewIssue{{Where: "别光盯着", Problem: "开头", Fix: "换回原文"}}, "revised": remixDraft{ContinuousScript: source}})
	out := ReviewRemixDraft(ReviewOptions{Client: &reviewerFakeClient{reply: string(reply)}, Source: source, DraftJSON: string(raw)})
	if out.Record.Verdict != "fixed" || out.RevisedJSON == "" {
		t.Fatalf("mechanical overlap check rejected revision: %s", out.Record.Verdict)
	}
}

func TestEditorialPublishingUpperBoundsAndShortMinimum(t *testing.T) {
	issues := strings.Join(publishFieldIssues(remixDraft{ShortTitles: []string{"太短", "六个字以上标题", "另外一条长标题", "第四个多余标题"}, Descriptions: []string{"一。", "二。", "三。", "四。"}, Topics: []string{"财经", "#工资", "#收支", "#存钱", "#第五个"}}), "|")
	for _, part := range []string{"short_titles", "6", "descriptions", "topics", "#"} {
		if !strings.Contains(issues, part) {
			t.Errorf("missing %s in %s", part, issues)
		}
	}
}
