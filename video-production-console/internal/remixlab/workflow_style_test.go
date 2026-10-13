package remixlab

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"video-production-console/internal/agentruntime/openaicompat"
)

func TestWorkflowPromptSwitchFromLegacyInlineToActiveLibrary(t *testing.T) {
	for _, oldID := range []string{"", "old-library"} {
		t.Run(oldID, func(t *testing.T) {
			st := Store{DataRoot: t.TempDir()}
			id := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
			if _, err := st.UpsertPrompt(PromptTemplate{ID: "selected", Name: "库模板", System: "库里的新正文", User: "新输入 {{SOURCE}}"}); err != nil {
				t.Fatal(err)
			}
			if err := st.SetActive(ActivePrompt{ID: "selected", System: "每日覆盖正文", User: "每日输入 {{SOURCE}}"}); err != nil {
				t.Fatal(err)
			}
			w := DefaultWorkflow(AgentPrompts{})
			writer := workflowWriter(w)
			writer.Config.PromptID, writer.Config.SystemPrompt, writer.Config.UserTemplate = oldID, "旧冻结正文", "旧冻结输入"
			path := st.accountWorkflowPath(id)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(w)
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			writer.Config.PromptID = "selected"
			if err := st.SaveWorkflowForAccount(id, w); err != nil {
				t.Fatal(err)
			}
			view, err := st.WorkflowPromptsForAccount(id)
			if err != nil {
				t.Fatal(err)
			}
			got := workflowWriter(view.Workflow).Config
			if got.SystemPrompt != "库里的新正文" || got.UserTemplate != "新输入 {{SOURCE}}" {
				t.Fatalf("explicit library switch resolved daily override: %+v", got)
			}
		})
	}
}

func TestWorkflowPromptSelectionReplacesFrozenWriter(t *testing.T) {
	st := Store{DataRoot: t.TempDir()}
	for _, id := range []string{"first-writer", "next-writer"} {
		if _, err := st.UpsertPrompt(PromptTemplate{ID: id, Name: id, System: id + " system", User: id + " {{SOURCE}}"}); err != nil {
			t.Fatal(err)
		}
	}
	wf := DefaultWorkflow(AgentPrompts{})
	rules := ""
	wf.EditorialRules = &rules
	writer := workflowWriter(wf)
	writer.Config.PromptID = "first-writer"
	writer.Config.SystemPrompt = "旧账号已确认的冻结正文"
	writer.Config.UserTemplate = "旧账号的输入 {{SOURCE}}"
	accountID := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	if err := st.SaveWorkflowForAccount(accountID, wf); err != nil {
		t.Fatal(err)
	}
	view, err := st.WorkflowPromptsForAccount(accountID)
	if err != nil || workflowWriter(view.Workflow).Config.SystemPrompt != "旧账号已确认的冻结正文" {
		t.Fatalf("legacy frozen prompt changed: %+v, %v", view, err)
	}
	// 直接修改旧文件读出的节点，覆盖没有冻结来源字段的历史工作流。
	wf, err = st.LoadWorkflowForAccount(accountID)
	if err != nil {
		t.Fatal(err)
	}
	workflowWriter(wf).Config.PromptID = "next-writer"
	if err := st.SaveWorkflowForAccount(accountID, wf); err != nil {
		t.Fatal(err)
	}
	view, err = st.WorkflowPromptsForAccount(accountID)
	if err != nil {
		t.Fatal(err)
	}
	got := workflowWriter(view.Workflow).Config
	if got.SystemPrompt != "next-writer system" || got.UserTemplate != "next-writer {{SOURCE}}" {
		t.Fatalf("prompt_id switch kept old frozen content: %+v", got)
	}
}

func TestApplyStylePreservesOtherNodesAndBriefOnlyUpdate(t *testing.T) {
	st := Store{DataRoot: t.TempDir()}
	id := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	w := DefaultWorkflow(AgentPrompts{})
	writer := workflowWriter(w)
	writer.Config.Model, writer.Config.ServiceTier, writer.Config.PromptID = "writer-model", "priority", "elder_stable"
	writer.Config.ReasoningEffort = "high"
	w.Nodes[1].Config.Model, w.Nodes[1].Config.ServiceTier = "planner-model", "priority"
	custom := WorkflowNode{ID: "facts", Type: WorkflowNodeAgent, Title: "事实来源", Config: WorkflowNodeConfig{SystemPrompt: "保留的自定义事实提示词", UserTemplate: "{{source}}", Model: "facts-model", Channel: "search"}}
	w.Nodes = append(w.Nodes, custom)
	w.Edges = append(w.Edges, [2]string{"source", "facts"}, [2]string{"facts", "writer"})
	if err := st.SaveWorkflowForAccount(id, w); err != nil {
		t.Fatal(err)
	}
	style, err := st.UpsertStyle(StyleTemplate{Name: "自定义门店", Writer: "门店写手", Planner: "门店策划", Rules: "额外规则"})
	if err != nil {
		t.Fatal(err)
	}
	view, err := st.ApplyStyleForAccount(id, style.ID, &Brief{Industry: " 餐饮 ", Conversion: "到店"})
	if err != nil {
		t.Fatal(err)
	}
	if view.Workflow.Style != style.ID || view.Workflow.Brief.Industry != "餐饮" || !reflect.DeepEqual(view.Workflow.Production, w.Production) {
		t.Fatalf("metadata not retained: %+v", view.Workflow)
	}
	if !strings.Contains(*view.Workflow.EditorialRules, UniversalPublishingContract) || !strings.Contains(*view.Workflow.EditorialRules, "额外规则") {
		t.Fatal("rules missing")
	}
	for _, node := range view.Workflow.Nodes {
		switch node.ID {
		case "facts":
			if !reflect.DeepEqual(node, custom) {
				t.Fatalf("custom node changed: %+v", node)
			}
		case "writer":
			if node.Config.Model != "writer-model" || node.Config.ServiceTier != "priority" || node.Config.ReasoningEffort != "high" || node.Config.PromptID != "" || node.Config.SystemPrompt != style.Writer {
				t.Fatalf("writer config: %+v", node)
			}
		case "hook":
			// 通用风格的策划用自己的 user 模板与注入纪律，不沿用财经的课尾五步。
			if node.Config.Model != "planner-model" || node.Config.ServiceTier != "priority" || node.Config.SystemPrompt != style.Planner || node.Config.UserTemplate != universalPlannerUser || node.Config.InjectRule != universalPlannerInjectRule {
				t.Fatalf("planner config: %+v", node)
			}
		case "review":
			if node.Config != workflowReviewer(w).Config {
				t.Fatalf("empty style reviewer replaced existing: %+v", node)
			}
		}
	}
	before, err := st.LoadWorkflowForAccount(id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpdateBriefForAccount(id, Brief{Offer: "新套餐"}); err != nil {
		t.Fatal(err)
	}
	after, err := st.LoadWorkflowForAccount(id)
	if err != nil {
		t.Fatal(err)
	}
	before.Brief = after.Brief
	if !reflect.DeepEqual(before, after) {
		t.Fatal("brief-only update changed workflow prompts or configuration")
	}
	if _, err := st.ApplyStyleForAccount(id, "light_wash", nil); err != nil { // 轻度洗稿不带策划
		t.Fatal(err)
	}
	after, err = st.LoadWorkflowForAccount(id)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range after.Nodes {
		if node.ID == "hook" {
			t.Fatal("empty planner retained hook")
		}
	}
	for _, edge := range after.Edges {
		if edge[0] == "hook" || edge[1] == "hook" {
			t.Fatal("empty planner retained hook edges")
		}
	}
	if after.Brief != nil {
		t.Fatal("nil brief not saved")
	}
}

func TestNewAccountWorkflowUsesUniversalStyle(t *testing.T) {
	st := Store{DataRoot: t.TempDir()}
	wf, err := st.LoadWorkflowForAccount("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(wf)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	if string(fields["style"]) != `"universal_remix"` {
		t.Fatalf("new account style = %s", fields["style"])
	}
	planners := 0
	for _, n := range wf.Nodes {
		if n.Type != WorkflowNodeAgent {
			continue
		}
		planners++
		if n.ID != openaicompat.PlannerNodeID || n.Config.InjectRule != universalPlannerInjectRule || strings.Contains(n.Config.SystemPrompt, "财富觉醒方法论") {
			t.Fatalf("new account planner is not the universal one: %+v", n)
		}
	}
	if planners != 1 {
		t.Fatalf("new account planners = %d, want the universal planner", planners)
	}
	global, err := st.LoadWorkflow()
	if err != nil {
		t.Fatal(err)
	}
	if len(global.Nodes) != 5 {
		t.Fatalf("legacy global default changed: %+v", global)
	}
}

func TestWorkflowPersistsBriefAndStyle(t *testing.T) {
	st := Store{DataRoot: t.TempDir()}
	raw, _ := json.Marshal(DefaultWorkflow(AgentPrompts{}))
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	fields["style"] = json.RawMessage(`"local_store"`)
	fields["brief"] = json.RawMessage(`{"industry":" 海鲜餐饮 ","selling_points":"双人套餐128元","duration_s":60}`)
	raw, _ = json.Marshal(fields)
	var wf Workflow
	if err := json.Unmarshal(raw, &wf); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveWorkflow(wf); err != nil {
		t.Fatal(err)
	}
	wf, err := st.LoadWorkflow()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(wf)
	fields = nil
	_ = json.Unmarshal(raw, &fields)
	var brief map[string]any
	_ = json.Unmarshal(fields["brief"], &brief)
	if string(fields["style"]) != `"local_store"` || brief["industry"] != "海鲜餐饮" || brief["duration_s"] != float64(60) {
		t.Fatalf("workflow dropped/failed to normalize brief: %s", raw)
	}
}
