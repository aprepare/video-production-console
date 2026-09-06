package remixlab

import (
	"strings"
	"testing"

	"video-production-console/internal/agentruntime/openaicompat"
	"video-production-console/internal/store"
)

func newAgentPromptsService(t *testing.T) *Service {
	t.Helper()
	db, err := store.Open(t.TempDir() + "/console.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return NewService(store.NewRemixLabRepository(db), stubRuntime{view: RuntimeView{RemixAPIKey: "sk-runtime"}}, remixFakeProtector{}, t.TempDir(), fastStubRunner, nil, nil)
}

func TestAgentPromptsDefaultViewAndOverrideRoundTrip(t *testing.T) {
	svc := newAgentPromptsService(t)

	view, err := svc.AgentPromptsView()
	if err != nil {
		t.Fatal(err)
	}
	if view.Overridden["hook_system"] || view.Overridden["reviewer_system"] {
		t.Fatalf("fresh store must not be overridden: %+v", view.Overridden)
	}
	if !strings.Contains(view.Prompts.HookSystem, "二创策划") {
		t.Fatalf("effective hook prompt should be builtin default, got: %.40s", view.Prompts.HookSystem)
	}
	if view.Prompts.ReviewerSystem != view.Defaults.ReviewerSystem {
		t.Fatal("effective reviewer prompt should equal default before override")
	}

	// 覆盖钩子提示词，其余提交默认文本（应回落为跟随默认）。
	in := view.Prompts
	in.HookSystem = "自定义钩子分析规则：只看前三句。"
	saved, err := svc.SaveAgentPrompts(in)
	if err != nil {
		t.Fatal(err)
	}
	if !saved.Overridden["hook_system"] {
		t.Fatal("hook should be overridden after save")
	}
	if saved.Overridden["facts_search_system"] || saved.Overridden["ammo_system"] || saved.Overridden["reviewer_system"] {
		t.Fatalf("default-equal fields must collapse to non-overridden: %+v", saved.Overridden)
	}
	if saved.Prompts.HookSystem != "自定义钩子分析规则：只看前三句。" {
		t.Fatalf("effective hook = %q", saved.Prompts.HookSystem)
	}

	// 覆盖文本要真的进运行 Options。
	var opts openaicompat.Options
	svc.applyAgentPromptOverrides(&opts)
	if opts.HookSystemPrompt != "自定义钩子分析规则：只看前三句。" {
		t.Fatalf("options hook = %q", opts.HookSystemPrompt)
	}
	if opts.ReviewerSystemPrompt != "" {
		t.Fatalf("non-overridden reviewer must stay empty (follow default), got %q", opts.ReviewerSystemPrompt)
	}

	// 把钩子改回默认文本 = 撤销覆盖。
	in = saved.Prompts
	in.HookSystem = saved.Defaults.HookSystem
	reset, err := svc.SaveAgentPrompts(in)
	if err != nil {
		t.Fatal(err)
	}
	if reset.Overridden["hook_system"] {
		t.Fatal("submitting default text must clear the override")
	}
}

func TestAgentPromptsRejectOversize(t *testing.T) {
	svc := newAgentPromptsService(t)
	in := AgentPrompts{ReviewerSystem: strings.Repeat("规", 20001)}
	if _, err := svc.SaveAgentPrompts(in); err != ErrInvalidAgentPrompts {
		t.Fatalf("want ErrInvalidAgentPrompts, got %v", err)
	}
}
