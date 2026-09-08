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
	WorkflowNodeSelfcheck = "selfcheck" // 已停用，仅用于旧快照解码和桥接
	WorkflowNodeReviewer  = "reviewer"  // 审稿终审（0-1个）
	WorkflowNodeOutput    = "output"    // 定稿与发布包（固定1个）
)

const maxWorkflowAgents = 6

var (
	ErrInvalidWorkflow = errors.New("invalid workflow")
	workflowIDPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)
)

type WorkflowNodeConfig struct {
	Role string `json:"role,omitempty"`
	// Model 等留空时用运行档默认（模型配置里的槽1/全局二创设置）。
	Model           string `json:"model,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	// Writer inherits its preset when empty; other nodes control Fast independently.
	ServiceTier string `json:"service_tier,omitempty"`
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
	// 仅供解码旧快照，机械审查已停用，不再读取这些阈值。
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
	EditorialRules *string        `json:"editorial_rules,omitempty"`
	Version        int            `json:"version"`
	Name           string         `json:"name"`
	Nodes          []WorkflowNode `json:"nodes"`
	Edges          [][2]string    `json:"edges"`
	// Production 是定稿之后生产段（建项目→口播→字幕→配音→混剪）的可编辑
	// 配置：字幕关键词默认关闭，口播/配音/混剪参数可改。随实验快照冻结。
	Production remixproducer.Config `json:"production,omitzero"`
}

// DefaultWorkflow：原文 → 二创策划 → 写手 → 审稿 → 人工定稿。
// 策划节点 ID 固定为 hook：引擎按此文件名读取写作计划并交给审稿 {{writing_plan}}。
// agent_prompts.json 里的历史覆盖会被吸收进来，用户已调过的提示词不丢。
func DefaultWorkflow(overrides AgentPrompts) Workflow {
	reviewer := openaicompat.DefaultReviewerPrompt()
	pickText := func(override, fallback string) string {
		if strings.TrimSpace(override) != "" {
			return override
		}
		return fallback
	}
	// 策划节点模型留空时回落到写手模型；推理强度用 medium 已够出计划，比写手省时。
	return Workflow{
		Version: 1,
		Name:    "默认二创工作流",
		Nodes: []WorkflowNode{
			{ID: "source", Type: WorkflowNodeInput, Title: "对标原文", X: 0, Y: 190},
			{ID: openaicompat.PlannerNodeID, Type: WorkflowNodeAgent, Title: openaicompat.PlannerNodeTitle, X: 320, Y: 190, Config: WorkflowNodeConfig{
				ReasoningEffort: "medium",
				SystemPrompt:    pickText(overrides.HookSystem, openaicompat.PlannerSystemPrompt),
				UserTemplate:    openaicompat.PlannerUserTemplate,
				InjectTitle:     openaicompat.PlannerInjectTitle,
				InjectRule:      openaicompat.PlannerInjectRule,
			}},
			{ID: "writer", Type: WorkflowNodeWriter, Title: "写手", X: 640, Y: 190},
			{ID: "review", Type: WorkflowNodeReviewer, Title: "审稿终审", X: 960, Y: 190, Config: WorkflowNodeConfig{
				SystemPrompt: pickText(overrides.ReviewerSystem, reviewer),
				UserTemplate: openaicompat.ReviewerUserTemplate,
			}},
			{ID: "final", Type: WorkflowNodeOutput, Title: "定稿与发布包", X: 1280, Y: 190},
		},
		Edges: [][2]string{
			{"source", openaicompat.PlannerNodeID}, {openaicompat.PlannerNodeID, "writer"},
			{"source", "writer"},
			{"writer", "review"}, {"review", "final"},
		},
		Production: remixproducer.Config{CaptionsDisabled: true},
	}
}

// ValidateWorkflow 校验节点数量、ID、连线拓扑。骨干链固定：
// writer →（reviewer →）output；旧快照可含已停用的 selfcheck，读取时桥接。
// agent 只能挂在 input/agent 之后、
// 汇入 writer 或别的 agent；整图必须无环。
func ValidateWorkflow(w Workflow) error {
	if err := validateEditablePrompts(w); err != nil {
		return err
	}
	byID := make(map[string]WorkflowNode, len(w.Nodes))
	counts := map[string]int{}
	for _, node := range w.Nodes {
		if node.Config.Role != "" && (node.Config.Role != openaicompat.ReferenceDraftRole || node.Type != WorkflowNodeAgent) {
			return fmt.Errorf("%w: 节点%s参考角色不合法", ErrInvalidWorkflow, node.ID)
		}
		if _, err := openaicompat.NormalizeServiceTier(node.Config.ServiceTier); err != nil {
			return fmt.Errorf("%w: %s: %v", ErrInvalidWorkflow, node.ID, err)
		}
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
		if node.Type == WorkflowNodeAgent && node.Config.Role != openaicompat.ReferenceDraftRole && strings.TrimSpace(node.Config.SystemPrompt) == "" {
			return fmt.Errorf("%w: agent节点 %s 缺系统提示词", ErrInvalidWorkflow, id)
		}
		byID[id] = node
		counts[node.Type]++
	}
	if counts[WorkflowNodeInput] != 1 || counts[WorkflowNodeWriter] != 1 ||
		counts[WorkflowNodeSelfcheck] > 1 || counts[WorkflowNodeOutput] != 1 {
		return fmt.Errorf("%w: 原文/写手/定稿各需恰好1个，旧机械节点最多1个", ErrInvalidWorkflow)
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
			if node.Config.Role == openaicompat.ReferenceDraftRole {
				choice := RerunModel{node.Config.Model, node.Config.ReasoningEffort, node.Config.ServiceTier}
				if err := validateRerunModel(&choice); err != nil {
					return fmt.Errorf("%w: 参考模型%s配置不合法", ErrInvalidWorkflow, node.ID)
				}
				if len(incoming[id]) != 1 || typeOf(incoming[id][0]) != WorkflowNodeInput || len(outgoing[id]) != 1 || typeOf(outgoing[id][0]) != WorkflowNodeWriter {
					return fmt.Errorf("%w: 参考模型%s须独立连接原文与写手", ErrInvalidWorkflow, node.ID)
				}
			}
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
			wantFrom := WorkflowNodeWriter
			if counts[WorkflowNodeSelfcheck] == 1 {
				wantFrom = WorkflowNodeSelfcheck
			}
			if len(incoming[id]) != 1 || typeOf(incoming[id][0]) != wantFrom {
				return fmt.Errorf("%w: 审稿必须接在写手之后", ErrInvalidWorkflow)
			}
		case WorkflowNodeOutput:
			if len(incoming[id]) != 1 {
				return fmt.Errorf("%w: 定稿必须且只有一条入线", ErrInvalidWorkflow)
			}
			fromType := typeOf(incoming[id][0])
			wantFrom := WorkflowNodeWriter
			if counts[WorkflowNodeSelfcheck] == 1 {
				wantFrom = WorkflowNodeSelfcheck
			}
			if counts[WorkflowNodeReviewer] == 1 {
				wantFrom = WorkflowNodeReviewer
			}
			if fromType != wantFrom {
				return fmt.Errorf("%w: 定稿的上游应是%s", ErrInvalidWorkflow, wantFrom)
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
	return withoutMechanicalReview(w), nil
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
	w = withReferenceDefaults(w)
	if err := ValidateWorkflow(w); err != nil {
		return err
	}
	w = withoutMechanicalReview(w)
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
	w = withReferenceDefaults(w)
	if w.Name == "" {
		w.Name = "默认二创工作流"
	}
	if w.Version == 0 {
		w.Version = 1
	}
	if err := (Store{DataRoot: s.dataRoot}).SaveWorkflowForAccount(accountID, w); err != nil {
		return Workflow{}, err
	}
	return withoutMechanicalReview(w), nil
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
	w = withoutMechanicalReview(w)
	return w, len(w.Nodes) > 0
}

// withoutMechanicalReview 只构造运行时视图；不覆写历史快照，不改变模型或生产设置。
func withoutMechanicalReview(w Workflow) Workflow {
	for _, retired := range w.Nodes {
		if retired.Type != WorkflowNodeSelfcheck {
			continue
		}
		nodes := make([]WorkflowNode, 0, len(w.Nodes)-1)
		for _, n := range w.Nodes {
			if n.ID != retired.ID {
				nodes = append(nodes, n)
			}
		}
		var incoming, outgoing []string
		edges := make([][2]string, 0, len(w.Edges))
		for _, e := range w.Edges {
			if e[1] == retired.ID {
				incoming = append(incoming, e[0])
			}
			if e[0] == retired.ID {
				outgoing = append(outgoing, e[1])
			}
			if e[0] != retired.ID && e[1] != retired.ID {
				edges = append(edges, e)
			}
		}
		for _, from := range incoming {
			for _, to := range outgoing {
				e := [2]string{from, to}
				exists := false
				for _, old := range edges {
					if old == e {
						exists = true
						break
					}
				}
				if !exists {
					edges = append(edges, e)
				}
			}
		}
		w.Nodes, w.Edges = nodes, edges
	}
	return w
}
