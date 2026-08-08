package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"video-production-console/internal/codex"
	"video-production-console/internal/domain"
	"video-production-console/internal/publishing"
	"video-production-console/internal/store"
)

type taskResultsHandler struct {
	repo     *store.TaskRepository
	semantic *store.ConversationRepository
}

func NewTaskResultsHandler(repo *store.TaskRepository) http.Handler {
	h := &taskResultsHandler{repo: repo, semantic: store.NewConversationRepository(repo.DB())}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/tasks/{id}/artifacts", h.artifacts)
	mux.HandleFunc("GET /api/tasks/{id}/result", h.result)
	mux.HandleFunc("GET /api/tasks/{id}/diagnostics", h.diagnostics)
	mux.HandleFunc("GET /api/tasks/{id}/semantic-events", h.semanticEvents)
	return mux
}
func (h *taskResultsHandler) artifacts(w http.ResponseWriter, r *http.Request) {
	items, err := h.repo.Artifacts(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, 500, "task_artifacts_failed", "Task artifacts could not be read.")
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{"id": item.ID, "kind": item.Kind, "filename": item.Filename, "mime_type": item.MIMEType, "size": item.Size, "created_at": item.CreatedAt})
	}
	writeJSON(w, 200, out)
}
func (h *taskResultsHandler) result(w http.ResponseWriter, r *http.Request) {
	task, err := h.repo.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, 404, "task_not_found", "Task was not found.")
		return
	}
	view := map[string]any{"id": task.ID, "action": task.Action, "status": task.Status, "completion_phase": task.CompletionPhase, "summary": task.ResultSummary, "error_code": task.ErrorCode, "error_message": task.ErrorMessage}
	if task.Action == domain.ActionMontageExecute {
		montage, montageErr := h.montageResult(r, task)
		if montageErr != nil {
			writeError(w, http.StatusInternalServerError, "montage_result_failed", "Montage result could not be read.")
			return
		}
		view["montage"] = montage
	}
	if isRemixAction(task.Action) {
		if publishing, publishingErr := h.publishingPackage(r, task.ID); publishingErr == nil && publishing != nil {
			view["publishing_package"] = publishing
		}
	}
	writeJSON(w, 200, view)
}

type publishingPackageView = publishing.Package

func isRemixAction(action domain.TaskAction) bool {
	switch action {
	case domain.ActionRemixStandard, domain.ActionRemixEnhanced, domain.ActionRemixFromTopic:
		return true
	default:
		return false
	}
}

func (h *taskResultsHandler) publishingPackage(r *http.Request, taskID string) (*publishingPackageView, error) {
	artifacts, err := h.repo.Artifacts(r.Context(), taskID)
	if err != nil {
		return nil, err
	}
	for _, artifact := range artifacts {
		if artifact.Kind != "publishing_package" {
			continue
		}
		packageView, readErr := (publishing.Reader{}).Read(artifact.Path, artifact.SHA256)
		if readErr != nil {
			return nil, readErr
		}
		return &packageView, nil
	}
	return nil, nil
}

func (h *taskResultsHandler) montageResult(r *http.Request, task domain.CodexTask) (map[string]any, error) {
	displayName := h.draftDisplayName(r, task.ID)
	storageName := ""
	attempts, err := store.NewMontageRepository(h.repo.DB()).Attempts(r.Context(), task.ID)
	if err != nil {
		return nil, err
	}
	artifacts, err := h.repo.Artifacts(r.Context(), task.ID)
	if err != nil {
		return nil, err
	}
	var workspace any
	for _, artifact := range artifacts {
		if artifact.Kind == "plaintext_workspace" {
			workspace = map[string]any{"id": artifact.ID, "kind": artifact.Kind, "filename": artifact.Filename, "mime_type": artifact.MIMEType, "size": artifact.Size, "created_at": artifact.CreatedAt}
			break
		}
	}
	registrationViews := make([]map[string]any, 0, len(attempts))
	for _, attempt := range attempts {
		registrationViews = append(registrationViews, map[string]any{"id": attempt.ID, "attempt": attempt.Attempt, "state": attempt.State, "registered_path": attempt.RegisteredPath, "receipt_path": attempt.ReceiptPath, "error_code": attempt.ErrorCode, "error_message": attempt.ErrorMessage, "started_at": attempt.StartedAt, "finished_at": attempt.FinishedAt})
	}
	var registeredAsset any
	if task.ProjectID != nil {
		assets, assetErr := store.NewAssetRepository(h.repo.DB()).ReadyMixDraftsByProject(r.Context(), *task.ProjectID)
		if assetErr != nil {
			return nil, assetErr
		}
		asset, found, selectErr := h.registeredDraftForTask(r, task, assets)
		if selectErr != nil {
			return nil, selectErr
		}
		if found {
			storageName = filepath.Base(filepath.Clean(asset.Path))
			registeredAsset = map[string]any{"id": asset.ID, "filename": asset.Filename, "path": asset.Path, "sha256": asset.SHA256, "display_name": displayName, "storage_name": storageName, "created_at": asset.CreatedAt}
		}
	}
	canRetry := false
	if len(attempts) > 0 && workspace != nil {
		canRetry = attempts[0].State == domain.RegistrationFailed || attempts[0].State == domain.RegistrationInterrupted
	}
	return map[string]any{"phase": task.CompletionPhase, "workspace": workspace, "registration_attempts": registrationViews, "registered_asset": registeredAsset, "display_name": displayName, "storage_name": storageName, "can_retry_registration": canRetry}, nil
}

func (h *taskResultsHandler) registeredDraftForTask(r *http.Request, task domain.CodexTask, ready []domain.AssetVersion) (domain.AssetVersion, bool, error) {
	for _, asset := range ready {
		if asset.SourceTaskID != nil && strings.TrimSpace(*asset.SourceTaskID) == task.ID {
			return asset, true, nil
		}
	}
	if len(ready) != 1 || task.ProjectID == nil {
		return domain.AssetVersion{}, false, nil
	}
	legacySource := ready[0].SourceTaskID == nil || strings.TrimSpace(*ready[0].SourceTaskID) == ""
	if !legacySource {
		return domain.AssetVersion{}, false, nil
	}
	projectTasks, err := h.repo.List(r.Context(), *task.ProjectID, "")
	if err != nil {
		return domain.AssetVersion{}, false, err
	}
	montageTasks := 0
	for _, projectTask := range projectTasks {
		if projectTask.Action == domain.ActionMontageExecute {
			montageTasks++
			if projectTask.ID != task.ID {
				return domain.AssetVersion{}, false, nil
			}
		}
	}
	if montageTasks != 1 {
		return domain.AssetVersion{}, false, nil
	}
	return ready[0], true, nil
}

func (h *taskResultsHandler) draftDisplayName(r *http.Request, taskID string) string {
	_, manifestPath, err := h.repo.PreparedManifest(r.Context(), taskID)
	if err != nil {
		return ""
	}
	info, err := os.Lstat(manifestPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return ""
	}
	file, err := os.Open(manifestPath)
	if err != nil {
		return ""
	}
	data, readErr := io.ReadAll(io.LimitReader(file, 1024*1024+1))
	_ = file.Close()
	if readErr != nil || len(data) > 1024*1024 {
		return ""
	}
	var manifest codex.TaskManifest
	if json.Unmarshal(data, &manifest) != nil {
		return ""
	}
	return strings.TrimSpace(manifest.NonSecretSettings.DraftDisplayName)
}
func (h *taskResultsHandler) diagnostics(w http.ResponseWriter, r *http.Request) {
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 {
		limit = 100
	}
	if limit > 100 {
		limit = 100
	}
	events, err := h.repo.Events(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, 500, "task_diagnostics_failed", "Task diagnostics could not be read.")
		return
	}
	out := make([]map[string]any, 0, limit)
	for _, event := range events {
		if event.Sequence <= after {
			continue
		}
		out = append(out, map[string]any{"sequence": event.Sequence, "kind": event.Kind, "level": event.Level, "display_text": event.DisplayText, "created_at": event.CreatedAt})
		if len(out) == limit {
			break
		}
	}
	writeJSON(w, 200, map[string]any{"events": out, "after": after, "limit": limit})
}

// semanticEvents exposes the short, Chinese progress timeline used by the
// workbench. Raw protocol JSON deliberately remains out of this endpoint.
func (h *taskResultsHandler) semanticEvents(w http.ResponseWriter, r *http.Request) {
	if _, err := h.repo.Get(r.Context(), r.PathValue("id")); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "task_not_found", "Task was not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, "task_read_failed", "Task could not be read.")
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	events, err := h.semantic.SemanticForTask(r.Context(), r.PathValue("id"), after, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "task_progress_failed", "Task progress could not be read.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events, "after": after, "limit": normalizedLimit(limit)})
}

func normalizedLimit(limit int) int {
	if limit < 1 {
		return 100
	}
	if limit > 100 {
		return 100
	}
	return limit
}
