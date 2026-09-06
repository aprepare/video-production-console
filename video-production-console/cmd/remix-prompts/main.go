// remix-prompts exports editable production prompts without contacting any model.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"video-production-console/internal/agentruntime/openaicompat"
	"video-production-console/internal/remixlab"
)

func main() {
	dataRoot := flag.String("data-root", "./video-console-data", "Console data directory")
	outDir := flag.String("out-dir", "./output/remix-prompts", "Output directory")
	flag.Parse()
	if err := export(*dataRoot, *outDir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func export(dataRoot, outDir string) error {
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return err
	}
	store := remixlab.Store{DataRoot: dataRoot}
	accounts := []string{""}
	files, err := filepath.Glob(filepath.Join(dataRoot, "remix_lab", "workflows", "*.json"))
	if err != nil {
		return err
	}
	for _, file := range files {
		accounts = append(accounts, strings.TrimSuffix(filepath.Base(file), ".json"))
	}
	var all strings.Builder
	all.WriteString("# 二创流程全部提示词\n\n当前主流程：同行原文 → 写手 → 审稿 → 人工定稿。\n\n系统中的“编辑二创提示词”保存到当前账号工作流，新建和重新二创使用最新版，历史运行保持原快照。编辑本文件不会自动同步系统，可把相应文本粘贴回编辑器。公共规则会附加到写手与审稿提示词，输出格式单独列出。\n\n")
	for _, id := range accounts {
		view, err := store.WorkflowPromptsForAccount(id)
		if err != nil {
			return err
		}
		label := id
		if label == "" {
			label = "全局默认"
		}
		raw, err := json.MarshalIndent(view, "", "  ")
		if err != nil {
			return err
		}
		if err = os.WriteFile(filepath.Join(outDir, label+".json"), raw, 0644); err != nil {
			return err
		}
		var b strings.Builder
		fmt.Fprintf(&b, "# %s · %s\n\n## 公共写作规则\n\n%s\n\n", label, view.Workflow.Name, *view.Workflow.EditorialRules)
		for _, n := range view.Workflow.Nodes {
			if n.Type == remixlab.WorkflowNodeInput || n.Type == remixlab.WorkflowNodeOutput {
				continue
			}
			fmt.Fprintf(&b, "## %s（%s / %s）\n\n### 系统提示词\n\n%s\n\n### 用户模板\n\n%s\n\n", n.Title, n.ID, n.Type, n.Config.SystemPrompt, n.Config.UserTemplate)
			if n.Config.InjectRule != "" {
				fmt.Fprintf(&b, "### 输出注入规则\n\n%s\n\n", n.Config.InjectRule)
			}
		}
		fmt.Fprintf(&b, "## 写手输出格式（系统附加）\n\n%s\n\n## 审稿输出格式（系统附加）\n\n%s\n\n", view.WriterContract, view.ReviewerContract)
		if err = os.WriteFile(filepath.Join(outDir, label+".md"), []byte(b.String()), 0644); err != nil {
			return err
		}
		all.WriteString(b.String())
		all.WriteString("\n---\n\n")
	}
	if err = os.WriteFile(filepath.Join(outDir, "全部提示词.md"), []byte(all.String()), 0644); err != nil {
		return err
	}
	// Archived/optional roles remain available as reference, not active default steps.
	hook, facts, offline, ammo := openaicompat.DefaultIntelAgentPrompts()
	archive := map[string]any{"说明": "以下为备用或已停用的角色与提示词库，不是当前主流程步骤。", "reference_system": openaicompat.ReferenceSystemPrompt(), "reference_user": openaicompat.ReferenceUserTemplate, "reference_inject_rule": openaicompat.ReferenceInjectRule, "planner_system": hook, "planner_user": openaicompat.PlannerUserTemplate, "planner_inject_rule": openaicompat.PlannerInjectRule, "facts_search_system": facts, "facts_offline_system": offline, "facts_inject_rule": openaicompat.FactsInjectRule, "ammo_system": ammo, "ammo_inject_rule": openaicompat.AmmoInjectRule}
	library, err := store.ListLibrary()
	if err != nil {
		return err
	}
	archive["prompt_library"] = library
	raw, err := json.MarshalIndent(archive, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(outDir, "备用与历史提示词.json"), raw, 0644); err != nil {
		return err
	}
	fmt.Println(filepath.Join(outDir, "全部提示词.md"))
	return nil
}
