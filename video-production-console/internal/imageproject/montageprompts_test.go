package imageproject

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

type fakeMontageChat struct {
	requests []ChatRequest
	response string
	err      error
}

func (f *fakeMontageChat) Complete(_ context.Context, request ChatRequest) (string, error) {
	f.requests = append(f.requests, request)
	return f.response, f.err
}

func montageSegments(count int) []MontageSegment {
	segments := make([]MontageSegment, count)
	for i := range segments {
		segments[i] = MontageSegment{
			ID:      fmt.Sprintf("seg-%03d", i+1),
			StartMS: int64(i) * 5000,
			EndMS:   int64(i+1) * 5000,
			Text:    fmt.Sprintf("第%d段旁白，讲家庭现金流风险。", i+1),
		}
	}
	return segments
}

func montagePromptResponse(count int) string {
	type item struct {
		Sequence int    `json:"sequence"`
		Prompt   string `json:"prompt"`
	}
	items := make([]item, count)
	for i := range items {
		items[i] = item{Sequence: i + 1, Prompt: fmt.Sprintf("画面 %d：深夜家庭餐桌", i+1)}
	}
	payload, _ := json.Marshal(map[string]any{"prompts": items})
	return string(payload)
}

func testMontageStyle() MontageStyle {
	return MontageStyle{
		Preset:  "red_ink",
		Ratio:   "16:9",
		Palette: "米白旧纸配黑墨与暗红",
		Motif:   "中年父亲的背影",
	}
}

func TestSuggestMontagePromptsBuildsAnchoredTemplate(t *testing.T) {
	chat := &fakeMontageChat{response: montagePromptResponse(2)}
	segments := montageSegments(2)
	prompts, err := SuggestMontagePrompts(context.Background(), chat, "test-model", segments, testMontageStyle())
	if err != nil {
		t.Fatal(err)
	}
	if len(prompts) != 2 {
		t.Fatalf("prompts=%d", len(prompts))
	}
	for i, prompt := range prompts {
		if prompt.SegmentID != segments[i].ID {
			t.Fatalf("prompt %d segment=%q want %q", i, prompt.SegmentID, segments[i].ID)
		}
		if strings.TrimSpace(prompt.Prompt) == "" {
			t.Fatalf("prompt %d is empty", i)
		}
	}
	if len(chat.requests) != 1 {
		t.Fatalf("chat calls=%d", len(chat.requests))
	}
	request := chat.requests[0]
	if request.Model != "test-model" {
		t.Fatalf("model=%q", request.Model)
	}
	// 一致性锚必须进入模板。
	for _, anchor := range []string{"米白旧纸配黑墨与暗红", "中年父亲的背影"} {
		if !strings.Contains(request.User, anchor) {
			t.Fatalf("user prompt missing anchor %q:\n%s", anchor, request.User)
		}
	}
	// 复用图文模式的风格预设文案。
	if !strings.Contains(request.User, "赤墨风") {
		t.Fatalf("user prompt missing style preset text:\n%s", request.User)
	}
	if !strings.Contains(request.User, "16:9") {
		t.Fatalf("user prompt missing ratio:\n%s", request.User)
	}
	// 既有负面约束：无伪文字/日期/收益率。
	if !strings.Contains(request.User, "伪文字") {
		t.Fatalf("user prompt missing pseudo-text ban:\n%s", request.User)
	}
	if !strings.Contains(request.System, "不要编造数字、日期、收益") {
		t.Fatalf("system prompt missing fabrication ban:\n%s", request.System)
	}
	// 带时间的段落必须原样进入模板供模型对齐。
	if !strings.Contains(request.User, segments[0].Text) || !strings.Contains(request.User, `"start_ms":0`) {
		t.Fatalf("user prompt missing timed segments:\n%s", request.User)
	}
}

func TestSuggestMontagePromptsSupportsMoreThanEighteenSegments(t *testing.T) {
	count := MaxImages + 6
	chat := &fakeMontageChat{response: montagePromptResponse(count)}
	prompts, err := SuggestMontagePrompts(context.Background(), chat, "m", montageSegments(count), testMontageStyle())
	if err != nil {
		t.Fatalf("montage prompts must not inherit the %d-image cap: %v", MaxImages, err)
	}
	if len(prompts) != count {
		t.Fatalf("prompts=%d want %d", len(prompts), count)
	}
}

func TestSuggestMontagePromptsUsesCustomStyleDescription(t *testing.T) {
	chat := &fakeMontageChat{response: montagePromptResponse(1)}
	style := MontageStyle{Preset: "custom", CustomStyle: "低饱和胶片质感，城市夜景", Ratio: "9:16", Palette: "青灰", Motif: "空钱包"}
	if _, err := SuggestMontagePrompts(context.Background(), chat, "m", montageSegments(1), style); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(chat.requests[0].User, "低饱和胶片质感，城市夜景") {
		t.Fatalf("custom style missing:\n%s", chat.requests[0].User)
	}
}

func TestSuggestMontagePromptsRequiresConsistencyAnchor(t *testing.T) {
	chat := &fakeMontageChat{response: montagePromptResponse(1)}
	style := MontageStyle{Preset: "finance_documentary", Ratio: "16:9"}
	if _, err := SuggestMontagePrompts(context.Background(), chat, "m", montageSegments(1), style); err == nil {
		t.Fatal("missing palette and motif must fail")
	}
	if len(chat.requests) != 0 {
		t.Fatal("invalid input must not reach the chat model")
	}
}

func TestSuggestMontagePromptsValidatesSegments(t *testing.T) {
	chat := &fakeMontageChat{response: montagePromptResponse(1)}
	style := testMontageStyle()
	ctx := context.Background()
	if _, err := SuggestMontagePrompts(ctx, chat, "m", nil, style); err == nil {
		t.Fatal("empty segments must fail")
	}
	bad := montageSegments(2)
	bad[1].EndMS = bad[1].StartMS
	if _, err := SuggestMontagePrompts(ctx, chat, "m", bad, style); err == nil {
		t.Fatal("zero-length segment must fail")
	}
	duplicated := montageSegments(2)
	duplicated[1].ID = duplicated[0].ID
	if _, err := SuggestMontagePrompts(ctx, chat, "m", duplicated, style); err == nil {
		t.Fatal("duplicate segment id must fail")
	}
	blank := montageSegments(1)
	blank[0].Text = "   "
	if _, err := SuggestMontagePrompts(ctx, chat, "m", blank, style); err == nil {
		t.Fatal("blank segment text must fail")
	}
	if _, err := SuggestMontagePrompts(ctx, nil, "m", montageSegments(1), style); err == nil {
		t.Fatal("nil chat client must fail")
	}
	if len(chat.requests) != 0 {
		t.Fatal("invalid inputs must not reach the chat model")
	}
}

func TestParseMontagePromptSuggestionsRejectsBadPayloads(t *testing.T) {
	segments := montageSegments(2)
	cases := []struct {
		name string
		raw  string
	}{
		{"count mismatch", montagePromptResponse(1)},
		{"duplicate sequence", `{"prompts":[{"sequence":1,"prompt":"a"},{"sequence":1,"prompt":"b"}]}`},
		{"out of range sequence", `{"prompts":[{"sequence":1,"prompt":"a"},{"sequence":9,"prompt":"b"}]}`},
		{"empty prompt", `{"prompts":[{"sequence":1,"prompt":"a"},{"sequence":2,"prompt":"  "}]}`},
		{"not json", "haha"},
	}
	for _, testCase := range cases {
		if _, err := ParseMontagePromptSuggestions(segments, testCase.raw); err == nil {
			t.Fatalf("%s must fail", testCase.name)
		}
	}
	// 代码块包裹的合法 JSON 沿用既有宽容解析。
	wrapped := "```json\n" + montagePromptResponse(2) + "\n```"
	prompts, err := ParseMontagePromptSuggestions(segments, wrapped)
	if err != nil || len(prompts) != 2 {
		t.Fatalf("wrapped payload prompts=%d err=%v", len(prompts), err)
	}
}
