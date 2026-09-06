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
	ID          string `json:"id"`
	Title       string `json:"title"`
	PromptStamp string `json:"prompt_stamp"`
	Status      string `json:"status"`
	// AccountID 是开跑时选的生产账号（空=挂在全局默认工作流下），
	// 历史栏「最近实验」按它过滤到当前账号。
	AccountID string    `json:"account_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	// 生产段（混剪）进度：历史栏把二创和混剪合成一条内容展示时用。
	// 多稿实验取最近更新的那条生产记录。
	ProductionStatus string `json:"production_status,omitempty"` // waiting_confirm / running / completed / failed
	ProductionStep   string `json:"production_step,omitempty"`
	ProjectID        string `json:"project_id,omitempty"`
}

func (s *Service) GetExperiment(ctx context.Context, id string) (Experiment, error) {
	exp, slots, runs, err := s.repo.GetExperiment(ctx, id)
	if err != nil {
		return Experiment{}, err
	}
	productions, err := s.repo.ListProductionsByExperiment(ctx, id)
	if err != nil {
		return Experiment{}, err
	}
	return mapExperiment(exp, slots, runs, productions), nil
}

func (s *Service) ListExperiments(ctx context.Context) ([]ExperimentSummary, error) {
	rows, err := s.repo.ListExperiments(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ExperimentSummary, 0, len(rows))
	for _, exp := range rows {
		summary := ExperimentSummary{
			ID:          exp.ID,
			Title:       exp.Title,
			PromptStamp: exp.PromptStamp,
			Status:      exp.Status,
			AccountID:   strings.TrimSpace(exp.ProduceAccountID),
			CreatedAt:   exp.CreatedAt,
			UpdatedAt:   exp.UpdatedAt,
		}
		if productions, err := s.repo.ListProductionsByExperiment(ctx, exp.ID); err == nil && len(productions) > 0 {
			latest := productions[0]
			for _, rec := range productions[1:] {
				if rec.UpdatedAt.After(latest.UpdatedAt) {
					latest = rec
				}
			}
			summary.ProductionStatus = latest.Status
			summary.ProductionStep = latest.Step
			summary.ProjectID = latest.ProjectID
			if summary.AccountID == "" {
				summary.AccountID = strings.TrimSpace(latest.AccountID)
			}
			if latest.UpdatedAt.After(summary.UpdatedAt) {
				summary.UpdatedAt = latest.UpdatedAt
			}
		}
		out = append(out, summary)
	}
	return out, nil
}

func (s *Service) DeleteExperiment(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return store.ErrRemixLabNotFound
	}
	if _, _, _, err := s.repo.GetExperiment(ctx, id); err != nil {
		return err
	}
	if err := s.repo.DeleteExperiment(ctx, id); err != nil {
		return err
	}
	s.removeExperimentFiles(id)
	return nil
}

func (s *Service) removeExperimentFiles(id string) {
	root := filepath.Join(filepath.Clean(s.dataRoot), "remix-lab")
	dir := filepath.Join(root, id)
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return
	}
	_ = os.RemoveAll(dir)
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

	var snapshotErr error
	exp, snapshotErr = s.effectiveRunExperiment(ctx, exp, run.ID)
	if snapshotErr != nil {
		finish("failed", "", "", snapshotErr.Error())
		return
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
	prompt := s.resolveRunPrompt(run)
	settings := map[string]string{
		"remix_prompt_style": "rewrite",
	}
	if strings.TrimSpace(prompt.System) != "" {
		settings["remix_system_prompt"] = prompt.System
		settings["remix_user_prompt"] = prompt.User
		if stamp := strings.TrimSpace(prompt.Stamp); stamp != "" {
			settings["remix_prompt_stamp"] = stamp
		}
	}
	manifest := map[string]any{
		"action":     "remix.standard",
		"output_dir": run.OutputDir,
		"inputs": []map[string]string{
			{"type": "source_script", "path": sourcePath},
		},
		"non_secret_settings": settings,
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

	defaultModel, defaultEffort := s.remixDefaults(ctx, rt, rtErr)
	opts := openaicompat.Options{
		ManifestPath:      manifestPath,
		OutputLastMessage: filepath.Join(run.OutputDir, "last.json"),
		Model:             slot.Model,
		ReasoningEffort:   slot.ReasoningEffort,
		ServiceTier:       slot.ServiceTier,
		DefaultModel:      defaultModel,
		DefaultEffort:     defaultEffort,
		BaseURL:           resolvedURL,
		APIKey:            resolvedKey,
		Pipeline:          slot.Pipeline,
		// 创作台默认走审稿agent终审：只修违规处，留 draft_v1/review 双版本产物。
		ReviewerEnabled: true,
		// 工作流 agent 节点 {{file:名字}} 占位符的取文件目录（爆款素材库等）。
		IntelFileDir: filepath.Join(s.dataRoot, "remix_lab"),
	}
	// 操作员在「Agent提示词」编辑器里的覆盖文本（钩子/事实/弹药/审稿）。
	s.applyAgentPromptOverrides(&opts)
	if slot.Pipeline == openaicompat.PipelineMultiAgent && rtErr == nil {
		opts.SearchBaseURL = rt.GrokBaseURL
		opts.SearchAPIKey = rt.GrokAPIKey
		opts.SearchModel = rt.GrokModel
	}
	// 工作流实验：快照驱动节点图执行，固定管线开关让位。
	if wf, ok := parseWorkflowJSON(exp.WorkflowJSON); ok {
		opts.WorkflowJSON = exp.WorkflowJSON
		opts.Pipeline = ""
		opts.ReviewerEnabled = false
		if rtErr == nil {
			for _, node := range wf.Nodes {
				if node.Type == WorkflowNodeAgent && strings.EqualFold(strings.TrimSpace(node.Config.Channel), "search") {
					opts.SearchBaseURL = rt.GrokBaseURL
					opts.SearchAPIKey = rt.GrokAPIKey
					opts.SearchModel = rt.GrokModel
					break
				}
			}
		}
	}
	// A resumed attempt reuses intermediate artifacts, not the previous
	// attempt's terminal envelope. Otherwise an old failure masks new success.
	if err := os.Remove(filepath.Join(run.OutputDir, "last.json")); err != nil && !os.IsNotExist(err) {
		finish("failed", "", "", err.Error())
		return
	}
	if err := s.runner(ctx, opts); err != nil {
		finish("failed", "", "", scrubSecret(err.Error(), resolvedKey))
		return
	}
	// The runtime reports business failures in its envelope and may retain a
	// usable draft. Writing that envelope successfully is not a successful run.
	if summary := failureSummary(filepath.Join(run.OutputDir, "last.json")); summary != "" {
		script, _ := os.ReadFile(filepath.Join(run.OutputDir, "continuous_script.txt"))
		run.DraftV1JSON = readRunArtifact(filepath.Join(run.OutputDir, "draft_v1.json"))
		run.ReviewJSON = readRunArtifact(filepath.Join(run.OutputDir, "review.json"))
		finish("failed", string(script), "", scrubSecret(summary, resolvedKey))
		return
	}

	scriptBytes, err := os.ReadFile(filepath.Join(run.OutputDir, "continuous_script.txt"))
	if err != nil {
		// Run 失败时把错误写进 last.json 信封并返回 nil，缺成稿只是表象；
		// 把信封里的真实原因（例如配额不足）透传到界面，别报一个文件错。
		message := scrubSecret(err.Error(), resolvedKey)
		if summary := failureSummary(filepath.Join(run.OutputDir, "last.json")); summary != "" {
			message = scrubSecret(summary, resolvedKey)
		}
		finish("failed", "", "", message)
		return
	}
	packagePath := filepath.Join(run.OutputDir, "publishing_package.json")
	titlesJSON := readTitlesJSON(packagePath)
	run.PackageJSON = buildRunPackageJSON(strings.TrimSpace(string(scriptBytes)), packagePath)
	run.DraftV1JSON = readRunArtifact(filepath.Join(run.OutputDir, "draft_v1.json"))
	run.ReviewJSON = readRunArtifact(filepath.Join(run.OutputDir, "review.json"))
	finish("completed", string(scriptBytes), titlesJSON, "")
	// 生产段：全自动直接放行，否则挂等待确认的闸门记录。
	if run.Status == "completed" {
		s.afterWorkflowRunCompleted(ctx, exp, run)
	}
}

// buildRunPackageJSON 把成稿正文和发布包文件合成一份写手 JSON（draft 形状），
// 作为创作台可编辑、可直接导入混剪项目的当前定稿。
func buildRunPackageJSON(script, packagePath string) string {
	draft := map[string]any{"continuous_script": script}
	if raw, err := os.ReadFile(packagePath); err == nil {
		var pkg struct {
			Titles       []string `json:"titles"`
			ShortTitles  []string `json:"short_titles"`
			Descriptions []string `json:"descriptions"`
			Topics       []string `json:"topics"`
			CTA          string   `json:"cta"`
		}
		if err := json.Unmarshal(raw, &pkg); err == nil {
			draft["titles"] = pkg.Titles
			draft["short_titles"] = pkg.ShortTitles
			draft["descriptions"] = pkg.Descriptions
			draft["topics"] = pkg.Topics
			draft["cta"] = pkg.CTA
		}
	}
	encoded, err := json.Marshal(draft)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func readRunArtifact(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// failureSummary 读取 openaicompat 失败信封的 summary；不可读或非失败时返回空。
func failureSummary(lastPath string) string {
	raw, err := os.ReadFile(lastPath)
	if err != nil {
		return ""
	}
	var envelope struct {
		Status  string `json:"status"`
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return ""
	}
	if envelope.Status != "failed" {
		return ""
	}
	return strings.TrimSpace(envelope.Summary)
}

func (s *Service) resolveRunPrompt(run store.RemixLabRunRecord) PromptTemplate {
	id := strings.TrimSpace(run.PromptID)
	if id == "" {
		id = "elder_stable"
	}
	p, ok, err := (Store{DataRoot: s.dataRoot}).GetPrompt(id)
	if err == nil && ok {
		return p
	}
	fallback := ResolvePrompt("elder_stable", "", "")
	if strings.TrimSpace(run.PromptName) != "" {
		fallback.Name = run.PromptName
	}
	if strings.TrimSpace(run.PromptStamp) != "" {
		fallback.Stamp = run.PromptStamp
	}
	return fallback
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

func mapExperiment(exp store.RemixLabExperimentRecord, slots []store.RemixLabSlotRecord, runs []store.RemixLabRunRecord, productions []store.RemixLabProductionRecord) Experiment {
	productionByRun := make(map[string]store.RemixLabProductionRecord, len(productions))
	for _, rec := range productions {
		productionByRun[rec.RunID] = rec
	}
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
			ServiceTier:      slot.ServiceTier,
			Pipeline:         slot.Pipeline,
			RunCount:         slot.RunCount,
			APIKeyConfigured: true,
		})
	}
	runViews := make([]RunView, 0, len(runs))
	for _, run := range runs {
		view := RunView{
			ID:               run.ID,
			ExperimentID:     run.ExperimentID,
			SlotID:           run.SlotID,
			RunIndex:         run.RunIndex,
			Status:           run.Status,
			PromptID:         run.PromptID,
			PromptStamp:      run.PromptStamp,
			PromptName:       run.PromptName,
			ContinuousScript: run.ContinuousScript,
			TitlesJSON:       run.TitlesJSON,
			PackageJSON:      run.PackageJSON,
			DraftV1JSON:      run.DraftV1JSON,
			ReviewJSON:       run.ReviewJSON,
			ErrorMessage:     run.ErrorMessage,
			Comment:          run.Comment,
			OutputDir:        run.OutputDir,
			AdoptedProjectID: run.AdoptedProjectID,
			StartedAt:        run.StartedAt,
			FinishedAt:       run.FinishedAt,
		}
		if rec, ok := productionByRun[run.ID]; ok {
			view.Production = productionView(rec)
		}
		runViews = append(runViews, view)
	}
	return Experiment{
		AccountID:   exp.ProduceAccountID,
		ID:          exp.ID,
		Title:       exp.Title,
		SourceText:  exp.SourceText,
		PromptStamp: exp.PromptStamp,
		Status:      exp.Status,
		Workflow:    strings.TrimSpace(exp.WorkflowJSON) != "",
		CreatedAt:   exp.CreatedAt,
		UpdatedAt:   exp.UpdatedAt,
		Slots:       slotViews,
		Runs:        runViews,
	}
}
