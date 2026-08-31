package remixlab

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"video-production-console/internal/agentruntime/openaicompat"
	"video-production-console/internal/remixproducer"
	"video-production-console/internal/store"
)

// 工作流里删掉字幕关键词节点后：生产段不再渲染该节点，且进度按固定
// 步骤序号推导（narration 进行中不因下标位移而错位）。
func TestAppendProductionStagesSkipsDisabledCaptions(t *testing.T) {
	view := RunStagesView{}
	rec := store.RemixLabProductionRecord{
		Status: "running", Step: "narration",
		ProjectID: "proj-9", SpokenTaskID: "task-spoken",
	}
	prod := remixproducer.Config{CaptionsDisabled: true, MontagePrompt: "自定义混剪提示词"}
	appendProductionStages(&view, rec, prod, "final", 0, 0, "")

	byID := map[string]RunStageView{}
	for _, stage := range view.Stages {
		byID[stage.ID] = stage
	}
	if _, exists := byID["produce-captions"]; exists {
		t.Fatalf("captions node must be omitted: %+v", view.Stages)
	}
	if byID["produce-spoken"].Status != "ok" {
		t.Fatalf("spoken should be done: %+v", byID["produce-spoken"])
	}
	if byID["produce-narration"].Status != "running" {
		t.Fatalf("narration should be running: %+v", byID["produce-narration"])
	}
	if byID["produce-montage"].Status != "missing" {
		t.Fatalf("montage should wait: %+v", byID["produce-montage"])
	}
	if byID["produce-montage"].System != "自定义混剪提示词" {
		t.Fatalf("montage prompt override: %+v", byID["produce-montage"])
	}
	// 边应当是 final→gate→project→spoken→narration→montage 共 5 条
	if len(view.Edges) != 5 {
		t.Fatalf("edges = %d (%v)", len(view.Edges), view.Edges)
	}
}

// 模拟一次多agent运行：把管线各阶段产物落盘，然后断言分解视图逐节点对得上。
func TestRunStagesAssemblesMultiAgentPipeline(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo := store.NewRemixLabRepository(db)

	runner := func(_ context.Context, opts openaicompat.Options) error {
		dir := filepath.Dir(opts.OutputLastMessage)
		write := func(name, content string) {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		write("hook_analysis.json", `{"hook_type":"数字砸脸"}`)
		write("facts_research.json", `{"source_facts":[]}`)
		write("imagery_ammo.json", `{"banned_imagery":[]}`)
		write("intel_summary.json", `{"search_used":true,"agents":[
			{"name":"hook","model":"m-main","ms":1200,"error":""},
			{"name":"facts","model":"grok-4.6-fast","ms":2100,"error":""},
			{"name":"ammo","model":"m-main","ms":900,"error":"ammo agent down"}]}`)
		write("prompt_system.txt", "写手系统提示词")
		write("prompt_user.txt", "写手用户提示词+情报包")
		write("model_raw.txt", `{"continuous_script":"成稿正文"}`)
		write("remix_run.json", `{"events":[
			{"event":"self_check","round":0,"verdict":"soft_fail","overlap_pct":24},
			{"event":"self_check","round":1,"verdict":"pass","overlap_pct":12}]}`)
		write("draft_v1.json", `{"continuous_script":"写手初稿"}`)
		write("review.json", `{"verdict":"fixed","round":1,"summary":"改了开头。","at":"2026-08-29T00:00:00Z"}`)
		write("publishing_package.json", `{"titles":["题"],"short_titles":["板","副","备"],"descriptions":["d1","d2","d3"],"topics":["#楼市"],"cta":""}`)
		write("continuous_script.txt", "成稿正文")
		return nil
	}

	svc := NewService(repo, stubRuntime{view: RuntimeView{RemixAPIKey: "sk-runtime", GrokBaseURL: "http://g/v1", GrokAPIKey: "gk", GrokModel: "grok-4.6-fast"}}, remixFakeProtector{}, t.TempDir(), runner, nil, nil)
	exp, err := svc.CreateExperiment(t.Context(), "工作流分解原文一二三四五", []SlotInput{
		{Model: "m-main", RunCount: 1, Pipeline: "multi_agent"},
	})
	if err != nil {
		t.Fatal(err)
	}
	done := waitExperimentTerminal(t, svc, exp.ID)
	if done.Runs[0].Status != "completed" {
		t.Fatalf("run status = %s (%s)", done.Runs[0].Status, done.Runs[0].ErrorMessage)
	}

	view, err := svc.RunStages(t.Context(), done.Runs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	// 原文 + 三路情报 + 写手 + 自检 + 审稿 + 定稿 = 8 个节点
	if view.Pipeline != "multi_agent" || len(view.Stages) != 8 {
		t.Fatalf("pipeline=%s stages=%d", view.Pipeline, len(view.Stages))
	}
	byID := map[string]RunStageView{}
	for _, stage := range view.Stages {
		byID[stage.ID] = stage
	}
	if byID["source"].Status != "ok" || byID["source"].Output == "" {
		t.Fatalf("source stage: %+v", byID["source"])
	}
	if byID["hook"].Status != "ok" || byID["hook"].Millis != 1200 || byID["hook"].PromptKey != "hook_system" {
		t.Fatalf("hook stage: %+v", byID["hook"])
	}
	if byID["facts"].Model != "grok-4.6-fast" {
		t.Fatalf("facts stage model: %+v", byID["facts"])
	}
	if byID["ammo"].Status != "failed" || byID["ammo"].Error != "ammo agent down" {
		t.Fatalf("ammo stage: %+v", byID["ammo"])
	}
	if byID["writer"].System != "写手系统提示词" || byID["writer"].User != "写手用户提示词+情报包" {
		t.Fatalf("writer prompts: %+v", byID["writer"])
	}
	if byID["selfcheck"].Status != "ok" {
		t.Fatalf("selfcheck stage: %+v", byID["selfcheck"])
	}
	events, _ := byID["selfcheck"].Extra["events"].([]map[string]any)
	if len(events) != 2 {
		t.Fatalf("selfcheck events = %d", len(events))
	}
	if byID["review"].Status != "ok" || byID["review"].Extra["verdict"] != "fixed" {
		t.Fatalf("review stage: %+v", byID["review"])
	}
	if byID["final"].Status != "ok" {
		t.Fatalf("final stage: %+v", byID["final"])
	}
	var pkg map[string]any
	if err := json.Unmarshal([]byte(byID["final"].Output), &pkg); err != nil {
		t.Fatalf("final output not json: %v", err)
	}
	if len(view.Edges) != 9 {
		t.Fatalf("edges = %d", len(view.Edges))
	}
}

// 单模型失败运行：写手节点应挂上失败原因，节点链是四段直线。
func TestRunStagesMarksFailureOnWriter(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo := store.NewRemixLabRepository(db)

	runner := func(_ context.Context, opts openaicompat.Options) error {
		envelope := `{"status":"failed","summary":"chat completions status 403: insufficient_quota"}`
		return os.WriteFile(opts.OutputLastMessage, []byte(envelope), 0o644)
	}
	svc := NewService(repo, stubRuntime{view: RuntimeView{RemixAPIKey: "sk-runtime"}}, remixFakeProtector{}, t.TempDir(), runner, nil, nil)
	exp, err := svc.CreateExperiment(t.Context(), "失败运行原文一二三四五", []SlotInput{{Model: "m", RunCount: 1}})
	if err != nil {
		t.Fatal(err)
	}
	done := waitExperimentTerminal(t, svc, exp.ID)

	view, err := svc.RunStages(t.Context(), done.Runs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Stages) != 5 || len(view.Edges) != 4 {
		t.Fatalf("stages=%d edges=%d", len(view.Stages), len(view.Edges))
	}
	byID := map[string]RunStageView{}
	for _, stage := range view.Stages {
		byID[stage.ID] = stage
	}
	if byID["writer"].Status != "failed" || byID["writer"].Error == "" {
		t.Fatalf("writer stage should carry failure: %+v", byID["writer"])
	}
	if byID["final"].Status != "failed" {
		t.Fatalf("final stage: %+v", byID["final"])
	}
}
