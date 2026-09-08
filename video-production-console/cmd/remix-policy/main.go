// remix-policy exports the compiled prompt authority for review and config sync.
package main

import (
	"encoding/json"
	"os"
	"video-production-console/internal/agentruntime/openaicompat"
)

func main() {
	hook, facts, offline, ammo := openaicompat.DefaultIntelAgentPrompts()
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(map[string]any{
		"remove_mechanical_review": true,
		"version":                  openaicompat.EditorialPolicyVersion,
		"reference_system":         openaicompat.ReferenceSystemPrompt(),
		"reference_user_template":  openaicompat.ReferenceUserTemplate,
		"reference_inject_rule":    openaicompat.ReferenceInjectRule,
		"writer_system":            openaicompat.BoneFleshSystemPrompt(), "writer_user": openaicompat.BoneFleshUserPrompt,
		"reviewer": openaicompat.DefaultReviewerPrompt(), "hook": hook, "facts": facts, "facts_offline": offline, "ammo": ammo,
		// 工作流文件把公共底线放在 editorial_rules，节点只存角色提示；下面四项供数据同步脚本直接写入节点。
		"editorial_rules":        openaicompat.SharedEditorialPolicy,
		"writer_role":            openaicompat.SeparateEditorialPrompt(openaicompat.BoneFleshSystemPrompt()),
		"reviewer_role":          openaicompat.SeparateEditorialPrompt(openaicompat.DefaultReviewerPrompt()),
		"reviewer_user_template": openaicompat.ReviewerUserTemplate,
		"planner_system":         openaicompat.PlannerSystemPrompt,
		"facts_inject_rule":     openaicompat.FactsInjectRule,
		"ammo_inject_rule":      openaicompat.AmmoInjectRule,
		"planner_user_template": openaicompat.PlannerUserTemplate,
		"planner_inject_title":  openaicompat.PlannerInjectTitle,
		"planner_inject_rule":   openaicompat.PlannerInjectRule,
	}); err != nil {
		panic(err)
	}
}
