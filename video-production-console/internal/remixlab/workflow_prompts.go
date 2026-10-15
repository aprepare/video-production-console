package remixlab

import (
	"fmt"
	"os"
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
	node := workflowWriter(w)
	if node != nil {
		id = strings.TrimSpace(node.Config.PromptID)
	}
	changed := writerSelectionChanged(node)
	// 风格正文已随工作流冻结；模板被编辑或删除不影响已有工作流。
	if id == "" && w.Style != "" && node != nil && strings.TrimSpace(node.Config.SystemPrompt) != "" {
		name := w.StyleName
		if name == "" {
			if style, ok, err := s.GetStyle(w.Style); err == nil && ok {
				name = style.Name
			}
		}
		return PromptTemplate{ID: w.Style, Name: pick(name, w.Name), Style: openaicompat.PromptStyleRewrite,
			System: node.Config.SystemPrompt, User: pick(node.Config.UserTemplate, styleWriterUserTemplate)}, nil
	}
	active, hasActive, err := s.GetActive()
	if err != nil {
		return PromptTemplate{}, err
	}
	var prompt PromptTemplate
	// 显式切换后正文已清空，必须从库里重新取值；已有冻结正文仍兼容日产覆盖。
	keepActive := id == "" || (id == active.ID && node != nil && strings.TrimSpace(node.Config.SystemPrompt) != "")
	if hasActive && keepActive && !changed {
		prompt = PromptTemplate{ID: active.ID, Name: active.Name, Stamp: active.Stamp, Style: active.Style, System: active.System, User: active.User}
	} else {
		if id == "" {
			id = "elder_stable"
		}
		var found bool
		prompt, found, err = s.GetPrompt(id)
		if err != nil {
			return PromptTemplate{}, err
		}
		if !found {
			prompt = ResolvePrompt("elder_stable", "", "")
		}
	}
	if node != nil && !changed {
		if strings.TrimSpace(node.Config.SystemPrompt) != "" {
			prompt.System = node.Config.SystemPrompt
		}
		if strings.TrimSpace(node.Config.UserTemplate) != "" {
			prompt.User = node.Config.UserTemplate
		}
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
			changed := writerSelectionChanged(&w.Nodes[i])
			if changed || strings.TrimSpace(w.Nodes[i].Config.SystemPrompt) == "" {
				w.Nodes[i].Config.SystemPrompt = prompt.System
			}
			if changed || strings.TrimSpace(w.Nodes[i].Config.UserTemplate) == "" {
				w.Nodes[i].Config.UserTemplate = prompt.User
			}
			if w.Nodes[i].Config.PromptID != "" || w.Style == "" {
				w.Nodes[i].Config.FrozenPromptID = prompt.ID
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
		if !w.FinanceMode() {
			rules = UniversalRules + "\n\n" + UniversalPublishingContract
		}
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
				if w.FinanceMode() {
					n.Config.SystemPrompt = pick(overrides.ReviewerSystem, openaicompat.DefaultReviewerPrompt())
				} else {
					n.Config.SystemPrompt = universalReviewerRole()
				}
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

func writerSelectionChanged(node *WorkflowNode) bool {
	return node != nil && node.Config.PromptID != "" && node.Config.FrozenPromptID != "" && node.Config.PromptID != node.Config.FrozenPromptID
}

// 保存时对照旧文件识别切换，兼容尚未记录 frozen_prompt_id 的生产工作流。
func (s Store) prepareWriterSelection(path string, w *Workflow) error {
	node := workflowWriter(*w)
	if node == nil {
		return nil
	}
	node.Config.PromptID = strings.TrimSpace(node.Config.PromptID)
	changed := writerSelectionChanged(node)
	raw, err := os.ReadFile(path)
	if err == nil {
		previous, err := decodeWorkflow(raw)
		if err != nil {
			return err
		}
		if old := workflowWriter(previous); old != nil && node.Config.PromptID != "" && node.Config.PromptID != old.Config.PromptID {
			changed = true
			if node.Config.FrozenPromptID == "" {
				node.Config.FrozenPromptID = pick(old.Config.FrozenPromptID, old.Config.PromptID)
			}
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read previous workflow: %w", err)
	}
	if changed {
		node.Config.SystemPrompt = ""
		node.Config.UserTemplate = ""
	} else if node.Config.PromptID != "" && node.Config.FrozenPromptID == "" && strings.TrimSpace(node.Config.SystemPrompt) != "" {
		node.Config.FrozenPromptID = node.Config.PromptID
	}
	return nil
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
