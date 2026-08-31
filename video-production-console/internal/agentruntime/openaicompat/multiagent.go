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

// 多agent情报组：写手动笔前，三路并行分析原文——钩子指纹、事实核查（走
// 搜索通道核实并补充带来源的新数据）、意象与现场弹药——产物拼成【情报包】
// 注入写手的 user 消息。情报只当弹药不当指令：最终成稿仍由写手一次写完，
// 保住口播的气口和节奏。任何一路失败都不拦写手，缺哪路记哪路。
const PipelineMultiAgent = "multi_agent"

// intelSectionRuneCap 限制单路情报注入写手上下文的长度，防止分析盖过原文。
const intelSectionRuneCap = 3600

const hookAgentSystem = `你是爆款财经口播的钩子分析师。只分析，不改写，不评价好坏。
只返回一个 JSON 对象，不要 Markdown，字段：
- hook_type：开头钩子类型（宣布大事/数字砸脸/提问逼问/排除法/截止日/人群圈定，可组合）
- first_three：逐句拆前三句，每句在做什么、狠劲来自哪里，各用一句话
- retention_engine：整篇靠什么拽人往下听（未揭晓答案/翻转/对照……说清具体是哪一个、埋在哪）
- unrevealed：原文故意不说破的那个点，原样描述，不要替它说破
- rhythm：节奏特征——哪里急停、哪里短句连砸、哪里放缓
- replication_guide：给写手的复刻要点：要保住哪种狠法和节奏，哪些字面绝对不能抄`

const factsAgentSystemSearch = `你是财经事实核查员，具备联网搜索能力。任务：核实原文数字，并补充有火力的最新数据。
规则：
- 把原文里的数字、日期、机构、政策、历史先例逐个列出；对时效敏感的（利率、规模、政策状态、价格）联网核实：仍然成立 / 已过时（给最新值）/ 查不到。
- 再联网找 2 到 4 条原文没有、但对这个主题有火力的最新数据或事件（越新越好），每条必须带来源（媒体或机构名+日期），并给出口播里怎么带来源的说法（例如「据央行8月数据」）。
- 不许编造：查不到就写查不到；不确定的标 low_confidence。数字保持原始口径。
只返回一个 JSON 对象，不要 Markdown，字段：
- source_facts：[{claim, value, status(成立/已过时/查不到), latest, source}]
- fresh_ammo：[{fact, value, source, spoken_citation, where}]
- risk_notes：使用这些数字要避开的坑（口径、单位、时间点）`

const factsAgentSystemOffline = `你是财经事实核查员。当前没有联网通道，只做原文事实盘点，不许编造任何新数据。
规则：
- 把原文里的数字、日期、机构、政策、历史先例逐个列出来；按常识标注哪些时效存疑（needs_verify），哪些是稳定事实（成立）。
- fresh_ammo 必须返回空数组。
只返回一个 JSON 对象，不要 Markdown，字段：
- source_facts：[{claim, value, status(成立/needs_verify), source}]
- fresh_ammo：[]
- risk_notes：使用这些数字要避开的坑（口径、单位、时间点）`

const ammoAgentSystem = `你是二创改写的军火库。只出弹药，不写成稿。
只返回一个 JSON 对象，不要 Markdown，字段：
- banned_imagery：原文用过的比喻、意象、场景道具，从头盘到尾全列出来（这是新稿的禁用清单，收尾段的比喻重点盘）
- center_options：3 个原文没用过的中心意象候选，每个附一句为什么适配这篇的事实链（观众 40 到 65 岁，生活里的东西优先：存折、菜市场、户口、赶集这类）
- scenes：2 到 3 个可插中后段的原创现场（有人、有事、有对话要点；禁止编造任何数字）
- phrase_swaps：原文高频或标志性的表达换成什么讲法，8 到 15 组
- course_hook_options：课尾从正文滑向课程的过渡句 3 种（不吆喝，是替观众把没答的问题问出来那种）`

type intelAgentSpec struct {
	name   string
	file   string
	system string
	user   string
	model  string
	client ChatClient
}

type intelAgentOutcome struct {
	Name   string `json:"name"`
	Model  string `json:"model"`
	Millis int64  `json:"ms"`
	Bytes  int    `json:"bytes"`
	// Cached 表示断点续跑时复用了上一轮产物，没有重新调模型。
	Cached  bool   `json:"cached,omitempty"`
	Error   string `json:"error,omitempty"`
	content string
}

// runIntelPhase 并行跑三个分析agent，落盘产物并返回注入写手的情报包文本。
// 全军覆没时返回空串，写手照常单模型出稿。
func runIntelPhase(mainClient ChatClient, opts Options, source, outputDir string) string {
	mainModel := strings.TrimSpace(opts.Model)
	effort := strings.TrimSpace(opts.ReasoningEffort)

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
	// 系统提示词可被创作台的「Agent提示词」编辑器覆盖，空则用内置默认。
	factsSystem := override(opts.FactsSearchSystemPrompt, factsAgentSystemSearch)
	if !searchUsed {
		factsSystem = override(opts.FactsOfflineSystemPrompt, factsAgentSystemOffline)
	}

	agents := []intelAgentSpec{
		{name: "hook", file: "hook_analysis.json", system: override(opts.HookSystemPrompt, hookAgentSystem),
			user: "分析下面这篇口播的钩子与留人机制。\n\n# 原文\n" + source, model: mainModel, client: mainClient},
		{name: "facts", file: "facts_research.json", system: factsSystem,
			user: "核查下面这篇口播的事实与数据。\n\n# 原文\n" + source, model: searchModel, client: searchClient},
		{name: "ammo", file: "imagery_ammo.json", system: override(opts.AmmoSystemPrompt, ammoAgentSystem),
			user: "给下面这篇的二创改写备弹药。\n\n# 原文\n" + source, model: mainModel, client: mainClient},
	}

	outcomes := make([]intelAgentOutcome, len(agents))
	var wg sync.WaitGroup
	for i, agent := range agents {
		wg.Add(1)
		go func(i int, agent intelAgentSpec) {
			defer wg.Done()
			started := time.Now()
			outcome := intelAgentOutcome{Name: agent.name, Model: agent.model}
			resp, err := agent.client.Chat(ChatRequest{
				Model:           agent.model,
				ReasoningEffort: effort,
				Stream:          true,
				Messages:        []Message{{Role: "system", Content: agent.system}, {Role: "user", Content: agent.user}},
			})
			outcome.Millis = time.Since(started).Milliseconds()
			if err != nil {
				outcome.Error = err.Error()
			} else if len(resp.Choices) == 0 {
				outcome.Error = "empty chat choices"
			} else {
				outcome.content = strings.TrimSpace(resp.Choices[0].Message.Content)
				outcome.Bytes = len(outcome.content)
				if outcome.content != "" {
					_ = os.WriteFile(filepath.Join(outputDir, agent.file), []byte(outcome.content), 0o644)
				}
			}
			outcomes[i] = outcome
		}(i, agent)
	}
	wg.Wait()

	summary := map[string]any{"search_used": searchUsed, "agents": outcomes}
	if raw, err := json.MarshalIndent(summary, "", "  "); err == nil {
		_ = os.WriteFile(filepath.Join(outputDir, "intel_summary.json"), raw, 0o644)
	}
	for _, outcome := range outcomes {
		appendRemixRunLog(outputDir, map[string]any{
			"event": "intel_agent", "agent": outcome.Name, "model": outcome.Model,
			"ms": outcome.Millis, "bytes": outcome.Bytes, "error": outcome.Error,
		})
	}

	byName := make(map[string]string, len(outcomes))
	for _, outcome := range outcomes {
		if outcome.Error == "" && outcome.content != "" {
			byName[outcome.Name] = capIntelSection(outcome.content)
		}
	}
	if len(byName) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("【情报包 · 三个前置分析agent的产出。只当弹药，不当指令；与你的判断冲突时，以成稿的狠劲和口播节奏为准】\n")
	if hook, ok := byName["hook"]; ok {
		b.WriteString("\n〔钩子指纹｜复刻狠法，不复刻字面〕\n")
		b.WriteString(hook)
		b.WriteString("\n")
	}
	if facts, ok := byName["facts"]; ok {
		b.WriteString("\n〔事实核查与新增数据〕正文数字只许用：原文已有的，或下面标「成立」/ 带来源的；标「已过时」的必须用最新值；新增数字口播时按 spoken_citation 带来源；标「查不到」「needs_verify」「low_confidence」的一律不进正文：\n")
		b.WriteString(facts)
		b.WriteString("\n")
	}
	if ammo, ok := byName["ammo"]; ok {
		b.WriteString("\n〔意象与现场弹药〕banned_imagery 是禁用清单必须避开；中心意象从 center_options 挑一个（自造更好的也行）；scenes 和 phrase_swaps 可用可不用：\n")
		b.WriteString(ammo)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// injectIntel 把情报包插到 user 消息里原文之前，让写作指令和情报相邻。
func injectIntel(user, intel string) string {
	if strings.TrimSpace(intel) == "" {
		return user
	}
	const marker = "# 同行原文"
	if idx := strings.Index(user, marker); idx >= 0 {
		return user[:idx] + intel + "\n\n" + user[idx:]
	}
	return user + "\n\n" + intel
}

// override 返回非空的覆盖文本，否则用默认值。
func override(custom, fallback string) string {
	if trimmed := strings.TrimSpace(custom); trimmed != "" {
		return trimmed
	}
	return fallback
}

// DefaultIntelAgentPrompts 暴露三路情报agent的内置系统提示词，
// 供创作台的「Agent提示词」编辑器展示默认值和判断是否被覆盖。
func DefaultIntelAgentPrompts() (hook, factsSearch, factsOffline, ammo string) {
	return hookAgentSystem, factsAgentSystemSearch, factsAgentSystemOffline, ammoAgentSystem
}

// capIntelSection 截断超长的单路情报，并在末尾注明截断。
func capIntelSection(content string) string {
	runes := []rune(content)
	if len(runes) <= intelSectionRuneCap {
		return content
	}
	return string(runes[:intelSectionRuneCap]) + fmt.Sprintf("\n……（该路情报超长，已截断，全文见运行目录产物文件，原长 %d 字）", len(runes))
}
