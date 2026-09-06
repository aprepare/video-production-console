package openaicompat

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// 工作流引擎：按快照里的节点图执行情报agent（支持 agent 链式依赖、按依赖
// 分波并行），输出按连线注入写手参考材料。写手成稿直接进入审稿，
// 审稿节点是否存在、用什么提示词由快照决定。节点输出逐个落盘为
// node_output_<id>.json，工作流视图直接读。

type flowNodeConfig struct {
	Role            string `json:"role,omitempty"`
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoning_effort"`
	ServiceTier     string `json:"service_tier,omitempty"`
	Channel         string `json:"channel"`
	SystemPrompt    string `json:"system_prompt"`
	UserTemplate    string `json:"user_template"`
	InjectTitle     string `json:"inject_title"`
	InjectRule      string `json:"inject_rule"`
	PromptID        string `json:"prompt_id"`
	// 兼容旧快照的已停用机械阈值；运行路径不使用。
	OverlapMaxPct  int     `json:"overlap_max_pct"`
	OverlapHardPct int     `json:"overlap_hard_pct"`
	LenMinRatio    float64 `json:"len_min_ratio"`
	LenHardRatio   float64 `json:"len_hard_ratio"`
	MaxRounds      int     `json:"max_rounds"`
}

type flowNode struct {
	ID     string         `json:"id"`
	Type   string         `json:"type"`
	Title  string         `json:"title"`
	Config flowNodeConfig `json:"config"`
}

type flowSpec struct {
	EditorialRules *string     `json:"editorial_rules,omitempty"`
	Nodes          []flowNode  `json:"nodes"`
	Edges          [][2]string `json:"edges"`
}

func (s flowSpec) writer() *flowNode {
	for i := range s.Nodes {
		if s.Nodes[i].Type == "writer" {
			return &s.Nodes[i]
		}
	}
	return nil
}

func parseFlowSpec(raw string) (flowSpec, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return flowSpec{}, false
	}
	var spec flowSpec
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		return flowSpec{}, false
	}
	return spec, len(spec.Nodes) > 0
}

func (s flowSpec) reviewer() *flowNode {
	for i := range s.Nodes {
		if s.Nodes[i].Type == "reviewer" {
			return &s.Nodes[i]
		}
	}
	return nil
}

func (s flowSpec) selfcheckNode() *flowNode {
	for i := range s.Nodes {
		if s.Nodes[i].Type == "selfcheck" {
			return &s.Nodes[i]
		}
	}
	return nil
}

// applyFlowSelfCheckLimits 把机械自检节点的覆盖值合进默认阈值（零值跳过）。
func applyFlowSelfCheckLimits(limits SelfCheckLimits, cfg flowNodeConfig) SelfCheckLimits {
	if cfg.OverlapMaxPct > 0 {
		limits.MaxOverlap = float64(cfg.OverlapMaxPct) / 100
	}
	if cfg.OverlapHardPct > 0 {
		limits.HardOverlap = float64(cfg.OverlapHardPct) / 100
	}
	if cfg.LenMinRatio > 0 {
		limits.MinLenRatio = cfg.LenMinRatio
	}
	if cfg.LenHardRatio > 0 {
		limits.HardLenRatio = cfg.LenHardRatio
	}
	if cfg.MaxRounds > 0 {
		limits.MaxRounds = cfg.MaxRounds
	}
	// 防呆：硬上限不能比触发线还严，硬下限不能比触发线还松。
	if limits.HardOverlap < limits.MaxOverlap {
		limits.HardOverlap = limits.MaxOverlap
	}
	if limits.HardLenRatio > limits.MinLenRatio {
		limits.HardLenRatio = limits.MinLenRatio
	}
	return limits
}

func (s flowSpec) agents() []flowNode {
	out := make([]flowNode, 0, len(s.Nodes))
	for _, node := range s.Nodes {
		if node.Type == "agent" {
			out = append(out, node)
		}
	}
	return out
}

// runFlowAgents 按依赖分波并行执行 agent 节点，返回注入写手的情报包文本。
// 任何节点失败不拦整体：缺哪路记哪路，写手照常动笔。
func runFlowAgents(mainClient ChatClient, opts Options, source, outputDir string, spec flowSpec) string {
	agents := spec.agents()
	if len(agents) == 0 {
		return ""
	}
	mainModel := strings.TrimSpace(opts.Model)

	searchClient := opts.SearchClient
	searchModel := strings.TrimSpace(opts.SearchModel)
	searchUsed := true
	if searchClient == nil {
		base := strings.TrimSpace(opts.SearchBaseURL)
		key := strings.TrimSpace(opts.SearchAPIKey)
		if base != "" && key != "" && searchModel != "" {
			searchClient = &HTTPChatClient{BaseURL: base, APIKey: key}
		}
	}
	if searchClient == nil || searchModel == "" {
		searchClient = mainClient
		searchModel = mainModel
		searchUsed = false
	}

	// agent 间依赖（只统计 agent→agent 的边；原文对所有节点可用）
	agentIDs := make(map[string]bool, len(agents))
	for _, node := range agents {
		agentIDs[node.ID] = true
	}
	deps := map[string][]string{}
	for _, edge := range spec.Edges {
		if agentIDs[edge[0]] && agentIDs[edge[1]] {
			deps[edge[1]] = append(deps[edge[1]], edge[0])
		}
	}

	type result struct {
		outcome intelAgentOutcome
		content string
	}
	results := map[string]result{}
	var mu sync.Mutex
	done := map[string]bool{}

	runNode := func(node flowNode) result {
		// 断点续跑：上一轮已产出的节点直接复用，不再花一次模型调用。
		// 重试指定节点时由服务端先删掉对应产物文件再驱动。
		if raw, err := os.ReadFile(filepath.Join(outputDir, flowNodeOutputFile(node.ID))); err == nil && len(strings.TrimSpace(string(raw))) > 0 {
			cached := result{
				outcome: intelAgentOutcome{Name: node.ID, Model: strings.TrimSpace(node.Config.Model), Cached: true, Bytes: len(raw)},
				content: strings.TrimSpace(string(raw)),
			}
			if isFactsNode(node) {
				cached.outcome.content = cached.content
				recordFactsOutcome(&cached.outcome, outputDir)
				cached.content = cached.outcome.content
			}
			if node.ID == "ammo" {
				cached.content = compactAmmo(cached.content)
			}
			if isReferenceNode(node) {
				if _, err := parseReferenceCopy(cached.content); err != nil {
					cached.outcome.Error = err.Error()
				}
			}
			return cached
		}
		model := strings.TrimSpace(node.Config.Model)
		client := mainClient
		if strings.EqualFold(strings.TrimSpace(node.Config.Channel), "search") {
			client = searchClient
			if model == "" {
				model = searchModel
			}
		}
		if model == "" {
			model = mainModel
		}
		effort := strings.TrimSpace(node.Config.ReasoningEffort)
		if effort == "" {
			effort = strings.TrimSpace(opts.ReasoningEffort)
		}
		mu.Lock()
		upstream := make(map[string]string, len(deps[node.ID]))
		for _, dep := range deps[node.ID] {
			if r, ok := results[dep]; ok && r.outcome.Error == "" {
				upstream[dep] = r.content
			} else {
				upstream[dep] = "（上游节点 " + dep + " 失败，无输出）"
			}
		}
		mu.Unlock()
		user := resolveFlowFiles(renderFlowTemplate(node, source, upstream, spec), opts.IntelFileDir)
		system := resolveFlowFiles(node.Config.SystemPrompt, opts.IntelFileDir)
		if isReferenceNode(node) && strings.TrimSpace(system) == "" {
			system = ReferenceSystemPrompt()
		}
		if isFactsNode(node) {
			user = factsTimeContext() + user
			client = boundedFactsClient(client)
		}

		started := time.Now()
		outcome := intelAgentOutcome{Name: node.ID, Model: model}
		resp, err := chatIntel(withServiceTier(client, node.Config.ServiceTier), ChatRequest{
			Model:           model,
			ReasoningEffort: effort,
			Stream:          true,
			Messages: []Message{
				{Role: "system", Content: system},
				{Role: "user", Content: user},
			},
		}, isFactsNode(node))
		outcome.Millis = time.Since(started).Milliseconds()
		if err != nil {
			outcome.Error = err.Error()
		} else if len(resp.Choices) == 0 {
			outcome.Error = "empty chat choices"
		} else {
			content := strings.TrimSpace(resp.Choices[0].Message.Content)
			outcome.Bytes = len(content)
			outcome.content = content
		}
		if isReferenceNode(node) {
			var cause error
			if outcome.Error != "" {
				cause = fmt.Errorf("%s", outcome.Error)
			}
			content, saveErr := recordReferenceAttempt(outputDir, node, model, effort, outcome.content, cause)
			if saveErr != nil {
				outcome.Error = saveErr.Error()
			} else {
				outcome.content = content
			}
		}
		if outcome.Error == "" && outcome.content != "" {
			if err := os.WriteFile(filepath.Join(outputDir, flowNodeOutputFile(node.ID)), []byte(outcome.content), 0o600); err != nil {
				outcome.Error = "保存节点输出: " + err.Error()
			}
		}
		if isFactsNode(node) {
			recordFactsOutcome(&outcome, outputDir)
		}
		if node.ID == "ammo" {
			outcome.content = compactAmmo(outcome.content)
		}
		return result{outcome: outcome, content: outcome.content}
	}

	// 按依赖分波：本波跑所有依赖已就绪的节点，直到全部完成（图已在保存时校验无环）。
	for len(done) < len(agents) {
		wave := make([]flowNode, 0, len(agents))
		for _, node := range agents {
			if done[node.ID] {
				continue
			}
			ready := true
			for _, dep := range deps[node.ID] {
				if !done[dep] {
					ready = false
					break
				}
			}
			if ready {
				wave = append(wave, node)
			}
		}
		if len(wave) == 0 {
			// 理论上到不了：保存时已做环检测。防御性兜底，别死循环。
			break
		}
		var wg sync.WaitGroup
		for _, node := range wave {
			wg.Add(1)
			go func(node flowNode) {
				defer wg.Done()
				r := runNode(node)
				mu.Lock()
				results[node.ID] = r
				mu.Unlock()
			}(node)
		}
		wg.Wait()
		for _, node := range wave {
			done[node.ID] = true
		}
	}

	outcomes := make([]intelAgentOutcome, 0, len(agents))
	for _, node := range agents {
		outcomes = append(outcomes, results[node.ID].outcome)
	}
	summary := map[string]any{"search_used": searchUsed, "workflow": true, "agents": outcomes}
	if raw, err := json.MarshalIndent(summary, "", "  "); err == nil {
		_ = os.WriteFile(filepath.Join(outputDir, "intel_summary.json"), raw, 0o644)
	}
	for _, outcome := range outcomes {
		appendRemixRunLog(outputDir, map[string]any{
			"event": "intel_agent", "agent": outcome.Name, "model": outcome.Model,
			"ms": outcome.Millis, "bytes": outcome.Bytes, "error": outcome.Error,
		})
	}

	// 情报包 = 有连线进写手的 agent 输出，按节点声明顺序拼装。
	writerID := ""
	for _, node := range spec.Nodes {
		if node.Type == "writer" {
			writerID = node.ID
			break
		}
	}
	intoWriter := map[string]bool{}
	for _, edge := range spec.Edges {
		if edge[1] == writerID && agentIDs[edge[0]] {
			intoWriter[edge[0]] = true
		}
	}
	var b strings.Builder
	sections := 0
	for _, node := range agents {
		if !intoWriter[node.ID] {
			continue
		}
		r := results[node.ID]
		section := capIntelSection(r.content)
		if isReferenceNode(node) {
			section = r.content
		}
		if !isReferenceNode(node) && (node.ID == "facts" || strings.Contains(r.content, "source_facts")) {
			section = normalizeFacts(r.content)
			_ = os.WriteFile(filepath.Join(outputDir, "facts_research.json"), []byte(section), 0644)
		}
		if r.outcome.Error != "" || strings.TrimSpace(r.content) == "" {
			continue
		}
		title := strings.TrimSpace(node.Config.InjectTitle)
		if title == "" {
			title = node.Title
		}
		b.WriteString("\n〔" + title + "〕")
		if rule := strings.TrimSpace(node.Config.InjectRule); rule != "" {
			b.WriteString(rule + "：")
		}
		b.WriteString("\n")
		b.WriteString(section)
		b.WriteString("\n")
		sections++
	}
	if sections == 0 {
		return ""
	}
	return IntelPacketHeader + strings.TrimRight(b.String(), "\n")
}

func flowNodeOutputFile(id string) string {
	return fmt.Sprintf("node_output_%s.json", id)
}

// resolveFlowFiles 把 {{file:名字}} 占位符替换成 dir 下同名文件的内容，让
// 素材库这类会持续追加的资料每次运行都读磁盘最新版。名字只取文件名部分，
// 防止路径穿越；dir 为空或文件缺失时替换成说明文字，不拦运行。
func resolveFlowFiles(text, dir string) string {
	const marker = "{{file:"
	var b strings.Builder
	rest := text
	for {
		start := strings.Index(rest, marker)
		if start < 0 {
			b.WriteString(rest)
			return b.String()
		}
		end := strings.Index(rest[start:], "}}")
		if end < 0 {
			b.WriteString(rest)
			return b.String()
		}
		end += start
		name := filepath.Base(strings.TrimSpace(rest[start+len(marker) : end]))
		replacement := "（素材文件 " + name + " 未配置）"
		if dir != "" && name != "" && name != "." {
			if raw, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
				replacement = strings.TrimSpace(string(raw))
			} else {
				replacement = "（素材文件 " + name + " 读取失败，忽略该部分）"
			}
		}
		b.WriteString(rest[:start])
		b.WriteString(replacement)
		rest = rest[end+2:]
	}
}

// renderFlowTemplate 渲染 agent 节点的用户消息：支持 {{source}} 和
// {{node:ID}} 占位；模板为空时自动拼原文和全部上游输出。
func renderFlowTemplate(node flowNode, source string, upstream map[string]string, spec flowSpec) string {
	tmpl := strings.TrimSpace(node.Config.UserTemplate)
	if tmpl == "" {
		var b strings.Builder
		b.WriteString("处理下面的材料，按系统提示输出。\n\n# 原文\n")
		b.WriteString(source)
		for _, other := range spec.Nodes {
			if content, ok := upstream[other.ID]; ok {
				b.WriteString("\n\n# 上游输出（" + other.Title + "）\n")
				b.WriteString(content)
			}
		}
		return b.String()
	}
	out := strings.ReplaceAll(tmpl, "{{source}}", source)
	for id, content := range upstream {
		out = strings.ReplaceAll(out, "{{node:"+id+"}}", content)
	}
	return out
}
