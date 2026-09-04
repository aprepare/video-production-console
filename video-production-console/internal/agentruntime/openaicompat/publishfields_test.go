package openaicompat

import (
	"strings"
	"testing"
)

func TestPublishFieldIssuesDetectsShortAndOverlong(t *testing.T) {
	draft := remixDraft{
		ContinuousScript: "正文",
		ShortTitles:      []string{"第六次财富洗牌来了", "9月1号之后，三份国家级文件同一天生效了"},
		Descriptions:     []string{"三份文件同一天生效。"},
		Topics:           []string{"#财经"},
	}
	issues := publishFieldIssues(draft)
	joined := strings.Join(issues, "|")
	for _, want := range []string{"short_titles 只有 2 条", "超过 15 个字", "descriptions 只有 1 条", "topics 只有 1 个"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing issue %q in %v", want, issues)
		}
	}
	clean := remixDraft{
		ShortTitles:  []string{"9月1日起你的数据能换钱了", "五次机会你抓住过几回", "这次不用本金也能进场"},
		Descriptions: []string{"三份文件同一天生效，你的痕迹值钱了。", "五次机会你抓住过几回？这回不用本金。"},
		Topics:       []string{"#财经", "#数据资产", "#新规落地"},
	}
	if got := publishFieldIssues(clean); len(got) != 0 {
		t.Fatalf("clean draft flagged: %v", got)
	}
}

// 发布字段给少了：一轮定向返工补齐，正文原样保留；模型顺手改正文也不采用。
func TestRepairPublishFieldsKeepsScriptAndFillsFields(t *testing.T) {
	content := `{"continuous_script":"原文正文一二三。","titles":[],"short_titles":["第六次财富洗牌来了"],"descriptions":[],"topics":["#经济"],"cta":""}`
	fixed := `{"continuous_script":"模型偷偷改了正文。","titles":[],"short_titles":["9月1日起你的数据能换钱了","五次机会你抓住过几回","这次不用本金也能进场"],"descriptions":["三份文件同一天生效，你的痕迹值钱了。","十个人里八个会划走，三个月后差距就出来了。"],"topics":["#财经","#数据资产","#9月1日新规"],"cta":""}`
	client := &sequenceClient{responses: []string{fixed}}
	out, note := repairPublishFields(client, "m", "", "sys", "user", content, t.TempDir())
	if len(client.requests) != 1 {
		t.Fatalf("expected one repair request, got %d", len(client.requests))
	}
	if !strings.Contains(client.requests[0].Messages[3].Content, "short_titles 只有 1 条") {
		t.Fatalf("repair prompt must list issues: %s", client.requests[0].Messages[3].Content)
	}
	draft, err := parseRemixDraft(out)
	if err != nil {
		t.Fatal(err)
	}
	if draft.ContinuousScript != "原文正文一二三。" {
		t.Fatalf("script must stay untouched, got %q", draft.ContinuousScript)
	}
	if len(draft.ShortTitles) != 3 || len(draft.Descriptions) != 2 || len(draft.Topics) != 3 {
		t.Fatalf("fields not filled: %+v", draft)
	}
	if !strings.Contains(note, "已定向返工") {
		t.Fatalf("note=%q", note)
	}

	// 已合格的稿子不打模型。
	quiet := &sequenceClient{}
	if _, note := repairPublishFields(quiet, "m", "", "sys", "user", out, t.TempDir()); note != "" || len(quiet.requests) != 0 {
		t.Fatalf("clean content must not trigger repair: note=%q requests=%d", note, len(quiet.requests))
	}
}

func TestFallbacksNoLongerInventSlogans(t *testing.T) {
	script := "9月1号之后，三份国家级文件同一天生效了。你还觉得这事跟你没关系？后面是正文。"
	for _, s := range shortTitleFallbacks(script) {
		if strings.Contains(s, "窗口不会等人") || strings.Contains(s, "钱会流向哪里") || strings.Contains(s, "窗口来了") {
			t.Fatalf("slogan fallback leaked: %q", s)
		}
		if n := len([]rune(s)); n > 15 || n < 6 {
			t.Fatalf("fallback title length out of range: %q", s)
		}
	}
	descs := descriptionFallbacks(script)
	if len(descs) != 2 || !strings.Contains(descs[1], "跟你没关系") {
		t.Fatalf("description fallbacks should be first sentence + first question: %v", descs)
	}
	pkg := publishingPackageFromDraft(remixDraft{}, script)
	titles, _ := pkg["titles"].([]string)
	if len(titles) != 0 {
		t.Fatalf("no invented titles expected, got %v", titles)
	}
	for _, d := range pkg["descriptions"].([]string) {
		if strings.Contains(d, "看懂资金上游") || strings.Contains(d, "答案先留着") {
			t.Fatalf("canned description leaked: %q", d)
		}
	}
}

func TestClipDescriptionBodyNeverCutsMidSentence(t *testing.T) {
	long := "9月1号三份国家级文件同时落地，你每天留下的痕迹正在被编号定价，变出来的钱可能是你一辈子都够不着的数目，而且这才刚开始还远没有到头呢"
	if kept := clipDescriptionBody("这句刚好不到六十个字所以原样保留，不做任何截断处理，也不补句号"); strings.HasSuffix(kept, "。") {
		t.Fatalf("under the hard limit must be kept verbatim, got %q", kept)
	}
	got := clipDescriptionBody(long)
	if strings.HasSuffix(got, "可能是") || !strings.HasSuffix(got, "。") {
		t.Fatalf("must cut at a clause boundary and close the sentence, got %q", got)
	}
	if len([]rune(got)) > descriptionHardRunes+1 {
		t.Fatalf("too long: %d", len([]rune(got)))
	}
	short := "三份文件同一天生效，你的痕迹值钱了。第二句也留着。第三句不要。"
	if got := clipDescriptionBody(short); strings.Contains(got, "第三句") {
		t.Fatalf("more than two sentences kept: %q", got)
	}
}
