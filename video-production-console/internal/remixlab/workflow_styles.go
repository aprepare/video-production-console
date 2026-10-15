package remixlab

import (
	"fmt"
	"strings"

	"video-production-console/internal/agentruntime/openaicompat"
)

const styleWriterUserTemplate = "{{NOTES}}\n{{BRIEF}}\n# 同行原文\n{{SOURCE}}"

func (w Workflow) FinanceMode() bool {
	return w.Style == "" || w.Style == "finance_course" || w.Finance
}

func universalReviewerRole() string {
	for _, style := range BuiltinStyles() {
		if style.ID == "universal_remix" {
			return style.Reviewer
		}
	}
	return "按内容简报与原文事实审稿。"
}

// DefaultUniversalWorkflow 用于新账号，不吸收历史财经 agent 覆盖。
func DefaultUniversalWorkflow() Workflow {
	w := DefaultWorkflow(AgentPrompts{})
	for _, style := range BuiltinStyles() {
		if style.ID == "universal_remix" {
			w, _ = workflowWithStyle(w, style, nil)
			break
		}
	}
	return w
}

func workflowWithStyle(w Workflow, style StyleTemplate, brief *Brief) (Workflow, error) {
	w.Nodes = append([]WorkflowNode(nil), w.Nodes...)
	w.Edges = append([][2]string(nil), w.Edges...)
	w.Style = style.ID
	w.StyleName = style.Name
	w.Publish = style.Publish
	w.Finance = style.Finance && style.ID != "finance_course"
	w.Brief = brief
	rules := style.Rules
	if style.Builtin && !style.Finance {
		// 内置通用风格沿用配套规则；自定义风格只应用用户明确保存的规则。
		rules = UniversalRules + "\n\n" + UniversalPublishingContract
	}
	w.EditorialRules = &rules
	// 财经策划的注入纪律提到祝福和课尾五步；通用风格用自己的那一份。
	plannerUser, injectRule := openaicompat.PlannerUserTemplate, openaicompat.PlannerInjectRule
	if !style.Finance {
		plannerUser, injectRule = universalPlannerUser, universalPlannerInjectRule
	}
	var sourceID, writerID string
	hasHook := false
	nodes := make([]WorkflowNode, 0, len(w.Nodes)+1)
	for _, node := range w.Nodes {
		switch node.Type {
		case WorkflowNodeInput:
			sourceID = node.ID
		case WorkflowNodeWriter:
			writerID = node.ID
			node.Config.SystemPrompt = style.Writer
			node.Config.UserTemplate = style.WriterUser
			node.Config.PromptID = ""
			node.Config.FrozenPromptID = ""
		case WorkflowNodeReviewer:
			if strings.TrimSpace(style.Reviewer) != "" {
				node.Config.SystemPrompt = style.Reviewer
			}
		}
		if node.ID == openaicompat.PlannerNodeID {
			if node.Type != WorkflowNodeAgent {
				return Workflow{}, fmt.Errorf("%w: hook 必须是策划 agent 节点", ErrInvalidWorkflow)
			}
			if strings.TrimSpace(style.Planner) == "" {
				continue
			}
			hasHook = true
			node.Config.Role = ""
			node.Config.SystemPrompt = style.Planner
			node.Config.UserTemplate = plannerUser
			node.Config.InjectTitle = openaicompat.PlannerInjectTitle
			node.Config.InjectRule = injectRule
		}
		nodes = append(nodes, node)
	}
	w.Nodes = nodes
	if strings.TrimSpace(style.Planner) == "" {
		edges := make([][2]string, 0, len(w.Edges))
		for _, edge := range w.Edges {
			if edge[0] != openaicompat.PlannerNodeID && edge[1] != openaicompat.PlannerNodeID {
				edges = append(edges, edge)
			}
		}
		w.Edges = edges
	} else {
		if !hasHook {
			w.Nodes = append(w.Nodes, WorkflowNode{ID: openaicompat.PlannerNodeID, Type: WorkflowNodeAgent, Title: openaicompat.PlannerNodeTitle, X: 320, Y: 190, Config: WorkflowNodeConfig{
				ReasoningEffort: "medium", SystemPrompt: style.Planner, UserTemplate: plannerUser,
				InjectTitle: openaicompat.PlannerInjectTitle, InjectRule: injectRule,
			}})
		}
		for _, edge := range [][2]string{{sourceID, openaicompat.PlannerNodeID}, {openaicompat.PlannerNodeID, writerID}} {
			found := false
			for _, existing := range w.Edges {
				found = found || edge == existing
			}
			if !found {
				w.Edges = append(w.Edges, edge)
			}
		}
	}
	return w, nil
}

func (s Store) ApplyStyleForAccount(accountID, styleID string, brief *Brief) (WorkflowPromptsView, error) {
	style, ok, err := s.GetStyle(styleID)
	if err != nil {
		return WorkflowPromptsView{}, err
	}
	if !ok {
		return WorkflowPromptsView{}, fmt.Errorf("%w: %s", ErrStyleNotFound, styleID)
	}
	w, err := s.LoadWorkflowForAccount(accountID)
	if err != nil {
		return WorkflowPromptsView{}, err
	}
	w, err = workflowWithStyle(w, style, brief)
	if err != nil {
		return WorkflowPromptsView{}, err
	}
	if err := s.SaveWorkflowForAccount(accountID, w); err != nil {
		return WorkflowPromptsView{}, err
	}
	return s.WorkflowPromptsForAccount(accountID)
}

func (s Store) UpdateBriefForAccount(accountID string, brief Brief) (WorkflowPromptsView, error) {
	w, err := s.LoadWorkflowForAccount(accountID)
	if err != nil {
		return WorkflowPromptsView{}, err
	}
	w.Brief = &brief
	if err := s.SaveWorkflowForAccount(accountID, w); err != nil {
		return WorkflowPromptsView{}, err
	}
	return s.WorkflowPromptsForAccount(accountID)
}
