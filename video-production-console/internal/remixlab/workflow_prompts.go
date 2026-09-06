package remixlab

import (
	"fmt"
	"strings"
	"unicode/utf8"
	"video-production-console/internal/agentruntime/openaicompat"
)

type WorkflowPromptsView struct {
	Workflow         Workflow `json:"workflow"`
	WriterContract   string   `json:"writer_contract"`
	ReviewerContract string   `json:"reviewer_contract"`
}

// Resolve the same writer selection for editing and for new run snapshots.
func (s Store) resolveWorkflowWriter(w Workflow) (PromptTemplate, error) {
	id := ""
	if node := workflowWriter(w); node != nil {
		id = strings.TrimSpace(node.Config.PromptID)
	}
	active, hasActive, err := s.GetActive()
	if err != nil {
		return PromptTemplate{}, err
	}
	if hasActive && (id == "" || id == active.ID) {
		return PromptTemplate{ID: active.ID, Name: active.Name, Stamp: active.Stamp, Style: active.Style, System: active.System, User: active.User}, nil
	}
	if id == "" {
		id = "elder_stable"
	}
	prompt, found, err := s.GetPrompt(id)
	if err != nil {
		return PromptTemplate{}, err
	}
	if !found {
		prompt = ResolvePrompt("elder_stable", "", "")
	}
	return prompt, nil
}

func (s Store) freezeWorkflowWriter(w Workflow) (Workflow, error) {
	prompt, err := s.resolveWorkflowWriter(w)
	if err != nil {
		return w, err
	}
	w.Nodes = append([]WorkflowNode(nil), w.Nodes...)
	for i := range w.Nodes {
		if w.Nodes[i].Type == WorkflowNodeWriter {
			if strings.TrimSpace(w.Nodes[i].Config.SystemPrompt) == "" {
				w.Nodes[i].Config.SystemPrompt = prompt.System
			}
			if strings.TrimSpace(w.Nodes[i].Config.UserTemplate) == "" {
				w.Nodes[i].Config.UserTemplate = prompt.User
			}
		}
	}
	return w, nil
}

func (s Store) WorkflowPromptsForAccount(accountID string) (WorkflowPromptsView, error) {
	w, err := s.LoadWorkflowForAccount(accountID)
	if err != nil {
		return WorkflowPromptsView{}, err
	}
	w, err = s.freezeWorkflowWriter(w)
	if err != nil {
		return WorkflowPromptsView{}, err
	}
	separate := w.EditorialRules == nil
	if separate {
		rules := openaicompat.SharedEditorialPolicy
		w.EditorialRules = &rules
	}
	overrides, err := s.LoadAgentPrompts()
	if err != nil {
		return WorkflowPromptsView{}, err
	}
	for i := range w.Nodes {
		n := &w.Nodes[i]
		if n.Type == WorkflowNodeReviewer {
			if strings.TrimSpace(n.Config.SystemPrompt) == "" {
				n.Config.SystemPrompt = pick(overrides.ReviewerSystem, openaicompat.DefaultReviewerPrompt())
			}
			if strings.TrimSpace(n.Config.UserTemplate) == "" {
				n.Config.UserTemplate = openaicompat.ReviewerUserTemplate
			}
		}
		if separate && (n.Type == WorkflowNodeReviewer || n.Type == WorkflowNodeWriter) {
			n.Config.SystemPrompt = openaicompat.SeparateEditorialPrompt(n.Config.SystemPrompt)
		}
	}
	return WorkflowPromptsView{Workflow: w, WriterContract: openaicompat.WriterJSONContract, ReviewerContract: openaicompat.ReviewerJSONContract}, nil
}

func validateEditablePrompts(w Workflow) error {
	if w.EditorialRules != nil && utf8.RuneCountInString(*w.EditorialRules) > 50000 {
		return fmt.Errorf("%w: 公共规则超过50000字", ErrInvalidWorkflow)
	}
	for _, node := range w.Nodes {
		if utf8.RuneCountInString(node.Config.SystemPrompt) > 50000 || utf8.RuneCountInString(node.Config.UserTemplate) > 50000 {
			return fmt.Errorf("%w: 节点提示词超过50000字", ErrInvalidWorkflow)
		}
	}
	return nil
}
