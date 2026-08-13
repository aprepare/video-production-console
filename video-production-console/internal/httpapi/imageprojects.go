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
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"video-production-console/internal/domain"
	"video-production-console/internal/imageproject"
	consoleSettings "video-production-console/internal/settings"
	"video-production-console/internal/store"
)

type imageRuntimeProvider interface {
	Runtime(context.Context) (consoleSettings.Runtime, error)
}

const maxImageArchiveEntrySize int64 = 32 << 20

type imageProjectsHandler struct {
	repo      *store.ImageProjectRepository
	runtime   imageRuntimeProvider
	generator imageproject.Generator
}

func NewImageProjectsHandler(db *sql.DB, runtime imageRuntimeProvider, generator imageproject.Generator) http.Handler {
	h := &imageProjectsHandler{repo: store.NewImageProjectRepository(db), runtime: runtime, generator: generator}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/image-projects", h.list)
	mux.HandleFunc("POST /api/image-projects", h.create)
	mux.HandleFunc("GET /api/image-projects/{id}", h.get)
	mux.HandleFunc("DELETE /api/image-projects/{id}", h.delete)
	mux.HandleFunc("PATCH /api/image-projects/{id}/items/{item}", h.updateItem)
	mux.HandleFunc("POST /api/image-projects/{id}/generate", h.generateAll)
	mux.HandleFunc("POST /api/image-projects/{id}/items/{item}/generate", h.generateOne)
	mux.HandleFunc("GET /api/image-projects/{id}/items/{item}/image", h.image)
	mux.HandleFunc("GET /api/image-projects/{id}/download", h.download)
	return mux
}

func (h *imageProjectsHandler) list(w http.ResponseWriter, r *http.Request) {
	projects, err := h.repo.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "image_projects_list_failed", "Image projects could not be listed.")
		return
	}
	writeJSON(w, http.StatusOK, projects)
}

func (h *imageProjectsHandler) create(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Title       string `json:"title"`
		Script      string `json:"script"`
		ImageCount  int    `json:"image_count"`
		Ratio       string `json:"ratio"`
		Style       string `json:"style"`
		CustomStyle string `json:"custom_style"`
		Concurrency int    `json:"concurrency"`
	}
	if err := decodeJSON(w, r, maxTaskJSONRequest, &in); err != nil {
		writeDecodeError(w, err, "invalid_image_project", "A valid image project is required.")
		return
	}
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" || strings.TrimSpace(in.Script) == "" || utf8.RuneCountInString(in.Title) > 120 || len([]byte(in.Script)) > 1<<20 || !validRatio(in.Ratio) || !validStyle(in.Style) || (in.Style == "custom" && strings.TrimSpace(in.CustomStyle) == "") || in.Concurrency < 1 || in.Concurrency > 5 {
		writeError(w, http.StatusBadRequest, "invalid_image_project", "Image project settings are invalid.")
		return
	}
	parts, err := imageproject.SplitScript(in.Script, in.ImageCount)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_image_count", "The script could not be split into that image count.")
		return
	}
	now := time.Now().UTC()
	project := domain.ImageProject{ID: uuid.NewString(), Title: in.Title, Script: in.Script, ImageCount: len(parts), Ratio: in.Ratio, Style: in.Style, CustomStyle: strings.TrimSpace(in.CustomStyle), Concurrency: in.Concurrency, Status: "draft", CreatedAt: now, UpdatedAt: now}
	items := make([]domain.ImageProjectItem, 0, len(parts))
	for index, part := range parts {
		items = append(items, domain.ImageProjectItem{ID: uuid.NewString(), ProjectID: project.ID, Sequence: index + 1, SourceText: part, Title: itemTitle(part, index+1), Prompt: imageproject.BuildPrompt(imageproject.PromptInput{SourceText: part, Ratio: project.Ratio, Style: project.Style, CustomStyle: project.CustomStyle}), Status: "pending", CreatedAt: now, UpdatedAt: now})
	}
	if err := h.repo.Create(r.Context(), project, items); err != nil {
		writeError(w, http.StatusInternalServerError, "image_project_create_failed", "The image project could not be created.")
		return
	}
	writeJSON(w, http.StatusCreated, domain.ImageProjectDetail{Project: project, Items: items})
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
	requests := make([]imageproject.GenerateRequest, len(items))
	for index, item := range items {
		if err := h.repo.MarkItemGenerating(ctx, item.ID, time.Now().UTC()); err != nil {
			return err
		}
		requests[index] = imageproject.GenerateRequest{BaseURL: runtime.ImageBaseURL, APIKey: runtime.ImageAPIKey, Model: runtime.ImageModel, Prompt: item.Prompt, Ratio: project.Ratio}
	}
	results := imageproject.GenerateBatch(ctx, h.generator, requests, minInt(project.Concurrency, runtime.MaxImageConcurrency))
	finalizeCtx, cancelFinalize := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelFinalize()
	for index, result := range results {
		item := items[index]
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
		if item.Status == "ready" && item.ImagePath != nil && item.MIMEType != nil {
			filename := fmt.Sprintf("%03d_%s%s", item.Sequence, safeImageName(item.Title), extensionForMIME(*item.MIMEType))
			file, openErr := os.Open(*item.ImagePath)
			if openErr == nil {
				info, statErr := file.Stat()
				if statErr == nil && info.Size() <= maxImageArchiveEntrySize && archiveInputSize+info.Size() <= maxArchiveInputSize {
					writer, createErr := archive.Create(filename)
					if createErr == nil {
						_, copyErr := io.Copy(writer, io.LimitReader(file, maxImageArchiveEntrySize+1))
						if copyErr == nil {
							archiveInputSize += info.Size()
							entry["filename"] = filename
						}
					}
				}
				_ = file.Close()
			}
		}
		manifestItems = append(manifestItems, entry)
	}
	manifest, _ := json.MarshalIndent(map[string]any{"project": project, "items": manifestItems}, "", "  ")
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
