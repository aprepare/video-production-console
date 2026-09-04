package remixlab

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"video-production-console/internal/store"
)

// CreateWorkflowExperiment 用当前工作流开跑：快照存进实验，运行按节点图执行。
// 模型档取 models 显式指定 > 写手节点覆盖 > 模型配置预设槽1 > 全局二创设置；
// models 给多个时每个模型各占一个槽、各跑 runCount 次，同一篇原文并行出稿
// 供快慢/质量对比。写手提示词取写手节点指定的库条目，空则用当前日产。
// produceAccountID/auto 是生产段参数：账号决定定稿进哪个号的混剪；auto=true
// 时跳过确认闸门直接一步到剪映草稿（要求已选账号且总运行数=1）。
func (s *Service) CreateWorkflowExperiment(ctx context.Context, source string, runCount int, produceAccountID string, auto bool, models ...string) (Experiment, error) {
	n := utf8.RuneCountInString(source)
	if n < 1 || n > 20000 {
		return Experiment{}, ErrInvalidSource
	}
	if runCount == 0 {
		runCount = 1
	}
	if runCount < 1 || runCount > 3 {
		return Experiment{}, ErrInvalidRunCount
	}
	cleanModels := make([]string, 0, len(models))
	seen := map[string]bool{}
	for _, m := range models {
		m = strings.TrimSpace(m)
		if m == "" || seen[m] {
			continue
		}
		seen[m] = true
		cleanModels = append(cleanModels, m)
	}
	if len(cleanModels) > 4 {
		return Experiment{}, ErrInvalidRunCount
	}
	if len(cleanModels) == 0 {
		cleanModels = []string{""} // 一个槽，走默认档
	}
	if err := validateProduceOptions(produceAccountID, auto, runCount*len(cleanModels)); err != nil {
		return Experiment{}, err
	}
	wf, err := s.WorkflowForAccount(produceAccountID)
	if err != nil {
		return Experiment{}, err
	}
	if err := ValidateWorkflow(wf); err != nil {
		return Experiment{}, err
	}
	snapshot, err := json.Marshal(wf)
	if err != nil {
		return Experiment{}, err
	}

	rt, err := s.runtime.Runtime(ctx)
	if err != nil {
		return Experiment{}, err
	}
	preset, err := s.loadPreset(ctx)
	if err != nil {
		return Experiment{}, err
	}

	in := SlotInput{RunCount: runCount}
	if len(preset.Slots) > 0 {
		zero := 0
		in.PresetIndex = &zero
		in.BaseURL = preset.Slots[0].BaseURL
		in.Model = preset.Slots[0].Model
		in.ReasoningEffort = preset.Slots[0].ReasoningEffort
	} else {
		in.BaseURL = strings.TrimSpace(rt.RemixBaseURL)
		in.Model = strings.TrimSpace(rt.RemixModel)
		in.ReasoningEffort = strings.TrimSpace(rt.RemixReasoningEffort)
	}
	writerNode := workflowWriter(wf)
	if writerNode != nil {
		if m := strings.TrimSpace(writerNode.Config.Model); m != "" {
			in.Model = m
		}
		if e := strings.TrimSpace(writerNode.Config.ReasoningEffort); e != "" {
			in.ReasoningEffort = e
		}
	}
	// 每个显式模型解析成一个槽；空字符串表示沿用上面算出的默认档。
	slots := make([]resolvedSlot, 0, len(cleanModels))
	for _, m := range cleanModels {
		slotIn := in
		if m != "" {
			slotIn.Model = m
		}
		slot, err := s.resolveSlot(slotIn, rt, preset)
		if err != nil {
			return Experiment{}, err
		}
		slots = append(slots, slot)
	}

	// 写手提示词：节点指定 > 当前日产 > 编译默认。
	libStore := Store{DataRoot: s.dataRoot}
	promptID := ""
	if writerNode != nil {
		promptID = strings.TrimSpace(writerNode.Config.PromptID)
	}
	if promptID == "" {
		if active, ok, err := libStore.GetActive(); err == nil && ok {
			promptID = active.ID
		}
	}
	if promptID == "" {
		promptID = "elder_stable"
	}
	prompt, found, err := libStore.GetPrompt(promptID)
	if err != nil {
		return Experiment{}, err
	}
	if !found {
		prompt = ResolvePrompt("elder_stable", "", "")
	}

	now := s.now()
	expID := uuid.NewString()
	title := experimentTitle(source, now)
	expRec := store.RemixLabExperimentRecord{
		ID:               expID,
		Title:            title,
		SourceText:       source,
		PromptStamp:      prompt.Stamp,
		Status:           "running",
		WorkflowJSON:     string(snapshot),
		ProduceAccountID: strings.TrimSpace(produceAccountID),
		ProduceAuto:      auto,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	slotRecs := make([]store.RemixLabSlotRecord, 0, len(slots))
	slotViews := make([]SlotView, 0, len(slots))
	runRecs := make([]store.RemixLabRunRecord, 0, runCount*len(slots))
	runViews := make([]RunView, 0, runCount*len(slots))
	for index, slot := range slots {
		slotID := uuid.NewString()
		slotRec := store.RemixLabSlotRecord{
			ID:               slotID,
			ExperimentID:     expID,
			SortIndex:        index,
			Label:            "工作流 · " + slot.Model,
			BaseURL:          slot.BaseURL,
			Model:            slot.Model,
			ReasoningEffort:  slot.ReasoningEffort,
			Pipeline:         "",
			RunCount:         runCount,
			APIKeyCiphertext: slot.APIKeyCiphertext,
		}
		slotRecs = append(slotRecs, slotRec)
		slotViews = append(slotViews, SlotView{
			ID: slotID, ExperimentID: expID, SortIndex: index, Label: slotRec.Label,
			BaseURL: slot.BaseURL, Model: slot.Model, ReasoningEffort: slot.ReasoningEffort,
			RunCount: runCount, APIKeyConfigured: slot.KeyConfigured,
		})
		for seq := 1; seq <= runCount; seq++ {
			runID := uuid.NewString()
			runRecs = append(runRecs, store.RemixLabRunRecord{
				ID: runID, ExperimentID: expID, SlotID: slotID, RunIndex: seq,
				Status: "queued", PromptID: prompt.ID, PromptStamp: prompt.Stamp, PromptName: prompt.Name,
			})
			runViews = append(runViews, RunView{
				ID: runID, ExperimentID: expID, SlotID: slotID, RunIndex: seq,
				Status: "queued", PromptID: prompt.ID, PromptStamp: prompt.Stamp, PromptName: prompt.Name,
			})
		}
	}
	if err := s.repo.CreateExperiment(ctx, expRec, slotRecs, runRecs); err != nil {
		return Experiment{}, err
	}
	go s.drive(expID)

	slog.Default().Info("remix lab workflow experiment created", "experiment_id", expID, "models", len(slots), "runs", len(runRecs))
	return Experiment{
		ID:          expID,
		Title:       title,
		SourceText:  source,
		PromptStamp: prompt.Stamp,
		Status:      "running",
		Workflow:    true,
		CreatedAt:   now,
		UpdatedAt:   now,
		Slots:       slotViews,
		Runs:        runViews,
	}, nil
}
