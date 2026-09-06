package openaicompat

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// 发布字段补齐：写手偶尔把 short_titles / descriptions / topics 给少、给空、
// 给超格。以前靠通用兜底文案顶上，发出去就是「窗口不会等人」这类废话；现在
// 改成一轮定向返工——正文原样保留，只让模型把发布字段写齐写对。

// publishFieldIssues 列出发布字段不合格的地方；空切片表示合格。
func publishFieldIssues(draft remixDraft) []string {
	issues := make([]string, 0, 4)
	if len(nonBlank(draft.Titles)) > 0 {
		issues = append(issues, "titles 应为 []")
	}
	shorts := nonBlank(draft.ShortTitles)
	if len(shorts) < 3 {
		issues = append(issues, fmt.Sprintf("short_titles 只有 %d 条，需要恰好 3 条", len(shorts)))
	} else if len(shorts) > 3 {
		issues = append(issues, fmt.Sprintf("short_titles 有 %d 条，需要恰好 3 条", len(shorts)))
	}
	for _, s := range shorts {
		if strings.Contains(s, "#") {
			issues = append(issues, "short_titles 不带 #")
		}
		if utf8.RuneCountInString(s) < 6 {
			issues = append(issues, fmt.Sprintf("短标题「%s」不足 6 个字，short_titles 每条需 6 到 15 个字", s))
		} else if utf8.RuneCountInString(s) > 15 {
			issues = append(issues, fmt.Sprintf("短标题「%s」超过 15 个字", s))
		}
	}
	descs := nonBlank(draft.Descriptions)
	if len(descs) < 2 {
		issues = append(issues, fmt.Sprintf("descriptions 只有 %d 条，需要 2 到 3 条", len(descs)))
	} else if len(descs) > 3 {
		issues = append(issues, fmt.Sprintf("descriptions 有 %d 条，需要 2 到 3 条", len(descs)))
	}
	for _, d := range descs {
		body := strings.TrimSpace(trailingHashtagRun.ReplaceAllString(d, ""))
		if strings.Contains(d, "#") {
			issues = append(issues, "descriptions 不带 #")
		}
		if utf8.RuneCountInString(body) > 40 {
			issues = append(issues, fmt.Sprintf("描述「%s…」超过 40 个字，压成一句", clipRunes(body, 0, 18)))
		}
	}
	topics := nonBlank(draft.Topics)
	if len(topics) > 0 && topics[0] != "#财经" && topics[0] != "#经济" && topics[0] != "#理财" {
		issues = append(issues, "topics 首个应为 #财经/#经济/#理财")
	}
	if len(topics) < 3 {
		issues = append(issues, fmt.Sprintf("topics 只有 %d 个，需要 3 到 4 个带#的话题", len(topics)))
	} else if len(topics) > 4 {
		issues = append(issues, fmt.Sprintf("topics 有 %d 个，需要 3 到 4 个带#的话题", len(topics)))
	}
	for _, t := range topics {
		if !strings.HasPrefix(t, "#") {
			issues = append(issues, fmt.Sprintf("topics 话题「%s」缺少 # 前缀，必须带#", t))
		}
	}
	for _, s := range append(append(append([]string{}, shorts...), descs...), topics...) {
		if strings.Contains(s, "财富觉醒方法论") || strings.Contains(s, "橱窗") || strings.Contains(s, "五块钱") {
			issues = append(issues, "发布字段含课程推广话术")
			break
		}
	}
	return issues
}

func nonBlank(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item) != "" {
			out = append(out, strings.TrimSpace(item))
		}
	}
	return out
}

func buildPublishFieldsRepairPrompt(issues []string) string {
	var b strings.Builder
	b.WriteString("发布字段不合格，只补发布字段，continuous_script 一个字不动，按上一条回复相同的 JSON 结构返回完整结果：\n")
	for i, issue := range issues {
		b.WriteString(fmt.Sprintf("%d. %s\n", i+1, issue))
	}
	b.WriteString("要求：short_titles 恰好 3 条、每条 6 到 15 个字、三条用三种不同钩子（数字或日期砸脸 / 反常识或反问 / 人群圈定或结果），第 1 条能当画面主标题；" +
		"descriptions 2 到 3 条、每条一句话不超过 40 个字、各带一个具体钩子（日期、数字、反问、对号入座）、不写#话题；" +
		"topics 3 到 4 个：第 1 个从 #财经 #经济 #理财 里选，其余必须是正文里真正出现过的名词（如 #数据资产 #存款利率 #楼市），禁止 #认知 #思维认知 #干货分享 #认知觉醒 #宏观趋势 这类空泛词；" +
		"修辞性小数字用汉字（第六次、十个里八个、五块钱），年份日期金额用阿拉伯数字；发布字段不写课名、橱窗、价格。\n")
	return b.String()
}

// repairPublishFields 发布字段不合格时打一轮定向返工；返工失败或没改好就原样返回。
func repairPublishFields(client ChatClient, model, effort, system, user, content, outputDir string) (string, string) {
	draft, err := parseRemixDraft(content)
	if err != nil {
		return content, ""
	}
	issues := publishFieldIssues(draft)
	if len(issues) == 0 {
		return content, ""
	}
	appendRemixRunLog(outputDir, map[string]any{"event": "publish_fields", "issues": issues})
	resp, chatErr := client.Chat(ChatRequest{
		Model:           strings.TrimSpace(model),
		ReasoningEffort: effort,
		Stream:          true,
		Messages: []Message{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
			{Role: "assistant", Content: content},
			{Role: "user", Content: buildPublishFieldsRepairPrompt(issues)},
		},
	})
	if chatErr != nil || len(resp.Choices) == 0 {
		return content, "发布字段返工请求失败，按现有字段交付。"
	}
	retry := resp.Choices[0].Message.Content
	retryDraft, parseErr := parseRemixDraft(retry)
	if parseErr != nil || strings.TrimSpace(retryDraft.ContinuousScript) == "" {
		return content, "发布字段返工结果无法解析，按现有字段交付。"
	}
	// 正文必须原样：模型顺手改了正文就不采用（会破坏自检结论）。
	if strings.TrimSpace(retryDraft.ContinuousScript) != strings.TrimSpace(draft.ContinuousScript) {
		retryDraft.ContinuousScript = draft.ContinuousScript
	}
	after := publishFieldIssues(retryDraft)
	if len(after) >= len(issues) {
		return content, "发布字段返工未见改善，按现有字段交付。"
	}
	appendRemixRunLog(outputDir, map[string]any{"event": "publish_fields_after", "issues": after})
	merged := replaceDraftFields(content, retryDraft)
	note := "发布字段已定向返工。"
	if len(after) > 0 {
		note += "仍有：" + strings.Join(after, "；") + "。"
	}
	return merged, note
}

// replaceDraftFields 把返工后的发布字段写回原 JSON，正文字段原样不动；
// 原文不是合法 JSON 时按现有字段重新组装。
func replaceDraftFields(content string, next remixDraft) string {
	text := strings.TrimSpace(stripCodeFence(content))
	var obj map[string]json.RawMessage
	if !strings.HasPrefix(text, "{") || json.Unmarshal([]byte(text), &obj) != nil {
		draft, err := parseRemixDraft(content)
		if err != nil {
			return content
		}
		obj = map[string]json.RawMessage{}
		obj["continuous_script"], _ = json.Marshal(draft.ContinuousScript)
	}
	set := func(key string, value any) {
		if raw, err := json.Marshal(value); err == nil {
			obj[key] = raw
		}
	}
	set("titles", next.Titles)
	set("short_titles", next.ShortTitles)
	set("descriptions", next.Descriptions)
	set("topics", next.Topics)
	set("cta", next.CTA)
	out, err := json.Marshal(obj)
	if err != nil {
		return content
	}
	return string(out)
}
