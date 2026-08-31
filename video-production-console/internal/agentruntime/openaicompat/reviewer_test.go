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

func TestReviewRemixDraftRejectsWholesaleRewrite(t *testing.T) {
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
	if outcome.Record.Verdict != "error" {
		t.Fatalf("verdict = %q, want error", outcome.Record.Verdict)
	}
	if outcome.RevisedJSON != "" {
		t.Fatalf("wholesale rewrite must be discarded, got: %s", outcome.RevisedJSON)
	}
	if _, err := os.Stat(filepath.Join(dir, "review_round_2.json")); err != nil {
		t.Fatalf("missing round artifact: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "draft_v1.json")); err == nil {
		t.Fatal("round 2 must not overwrite draft_v1.json")
	}
}

func TestReviewRemixDraftUnparsableReplyBecomesError(t *testing.T) {
	client := &reviewerFakeClient{reply: "我审完了，没问题。"}
	outcome := ReviewRemixDraft(ReviewOptions{
		Client: client, Model: "m", Source: "原文", DraftJSON: reviewerTestDraft(), OutputDir: t.TempDir(),
	})
	if outcome.Record.Verdict != "error" {
		t.Fatalf("verdict = %q, want error", outcome.Record.Verdict)
	}
	if outcome.RevisedJSON != "" {
		t.Fatal("unparsable reply must keep original draft")
	}
}

func TestReviewRemixDraftUsesSystemOverride(t *testing.T) {
	reply := `{"verdict":"pass","summary":"ok","issues":[],"revised":{}}`
	client := &reviewerFakeClient{reply: reply}
	ReviewRemixDraft(ReviewOptions{
		Client: client, Model: "m", Source: "原文", DraftJSON: reviewerTestDraft(),
		OutputDir: t.TempDir(), SystemPrompt: "自定义审稿规则：只查课尾。",
	})
	if len(client.requests) != 1 || client.requests[0].Messages[0].Content != "自定义审稿规则：只查课尾。" {
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
