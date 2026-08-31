package remixlab

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"video-production-console/internal/agentruntime/openaicompat"
	"video-production-console/internal/remixproducer"
)

// 工作流：把二创管线从代码里的固定链路改成可编辑的数据。节点带自己的
// 模型、通道和提示词配置；边决定数据流向——进写手的 agent 输出会按
// 节点顺序拼成【情报包】注入写手上下文。运行时把当时的工作流快照存进
// 实验，改工作流不影响历史运行的可追溯性。

const (
	WorkflowNodeInput     = "input"     // 对标原文（固定1个）
	WorkflowNodeAgent     = "agent"     // 情报agent（0-6个，可增删）
	WorkflowNodeWriter    = "writer"    // 写手（固定1个，提示词走提示词库）
	WorkflowNodeSelfcheck = "selfcheck" // 机械自检（固定1个，写手内置闸门，只读）
	WorkflowNodeReviewer  = "reviewer"  // 审稿终审（0-1个）
	WorkflowNodeOutput    = "output"    // 定稿与发布包（固定1个）
)

const maxWorkflowAgents = 6

var (
	ErrInvalidWorkflow = errors.New("invalid workflow")
	workflowIDPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)
)

type WorkflowNodeConfig struct {
	// Model 等留空时用运行档默认（模型配置里的槽1/全局二创设置）。
	Model           string `json:"model,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	// Channel "search" 走设置页的 Grok 搜索通道（联网核查用）；空走写手通道。
	Channel      string `json:"channel,omitempty"`
	SystemPrompt string `json:"system_prompt,omitempty"`
	// UserTemplate 支持 {{source}} 和 {{node:节点ID}} 占位；空则自动拼原文+上游输出。
	UserTemplate string `json:"user_template,omitempty"`
	// InjectTitle/InjectRule：该 agent 输出注入写手情报包时的小节标题和使用纪律。
	InjectTitle string `json:"inject_title,omitempty"`
	InjectRule  string `json:"inject_rule,omitempty"`
	// PromptID 写手节点用：提示词库条目；空=当前日产（active prompt）。
	PromptID string `json:"prompt_id,omitempty"`
	// 机械自检节点的阈值覆盖（0=用内置默认：连抄20%/硬30%、篇幅0.8/硬0.65、返工2轮）。
	OverlapMaxPct  int     `json:"overlap_max_pct,omitempty"`
	OverlapHardPct int     `json:"overlap_hard_pct,omitempty"`
	LenMinRatio    float64 `json:"len_min_ratio,omitempty"`
	LenHardRatio   float64 `json:"len_hard_ratio,omitempty"`
	MaxRounds      int     `json:"max_rounds,omitempty"`
}

type WorkflowNode struct {
	ID     string             `json:"id"`
	Type   string             `json:"type"`
	Title  string             `json:"title"`
	X      float64            `json:"x"`
	Y      float64            `json:"y"`
	Config WorkflowNodeConfig `json:"config"`
}

type Workflow struct {
	Version int            `json:"version"`
	Name    string         `json:"name"`
	Nodes   []WorkflowNode `json:"nodes"`
	Edges   [][2]string    `json:"edges"`
	// Production 是定稿之后生产段（建项目→口播→字幕→配音→混剪）的可编辑
	// 配置：字幕关键词默认关闭，口播/配音/混剪参数可改。随实验快照冻结。
	Production remixproducer.Config `json:"production,omitzero"`
}

// DefaultWorkflow 是现行管线的数据化表达：三路情报 → 写手（内置自检）→ 审稿 → 定稿。
// agent_prompts.json 里的历史覆盖会被吸收进来，用户已调过的提示词不丢。
func DefaultWorkflow(overrides AgentPrompts) Workflow {
	hook, factsSearch, _, ammo := openaicompat.DefaultIntelAgentPrompts()
	reviewer := openaicompat.DefaultReviewerPrompt()
	pickText := func(override, fallback string) string {
		if strings.TrimSpace(override) != "" {
			return override
		}
		return fallback
	}
	return Workflow{
		Version: 1,
		Name:    "默认二创工作流",
		Nodes: []WorkflowNode{
			{ID: "source", Type: WorkflowNodeInput, Title: "对标原文", X: 0, Y: 190},
			{ID: "hook", Type: WorkflowNodeAgent, Title: "钩子分析", X: 300, Y: 10, Config: WorkflowNodeConfig{
				SystemPrompt: pickText(overrides.HookSystem, hook),
				UserTemplate: "分析下面这篇口播的钩子与留人机制。\n\n# 原文\n{{source}}",
				InjectTitle:  "钩子指纹｜复刻狠法，不复刻字面",
			}},
			{ID: "facts", Type: WorkflowNodeAgent, Title: "事实核查", X: 300, Y: 190, Config: WorkflowNodeConfig{
				Channel:      "search",
				SystemPrompt: pickText(overrides.FactsSearchSystem, factsSearch),
				UserTemplate: "核查下面这篇口播的事实与数据。\n\n# 原文\n{{source}}",
				InjectTitle:  "事实核查与新增数据",
				InjectRule:   "正文数字只许用：原文已有的，或下面标「成立」/带来源的；标「已过时」的必须用最新值；新增数字口播时按 spoken_citation 带来源；标「查不到」「needs_verify」「low_confidence」的一律不进正文",
			}},
			{ID: "ammo", Type: WorkflowNodeAgent, Title: "弹药库", X: 300, Y: 370, Config: WorkflowNodeConfig{
				SystemPrompt: pickText(overrides.AmmoSystem, ammo),
				UserTemplate: "给下面这篇的二创改写备弹药。\n\n# 原文\n{{source}}",
				InjectTitle:  "意象与现场弹药",
				InjectRule:   "banned_imagery 是禁用清单必须避开；中心意象从 center_options 挑一个（自造更好的也行）；scenes 和 phrase_swaps 可用可不用",
			}},
			{ID: "writer", Type: WorkflowNodeWriter, Title: "写手", X: 600, Y: 190},
			{ID: "selfcheck", Type: WorkflowNodeSelfcheck, Title: "机械自检", X: 880, Y: 190},
			{ID: "review", Type: WorkflowNodeReviewer, Title: "审稿终审", X: 1160, Y: 190, Config: WorkflowNodeConfig{
				SystemPrompt: pickText(overrides.ReviewerSystem, reviewer),
			}},
			{ID: "final", Type: WorkflowNodeOutput, Title: "定稿与发布包", X: 1440, Y: 190},
		},
		Edges: [][2]string{
			{"source", "hook"}, {"source", "facts"}, {"source", "ammo"},
			{"hook", "writer"}, {"facts", "writer"}, {"ammo", "writer"},
			{"writer", "selfcheck"}, {"selfcheck", "review"}, {"review", "final"},
		},
		Production: remixproducer.Config{CaptionsDisabled: true},
	}
}

// ValidateWorkflow 校验节点数量、ID、连线拓扑。骨干链固定：
// writer → selfcheck →（reviewer →）output；agent 只能挂在 input/agent 之后、
// 汇入 writer 或别的 agent；整图必须无环。
func ValidateWorkflow(w Workflow) error {
	byID := make(map[string]WorkflowNode, len(w.Nodes))
	counts := map[string]int{}
	for _, node := range w.Nodes {
		id := strings.TrimSpace(node.ID)
		if !workflowIDPattern.MatchString(id) {
			return fmt.Errorf("%w: 节点ID %q 不合法（小写字母数字-_，32字以内）", ErrInvalidWorkflow, node.ID)
		}
		if _, dup := byID[id]; dup {
			return fmt.Errorf("%w: 节点ID %q 重复", ErrInvalidWorkflow, id)
		}
		switch node.Type {
		case WorkflowNodeInput, WorkflowNodeAgent, WorkflowNodeWriter, WorkflowNodeSelfcheck, WorkflowNodeReviewer, WorkflowNodeOutput:
		default:
			return fmt.Errorf("%w: 节点 %s 类型 %q 不认识", ErrInvalidWorkflow, id, node.Type)
		}
		if strings.TrimSpace(node.Title) == "" {
			return fmt.Errorf("%w: 节点 %s 缺标题", ErrInvalidWorkflow, id)
		}
		if node.Type == WorkflowNodeAgent && strings.TrimSpace(node.Config.SystemPrompt) == "" {
			return fmt.Errorf("%w: agent节点 %s 缺系统提示词", ErrInvalidWorkflow, id)
		}
		if node.Type == WorkflowNodeSelfcheck {
			cfg := node.Config
			if cfg.OverlapMaxPct != 0 && (cfg.OverlapMaxPct < 5 || cfg.OverlapMaxPct > 60) {
				return fmt.Errorf("%w: 连抄上限要在5%%到60%%之间（0=默认20%%）", ErrInvalidWorkflow)
			}
			if cfg.OverlapHardPct != 0 && (cfg.OverlapHardPct < 5 || cfg.OverlapHardPct > 80) {
				return fmt.Errorf("%w: 连抄硬上限要在5%%到80%%之间（0=默认30%%）", ErrInvalidWorkflow)
			}
			if cfg.OverlapMaxPct != 0 && cfg.OverlapHardPct != 0 && cfg.OverlapHardPct < cfg.OverlapMaxPct {
				return fmt.Errorf("%w: 连抄硬上限不能低于触发上限", ErrInvalidWorkflow)
			}
			if cfg.LenMinRatio != 0 && (cfg.LenMinRatio < 0.3 || cfg.LenMinRatio > 1.5) {
				return fmt.Errorf("%w: 篇幅下限要在0.3到1.5倍之间（0=默认0.8）", ErrInvalidWorkflow)
			}
			if cfg.LenHardRatio != 0 && (cfg.LenHardRatio < 0.2 || cfg.LenHardRatio > 1.5) {
				return fmt.Errorf("%w: 篇幅硬下限要在0.2到1.5倍之间（0=默认0.65）", ErrInvalidWorkflow)
			}
			if cfg.LenMinRatio != 0 && cfg.LenHardRatio != 0 && cfg.LenHardRatio > cfg.LenMinRatio {
				return fmt.Errorf("%w: 篇幅硬下限不能高于触发下限", ErrInvalidWorkflow)
			}
			if cfg.MaxRounds != 0 && (cfg.MaxRounds < 1 || cfg.MaxRounds > 4) {
				return fmt.Errorf("%w: 返工轮数要在1到4之间（0=默认2）", ErrInvalidWorkflow)
			}
		}
		byID[id] = node
		counts[node.Type]++
	}
	if counts[WorkflowNodeInput] != 1 || counts[WorkflowNodeWriter] != 1 ||
		counts[WorkflowNodeSelfcheck] != 1 || counts[WorkflowNodeOutput] != 1 {
		return fmt.Errorf("%w: 原文/写手/机械自检/定稿各需恰好1个", ErrInvalidWorkflow)
	}
	if counts[WorkflowNodeReviewer] > 1 {
		return fmt.Errorf("%w: 审稿节点最多1个", ErrInvalidWorkflow)
	}
	if counts[WorkflowNodeAgent] > maxWorkflowAgents {
		return fmt.Errorf("%w: agent节点最多%d个", ErrInvalidWorkflow, maxWorkflowAgents)
	}

	incoming := map[string][]string{}
	outgoing := map[string][]string{}
	seenEdge := map[string]bool{}
	for _, edge := range w.Edges {
		from, to := edge[0], edge[1]
		if _, ok := byID[from]; !ok {
			return fmt.Errorf("%w: 连线起点 %q 不存在", ErrInvalidWorkflow, from)
		}
		if _, ok := byID[to]; !ok {
			return fmt.Errorf("%w: 连线终点 %q 不存在", ErrInvalidWorkflow, to)
		}
		if from == to {
			return fmt.Errorf("%w: 节点 %q 不能连自己", ErrInvalidWorkflow, from)
		}
		key := from + "->" + to
		if seenEdge[key] {
			return fmt.Errorf("%w: 连线 %s 重复", ErrInvalidWorkflow, key)
		}
		seenEdge[key] = true
		incoming[to] = append(incoming[to], from)
		outgoing[from] = append(outgoing[from], to)
	}

	typeOf := func(id string) string { return byID[id].Type }
	for id, node := range byID {
		switch node.Type {
		case WorkflowNodeInput:
			if len(incoming[id]) > 0 {
				return fmt.Errorf("%w: 原文节点不能有入线", ErrInvalidWorkflow)
			}
		case WorkflowNodeAgent:
			for _, from := range incoming[id] {
				if t := typeOf(from); t != WorkflowNodeInput && t != WorkflowNodeAgent {
					return fmt.Errorf("%w: agent节点 %s 只能接原文或别的agent，不能接 %s", ErrInvalidWorkflow, id, from)
				}
			}
		case WorkflowNodeWriter:
			for _, from := range incoming[id] {
				if t := typeOf(from); t != WorkflowNodeInput && t != WorkflowNodeAgent {
					return fmt.Errorf("%w: 写手只能接原文和agent，不能接 %s", ErrInvalidWorkflow, from)
				}
			}
		case WorkflowNodeSelfcheck:
			if len(incoming[id]) != 1 || typeOf(incoming[id][0]) != WorkflowNodeWriter {
				return fmt.Errorf("%w: 机械自检必须且只能接在写手之后", ErrInvalidWorkflow)
			}
		case WorkflowNodeReviewer:
			if len(incoming[id]) != 1 || typeOf(incoming[id][0]) != WorkflowNodeSelfcheck {
				return fmt.Errorf("%w: 审稿必须接在机械自检之后", ErrInvalidWorkflow)
			}
		case WorkflowNodeOutput:
			if len(incoming[id]) != 1 {
				return fmt.Errorf("%w: 定稿必须且只有一条入线", ErrInvalidWorkflow)
			}
			fromType := typeOf(incoming[id][0])
			wantFrom := WorkflowNodeSelfcheck
			if counts[WorkflowNodeReviewer] == 1 {
				wantFrom = WorkflowNodeReviewer
			}
			if fromType != wantFrom {
				return fmt.Errorf("%w: 定稿的上游应是%s", ErrInvalidWorkflow, map[string]string{WorkflowNodeSelfcheck: "机械自检", WorkflowNodeReviewer: "审稿"}[wantFrom])
			}
			if len(outgoing[id]) > 0 {
				return fmt.Errorf("%w: 定稿节点不能有出线", ErrInvalidWorkflow)
			}
		}
	}

	// 环检测（Kahn）
	degree := map[string]int{}
	for id := range byID {
		degree[id] = len(incoming[id])
	}
	queue := make([]string, 0, len(byID))
	for id, d := range degree {
		if d == 0 {
			queue = append(queue, id)
		}
	}
	visited := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		visited++
		for _, next := range outgoing[id] {
			degree[next]--
			if degree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	if visited != len(byID) {
		return fmt.Errorf("%w: 连线成环了", ErrInvalidWorkflow)
	}
	return nil
}

func (s Store) workflowPath() string {
	return filepath.Join(filepath.Clean(s.DataRoot), "remix_lab", "workflow.json")
}

func (s Store) accountWorkflowPath(accountID string) string {
	return filepath.Join(filepath.Clean(s.DataRoot), "remix_lab", "workflows", accountID+".json")
}

func normalizeWorkflowAccountID(accountID string) (string, error) {
	id := strings.TrimSpace(accountID)
	if id == "" {
		return "", nil
	}
	parsed, err := uuid.Parse(id)
	if err != nil {
		return "", fmt.Errorf("%w: 账号ID不合法", ErrInvalidWorkflow)
	}
	return parsed.String(), nil
}

// LoadWorkflow 读全局默认工作流；文件不存在时返回内置默认图（吸收 agent_prompts 覆盖）。
func (s Store) LoadWorkflow() (Workflow, error) {
	return s.loadWorkflowFile(s.workflowPath())
}

// LoadWorkflowForAccount 读该账号最新一版工作流。还没存过就回落全局默认（不落盘）。
func (s Store) LoadWorkflowForAccount(accountID string) (Workflow, error) {
	id, err := normalizeWorkflowAccountID(accountID)
	if err != nil {
		return Workflow{}, err
	}
	if id == "" {
		return s.LoadWorkflow()
	}
	raw, err := os.ReadFile(s.accountWorkflowPath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return s.LoadWorkflow()
		}
		return Workflow{}, fmt.Errorf("read account workflow: %w", err)
	}
	return decodeWorkflow(raw)
}

func (s Store) loadWorkflowFile(path string) (Workflow, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			overrides, _ := s.LoadAgentPrompts()
			return DefaultWorkflow(overrides), nil
		}
		return Workflow{}, fmt.Errorf("read workflow: %w", err)
	}
	return decodeWorkflow(raw)
}

func decodeWorkflow(raw []byte) (Workflow, error) {
	var w Workflow
	if err := json.Unmarshal(raw, &w); err != nil {
		return Workflow{}, fmt.Errorf("decode workflow: %w", err)
	}
	w.Production = remixproducer.ParseConfig(string(raw))
	return w, nil
}

func (s Store) SaveWorkflow(w Workflow) error {
	return s.writeWorkflowFile(s.workflowPath(), filepath.Join(filepath.Clean(s.DataRoot), "remix_lab"), w)
}

func (s Store) SaveWorkflowForAccount(accountID string, w Workflow) error {
	id, err := normalizeWorkflowAccountID(accountID)
	if err != nil {
		return err
	}
	if id == "" {
		return s.SaveWorkflow(w)
	}
	dir := filepath.Join(filepath.Clean(s.DataRoot), "remix_lab", "workflows")
	return s.writeWorkflowFile(s.accountWorkflowPath(id), dir, w)
}

func (s Store) writeWorkflowFile(path, dir string, w Workflow) error {
	if err := ValidateWorkflow(w); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create remix_lab dir: %w", err)
	}
	raw, err := json.MarshalIndent(w, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("write workflow: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("publish workflow: %w", err)
	}
	return nil
}

// Workflow / SaveWorkflowDefinition 是服务层入口，给 HTTP 和智能体提案用。
func (s *Service) Workflow() (Workflow, error) {
	return s.WorkflowForAccount("")
}

func (s *Service) WorkflowForAccount(accountID string) (Workflow, error) {
	return (Store{DataRoot: s.dataRoot}).LoadWorkflowForAccount(accountID)
}

func (s *Service) SaveWorkflowDefinition(w Workflow) (Workflow, error) {
	return s.SaveWorkflowDefinitionForAccount("", w)
}

func (s *Service) SaveWorkflowDefinitionForAccount(accountID string, w Workflow) (Workflow, error) {
	if w.Name == "" {
		w.Name = "默认二创工作流"
	}
	if w.Version == 0 {
		w.Version = 1
	}
	if err := (Store{DataRoot: s.dataRoot}).SaveWorkflowForAccount(accountID, w); err != nil {
		return Workflow{}, err
	}
	return w, nil
}

// workflowReviewer 返回快照里的审稿节点（没有则 nil）。
func workflowReviewer(w Workflow) *WorkflowNode {
	for i := range w.Nodes {
		if w.Nodes[i].Type == WorkflowNodeReviewer {
			return &w.Nodes[i]
		}
	}
	return nil
}

// workflowWriter 返回写手节点。
func workflowWriter(w Workflow) *WorkflowNode {
	for i := range w.Nodes {
		if w.Nodes[i].Type == WorkflowNodeWriter {
			return &w.Nodes[i]
		}
	}
	return nil
}

func parseWorkflowJSON(raw string) (Workflow, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Workflow{}, false
	}
	var w Workflow
	if err := json.Unmarshal([]byte(raw), &w); err != nil {
		return Workflow{}, false
	}
	w.Production = remixproducer.ParseConfig(raw)
	return w, len(w.Nodes) > 0
}
