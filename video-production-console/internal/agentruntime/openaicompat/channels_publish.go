package openaicompat

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ChannelsPublishingContract 由 v2 写手、审稿和发布字段定向修复共用。
const ChannelsPublishingContract = `【视频号发布字段要求｜channels_v2】
titles=[]。
short_titles：恰好 3 条，每条一句话，1–16 个字（按 Unicode 字符计数，含数字字母），不含任何标点、符号（包括 #）和空白。要有冲突或悬念，点名五六十岁以上观众在意的钱和家；三条分别走「人群+损失」「反常现象+悬念」「身份认同或结论前置」，不用平铺直叙。
descriptions：恰好 3 条，每条以 0–6 个汉字或字母组成的短词开头，随后紧接 3–4 个 #话题，话题之间无空格。例如「认知#楼市#认知提升#经济趋势」或「#楼市#认知提升#趋势分析」。不写长句、数据或课程信息。
topics：从 descriptions 中出现的 #话题去重汇总，恰好包含这些话题，共 3–6 个，不要求以 #财经 开头。
cover_titles：恰好 2 条封面大字，每行 1–12 个字，无标点、符号和空白；第一行抓人，第二行补足，不照抄同行标题。
cta：原样摘录正文最后一句购买动作，不另造承诺。
标题、简介、话题和封面大字均不写课名、橱窗、五块钱等课程推广话术；cta 沿用正文购买句。
所有发布字段只根据最终正文写，不增加稿件之外的数据、身份或收益承诺。`

var channelsDescriptionPattern = regexp.MustCompile(`^\p{L}{0,6}(?:#[\p{L}\p{N}]+){3,4}$`)

func channelsTitleHasSeparator(text string) bool {
	return strings.ContainsFunc(text, func(r rune) bool {
		return unicode.IsPunct(r) || unicode.IsSymbol(r) || unicode.IsSpace(r) || unicode.IsControl(r)
	})
}

func channelsPublishFieldIssues(draft remixDraft) []string {
	var issues []string
	if len(draft.Titles) != 0 {
		issues = append(issues, "titles 应为 []")
	}
	if len(draft.ShortTitles) != 3 {
		issues = append(issues, fmt.Sprintf("short_titles 有 %d 条，需要恰好 3 条", len(draft.ShortTitles)))
	}
	for _, title := range draft.ShortTitles {
		if n := utf8.RuneCountInString(title); n < 1 || n > 16 {
			issues = append(issues, fmt.Sprintf("short_titles 短标题「%s」需要 1 到 16 个字", title))
		}
		if channelsTitleHasSeparator(title) {
			issues = append(issues, fmt.Sprintf("short_titles 短标题「%s」不能含标点、符号（包括 #）或空白", title))
		}
	}
	if len(draft.Descriptions) != 3 {
		issues = append(issues, fmt.Sprintf("descriptions 有 %d 条，需要恰好 3 条", len(draft.Descriptions)))
	}
	descriptionTopics := map[string]bool{}
	for _, description := range draft.Descriptions {
		if !channelsDescriptionPattern.MatchString(description) {
			issues = append(issues, fmt.Sprintf("descriptions「%s」须为 0 到 6 个汉字或字母的短词紧接 3 到 4 个 #话题，不含空白或长句", description))
			continue
		}
		for _, topic := range strings.Split(description, "#")[1:] {
			descriptionTopics["#"+topic] = true
		}
	}
	if len(draft.Topics) < 3 || len(draft.Topics) > 6 {
		issues = append(issues, "topics 需要 3 到 6 个从 descriptions 去重汇总的 #话题")
	}
	seen := map[string]bool{}
	for _, topic := range draft.Topics {
		if !descriptionTopics[topic] || seen[topic] {
			issues = append(issues, fmt.Sprintf("topics 话题「%s」未出现在 descriptions 中或重复", topic))
		}
		seen[topic] = true
	}
	for topic := range descriptionTopics {
		if !seen[topic] {
			issues = append(issues, fmt.Sprintf("topics 缺少 descriptions 中的话题「%s」", topic))
		}
	}
	if len(draft.CoverTitles) != 2 {
		issues = append(issues, "cover_titles 需要恰好 2 条")
	}
	for _, title := range draft.CoverTitles {
		if n := utf8.RuneCountInString(title); n < 1 || n > 12 || channelsTitleHasSeparator(title) {
			issues = append(issues, fmt.Sprintf("cover_titles「%s」需要 1 到 12 个字，不能含标点、符号或空白", title))
		}
	}
	// CTA 沿用正文购买句；其余对外发布文案保留财经的推广词检查。
	for _, fields := range [][]string{draft.Titles, draft.ShortTitles, draft.Descriptions, draft.Topics, draft.CoverTitles} {
		for _, field := range fields {
			if strings.Contains(field, "财富觉醒方法论") || strings.Contains(field, "橱窗") || strings.Contains(field, "五块钱") {
				return append(issues, "发布字段含课程推广话术")
			}
		}
	}
	return issues
}
