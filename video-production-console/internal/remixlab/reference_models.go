package remixlab

import (
	"fmt"
	"video-production-console/internal/agentruntime/openaicompat"
)

func withReferenceDefaults(w Workflow) Workflow {
	w.Nodes = append([]WorkflowNode(nil), w.Nodes...)
	for i := range w.Nodes {
		n := &w.Nodes[i]
		if n.Type != WorkflowNodeAgent || n.Config.Role != openaicompat.ReferenceDraftRole {
			continue
		}
		if n.Config.SystemPrompt == "" {
			n.Config.SystemPrompt = openaicompat.ReferenceSystemPrompt()
		}
		if n.Config.UserTemplate == "" {
			n.Config.UserTemplate = openaicompat.ReferenceUserTemplate
		}
		if n.Config.InjectTitle == "" {
			n.Config.InjectTitle = n.Title
		}
		if n.Config.InjectRule == "" {
			n.Config.InjectRule = openaicompat.ReferenceInjectRule
		}
	}
	return w
}

// A nil request keeps the saved graph; an explicit empty list selects direct writing.
func withReferenceModels(w Workflow, models []RerunModel) (Workflow, error) {
	if len(models) > maxWorkflowAgents {
		return w, fmt.Errorf("%w: 参考模型最多%d个", ErrInvalidRerun, maxWorkflowAgents)
	}
	for i := range models {
		if err := validateRerunModel(&models[i]); err != nil {
			return w, err
		}
	}
	removed := map[string]bool{}
	var old []WorkflowNode
	var nodes []WorkflowNode
	var source, writer WorkflowNode
	used := map[string]bool{}
	for _, n := range w.Nodes {
		if n.Type == WorkflowNodeAgent && n.Config.Role == openaicompat.ReferenceDraftRole {
			removed[n.ID] = true
			old = append(old, n)
			continue
		}
		if n.Type == WorkflowNodeInput {
			source = n
		}
		if n.Type == WorkflowNodeWriter {
			writer = n
		}
		nodes = append(nodes, n)
		used[n.ID] = true
	}
	var edges [][2]string
	for _, e := range w.Edges {
		if removed[e[0]] && !removed[e[1]] && e[1] != writer.ID {
			return w, fmt.Errorf("%w: 参考节点仍被%s依赖，请先调整连线", ErrInvalidRerun, e[1])
		}
		if !removed[e[0]] && !removed[e[1]] {
			edges = append(edges, e)
		}
	}
	for i, m := range models {
		id := fmt.Sprintf("reference_%d", i+1)
		if i < len(old) {
			id = old[i].ID
		}
		for used[id] {
			id += "_r"
		}
		used[id] = true
		n := WorkflowNode{ID: id, Type: WorkflowNodeAgent, Title: fmt.Sprintf("参考稿%d", i+1), X: (source.X + writer.X) / 2, Y: source.Y + float64(i)*150, Config: WorkflowNodeConfig{Role: openaicompat.ReferenceDraftRole, Model: m.Model, ReasoningEffort: m.ReasoningEffort, ServiceTier: m.ServiceTier}}
		if i < len(old) && old[i].Config.Role == openaicompat.ReferenceDraftRole {
			n.Config = old[i].Config
			n.Config.Model = m.Model
			n.Config.ReasoningEffort = m.ReasoningEffort
			n.Config.ServiceTier = m.ServiceTier
			n.X = old[i].X
			n.Y = old[i].Y
		}
		nodes = append(nodes, n)
		edges = append(edges, [2]string{source.ID, id}, [2]string{id, writer.ID})
	}
	hasSourceEdge := false
	for _, e := range edges {
		if e == [2]string{source.ID, writer.ID} {
			hasSourceEdge = true
		}
	}
	if !hasSourceEdge {
		edges = append(edges, [2]string{source.ID, writer.ID})
	}
	w.Nodes = nodes
	w.Edges = edges
	w = withReferenceDefaults(w)
	return w, ValidateWorkflow(w)
}
