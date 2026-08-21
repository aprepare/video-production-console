package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"video-production-console/internal/domain"
	"video-production-console/internal/imageproject"
	"video-production-console/internal/imagevideo"
	"video-production-console/internal/imagevideoruntime"
	consoleSettings "video-production-console/internal/settings"
	"video-production-console/internal/store"
)

const videoPipelineBudget = 45 * time.Minute

type createImageVideoRequest struct {
	Script         string                `json:"script"`
	Title          string                `json:"title"`
	AccountID      string                `json:"account_id"`
	OutputMode     imagevideo.OutputMode `json:"output_mode"`
	IdempotencyKey string                `json:"idempotency_key"`
	TextModel      string                `json:"text_model"`
	ImageModel     string                `json:"image_model"`
	Concurrency    int                   `json:"concurrency"`
	ImageAttempts  int                   `json:"image_attempts"`
}

func (h *imageProjectsHandler) listVideos(w http.ResponseWriter, r *http.Request) {
	projects, err := h.repo.ListVideo(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "image_videos_list_failed", "Image videos could not be listed.")
		return
	}
	writeJSON(w, http.StatusOK, projects)
}

func (h *imageProjectsHandler) getVideo(w http.ResponseWriter, r *http.Request) {
	project, _, err := h.repo.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrImageProjectNotFound) {
		writeError(w, http.StatusNotFound, "image_video_not_found", "The image video was not found.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "image_video_read_failed", "The image video could not be read.")
		return
	}
	if project.RunMode != "video" {
		writeError(w, http.StatusNotFound, "image_video_not_found", "The image video was not found.")
		return
	}
	h.get(w, r)
}

func (h *imageProjectsHandler) createVideo(w http.ResponseWriter, r *http.Request) {
	var in createImageVideoRequest
	if err := decodeJSON(w, r, maxTaskJSONRequest, &in); err != nil {
		writeDecodeError(w, err, "invalid_image_video", "A valid image video is required.")
		return
	}
	in.Script = strings.TrimSpace(in.Script)
	in.Title = strings.TrimSpace(in.Title)
	in.AccountID = strings.TrimSpace(in.AccountID)
	in.IdempotencyKey = strings.TrimSpace(in.IdempotencyKey)
	in.TextModel = strings.TrimSpace(in.TextModel)
	in.ImageModel = strings.TrimSpace(in.ImageModel)
	if in.OutputMode == "" {
		in.OutputMode = imagevideo.ModeSlideshow
	}
	if in.Script == "" || len([]byte(in.Script)) > 1<<20 || in.AccountID == "" || in.IdempotencyKey == "" {
		writeError(w, http.StatusBadRequest, "invalid_image_video", "Script, account, and idempotency key are required.")
		return
	}
	if utf8.RuneCountInString(in.Title) > 120 {
		writeError(w, http.StatusBadRequest, "invalid_image_video", "Title is too long.")
		return
	}
	if in.OutputMode != imagevideo.ModeSlideshow && in.OutputMode != imagevideo.ModeImageToVideo {
		writeError(w, http.StatusBadRequest, "invalid_image_video", "Output mode must be image_slideshow or image_to_video.")
		return
	}
	if in.Concurrency == 0 {
		in.Concurrency = 6
	}
	if in.Concurrency < 1 || in.Concurrency > imageproject.MaxImages {
		writeError(w, http.StatusBadRequest, "invalid_image_video", "Concurrency must be between 1 and 18.")
		return
	}
	if in.ImageAttempts != 0 && (in.ImageAttempts < 1 || in.ImageAttempts > 4) {
		writeError(w, http.StatusBadRequest, "invalid_image_video", "Image attempts must be between 1 and 4.")
		return
	}
	if err := h.requireAccount(r.Context(), in.AccountID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_image_video_account", "A configured publishing account is required.")
		return
	}
	attempts := in.ImageAttempts
	if attempts < 1 || attempts > 4 {
		if h.runtime != nil {
			if runtime, err := h.runtime.Runtime(r.Context()); err == nil && runtime.ImageGenerationAttempts >= 1 && runtime.ImageGenerationAttempts <= 4 {
				attempts = runtime.ImageGenerationAttempts
			}
		}
		if attempts < 1 || attempts > 4 {
			attempts = 2
		}
	}
	now := time.Now().UTC()
	title := in.Title
	if title == "" {
		title = imageproject.FallbackProjectTitle(in.Script)
	}
	accountID := in.AccountID
	project := domain.ImageProject{
		ID:            uuid.NewString(),
		Title:         title,
		Script:        in.Script,
		ImageCount:    1,
		Ratio:         imageproject.VideoSceneRatio,
		Style:         imageproject.VideoSceneStyle,
		Concurrency:   in.Concurrency,
		Status:        "draft",
		RunMode:       "video",
		RunPhase:      "planning",
		RunStatus:     "running",
		ImageAttempts: attempts,
		TextModel:     in.TextModel,
		ImageModel:    in.ImageModel,
		OutputMode:    domain.ImageProjectOutputMode(in.OutputMode),
		AccountID:     &accountID,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := h.repo.Create(r.Context(), project, nil); err != nil {
		writeError(w, http.StatusInternalServerError, "image_video_create_failed", "The image video could not be created.")
		return
	}
	if !h.jobs.Start(project.ID, func() {
		ctx, cancel := context.WithTimeout(context.Background(), videoPipelineBudget)
		defer cancel()
		h.runVideoProject(ctx, project.ID, "video:"+project.ID)
	}) {
		writeError(w, http.StatusConflict, "image_video_running", "The image video is already running.")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{
		"project_id": project.ID,
		"run_status": "running",
	})
}

func (h *imageProjectsHandler) resumeVideo(w http.ResponseWriter, r *http.Request) {
	project, _, err := h.repo.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrImageProjectNotFound) || project.RunMode != "video" {
		writeError(w, http.StatusNotFound, "image_video_not_found", "The image video was not found.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "image_video_read_failed", "The image video could not be read.")
		return
	}
	if project.RunStatus == "completed" {
		h.get(w, r)
		return
	}
	if project.RunStatus == "running" {
		writeError(w, http.StatusConflict, "image_video_running", "The image video is already running.")
		return
	}
	if project.RunStatus != "failed" && project.RunStatus != "interrupted" {
		writeError(w, http.StatusConflict, "image_video_not_resumable", "The image video cannot be resumed.")
		return
	}
	if err := h.repo.SetRunState(r.Context(), project.ID, project.RunPhase, "running", "", project.SuccessCount, project.FailureCount, time.Now().UTC()); err != nil {
		writeError(w, http.StatusInternalServerError, "image_video_resume_failed", "The image video could not be resumed.")
		return
	}
	if !h.jobs.Start(project.ID, func() {
		ctx, cancel := context.WithTimeout(context.Background(), videoPipelineBudget)
		defer cancel()
		h.runVideoProject(ctx, project.ID, "video:"+project.ID)
	}) {
		writeError(w, http.StatusConflict, "image_video_running", "The image video is already running.")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{
		"project_id": project.ID,
		"run_status": "running",
	})
}

func (h *imageProjectsHandler) runVideoProject(ctx context.Context, projectID, idempotencyKey string) {
	for {
		project, items, err := h.repo.Get(ctx, projectID)
		if err != nil {
			return
		}
		var phaseErr error
		switch project.RunPhase {
		case "planning":
			phaseErr = h.runVideoPlanning(ctx, project)
		case "imaging":
			phaseErr = h.runVideoImaging(ctx, project, items, idempotencyKey)
		case "completed":
			return
		default:
			phaseErr = errors.New("image video phase is invalid")
		}
		if phaseErr != nil {
			_ = h.repo.SetRunState(context.Background(), projectID, project.RunPhase, "failed", safeQuickError(phaseErr), project.SuccessCount, project.FailureCount, time.Now().UTC())
			return
		}
	}
}

func (h *imageProjectsHandler) runVideoPlanning(ctx context.Context, project domain.ImageProject) error {
	artifact, err := h.narrate(ctx, project)
	if err != nil {
		return err
	}
	scenes, err := imagevideo.BuildScenesFromTiming(artifact.TimingDocument)
	if err != nil {
		return err
	}
	videoScenes := make([]imageproject.VideoScene, 0, len(scenes))
	for _, scene := range scenes {
		videoScenes = append(videoScenes, imageproject.VideoScene{
			ID:      fmt.Sprintf("scene-%03d", scene.Ordinal),
			StartMS: scene.StartUS / 1000,
			EndMS:   scene.EndUS / 1000,
			Text:    scene.Text,
		})
	}
	prompts, promptErr := h.suggestVideoScenePrompts(ctx, project, videoScenes)
	now := time.Now().UTC()
	items := make([]domain.ImageProjectItem, 0, len(scenes))
	for index, scene := range scenes {
		prompt := imageproject.FallbackVideoScenePrompt(videoScenes[index], scene.Ordinal)
		if promptErr == nil && index < len(prompts) {
			prompt = prompts[index]
		}
		items = append(items, domain.ImageProjectItem{
			ID: uuid.NewString(), ProjectID: project.ID, Sequence: scene.Ordinal, Role: imageproject.RoleContent,
			SourceText: scene.Text, Title: prompt.Title, Prompt: prompt.Prompt, Status: "pending",
			CreatedAt: now, UpdatedAt: now,
		})
	}
	title := project.Title
	if strings.TrimSpace(title) == "" {
		title = imageproject.FallbackProjectTitle(project.Script)
	}
	return h.repo.SaveVideoPlan(ctx, project.ID, title, items, now)
}

func (h *imageProjectsHandler) suggestVideoScenePrompts(ctx context.Context, project domain.ImageProject, scenes []imageproject.VideoScene) ([]imageproject.VideoScenePrompt, error) {
	client, model, err := h.plannerClient(ctx, project.TextModel)
	if err != nil {
		return nil, err
	}
	workCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), imageproject.DefaultChatTimeout)
	defer cancel()
	return imageproject.SuggestVideoScenePrompts(workCtx, client, model, scenes)
}

func (h *imageProjectsHandler) runVideoImaging(ctx context.Context, project domain.ImageProject, items []domain.ImageProjectItem, idempotencyKey string) error {
	pending := make([]domain.ImageProjectItem, 0, len(items))
	for _, item := range items {
		if item.Status != "ready" {
			pending = append(pending, item)
		}
	}
	if err := h.generate(ctx, project, pending); err != nil {
		return err
	}
	_, refreshed, err := h.repo.Get(ctx, project.ID)
	if err != nil {
		return err
	}
	success, failure := 0, 0
	for _, item := range refreshed {
		switch item.Status {
		case "ready":
			success++
		case "failed":
			failure++
		}
	}
	if failure == 0 && success == len(refreshed) && len(refreshed) > 0 {
		if startErr := h.startReadyVideoJob(ctx, project, idempotencyKey); startErr != nil {
			_ = h.repo.SetRunState(ctx, project.ID, "imaging", "completed", "", success, failure, time.Now().UTC())
			return startErr
		}
	}
	return h.repo.SetRunState(ctx, project.ID, "completed", "completed", "", success, failure, time.Now().UTC())
}

func (h *imageProjectsHandler) startReadyVideoJob(ctx context.Context, project domain.ImageProject, idempotencyKey string) error {
	if h.imageVideo == nil || h.imageVideo.service == nil {
		return nil
	}
	accountID := ""
	if project.AccountID != nil {
		accountID = strings.TrimSpace(*project.AccountID)
	}
	if accountID == "" || strings.TrimSpace(idempotencyKey) == "" {
		return fmt.Errorf("account and idempotency key are required to start the image video job")
	}
	_, err := h.imageVideo.service.Start(ctx, imagevideo.StartRequest{
		ProjectID:      project.ID,
		AccountID:      accountID,
		OutputMode:     imagevideo.OutputMode(project.OutputMode),
		IdempotencyKey: idempotencyKey,
	})
	return err
}

func (h *imageProjectsHandler) narrate(ctx context.Context, project domain.ImageProject) (imagevideo.NarrationArtifact, error) {
	if h.produceNarration != nil {
		return h.produceNarration(ctx, project)
	}
	if h.runtime == nil {
		return imagevideo.NarrationArtifact{}, consoleSettings.ErrNotConfigured
	}
	runtime, err := h.runtime.Runtime(ctx)
	if err != nil {
		return imagevideo.NarrationArtifact{}, err
	}
	producer, err := imagevideo.NewNarrationAdapterFromRuntime(imagevideoruntime.Snapshot(runtime), "")
	if err != nil {
		return imagevideo.NarrationArtifact{}, err
	}
	return producer.Produce(ctx, project)
}

func (h *imageProjectsHandler) requireAccount(ctx context.Context, accountID string) error {
	if h.imageVideo == nil || h.imageVideo.db == nil {
		return errors.New("accounts are unavailable")
	}
	var status string
	err := h.imageVideo.db.QueryRowContext(ctx, `SELECT status FROM accounts WHERE id=?`, accountID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("account not found")
	}
	if err != nil {
		return err
	}
	if status != "active" {
		return errors.New("account is not active")
	}
	return nil
}
