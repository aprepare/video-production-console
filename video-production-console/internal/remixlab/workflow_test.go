package remixlab

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"video-production-console/internal/agentruntime/openaicompat"
	"video-production-console/internal/remixproducer"
	"video-production-console/internal/store"
)

func TestDefaultWorkflowIsValidAndAbsorbsOverrides(t *testing.T) {
	wf := DefaultWorkflow(AgentPrompts{HookSystem: "自定义钩子规则"})
	if err := ValidateWorkflow(wf); err != nil {
		t.Fatal(err)
	}
	var hook *WorkflowNode
	for i := range wf.Nodes {
		if wf.Nodes[i].ID == "hook" {
			hook = &wf.Nodes[i]
		}
	}
	if hook == nil || hook.Config.SystemPrompt != "自定义钩子规则" {
		t.Fatalf("hook override not absorbed: %+v", hook)
	}
	if !wf.Production.CaptionsDisabled {
		t.Fatal("default workflow should hide the captions node")
	}
}

func TestValidateWorkflowRejectsBrokenTopology(t *testing.T) {
	base := DefaultWorkflow(AgentPrompts{})

	noWriter := base
	noWriter.Nodes = nil
	for _, node := range base.Nodes {
		if node.Type != WorkflowNodeWriter {
			noWriter.Nodes = append(noWriter.Nodes, node)
		}
	}
	if err := ValidateWorkflow(noWriter); !errors.Is(err, ErrInvalidWorkflow) {
		t.Fatalf("missing writer must fail, got %v", err)
	}

	cycle := DefaultWorkflow(AgentPrompts{})
	cycle.Edges = append(cycle.Edges, [2]string{"ammo", "hook"}, [2]string{"hook", "ammo"})
	if err := ValidateWorkflow(cycle); !errors.Is(err, ErrInvalidWorkflow) {
		t.Fatalf("cycle must fail, got %v", err)
	}

	badEdge := DefaultWorkflow(AgentPrompts{})
	badEdge.Edges = append(badEdge.Edges, [2]string{"review", "hook"})
	if err := ValidateWorkflow(badEdge); !errors.Is(err, ErrInvalidWorkflow) {
		t.Fatalf("reviewer→agent must fail, got %v", err)
	}

	// agent 链式依赖是合法的
	chain := DefaultWorkflow(AgentPrompts{})
	chain.Edges = append(chain.Edges, [2]string{"hook", "ammo"})
	if err := ValidateWorkflow(chain); err != nil {
		t.Fatalf("agent chain should be valid: %v", err)
	}
}

func TestWorkflowStoreRoundTrip(t *testing.T) {
	dataRoot := t.TempDir()
	st := Store{DataRoot: dataRoot}

	loaded, err := st.LoadWorkflow()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Nodes) == 0 || loaded.Name == "" {
		t.Fatalf("missing default workflow: %+v", loaded)
	}

	loaded.Nodes = append(loaded.Nodes, WorkflowNode{
		ID: "titles", Type: WorkflowNodeAgent, Title: "标题专家", X: 300, Y: 550,
		Config: WorkflowNodeConfig{SystemPrompt: "你是标题专家。", UserTemplate: "给这篇出标题弹药：{{source}}"},
	})
	loaded.Edges = append(loaded.Edges, [2]string{"source", "titles"}, [2]string{"titles", "writer"})
	if err := st.SaveWorkflow(loaded); err != nil {
		t.Fatal(err)
	}
	again, err := st.LoadWorkflow()
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Nodes) != len(loaded.Nodes) {
		t.Fatalf("nodes = %d, want %d", len(again.Nodes), len(loaded.Nodes))
	}
	if _, err := os.Stat(filepath.Join(dataRoot, "remix_lab", "workflow.json")); err != nil {
		t.Fatal(err)
	}
}

func TestAccountWorkflowFallsBackThenPersistsLatest(t *testing.T) {
	dataRoot := t.TempDir()
	st := Store{DataRoot: dataRoot}
	accountID := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"

	first, err := st.LoadWorkflowForAccount(accountID)
	if err != nil {
		t.Fatal(err)
	}
	if first.Name == "" {
		t.Fatal("account without a file should fall back to the default workflow")
	}

	first.Name = "观局思考工作流"
	if err := st.SaveWorkflowForAccount(accountID, first); err != nil {
		t.Fatal(err)
	}
	again, err := st.LoadWorkflowForAccount(accountID)
	if err != nil {
		t.Fatal(err)
	}
	if again.Name != "观局思考工作流" {
		t.Fatalf("account latest = %q", again.Name)
	}
	if _, err := os.Stat(filepath.Join(dataRoot, "remix_lab", "workflows", accountID+".json")); err != nil {
		t.Fatal(err)
	}

	global, err := st.LoadWorkflow()
	if err != nil {
		t.Fatal(err)
	}
	if global.Name == "观局思考工作流" {
		t.Fatal("saving an account workflow must not overwrite the global default")
	}

	if _, err := st.LoadWorkflowForAccount("not-a-uuid"); !errors.Is(err, ErrInvalidWorkflow) {
		t.Fatalf("invalid account id: %v", err)
	}
}

func TestCreateWorkflowExperimentSnapshotsAndRuns(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo := store.NewRemixLabRepository(db)

	var gotWorkflowJSON string
	runner := func(_ context.Context, opts openaicompat.Options) error {
		gotWorkflowJSON = opts.WorkflowJSON
		dir := filepath.Dir(opts.OutputLastMessage)
		return os.WriteFile(filepath.Join(dir, "continuous_script.txt"), []byte("工作流成稿"), 0o644)
	}
	svc := NewService(repo, stubRuntime{view: RuntimeView{
		RemixBaseURL: "http://x/v1", RemixModel: "m-default", RemixAPIKey: "sk-runtime",
		GrokBaseURL: "http://g/v1", GrokAPIKey: "gk", GrokModel: "grok-4.6-fast",
	}}, remixFakeProtector{}, t.TempDir(), runner, nil, nil)

	exp, err := svc.CreateWorkflowExperiment(t.Context(), "工作流实验原文一二三四五", 2, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !exp.Workflow || len(exp.Runs) != 2 {
		t.Fatalf("exp: workflow=%v runs=%d", exp.Workflow, len(exp.Runs))
	}
	done := waitExperimentTerminal(t, svc, exp.ID)
	if done.Status != "completed" {
		t.Fatalf("status=%s (%+v)", done.Status, done.Runs)
	}
	if !done.Workflow {
		t.Fatal("experiment view must carry workflow flag")
	}
	var snapshot Workflow
	if err := json.Unmarshal([]byte(gotWorkflowJSON), &snapshot); err != nil {
		t.Fatalf("runner must receive workflow snapshot: %v (raw=%q)", err, gotWorkflowJSON[:40])
	}
	if len(snapshot.Nodes) == 0 {
		t.Fatal("snapshot has no nodes")
	}

	// stages 视图按快照组装：节点带坐标、边来自快照
	view, err := svc.RunStages(t.Context(), done.Runs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Pipeline != "workflow" {
		t.Fatalf("pipeline=%s", view.Pipeline)
	}
	agents := 0
	byID := map[string]RunStageView{}
	for _, stage := range view.Stages {
		byID[stage.ID] = stage
		if stage.Kind == "agent" && stage.ID != "review" && stage.ID != "writer" {
			agents++
		}
		if stage.ID == "hook" && (stage.X == 0 && stage.Y == 0) {
			t.Fatalf("hook stage should carry snapshot position: %+v", stage)
		}
	}
	if agents != 3 {
		t.Fatalf("agent stages = %d, want 3", agents)
	}
	// 二创段 9 条边 + 生产段 5 条（定稿→闸门→建项目→口播→配音→混剪；关键词默认关闭）
	if len(view.Edges) != 14 {
		t.Fatalf("edges = %d", len(view.Edges))
	}
	if _, exists := byID["produce-captions"]; exists {
		t.Fatal("default workflow should hide the captions stage")
	}
	if view.Production == nil || view.Production.Status != "waiting_confirm" {
		t.Fatalf("workflow run should carry a waiting confirm gate: %+v", view.Production)
	}
	gate, hasGate := byID["produce-gate"]
	if !hasGate || gate.Status != "waiting" {
		t.Fatalf("gate stage: %+v", gate)
	}
	if byID["produce-montage"].Status != "missing" {
		t.Fatalf("montage stage should wait: %+v", byID["produce-montage"])
	}
	// 生产节点带任务提示词和产物资产提示，检视器据此展示
	spoken := byID["produce-spoken"]
	if spoken.System != remixproducer.DefaultSpokenPrompt {
		t.Fatalf("spoken stage should carry task prompt: %+v", spoken)
	}
	if spoken.Extra["asset_type"] != "spoken_script" {
		t.Fatalf("spoken stage asset hint: %+v", spoken.Extra)
	}
}

// 断点重试：失败整体重试时 agent 产物保留（缓存复用）、写手链路产物被清；
// 已完成的运行可指定节点作废重跑；不带节点的完成态重试被拒。
func TestRetryRunResumesAndInvalidatesNode(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo := store.NewRemixLabRepository(db)

	attempt := 0
	runner := func(_ context.Context, opts openaicompat.Options) error {
		attempt++
		dir := filepath.Dir(opts.OutputLastMessage)
		if attempt == 1 {
			envelope := `{"status":"failed","summary":"chat completions status 503: auth_unavailable"}`
			return os.WriteFile(opts.OutputLastMessage, []byte(envelope), 0o644)
		}
		return os.WriteFile(filepath.Join(dir, "continuous_script.txt"), []byte("重试后的成稿"), 0o644)
	}
	svc := NewService(repo, stubRuntime{view: RuntimeView{RemixBaseURL: "http://x/v1", RemixModel: "m-default", RemixAPIKey: "sk-runtime"}}, remixFakeProtector{}, t.TempDir(), runner, nil, nil)

	exp, err := svc.CreateWorkflowExperiment(t.Context(), "断点重试实验原文一二三四五", 1, "", false)
	if err != nil {
		t.Fatal(err)
	}
	failed := waitExperimentTerminal(t, svc, exp.ID)
	if failed.Runs[0].Status != "failed" {
		t.Fatalf("first attempt should fail: %+v", failed.Runs[0].Status)
	}
	runID := failed.Runs[0].ID
	dir := failed.Runs[0].OutputDir

	// 模拟第一轮已产出的 agent 情报和残留的写手产物
	if err := os.WriteFile(filepath.Join(dir, "node_output_hook.json"), []byte(`{"hook":"缓存"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "model_raw.txt"), []byte("上一轮的残稿"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := svc.RetryRun(t.Context(), runID, ""); err != nil {
		t.Fatal(err)
	}
	retried := waitExperimentTerminal(t, svc, exp.ID)
	if retried.Runs[0].Status != "completed" {
		t.Fatalf("retry should complete: %s (%s)", retried.Runs[0].Status, retried.Runs[0].ErrorMessage)
	}
	if _, err := os.Stat(filepath.Join(dir, "node_output_hook.json")); err != nil {
		t.Fatal("agent 产物应保留供缓存复用")
	}
	if _, err := os.Stat(filepath.Join(dir, "model_raw.txt")); !os.IsNotExist(err) {
		t.Fatal("写手链路产物应在重试前被清掉")
	}

	// 已完成运行：指定 agent 节点重跑 → 该节点产物被作废
	if err := svc.RetryRun(t.Context(), runID, "hook"); err != nil {
		t.Fatal(err)
	}
	again := waitExperimentTerminal(t, svc, exp.ID)
	if again.Runs[0].Status != "completed" {
		t.Fatalf("node retry should complete: %s", again.Runs[0].Status)
	}
	if _, err := os.Stat(filepath.Join(dir, "node_output_hook.json")); !os.IsNotExist(err) {
		t.Fatal("指定节点重试应作废该节点产物")
	}
	if attempt != 3 {
		t.Fatalf("runner attempts = %d, want 3", attempt)
	}

	// 已完成且不带节点 → 拒绝
	if err := svc.RetryRun(t.Context(), runID, ""); !errors.Is(err, ErrRunNotRetryable) {
		t.Fatalf("want ErrRunNotRetryable, got %v", err)
	}
	// 不存在的节点 → 拒绝
	if err := svc.RetryRun(t.Context(), runID, "nonexist"); !errors.Is(err, ErrRunNotRetryable) {
		t.Fatalf("want ErrRunNotRetryable for unknown node, got %v", err)
	}
}

func TestSaveWorkflowDefinitionRejectsInvalid(t *testing.T) {
	svc := newAgentPromptsService(t)
	wf := DefaultWorkflow(AgentPrompts{})
	wf.Edges = append(wf.Edges, [2]string{"final", "hook"})
	if _, err := svc.SaveWorkflowDefinition(wf); !errors.Is(err, ErrInvalidWorkflow) {
		t.Fatalf("want ErrInvalidWorkflow, got %v", err)
	}
	good := DefaultWorkflow(AgentPrompts{})
	saved, err := svc.SaveWorkflowDefinition(good)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(saved.Name, "工作流") {
		t.Fatalf("name=%q", saved.Name)
	}
}
