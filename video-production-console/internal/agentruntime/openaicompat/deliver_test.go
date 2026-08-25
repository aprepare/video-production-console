package openaicompat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"video-production-console/internal/codex"
	"video-production-console/internal/domain"
	"video-production-console/internal/spokenlines"
)

func TestWriteRemixDeliverablePassesValidator(t *testing.T) {
	outputDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outputDir, "prompt_system.txt"), []byte("system"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outputDir, "prompt_user.txt"), []byte("user"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "又一批人要发财了，人民币第三次换锚已经开始。前两波是美元外贸和土地房子，旧锚死了，利率下来，一百七十万亿存款在找出路。第三个锚先不说完，现在就上车。"
	if err := writeRemixDeliverable(outputDir, "11111111-1111-1111-1111-111111111111", string(domain.ActionRemixStandard), script, []string{"自检：该句与原文重合未修复「示例片段」"}, "质检（test-model）：发现 1 处与原文重合，返工未见改善，保留首稿，见警告。"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(outputDir, "result.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := codex.ValidateResultEnvelopeJSON(raw, "11111111-1111-1111-1111-111111111111", domain.ActionRemixStandard, outputDir); err != nil {
		t.Fatal(err)
	}
}

func TestWriteRemixDeliverableRejectsMalformedStructuredResponseWithoutFiles(t *testing.T) {
	outputDir := t.TempDir()
	raw := `{"oops": true, "not_a_script": "太短"}`
	if err := writeRemixDeliverable(outputDir, "11111111-1111-1111-1111-111111111111", string(domain.ActionRemixStandard), raw, nil, ""); err == nil {
		t.Fatal("expected malformed structured response to fail")
	}
	if _, err := os.Stat(filepath.Join(outputDir, "model_raw.txt")); err != nil {
		t.Fatalf("malformed response must keep model_raw.txt: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outputDir, "continuous_script.txt")); err == nil {
		t.Fatal("malformed response must not write continuous_script.txt")
	}
}

func TestParseRemixDraftRecoversTruncatedContinuousScript(t *testing.T) {
	raw := `{"continuous_script":"两个月，2.05万亿，从银行账户里没了。不是大家取出来花掉了，是被一步一步赶出来的。这些钱正拐进一条大多数人到现在都没看见的道，你把时间轴拉长看，`
	draft, err := parseRemixDraft(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(draft.ContinuousScript, "2.05万亿") || !strings.Contains(draft.ContinuousScript, "时间轴拉长看") {
		t.Fatalf("script=%q", draft.ContinuousScript)
	}
	if strings.Contains(draft.ContinuousScript, `"continuous_script"`) {
		t.Fatalf("recovered script still looks like JSON: %q", draft.ContinuousScript)
	}
}

func TestParseRemixDraftExtractsJSONFromProse(t *testing.T) {
	raw := "好的，这是整理后的口播：\n```json\n{\"continuous_script\":\"两个月，老百姓存在银行里的钱少了整整2万亿。不是买房，不是炒股，那它到底变成了什么？点开主页橱窗看《财富觉醒方法论》。\",\"titles\":[\"2万亿去哪了\"],\"short_titles\":[\"2万亿去哪了\"],\"descriptions\":[\"两个月少了2万亿\"],\"topics\":[\"#经济\"],\"cta\":\"\"}\n```\n"
	draft, err := parseRemixDraft(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(draft.ContinuousScript, "2万亿") {
		t.Fatalf("script=%q", draft.ContinuousScript)
	}
}

func TestParseRemixDraftRepairsUnescapedQuotesInScript(t *testing.T) {
	raw := "```json\n{\n  \"continuous_script\": \"评论区打出\"接财\"，我会把方向说透。两个月老百姓存款少了整整2万亿，点开主页橱窗看《财富觉醒方法论》。\",\n  \"titles\": [\"2万亿去哪了\"],\n  \"short_titles\": [\"2万亿去哪了\"],\n  \"descriptions\": [\"两个月少了2万亿\"],\n  \"topics\": [\"#经济\"],\n  \"cta\": \"\"\n}\n```"
	draft, err := parseRemixDraft(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(draft.ContinuousScript, "接财") {
		t.Fatalf("script=%q", draft.ContinuousScript)
	}
}

func TestParseRemixDraftRepairsFailedTaskRaw(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "unescaped_quotes_model_raw.txt"))
	if err != nil {
		t.Fatal(err)
	}
	draft, err := parseRemixDraft(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(draft.ContinuousScript, "接财") || !strings.Contains(draft.ContinuousScript, "财富觉醒方法论") {
		t.Fatalf("script=%q", draft.ContinuousScript[:80])
	}
}

func TestPublishingFallbacksDoNotHardcodeFixedPlot(t *testing.T) {
	titles := titleFallbacks("法拍房快堆到四十万套。普通人还在观望。")
	shorts := shortTitleFallbacks("法拍房快堆到四十万套。")
	descs := descriptionFallbacks("法拍房快堆到四十万套。")
	joined := strings.Join(append(append(titles, shorts...), descs...), "\n")
	for _, forbid := range []string{"第三次换锚", "170万亿", "一百七十万亿", "第三个锚"} {
		if strings.Contains(joined, forbid) {
			t.Fatalf("fallback still hardcodes %q: %s", forbid, joined)
		}
	}
	if !strings.Contains(titles[0], "法拍房") {
		t.Fatalf("title seed should follow source, got %q", titles[0])
	}
}

func TestPickHotTopicsKeepsOnlyAllowlist(t *testing.T) {
	got := pickHotTopics([]string{"#人民币", "#经济", "#干货分享", "#随便写"}, "seed")
	if len(got) < 3 || len(got) > 4 {
		t.Fatalf("count=%d %v", len(got), got)
	}
	if got[0] != "#经济" || got[1] != "#干货分享" {
		t.Fatalf("kept order = %v", got)
	}
	allow := map[string]bool{}
	for _, tag := range hotPublishingTopics {
		allow[tag] = true
	}
	for _, tag := range got {
		if !allow[tag] {
			t.Fatalf("unexpected %q", tag)
		}
	}
}

func TestPublishingPackageRewritesInventedHashtags(t *testing.T) {
	pkg := publishingPackageFromDraft(remixDraft{
		Descriptions: []string{"前两次换锚分别推高了外贸和房子。 #人民币 #财富趋势 #经济周期"},
		Topics:       []string{"#人民币", "#财富趋势", "#资金流向"},
	}, "又一批人要发财了。")
	topics, _ := pkg["topics"].([]string)
	if len(topics) < 3 || len(topics) > 4 {
		t.Fatalf("topics=%v", topics)
	}
	joined := strings.Join(topics, " ")
	for _, desc := range pkg["descriptions"].([]string) {
		if !strings.HasSuffix(desc, joined) {
			t.Fatalf("description=%q topics=%v", desc, topics)
		}
		if strings.Contains(desc, "#人民币") || strings.Contains(desc, "#财富趋势") {
			t.Fatalf("invented hashtag leaked: %q", desc)
		}
	}
}

func TestWriteSpokenDeliverablePassesValidator(t *testing.T) {
	outputDir := t.TempDir()
	raw := "第一句。\n百分之六十七的人还在等。\n去看财富觉醒方法论。"
	if err := writeSpokenDeliverable(outputDir, "11111111-1111-1111-1111-111111111111", raw); err != nil {
		t.Fatal(err)
	}
	envelope, err := os.ReadFile(filepath.Join(outputDir, "result.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := codex.ValidateResultEnvelopeJSON(envelope, "11111111-1111-1111-1111-111111111111", domain.ActionRemixSpokenLines, outputDir); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(outputDir, "spoken_script.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "67%") || !strings.Contains(string(got), " \n") {
		t.Fatalf("spoken script=%q", got)
	}
}

func TestWriteSpokenDeliverableStripsEOSMarker(t *testing.T) {
	outputDir := t.TempDir()
	raw := "后面的政策节奏\n和钱的去向\n咱们接着盯 <|eos|>"
	if err := writeSpokenDeliverable(outputDir, "11111111-1111-1111-1111-111111111111", raw); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(outputDir, "spoken_script.txt"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	if !strings.Contains(text, "咱们接着盯") {
		t.Fatalf("spoken script missing last line: %q", text)
	}
	if strings.Contains(strings.ToLower(text), "eos") || strings.Contains(text, "<|") {
		t.Fatalf("end marker leaked into spoken_script.txt: %q", text)
	}
}

func TestWriteKeywordsDeliverablePassesValidator(t *testing.T) {
	outputDir := t.TempDir()
	lines := []string{"全国法拍房挂牌", "已经堆到40万套"}
	raw := `{"lines":[
		{"line":"全国法拍房挂牌","keywords":[{"text":"法拍房","kind":"warning"}]},
		{"line":"已经堆到40万套","keywords":[{"text":"40万套","kind":"number"}]}
	]}`
	if err := writeKeywordsDeliverable(outputDir, "11111111-1111-1111-1111-111111111111", raw, lines); err != nil {
		t.Fatal(err)
	}
	envelope, err := os.ReadFile(filepath.Join(outputDir, "result.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := codex.ValidateResultEnvelopeJSON(envelope, "11111111-1111-1111-1111-111111111111", domain.ActionCaptionKeywords, outputDir); err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(filepath.Join(outputDir, "caption_keywords.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := spokenlines.ParseKeywordDoc(stored)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Lines) != 2 || doc.Lines[0].Keywords[0].Text != "法拍房" {
		t.Fatalf("stored keywords = %#v", doc)
	}
}
