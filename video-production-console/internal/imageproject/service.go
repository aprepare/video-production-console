package imageproject

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

type PromptInput struct {
	SourceText  string
	Ratio       string
	Style       string
	CustomStyle string
}

var styleInstructions = map[string]string{
	"finance_documentary":  "财经纪实插画，真实中国家庭与商业场景，构图稳定，主体明确",
	"red_ink":              "赤墨风，复古中国财经报纸木刻，米白旧纸、黑墨、暗红强调",
	"old_newspaper":        "旧报档案风，泛黄剪报、档案证据、暗红圈注",
	"ledger_investigation": "账本调查风，家庭账本、收支证据、现金流关系，清晰可读的视觉层级",
	"dark_crisis":          "暗黑危机风，低饱和工业金融寓言，克制的暗红风险提示",
	"city_era":             "城市时代感，中国城市、产业、就业与AI变化，纪实电影感",
	"blackboard":           "黑板讲解风，粉笔关系图、因果箭头、步骤清晰",
}

func SplitScript(script string, count int) ([]string, error) {
	if strings.TrimSpace(script) == "" {
		return nil, errors.New("script is required")
	}
	if count < 1 || count > 60 {
		return nil, errors.New("image count must be between 1 and 60")
	}
	if count > utf8.RuneCountInString(script) {
		return nil, errors.New("image count exceeds available script characters")
	}
	units := splitLongest(semanticUnits(script), count)
	if count > len(units) {
		return nil, errors.New("script cannot be split into requested image count")
	}
	return mergeAdjacentUnits(units, count), nil
}

func semanticUnits(script string) []string {
	runes := []rune(script)
	units := make([]string, 0)
	start := 0
	for index, value := range runes {
		if !strings.ContainsRune("。！？!?；;", value) {
			continue
		}
		end := index + 1
		for end < len(runes) && unicode.IsSpace(runes[end]) {
			end++
		}
		units = append(units, string(runes[start:end]))
		start = end
	}
	if start < len(runes) {
		units = append(units, string(runes[start:]))
	}
	if len(units) == 0 {
		return []string{script}
	}
	return units
}

func splitLongest(parts []string, target int) []string {
	for len(parts) < target {
		index := -1
		longest := 0
		for i, part := range parts {
			if n := utf8.RuneCountInString(part); n > longest && n >= 2 {
				index, longest = i, n
			}
		}
		if index < 0 {
			break
		}
		runes := []rune(parts[index])
		cut := len(runes) / 2
		left := string(runes[:cut])
		right := string(runes[cut:])
		if left == "" || right == "" {
			break
		}
		next := append([]string{}, parts[:index]...)
		next = append(next, left, right)
		next = append(next, parts[index+1:]...)
		parts = next
	}
	return parts
}

func mergeAdjacentUnits(units []string, count int) []string {
	if len(units) == count {
		return units
	}
	lengths := make([]int, len(units))
	total := 0
	for i, unit := range units {
		lengths[i] = utf8.RuneCountInString(unit)
		total += lengths[i]
	}

	result := make([]string, 0, count)
	start := 0
	consumed := 0
	for group := 1; group < count; group++ {
		remainingGroups := count - group
		maxEnd := len(units) - remainingGroups
		target := total * group
		end := start + 1
		prefix := consumed + lengths[start]
		bestDistance := absolute(prefix*count - target)
		for candidate := start + 2; candidate <= maxEnd; candidate++ {
			prefix += lengths[candidate-1]
			distance := absolute(prefix*count - target)
			if distance >= bestDistance {
				break
			}
			end = candidate
			bestDistance = distance
		}
		result = append(result, strings.Join(units[start:end], ""))
		for _, length := range lengths[start:end] {
			consumed += length
		}
		start = end
	}
	result = append(result, strings.Join(units[start:], ""))
	return result
}

func absolute(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func BuildPrompt(input PromptInput) string {
	style := styleInstructions[input.Style]
	if input.Style == "custom" || style == "" {
		style = strings.TrimSpace(input.CustomStyle)
	}
	if style == "" {
		style = styleInstructions["finance_documentary"]
	}
	return fmt.Sprintf("为中国45—65岁观众制作财经认知视频内容图。画幅比例 %s。画面内容严格对应：%s。视觉风格：%s。优先中国人物、家庭、银行、住房、养老或商业语境；主体明确，中高信息密度，节奏稳定，不花哨。不要生成可读文字、标题、日期、年份、收益、比例、品牌标志、水印或伪文字。", input.Ratio, strings.TrimSpace(input.SourceText), style)
}
