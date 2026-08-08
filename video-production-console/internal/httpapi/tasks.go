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
	"video-production-console/internal/taskmodel"
)

type taskAPI struct {
	repo      *store.TaskRepository
	projects  *store.ProjectRepository
	accounts  *store.AccountRepository
	scheduler codex.Scheduler
	preparer  TaskManifestPreparer
	models    TaskModelResolver
}

func NewTasksHandler(db *sql.DB, s codex.Scheduler, preparer TaskManifestPreparer, models TaskModelResolver) http.Handler {
	h := &taskAPI{repo: store.NewTaskRepository(db), projects: store.NewProjectRepository(db), accounts: store.NewAccountRepository(db), scheduler: s, preparer: preparer, models: models}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/projects/{id}/tasks", h.create)
	mux.HandleFunc("POST /api/projects/{id}/topic-card", h.commitTopicCard)
	mux.HandleFunc("GET /api/tasks", h.list)
	mux.HandleFunc("GET /api/tasks/{id}", h.get)
	mux.HandleFunc("POST /api/tasks/{id}/answer", h.answer)
	mux.HandleFunc("POST /api/tasks/{id}/cancel", h.cancel)
	return mux
}

func (h *taskAPI) commitTopicCard(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("id")
	if _, err := uuid.Parse(pid); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_project_id", "Project ID must be a UUID.")
		return
	}
	project, err := h.projects.GetProject(r.Context(), pid)
	if errors.Is(err, store.ErrProjectNotFound) || errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "project_not_found", "Project was not found.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "project_read_failed", "Project could not be read.")
		return
	}
	versions, err := store.NewAssetRepository(h.repo.DB()).CurrentByProject(r.Context(), pid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "project_assets_failed", "Project assets could not be read.")
		return
	}
	for _, version := range versions {
		if version.Type == domain.AssetTopicCard && version.State == domain.AssetReady {
			writeError(w, http.StatusConflict, "topic_card_exists", "This project already has a formal topic card.")
			return
		}
	}
	selection, err := findProjectTopicSelection(r.Context(), h.repo.DB(), project)
	if err != nil {
		writeError(w, http.StatusConflict, "topic_candidate_missing", "No confirmed candidate source could be matched to this project.")
		return
	}
	task, err := enqueueTopicCommit(r.Context(), h.repo.DB(), h.scheduler, h.preparer, h.models, project, selection)
	if err != nil {
		writeError(w, http.StatusConflict, "topic_commit_not_ready", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, viewTask(task))
}

type taskView struct {
	ID              string               `json:"id"`
	ProjectID       *string              `json:"project_id,omitempty"`
	AccountID       string               `json:"account_id"`
	Type            string               `json:"type"`
	SkillName       string               `json:"skill_name"`
	Action          domain.TaskAction    `json:"action"`
	Status          domain.TaskStatus    `json:"status"`
	CodexSessionID  *string              `json:"codex_session_id,omitempty"`
	ModelName       string               `json:"model"`
	ReasoningEffort string               `json:"reasoning_effort"`
	ResultSummary   *string              `json:"result_summary,omitempty"`
	ErrorCode       *string              `json:"error_code,omitempty"`
	ErrorMessage    *string              `json:"error_message,omitempty"`
	CreatedAt       time.Time            `json:"created_at"`
	StartedAt       *time.Time           `json:"started_at,omitempty"`
	FinishedAt      *time.Time           `json:"finished_at,omitempty"`
	Events          []domain.TaskEvent   `json:"events,omitempty"`
	Messages        []domain.TaskMessage `json:"messages,omitempty"`
}

func viewTask(t domain.CodexTask) taskView {
	return taskView{ID: t.ID, ProjectID: t.ProjectID, AccountID: t.AccountID, Type: t.Type, SkillName: t.SkillName, Action: t.Action, Status: t.Status, CodexSessionID: t.CodexSessionID, ModelName: t.ModelName, ReasoningEffort: t.ReasoningEffort, ResultSummary: t.ResultSummary, ErrorCode: t.ErrorCode, ErrorMessage: t.ErrorMessage, CreatedAt: t.CreatedAt, StartedAt: t.StartedAt, FinishedAt: t.FinishedAt}
}
func (h *taskAPI) create(w http.ResponseWriter, r *http.Request) {
	prepareStartedAt := time.Now().UTC()
	pid := r.PathValue("id")
	if _, e := uuid.Parse(pid); e != nil {
		writeError(w, 400, "invalid_project_id", "Project ID must be a UUID.")
		return
	}
	var in struct {
		AccountID     string            `json:"account_id"`
		Type          string            `json:"type"`
		Action        domain.TaskAction `json:"action"`
		Prompt        string            `json:"prompt"`
		ChatSessionID string            `json:"chat_session_id"`
		TaskManifestRequest
		taskModelRequest
	}
	if decodeJSON(r, &in) != nil || strings.TrimSpace(in.Type) == "" || strings.TrimSpace(in.Prompt) == "" {
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
	project, e := h.projects.GetProject(r.Context(), pid)
	if errors.Is(e, store.ErrProjectNotFound) || errors.Is(e, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "project_not_found", "Project was not found.")
		return
	}
	if e != nil {
		writeError(w, http.StatusInternalServerError, "project_read_failed", "Project could not be read.")
		return
	}
	if project.AccountID != in.AccountID {
		writeError(w, http.StatusConflict, "account_project_mismatch", "Task account does not belong to the project.")
		return
	}
	if _, e := h.accounts.Get(r.Context(), in.AccountID); errors.Is(e, store.ErrAccountNotFound) || errors.Is(e, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "account_not_found", "Account was not found.")
		return
	} else if e != nil {
		writeError(w, http.StatusInternalServerError, "account_read_failed", "Account could not be read.")
		return
	}
	id := uuid.NewString()
	p := pid
	selection, e := resolveTaskModel(r.Context(), h.models, taskmodel.Selection{Model: in.Model, ReasoningEffort: in.ReasoningEffort})
	if e != nil {
		writeError(w, 400, "invalid_task_model", "Task model selection is invalid.")
		return
	}
	t := domain.CodexTask{ID: id, ProjectID: &p, AccountID: in.AccountID, Type: in.Type, SkillName: resolved.Skill, Action: action, Status: domain.TaskQueued, PromptSnapshot: in.Prompt, ModelName: selection.Model, ReasoningEffort: selection.ReasoningEffort, CreatedAt: prepareStartedAt}
	// chat_session_id is accepted for wire compatibility but is never routing
	// authority. CompositeScheduler resolves the console-owned project session.
	if h.preparer != nil {
		manifestRequest := in.TaskManifestRequest
		if action == domain.ActionTopicDeepen && strings.TrimSpace(manifestRequest.SessionID) == "" {
			_ = h.repo.DB().QueryRowContext(r.Context(), `SELECT id FROM idea_sessions WHERE project_id=? ORDER BY updated_at DESC LIMIT 1`, pid).Scan(&manifestRequest.SessionID)
		}
		prepared, e := prepareAndPublishTask(r.Context(), h.repo.DB(), h.preparer, t, manifestRequest, prepareStartedAt, h.scheduler.Enqueue, nil)
		if e != nil {
			var publishErr taskPublishError
			if errors.As(e, &publishErr) {
				writeError(w, 500, "task_enqueue_failed", e.Error())
				return
			}
			writeError(w, http.StatusConflict, "task_manifest_not_ready", e.Error())
			return
		}
		t = prepared
	} else {
		if e := h.scheduler.Enqueue(r.Context(), t); e != nil {
			writeError(w, 500, "task_enqueue_failed", e.Error())
			return
		}
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
