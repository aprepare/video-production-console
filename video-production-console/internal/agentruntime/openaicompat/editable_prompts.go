package openaicompat

import "strings"

// Formatting contracts are kept separate from editable editorial requirements.
const WriterJSONContract = `【系统输出格式：写手】
只返回一个合法 JSON 对象，不加 Markdown 围栏或解释。结构：
{"continuous_script":"完整口播正文","titles":[],"short_titles":[],"descriptions":[],"topics":[],"cta":""}
continuous_script、cta 是字符串；其他列出的字段是字符串数组。正文的实际写法和发布字段内容遵循上方用户编审要求。
字符串中的英文双引号必须写成 \"，换行写成 \n；引用句子优先用中文引号。不要在 JSON 之前输出 Let、思考过程或其他文字。`

const ReviewerJSONContract = `【系统输出格式：审稿；本格式优先于其他输出形状要求】
只返回一个合法 JSON 对象，不加 Markdown 围栏或解释。无修改时：
{"verdict":"pass","summary":"审稿结论","issues":[]}
确有修改时：
{"verdict":"fixed","summary":"审稿结论","issues":[{"where":"具体位置","problem":"修改依据","fix":"实际改动"}],"revised":{"continuous_script":"完整修订正文","titles":[],"short_titles":[],"descriptions":[],"topics":[],"cta":""}}
verdict 只能为 pass 或 fixed。fixed 必须含完整 revised 正文及至少一条具体修改依据，不得省略正文。revised 中未改字段原样带回。无法核实的事项在 summary 中如实说明，不把未知判为已核实。
字符串内的英文双引号必须写成 \"，换行写成 \n；引用句子优先用中文引号。不要在 JSON 之前输出 Let、思考过程或其他文字。`

const ReviewerUserTemplate = `按共同规则终审下面的稿子。

【操作员批注】
{{annotations}}

【本轮二创策划（仅作参考，无则忽略）】
{{writing_plan}}

【事实核查结论（看状态与来源，不把未核查视为核准）】
{{facts}}

【同行原文】
{{source}}

【待审成稿（写手 JSON）】
{{draft}}`

// Only remove exact built-in copies, never arbitrary user prose.
func SeparateEditorialPrompt(prompt string) string {
	for _, bundled := range []string{SharedEditorialPolicy, EditorialWritingRules, PublishingContract, WriterJSONContract, ReviewerJSONContract} {
		prompt = strings.ReplaceAll(prompt, bundled, "")
	}
	prompt = strings.ReplaceAll(prompt, `只返回 JSON：{verdict:"pass"或"fixed",summary,issues:[{where,problem,fix}],revised:{continuous_script,titles,short_titles,descriptions,topics,cta}}。fixed 时返回完整对象，未改字段原样带回。`, "")
	return strings.TrimSpace(prompt)
}

func reviewUserFromTemplate(template, source, draft, annotations, facts, plan string) string {
	if strings.TrimSpace(template) == "" {
		template = ReviewerUserTemplate
	}
	// A single replacement pass prevents placeholders inside source material from expanding.
	result := strings.NewReplacer("{{source}}", source, "{{draft}}", draft, "{{annotations}}", annotations, "{{facts}}", facts, "{{writing_plan}}", plan).Replace(template)
	// Core evidence is always supplied even if the operator removes a placeholder.
	for _, item := range []struct{ key, label, value string }{{"{{source}}", "同行原文", source}, {"{{draft}}", "待审成稿", draft}, {"{{annotations}}", "操作员批注", annotations}, {"{{facts}}", "事实核查结论", facts}} {
		if !strings.Contains(template, item.key) && strings.TrimSpace(item.value) != "" {
			result += "\n\n【" + item.label + "】\n" + item.value
		}
	}
	return result
}
