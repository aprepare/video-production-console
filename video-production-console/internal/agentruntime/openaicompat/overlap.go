package openaicompat

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// 交付前自检：新稿与同行原文的原句重合检测。
//
// 比对在「内容字」上做（去掉空白和标点），所以只改标点的照搬照样会被抓住。
// 数字必须原词保留是写作规则，但带数字的句子同样必须换说法，所以数字不打断
// 匹配；只有整段几乎全是数字时（letterCount 不足）才放过。
const (
	// overlapSeedRunes 是判定「照搬」的最短公共片段长度（内容字）。
	// 口头禅和固定表达通常在 10 字以内；12 字以上还一字不差基本就是抄。
	overlapSeedRunes = 12
	// overlapMinLetters 要求片段里至少有这么多非数字的字，纯数据串不算抄。
	overlapMinLetters = 8
	// overlapMaxFragments 限制报告条数，避免整篇照搬时刷屏。
	overlapMaxFragments = 10
	// overlapWarningRunes 是警告里展示片段的最大长度。
	overlapWarningRunes = 40
)

// overlapAllowedPhrases 是允许在每篇稿子里原样出现的固定要素：课程名、入口
// 和上车话术。比对前把它们打断，避免把「必须重复」判成「照搬」。
var overlapAllowedPhrases = []string{
	"财富觉醒方法论",
	"主页橱窗",
	"推广期间只要五块钱",
	"推广期间只要5块钱",
	"现在上车还来得及",
}

// contentRunes 只保留会被观众听到的字：字母、数字和百分号。
func overlapContentRunes(text string, sentinel rune) []rune {
	// 固定要素替换成 sentinel；source 和 draft 用不同 sentinel，保证匹配
	// 永远无法穿过这些短语。
	for _, phrase := range overlapAllowedPhrases {
		text = strings.ReplaceAll(text, phrase, string(sentinel))
	}
	out := make([]rune, 0, utf8.RuneCountInString(text))
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '%' || r == '％' || r == sentinel {
			out = append(out, r)
		}
	}
	return out
}

// overlapFragments 返回新稿里与原文一字不差的片段（内容字 ≥ overlapSeedRunes
// 且非数字字 ≥ overlapMinLetters），按出现顺序排列。
func overlapFragments(source, draft string) []string {
	src := overlapContentRunes(source, '\x01')
	dst := overlapContentRunes(draft, '\x02')
	if len(src) < overlapSeedRunes || len(dst) < overlapSeedRunes {
		return nil
	}
	seeds := make(map[string][]int, len(src))
	for i := 0; i+overlapSeedRunes <= len(src); i++ {
		key := string(src[i : i+overlapSeedRunes])
		seeds[key] = append(seeds[key], i)
	}
	fragments := make([]string, 0, 4)
	for i := 0; i+overlapSeedRunes <= len(dst) && len(fragments) < overlapMaxFragments; {
		positions, ok := seeds[string(dst[i:i+overlapSeedRunes])]
		if !ok {
			i++
			continue
		}
		best := overlapSeedRunes
		for _, p := range positions {
			n := overlapSeedRunes
			for p+n < len(src) && i+n < len(dst) && src[p+n] == dst[i+n] {
				n++
			}
			if n > best {
				best = n
			}
		}
		fragment := dst[i : i+best]
		if overlapLetterCount(fragment) >= overlapMinLetters {
			fragments = append(fragments, string(fragment))
		}
		i += best
	}
	return fragments
}

func overlapLetterCount(runes []rune) int {
	n := 0
	for _, r := range runes {
		if unicode.IsLetter(r) {
			n++
		}
	}
	return n
}

// overlapRuneTotal 用于比较返工前后哪版抄得更少。
func overlapRuneTotal(fragments []string) int {
	total := 0
	for _, fragment := range fragments {
		total += utf8.RuneCountInString(fragment)
	}
	return total
}

// overlapWarnings 把残留重合渲染成任务警告，控制台任务结果里能直接看到。
func overlapWarnings(fragments []string) []string {
	if len(fragments) == 0 {
		return nil
	}
	warnings := make([]string, 0, len(fragments))
	for _, fragment := range fragments {
		runes := []rune(fragment)
		if len(runes) > overlapWarningRunes {
			fragment = string(runes[:overlapWarningRunes]) + "…"
		}
		warnings = append(warnings, "自检：该句与原文重合未修复「"+fragment+"」")
	}
	return warnings
}

// buildOverlapRepairPrompt 是自检返工的第二轮用户消息：把照搬片段当证据列给
// 模型，只许重说这些句子。
func buildOverlapRepairPrompt(fragments []string) string {
	var b strings.Builder
	b.WriteString("自检发现下面这些片段和同行原文一字不差（比对时已去掉标点）。只把含这些片段的句子换成新说法，句子里的数字原词保留，其余所有内容一个字都不要改，按上一条回复相同的 JSON 结构返回完整结果：\n")
	for i, fragment := range fragments {
		b.WriteString(fmt.Sprintf("%d. %s\n", i+1, fragment))
	}
	return b.String()
}

// repairSourceOverlap 在交付前做一轮自检返工：新稿照搬原文时，把证据发回同
// 一会话让模型只重写那些句子。返工失败或改得更差就保留首稿；两版残留的重合
// 都会变成任务警告。质检只在配置了质检模型时运行：留空表示关闭质检，写稿
// 模型的原始输出原样交付。第三个返回值是一句质检结论，会进入任务结果摘要，
// 让操作者看到质检用了哪个模型、发现几处、返工是否成功。
func repairSourceOverlap(client ChatClient, model, checkModel, effort, system, user, source, content string) (string, []string, string) {
	_ = model
	checkModel = strings.TrimSpace(checkModel)
	if checkModel == "" {
		return content, nil, "质检未启用（质检模型留空），交付写稿模型原始输出。"
	}
	draft, err := parseRemixDraft(content)
	if err != nil || strings.TrimSpace(draft.ContinuousScript) == "" {
		return content, nil, "质检未执行：草稿无法解析。"
	}
	fragments := overlapFragments(source, draft.ContinuousScript)
	if len(fragments) == 0 {
		return content, nil, fmt.Sprintf("质检通过（%s）：未发现与原文重合的片段。", checkModel)
	}
	resp, err := client.Chat(ChatRequest{
		Model:           checkModel,
		ReasoningEffort: effort,
		Stream:          true,
		Messages: []Message{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
			{Role: "assistant", Content: content},
			{Role: "user", Content: buildOverlapRepairPrompt(fragments)},
		},
	})
	if err != nil || len(resp.Choices) == 0 {
		return content, overlapWarnings(fragments),
			fmt.Sprintf("质检（%s）：发现 %d 处与原文重合，返工请求失败，保留首稿，见警告。", checkModel, len(fragments))
	}
	retry := resp.Choices[0].Message.Content
	retryDraft, err := parseRemixDraft(retry)
	if err != nil || utf8.RuneCountInString(strings.TrimSpace(retryDraft.ContinuousScript)) < 40 {
		return content, overlapWarnings(fragments),
			fmt.Sprintf("质检（%s）：发现 %d 处与原文重合，返工结果无效，保留首稿，见警告。", checkModel, len(fragments))
	}
	retryFragments := overlapFragments(source, retryDraft.ContinuousScript)
	if overlapRuneTotal(retryFragments) < overlapRuneTotal(fragments) {
		if len(retryFragments) == 0 {
			return retry, nil,
				fmt.Sprintf("质检（%s）：发现 %d 处与原文重合，已全部改写。", checkModel, len(fragments))
		}
		return retry, overlapWarnings(retryFragments),
			fmt.Sprintf("质检（%s）：发现 %d 处与原文重合，改写后剩余 %d 处，见警告。", checkModel, len(fragments), len(retryFragments))
	}
	return content, overlapWarnings(fragments),
		fmt.Sprintf("质检（%s）：发现 %d 处与原文重合，返工未见改善，保留首稿，见警告。", checkModel, len(fragments))
}
