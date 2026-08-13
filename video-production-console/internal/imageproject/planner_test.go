package imageproject

import (
	"strings"
	"testing"
)

func TestParseSegmentSuggestionsKeepsOriginalTextAndCoverFirst(t *testing.T) {
	script := "存款看起来很多，现金流已经断了。房子卖不掉，养老钱会被房子拖死。子女工作也不稳。"
	raw := `{
		"segments": [
			{"sequence":1,"role":"cover","title":"现金流断了","source_text":"存款看起来很多，现金流已经断了。","rationale":"开场反差"},
			{"sequence":2,"role":"content","title":"房子拖死养老","source_text":"房子卖不掉，养老钱会被房子拖死。","rationale":"房产风险"},
			{"sequence":3,"role":"content","title":"子女工作不稳","source_text":"子女工作也不稳。","rationale":"家庭压力"}
		]
	}`
	segments, err := ParseSegmentSuggestions(script, raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 3 || segments[0].Role != "cover" || segments[0].Sequence != 1 {
		t.Fatalf("segments=%+v", segments)
	}
	if segments[1].Role != "content" || segments[2].SourceText != "子女工作也不稳。" {
		t.Fatalf("order or text changed: %+v", segments)
	}
}

func TestParseSegmentSuggestionsRejectsRewriteAndTooManyCards(t *testing.T) {
	script := "第一句。第二句。"
	if _, err := ParseSegmentSuggestions(script, `{"segments":[{"sequence":1,"role":"cover","title":"x","source_text":"这是改写后的句子。"}]}`); err == nil {
		t.Fatal("rewritten source accepted")
	}
	tooMany := `{"segments":[`
	for i := 1; i <= 19; i++ {
		if i > 1 {
			tooMany += ","
		}
		tooMany += `{"sequence":` + itoa(i) + `,"role":"content","title":"t","source_text":"第一句。"}`
	}
	tooMany += `]}`
	if _, err := ParseSegmentSuggestions(script, tooMany); err == nil {
		t.Fatal("19 segments accepted")
	}
}

func TestParsePromptSuggestionsAlignsToConfirmedSegments(t *testing.T) {
	prompts, err := ParsePromptSuggestions([]Segment{
		{Sequence: 1, Role: "cover", SourceText: "开场。"},
		{Sequence: 2, Role: "content", SourceText: "正文。"},
	}, `{"prompts":[{"sequence":2,"prompt":"第二张图"},{"sequence":1,"prompt":"封面图，强冲突"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if prompts[0] != "封面图，强冲突" || prompts[1] != "第二张图" {
		t.Fatalf("prompts=%v", prompts)
	}
}

func TestBuildPromptMarksCoverAndForbidsInventedText(t *testing.T) {
	cover := BuildPrompt(PromptInput{SourceText: "存款很多，现金流断了。", Ratio: "3:4", Style: "dark_crisis", Role: "cover"})
	body := BuildPrompt(PromptInput{SourceText: "房子卖不掉。", Ratio: "3:4", Style: "dark_crisis", Role: "content"})
	for _, required := range []string{"封面", "第一眼", "3:4", "不要生成可读文字"} {
		if !strings.Contains(cover, required) {
			t.Fatalf("cover prompt missing %q: %s", required, cover)
		}
	}
	if strings.Contains(body, "这是封面图") {
		t.Fatalf("content prompt should not be marked as cover: %s", body)
	}
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := []byte{}
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}
