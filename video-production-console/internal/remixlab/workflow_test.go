package remixlab

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"video-production-console/internal/agentruntime/openaicompat"
	"video-production-console/internal/remixproducer"
	"video-production-console/internal/store"
)

func TestDefaultWorkflowIsValidAndAbsorbsOverrides(t *testing.T) {
	wf := DefaultWorkflow(AgentPrompts{HookSystem: "旧策划规则", ReviewerSystem: "自定义审稿规则"})
	if err := ValidateWorkflow(wf); err != nil {
		t.Fatal(err)
	}
	var hook *WorkflowNode
	for i := range wf.Nodes {
		if wf.Nodes[i].Type == WorkflowNodeReviewer {
			hook = &wf.Nodes[i]
		}
	}
	if hook == nil || hook.Config.SystemPrompt != "自定义审稿规则" {
		t.Fatalf("reviewer override not absorbed: %+v", hook)
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
	cycle.Nodes = append(cycle.Nodes, WorkflowNode{ID: "ammo", Type: WorkflowNodeAgent, Title: "自定义表达", Config: WorkflowNodeConfig{SystemPrompt: "表达规则"}})
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
	for i := range chain.Nodes {
		if chain.Nodes[i].ID == "hook" {
			chain.Nodes[i].Config.Role = ""
		}
	}
	chain.Nodes = append(chain.Nodes, WorkflowNode{ID: "ammo", Type: WorkflowNodeAgent, Title: "自定义表达", Config: WorkflowNodeConfig{SystemPrompt: "表达规则"}})
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

func TestWorkflowWriterModelDoesNotBecomeReviewerDefault(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo := store.NewRemixLabRepository(db)
	var got openaicompat.Options
	runner := func(_ context.Context, opts openaicompat.Options) error {
		got = opts
		dir := filepath.Dir(opts.OutputLastMessage)
		return os.WriteFile(filepath.Join(dir, "continuous_script.txt"), []byte("工作流成稿"), 0o644)
	}
	svc := NewService(repo, stubRuntime{view: RuntimeView{
		RemixBaseURL: "http://x/v1", RemixModel: "m-default", RemixReasoningEffort: "low", RemixAPIKey: "sk-runtime",
	}}, remixFakeProtector{}, t.TempDir(), runner, nil, nil)

	wf, err := svc.Workflow()
	if err != nil {
		t.Fatal(err)
	}
	for i := range wf.Nodes {
		if wf.Nodes[i].Type == WorkflowNodeWriter {
			wf.Nodes[i].Config.Model = "claude-opus-4-6-thinking"
			wf.Nodes[i].Config.ReasoningEffort = "high"
		}
	}
	if _, err := svc.SaveWorkflowDefinition(wf); err != nil {
		t.Fatal(err)
	}
	exp, err := svc.CreateWorkflowExperiment(t.Context(), "写手模型不应污染审稿原文一二三四五", 1, "", false, "")
	if err != nil {
		t.Fatal(err)
	}
	done := waitExperimentTerminal(t, svc, exp.ID)
	if done.Status != "completed" {
		t.Fatalf("status=%s", done.Status)
	}
	if got.Model != "claude-opus-4-6-thinking" {
		t.Fatalf("writer model=%q", got.Model)
	}
	if got.ReasoningEffort != "high" {
		t.Fatalf("writer effort=%q", got.ReasoningEffort)
	}
	if got.DefaultModel != "m-default" {
		t.Fatalf("reviewer default model leaked writer: %q", got.DefaultModel)
	}
	if got.DefaultEffort != "low" {
		t.Fatalf("reviewer default effort leaked writer: %q", got.DefaultEffort)
	}
}

func TestCompareModelsOverrideWriterSlotOnly(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo := store.NewRemixLabRepository(db)
	var got openaicompat.Options
	runner := func(_ context.Context, opts openaicompat.Options) error {
		got = opts
		dir := filepath.Dir(opts.OutputLastMessage)
		return os.WriteFile(filepath.Join(dir, "continuous_script.txt"), []byte("工作流成稿"), 0o644)
	}
	svc := NewService(repo, stubRuntime{view: RuntimeView{
		RemixBaseURL: "http://x/v1", RemixModel: "m-default", RemixReasoningEffort: "low", RemixAPIKey: "sk-runtime",
	}}, remixFakeProtector{}, t.TempDir(), runner, nil, nil)

	exp, err := svc.CreateWorkflowExperiment(t.Context(), "对标原文勾一个对比模型一二三四五", 1, "", false, "claude-opus-4-6-thinking")
	if err != nil {
		t.Fatal(err)
	}
	done := waitExperimentTerminal(t, svc, exp.ID)
	if done.Status != "completed" {
		t.Fatalf("status=%s", done.Status)
	}
	if got.Model != "claude-opus-4-6-thinking" {
		t.Fatalf("writer slot=%q", got.Model)
	}
	if got.DefaultModel != "m-default" {
		t.Fatalf("勾对比模型不应改审稿默认档: %q", got.DefaultModel)
	}
	view, err := svc.RunStages(t.Context(), done.Runs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]RunStageView{}
	for _, stage := range view.Stages {
		byID[stage.ID] = stage
	}
	if byID["writer"].Model != "claude-opus-4-6-thinking" {
		t.Fatalf("writer stage model=%q", byID["writer"].Model)
	}
	if byID["review"].Model != "m-default" {
		t.Fatalf("review stage must show default档 not compare model, got %q", byID["review"].Model)
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

	exp, err := svc.CreateWorkflowExperiment(t.Context(), "工作流实验原文一二三四五", 2, "", false, "")
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
	if agents != 1 {
		t.Fatalf("agent stages = %d, want 1", agents)
	}
	// 二创段 4 条边 + 生产段 6 条（关键词默认关闭）。
	if len(view.Edges) != 10 {
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

	exp, err := svc.CreateWorkflowExperiment(t.Context(), "断点重试实验原文一二三四五", 1, "", false, "")
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

	if err := svc.RetryRun(t.Context(), runID, "", ""); err != nil {
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
	if err := svc.RetryRun(t.Context(), runID, "hook", ""); err != nil {
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
	if err := svc.RetryRun(t.Context(), runID, "", ""); !errors.Is(err, ErrRunNotRetryable) {
		t.Fatalf("want ErrRunNotRetryable, got %v", err)
	}
	// 不存在的节点 → 拒绝
	if err := svc.RetryRun(t.Context(), runID, "nonexist", ""); !errors.Is(err, ErrRunNotRetryable) {
		t.Fatalf("want ErrRunNotRetryable for unknown node, got %v", err)
	}
}

// 开跑时显式选择的模型优先级最高，并落进槽位供整个实验（含重试）沿用。
func TestCreateWorkflowExperimentModelOverride(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo := store.NewRemixLabRepository(db)

	var mu sync.Mutex
	gotModel := ""
	runner := func(_ context.Context, opts openaicompat.Options) error {
		mu.Lock()
		gotModel = opts.Model
		mu.Unlock()
		dir := filepath.Dir(opts.OutputLastMessage)
		return os.WriteFile(filepath.Join(dir, "continuous_script.txt"), []byte("选模型开跑成稿"), 0o644)
	}
	svc := NewService(repo, stubRuntime{view: RuntimeView{RemixBaseURL: "http://x/v1", RemixModel: "m-default", RemixAPIKey: "sk-runtime"}}, remixFakeProtector{}, t.TempDir(), runner, nil, nil)

	exp, err := svc.CreateWorkflowExperiment(t.Context(), "选模型开跑原文一二三四五", 1, "", false, "m-pick")
	if err != nil {
		t.Fatal(err)
	}
	done := waitExperimentTerminal(t, svc, exp.ID)
	if done.Runs[0].Status != "completed" {
		t.Fatalf("run status = %s", done.Runs[0].Status)
	}
	mu.Lock()
	model := gotModel
	mu.Unlock()
	if model != "m-pick" {
		t.Fatalf("runner model = %q, want m-pick", model)
	}
	_, slots, _, err := repo.GetExperiment(t.Context(), exp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if slots[0].Model != "m-pick" {
		t.Fatalf("slot model = %q, want m-pick", slots[0].Model)
	}
}

// 多模型对比开跑：每个模型一个槽、各跑 runCount 次，全部并行；重复与空模型
// 去掉；全自动混剪要求总运行数为 1。
func TestCreateWorkflowExperimentMultiModel(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo := store.NewRemixLabRepository(db)

	var mu sync.Mutex
	seenModels := map[string]int{}
	runner := func(_ context.Context, opts openaicompat.Options) error {
		mu.Lock()
		seenModels[opts.Model]++
		mu.Unlock()
		dir := filepath.Dir(opts.OutputLastMessage)
		return os.WriteFile(filepath.Join(dir, "continuous_script.txt"), []byte("成稿 "+opts.Model), 0o644)
	}
	svc := NewService(repo, stubRuntime{view: RuntimeView{RemixBaseURL: "http://x/v1", RemixModel: "m-default", RemixAPIKey: "sk-runtime"}}, remixFakeProtector{}, t.TempDir(), runner, nil, nil)

	exp, err := svc.CreateWorkflowExperiment(t.Context(), "多模型对比原文一二三四五", 1, "", false, "m-fast", "m-slow", "m-fast", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(exp.Slots) != 2 || len(exp.Runs) != 2 {
		t.Fatalf("slots=%d runs=%d, want 2/2", len(exp.Slots), len(exp.Runs))
	}
	done := waitExperimentTerminal(t, svc, exp.ID)
	if done.Status != "completed" {
		t.Fatalf("status = %s", done.Status)
	}
	mu.Lock()
	defer mu.Unlock()
	if seenModels["m-fast"] != 1 || seenModels["m-slow"] != 1 {
		t.Fatalf("models run = %v", seenModels)
	}

	accountID := "8c232ac1-a2e2-424e-866e-77fe8be1195d"
	if _, err := svc.CreateWorkflowExperiment(t.Context(), "全自动多模型原文一二三四五", 1, accountID, true, "m-fast", "m-slow"); !errors.Is(err, ErrInvalidAutoProduce) {
		t.Fatalf("auto with two models must be rejected, got %v", err)
	}
}

// 换模型重试：整体重试改槽位模型（写手链沿用新模型），指定 agent 节点重试
// 改实验快照里该节点的模型；两处都持久化，后续重试不再回到坏模型。
func TestRetryRunSwitchesModel(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo := store.NewRemixLabRepository(db)

	var mu sync.Mutex
	var models []string
	attempt := 0
	runner := func(_ context.Context, opts openaicompat.Options) error {
		mu.Lock()
		attempt++
		n := attempt
		models = append(models, opts.Model)
		mu.Unlock()
		dir := filepath.Dir(opts.OutputLastMessage)
		if n == 1 {
			envelope := `{"status":"failed","summary":"chat completions status 403: model_disabled"}`
			return os.WriteFile(opts.OutputLastMessage, []byte(envelope), 0o644)
		}
		return os.WriteFile(filepath.Join(dir, "continuous_script.txt"), []byte("换模型后的成稿"), 0o644)
	}
	svc := NewService(repo, stubRuntime{view: RuntimeView{RemixBaseURL: "http://x/v1", RemixModel: "m-dead", RemixAPIKey: "sk-runtime"}}, remixFakeProtector{}, t.TempDir(), runner, nil, nil)

	exp, err := svc.CreateWorkflowExperiment(t.Context(), "换模型重试原文一二三四五", 1, "", false, "")
	if err != nil {
		t.Fatal(err)
	}
	failed := waitExperimentTerminal(t, svc, exp.ID)
	if failed.Runs[0].Status != "failed" {
		t.Fatalf("first attempt should fail: %s", failed.Runs[0].Status)
	}
	runID := failed.Runs[0].ID

	// 整体重试带新模型 → 槽位模型持久化，本次运行用新模型
	if err := svc.RetryRun(t.Context(), runID, "", "m-fresh"); err != nil {
		t.Fatal(err)
	}
	done := waitExperimentTerminal(t, svc, exp.ID)
	if done.Runs[0].Status != "completed" {
		t.Fatalf("retry should complete: %s", done.Runs[0].Status)
	}
	mu.Lock()
	gotModel := models[len(models)-1]
	mu.Unlock()
	if gotModel != "m-fresh" {
		t.Fatalf("retry model = %q, want m-fresh", gotModel)
	}
	_, slots, _, err := repo.GetExperiment(t.Context(), exp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if slots[0].Model != "m-fresh" {
		t.Fatalf("slot model = %q, want m-fresh", slots[0].Model)
	}

	// 指定 agent 节点重试带新模型 → 快照里该节点模型更新
	if err := svc.RetryRun(t.Context(), runID, "hook", "grok-next"); err != nil {
		t.Fatal(err)
	}
	waitExperimentTerminal(t, svc, exp.ID)
	expRec, _, _, err := repo.GetExperiment(t.Context(), exp.ID)
	if err != nil {
		t.Fatal(err)
	}
	wf, ok := parseWorkflowJSON(expRec.WorkflowJSON)
	if !ok {
		t.Fatal("snapshot unparsable after node model switch")
	}
	for _, node := range wf.Nodes {
		if node.ID == "hook" && node.Config.Model != "grok-next" {
			t.Fatalf("hook node model = %q, want grok-next", node.Config.Model)
		}
	}
}

func TestImportDraftCreatesCompletedRunWithoutRunner(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo := store.NewRemixLabRepository(db)
	runner := func(context.Context, openaicompat.Options) error {
		t.Fatal("import draft must not call the remix runner")
		return nil
	}
	svc := NewService(repo, stubRuntime{view: RuntimeView{RemixAPIKey: "sk-runtime"}}, remixFakeProtector{}, t.TempDir(), runner, nil, nil)

	script := strings.Repeat("手工定稿正文。", 8)
	exp, err := svc.ImportDraft(t.Context(), DraftInput{
		PackageInput: PackageInput{ContinuousScript: script},
	})
	if err != nil {
		t.Fatal(err)
	}
	if exp.Status != "completed" || !exp.Workflow || len(exp.Runs) != 1 {
		t.Fatalf("exp status=%s workflow=%v runs=%d", exp.Status, exp.Workflow, len(exp.Runs))
	}
	run := exp.Runs[0]
	if run.Status != "completed" || run.ContinuousScript != script {
		t.Fatalf("run=%+v", run)
	}
	if run.Production == nil || run.Production.Status != "waiting_confirm" {
		t.Fatalf("production gate: %+v", run.Production)
	}
	var pkg PackageInput
	if err := json.Unmarshal([]byte(run.PackageJSON), &pkg); err != nil {
		t.Fatal(err)
	}
	if pkg.ContinuousScript != script || len(pkg.ShortTitles) == 0 {
		t.Fatalf("package=%+v", pkg)
	}

	view, err := svc.RunStages(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]RunStageView{}
	for _, stage := range view.Stages {
		byID[stage.ID] = stage
	}
	for _, id := range []string{"source", "hook", "writer", "review"} {
		if byID[id].Status != "skipped" {
			t.Fatalf("%s status=%s, want skipped", id, byID[id].Status)
		}
	}
	if byID["final"].Status != "ok" || !strings.Contains(byID["final"].Output, "手工定稿正文") {
		t.Fatalf("final=%+v", byID["final"])
	}
	if pkg.ShortTitles[0] != boardTitleFromScript(script) {
		t.Fatalf("auto board title=%q", pkg.ShortTitles[0])
	}
	if err := svc.Rework(t.Context(), run.ID, "开头再狠一点"); !errors.Is(err, ErrRunNotReworkable) {
		t.Fatalf("manual draft must not rework: %v", err)
	}
	if err := svc.RetryRun(t.Context(), run.ID, "hook", ""); !errors.Is(err, ErrRunNotRetryable) {
		t.Fatalf("manual draft must not retry: %v", err)
	}

	if _, err := svc.ImportDraft(t.Context(), DraftInput{PackageInput: PackageInput{ContinuousScript: ""}}); !errors.Is(err, ErrInvalidPackage) {
		t.Fatalf("empty script: %v", err)
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
