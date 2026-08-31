package remixlab

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"video-production-console/internal/agentruntime/openaicompat"
	"video-production-console/internal/remixproducer"
	"video-production-console/internal/store"
)

// 工作流视图：把一次 run 落盘的中间产物按管线节点分解，给前端画
// n8n 式节点图。每个节点带状态、耗时、用到的提示词和实际输入输出，
// 让操作员看清楚每一步发生了什么、哪一步出了问题、该改哪路提示词。

type RunStageView struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`   // input / agent / gate / output
	Title  string `json:"title"`
	Status string `json:"status"` // ok / failed / skipped / missing
	// X/Y 是画布坐标：工作流运行取快照里的节点位置；固定管线为 0（前端用内置布局）。
	X      float64 `json:"x,omitempty"`
	Y      float64 `json:"y,omitempty"`
	Model  string  `json:"model,omitempty"`
	Millis int64   `json:"ms,omitempty"`
	Error  string  `json:"error,omitempty"`
	// PromptKey 对应「Agent提示词」编辑器的字段名；写手节点为 "writer"（进提示词库改）。
	PromptKey string `json:"prompt_key,omitempty"`
	System    string `json:"system_prompt,omitempty"`
	User      string `json:"user_prompt,omitempty"`
	// Output 是该节点的主输出（JSON 或纯文本，前端按内容展示）。
	Output string         `json:"output,omitempty"`
	Extra  map[string]any `json:"extra,omitempty"`
}

type RunStagesView struct {
	RunID    string         `json:"run_id"`
	Pipeline string         `json:"pipeline"`
	Status   string         `json:"status"`
	Stages   []RunStageView `json:"stages"`
	Edges    [][2]string    `json:"edges"`
	// Production 生产段总览（画布确认按钮/重试按钮据此渲染）。
	Production *ProductionView `json:"production,omitempty"`
}

// RunStages 组装一次 run 的工作流分解视图。产物缺失不报错：缺哪个节点标哪个。
// 工作流实验按快照的节点图组装；旧的固定管线运行走内置链路。
func (s *Service) RunStages(ctx context.Context, runID string) (RunStagesView, error) {
	run, err := s.repo.GetRun(ctx, runID)
	if err != nil {
		return RunStagesView{}, err
	}
	exp, slots, _, err := s.repo.GetExperiment(ctx, run.ExperimentID)
	if err != nil {
		return RunStagesView{}, err
	}
	var slot store.RemixLabSlotRecord
	for _, item := range slots {
		if item.ID == run.SlotID {
			slot = item
			break
		}
	}
	dir := strings.TrimSpace(run.OutputDir)
	read := func(name string) string {
		if dir == "" {
			return ""
		}
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(raw))
	}

	failure := ""
	if run.Status == "failed" {
		failure = strings.TrimSpace(run.ErrorMessage)
	}

	view := RunStagesView{RunID: run.ID, Pipeline: slot.Pipeline, Status: run.Status}
	workflow, hasWorkflow := parseWorkflowJSON(exp.WorkflowJSON)
	if hasWorkflow {
		view.Pipeline = "workflow"
	}
	multiAgent := !hasWorkflow && slot.Pipeline == "multi_agent"
	nodePos := map[string][2]float64{}
	if hasWorkflow {
		for _, node := range workflow.Nodes {
			nodePos[node.ID] = [2]float64{node.X, node.Y}
		}
	}
	at := func(stage RunStageView) RunStageView {
		if pos, ok := nodePos[stage.ID]; ok {
			stage.X, stage.Y = pos[0], pos[1]
		}
		return stage
	}
	ids := resolveStageIDs(workflow, hasWorkflow)

	// —— 原文 ——
	source := read("source.txt")
	view.Stages = append(view.Stages, at(RunStageView{
		ID: ids.source, Kind: "input", Title: "对标原文",
		Status: okOr(source != "", "missing"),
		Output: source,
		Extra:  map[string]any{"chars": utf8.RuneCountInString(source)},
	}))

	// —— 情报agent ——
	prompts, _ := s.AgentPromptsView()
	if hasWorkflow {
		outcomes := readIntelSummary(read("intel_summary.json"))
		for _, node := range workflow.Nodes {
			if node.Type != WorkflowNodeAgent {
				continue
			}
			stage := RunStageView{
				ID: node.ID, Kind: "agent", Title: node.Title,
				System: node.Config.SystemPrompt,
				User:   node.Config.UserTemplate,
				Output: read("node_output_" + node.ID + ".json"),
				Extra:  map[string]any{"channel": node.Config.Channel},
			}
			if outcome, ok := outcomes[node.ID]; ok {
				stage.Model = outcome.Model
				stage.Millis = outcome.Millis
				stage.Error = outcome.Error
			}
			switch {
			case stage.Error != "":
				stage.Status = "failed"
			case stage.Output != "":
				stage.Status = "ok"
			default:
				stage.Status = "missing"
			}
			view.Stages = append(view.Stages, at(stage))
		}
	} else if multiAgent {
		outcomes := readIntelSummary(read("intel_summary.json"))
		intelStage := func(id, title, file, promptKey, system string) RunStageView {
			stage := RunStageView{
				ID: id, Kind: "agent", Title: title,
				PromptKey: promptKey, System: system,
				Output: read(file),
			}
			if outcome, ok := outcomes[id]; ok {
				stage.Model = outcome.Model
				stage.Millis = outcome.Millis
				stage.Error = outcome.Error
			}
			switch {
			case stage.Error != "":
				stage.Status = "failed"
			case stage.Output != "":
				stage.Status = "ok"
			default:
				stage.Status = "missing"
			}
			return stage
		}
		view.Stages = append(view.Stages,
			intelStage("hook", "钩子分析", "hook_analysis.json", "hook_system", prompts.Prompts.HookSystem),
			intelStage("facts", "事实核查", "facts_research.json", "facts_search_system", prompts.Prompts.FactsSearchSystem),
			intelStage("ammo", "弹药库", "imagery_ammo.json", "ammo_system", prompts.Prompts.AmmoSystem),
		)
	}

	// —— 写手 ——（提示词是当次运行落盘的原文，含注入的情报包）
	writerRaw := read("model_raw.txt")
	writer := RunStageView{
		ID: ids.writer, Kind: "agent", Title: "写手",
		Model:     slot.Model,
		PromptKey: "writer",
		System:    read("prompt_system.txt"),
		User:      read("prompt_user.txt"),
		Output:    writerRaw,
		Status:    okOr(writerRaw != "", "missing"),
		Extra:     map[string]any{"prompt_name": run.PromptName, "prompt_stamp": run.PromptStamp},
	}
	if writer.Status == "missing" && failure != "" {
		writer.Status = "failed"
		writer.Error = failure
		failure = "" // 失败原因只挂最早断掉的节点
	}
	view.Stages = append(view.Stages, at(writer))

	// —— 机械自检 ——（轮次和判定从运行日志取；阈值=默认+快照节点覆盖）
	checkEvents := readRunLogEvents(read("remix_run.json"), "self_check")
	overlapMax, overlapHard, lenMin, lenHard, maxRounds := openaicompat.SelfCheckDefaults()
	if hasWorkflow {
		for _, node := range workflow.Nodes {
			if node.Type != WorkflowNodeSelfcheck {
				continue
			}
			cfg := node.Config
			if cfg.OverlapMaxPct > 0 {
				overlapMax = cfg.OverlapMaxPct
			}
			if cfg.OverlapHardPct > 0 {
				overlapHard = cfg.OverlapHardPct
			}
			if cfg.LenMinRatio > 0 {
				lenMin = cfg.LenMinRatio
			}
			if cfg.LenHardRatio > 0 {
				lenHard = cfg.LenHardRatio
			}
			if cfg.MaxRounds > 0 {
				maxRounds = cfg.MaxRounds
			}
			break
		}
	}
	check := RunStageView{
		ID: ids.selfcheck, Kind: "gate", Title: "机械自检",
		Extra: map[string]any{
			"events": checkEvents,
			"limits": map[string]any{
				"overlap_max_pct": overlapMax, "overlap_hard_pct": overlapHard,
				"len_min_ratio": lenMin, "len_hard_ratio": lenHard, "max_rounds": maxRounds,
			},
		},
	}
	switch {
	case len(checkEvents) == 0:
		check.Status = "missing"
	case lastVerdict(checkEvents) == "hard_fail":
		check.Status = "failed"
	default:
		check.Status = "ok"
	}
	if check.Status != "failed" && failure != "" && writerRaw != "" && read("continuous_script.txt") == "" {
		check.Status = "failed"
		check.Error = failure
		failure = ""
	}
	view.Stages = append(view.Stages, at(check))

	// —— 审稿终审 ——（工作流没有审稿节点时不出现在图上）
	if ids.review != "" {
		reviewSystem := prompts.Prompts.ReviewerSystem
		if hasWorkflow {
			if node := workflowReviewer(workflow); node != nil && strings.TrimSpace(node.Config.SystemPrompt) != "" {
				reviewSystem = node.Config.SystemPrompt
			}
		}
		reviewRaw := read("review.json")
		review := RunStageView{
			ID: ids.review, Kind: "agent", Title: "审稿终审",
			Model:     slot.Model,
			PromptKey: "reviewer_system",
			System:    reviewSystem,
			Output:    reviewRaw,
		}
		if reviewRaw == "" {
			review.Status = "skipped"
		} else {
			var record struct {
				Verdict string `json:"verdict"`
				Round   int    `json:"round"`
				Error   string `json:"error"`
			}
			_ = json.Unmarshal([]byte(reviewRaw), &record)
			review.Extra = map[string]any{
				"verdict": record.Verdict, "round": record.Round,
				"draft_v1": read("draft_v1.json"),
			}
			switch record.Verdict {
			case "error":
				review.Status = "failed"
				review.Error = record.Error
			case "":
				review.Status = "skipped"
			default:
				review.Status = "ok"
			}
		}
		view.Stages = append(view.Stages, at(review))
	}

	// —— 定稿 ——
	final := RunStageView{
		ID: ids.final, Kind: "output", Title: "定稿与发布包",
		Output: strings.TrimSpace(run.PackageJSON),
	}
	if final.Output == "" {
		final.Output = read("publishing_package.json")
	}
	switch {
	case run.Status == "completed":
		final.Status = "ok"
	case run.Status == "failed":
		final.Status = "failed"
		final.Error = firstNonBlank(failure, run.ErrorMessage)
	default:
		final.Status = "missing"
	}
	view.Stages = append(view.Stages, at(final))

	switch {
	case hasWorkflow:
		view.Edges = workflow.Edges
	case multiAgent:
		view.Edges = [][2]string{
			{"source", "hook"}, {"source", "facts"}, {"source", "ammo"},
			{"hook", "writer"}, {"facts", "writer"}, {"ammo", "writer"},
			{"writer", "selfcheck"}, {"selfcheck", "review"}, {"review", "final"},
		}
	default:
		view.Edges = [][2]string{
			{"source", "writer"}, {"writer", "selfcheck"}, {"selfcheck", "review"}, {"review", "final"},
		}
	}

	// —— 生产段（工作流运行）：确认闸门 → 建项目 → 口播稿 → 字幕 → 配音 → 混剪 ——
	if hasWorkflow {
		if rec, err := s.repo.GetProduction(ctx, run.ID); err == nil {
			view.Production = productionView(rec)
			finalX, finalY := 1440.0, 190.0
			if pos, ok := nodePos[ids.final]; ok {
				finalX, finalY = pos[0], pos[1]
			}
			appendProductionStages(&view, rec, workflow.Production, ids.final, finalX, finalY, run.ContinuousScript)
		}
	}
	return view, nil
}

// appendProductionStages 把生产段节点接在定稿之后。节点状态由生产记录的
// status+step 推导：跑过的 ok、当前步 running/failed、没到的 missing。
func appendProductionStages(view *RunStagesView, rec store.RemixLabProductionRecord, prod remixproducer.Config, finalID string, finalX, finalY float64, script string) {
	type produceStep struct {
		id, step, title string
		taskID          string
		// system 是该步发给任务系统的提示词（可在设计画布改）；assetType 是
		// 该步主产物的资产类型，前端检视器据此拉内容展示。
		system    string
		assetType string
		desc      string
	}
	steps := []produceStep{
		{id: "produce-project", step: "project", title: "建项目",
			desc: "在所选账号下建项目，写入定稿发布包。"},
		{id: "produce-spoken", step: "spoken", title: "口播稿", taskID: rec.SpokenTaskID,
			system: prod.EffectiveSpokenPrompt(), assetType: "spoken_script",
			desc: "按口播节奏切句，供配音和字幕使用。"},
		{id: "produce-captions", step: "captions", title: "字幕关键词", taskID: rec.CaptionTaskID,
			system: prod.EffectiveCaptionsPrompt(), assetType: "caption_keywords"},
		{id: "produce-narration", step: "narration", title: "配音",
			assetType: "subtitle_srt",
			desc:      "生成旁白音频与逐句 SRT。点此节点查看产物，失败可重试续跑。"},
		{id: "produce-montage", step: "montage", title: "混剪草稿", taskID: rec.MontageTaskID,
			system: prod.EffectiveMontagePrompt(),
			desc:   "生成可编辑的剪映草稿。点此查看状态，失败可重试。"},
	}
	if prod.CaptionsDisabled {
		// 字幕关键词节点被从工作流里删掉：画布不再渲染这一步。
		filtered := steps[:0]
		for _, step := range steps {
			if step.step != "captions" {
				filtered = append(filtered, step)
			}
		}
		steps = filtered
	}
	stepIndex := map[string]int{"confirm": -1, "project": 0, "spoken": 1, "captions": 2, "narration": 3, "montage": 4, "done": 5}
	current, ok := stepIndex[rec.Step]
	if !ok {
		current = -1
	}

	minX, maxY := finalX, finalY
	for _, stage := range view.Stages {
		if stage.X < minX {
			minX = stage.X
		}
		if stage.Y > maxY {
			maxY = stage.Y
		}
	}
	gate := RunStageView{
		ID: "produce-gate", Kind: "gate", Title: "确认二创",
		X: minX, Y: maxY + 240,
		Extra: map[string]any{"production": true, "account_id": rec.AccountID, "auto": rec.Auto},
	}
	if trimmed := strings.TrimSpace(script); trimmed != "" {
		runes := []rune(trimmed)
		if len(runes) > 8000 {
			trimmed = string(runes[:8000]) + "…"
		}
		gate.Extra["script"] = trimmed
	}
	switch rec.Status {
	case "waiting_confirm":
		gate.Status = "waiting"
	default:
		gate.Status = "ok"
		if rec.Auto {
			gate.Extra["passed_by"] = "auto"
		}
	}
	view.Stages = append(view.Stages, gate)
	view.Edges = append(view.Edges, [2]string{finalID, "produce-gate"})

	prev := "produce-gate"
	for i, step := range steps {
		stage := RunStageView{
			ID: step.id, Kind: "produce", Title: step.title,
			X: minX + 230*float64(i+1), Y: maxY + 240,
			System: step.system,
			Extra:  map[string]any{"production": true, "production_step": step.step},
		}
		if step.desc != "" {
			stage.Extra["desc"] = step.desc
		}
		if step.assetType != "" {
			stage.Extra["asset_type"] = step.assetType
		}
		if rec.ProjectID != "" {
			stage.Extra["project_id"] = rec.ProjectID
		}
		if step.taskID != "" {
			stage.Extra["task_id"] = step.taskID
		}
		// 用固定步骤序号（而非过滤后的下标）比对进度，字幕节点删除后不错位。
		order := stepIndex[step.step]
		switch {
		case rec.Status == "waiting_confirm":
			stage.Status = "missing"
		case order < current || rec.Step == "done":
			stage.Status = "ok"
			if step.step == "captions" && rec.CaptionTaskID == "" {
				stage.Status = "skipped"
			}
		case order == current && rec.Status == "running":
			stage.Status = "running"
		case order == current && rec.Status == "failed":
			stage.Status = "failed"
			stage.Error = rec.Error
		case order == current && rec.Status == "completed":
			stage.Status = "ok"
		default:
			stage.Status = "missing"
		}
		view.Stages = append(view.Stages, stage)
		view.Edges = append(view.Edges, [2]string{prev, step.id})
		prev = step.id
	}
}

// stageNodeIDs 是骨干节点在图里的实际ID：工作流按快照，固定管线用内置名。
type stageNodeIDs struct {
	source, writer, selfcheck, review, final string
}

func resolveStageIDs(w Workflow, hasWorkflow bool) stageNodeIDs {
	ids := stageNodeIDs{source: "source", writer: "writer", selfcheck: "selfcheck", review: "review", final: "final"}
	if !hasWorkflow {
		return ids
	}
	ids.review = ""
	for _, node := range w.Nodes {
		switch node.Type {
		case WorkflowNodeInput:
			ids.source = node.ID
		case WorkflowNodeWriter:
			ids.writer = node.ID
		case WorkflowNodeSelfcheck:
			ids.selfcheck = node.ID
		case WorkflowNodeReviewer:
			ids.review = node.ID
		case WorkflowNodeOutput:
			ids.final = node.ID
		}
	}
	return ids
}

type intelOutcome struct {
	Model  string
	Millis int64
	Error  string
}

func readIntelSummary(raw string) map[string]intelOutcome {
	out := map[string]intelOutcome{}
	if raw == "" {
		return out
	}
	var summary struct {
		Agents []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
			MS    int64  `json:"ms"`
			Error string `json:"error"`
		} `json:"agents"`
	}
	if err := json.Unmarshal([]byte(raw), &summary); err != nil {
		return out
	}
	for _, agent := range summary.Agents {
		out[agent.Name] = intelOutcome{Model: agent.Model, Millis: agent.MS, Error: agent.Error}
	}
	return out
}

func readRunLogEvents(raw, event string) []map[string]any {
	if raw == "" {
		return nil
	}
	var log struct {
		Events []map[string]any `json:"events"`
	}
	if err := json.Unmarshal([]byte(raw), &log); err != nil {
		return nil
	}
	matched := make([]map[string]any, 0, 4)
	for _, item := range log.Events {
		if item["event"] == event {
			matched = append(matched, item)
		}
	}
	return matched
}

func lastVerdict(events []map[string]any) string {
	for i := len(events) - 1; i >= 0; i-- {
		if verdict, ok := events[i]["verdict"].(string); ok && verdict != "" {
			return verdict
		}
	}
	return ""
}

func okOr(ok bool, fallback string) string {
	if ok {
		return "ok"
	}
	return fallback
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
