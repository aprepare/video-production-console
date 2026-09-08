package remixlab

import (
	"strings"
	"testing"

	"video-production-console/internal/agentruntime/openaicompat"
)

// 默认图：原文 → 二创策划(hook) → 写手 → 审稿 → 定稿。策划是唯一的前置 agent，
// 不是参考稿角色；ID 固定 hook 以便引擎把计划交给审稿。
func TestDefaultWorkflowHasOnePlannerBeforeWriter(t *testing.T) {
	wf := DefaultWorkflow(AgentPrompts{})
	if err := ValidateWorkflow(wf); err != nil {
		t.Fatal(err)
	}
	agents := 0
	for _, n := range wf.Nodes {
		if n.Type != WorkflowNodeAgent {
			continue
		}
		agents++
		if n.ID != openaicompat.PlannerNodeID || n.Title != openaicompat.PlannerNodeTitle || n.Config.Role != "" ||
			!strings.Contains(n.Config.SystemPrompt, "core_question") || !strings.Contains(n.Config.SystemPrompt, "月度收支计划表") ||
			n.Config.InjectTitle != openaicompat.PlannerInjectTitle || n.Config.UserTemplate != openaicompat.PlannerUserTemplate {
			t.Fatalf("unexpected default prewriter node: %s/%s", n.ID, n.Title)
		}
	}
	if agents != 1 {
		t.Fatalf("want one default planner, got %d agents", agents)
	}
	hasPlannerEdge, hasSourceEdge := false, false
	for _, e := range wf.Edges {
		if e == [2]string{openaicompat.PlannerNodeID, "writer"} {
			hasPlannerEdge = true
		}
		if e == [2]string{"source", "writer"} {
			hasSourceEdge = true
		}
	}
	if !hasPlannerEdge || !hasSourceEdge {
		t.Fatalf("planner and source must both feed the writer: %v", wf.Edges)
	}
	if review := workflowReviewer(wf); review == nil || !strings.Contains(review.Config.UserTemplate, "{{writing_plan}}") {
		t.Fatal("default reviewer must receive the writing plan")
	}
}
