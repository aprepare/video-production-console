package imageproject

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	MaxImages          = 18
	minImages          = 1
	RoleCover          = "cover"
	RoleContent        = "content"
	maxPlannerBodySize = 1 << 20
)

type Segment struct {
	Sequence   int    `json:"sequence"`
	Role       string `json:"role"`
	Title      string `json:"title"`
	SourceText string `json:"source_text"`
	Rationale  string `json:"rationale,omitempty"`
}

type ChatRequest struct {
	Model  string
	System string
	User   string
}

type ChatClient interface {
	Complete(context.Context, ChatRequest) (string, error)
}

func ParseSegmentSuggestions(script, raw string) ([]Segment, error) {
	if strings.TrimSpace(script) == "" {
		return nil, errors.New("script is required")
	}
	var payload struct {
		Segments []Segment `json:"segments"`
	}
	if err := decodePlannerJSON(raw, &payload); err != nil {
		return nil, err
	}
	if len(payload.Segments) < minImages || len(payload.Segments) > MaxImages {
		return nil, fmt.Errorf("segment count must be between %d and %d", minImages, MaxImages)
	}
	used := make(map[int]bool, len(payload.Segments))
	normalized := make([]Segment, len(payload.Segments))
	for _, item := range payload.Segments {
		if item.Sequence < 1 || item.Sequence > len(payload.Segments) || used[item.Sequence] {
			return nil, errors.New("segment sequence is invalid")
		}
		used[item.Sequence] = true
		item.Title = strings.TrimSpace(item.Title)
		item.Rationale = strings.TrimSpace(item.Rationale)
		item.Role = strings.TrimSpace(item.Role)
		if item.Title == "" || strings.TrimSpace(item.SourceText) == "" {
			return nil, errors.New("segment title and source text are required")
		}
		if utf8.RuneCountInString(item.Title) > 40 {
			return nil, errors.New("segment title is too long")
		}
		if item.Sequence == 1 {
			if item.Role == "" {
				item.Role = RoleCover
			}
			if item.Role != RoleCover {
				return nil, errors.New("first segment must be the cover")
			}
		} else if item.Role == "" {
			item.Role = RoleContent
		} else if item.Role != RoleContent {
			return nil, errors.New("only the first segment can be the cover")
		}
		normalized[item.Sequence-1] = item
	}
	var rebuilt strings.Builder
	for _, item := range normalized {
		if !strings.Contains(script, item.SourceText) {
			return nil, errors.New("segment rewrote the original script")
		}
		rebuilt.WriteString(item.SourceText)
	}
	if rebuilt.String() != script {
		return nil, errors.New("segments must cover the original script in order")
	}
	return normalized, nil
}

func ParsePromptSuggestions(segments []Segment, raw string) ([]string, error) {
	if len(segments) == 0 {
		return nil, errors.New("confirmed segments are required")
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
		return nil, errors.New("prompt count must match confirmed segments")
	}
	result := make([]string, len(segments))
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
		result[item.Sequence-1] = prompt
	}
	for _, prompt := range result {
		if prompt == "" {
			return nil, errors.New("prompt is required")
		}
	}
	return result, nil
}

func SuggestSegments(ctx context.Context, client ChatClient, model, script string, preferredCount int) ([]Segment, error) {
	if client == nil {
		return nil, errors.New("text model is not configured")
	}
	system, user := SegmentPlanPrompt(script, preferredCount)
	raw, err := client.Complete(ctx, ChatRequest{Model: model, System: system, User: user})
	if err != nil {
		return nil, err
	}
	return ParseSegmentSuggestions(script, raw)
}

func SuggestPrompts(ctx context.Context, client ChatClient, model string, segments []Segment, ratio, style, customStyle string) ([]string, error) {
	if client == nil {
		return nil, errors.New("text model is not configured")
	}
	system, user := PromptPlanPrompt(segments, ratio, style, customStyle)
	raw, err := client.Complete(ctx, ChatRequest{Model: model, System: system, User: user})
	if err != nil {
		return nil, err
	}
	return ParsePromptSuggestions(segments, raw)
}

func SegmentPlanPrompt(script string, preferredCount int) (system, user string) {
	count := preferredCount
	if count < minImages || count > MaxImages {
		count = 0
	}
	system = "你是财经图文视频的分镜编辑。只根据用户给的最终口播原文切段，禁止改写、扩写、删句或发明事实。第一张必须是封面，总共最多18张。只输出 JSON。"
	builder := strings.Builder{}
	builder.WriteString("把下面最终口播原文切成图文卡片。规则：\n")
	builder.WriteString("1. 第一张 role 必须是 cover，要能第一眼抓住 45-65 岁观众；其余 role 为 content。\n")
	builder.WriteString("2. 总共 1 到 18 张，含封面。\n")
	builder.WriteString("3. 每段 source_text 必须是原文连续摘录，所有 source_text 按顺序拼接后必须与原文完全一致，包括换行和空格。\n")
	builder.WriteString("4. title 不超过 12 个字，rationale 用一句话说明为什么这样切。\n")
	if count > 0 {
		fmt.Fprintf(&builder, "5. 尽量切成 %d 张，只有原文实在不够信息量时才能减少。\n", count)
	} else {
		builder.WriteString("5. 按语义自然切分，宁少勿碎。\n")
	}
	builder.WriteString("输出：{\"segments\":[{\"sequence\":1,\"role\":\"cover\",\"title\":\"\",\"source_text\":\"\",\"rationale\":\"\"}]}\n\n原文：\n")
	builder.WriteString(script)
	return system, builder.String()
}

func PromptPlanPrompt(segments []Segment, ratio, style, customStyle string) (system, user string) {
	system = "你是财经图文视频的画面提示词编辑。按已确认段落写生图提示词，不要改写对应原文，不要编造数字、日期、收益或可读中文。只输出 JSON。"
	styleText := styleInstructions[style]
	if style == "custom" || styleText == "" {
		styleText = strings.TrimSpace(customStyle)
	}
	if styleText == "" {
		styleText = styleInstructions["finance_documentary"]
	}
	var builder strings.Builder
	builder.WriteString("为每张已确认卡片写生图提示词。规则：\n")
	fmt.Fprintf(&builder, "1. 画幅 %s，风格：%s。\n", ratio, styleText)
	builder.WriteString("2. sequence=1 是封面，必须更强冲突、主体更大、第一眼能看懂风险或反差。\n")
	builder.WriteString("3. 优先中国家庭、银行、住房、养老、子女与商业场景；禁止可读文字、水印、品牌、伪文字、表格数字。\n")
	builder.WriteString("输出：{\"prompts\":[{\"sequence\":1,\"prompt\":\"\"}]}\n\n卡片：\n")
	encoded, _ := json.Marshal(segments)
	builder.Write(encoded)
	return system, builder.String()
}

func decodePlannerJSON(raw string, dest any) error {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > maxPlannerBodySize {
		return errors.New("planner response is empty or too large")
	}
	if idx := strings.Index(raw, "```"); idx >= 0 {
		raw = raw[idx+3:]
		raw = strings.TrimPrefix(raw, "json")
		if end := strings.Index(raw, "```"); end >= 0 {
			raw = raw[:end]
		}
		raw = strings.TrimSpace(raw)
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(dest); err != nil {
		return errors.New("planner response is not valid JSON")
	}
	return nil
}
