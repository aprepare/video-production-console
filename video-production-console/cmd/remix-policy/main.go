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
		"facts_inject_rule":     openaicompat.FactsInjectRule,
		"ammo_inject_rule":      openaicompat.AmmoInjectRule,
		"planner_user_template": openaicompat.PlannerUserTemplate,
		"planner_inject_title":  openaicompat.PlannerInjectTitle,
		"planner_inject_rule":   openaicompat.PlannerInjectRule,
	}); err != nil {
		panic(err)
	}
}
