package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"net/http"
	"strings"
	"time"
	"video-production-console/internal/codex"
	"video-production-console/internal/domain"
	"video-production-console/internal/store"
)

type taskAPI struct {
	repo      *store.TaskRepository
	scheduler codex.Scheduler
	preparer  TaskManifestPreparer
}

func NewTasksHandler(db *sql.DB, s codex.Scheduler, preparers ...TaskManifestPreparer) http.Handler {
	var preparer TaskManifestPreparer
	if len(preparers) > 0 {
		preparer = preparers[0]
	}
	h := &taskAPI{repo: store.NewTaskRepository(db), scheduler: s, preparer: preparer}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/projects/{id}/tasks", h.create)
	mux.HandleFunc("GET /api/tasks", h.list)
	mux.HandleFunc("GET /api/tasks/{id}", h.get)
	mux.HandleFunc("POST /api/tasks/{id}/answer", h.answer)
	mux.HandleFunc("POST /api/tasks/{id}/cancel", h.cancel)
	return mux
}

type taskView struct {
	ID             string               `json:"id"`
	ProjectID      *string              `json:"project_id,omitempty"`
	AccountID      string               `json:"account_id"`
	Type           string               `json:"type"`
	SkillName      string               `json:"skill_name"`
	Action         domain.TaskAction    `json:"action"`
	Status         domain.TaskStatus    `json:"status"`
	CodexSessionID *string              `json:"codex_session_id,omitempty"`
	ResultSummary  *string              `json:"result_summary,omitempty"`
	ErrorCode      *string              `json:"error_code,omitempty"`
	ErrorMessage   *string              `json:"error_message,omitempty"`
	CreatedAt      time.Time            `json:"created_at"`
	StartedAt      *time.Time           `json:"started_at,omitempty"`
	FinishedAt     *time.Time           `json:"finished_at,omitempty"`
	Events         []domain.TaskEvent   `json:"events,omitempty"`
	Messages       []domain.TaskMessage `json:"messages,omitempty"`
}

func viewTask(t domain.CodexTask) taskView {
	return taskView{t.ID, t.ProjectID, t.AccountID, t.Type, t.SkillName, t.Action, t.Status, t.CodexSessionID, t.ResultSummary, t.ErrorCode, t.ErrorMessage, t.CreatedAt, t.StartedAt, t.FinishedAt, nil, nil}
}
func (h *taskAPI) create(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("id")
	if _, e := uuid.Parse(pid); e != nil {
		writeError(w, 400, "invalid_project_id", "Project ID must be a UUID.")
		return
	}
	var in struct {
		AccountID string            `json:"account_id"`
		Type      string            `json:"type"`
		Action    domain.TaskAction `json:"action"`
		Prompt    string            `json:"prompt"`
		TaskManifestRequest
	}
	if decodeJSON(r, &in) != nil || strings.TrimSpace(in.Type) == "" {
		writeError(w, 400, "invalid_task", "Task type and prompt are required.")
		return
	}
	action, resolved, e := codex.ResolveTaskAction(in.Type, in.Action)
	if e != nil {
		writeError(w, 400, "invalid_task_type", e.Error())
		return
	}
	if _, e := uuid.Parse(in.AccountID); e != nil {
		writeError(w, 400, "invalid_account_id", "Account ID must be a UUID.")
		return
	}
	id := uuid.NewString()
	p := pid
	t := domain.CodexTask{ID: id, ProjectID: &p, AccountID: in.AccountID, Type: in.Type, SkillName: resolved.Skill, Action: action, Status: domain.TaskQueued, PromptSnapshot: in.Prompt, CreatedAt: time.Now().UTC()}
	if h.preparer != nil {
		if e := h.preparer.Prepare(r.Context(), t, in.TaskManifestRequest); e != nil {
			writeError(w, http.StatusConflict, "task_manifest_not_ready", e.Error())
			return
		}
	}
	if e := h.scheduler.Enqueue(r.Context(), t); e != nil {
		writeError(w, 500, "task_enqueue_failed", e.Error())
		return
	}
	writeJSON(w, 201, viewTask(t))
}
func (h *taskAPI) list(w http.ResponseWriter, r *http.Request) {
	ts, e := h.repo.List(r.Context(), r.URL.Query().Get("project_id"), domain.TaskStatus(r.URL.Query().Get("status")))
	if e != nil {
		writeError(w, 500, "tasks_list_failed", e.Error())
		return
	}
	out := make([]taskView, 0, len(ts))
	for _, t := range ts {
		out = append(out, viewTask(t))
	}
	writeJSON(w, 200, out)
}
func (h *taskAPI) get(w http.ResponseWriter, r *http.Request) {
	t, e := h.repo.Get(r.Context(), r.PathValue("id"))
	if errors.Is(e, sql.ErrNoRows) {
		writeError(w, 404, "task_not_found", "Task was not found.")
		return
	}
	if e != nil {
		writeError(w, 500, "task_read_failed", e.Error())
		return
	}
	v := viewTask(t)
	v.Events, _ = h.repo.Events(r.Context(), t.ID)
	v.Messages, _ = h.repo.Messages(r.Context(), t.ID)
	writeJSON(w, 200, v)
}
func (h *taskAPI) answer(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Answer string `json:"answer"`
	}
	if decodeJSON(r, &in) != nil || strings.TrimSpace(in.Answer) == "" {
		writeError(w, 400, "answer_required", "Answer is required.")
		return
	}
	e := h.scheduler.Resume(r.Context(), r.PathValue("id"), in.Answer)
	if errors.Is(e, sql.ErrNoRows) {
		writeError(w, 404, "task_not_found", "Task was not found.")
		return
	}
	if e != nil {
		writeError(w, 409, "task_resume_conflict", e.Error())
		return
	}
	t, e := h.repo.Get(context.Background(), r.PathValue("id"))
	if e != nil {
		writeError(w, 500, "task_read_failed", e.Error())
		return
	}
	writeJSON(w, 200, viewTask(t))
}
func (h *taskAPI) cancel(w http.ResponseWriter, r *http.Request) {
	e := h.scheduler.Cancel(r.Context(), r.PathValue("id"))
	if errors.Is(e, sql.ErrNoRows) {
		writeError(w, 404, "task_not_found", "Task was not found.")
		return
	}
	if e != nil {
		writeError(w, 409, "task_cancel_conflict", e.Error())
		return
	}
	t, e := h.repo.Get(r.Context(), r.PathValue("id"))
	if e != nil {
		writeError(w, 500, "task_read_failed", e.Error())
		return
	}
	writeJSON(w, 200, viewTask(t))
}

var _ = json.Valid
