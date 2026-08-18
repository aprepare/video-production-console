package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"net/http"
	"os"
	"strings"
	"time"
	"video-production-console/internal/codex"
	"video-production-console/internal/domain"
	"video-production-console/internal/store"
	"video-production-console/internal/taskmodel"
)

type taskAPI struct {
	repo      *store.TaskRepository
	timings   *store.TaskTimingRepository
	projects  *store.ProjectRepository
	accounts  *store.AccountRepository
	scheduler codex.Scheduler
	preparer  TaskManifestPreparer
	models    TaskModelResolver
}

func NewTasksHandler(db *sql.DB, s codex.Scheduler, preparer TaskManifestPreparer, models TaskModelResolver) http.Handler {
	h := &taskAPI{repo: store.NewTaskRepository(db), timings: store.NewTaskTimingRepository(db), projects: store.NewProjectRepository(db), accounts: store.NewAccountRepository(db), scheduler: s, preparer: preparer, models: models}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/projects/{id}/tasks", h.create)
	mux.HandleFunc("POST /api/projects/{id}/topic-card", h.commitTopicCard)
	mux.HandleFunc("GET /api/tasks", h.list)
	mux.HandleFunc("GET /api/tasks/{id}/timing/summary", h.timingSummary)
	mux.HandleFunc("GET /api/tasks/{id}/timing/runs", h.timingRuns)
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
	ID              string                    `json:"id"`
	ProjectID       *string                   `json:"project_id,omitempty"`
	AccountID       string                    `json:"account_id"`
	Type            string                    `json:"type"`
	SkillName       string                    `json:"skill_name"`
	Action          domain.TaskAction         `json:"action"`
	Status          domain.TaskStatus         `json:"status"`
	CodexSessionID  *string                   `json:"codex_session_id,omitempty"`
	ModelName       string                    `json:"model"`
	ReasoningEffort string                    `json:"reasoning_effort"`
	ResultSummary   *string                   `json:"result_summary,omitempty"`
	ErrorCode       *string                   `json:"error_code,omitempty"`
	ErrorMessage    *string                   `json:"error_message,omitempty"`
	CreatedAt       time.Time                 `json:"created_at"`
	StartedAt       *time.Time                `json:"started_at,omitempty"`
	FinishedAt      *time.Time                `json:"finished_at,omitempty"`
	Events          []domain.TaskEvent        `json:"events,omitempty"`
	Messages        []domain.TaskMessage      `json:"messages,omitempty"`
	TimingSummary   *domain.TaskTimingSummary `json:"timing_summary,omitempty"`
	TimingRuns      []domain.TaskPhaseRun     `json:"timing_runs,omitempty"`
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
	if err := decodeJSON(w, r, maxTaskJSONRequest, &in); err != nil {
		writeDecodeError(w, err, "invalid_task", "Task type and prompt are required.")
		return
	}
	if strings.TrimSpace(in.Type) == "" || strings.TrimSpace(in.Prompt) == "" {
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
	if action == domain.ActionRemixStandard {
		active, activeErr := h.repo.ActiveByProjectAction(r.Context(), pid, action)
		if activeErr == nil {
			requestedSource := strings.TrimSpace(in.SourceVersionID)
			if requestedSource == "" {
				writeJSON(w, http.StatusOK, viewTask(active))
				return
			}
			activeSource, sourceErr := h.activeTaskSourceVersion(r.Context(), active.ID)
			if sourceErr == nil && activeSource == requestedSource {
				writeJSON(w, http.StatusOK, viewTask(active))
				return
			}
			writeError(w, http.StatusConflict, "active_remix_conflict", "A remix task is already active with a different source version; wait for it to finish before starting another remix.")
			return
		}
		if !errors.Is(activeErr, sql.ErrNoRows) {
			writeError(w, http.StatusInternalServerError, "active_task_read_failed", "Active remix task could not be read.")
			return
		}
	}
	id := uuid.NewString()
	p := pid
	selection, e := resolveTaskModel(r.Context(), h.models, taskmodel.Selection{Model: in.Model, ReasoningEffort: in.ReasoningEffort, Kind: modelKindForAction(action)})
	if e != nil {
		writeError(w, 400, "invalid_task_model", "Task model selection is invalid.")
		return
	}
	manifestRequest := in.TaskManifestRequest
	if remixActionOmitsGrok(action) {
		style, err := normalizeRemixPromptStyle(manifestRequest.RemixPromptStyle)
		if err != nil {
			writeError(w, 400, "invalid_remix_prompt_style", "remix_prompt_style must be rewrite or wash.")
			return
		}
		manifestRequest.RemixPromptStyle = style
	}
	if action == domain.ActionRemixReview {
		notes := strings.TrimSpace(manifestRequest.RevisionNotes)
		if notes == "" {
			notes = strings.TrimSpace(in.Prompt)
		}
		if notes == "" {
			writeError(w, 400, "revision_notes_required", "Revision notes are required for remix.review.")
			return
		}
		manifestRequest.RevisionNotes = notes
		if _, err := store.NewProjectStepNotesRepository(h.repo.DB()).Upsert(r.Context(), pid, "remix", notes, prepareStartedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "step_notes_save_failed", "Project revision notes could not be saved.")
			return
		}
	}
	t := domain.CodexTask{ID: id, ProjectID: &p, AccountID: in.AccountID, Type: in.Type, SkillName: resolved.Skill, Action: action, Status: domain.TaskQueued, PromptSnapshot: in.Prompt, ModelName: selection.Model, ReasoningEffort: selection.ReasoningEffort, CreatedAt: prepareStartedAt}
	// chat_session_id is accepted for wire compatibility but is never routing
	// authority. CompositeScheduler resolves the console-owned project session.
	if h.preparer != nil {
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

func (h *taskAPI) activeTaskSourceVersion(ctx context.Context, taskID string) (string, error) {
	_, manifestPath, err := h.repo.PreparedManifest(ctx, taskID)
	if err != nil {
		return "", fmt.Errorf("read active task manifest: %w", err)
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", fmt.Errorf("read active task manifest: %w", err)
	}
	var manifest codex.TaskManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "", fmt.Errorf("parse active task manifest: %w", err)
	}
	for _, input := range manifest.Inputs {
		if input.Type == domain.AssetSourceScript && strings.TrimSpace(input.VersionID) != "" {
			return input.VersionID, nil
		}
	}
	return "", errors.New("active task manifest has no source script input")
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
	if h.timings != nil {
		if summary, timingErr := h.timings.SummaryForTask(r.Context(), t.ID, time.Now().UTC()); timingErr == nil {
			v.TimingSummary = &summary
			v.TimingRuns = summary.Phases
		}
	}
	writeJSON(w, 200, v)
}

func (h *taskAPI) timingSummary(w http.ResponseWriter, r *http.Request) {
	if _, err := h.repo.Get(r.Context(), r.PathValue("id")); errors.Is(err, sql.ErrNoRows) {
		writeError(w, 404, "task_not_found", "Task was not found.")
		return
	} else if err != nil {
		writeError(w, 500, "task_read_failed", err.Error())
		return
	}
	summary, err := h.timings.SummaryForTask(r.Context(), r.PathValue("id"), time.Now().UTC())
	if err != nil {
		writeError(w, 500, "task_timing_read_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func (h *taskAPI) timingRuns(w http.ResponseWriter, r *http.Request) {
	if _, err := h.repo.Get(r.Context(), r.PathValue("id")); errors.Is(err, sql.ErrNoRows) {
		writeError(w, 404, "task_not_found", "Task was not found.")
		return
	} else if err != nil {
		writeError(w, 500, "task_read_failed", err.Error())
		return
	}
	runs, err := h.timings.ForTask(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, 500, "task_timing_read_failed", err.Error())
		return
	}
	if runs == nil {
		runs = []domain.TaskPhaseRun{}
	}
	writeJSON(w, http.StatusOK, runs)
}
func (h *taskAPI) answer(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Answer string `json:"answer"`
	}
	if err := decodeJSON(w, r, maxMessageJSONRequest, &in); err != nil {
		writeDecodeError(w, err, "answer_required", "Answer is required.")
		return
	}
	if strings.TrimSpace(in.Answer) == "" {
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
