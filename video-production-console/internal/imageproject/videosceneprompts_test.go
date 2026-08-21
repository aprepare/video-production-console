package imageproject

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestSuggestVideoScenePromptsRequiresReadableChineseTitle(t *testing.T) {
	scenes := videoScenes(2)
	chat := &fakeMontageChat{response: videoScenePromptResponse(2)}
	prompts, err := SuggestVideoScenePrompts(context.Background(), chat, "test-model", scenes)
	if err != nil {
		t.Fatal(err)
	}
	if len(prompts) != 2 {
		t.Fatalf("prompts=%d", len(prompts))
	}
	if !strings.Contains(chat.requests[0].User, "赤墨风") || !strings.Contains(chat.requests[0].User, "9:16") {
		t.Fatalf("plan missing 赤墨风 9:16: %s", chat.requests[0].User)
	}
	if strings.Contains(chat.requests[0].System, "禁止可读") {
		t.Fatal("video scene prompts must require readable Chinese titles")
	}
	if prompts[0].Title == "" || !strings.Contains(prompts[0].Prompt, prompts[0].Title) {
		t.Fatalf("title not baked into prompt: %+v", prompts[0])
	}
}

func TestSuggestVideoScenePromptsSupportsMoreThanEighteenScenes(t *testing.T) {
	count := MaxImages + 6
	chat := &fakeMontageChat{response: videoScenePromptResponse(count)}
	prompts, err := SuggestVideoScenePrompts(context.Background(), chat, "m", videoScenes(count))
	if err != nil {
		t.Fatalf("video scene prompts must not inherit the %d-image cap: %v", MaxImages, err)
	}
	if len(prompts) != count {
		t.Fatalf("prompts=%d want %d", len(prompts), count)
	}
}

func TestParseVideoScenePromptSuggestionsRejectsMissingTitle(t *testing.T) {
	if _, err := ParseVideoScenePromptSuggestions(videoScenes(1), `{"prompts":[{"sequence":1,"title":"","prompt":"画面"}]}`); err == nil {
		t.Fatal("expected title error")
	}
}

func TestFallbackVideoScenePromptBakesTitle(t *testing.T) {
	prompt := FallbackVideoScenePrompt(VideoScene{ID: "s1", StartMS: 0, EndMS: 4500, Text: "家庭现金流正在变薄。"}, 1)
	if prompt.Title == "" || !strings.Contains(prompt.Prompt, prompt.Title) {
		t.Fatalf("fallback=%+v", prompt)
	}
	if !strings.Contains(prompt.Prompt, "赤墨风") || !strings.Contains(prompt.Prompt, "9:16") {
		t.Fatalf("fallback missing style: %s", prompt.Prompt)
	}
}

func videoScenes(count int) []VideoScene {
	scenes := make([]VideoScene, count)
	for i := range scenes {
		scenes[i] = VideoScene{
			ID:      fmt.Sprintf("scene-%03d", i+1),
			StartMS: int64(i) * 4500,
			EndMS:   int64(i+1) * 4500,
			Text:    fmt.Sprintf("第%d段旁白，讲家庭现金流风险。", i+1),
		}
	}
	return scenes
}

func videoScenePromptResponse(count int) string {
	type item struct {
		Sequence int    `json:"sequence"`
		Title    string `json:"title"`
		Prompt   string `json:"prompt"`
	}
	items := make([]item, count)
	for i := range items {
		title := fmt.Sprintf("现金流风险%02d", i+1)
		items[i] = item{Sequence: i + 1, Title: title, Prompt: "主标题「" + title + "」。赤墨风竖屏，家庭餐桌与银行流水。"}
	}
	payload, _ := json.Marshal(map[string]any{"prompts": items})
	return string(payload)
}
