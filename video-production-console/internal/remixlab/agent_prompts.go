package remixlab

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"video-production-console/internal/agentruntime/openaicompat"
)

// AgentPrompts 是四路agent（钩子/事实核查联网/事实核查离线/弹药/审稿）的
// 系统提示词覆盖。空字段表示跟随内置默认——默认升级时自动生效。
type AgentPrompts struct {
	HookSystem         string `json:"hook_system"`
	FactsSearchSystem  string `json:"facts_search_system"`
	FactsOfflineSystem string `json:"facts_offline_system"`
	AmmoSystem         string `json:"ammo_system"`
	ReviewerSystem     string `json:"reviewer_system"`
}

// AgentPromptsView 给前端编辑器：effective 是当前生效文本（覆盖或默认），
// defaults 用于「恢复默认」，overridden 标记哪几路被自定义过。
type AgentPromptsView struct {
	Prompts    AgentPrompts    `json:"prompts"`
	Defaults   AgentPrompts    `json:"defaults"`
	Overridden map[string]bool `json:"overridden"`
}

var ErrInvalidAgentPrompts = errors.New("agent prompt too long")

const agentPromptMaxRunes = 20000

func defaultAgentPrompts() AgentPrompts {
	hook, factsSearch, factsOffline, ammo := openaicompat.DefaultIntelAgentPrompts()
	return AgentPrompts{
		HookSystem:         hook,
		FactsSearchSystem:  factsSearch,
		FactsOfflineSystem: factsOffline,
		AmmoSystem:         ammo,
		ReviewerSystem:     openaicompat.DefaultReviewerPrompt(),
	}
}

func (s Store) agentPromptsPath() string {
	return filepath.Join(filepath.Clean(s.DataRoot), "remix_lab", "agent_prompts.json")
}

// LoadAgentPrompts 读覆盖文件；不存在时返回全空（即全部跟随默认）。
func (s Store) LoadAgentPrompts() (AgentPrompts, error) {
	raw, err := os.ReadFile(s.agentPromptsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return AgentPrompts{}, nil
		}
		return AgentPrompts{}, fmt.Errorf("read agent prompts: %w", err)
	}
	var prompts AgentPrompts
	if err := json.Unmarshal(raw, &prompts); err != nil {
		return AgentPrompts{}, fmt.Errorf("decode agent prompts: %w", err)
	}
	return prompts, nil
}

func (s Store) saveAgentPrompts(prompts AgentPrompts) error {
	dir := filepath.Join(filepath.Clean(s.DataRoot), "remix_lab")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create remix_lab dir: %w", err)
	}
	raw, err := json.MarshalIndent(prompts, "", "  ")
	if err != nil {
		return err
	}
	path := s.agentPromptsPath()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("write agent prompts: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("publish agent prompts: %w", err)
	}
	return nil
}

// AgentPromptsView 返回编辑器视图。
func (s *Service) AgentPromptsView() (AgentPromptsView, error) {
	overrides, err := (Store{DataRoot: s.dataRoot}).LoadAgentPrompts()
	if err != nil {
		return AgentPromptsView{}, err
	}
	defaults := defaultAgentPrompts()
	effective := AgentPrompts{
		HookSystem:         pick(overrides.HookSystem, defaults.HookSystem),
		FactsSearchSystem:  pick(overrides.FactsSearchSystem, defaults.FactsSearchSystem),
		FactsOfflineSystem: pick(overrides.FactsOfflineSystem, defaults.FactsOfflineSystem),
		AmmoSystem:         pick(overrides.AmmoSystem, defaults.AmmoSystem),
		ReviewerSystem:     pick(overrides.ReviewerSystem, defaults.ReviewerSystem),
	}
	return AgentPromptsView{
		Prompts:  effective,
		Defaults: defaults,
		Overridden: map[string]bool{
			"hook_system":          strings.TrimSpace(overrides.HookSystem) != "",
			"facts_search_system":  strings.TrimSpace(overrides.FactsSearchSystem) != "",
			"facts_offline_system": strings.TrimSpace(overrides.FactsOfflineSystem) != "",
			"ammo_system":          strings.TrimSpace(overrides.AmmoSystem) != "",
			"reviewer_system":      strings.TrimSpace(overrides.ReviewerSystem) != "",
		},
	}, nil
}

// SaveAgentPrompts 保存编辑器提交的全量文本：与默认一致的字段落成空串
// （继续跟随默认），改过的字段存覆盖文本。
func (s *Service) SaveAgentPrompts(in AgentPrompts) (AgentPromptsView, error) {
	defaults := defaultAgentPrompts()
	normalized := AgentPrompts{
		HookSystem:         collapseDefault(in.HookSystem, defaults.HookSystem),
		FactsSearchSystem:  collapseDefault(in.FactsSearchSystem, defaults.FactsSearchSystem),
		FactsOfflineSystem: collapseDefault(in.FactsOfflineSystem, defaults.FactsOfflineSystem),
		AmmoSystem:         collapseDefault(in.AmmoSystem, defaults.AmmoSystem),
		ReviewerSystem:     collapseDefault(in.ReviewerSystem, defaults.ReviewerSystem),
	}
	for _, text := range []string{
		normalized.HookSystem, normalized.FactsSearchSystem, normalized.FactsOfflineSystem,
		normalized.AmmoSystem, normalized.ReviewerSystem,
	} {
		if utf8.RuneCountInString(text) > agentPromptMaxRunes {
			return AgentPromptsView{}, ErrInvalidAgentPrompts
		}
	}
	if err := (Store{DataRoot: s.dataRoot}).saveAgentPrompts(normalized); err != nil {
		return AgentPromptsView{}, err
	}
	return s.AgentPromptsView()
}

// applyAgentPromptOverrides 把覆盖文本填进一次运行的 Options。
func (s *Service) applyAgentPromptOverrides(opts *openaicompat.Options) {
	overrides, err := (Store{DataRoot: s.dataRoot}).LoadAgentPrompts()
	if err != nil {
		return // 读不到就用内置默认，不拦运行
	}
	opts.HookSystemPrompt = overrides.HookSystem
	opts.FactsSearchSystemPrompt = overrides.FactsSearchSystem
	opts.FactsOfflineSystemPrompt = overrides.FactsOfflineSystem
	opts.AmmoSystemPrompt = overrides.AmmoSystem
	opts.ReviewerSystemPrompt = overrides.ReviewerSystem
}

func pick(overrideText, fallback string) string {
	if strings.TrimSpace(overrideText) != "" {
		return overrideText
	}
	return fallback
}

// collapseDefault 提交文本与默认一致（或为空）时返回空串，表示跟随默认。
func collapseDefault(text, fallback string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || trimmed == strings.TrimSpace(fallback) {
		return ""
	}
	return trimmed
}
