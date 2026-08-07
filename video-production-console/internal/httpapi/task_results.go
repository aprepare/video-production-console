package httpapi

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"video-production-console/internal/domain"
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

type publishingTitleRecommendation struct {
	Rank   int    `json:"rank"`
	Title  string `json:"title"`
	Reason string `json:"reason"`
}

type publishingPackageView struct {
	Titles       []string                        `json:"titles"`
	TopTitles    []publishingTitleRecommendation `json:"top_titles"`
	ShortTitles  []string                        `json:"short_titles"`
	Descriptions []string                        `json:"descriptions"`
	Description  string                          `json:"description"`
	Topics       []string                        `json:"topics"`
	CTA          string                          `json:"cta"`
}

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
		info, statErr := os.Lstat(artifact.Path)
		if statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("publishing package is unavailable")
		}
		file, openErr := os.Open(artifact.Path)
		if openErr != nil {
			return nil, openErr
		}
		data, readErr := io.ReadAll(io.LimitReader(file, 512*1024+1))
		_ = file.Close()
		if readErr != nil || len(data) > 512*1024 {
			return nil, errors.New("publishing package could not be read")
		}
		digest := sha256.Sum256(data)
		if artifact.SHA256 != "" && !strings.EqualFold(hex.EncodeToString(digest[:]), artifact.SHA256) {
			return nil, errors.New("publishing package changed after validation")
		}
		var packageView publishingPackageView
		if json.Unmarshal(data, &packageView) != nil {
			return nil, errors.New("publishing package is invalid")
		}
		return &packageView, nil
	}
	return nil, nil
}

func (h *taskResultsHandler) montageResult(r *http.Request, task domain.CodexTask) (map[string]any, error) {
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
		assets, assetErr := store.NewAssetRepository(h.repo.DB()).CurrentByProject(r.Context(), *task.ProjectID)
		if assetErr != nil {
			return nil, assetErr
		}
		for _, asset := range assets {
			if asset.Type == domain.AssetMixDraft && asset.State == domain.AssetReady {
				registeredAsset = map[string]any{"id": asset.ID, "filename": asset.Filename, "path": asset.Path, "sha256": asset.SHA256, "created_at": asset.CreatedAt}
				break
			}
		}
	}
	canRetry := false
	if len(attempts) > 0 && workspace != nil {
		canRetry = attempts[0].State == domain.RegistrationFailed || attempts[0].State == domain.RegistrationInterrupted
	}
	return map[string]any{"phase": task.CompletionPhase, "workspace": workspace, "registration_attempts": registrationViews, "registered_asset": registeredAsset, "can_retry_registration": canRetry}, nil
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
