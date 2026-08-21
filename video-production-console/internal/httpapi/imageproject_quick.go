package httpapi

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/domain"
	"video-production-console/internal/imageproject"
	"video-production-console/internal/store"
	"video-production-console/internal/taskmodel"
)

var authorizationValue = regexp.MustCompile(`(?i)(authorization:\s*bearer\s+)\S+`)

func (h *imageProjectsHandler) quickGenerate(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeQuickGenerate(w, r)
	if !ok {
		return
	}
	now := time.Now().UTC()
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
	project := domain.ImageProject{
		ID:              uuid.NewString(),
		Title:           imageproject.FallbackProjectTitle(in.Script),
		Script:          in.Script,
		ImageCount:      1,
		Ratio:           in.Ratio,
		Style:           in.Style,
		CustomStyle:     strings.TrimSpace(in.CustomStyle),
		Concurrency:     in.Concurrency,
		Status:          "draft",
		RunMode:         "quick",
		RunPhase:        "planning",
		RunStatus:       "running",
		ImageAttempts:   attempts,
		TextModel:       in.TextModel,
		ReasoningEffort: in.ReasoningEffort,
		ImageModel:      strings.TrimSpace(in.ImageModel),
		OutputMode:      in.OutputMode,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := h.repo.Create(r.Context(), project, nil); err != nil {
		writeError(w, http.StatusInternalServerError, "image_project_create_failed", "The image project could not be created.")
		return
	}
	if !h.jobs.Start(project.ID, func() {
		ctx, cancel := context.WithTimeout(context.Background(), imageproject.GenerateBatchBudget+imageproject.DefaultChatTimeout)
		defer cancel()
		h.runQuickProject(ctx, project.ID)
	}) {
		writeError(w, http.StatusConflict, "image_project_running", "The image project is already running.")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{
		"project_id": project.ID,
		"run_status": "running",
	})
}

func (h *imageProjectsHandler) resumeQuickGenerate(w http.ResponseWriter, r *http.Request) {
	project, _, err := h.repo.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrImageProjectNotFound) {
		writeError(w, http.StatusNotFound, "image_project_not_found", "The image project was not found.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "image_project_read_failed", "The image project could not be read.")
		return
	}
	if project.RunMode != "quick" {
		writeError(w, http.StatusBadRequest, "invalid_image_project", "Only quick image projects can be resumed.")
		return
	}
	if project.RunStatus == "completed" {
		h.get(w, r)
		return
	}
	if project.RunStatus == "running" {
		writeError(w, http.StatusConflict, "image_project_running", "The image project is already running.")
		return
	}
	if project.RunStatus != "failed" && project.RunStatus != "interrupted" {
		writeError(w, http.StatusConflict, "image_project_not_resumable", "The image project cannot be resumed.")
		return
	}
	if err := h.repo.SetRunState(r.Context(), project.ID, project.RunPhase, "running", "", project.SuccessCount, project.FailureCount, time.Now().UTC()); err != nil {
		writeError(w, http.StatusInternalServerError, "image_project_resume_failed", "The image project could not be resumed.")
		return
	}
	if !h.jobs.Start(project.ID, func() {
		ctx, cancel := context.WithTimeout(context.Background(), imageproject.GenerateBatchBudget+imageproject.DefaultChatTimeout)
		defer cancel()
		h.runQuickProject(ctx, project.ID)
	}) {
		writeError(w, http.StatusConflict, "image_project_running", "The image project is already running.")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{
		"project_id": project.ID,
		"run_status": "running",
	})
}

func (h *imageProjectsHandler) updateProjectTitle(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Title string `json:"title"`
	}
	if err := decodeJSON(w, r, maxMessageJSONRequest, &in); err != nil {
		writeDecodeError(w, err, "invalid_image_project", "A valid image project is required.")
		return
	}
	if err := h.repo.UpdateProjectTitle(r.Context(), r.PathValue("id"), in.Title, time.Now().UTC()); err != nil {
		if strings.Contains(err.Error(), "invalid project title") {
			writeError(w, http.StatusBadRequest, "invalid_image_project", "Image project settings are invalid.")
			return
		}
		h.writeReadError(w, err)
		return
	}
	h.get(w, r)
}

func (h *imageProjectsHandler) runQuickProject(ctx context.Context, projectID string) {
	for {
		project, items, err := h.repo.Get(ctx, projectID)
		if err != nil {
			return
		}
		var phaseErr error
		switch project.RunPhase {
		case "planning":
			phaseErr = h.runQuickPlanning(ctx, project)
		case "prompting":
			phaseErr = h.runQuickPrompting(ctx, project, items)
		case "imaging":
			phaseErr = h.runQuickImaging(ctx, project, items)
		case "completed":
			return
		default:
			phaseErr = errors.New("image project phase is invalid")
		}
		if phaseErr != nil {
			_ = h.repo.SetRunState(context.Background(), projectID, project.RunPhase, "failed", safeQuickError(phaseErr), project.SuccessCount, project.FailureCount, time.Now().UTC())
			return
		}
	}
}

func (h *imageProjectsHandler) runQuickPlanning(ctx context.Context, project domain.ImageProject) error {
	client, model, err := h.plannerClient(ctx, project.TextModel)
	if err != nil {
		return err
	}
	plan, err := imageproject.SuggestQuickPlan(ctx, client, model, project.Script, 0, h.plannerEffort(ctx, project.ReasoningEffort))
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	items := make([]domain.ImageProjectItem, 0, len(plan.Segments))
	for _, segment := range plan.Segments {
		title := strings.TrimSpace(segment.Title)
		if title == "" {
			title = itemTitle(segment.SourceText, segment.Sequence)
		}
		items = append(items, domain.ImageProjectItem{
			ID: uuid.NewString(), ProjectID: project.ID, Sequence: segment.Sequence, Role: segment.Role,
			SourceText: segment.SourceText, Title: title, Status: "pending", CreatedAt: now, UpdatedAt: now,
		})
	}
	candidates := make([]domain.PublishingCandidate, len(plan.Publishing))
	for index, candidate := range plan.Publishing {
		candidates[index] = domain.PublishingCandidate{Position: candidate.Position, Title: candidate.Title, Description: candidate.Description}
	}
	return h.repo.SaveQuickPlan(ctx, project.ID, plan.ProjectTitle, items, candidates, plan.PublishingError, now)
}

func (h *imageProjectsHandler) runQuickPrompting(ctx context.Context, project domain.ImageProject, items []domain.ImageProjectItem) error {
	segments := make([]imageproject.Segment, 0, len(items))
	for _, item := range items {
		segments = append(segments, imageproject.Segment{
			Sequence: item.Sequence, Role: item.Role, Title: item.Title, SourceText: item.SourceText,
		})
	}
	in := imageProjectDraft{
		Ratio: project.Ratio, Style: project.Style, CustomStyle: project.CustomStyle,
		TextModel: project.TextModel, ReasoningEffort: project.ReasoningEffort,
	}
	prompts, _, err := h.suggestPrompts(ctx, in, segments)
	if err != nil {
		return err
	}
	return h.repo.SavePrompts(ctx, project.ID, prompts, time.Now().UTC())
}

func (h *imageProjectsHandler) runQuickImaging(ctx context.Context, project domain.ImageProject, items []domain.ImageProjectItem) error {
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
	return h.repo.SetRunState(ctx, project.ID, "completed", "completed", "", success, failure, time.Now().UTC())
}

func decodeQuickGenerate(w http.ResponseWriter, r *http.Request) (imageProjectDraft, bool) {
	var in imageProjectDraft
	if err := decodeJSON(w, r, maxTaskJSONRequest, &in); err != nil {
		writeDecodeError(w, err, "invalid_image_project", "A valid image project is required.")
		return imageProjectDraft{}, false
	}
	in.TextModel = strings.TrimSpace(in.TextModel)
	in.ReasoningEffort = strings.ToLower(strings.TrimSpace(in.ReasoningEffort))
	in.ImageModel = strings.TrimSpace(in.ImageModel)
	if in.OutputMode == "" {
		in.OutputMode = domain.ImageProjectOutputModeImageSlideshow
	}
	if strings.TrimSpace(in.Script) == "" || len([]byte(in.Script)) > 1<<20 || !validRatio(in.Ratio) || !validStyle(in.Style) || (in.Style == "custom" && strings.TrimSpace(in.CustomStyle) == "") || in.Concurrency < 1 || in.Concurrency > imageproject.MaxImages || (in.OutputMode != domain.ImageProjectOutputModeImageSlideshow && in.OutputMode != domain.ImageProjectOutputModeImageToVideo) {
		writeError(w, http.StatusBadRequest, "invalid_image_project", "Image project settings are invalid.")
		return imageProjectDraft{}, false
	}
	if in.ImageCount != 0 && (in.ImageCount < 1 || in.ImageCount > imageproject.MaxImages) {
		writeError(w, http.StatusBadRequest, "invalid_image_count", "Image count must be between 1 and 18.")
		return imageProjectDraft{}, false
	}
	if in.ImageAttempts != 0 && (in.ImageAttempts < 1 || in.ImageAttempts > 4) {
		writeError(w, http.StatusBadRequest, "invalid_image_project", "Image project settings are invalid.")
		return imageProjectDraft{}, false
	}
	if in.ReasoningEffort != "" {
		if _, err := taskmodel.Normalize(taskmodel.Selection{Model: taskmodel.DefaultModel, ReasoningEffort: in.ReasoningEffort}); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_image_project", "Image project settings are invalid.")
			return imageProjectDraft{}, false
		}
	}
	return in, true
}

func safeQuickError(err error) string {
	if err == nil {
		return ""
	}
	text := authorizationValue.ReplaceAllString(err.Error(), "${1}[redacted]")
	if len(text) > 500 {
		text = text[:500]
	}
	return text
}
