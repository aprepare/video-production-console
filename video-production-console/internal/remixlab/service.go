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
	ErrInvalidRunCount  = errors.New("run_count must be 1 to 3")
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
	BaseURL           string `json:"base_url"`
	Model             string `json:"model"`
	ReasoningEffort   string `json:"reasoning_effort"`
	RunCount          int    `json:"run_count"`
	APIKeyCiphertext  string `json:"api_key_ciphertext"`
}

type resolvedSlot struct {
	BaseURL          string
	Model            string
	ReasoningEffort  string
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
			BaseURL:          slot.BaseURL,
			Model:            slot.Model,
			ReasoningEffort:  slot.ReasoningEffort,
			RunCount:         runCount,
			APIKeyConfigured: strings.TrimSpace(slot.APIKeyCiphertext) != "",
			PresetIndex:      i,
		})
	}
	return view, nil
}

func (s *Service) CreateExperiment(ctx context.Context, source string, slots []SlotInput) (Experiment, error) {
	n := utf8.RuneCountInString(source)
	if n < 1 || n > 20000 {
		return Experiment{}, ErrInvalidSource
	}
	if len(slots) < 1 || len(slots) > 4 {
		return Experiment{}, ErrInvalidSlots
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
	stamp := openaicompat.RewritePromptStampStable

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
			ID:               slotID,
			ExperimentID:     expID,
			SortIndex:        i,
			Label:            slot.Model,
			BaseURL:          slot.BaseURL,
			Model:            slot.Model,
			ReasoningEffort:  slot.ReasoningEffort,
			RunCount:         slot.RunCount,
			APIKeyCiphertext: slot.APIKeyCiphertext,
		})
		slotViews = append(slotViews, SlotView{
			ID:               slotID,
			ExperimentID:     expID,
			SortIndex:        i,
			Label:            slot.Model,
			BaseURL:          slot.BaseURL,
			Model:            slot.Model,
			ReasoningEffort:  slot.ReasoningEffort,
			RunCount:         slot.RunCount,
			APIKeyConfigured: slot.KeyConfigured,
		})
		for runIndex := 1; runIndex <= slot.RunCount; runIndex++ {
			runID := uuid.NewString()
			runRecs = append(runRecs, store.RemixLabRunRecord{
				ID:           runID,
				ExperimentID: expID,
				SlotID:       slotID,
				RunIndex:     runIndex,
				Status:       "queued",
			})
			runViews = append(runViews, RunView{
				ID:           runID,
				ExperimentID: expID,
				SlotID:       slotID,
				RunIndex:     runIndex,
				Status:       "queued",
			})
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

func (s *Service) resolveSlot(in SlotInput, rt RuntimeView, preset presetFile) (resolvedSlot, error) {
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

	out := resolvedSlot{
		BaseURL:         baseURL,
		Model:           model,
		ReasoningEffort: strings.TrimSpace(in.ReasoningEffort),
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
			BaseURL:          slot.BaseURL,
			Model:            slot.Model,
			ReasoningEffort:  slot.ReasoningEffort,
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
