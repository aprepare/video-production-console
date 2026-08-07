package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"video-production-console/internal/domain"
	"video-production-console/internal/history"
)

type historyAPI interface {
	List(context.Context, int, string) ([]history.ThreadSummary, error)
	Read(context.Context, string) (history.ThreadDetail, error)
	Resume(context.Context, string) (domain.ChatSession, error)
	Fork(context.Context, string) (domain.ChatSession, error)
}
type historyHandler struct{ service historyAPI }

func NewHistoryHandler(service historyAPI) http.Handler {
	h := &historyHandler{service: service}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/codex/history", h.list)
	mux.HandleFunc("GET /api/codex/history/{id}", h.read)
	mux.HandleFunc("POST /api/codex/history/{id}/resume", h.resume)
	mux.HandleFunc("POST /api/codex/history/{id}/fork", h.fork)
	return mux
}
func (h *historyHandler) list(w http.ResponseWriter, r *http.Request) {
	limit := 10
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, 400, "invalid_history_limit", "History limit is invalid.")
			return
		}
		limit = parsed
	}
	source := r.URL.Query().Get("source")
	if source != "" && source != "desktop" && source != "cli" && source != "task" {
		writeError(w, 400, "invalid_history_source", "History source is invalid.")
		return
	}
	items, err := h.service.List(r.Context(), limit, source)
	if err != nil {
		writeError(w, 500, "history_list_failed", "History could not be read.")
		return
	}
	view := make([]historyThreadView, 0, len(items))
	for _, item := range items {
		view = append(view, toHistoryThreadView(item))
	}
	writeJSON(w, 200, view)
}
func (h *historyHandler) read(w http.ResponseWriter, r *http.Request) {
	id, ok := historyID(w, r)
	if !ok {
		return
	}
	item, err := h.service.Read(r.Context(), id)
	if err != nil {
		writeError(w, 404, "history_not_found", "History conversation was not found.")
		return
	}
	writeJSON(w, 200, map[string]any{"thread": toHistoryThreadView(item.ThreadSummary), "messages": item.Messages})
}
func (h *historyHandler) resume(w http.ResponseWriter, r *http.Request) {
	id, ok := historyID(w, r)
	if !ok {
		return
	}
	session, err := h.service.Resume(r.Context(), id)
	if err != nil {
		writeHistoryError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toChatSessionView(session))
}
func (h *historyHandler) fork(w http.ResponseWriter, r *http.Request) {
	id, ok := historyID(w, r)
	if !ok {
		return
	}
	session, err := h.service.Fork(r.Context(), id)
	if err != nil {
		writeHistoryError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toChatSessionView(session))
}

type historyThreadView struct {
	ID              string    `json:"id"`
	Title           string    `json:"title"`
	Preview         string    `json:"preview"`
	Source          string    `json:"source"`
	Model           string    `json:"model,omitempty"`
	ReasoningEffort string    `json:"reasoning_effort,omitempty"`
	Active          bool      `json:"active"`
	Recency         time.Time `json:"recency"`
}

func toHistoryThreadView(item history.ThreadSummary) historyThreadView {
	return historyThreadView{ID: item.ID, Title: item.Title, Preview: item.Preview, Source: item.Source, Model: item.Model, ReasoningEffort: item.ReasoningEffort, Active: item.Active, Recency: item.Recency}
}
func historyID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" || strings.Contains(id, "/") {
		writeError(w, 400, "invalid_history_id", "History conversation ID is invalid.")
		return "", false
	}
	return id, true
}
func writeHistoryError(w http.ResponseWriter, err error) {
	if strings.Contains(err.Error(), "thread_active_elsewhere") {
		writeError(w, http.StatusConflict, "thread_active_elsewhere", "This conversation is active elsewhere. Wait or fork it.")
		return
	}
	if errors.Is(err, context.Canceled) {
		writeError(w, http.StatusConflict, "history_request_cancelled", "History request was cancelled.")
		return
	}
	writeError(w, 500, "history_operation_failed", "History operation could not be completed.")
}
