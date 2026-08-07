package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/codex"
	"video-production-console/internal/domain"
	"video-production-console/internal/store"
	"video-production-console/internal/taskmodel"
)

type ideasHandler struct {
	repo      *store.IdeaRepository
	scheduler codex.Scheduler
	preparer  TaskManifestPreparer
	models    TaskModelResolver
}

func NewIdeasHandler(db *sql.DB, scheduler codex.Scheduler, preparer TaskManifestPreparer, models TaskModelResolver) http.Handler {
	h := &ideasHandler{repo: store.NewIdeaRepository(db), scheduler: scheduler, preparer: preparer, models: models}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ideas", h.list)
	mux.HandleFunc("POST /api/ideas", h.create)
	mux.HandleFunc("GET /api/ideas/{id}", h.get)
	mux.HandleFunc("DELETE /api/ideas/{id}", h.delete)
	mux.HandleFunc("POST /api/ideas/{id}/messages", h.message)
	mux.HandleFunc("POST /api/ideas/{id}/select", h.selectCandidate)
	return mux
}

func (h *ideasHandler) delete(w http.ResponseWriter, r *http.Request) {
	err := h.repo.DeleteSession(r.Context(), r.PathValue("id"))
	switch {
	case errors.Is(err, store.ErrIdeaSessionNotFound):
		writeError(w, http.StatusNotFound, "idea_not_found", "Idea session was not found.")
	case errors.Is(err, store.ErrIdeaSessionBusy):
		writeError(w, http.StatusConflict, "idea_active_task", "Stop the active task before deleting this conversation.")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "idea_delete_failed", "Idea session could not be deleted.")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *ideasHandler) list(w http.ResponseWriter, r *http.Request) {
	out, err := h.repo.ListSessions(r.Context())
	if err != nil {
		writeError(w, 500, "ideas_list_failed", "Idea sessions could not be listed.")
		return
	}
	writeJSON(w, 200, out)
}
func (h *ideasHandler) create(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AccountID string `json:"account_id"`
		Title     string `json:"title"`
	}
	if decodeJSON(r, &in) != nil {
		writeError(w, 400, "invalid_idea", "A valid idea session is required.")
		return
	}
	id := uuid.NewString()
	var account *string
	if strings.TrimSpace(in.AccountID) != "" {
		v, e := uuid.Parse(in.AccountID)
		if e != nil {
			writeError(w, 400, "invalid_account_id", "Account ID must be a UUID.")
			return
		}
		s := v.String()
		account = &s
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = "新选题规划"
	}
	now := time.Now().UTC()
	session := domain.IdeaSession{ID: id, AccountID: account, Title: title, Status: "planning", CreatedAt: now, UpdatedAt: now}
	if err := h.repo.CreateSession(r.Context(), session); err != nil {
		writeError(w, 500, "idea_create_failed", "Idea session could not be created.")
		return
	}
	writeJSON(w, 201, session)
}
func (h *ideasHandler) get(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s, e := h.repo.GetSession(r.Context(), id)
	if errors.Is(e, store.ErrIdeaSessionNotFound) {
		writeError(w, 404, "idea_not_found", "Idea session was not found.")
		return
	}
	if e != nil {
		writeError(w, 500, "idea_read_failed", "Idea session could not be read.")
		return
	}
	m, e := h.repo.Messages(r.Context(), id)
	if e != nil {
		writeError(w, 500, "idea_messages_failed", "Idea messages could not be read.")
		return
	}
	c, e := h.repo.Candidates(r.Context(), id)
	if e != nil {
		writeError(w, 500, "idea_candidates_failed", "Idea candidates could not be read.")
		return
	}
	c = hydrateIdeaCandidates(r.Context(), h.repo.DB(), c)
	writeJSON(w, 200, domain.IdeaSessionDetail{Session: s, Messages: m, Candidates: c})
}
func (h *ideasHandler) message(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s, e := h.repo.GetSession(r.Context(), id)
	if errors.Is(e, store.ErrIdeaSessionNotFound) {
		writeError(w, 404, "idea_not_found", "Idea session was not found.")
		return
	}
	if e != nil {
		writeError(w, 500, "idea_read_failed", "Idea session could not be read.")
		return
	}
	var in struct {
		Content   string `json:"content"`
		AccountID string `json:"account_id"`
		taskModelRequest
	}
	if decodeJSON(r, &in) != nil || strings.TrimSpace(in.Content) == "" {
		writeError(w, 400, "message_required", "Message content is required.")
		return
	}
	now := time.Now().UTC()
	msg := domain.IdeaMessage{ID: uuid.NewString(), SessionID: id, Role: "user", Content: strings.TrimSpace(in.Content), CreatedAt: now}
	account := s.AccountID
	// An explicit account in the confirmation request is authoritative. The
	// planner session may have been opened under a different sidebar filter.
	if strings.TrimSpace(in.AccountID) != "" {
		v, pe := uuid.Parse(in.AccountID)
		if pe != nil {
			writeError(w, 400, "invalid_account_id", "Account ID must be a UUID.")
			return
		}
		x := v.String()
		account = &x
		if e := h.repo.UpdateAccount(r.Context(), id, x); e != nil {
			writeError(w, 409, "idea_account_update_failed", "The selected account could not be applied to this conversation.")
			return
		}
	}
	if h.scheduler == nil || account == nil {
		if e := h.repo.AddMessage(r.Context(), msg); e != nil {
			writeError(w, 500, "idea_message_failed", "Idea message could not be saved.")
			return
		}
		writeJSON(w, 202, msg)
		return
	}
	selection, e := resolveTaskModel(r.Context(), h.models, taskmodel.Selection{Model: in.Model, ReasoningEffort: in.ReasoningEffort})
	if e != nil {
		writeError(w, 400, "invalid_task_model", "Task model selection is invalid.")
		return
	}
	taskID := uuid.NewString()
	task := domain.CodexTask{ID: taskID, AccountID: *account, Type: "topic_select", SkillName: "finance-topic-selector", Action: domain.ActionTopicBrainstorm, Status: domain.TaskQueued, PromptSnapshot: msg.Content, ModelName: selection.Model, ReasoningEffort: selection.ReasoningEffort, CreatedAt: now}
	if h.preparer != nil {
		if e := h.preparer.Prepare(r.Context(), task, TaskManifestRequest{SessionID: id}); e != nil {
			writeError(w, http.StatusConflict, "task_manifest_not_ready", e.Error())
			return
		}
	}
	// Enqueue persists codex_tasks synchronously before signalling the worker.
	// The message references that task through a foreign key, so its parent
	// must exist before the conversation entry is inserted.
	if e := h.scheduler.Enqueue(r.Context(), task); e != nil {
		writeError(w, 500, "idea_task_failed", "Idea planning task could not be queued.")
		return
	}
	taskMsg := msg
	taskMsg.TaskID = &taskID
	if e := h.repo.AddMessage(r.Context(), taskMsg); e != nil {
		writeError(w, 500, "idea_message_failed", "Idea message could not be saved.")
		return
	}
	writeJSON(w, 202, map[string]any{"message": taskMsg, "task_id": taskID})
}
func (h *ideasHandler) selectCandidate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		CandidateID string `json:"candidate_id"`
		Title       string `json:"title"`
		AccountID   string `json:"account_id"`
	}
	if decodeJSON(r, &in) != nil || strings.TrimSpace(in.CandidateID) == "" {
		writeError(w, 400, "candidate_required", "Candidate ID is required.")
		return
	}
	c, e := h.repo.SelectCandidate(r.Context(), id, in.CandidateID)
	if errors.Is(e, store.ErrIdeaCandidateNotFound) {
		writeError(w, 404, "candidate_not_found", "Candidate was not found.")
		return
	}
	if e != nil {
		writeError(w, 500, "candidate_select_failed", "Candidate could not be selected.")
		return
	}
	s, e := h.repo.GetSession(r.Context(), id)
	if e != nil {
		writeError(w, 500, "idea_read_failed", "Idea session could not be read.")
		return
	}
	account := s.AccountID
	if strings.TrimSpace(in.AccountID) != "" {
		v, pe := uuid.Parse(in.AccountID)
		if pe != nil {
			writeError(w, 400, "invalid_account_id", "Account ID must be a UUID.")
			return
		}
		x := v.String()
		account = &x
	}
	if account == nil {
		writeError(w, 409, "account_required", "Select an account before creating a project.")
		return
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = c.Title
	}
	pid := uuid.NewString()
	now := time.Now().UTC()
	p := domain.Project{ID: pid, AccountID: *account, Title: title, Stage: domain.StageScript, Status: domain.ProjectDraft, CreatedAt: now, UpdatedAt: now}
	projectRepo := store.NewProjectRepository(h.repo.DB())
	if e := projectRepo.CreateProject(r.Context(), p); errors.Is(e, store.ErrAccountInactive) {
		writeError(w, http.StatusConflict, "account_inactive", "The selected account is not active.")
		return
	} else if e != nil {
		writeError(w, 409, "project_create_failed", "Project could not be created from this candidate.")
		return
	}
	_ = h.repo.LinkProject(r.Context(), id, pid, *account)
	selection := projectTopicSelection{SessionID: id, Candidate: c}
	if c.TaskID != nil {
		selection.TopicCandidatesPath, _ = topicCandidatesArtifactPath(r.Context(), h.repo.DB(), *c.TaskID)
	}
	commitTask, commitErr := enqueueTopicCommit(r.Context(), h.repo.DB(), h.scheduler, h.preparer, h.models, p, selection)
	result := map[string]any{"project": p, "candidate": hydrateIdeaCandidates(r.Context(), h.repo.DB(), []domain.IdeaCandidate{c})[0]}
	if commitErr != nil {
		result["topic_card_task_error"] = commitErr.Error()
	} else {
		result["topic_card_task"] = viewTask(commitTask)
	}
	writeJSON(w, 201, result)
}
