package imageproject

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	VideoSceneRatio = "9:16"
	VideoSceneStyle = "red_ink"
	minSceneTitle   = 4
	maxSceneTitle   = 14
)

// VideoScene is one timed narration slice that needs a titled still.
type VideoScene struct {
	ID      string `json:"segment_id"`
	StartMS int64  `json:"start_ms"`
	EndMS   int64  `json:"end_ms"`
	Text    string `json:"text"`
}

// VideoScenePrompt pairs a baked-in Chinese title with its image prompt.
type VideoScenePrompt struct {
	SegmentID string
	Title     string
	Prompt    string
}

// SuggestVideoScenePrompts asks the text model for one 9:16 赤墨风 prompt per
// timed scene. Unlike montage prompts, readable Chinese titles are required
// and printed into the image. Scene count is not limited to MaxImages.
func SuggestVideoScenePrompts(ctx context.Context, client ChatClient, model string, scenes []VideoScene) ([]VideoScenePrompt, error) {
	if client == nil {
		return nil, errors.New("text model is not configured")
	}
	if err := validateVideoScenes(scenes); err != nil {
		return nil, err
	}
	system, user := VideoScenePromptPlan(scenes)
	raw, err := client.Complete(ctx, ChatRequest{Model: model, System: system, User: user})
	if err != nil {
		return nil, err
	}
	return ParseVideoScenePromptSuggestions(scenes, raw)
}

func validateVideoScenes(scenes []VideoScene) error {
	if len(scenes) == 0 {
		return errors.New("timed scenes are required")
	}
	seen := make(map[string]bool, len(scenes))
	for index, scene := range scenes {
		id := strings.TrimSpace(scene.ID)
		if id == "" || seen[id] {
			return fmt.Errorf("scene %d needs a unique id", index+1)
		}
		seen[id] = true
		if strings.TrimSpace(scene.Text) == "" {
			return fmt.Errorf("scene %q needs narration text", id)
		}
		if scene.StartMS < 0 || scene.EndMS <= scene.StartMS {
			return fmt.Errorf("scene %q has an invalid time range", id)
		}
	}
	return nil
}

// VideoScenePromptPlan builds the chat request for titled 赤墨风 stills.
func VideoScenePromptPlan(scenes []VideoScene) (system, user string) {
	system = "你是财经图文视频的画面提示词编辑。按口播镜头写生图提示词，中文标题必须印在图内并保持准确可读，不要改写段落原文，不要编造数字、日期或收益。只输出 JSON。"
	styleText := styleInstructions[VideoSceneStyle]
	var builder strings.Builder
	builder.WriteString("为每个口播镜头写一条生图提示词。规则：\n")
	fmt.Fprintf(&builder, "1. 画幅 %s，全片统一风格：%s。\n", VideoSceneRatio, styleText)
	fmt.Fprintf(&builder, "2. 每条必须含主标题 %d-%d 个中文汉字，直接印入画面；可用可选副标题。标题必须忠实概括该镜头口播，不得遮挡人物面部。\n", minSceneTitle, maxSceneTitle)
	builder.WriteString("3. 优先中国家庭、银行、住房、养老与商业场景；禁止乱码、伪文字、水印、二维码、品牌标志。\n")
	builder.WriteString("4. sequence 与镜头顺序一一对应，一段一条。\n")
	builder.WriteString(imageTextLayoutRules + "\n")
	builder.WriteString("输出：{\"prompts\":[{\"sequence\":1,\"title\":\"\",\"prompt\":\"\"}]}\n\n镜头：\n")
	encoded, _ := json.Marshal(withVideoSceneSequence(scenes))
	builder.Write(encoded)
	return system, builder.String()
}

type sequencedVideoScene struct {
	Sequence int `json:"sequence"`
	VideoScene
}

func withVideoSceneSequence(scenes []VideoScene) []sequencedVideoScene {
	sequenced := make([]sequencedVideoScene, len(scenes))
	for index, scene := range scenes {
		sequenced[index] = sequencedVideoScene{Sequence: index + 1, VideoScene: scene}
	}
	return sequenced
}

func ParseVideoScenePromptSuggestions(scenes []VideoScene, raw string) ([]VideoScenePrompt, error) {
	if len(scenes) == 0 {
		return nil, errors.New("timed scenes are required")
	}
	var payload struct {
		Prompts []struct {
			Sequence int    `json:"sequence"`
			Title    string `json:"title"`
			Prompt   string `json:"prompt"`
		} `json:"prompts"`
	}
	if err := decodePlannerJSON(raw, &payload); err != nil {
		return nil, err
	}
	if len(payload.Prompts) != len(scenes) {
		return nil, errors.New("prompt count must match the timed scenes")
	}
	result := make([]VideoScenePrompt, len(scenes))
	seen := make(map[int]bool, len(scenes))
	for _, item := range payload.Prompts {
		if item.Sequence < 1 || item.Sequence > len(scenes) || seen[item.Sequence] {
			return nil, errors.New("prompt sequence is invalid")
		}
		title := strings.TrimSpace(item.Title)
		prompt := strings.TrimSpace(item.Prompt)
		if title == "" || prompt == "" {
			return nil, errors.New("title and prompt are required")
		}
		if n := utf8.RuneCountInString(title); n < minSceneTitle || n > maxSceneTitle {
			return nil, fmt.Errorf("title length must be between %d and %d characters", minSceneTitle, maxSceneTitle)
		}
		if !strings.Contains(prompt, title) {
			prompt = fmt.Sprintf("主标题「%s」。%s", title, prompt)
		}
		seen[item.Sequence] = true
		result[item.Sequence-1] = VideoScenePrompt{SegmentID: scenes[item.Sequence-1].ID, Title: title, Prompt: prompt}
	}
	return result, nil
}

// FallbackVideoScenePrompt builds a local titled prompt when the text model fails.
func FallbackVideoScenePrompt(scene VideoScene, sequence int) VideoScenePrompt {
	title := strings.TrimSpace(strings.TrimRight(scene.Text, "。！？!?；;，,、. "))
	runes := []rune(title)
	if len(runes) > maxSceneTitle {
		title = string(runes[:maxSceneTitle])
	}
	if utf8.RuneCountInString(title) < minSceneTitle {
		title = fmt.Sprintf("镜头%02d要点", sequence)
	}
	prompt := BuildPrompt(PromptInput{
		SourceText: scene.Text,
		Ratio:      VideoSceneRatio,
		Style:      VideoSceneStyle,
		Role:       RoleContent,
	})
	if !strings.Contains(prompt, title) {
		prompt = fmt.Sprintf("主标题「%s」。%s", title, prompt)
	}
	id := strings.TrimSpace(scene.ID)
	if id == "" {
		id = fmt.Sprintf("scene-%03d", sequence)
	}
	return VideoScenePrompt{SegmentID: id, Title: title, Prompt: prompt}
}
