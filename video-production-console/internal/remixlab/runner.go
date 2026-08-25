package remixlab

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/sync/semaphore"

	"video-production-console/internal/agentruntime/openaicompat"
	"video-production-console/internal/store"
)

type ExperimentSummary struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	PromptStamp string    `json:"prompt_stamp"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (s *Service) GetExperiment(ctx context.Context, id string) (Experiment, error) {
	exp, slots, runs, err := s.repo.GetExperiment(ctx, id)
	if err != nil {
		return Experiment{}, err
	}
	return mapExperiment(exp, slots, runs), nil
}

func (s *Service) ListExperiments(ctx context.Context) ([]ExperimentSummary, error) {
	rows, err := s.repo.ListExperiments(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ExperimentSummary, 0, len(rows))
	for _, exp := range rows {
		out = append(out, ExperimentSummary{
			ID:          exp.ID,
			Title:       exp.Title,
			PromptStamp: exp.PromptStamp,
			Status:      exp.Status,
			CreatedAt:   exp.CreatedAt,
			UpdatedAt:   exp.UpdatedAt,
		})
	}
	return out, nil
}

func (s *Service) PatchComment(ctx context.Context, runID, comment string) error {
	if utf8.RuneCountInString(comment) > 2000 {
		return ErrInvalidComment
	}
	if _, err := s.repo.GetRun(ctx, runID); err != nil {
		return err
	}
	return s.repo.UpdateRunComment(ctx, runID, comment)
}

func (s *Service) FailStale(ctx context.Context) error {
	_, err := s.repo.FailNonTerminal(ctx, "控制台已重启", s.now())
	return err
}

func (s *Service) drive(expID string) {
	ctx := context.Background()
	exp, slots, runs, err := s.repo.GetExperiment(ctx, expID)
	if err != nil {
		return
	}
	slotByID := make(map[string]store.RemixLabSlotRecord, len(slots))
	for _, slot := range slots {
		slotByID[slot.ID] = slot
	}

	rt, rtErr := s.runtime.Runtime(ctx)
	queued := 0
	for _, run := range runs {
		if run.Status == "queued" {
			queued++
		}
	}
	if queued < 1 {
		queued = 1
	}
	sem := semaphore.NewWeighted(int64(queued))
	var wg sync.WaitGroup
	for _, run := range runs {
		if run.Status != "queued" {
			continue
		}
		run := run
		slot := slotByID[run.SlotID]
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := sem.Acquire(ctx, 1); err != nil {
				return
			}
			defer sem.Release(1)
			s.executeRun(ctx, exp, slot, run, rt, rtErr)
		}()
	}
	wg.Wait()
}

func (s *Service) executeRun(ctx context.Context, exp store.RemixLabExperimentRecord, slot store.RemixLabSlotRecord, run store.RemixLabRunRecord, rt RuntimeView, rtErr error) {
	now := s.now()
	started := now
	run.Status = "running"
	run.StartedAt = &started
	run.OutputDir = filepath.Join(s.dataRoot, "remix-lab", exp.ID, run.ID)
	if err := s.repo.UpdateRun(ctx, run); err != nil {
		slog.Default().Error("remix lab start UpdateRun failed", "experiment_id", exp.ID, "run_id", run.ID, "error", err)
		finished := s.now()
		run.Status = "failed"
		run.ErrorMessage = "failed to persist run start"
		run.FinishedAt = &finished
		if persistErr := s.repo.UpdateRun(ctx, run); persistErr != nil {
			slog.Default().Error("remix lab failed to mark run failed after start UpdateRun error", "experiment_id", exp.ID, "run_id", run.ID, "error", persistErr)
		}
		return
	}

	finish := func(status, script, titlesJSON, errMsg string) {
		finished := s.now()
		run.Status = status
		run.ContinuousScript = script
		run.TitlesJSON = titlesJSON
		run.ErrorMessage = errMsg
		run.FinishedAt = &finished
		if err := s.repo.UpdateRun(ctx, run); err != nil {
			slog.Default().Error("remix lab finish UpdateRun failed", "experiment_id", exp.ID, "run_id", run.ID, "status", status, "error", err)
			run.Status = "failed"
			run.ContinuousScript = ""
			run.TitlesJSON = ""
			run.ErrorMessage = "failed to persist run result"
			run.FinishedAt = &finished
			if persistErr := s.repo.UpdateRun(ctx, run); persistErr != nil {
				slog.Default().Error("remix lab failed to mark run failed after finish UpdateRun error", "experiment_id", exp.ID, "run_id", run.ID, "error", persistErr)
			}
		}
	}

	if err := os.MkdirAll(run.OutputDir, 0o755); err != nil {
		finish("failed", "", "", err.Error())
		return
	}
	sourcePath := filepath.Join(run.OutputDir, "source.txt")
	if err := os.WriteFile(sourcePath, []byte(exp.SourceText), 0o644); err != nil {
		finish("failed", "", "", err.Error())
		return
	}
	manifestPath := filepath.Join(run.OutputDir, "task_manifest.json")
	manifest := map[string]any{
		"action":     "remix.standard",
		"output_dir": run.OutputDir,
		"inputs": []map[string]string{
			{"type": "source_script", "path": sourcePath},
		},
		"non_secret_settings": map[string]string{
			"remix_prompt_style": "rewrite",
		},
	}
	rawManifest, err := json.Marshal(manifest)
	if err != nil {
		finish("failed", "", "", err.Error())
		return
	}
	if err := os.WriteFile(manifestPath, rawManifest, 0o644); err != nil {
		finish("failed", "", "", err.Error())
		return
	}

	resolvedURL := strings.TrimSpace(slot.BaseURL)
	resolvedKey, keyErr := s.resolveRunKey(slot, rt, rtErr)
	if keyErr != nil {
		finish("failed", "", "", scrubSecret(keyErr.Error(), resolvedKey))
		return
	}

	opts := openaicompat.Options{
		ManifestPath:      manifestPath,
		OutputLastMessage: filepath.Join(run.OutputDir, "last.json"),
		Model:             slot.Model,
		ReasoningEffort:   slot.ReasoningEffort,
		BaseURL:           resolvedURL,
		APIKey:            resolvedKey,
	}
	if err := s.runner(ctx, opts); err != nil {
		finish("failed", "", "", scrubSecret(err.Error(), resolvedKey))
		return
	}

	scriptBytes, err := os.ReadFile(filepath.Join(run.OutputDir, "continuous_script.txt"))
	if err != nil {
		finish("failed", "", "", scrubSecret(err.Error(), resolvedKey))
		return
	}
	titlesJSON := readTitlesJSON(filepath.Join(run.OutputDir, "publishing_package.json"))
	finish("completed", string(scriptBytes), titlesJSON, "")
}

func (s *Service) resolveRunKey(slot store.RemixLabSlotRecord, rt RuntimeView, rtErr error) (string, error) {
	cipher := strings.TrimSpace(slot.APIKeyCiphertext)
	if cipher != "" {
		raw, err := base64.StdEncoding.DecodeString(cipher)
		if err != nil {
			return "", err
		}
		plain, err := s.protector.Unprotect(raw)
		if err != nil {
			return "", err
		}
		return string(plain), nil
	}
	if rtErr != nil {
		return "", rtErr
	}
	key := strings.TrimSpace(rt.RemixAPIKey)
	if key == "" {
		return "", ErrMissingAPIKey
	}
	return key, nil
}

func readTitlesJSON(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var pkg struct {
		Titles []string `json:"titles"`
	}
	if err := json.Unmarshal(raw, &pkg); err != nil {
		return ""
	}
	if pkg.Titles == nil {
		return "[]"
	}
	encoded, err := json.Marshal(pkg.Titles)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func scrubSecret(msg, secret string) string {
	if secret == "" || !strings.Contains(msg, secret) {
		return msg
	}
	return strings.ReplaceAll(msg, secret, "***")
}

func mapExperiment(exp store.RemixLabExperimentRecord, slots []store.RemixLabSlotRecord, runs []store.RemixLabRunRecord) Experiment {
	slotViews := make([]SlotView, 0, len(slots))
	for _, slot := range slots {
		slotViews = append(slotViews, SlotView{
			ID:               slot.ID,
			ExperimentID:     slot.ExperimentID,
			SortIndex:        slot.SortIndex,
			Label:            slot.Label,
			BaseURL:          slot.BaseURL,
			Model:            slot.Model,
			ReasoningEffort:  slot.ReasoningEffort,
			RunCount:         slot.RunCount,
			APIKeyConfigured: true,
		})
	}
	runViews := make([]RunView, 0, len(runs))
	for _, run := range runs {
		runViews = append(runViews, RunView{
			ID:               run.ID,
			ExperimentID:     run.ExperimentID,
			SlotID:           run.SlotID,
			RunIndex:         run.RunIndex,
			Status:           run.Status,
			ContinuousScript: run.ContinuousScript,
			TitlesJSON:       run.TitlesJSON,
			ErrorMessage:     run.ErrorMessage,
			Comment:          run.Comment,
			OutputDir:        run.OutputDir,
			AdoptedProjectID: run.AdoptedProjectID,
			StartedAt:        run.StartedAt,
			FinishedAt:       run.FinishedAt,
		})
	}
	return Experiment{
		ID:          exp.ID,
		Title:       exp.Title,
		SourceText:  exp.SourceText,
		PromptStamp: exp.PromptStamp,
		Status:      exp.Status,
		CreatedAt:   exp.CreatedAt,
		UpdatedAt:   exp.UpdatedAt,
		Slots:       slotViews,
		Runs:        runViews,
	}
}
