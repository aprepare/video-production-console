package remixlab

import (
	"fmt"
	"strings"

	"video-production-console/internal/agentruntime/openaicompat"
)

// The override belongs to this run's snapshot; never save it to account defaults.
func withBatchReviewer(w Workflow, choice *RerunModel) (Workflow, error) {
	if choice == nil {
		return w, nil
	}
	selected := *choice
	if strings.TrimSpace(selected.Model) != "" {
		if err := validateRerunModel(&selected); err != nil {
			return w, err
		}
	}
	w.Nodes = append([]WorkflowNode(nil), w.Nodes...)
	w.Edges = append([][2]string(nil), w.Edges...)
	existing := workflowReviewer(w)
	if selected.Model == "" || strings.TrimSpace(selected.Model) == "" {
		if existing == nil {
			return w, nil
		}
		id := existing.ID
		var from, to []string
		var edges [][2]string
		for _, e := range w.Edges {
			if e[1] == id {
				from = append(from, e[0])
			}
			if e[0] == id {
				to = append(to, e[1])
			}
			if e[0] != id && e[1] != id {
				edges = append(edges, e)
			}
		}
		for _, a := range from {
			for _, b := range to {
				edges = append(edges, [2]string{a, b})
			}
		}
		nodes := make([]WorkflowNode, 0, len(w.Nodes)-1)
		for _, n := range w.Nodes {
			if n.ID != id {
				nodes = append(nodes, n)
			}
		}
		w.Nodes, w.Edges = nodes, edges
		return w, ValidateWorkflow(w)
	}
	if existing == nil {
		id := "batch_review"
		ids := map[string]bool{}
		for _, n := range w.Nodes {
			ids[n.ID] = true
		}
		for i := 1; ids[id]; i++ {
			id = fmt.Sprintf("batch_review_%d", i)
		}
		for i, e := range w.Edges {
			for _, n := range w.Nodes {
				if n.Type == WorkflowNodeOutput && n.ID == e[1] {
					w.Edges[i][1] = id
					w.Edges = append(w.Edges, [2]string{id, n.ID})
					break
				}
			}
		}
		w.Nodes = append(w.Nodes, WorkflowNode{ID: id, Type: WorkflowNodeReviewer, Title: "本次审稿", Config: WorkflowNodeConfig{
			SystemPrompt: "你是本次批量二创的审稿编辑。遵循本篇公共规则和用户要求；保留已锁定头尾，检查中段是否实质重构、事实是否被无依据改动、口播是否通顺及课程是否匹配。只改具体问题，不另换开头、不强加固定课尾或字数，不把没有核实的内容声称已核实。保持完整正文与发布字段，说明每处修改依据。没有问题直接通过。",
			UserTemplate: openaicompat.ReviewerUserTemplate,
		}})
		existing = workflowReviewer(w)
	}
	existing.Config.Model = selected.Model
	existing.Config.ReasoningEffort = selected.ReasoningEffort
	existing.Config.ServiceTier = selected.ServiceTier
	return w, ValidateWorkflow(w)
}
