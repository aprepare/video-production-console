package remixlab

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"video-production-console/internal/agentruntime/openaicompat"
	"video-production-console/internal/domain"
	"video-production-console/internal/security"
	"video-production-console/internal/store"
)

var (
	ErrInvalidSource    = errors.New("source must be 1 to 20000 characters")
	ErrInvalidSlots     = errors.New("slots must be 1 to 4")
	ErrInvalidPrompts   = errors.New("prompt_ids must be 1 to 6 known prompts")
	ErrInvalidRunCount  = errors.New("run_count must be 1 to 3")
	ErrInvalidPipeline  = errors.New("pipeline must be empty, single, or multi_agent")
	ErrMissingModel     = errors.New("model is required")
	ErrMissingAPIKey    = errors.New("remix api key is not configured")
	ErrInvalidComment   = errors.New("comment must be at most 2000 characters")
	ErrRunNotAdoptable  = errors.New("run is not adoptable")
	ErrAdoptUnavailable = errors.New("adopt is not configured")
)

type presetFile struct {
	Slots []presetSlot `json:"slots"`
}

type presetSlot struct {
	ServiceTier      string `json:"service_tier,omitempty"`
	BaseURL          string `json:"base_url"`
	Model            string `json:"model"`
	ReasoningEffort  string `json:"reasoning_effort"`
	Pipeline         string `json:"pipeline,omitempty"`
	RunCount         int    `json:"run_count"`
	APIKeyCiphertext string `json:"api_key_ciphertext"`
}

type resolvedSlot struct {
	ServiceTier      string
	BaseURL          string
	Model            string
	ReasoningEffort  string
	Pipeline         string
	RunCount         int
	APIKeyCiphertext string
	KeyConfigured    bool
}

func NewService(repo *store.RemixLabRepository, runtime RuntimeProvider, protector security.Protector, dataRoot string, runner Runner, adopter AssetAdopter, binder ProjectBinder) *Service {
	if runner == nil {
		runner = func(_ context.Context, opts openaicompat.Options) error {
			return openaicompat.Run(opts)
		}
	}
	return &Service{
		repo:      repo,
		runtime:   runtime,
		protector: protector,
		dataRoot:  dataRoot,
		runner:    runner,
		adopter:   adopter,
		binder:    binder,
		now:       func() time.Time { return time.Now().UTC() },
	}
}

func (s *Service) Adopt(ctx context.Context, runID, projectID string) error {
	if s.adopter == nil || s.binder == nil {
		return ErrAdoptUnavailable
	}
	run, err := s.repo.GetRun(ctx, runID)
	if err != nil {
		return err
	}
	if run.Status != "completed" || strings.TrimSpace(run.ContinuousScript) == "" {
		return ErrRunNotAdoptable
	}
	if _, err := s.binder.GetProject(ctx, projectID); err != nil {
		return err
	}
	const filename = "continuous-script.txt"
	saved, err := s.adopter.SaveProjectAsset(projectID, domain.AssetContinuousScript, filename, strings.NewReader(run.ContinuousScript))
	if err != nil {
		return err
	}
	now := s.now()
	asset := &domain.Asset{
		ID:        uuid.NewString(),
		ProjectID: &projectID,
		Type:      domain.AssetContinuousScript,
		Path:      saved.Path,
		Filename:  filename,
		MIMEType:  saved.MIMEType,
		Size:      saved.Size,
		SHA256:    saved.SHA256,
		Status:    "active",
		CreatedAt: now,
	}
	if _, err := s.binder.AddAsset(ctx, asset); err != nil {
		return err
	}
	return s.repo.UpdateRunAdoptedProject(ctx, run.ID, projectID)
}

func (s *Service) Defaults(ctx context.Context) (DefaultsView, error) {
	rt, err := s.runtime.Runtime(ctx)
	if err != nil {
		return DefaultsView{}, err
	}
	view := DefaultsView{
		RemixBaseURL:          rt.RemixBaseURL,
		RemixModel:            rt.RemixModel,
		RemixReasoningEffort:  rt.RemixReasoningEffort,
		RemixAPIKeyConfigured: strings.TrimSpace(rt.RemixAPIKey) != "",
		Presets:               []PresetSlotView{},
	}
	raw, err := s.repo.GetPresetJSON(ctx)
	if err != nil {
		return DefaultsView{}, err
	}
	if strings.TrimSpace(raw) == "" {
		return view, nil
	}
	var file presetFile
	if err := json.Unmarshal([]byte(raw), &file); err != nil {
		return DefaultsView{}, err
	}
	for i, slot := range file.Slots {
		runCount := slot.RunCount
		if runCount == 0 {
			runCount = 1
		}
		view.Presets = append(view.Presets, PresetSlotView{
			ServiceTier:      slot.ServiceTier,
			BaseURL:          slot.BaseURL,
			Model:            slot.Model,
			ReasoningEffort:  slot.ReasoningEffort,
			Pipeline:         slot.Pipeline,
			RunCount:         runCount,
			APIKeyConfigured: strings.TrimSpace(slot.APIKeyCiphertext) != "",
			PresetIndex:      i,
		})
	}
	return view, nil
}

func (s *Service) resolvePromptIDs(ids []string) ([]PromptTemplate, error) {
	if len(ids) == 0 {
		ids = []string{"elder_stable"}
	}
	if len(ids) > 6 {
		return nil, ErrInvalidPrompts
	}
	lib := Store{DataRoot: s.dataRoot}
	out := make([]PromptTemplate, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		p, ok, err := lib.GetPrompt(id)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrInvalidPrompts
		}
		seen[id] = true
		out = append(out, p)
	}
	if len(out) < 1 {
		return nil, ErrInvalidPrompts
	}
	return out, nil
}

func (s *Service) CreateExperiment(ctx context.Context, source string, slots []SlotInput) (Experiment, error) {
	return s.CreateExperimentWithPrompts(ctx, source, slots, nil)
}

func (s *Service) CreateExperimentWithPrompts(ctx context.Context, source string, slots []SlotInput, promptIDs []string) (Experiment, error) {
	n := utf8.RuneCountInString(source)
	if n < 1 || n > 20000 {
		return Experiment{}, ErrInvalidSource
	}
	if len(slots) < 1 || len(slots) > 4 {
		return Experiment{}, ErrInvalidSlots
	}
	prompts, err := s.resolvePromptIDs(promptIDs)
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

	resolved := make([]resolvedSlot, 0, len(slots))
	for _, in := range slots {
		slot, err := s.resolveSlot(in, rt, preset)
		if err != nil {
			return Experiment{}, err
		}
		resolved = append(resolved, slot)
	}

	now := s.now()
	expID := uuid.NewString()
	title := experimentTitle(source, now)
	stamp := experimentStamp(prompts)

	expRec := store.RemixLabExperimentRecord{
		ID:          expID,
		Title:       title,
		SourceText:  source,
		PromptStamp: stamp,
		Status:      "running",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	slotRecs := make([]store.RemixLabSlotRecord, 0, len(resolved))
	runRecs := make([]store.RemixLabRunRecord, 0)
	slotViews := make([]SlotView, 0, len(resolved))
	runViews := make([]RunView, 0)

	for i, slot := range resolved {
		slotID := uuid.NewString()
		slotRecs = append(slotRecs, store.RemixLabSlotRecord{
			ServiceTier:      slot.ServiceTier,
			ID:               slotID,
			ExperimentID:     expID,
			SortIndex:        i,
			Label:            slot.Model,
			BaseURL:          slot.BaseURL,
			Model:            slot.Model,
			ReasoningEffort:  slot.ReasoningEffort,
			Pipeline:         slot.Pipeline,
			RunCount:         slot.RunCount,
			APIKeyCiphertext: slot.APIKeyCiphertext,
		})
		slotViews = append(slotViews, SlotView{
			ServiceTier:      slot.ServiceTier,
			ID:               slotID,
			ExperimentID:     expID,
			SortIndex:        i,
			Label:            slot.Model,
			BaseURL:          slot.BaseURL,
			Model:            slot.Model,
			ReasoningEffort:  slot.ReasoningEffort,
			Pipeline:         slot.Pipeline,
			RunCount:         slot.RunCount,
			APIKeyConfigured: slot.KeyConfigured,
		})
		seq := 0
		for _, prompt := range prompts {
			for range slot.RunCount {
				seq++
				runID := uuid.NewString()
				runRecs = append(runRecs, store.RemixLabRunRecord{
					ID:           runID,
					ExperimentID: expID,
					SlotID:       slotID,
					RunIndex:     seq,
					Status:       "queued",
					PromptID:     prompt.ID,
					PromptStamp:  prompt.Stamp,
					PromptName:   prompt.Name,
				})
				runViews = append(runViews, RunView{
					ID:           runID,
					ExperimentID: expID,
					SlotID:       slotID,
					RunIndex:     seq,
					Status:       "queued",
					PromptID:     prompt.ID,
					PromptStamp:  prompt.Stamp,
					PromptName:   prompt.Name,
				})
			}
		}
	}

	if err := s.repo.CreateExperiment(ctx, expRec, slotRecs, runRecs); err != nil {
		return Experiment{}, err
	}
	if err := s.savePreset(ctx, resolved); err != nil {
		slog.Default().Error("remix lab savePreset failed", "experiment_id", expID, "error", err)
	}

	go s.drive(expID)

	return Experiment{
		ID:          expID,
		Title:       title,
		SourceText:  source,
		PromptStamp: stamp,
		Status:      "running",
		CreatedAt:   now,
		UpdatedAt:   now,
		Slots:       slotViews,
		Runs:        runViews,
	}, nil
}

// normalizePipeline 校验槽位管线取值："" 与 "single" 都归一为单模型。
func normalizePipeline(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "single":
		return "", nil
	case openaicompat.PipelineMultiAgent:
		return openaicompat.PipelineMultiAgent, nil
	default:
		return "", ErrInvalidPipeline
	}
}

func (s *Service) resolveSlot(in SlotInput, rt RuntimeView, preset presetFile) (resolvedSlot, error) {
	tier, err := openaicompat.NormalizeServiceTier(in.ServiceTier)
	if err != nil {
		return resolvedSlot{}, err
	}
	model := strings.TrimSpace(in.Model)
	if model == "" {
		return resolvedSlot{}, ErrMissingModel
	}
	runCount := in.RunCount
	if runCount == 0 {
		runCount = 1
	}
	if runCount < 1 || runCount > 3 {
		return resolvedSlot{}, ErrInvalidRunCount
	}

	baseURL := strings.TrimSpace(in.BaseURL)
	if baseURL == "" {
		baseURL = strings.TrimSpace(rt.RemixBaseURL)
	}
	pipeline, err := normalizePipeline(in.Pipeline)
	if err != nil {
		return resolvedSlot{}, err
	}

	out := resolvedSlot{
		ServiceTier:     tier,
		BaseURL:         baseURL,
		Model:           model,
		ReasoningEffort: strings.TrimSpace(in.ReasoningEffort),
		Pipeline:        pipeline,
		RunCount:        runCount,
	}

	apiKey := strings.TrimSpace(in.APIKey)
	switch {
	case apiKey != "":
		cipher, err := s.protector.Protect([]byte(apiKey))
		if err != nil {
			return resolvedSlot{}, err
		}
		out.APIKeyCiphertext = base64.StdEncoding.EncodeToString(cipher)
		out.KeyConfigured = true
	case in.PresetIndex != nil:
		idx := *in.PresetIndex
		if idx >= 0 && idx < len(preset.Slots) {
			out.APIKeyCiphertext = preset.Slots[idx].APIKeyCiphertext
		}
		if strings.TrimSpace(out.APIKeyCiphertext) != "" {
			out.KeyConfigured = true
			return out, nil
		}
		// Empty preset ciphertext falls through to Runtime remix_api_key.
		fallthrough
	default:
		if strings.TrimSpace(rt.RemixAPIKey) == "" {
			return resolvedSlot{}, ErrMissingAPIKey
		}
		out.APIKeyCiphertext = ""
		out.KeyConfigured = true
	}
	return out, nil
}

// SavePresets 把弹窗里的槽位草稿直接持久化为预设（点「完成」即保存），
// 不要求配置 API Key——预设可以留空，跑实验时回落全局 remix key。
func (s *Service) SavePresets(ctx context.Context, slots []SlotInput) (DefaultsView, error) {
	if len(slots) < 1 || len(slots) > 4 {
		return DefaultsView{}, ErrInvalidSlots
	}
	rt, err := s.runtime.Runtime(ctx)
	if err != nil {
		return DefaultsView{}, err
	}
	preset, err := s.loadPreset(ctx)
	if err != nil {
		return DefaultsView{}, err
	}
	resolved := make([]resolvedSlot, 0, len(slots))
	for _, in := range slots {
		slot, err := s.resolvePresetSlot(in, rt, preset)
		if err != nil {
			return DefaultsView{}, err
		}
		resolved = append(resolved, slot)
	}
	if err := s.savePreset(ctx, resolved); err != nil {
		return DefaultsView{}, err
	}
	return s.Defaults(ctx)
}

// resolvePresetSlot 与 resolveSlot 的差别只有一处：不强制要有 API Key。
func (s *Service) resolvePresetSlot(in SlotInput, rt RuntimeView, preset presetFile) (resolvedSlot, error) {
	tier, err := openaicompat.NormalizeServiceTier(in.ServiceTier)
	if err != nil {
		return resolvedSlot{}, err
	}
	model := strings.TrimSpace(in.Model)
	if model == "" {
		return resolvedSlot{}, ErrMissingModel
	}
	runCount := in.RunCount
	if runCount == 0 {
		runCount = 1
	}
	if runCount < 1 || runCount > 3 {
		return resolvedSlot{}, ErrInvalidRunCount
	}
	baseURL := strings.TrimSpace(in.BaseURL)
	if baseURL == "" {
		baseURL = strings.TrimSpace(rt.RemixBaseURL)
	}
	pipeline, err := normalizePipeline(in.Pipeline)
	if err != nil {
		return resolvedSlot{}, err
	}
	out := resolvedSlot{
		BaseURL:         baseURL,
		Model:           model,
		ReasoningEffort: strings.TrimSpace(in.ReasoningEffort),
		ServiceTier:     tier,
		Pipeline:        pipeline,
		RunCount:        runCount,
	}
	apiKey := strings.TrimSpace(in.APIKey)
	switch {
	case apiKey != "":
		cipher, err := s.protector.Protect([]byte(apiKey))
		if err != nil {
			return resolvedSlot{}, err
		}
		out.APIKeyCiphertext = base64.StdEncoding.EncodeToString(cipher)
	case in.PresetIndex != nil:
		idx := *in.PresetIndex
		if idx >= 0 && idx < len(preset.Slots) {
			out.APIKeyCiphertext = preset.Slots[idx].APIKeyCiphertext
		}
	}
	out.KeyConfigured = strings.TrimSpace(out.APIKeyCiphertext) != "" || strings.TrimSpace(rt.RemixAPIKey) != ""
	return out, nil
}

// remixDefaults 是设置页/模型档默认，给审稿等节点在自身配置留空时用，
// 不跟写手槽位走。
func (s *Service) remixDefaults(ctx context.Context, rt RuntimeView, rtErr error) (model, effort string) {
	if rtErr == nil {
		model = strings.TrimSpace(rt.RemixModel)
		effort = strings.TrimSpace(rt.RemixReasoningEffort)
	}
	preset, err := s.loadPreset(ctx)
	if err == nil && len(preset.Slots) > 0 {
		if m := strings.TrimSpace(preset.Slots[0].Model); m != "" {
			model = m
		}
		if e := strings.TrimSpace(preset.Slots[0].ReasoningEffort); e != "" {
			effort = e
		}
	}
	return
}

func (s *Service) loadPreset(ctx context.Context) (presetFile, error) {
	raw, err := s.repo.GetPresetJSON(ctx)
	if err != nil {
		return presetFile{}, err
	}
	if strings.TrimSpace(raw) == "" {
		return presetFile{Slots: []presetSlot{}}, nil
	}
	var file presetFile
	if err := json.Unmarshal([]byte(raw), &file); err != nil {
		return presetFile{}, err
	}
	if file.Slots == nil {
		file.Slots = []presetSlot{}
	}
	return file, nil
}

func (s *Service) savePreset(ctx context.Context, slots []resolvedSlot) error {
	file := presetFile{Slots: make([]presetSlot, 0, len(slots))}
	for _, slot := range slots {
		file.Slots = append(file.Slots, presetSlot{
			ServiceTier:      slot.ServiceTier,
			BaseURL:          slot.BaseURL,
			Model:            slot.Model,
			ReasoningEffort:  slot.ReasoningEffort,
			Pipeline:         slot.Pipeline,
			RunCount:         slot.RunCount,
			APIKeyCiphertext: slot.APIKeyCiphertext,
		})
	}
	raw, err := json.Marshal(file)
	if err != nil {
		return err
	}
	if s.putPresetJSON != nil {
		return s.putPresetJSON(ctx, string(raw))
	}
	return s.repo.PutPresetJSON(ctx, string(raw))
}

func experimentStamp(prompts []PromptTemplate) string {
	if len(prompts) == 1 && strings.TrimSpace(prompts[0].Stamp) != "" {
		return prompts[0].Stamp
	}
	if len(prompts) > 1 {
		return "交叉试验"
	}
	return openaicompat.RewritePromptStampStable
}

func experimentTitle(source string, now time.Time) string {
	flat := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, source)
	runes := []rune(flat)
	if len(runes) == 0 {
		return "实验" + now.Format("01-02 15:04")
	}
	if len(runes) > 24 {
		runes = runes[:24]
	}
	return string(runes)
}
