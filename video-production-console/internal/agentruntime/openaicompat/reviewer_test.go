package openaicompat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type reviewerFakeClient struct {
	reply    string
	requests []ChatRequest
}

func (c *reviewerFakeClient) Chat(req ChatRequest) (ChatResponse, error) {
	c.requests = append(c.requests, req)
	return textResponse(c.reply), nil
}

func reviewerTestDraft() string {
	raw, _ := json.Marshal(remixDraft{
		ContinuousScript: strings.Repeat("这套房子往后怎么走，家里那笔钱该往哪放，今天一次说明白。", 8),
		Titles:           []string{"标题一", "标题二"},
		ShortTitles:      []string{"板标题", "副标题", "备选"},
		Descriptions:     []string{"描述一", "描述二", "描述三"},
		Topics:           []string{"#楼市", "#财经"},
	})
	return string(raw)
}

func TestReviewRemixDraftAppliesFix(t *testing.T) {
	dir := t.TempDir()
	draftJSON := reviewerTestDraft()
	var draft remixDraft
	if err := json.Unmarshal([]byte(draftJSON), &draft); err != nil {
		t.Fatal(err)
	}
	revised := draft
	revised.ContinuousScript = strings.Replace(draft.ContinuousScript, "今天一次说明白。", "今天掰开揉碎说明白。", 1)
	reply, _ := json.Marshal(map[string]any{
		"verdict": "fixed",
		"summary": "改了开头一处。",
		"issues":  []map[string]string{{"where": "今天一次说明白", "problem": "口语度", "fix": "换成掰开揉碎"}},
		"revised": revised,
	})
	client := &reviewerFakeClient{reply: string(reply)}

	outcome := ReviewRemixDraft(ReviewOptions{
		Client: client, Model: "m", Source: strings.Repeat("原文", 200),
		DraftJSON: draftJSON, OutputDir: dir, Round: 1,
	})
	if outcome.Record.Verdict != "fixed" {
		t.Fatalf("verdict = %q, want fixed (err=%q)", outcome.Record.Verdict, outcome.Record.Error)
	}
	if !strings.Contains(outcome.RevisedJSON, "掰开揉碎") {
		t.Fatalf("revised JSON missing fix: %s", outcome.RevisedJSON)
	}
	if len(outcome.Record.Issues) != 1 {
		t.Fatalf("issues = %d, want 1", len(outcome.Record.Issues))
	}
	for _, name := range []string{"draft_v1.json", "review.json", "review_round_1.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("missing artifact %s: %v", name, err)
		}
	}
	v1Raw, err := os.ReadFile(filepath.Join(dir, "draft_v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(v1Raw), "今天一次说明白") {
		t.Fatalf("draft_v1.json should keep writer original, got: %s", string(v1Raw)[:120])
	}

	// 结论里自带审稿前后两版全字段，界面并排对照靠这个，不靠可能被手改的定稿。
	reviewRaw, err := os.ReadFile(filepath.Join(dir, "review.json"))
	if err != nil {
		t.Fatal(err)
	}
	var stored ReviewRecord
	if err := json.Unmarshal(reviewRaw, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Before == nil || !strings.Contains(stored.Before.ContinuousScript, "今天一次说明白") {
		t.Fatalf("review.json before missing writer script: %+v", stored.Before)
	}
	if stored.Revised == nil || !strings.Contains(stored.Revised.ContinuousScript, "掰开揉碎") {
		t.Fatalf("review.json revised missing fix: %+v", stored.Revised)
	}
	if len(stored.Before.ShortTitles) != 3 || len(stored.Revised.Descriptions) != 3 || len(stored.Revised.Topics) != 2 {
		t.Fatalf("both snapshots must carry publish fields: before=%+v revised=%+v", stored.Before, stored.Revised)
	}
}

func TestReviewRemixDraftAdoptsPublishFieldOnlyFix(t *testing.T) {
	dir := t.TempDir()
	draftJSON := reviewerTestDraft()
	var draft remixDraft
	if err := json.Unmarshal([]byte(draftJSON), &draft); err != nil {
		t.Fatal(err)
	}
	revised := draft
	revised.ShortTitles = []string{"三口人一户", "房贷这笔账", "钱往哪放"}
	revised.Descriptions = []string{"一户三口人，房贷怎么算才不亏？", "家里那笔钱今年往哪放。"}
	reply, _ := json.Marshal(map[string]any{
		"verdict": "fixed",
		"summary": "正文没问题，只改了短标题和描述。",
		"issues": []map[string]string{
			{"where": "板标题", "problem": "规范7：通用口号", "fix": "改成正文里的具体物"},
			{"where": "描述一", "problem": "规范7：没有具体钩子", "fix": "带上数字"},
		},
		"revised": revised,
	})
	client := &reviewerFakeClient{reply: string(reply)}
	outcome := ReviewRemixDraft(ReviewOptions{
		Client: client, Model: "m", Source: "原文", DraftJSON: draftJSON, OutputDir: dir, Round: 1,
	})
	if outcome.Record.Verdict != "fixed" {
		t.Fatalf("verdict = %q, want fixed (err=%q)", outcome.Record.Verdict, outcome.Record.Error)
	}
	var got remixDraft
	if err := json.Unmarshal([]byte(outcome.RevisedJSON), &got); err != nil {
		t.Fatal(err)
	}
	if got.ContinuousScript != draft.ContinuousScript {
		t.Fatal("script must stay untouched when only publish fields were fixed")
	}
	if got.ShortTitles[0] != "三口人一户" || len(got.Descriptions) != 2 {
		t.Fatalf("publish field fixes not adopted: %+v", got)
	}
	if outcome.Record.Revised == nil || outcome.Record.Revised.ShortTitles[0] != "三口人一户" {
		t.Fatalf("record.revised must carry fixed publish fields: %+v", outcome.Record.Revised)
	}
	if outcome.Record.Before == nil || outcome.Record.Before.ShortTitles[0] != "板标题" {
		t.Fatalf("record.before must keep writer publish fields: %+v", outcome.Record.Before)
	}
}

func TestReviewRemixDraftPassKeepsOriginal(t *testing.T) {
	dir := t.TempDir()
	reply := `{"verdict":"pass","summary":"没有问题。","issues":[],"revised":{}}`
	client := &reviewerFakeClient{reply: reply}
	outcome := ReviewRemixDraft(ReviewOptions{
		Client: client, Model: "m", Source: "原文", DraftJSON: reviewerTestDraft(), OutputDir: dir, Round: 1,
	})
	if outcome.Record.Verdict != "pass" {
		t.Fatalf("verdict = %q, want pass", outcome.Record.Verdict)
	}
	if outcome.RevisedJSON != "" {
		t.Fatalf("pass verdict must keep original, got revised: %s", outcome.RevisedJSON)
	}
}

func TestReviewRemixDraftKeepsShortRevisionForHumanChoice(t *testing.T) {
	dir := t.TempDir()
	reply, _ := json.Marshal(map[string]any{
		"verdict": "fixed",
		"summary": "重写了整篇。",
		"issues":  []map[string]string{{"where": "全文", "problem": "口感", "fix": "重写"}},
		"revised": remixDraft{ContinuousScript: "只剩一句话。"},
	})
	client := &reviewerFakeClient{reply: string(reply)}
	outcome := ReviewRemixDraft(ReviewOptions{
		Client: client, Model: "m", Source: "原文", DraftJSON: reviewerTestDraft(), OutputDir: dir, Round: 2,
	})
	if outcome.Record.Verdict != "fixed" {
		t.Fatalf("verdict = %q, want fixed", outcome.Record.Verdict)
	}
	if outcome.RevisedJSON == "" {
		t.Fatalf("short revision must be preserved, got: %s", outcome.RevisedJSON)
	}
	if _, err := os.Stat(filepath.Join(dir, "review_round_2.json")); err != nil {
		t.Fatalf("missing round artifact: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "draft_v1.json")); err == nil {
		t.Fatal("round 2 must not overwrite draft_v1.json")
	}
}

func TestReviewRemixDraftUnparsableReplyBecomesError(t *testing.T) {
	dir := t.TempDir()
	client := &reviewerFakeClient{reply: "我审完了，没问题。"}
	outcome := ReviewRemixDraft(ReviewOptions{
		Client: client, Model: "m", Source: "原文", DraftJSON: reviewerTestDraft(), OutputDir: dir,
	})
	if outcome.Record.Verdict != "error" {
		t.Fatalf("verdict = %q, want error", outcome.Record.Verdict)
	}
	if outcome.RevisedJSON != "" {
		t.Fatal("unparsable reply must keep original draft")
	}
	// 解析失败必须留现场，否则永远查不出模型回了什么。
	raw, err := os.ReadFile(filepath.Join(dir, "review_reply_raw.txt"))
	if err != nil || string(raw) != "我审完了，没问题。" {
		t.Fatalf("raw reply not persisted: %q, %v", string(raw), err)
	}
	if !strings.Contains(outcome.Record.Error, "review_reply_raw.txt") {
		t.Fatalf("error should point to raw artifact: %q", outcome.Record.Error)
	}
}

// 思考模型的常见烂法：JSON 前带说明文字、字符串里带裸换行。都要能解析。
func TestParseReviewerReplyRepairsControlCharsAndPreamble(t *testing.T) {
	withNewlines := "{\"verdict\": \"fixed\",\n\"summary\": \"改了一处\",\n\"issues\": [],\n" +
		"\"revised\": {\"continuous_script\": \"第一段。\n\n第二段带\t制表符。\"}}"
	reply, err := parseReviewerReply(withNewlines)
	if err != nil {
		t.Fatalf("raw newlines in string: %v", err)
	}
	if reply.Verdict != "fixed" || !strings.Contains(reply.Revised.ContinuousScript, "第二段") {
		t.Fatalf("parsed reply: %+v", reply)
	}

	withPreamble := "好的，我按清单审完了，结论如下：\n\n" +
		`{"verdict": "pass", "summary": "没有问题", "issues": [], "revised": {}}` +
		"\n\n以上就是审稿结论。"
	reply, err = parseReviewerReply(withPreamble)
	if err != nil {
		t.Fatalf("preamble text: %v", err)
	}
	if reply.Verdict != "pass" {
		t.Fatalf("verdict = %q", reply.Verdict)
	}
}

func TestReviewRemixDraftUsesSystemOverride(t *testing.T) {
	reply := `{"verdict":"pass","summary":"ok","issues":[],"revised":{}}`
	client := &reviewerFakeClient{reply: reply}
	ReviewRemixDraft(ReviewOptions{
		Client: client, Model: "m", Source: "原文", DraftJSON: reviewerTestDraft(),
		OutputDir: t.TempDir(), SystemPrompt: "自定义审稿规则：只查课尾。",
	})
	if len(client.requests) != 1 || !strings.Contains(client.requests[0].Messages[0].Content, "自定义审稿规则：只查课尾。") || !strings.Contains(client.requests[0].Messages[0].Content, SharedEditorialPolicy) {
		t.Fatalf("system override not applied: %+v", client.requests[0].Messages[0].Content[:40])
	}
}

func TestReviewRemixDraftSendsAnnotations(t *testing.T) {
	reply := `{"verdict":"pass","summary":"ok","issues":[],"revised":{}}`
	client := &reviewerFakeClient{reply: reply}
	ReviewRemixDraft(ReviewOptions{
		Client: client, Model: "m", Source: "原文正文",
		DraftJSON: reviewerTestDraft(), OutputDir: t.TempDir(),
		Annotations: "开头第二句删掉；话题换成#房贷", Round: 2,
	})
	if len(client.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(client.requests))
	}
	user := client.requests[0].Messages[1].Content
	if !strings.Contains(user, "开头第二句删掉") || !strings.Contains(user, "# 同行原文") {
		t.Fatalf("user message missing annotations/source: %s", user[:200])
	}
}

func TestDefaultReviewerPromptUsesSharedCourseAndEditingRules(t *testing.T) {
	prompt := DefaultReviewerPrompt()
	for _, want := range []string{SharedEditorialPolicy, "只修有证据的问题", "禁止整篇重写", "where", "课程资料", "关注理由", "祝福互动"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("missing %q", want)
		}
	}
	for _, old := range []string{"原样照搬前两三句", "命定留存句", "小数最多留一位", "年份、百分比、金额与原文逐个核对，写错的改回原文"} {
		if strings.Contains(prompt, old) {
			t.Fatalf("stale conflicting rule %q", old)
		}
	}
}
