package remixlab

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"video-production-console/internal/agentruntime/openaicompat"
	"video-production-console/internal/store"
)

var (
	ErrRunNotReworkable   = errors.New("run is not reworkable")
	ErrRunNotRetryable    = errors.New("run is not retryable")
	ErrInvalidAnnotations = errors.New("annotations must be 1 to 2000 characters")
	ErrInvalidPackage     = errors.New("package fields are invalid")
)

// flowDownstreamArtifacts 是重试时必须作废的写手链路产物：写手及其后的环节
// 永远重跑（自检硬失败说明旧稿不能要，配额失败说明根本没稿）；agent 节点的
// node_output_*.json 不在此列，留着的会被引擎断点复用。
var flowDownstreamArtifacts = []string{
	"model_raw.txt", "continuous_script.txt", "publishing_package.json",
	"review.json", "self_check.json", "viral_analysis.json", "structure_design.json", "result.json",
}

// RetryRun 重试一次运行：失败的运行可整体重试（已有产物的 agent 节点直接
// 复用，缺的重跑，写手链路从头重做后自动续走）；已完成的运行必须指定要重
// 跑的 agent 节点（该节点产物作废重生成，写手链路跟着重做）。
// model 非空时先换模型再重试：指定 agent 节点改实验快照里该节点的模型，
// 其余情况改槽位模型（写手/审稿主模型）；换过之后后续重试与返工都沿用。
func (s *Service) RetryRun(ctx context.Context, runID, nodeID, model string) error {
	run, err := s.repo.GetRun(ctx, runID)
	if err != nil {
		return err
	}
	nodeID = strings.TrimSpace(nodeID)
	model = strings.TrimSpace(model)
	switch run.Status {
	case "failed":
		// 整体重试或指定节点都行
	case "completed":
		if nodeID == "" {
			return ErrRunNotRetryable
		}
	default:
		return ErrRunNotRetryable
	}
	exp, _, _, err := s.repo.GetExperiment(ctx, run.ExperimentID)
	if err != nil {
		return err
	}
	targetIsAgent := false
	if nodeID != "" {
		wf, ok := parseWorkflowJSON(exp.WorkflowJSON)
		if !ok {
			return ErrRunNotRetryable
		}
		found := false
		for i := range wf.Nodes {
			node := &wf.Nodes[i]
			if node.ID != nodeID {
				continue
			}
			found = true
			if node.Type == WorkflowNodeAgent {
				targetIsAgent = true
				if strings.TrimSpace(run.OutputDir) != "" {
					// 作废该节点产物，引擎重跑它；其余 agent 继续吃缓存。
					_ = os.Remove(filepath.Join(run.OutputDir, "node_output_"+nodeID+".json"))
				}
				if model != "" {
					// 换模型落到实验快照里的这个节点，同实验后续重试沿用。
					node.Config.Model = model
					raw, err := json.Marshal(wf)
					if err != nil {
						return err
					}
					if err := s.repo.UpdateExperimentWorkflowJSON(ctx, exp.ID, string(raw)); err != nil {
						return err
					}
				}
			}
			break
		}
		if !found {
			return ErrRunNotRetryable
		}
	}
	if model != "" && !targetIsAgent {
		if err := s.repo.UpdateSlotModel(ctx, run.SlotID, model); err != nil {
			return err
		}
	}
	if dir := strings.TrimSpace(run.OutputDir); dir != "" {
		for _, name := range flowDownstreamArtifacts {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
	run.Status = "queued"
	run.ErrorMessage = ""
	run.StartedAt = nil
	run.FinishedAt = nil
	if err := s.repo.UpdateRun(ctx, run); err != nil {
		return err
	}
	go s.drive(run.ExperimentID)
	return nil
}

// PackageInput 是操作员在创作台编辑后的定稿字段（写手 JSON 的 draft 形状）。
type PackageInput struct {
	ContinuousScript string   `json:"continuous_script"`
	Titles           []string `json:"titles"`
	ShortTitles      []string `json:"short_titles"`
	Descriptions     []string `json:"descriptions"`
	Topics           []string `json:"topics"`
	CTA              string   `json:"cta"`
}

// UpdateRunPackage 保存操作员编辑后的定稿。编辑内容原样入库不做归一化——
// 操作员的取舍是最终口径；只校验正文非空和板标题（short_titles[0]）在。
func (s *Service) UpdateRunPackage(ctx context.Context, runID string, input PackageInput) error {
	script := strings.TrimSpace(input.ContinuousScript)
	if utf8.RuneCountInString(script) < 40 {
		return ErrInvalidPackage
	}
	cleaned := PackageInput{
		ContinuousScript: script,
		Titles:           cleanStringList(input.Titles),
		ShortTitles:      cleanStringList(input.ShortTitles),
		Descriptions:     cleanStringList(input.Descriptions),
		Topics:           cleanStringList(input.Topics),
		CTA:              strings.TrimSpace(input.CTA),
	}
	if len(cleaned.ShortTitles) == 0 {
		return ErrInvalidPackage
	}
	run, err := s.repo.GetRun(ctx, runID)
	if err != nil {
		return err
	}
	if run.Status != "completed" {
		return ErrRunNotReworkable
	}
	packageJSON, err := json.Marshal(cleaned)
	if err != nil {
		return err
	}
	titlesJSON := "[]"
	if raw, err := json.Marshal(cleaned.Titles); err == nil && cleaned.Titles != nil {
		titlesJSON = string(raw)
	}
	// 运行目录同步一份正文，保持目录审计与数据库一致；发布包审计文件保留模型版本。
	if dir := strings.TrimSpace(run.OutputDir); dir != "" {
		_ = os.WriteFile(filepath.Join(dir, "continuous_script.txt"), []byte(script), 0o644)
	}
	return s.repo.UpdateRunPackage(ctx, runID, script, titlesJSON, string(packageJSON))
}

// Rework 把操作员批注交给审稿agent返工：run 置回 running（实验状态联动，前端
// 恢复轮询），后台完成后回填修订稿与审稿结论。审稿失败时保留原稿，结论里带原因。
func (s *Service) Rework(ctx context.Context, runID, annotations string) error {
	annotations = strings.TrimSpace(annotations)
	if annotations == "" || utf8.RuneCountInString(annotations) > 2000 {
		return ErrInvalidAnnotations
	}
	run, err := s.repo.GetRun(ctx, runID)
	if err != nil {
		return err
	}
	if run.Status != "completed" || strings.TrimSpace(run.ContinuousScript) == "" {
		return ErrRunNotReworkable
	}
	exp, slots, _, err := s.repo.GetExperiment(ctx, run.ExperimentID)
	if err != nil {
		return err
	}
	var slot store.RemixLabSlotRecord
	found := false
	for _, item := range slots {
		if item.ID == run.SlotID {
			slot = item
			found = true
			break
		}
	}
	if !found {
		return store.ErrRemixLabNotFound
	}
	rt, rtErr := s.runtime.Runtime(ctx)
	key, keyErr := s.resolveRunKey(slot, rt, rtErr)
	if keyErr != nil {
		return keyErr
	}

	draftJSON := strings.TrimSpace(run.PackageJSON)
	if draftJSON == "" {
		encoded, encodeErr := json.Marshal(map[string]string{"continuous_script": run.ContinuousScript})
		if encodeErr != nil {
			return encodeErr
		}
		draftJSON = string(encoded)
	}
	round := nextReviewRound(run.ReviewJSON)

	// 置回 running 让前端恢复轮询。控制台若在返工中重启，FailStale 会把它标成
	// failed，但正文列仍保留上一版定稿，不丢内容。
	now := s.now()
	run.Status = "running"
	run.StartedAt = &now
	run.FinishedAt = nil
	if err := s.repo.UpdateRun(ctx, run); err != nil {
		return err
	}
	go s.executeRework(exp, slot, run, key, draftJSON, annotations, round)
	return nil
}

func (s *Service) executeRework(exp store.RemixLabExperimentRecord, slot store.RemixLabSlotRecord, run store.RemixLabRunRecord, key, draftJSON, annotations string, round int) {
	ctx := context.Background()
	overrides, _ := (Store{DataRoot: s.dataRoot}).LoadAgentPrompts()
	reviewerPrompt := overrides.ReviewerSystem
	// 工作流实验的打回用快照里审稿节点的提示词，跟首轮口径一致。
	if wf, ok := parseWorkflowJSON(exp.WorkflowJSON); ok {
		if node := workflowReviewer(wf); node != nil && strings.TrimSpace(node.Config.SystemPrompt) != "" {
			reviewerPrompt = node.Config.SystemPrompt
		}
	}
	outcome := openaicompat.ReviewRemixDraft(openaicompat.ReviewOptions{
		BaseURL:         slot.BaseURL,
		APIKey:          key,
		Model:           slot.Model,
		ReasoningEffort: slot.ReasoningEffort,
		SystemPrompt:    reviewerPrompt,
		Source:          exp.SourceText,
		DraftJSON:       draftJSON,
		Annotations:     annotations,
		OutputDir:       run.OutputDir,
		Round:           round,
	})
	if raw, err := json.Marshal(outcome.Record); err == nil {
		run.ReviewJSON = string(raw)
	}
	if outcome.RevisedJSON != "" {
		if script, err := openaicompat.WriteReviewedFiles(run.OutputDir, outcome.RevisedJSON); err == nil {
			run.ContinuousScript = script
			run.PackageJSON = outcome.RevisedJSON
			run.TitlesJSON = titlesFromDraftJSON(outcome.RevisedJSON)
		} else {
			slog.Default().Error("remix lab rework write files failed", "run_id", run.ID, "error", err)
		}
	}
	finished := s.now()
	run.Status = "completed"
	run.FinishedAt = &finished
	if err := s.repo.UpdateRun(ctx, run); err != nil {
		slog.Default().Error("remix lab rework UpdateRun failed", "run_id", run.ID, "error", err)
	}
	// 打回完成后如果还没有生产记录（老运行），补一条等待确认的闸门。
	s.afterWorkflowRunCompleted(ctx, exp, run)
}

func nextReviewRound(reviewJSON string) int {
	var record struct {
		Round int `json:"round"`
	}
	if err := json.Unmarshal([]byte(reviewJSON), &record); err == nil && record.Round > 0 {
		return record.Round + 1
	}
	// 首轮自动审稿是第1轮；没有留下记录时，打回按第2轮记。
	return 2
}

func titlesFromDraftJSON(draftJSON string) string {
	var draft struct {
		Titles []string `json:"titles"`
	}
	if err := json.Unmarshal([]byte(draftJSON), &draft); err != nil || draft.Titles == nil {
		return "[]"
	}
	raw, err := json.Marshal(draft.Titles)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

func cleanStringList(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		out = append(out, item)
	}
	return out
}
