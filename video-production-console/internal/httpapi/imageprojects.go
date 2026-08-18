package httpapi

import (
	"archive/zip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"video-production-console/internal/domain"
	"video-production-console/internal/imageproject"
	consoleSettings "video-production-console/internal/settings"
	"video-production-console/internal/store"
	"video-production-console/internal/taskmodel"
)

type imageRuntimeProvider interface {
	Runtime(context.Context) (consoleSettings.Runtime, error)
}

const maxImageArchiveEntrySize int64 = 32 << 20

type imageProjectsHandler struct {
	repo      *store.ImageProjectRepository
	runtime   imageRuntimeProvider
	generator imageproject.Generator
	planner   imageproject.ChatClient
	jobs      *imageProjectJobs
}

func NewImageProjectsHandler(db *sql.DB, runtime imageRuntimeProvider, generator imageproject.Generator) http.Handler {
	return NewImageProjectsHandlerWithPlanner(db, runtime, generator, imageproject.NewHTTPChatClient(nil))
}

func NewImageProjectsHandlerWithPlanner(db *sql.DB, runtime imageRuntimeProvider, generator imageproject.Generator, planner imageproject.ChatClient) http.Handler {
	h := &imageProjectsHandler{repo: store.NewImageProjectRepository(db), runtime: runtime, generator: generator, planner: planner, jobs: newImageProjectJobs()}
	_ = h.repo.MarkRunningQuickProjectsInterrupted(context.Background(), time.Now().UTC())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/image-projects", h.list)
	mux.HandleFunc("POST /api/image-projects", h.create)
	mux.HandleFunc("POST /api/image-projects/quick-generate", h.quickGenerate)
	mux.HandleFunc("POST /api/image-projects/segment-preview", h.previewSegments)
	mux.HandleFunc("GET /api/image-projects/{id}", h.get)
	mux.HandleFunc("POST /api/image-projects/{id}/resume", h.resumeQuickGenerate)
	mux.HandleFunc("PATCH /api/image-projects/{id}", h.updateProjectTitle)
	mux.HandleFunc("DELETE /api/image-projects/{id}", h.delete)
	mux.HandleFunc("PATCH /api/image-projects/{id}/items/{item}", h.updateItem)
	mux.HandleFunc("POST /api/image-projects/{id}/generate", h.generateAll)
	mux.HandleFunc("PATCH /api/image-projects/{id}/publishing-candidates/{position}", h.updatePublishing)
	mux.HandleFunc("POST /api/image-projects/{id}/publishing-candidates/{position}/select", h.selectPublishing)
	mux.HandleFunc("POST /api/image-projects/{id}/publishing-candidates/select", h.selectPublishingBody)
	mux.HandleFunc("POST /api/image-projects/{id}/publishing-candidates/generate", h.generatePublishing)
	mux.HandleFunc("POST /api/image-projects/{id}/items/{item}/generate", h.generateOne)
	mux.HandleFunc("GET /api/image-projects/{id}/items/{item}/image", h.image)
	mux.HandleFunc("GET /api/image-projects/{id}/download", h.download)
	return mux
}

func (h *imageProjectsHandler) updatePublishing(w http.ResponseWriter, r *http.Request) {
	pos, _ := strconv.Atoi(r.PathValue("position"))
	var in domain.PublishingCandidate
	if err := decodeJSON(w, r, maxMessageJSONRequest, &in); err != nil {
		writeError(w, 400, "invalid_publishing_candidate", "Invalid candidate.")
		return
	}
	in.Position = pos
	if pos < 1 || pos > 5 || utf8.RuneCountInString(in.Title) > 22 || utf8.RuneCountInString(in.Description) > 1000 || strings.TrimSpace(in.Title) == "" || strings.TrimSpace(in.Description) == "" {
		writeError(w, 400, "invalid_publishing_candidate", "Invalid candidate.")
		return
	}
	if err := h.repo.UpdatePublishingCandidate(r.Context(), r.PathValue("id"), in); err != nil {
		h.writeReadError(w, err)
		return
	}
	h.get(w, r)
}
func (h *imageProjectsHandler) selectPublishing(w http.ResponseWriter, r *http.Request) {
	pos, _ := strconv.Atoi(r.PathValue("position"))
	if pos < 1 || pos > 5 {
		writeError(w, 400, "invalid_publishing_position", "Invalid position.")
		return
	}
	if err := h.repo.SelectPublishingPosition(r.Context(), r.PathValue("id"), pos); err != nil {
		h.writeReadError(w, err)
		return
	}
	h.get(w, r)
}
func (h *imageProjectsHandler) selectPublishingBody(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Position int `json:"position"`
	}
	if decodeJSON(w, r, maxMessageJSONRequest, &in) != nil {
		return
	}
	if in.Position < 1 || in.Position > 5 {
		writeError(w, 400, "invalid_publishing_position", "Invalid position.")
		return
	}
	if err := h.repo.SelectPublishingPosition(r.Context(), r.PathValue("id"), in.Position); err != nil {
		h.writeReadError(w, err)
		return
	}
	h.get(w, r)
}
func (h *imageProjectsHandler) generatePublishing(w http.ResponseWriter, r *http.Request) {
	p, _, err := h.repo.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeReadError(w, err)
		return
	}
	in := imageProjectDraft{Script: p.Script}
	_, pubs, _, err := h.suggestSegmentsAndPublishing(r.Context(), in)
	if err != nil {
		h.writePlannerError(w, err)
		return
	}
	if len(pubs) != 5 {
		writeError(w, 400, "invalid_publishing_candidates", "Exactly five publishing candidates are required.")
		return
	}
	p.PublishingCandidates = pubs
	pos := 1
	p.SelectedPosition = &pos
	if err := h.repo.ReplacePublishingCandidates(r.Context(), p.ID, pubs); err != nil {
		writeError(w, 500, "publishing_candidates_save_failed", "Publishing candidates could not be saved.")
		return
	}
	h.get(w, r)
}

func (h *imageProjectsHandler) list(w http.ResponseWriter, r *http.Request) {
	projects, err := h.repo.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "image_projects_list_failed", "Image projects could not be listed.")
		return
	}
	writeJSON(w, http.StatusOK, projects)
}

func (h *imageProjectsHandler) previewSegments(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeImageProjectDraft(w, r)
	if !ok {
		return
	}
	segments, candidates, model, err := h.suggestSegmentsAndPublishing(r.Context(), in)
	if err != nil {
		h.writePlannerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"script": in.Script, "model": model, "segments": segments, "publishing_candidates": candidates})
}

func (h *imageProjectsHandler) create(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeImageProjectDraft(w, r)
	if !ok {
		return
	}
	segments, err := confirmedSegments(in)
	if err != nil {
		h.writePlannerError(w, err)
		return
	}
	prompts, _, err := h.suggestPrompts(r.Context(), in, segments)
	if err != nil {
		h.writePlannerError(w, err)
		return
	}
	now := time.Now().UTC()
	attempts := in.ImageAttempts
	if attempts < 1 || attempts > 4 {
		if h.runtime != nil {
			if runtime, runtimeErr := h.runtime.Runtime(r.Context()); runtimeErr == nil && runtime.ImageGenerationAttempts >= 1 && runtime.ImageGenerationAttempts <= 4 {
				attempts = runtime.ImageGenerationAttempts
			}
		}
		if attempts < 1 || attempts > 4 {
			attempts = 2
		}
	}
	project := domain.ImageProject{ID: uuid.NewString(), Title: in.Title, Script: in.Script, ImageCount: len(segments), Ratio: in.Ratio, Style: in.Style, CustomStyle: strings.TrimSpace(in.CustomStyle), Concurrency: in.Concurrency, Status: "draft", ImageAttempts: attempts, CreatedAt: now, UpdatedAt: now, PublishingCandidates: in.PublishingCandidates}
	if len(project.PublishingCandidates) == 5 {
		p := 1
		project.SelectedPosition = &p
	}
	items := make([]domain.ImageProjectItem, 0, len(segments))
	for index, segment := range segments {
		title := strings.TrimSpace(segment.Title)
		if title == "" {
			title = itemTitle(segment.SourceText, segment.Sequence)
		}
		items = append(items, domain.ImageProjectItem{ID: uuid.NewString(), ProjectID: project.ID, Sequence: segment.Sequence, Role: segment.Role, SourceText: segment.SourceText, Title: title, Prompt: prompts[index], Status: "pending", CreatedAt: now, UpdatedAt: now})
	}
	if err := h.repo.Create(r.Context(), project, items); err != nil {
		writeError(w, http.StatusInternalServerError, "image_project_create_failed", "The image project could not be created.")
		return
	}
	writeJSON(w, http.StatusCreated, domain.ImageProjectDetail{Project: project, Items: items})
}

type imageProjectDraft struct {
	Title                string                       `json:"title"`
	Script               string                       `json:"script"`
	ImageCount           int                          `json:"image_count"`
	Ratio                string                       `json:"ratio"`
	Style                string                       `json:"style"`
	CustomStyle          string                       `json:"custom_style"`
	Concurrency          int                          `json:"concurrency"`
	TextModel            string                       `json:"text_model"`
	ReasoningEffort      string                       `json:"reasoning_effort"`
	ConfirmedSegments    []imageproject.Segment       `json:"segments"`
	PublishingCandidates []domain.PublishingCandidate `json:"publishing_candidates"`
	SelectedPosition     int                          `json:"selected_position"`
	ImageAttempts        int                          `json:"image_attempts"`
	ImageModel           string                       `json:"image_model"`
}

func decodeImageProjectDraft(w http.ResponseWriter, r *http.Request) (imageProjectDraft, bool) {
	var in imageProjectDraft
	if err := decodeJSON(w, r, maxTaskJSONRequest, &in); err != nil {
		writeDecodeError(w, err, "invalid_image_project", "A valid image project is required.")
		return imageProjectDraft{}, false
	}
	in.Title = strings.TrimSpace(in.Title)
	in.TextModel = strings.TrimSpace(in.TextModel)
	in.ReasoningEffort = strings.ToLower(strings.TrimSpace(in.ReasoningEffort))
	if in.Title == "" || strings.TrimSpace(in.Script) == "" || utf8.RuneCountInString(in.Title) > 120 || len([]byte(in.Script)) > 1<<20 || !validRatio(in.Ratio) || !validStyle(in.Style) || (in.Style == "custom" && strings.TrimSpace(in.CustomStyle) == "") || in.Concurrency < 1 || in.Concurrency > imageproject.MaxImages {
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

func confirmedSegments(in imageProjectDraft) ([]imageproject.Segment, error) {
	if len(in.ConfirmedSegments) == 0 {
		return nil, errors.New("confirmed segments are required")
	}
	encoded, err := json.Marshal(map[string]any{"segments": in.ConfirmedSegments})
	if err != nil {
		return nil, err
	}
	return imageproject.ParseSegmentSuggestions(in.Script, string(encoded))
}

func (h *imageProjectsHandler) suggestSegments(ctx context.Context, in imageProjectDraft) ([]imageproject.Segment, string, error) {
	client, model, err := h.plannerClient(ctx, in.TextModel)
	if err != nil {
		return nil, "", err
	}
	workCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), imageproject.DefaultChatTimeout)
	defer cancel()
	segments, err := imageproject.SuggestSegments(workCtx, client, model, in.Script, in.ImageCount, h.plannerEffort(ctx, in.ReasoningEffort))
	return segments, model, err
}

func (h *imageProjectsHandler) suggestSegmentsAndPublishing(ctx context.Context, in imageProjectDraft) ([]imageproject.Segment, []domain.PublishingCandidate, string, error) {
	client, model, err := h.plannerClient(ctx, in.TextModel)
	if err != nil {
		return nil, nil, "", err
	}
	workCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), imageproject.DefaultChatTimeout)
	defer cancel()
	segs, pubs, err := imageproject.SuggestSegmentsAndPublishing(workCtx, client, model, in.Script, in.ImageCount, h.plannerEffort(ctx, in.ReasoningEffort))
	if err != nil {
		return nil, nil, model, err
	}
	converted := make([]domain.PublishingCandidate, len(pubs))
	for i, p := range pubs {
		converted[i] = domain.PublishingCandidate{Position: p.Position, Title: p.Title, Description: p.Description}
	}
	return segs, converted, model, err
}

func (h *imageProjectsHandler) suggestPrompts(ctx context.Context, in imageProjectDraft, segments []imageproject.Segment) ([]string, string, error) {
	client, model, err := h.plannerClient(ctx, in.TextModel)
	if err != nil {
		return nil, "", err
	}
	workCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), imageproject.DefaultChatTimeout)
	defer cancel()
	prompts, err := imageproject.SuggestPrompts(workCtx, client, model, segments, in.Ratio, in.Style, in.CustomStyle, h.plannerEffort(ctx, in.ReasoningEffort))
	return prompts, model, err
}

func (h *imageProjectsHandler) plannerEffort(ctx context.Context, requested string) string {
	if effort := strings.ToLower(strings.TrimSpace(requested)); effort != "" {
		return effort
	}
	if h.runtime == nil {
		return ""
	}
	runtime, err := h.runtime.Runtime(ctx)
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(runtime.ImageTextReasoningEffort))
}

func (h *imageProjectsHandler) plannerClient(ctx context.Context, requestedModel string) (imageproject.ChatClient, string, error) {
	if h.runtime == nil {
		return nil, "", consoleSettings.ErrNotConfigured
	}
	runtime, err := h.runtime.Runtime(ctx)
	if err != nil {
		return nil, "", err
	}
	baseURL := strings.TrimSpace(runtime.ImageTextBaseURL)
	apiKey := strings.TrimSpace(runtime.ImageTextAPIKey)
	model := strings.TrimSpace(requestedModel)
	if model == "" {
		model = strings.TrimSpace(runtime.ImageTextModel)
	}
	if model == "" {
		model = strings.TrimSpace(runtime.GrokModel)
	}
	if baseURL == "" {
		baseURL = strings.TrimSpace(runtime.GrokBaseURL)
	}
	if apiKey == "" {
		apiKey = strings.TrimSpace(runtime.GrokAPIKey)
	}
	if baseURL == "" || apiKey == "" || model == "" {
		return nil, "", consoleSettings.ErrNotConfigured
	}
	switch planner := h.planner.(type) {
	case imageproject.ConfiguredChatClient:
		planner.BaseURL, planner.APIKey, planner.Model = baseURL, apiKey, model
		return planner, model, nil
	case *imageproject.HTTPChatClient:
		return imageproject.ConfiguredChatClient{HTTP: planner.HTTP, BaseURL: baseURL, APIKey: apiKey, Model: model}, model, nil
	case nil:
		return imageproject.ConfiguredChatClient{BaseURL: baseURL, APIKey: apiKey, Model: model}, model, nil
	default:
		return planner, model, nil
	}
}

func (h *imageProjectsHandler) writePlannerError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, consoleSettings.ErrNotConfigured):
		writeError(w, http.StatusConflict, "image_text_model_not_configured", "Configure the image text model before requesting segment suggestions.")
	case strings.Contains(err.Error(), "rewrote") || strings.Contains(err.Error(), "cover the original") || strings.Contains(err.Error(), "must be the cover") || strings.Contains(err.Error(), "between"):
		writeError(w, http.StatusBadRequest, "invalid_image_segments", "The confirmed segments must keep the original script, start with a cover, and stay within 18 cards.")
	case strings.Contains(err.Error(), "confirmed segments"):
		writeError(w, http.StatusBadRequest, "segments_not_confirmed", "Confirm the AI segment suggestions before creating the image project.")
	default:
		detail := strings.TrimSpace(err.Error())
		if detail == "" {
			detail = "The text model could not produce a usable plan."
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"code":    "image_text_model_failed",
			"message": detail,
		})
	}
}

func (h *imageProjectsHandler) get(w http.ResponseWriter, r *http.Request) {
	project, items, err := h.repo.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrImageProjectNotFound) {
		writeError(w, http.StatusNotFound, "image_project_not_found", "The image project was not found.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "image_project_read_failed", "The image project could not be read.")
		return
	}
	writeJSON(w, http.StatusOK, domain.ImageProjectDetail{Project: project, Items: items})
}

func (h *imageProjectsHandler) updateItem(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SourceText string `json:"source_text"`
		Title      string `json:"title"`
		Prompt     string `json:"prompt"`
	}
	if err := decodeJSON(w, r, maxMessageJSONRequest, &in); err != nil {
		writeDecodeError(w, err, "invalid_image_item", "A valid image item is required.")
		return
	}
	in.Title, in.Prompt = strings.TrimSpace(in.Title), strings.TrimSpace(in.Prompt)
	if strings.TrimSpace(in.SourceText) == "" || in.Title == "" || in.Prompt == "" {
		writeError(w, http.StatusBadRequest, "invalid_image_item", "Image item fields are required.")
		return
	}
	if err := h.repo.UpdateItemText(r.Context(), r.PathValue("id"), r.PathValue("item"), in.SourceText, in.Title, in.Prompt, time.Now().UTC()); errors.Is(err, store.ErrImageProjectItemNotFound) {
		writeError(w, http.StatusNotFound, "image_item_not_found", "The image item was not found.")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "image_item_update_failed", "The image item could not be updated.")
		return
	}
	if status, err := h.aggregateProjectStatus(r.Context(), r.PathValue("id")); err == nil {
		_ = h.repo.MarkProjectStatus(r.Context(), r.PathValue("id"), status, time.Now().UTC())
	}
	h.get(w, r)
}

func (h *imageProjectsHandler) generateAll(w http.ResponseWriter, r *http.Request) {
	project, items, err := h.repo.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeReadError(w, err)
		return
	}
	selected := make([]domain.ImageProjectItem, 0, len(items))
	for _, item := range items {
		if item.Status != "ready" {
			selected = append(selected, item)
		}
	}
	if err := h.generate(r.Context(), project, selected); err != nil {
		if errors.Is(err, consoleSettings.ErrNotConfigured) {
			writeError(w, http.StatusConflict, "image_service_not_configured", "Configure the image service before generating.")
			return
		}
		writeError(w, http.StatusBadGateway, "image_generation_failed", "Image generation could not be completed.")
		return
	}
	h.get(w, r)
}

func (h *imageProjectsHandler) generateOne(w http.ResponseWriter, r *http.Request) {
	project, _, err := h.repo.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeReadError(w, err)
		return
	}
	item, err := h.repo.Item(r.Context(), project.ID, r.PathValue("item"))
	if err != nil {
		writeError(w, http.StatusNotFound, "image_item_not_found", "The image item was not found.")
		return
	}
	if err := h.generate(r.Context(), project, []domain.ImageProjectItem{item}); err != nil {
		if errors.Is(err, consoleSettings.ErrNotConfigured) {
			writeError(w, http.StatusConflict, "image_service_not_configured", "Configure the image service before generating.")
			return
		}
		writeError(w, http.StatusBadGateway, "image_generation_failed", "Image generation could not be completed.")
		return
	}
	h.get(w, r)
}

func (h *imageProjectsHandler) generate(ctx context.Context, project domain.ImageProject, items []domain.ImageProjectItem) error {
	if len(items) == 0 {
		return nil
	}
	if h.runtime == nil || h.generator == nil {
		return consoleSettings.ErrNotConfigured
	}
	runtime, err := h.runtime.Runtime(ctx)
	if err != nil || strings.TrimSpace(runtime.ImageBaseURL) == "" || strings.TrimSpace(runtime.ImageAPIKey) == "" || strings.TrimSpace(runtime.ImageModel) == "" {
		return consoleSettings.ErrNotConfigured
	}
	root := filepath.Join(runtime.DataRoot, "image-projects", project.ID)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	if err := h.repo.MarkProjectStatus(ctx, project.ID, "generating", time.Now().UTC()); err != nil {
		return err
	}
	attempts := project.ImageAttempts
	if attempts < 1 || attempts > 4 {
		attempts = runtime.ImageGenerationAttempts
	}
	if attempts < 1 || attempts > 4 {
		attempts = 2
	}
	model := strings.TrimSpace(project.ImageModel)
	if model == "" {
		model = runtime.ImageModel
	}
	requests := make([]imageproject.GenerateRequest, len(items))
	for index, item := range items {
		if err := h.repo.MarkItemGenerating(ctx, item.ID, time.Now().UTC()); err != nil {
			return err
		}
		requests[index] = imageproject.GenerateRequest{BaseURL: runtime.ImageBaseURL, APIKey: runtime.ImageAPIKey, Model: model, Prompt: item.Prompt, Ratio: project.Ratio, Stream: runtime.ImageStream}
	}
	workCtx, cancelGenerate := context.WithTimeout(context.WithoutCancel(ctx), imageproject.GenerateBatchBudget)
	defer cancelGenerate()
	results := imageproject.GenerateBatch(workCtx, h.generator, requests, minInt(project.Concurrency, runtime.MaxImageConcurrency), attempts)
	finalizeCtx, cancelFinalize := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelFinalize()
	for index, result := range results {
		item := items[index]
		if err := h.repo.MarkItemAttempts(finalizeCtx, item.ID, result.Attempts, time.Now().UTC()); err != nil {
			return err
		}
		if result.Error != nil {
			if item.ImagePath != nil {
				if err := h.repo.MarkItemRegenerationFailed(finalizeCtx, item.ID, item.Status, result.Error.Error(), time.Now().UTC()); err != nil {
					return err
				}
			} else {
				if err := h.repo.MarkItemFailed(finalizeCtx, item.ID, result.Error.Error(), time.Now().UTC()); err != nil {
					return err
				}
			}
			continue
		}
		extension := extensionForMIME(result.MIMEType)
		path := filepath.Join(root, fmt.Sprintf("%03d_%s_%s%s", item.Sequence, safeImageName(item.Title), uuid.NewString(), extension))
		if err := os.WriteFile(path, result.Bytes, 0o600); err != nil {
			if item.ImagePath != nil {
				if err := h.repo.MarkItemRegenerationFailed(finalizeCtx, item.ID, item.Status, "generated image could not be saved", time.Now().UTC()); err != nil {
					return err
				}
			} else {
				if err := h.repo.MarkItemFailed(finalizeCtx, item.ID, "generated image could not be saved", time.Now().UTC()); err != nil {
					return err
				}
			}
			continue
		}
		if err := h.repo.MarkItemReady(finalizeCtx, item.ID, path, result.MIMEType, result.Width, result.Height, time.Now().UTC()); err != nil {
			_ = os.Remove(path)
			return err
		}
		if item.ImagePath != nil && filepath.Clean(*item.ImagePath) != filepath.Clean(path) {
			_ = os.Remove(*item.ImagePath)
		}
	}
	status, statusErr := h.aggregateProjectStatus(finalizeCtx, project.ID)
	if statusErr != nil {
		return statusErr
	}
	return h.repo.MarkProjectStatus(finalizeCtx, project.ID, status, time.Now().UTC())
}

func (h *imageProjectsHandler) aggregateProjectStatus(ctx context.Context, projectID string) (string, error) {
	_, items, err := h.repo.Get(ctx, projectID)
	if err != nil {
		return "", err
	}
	ready, failed := 0, 0
	for _, item := range items {
		switch item.Status {
		case "ready":
			ready++
		case "failed":
			failed++
		}
	}
	switch {
	case ready == len(items):
		return "ready", nil
	case ready > 0:
		return "partial", nil
	case failed == len(items):
		return "failed", nil
	default:
		return "draft", nil
	}
}

func (h *imageProjectsHandler) image(w http.ResponseWriter, r *http.Request) {
	item, err := h.repo.Item(r.Context(), r.PathValue("id"), r.PathValue("item"))
	if err != nil || item.Status != "ready" || item.ImagePath == nil || item.MIMEType == nil {
		writeError(w, http.StatusNotFound, "image_not_found", "The generated image was not found.")
		return
	}
	file, err := os.Open(*item.ImagePath)
	if err != nil {
		writeError(w, http.StatusNotFound, "image_not_found", "The generated image was not found.")
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", *item.MIMEType)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.Copy(w, file)
}

func (h *imageProjectsHandler) download(w http.ResponseWriter, r *http.Request) {
	project, items, err := h.repo.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeReadError(w, err)
		return
	}
	for _, item := range items {
		if item.Status != "ready" || item.ImagePath == nil || item.MIMEType == nil {
			writeError(w, http.StatusConflict, "image_project_incomplete", "All images must be ready before downloading.")
			return
		}
	}
	temporary, err := os.CreateTemp("", "image-project-*.zip")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "image_archive_failed", "The image archive could not be created.")
		return
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	archive := zip.NewWriter(temporary)
	const maxArchiveInputSize int64 = 512 << 20
	var archiveInputSize int64
	manifestItems := make([]map[string]any, 0, len(items))
	for _, item := range items {
		entry := map[string]any{"sequence": item.Sequence, "title": item.Title, "source_text": item.SourceText, "prompt": item.Prompt, "status": item.Status, "width": item.Width, "height": item.Height}
		filename := fmt.Sprintf("%03d_%s%s", item.Sequence, safeImageName(item.Title), extensionForMIME(*item.MIMEType))
		file, openErr := os.Open(*item.ImagePath)
		if openErr != nil {
			_ = archive.Close()
			_ = temporary.Close()
			writeError(w, http.StatusConflict, "image_archive_source_missing", "A generated image is missing.")
			return
		}
		info, statErr := file.Stat()
		if statErr != nil || info.Size() > maxImageArchiveEntrySize || archiveInputSize+info.Size() > maxArchiveInputSize {
			_ = file.Close()
			_ = archive.Close()
			_ = temporary.Close()
			writeError(w, http.StatusRequestEntityTooLarge, "image_archive_too_large", "The image archive is too large.")
			return
		}
		writer, createErr := archive.Create(filename)
		if createErr != nil {
			_ = file.Close()
			_ = archive.Close()
			_ = temporary.Close()
			writeError(w, http.StatusInternalServerError, "image_archive_failed", "The image archive could not be created.")
			return
		}
		_, copyErr := io.Copy(writer, io.LimitReader(file, maxImageArchiveEntrySize+1))
		_ = file.Close()
		if copyErr != nil {
			_ = archive.Close()
			_ = temporary.Close()
			writeError(w, http.StatusInternalServerError, "image_archive_failed", "The image archive could not be created.")
			return
		}
		archiveInputSize += info.Size()
		entry["filename"] = filename
		manifestItems = append(manifestItems, entry)
	}
	var current *domain.PublishingCandidate
	for i := range project.PublishingCandidates {
		if project.SelectedPosition != nil && project.PublishingCandidates[i].Position == *project.SelectedPosition {
			current = &project.PublishingCandidates[i]
		}
	}
	manifest, _ := json.MarshalIndent(map[string]any{"project": project, "items": manifestItems, "publishing": domain.PublishingState{Current: current, All: project.PublishingCandidates}}, "", "  ")
	writer, _ := archive.Create("manifest.json")
	_, _ = writer.Write(manifest)
	if err := archive.Close(); err != nil || temporary.Close() != nil {
		writeError(w, http.StatusInternalServerError, "image_archive_failed", "The image archive could not be created.")
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.zip"`, safeImageName(project.Title)))
	file, err := os.Open(temporaryPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "image_archive_failed", "The image archive could not be opened.")
		return
	}
	defer file.Close()
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, file)
}

func (h *imageProjectsHandler) delete(w http.ResponseWriter, r *http.Request) {
	_, items, err := h.repo.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeReadError(w, err)
		return
	}
	if err := h.repo.Delete(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, http.StatusInternalServerError, "image_project_delete_failed", "The image project could not be deleted.")
		return
	}
	for _, item := range items {
		if item.ImagePath != nil {
			_ = os.Remove(*item.ImagePath)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *imageProjectsHandler) writeReadError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrImageProjectNotFound) {
		writeError(w, http.StatusNotFound, "image_project_not_found", "The image project was not found.")
		return
	}
	writeError(w, http.StatusInternalServerError, "image_project_read_failed", "The image project could not be read.")
}

var unsafeImageName = regexp.MustCompile(`[^\p{Han}A-Za-z0-9_-]+`)

func itemTitle(text string, sequence int) string {
	value := strings.TrimSpace(strings.TrimRight(text, "。！？!?；;，,"))
	runes := []rune(value)
	if len(runes) > 12 {
		value = string(runes[:12])
	}
	if value == "" {
		return fmt.Sprintf("图片%d", sequence)
	}
	return value
}

func safeImageName(value string) string {
	value = unsafeImageName.ReplaceAllString(strings.TrimSpace(value), "_")
	value = strings.Trim(value, "._-")
	if value == "" {
		return "image"
	}
	runes := []rune(value)
	if len(runes) > 40 {
		value = string(runes[:40])
	}
	return value
}

func validRatio(value string) bool {
	return value == "3:4" || value == "4:3" || value == "9:16" || value == "1:1"
}

func validStyle(value string) bool {
	switch value {
	case "finance_documentary", "red_ink", "old_newspaper", "ledger_investigation", "dark_crisis", "city_era", "blackboard", "custom":
		return true
	default:
		return false
	}
}

func extensionForMIME(value string) string {
	switch value {
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	default:
		return ".png"
	}
}

func minInt(left, right int) int {
	if left < 1 {
		left = 1
	}
	if right < 1 {
		right = 1
	}
	if left < right {
		return left
	}
	return right
}
