// montageprompts.go adapts the image-mode AI prompt planner for montage use:
// timed narrative segments plus series-consistency anchors, with no 18-image
// cap. It reuses SuggestPrompts' chat/JSON approach and style presets and
// deliberately stays off the retired SplitScript/BuildPrompt path.
package imageproject

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// MontageSegment is one timed narration slice that needs a generated visual.
type MontageSegment struct {
	ID      string `json:"segment_id"`
	StartMS int64  `json:"start_ms"`
	EndMS   int64  `json:"end_ms"`
	Text    string `json:"text"`
}

// MontageStyle carries the global look plus the consistency anchors that keep
// a whole series coherent: a shared palette and a recurring visual motif.
// Preset selects an image-mode style preset; "custom" (or an unknown preset)
// falls back to CustomStyle, then to the finance documentary default.
type MontageStyle struct {
	Preset      string
	CustomStyle string
	Ratio       string
	Palette     string
	Motif       string
}

// MontagePrompt pairs a generated image prompt with its source segment.
type MontagePrompt struct {
	SegmentID string
	Prompt    string
}

// SuggestMontagePrompts asks the text model for one image prompt per timed
// segment. Unlike the image-mode card planner, the segment count is not
// limited to MaxImages.
func SuggestMontagePrompts(ctx context.Context, client ChatClient, model string, segments []MontageSegment, style MontageStyle) ([]MontagePrompt, error) {
	if client == nil {
		return nil, errors.New("text model is not configured")
	}
	if err := validateMontageSegments(segments); err != nil {
		return nil, err
	}
	if strings.TrimSpace(style.Palette) == "" && strings.TrimSpace(style.Motif) == "" {
		return nil, errors.New("montage prompts require a consistency anchor (palette or motif)")
	}
	system, user := MontagePromptPlan(segments, style)
	raw, err := client.Complete(ctx, ChatRequest{Model: model, System: system, User: user})
	if err != nil {
		return nil, err
	}
	return ParseMontagePromptSuggestions(segments, raw)
}

func validateMontageSegments(segments []MontageSegment) error {
	if len(segments) == 0 {
		return errors.New("timed segments are required")
	}
	seen := make(map[string]bool, len(segments))
	for index, segment := range segments {
		id := strings.TrimSpace(segment.ID)
		if id == "" || seen[id] {
			return fmt.Errorf("segment %d needs a unique id", index+1)
		}
		seen[id] = true
		if strings.TrimSpace(segment.Text) == "" {
			return fmt.Errorf("segment %q needs narration text", id)
		}
		if segment.StartMS < 0 || segment.EndMS <= segment.StartMS {
			return fmt.Errorf("segment %q has an invalid time range", id)
		}
	}
	return nil
}

// MontagePromptPlan builds the chat request. The template always carries the
// consistency anchors and the established negative constraints (no readable
// or pseudo text, no fabricated numbers, dates, or yields).
func MontagePromptPlan(segments []MontageSegment, style MontageStyle) (system, user string) {
	system = "你是财经混剪视频的画面提示词编辑。按带时间的叙事段落写生图提示词，不要改写段落原文，不要编造数字、日期、收益或可读中文。只输出 JSON。"
	styleText := styleInstructions[style.Preset]
	if style.Preset == "custom" || styleText == "" {
		styleText = strings.TrimSpace(style.CustomStyle)
	}
	if styleText == "" {
		styleText = styleInstructions["finance_documentary"]
	}
	ratio := strings.TrimSpace(style.Ratio)
	if ratio == "" {
		ratio = "16:9"
	}
	var builder strings.Builder
	builder.WriteString("为每个叙事段落写一条生图提示词。规则：\n")
	fmt.Fprintf(&builder, "1. 画幅 %s，全片统一风格：%s。\n", ratio, styleText)
	builder.WriteString("2. 系列一致性锚，必须写进每条提示词：")
	if palette := strings.TrimSpace(style.Palette); palette != "" {
		fmt.Fprintf(&builder, "统一色板：%s。", palette)
	}
	if motif := strings.TrimSpace(style.Motif); motif != "" {
		fmt.Fprintf(&builder, "固定视觉母题：%s。", motif)
	}
	builder.WriteString("\n")
	builder.WriteString("3. 画面要贴合段落的叙事与情绪，可用直接主题或隐喻，但禁止可读文字、伪文字、水印、品牌、表格数字、日期与收益率。\n")
	builder.WriteString("4. sequence 与段落顺序一一对应，一段一条。\n")
	builder.WriteString("输出：{\"prompts\":[{\"sequence\":1,\"prompt\":\"\"}]}\n\n段落：\n")
	encoded, _ := json.Marshal(withMontageSequence(segments))
	builder.Write(encoded)
	return system, builder.String()
}

type sequencedMontageSegment struct {
	Sequence int `json:"sequence"`
	MontageSegment
}

func withMontageSequence(segments []MontageSegment) []sequencedMontageSegment {
	sequenced := make([]sequencedMontageSegment, len(segments))
	for index, segment := range segments {
		sequenced[index] = sequencedMontageSegment{Sequence: index + 1, MontageSegment: segment}
	}
	return sequenced
}

// ParseMontagePromptSuggestions validates the model response against the
// requested segments: every sequence exactly once, every prompt non-empty.
func ParseMontagePromptSuggestions(segments []MontageSegment, raw string) ([]MontagePrompt, error) {
	if len(segments) == 0 {
		return nil, errors.New("timed segments are required")
	}
	var payload struct {
		Prompts []struct {
			Sequence int    `json:"sequence"`
			Prompt   string `json:"prompt"`
		} `json:"prompts"`
	}
	if err := decodePlannerJSON(raw, &payload); err != nil {
		return nil, err
	}
	if len(payload.Prompts) != len(segments) {
		return nil, errors.New("prompt count must match the timed segments")
	}
	result := make([]MontagePrompt, len(segments))
	seen := make(map[int]bool, len(segments))
	for _, item := range payload.Prompts {
		if item.Sequence < 1 || item.Sequence > len(segments) || seen[item.Sequence] {
			return nil, errors.New("prompt sequence is invalid")
		}
		prompt := strings.TrimSpace(item.Prompt)
		if prompt == "" {
			return nil, errors.New("prompt is required")
		}
		seen[item.Sequence] = true
		result[item.Sequence-1] = MontagePrompt{SegmentID: segments[item.Sequence-1].ID, Prompt: prompt}
	}
	return result, nil
}
