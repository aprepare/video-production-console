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
)

type ideasHandler struct {
	repo      *store.IdeaRepository
	scheduler codex.Scheduler
}

func NewIdeasHandler(db *sql.DB, scheduler codex.Scheduler) http.Handler {
	h := &ideasHandler{repo: store.NewIdeaRepository(db), scheduler: scheduler}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ideas", h.list)
	mux.HandleFunc("POST /api/ideas", h.create)
	mux.HandleFunc("GET /api/ideas/{id}", h.get)
	mux.HandleFunc("POST /api/ideas/{id}/messages", h.message)
	mux.HandleFunc("POST /api/ideas/{id}/select", h.selectCandidate)
	return mux
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
	}
	if decodeJSON(r, &in) != nil || strings.TrimSpace(in.Content) == "" {
		writeError(w, 400, "message_required", "Message content is required.")
		return
	}
	now := time.Now().UTC()
	msg := domain.IdeaMessage{ID: uuid.NewString(), SessionID: id, Role: "user", Content: strings.TrimSpace(in.Content), CreatedAt: now}
	account := s.AccountID
	if account == nil && strings.TrimSpace(in.AccountID) != "" {
		v, pe := uuid.Parse(in.AccountID)
		if pe != nil {
			writeError(w, 400, "invalid_account_id", "Account ID must be a UUID.")
			return
		}
		x := v.String()
		account = &x
	}
	if h.scheduler == nil || account == nil {
		if e := h.repo.AddMessage(r.Context(), msg); e != nil {
			writeError(w, 500, "idea_message_failed", "Idea message could not be saved.")
			return
		}
		writeJSON(w, 202, msg)
		return
	}
	taskID := uuid.NewString()
	task := domain.CodexTask{ID: taskID, AccountID: *account, Type: "topic_select", SkillName: "finance-topic-selector", Action: domain.ActionTopicBrainstorm, Status: domain.TaskQueued, PromptSnapshot: msg.Content, CreatedAt: now}
	taskMsg := msg
	taskMsg.TaskID = &taskID
	if e := h.repo.AddMessage(r.Context(), taskMsg); e != nil {
		writeError(w, 500, "idea_message_failed", "Idea message could not be saved.")
		return
	}
	if e := h.scheduler.Enqueue(r.Context(), task); e != nil {
		writeError(w, 500, "idea_task_failed", "Idea planning task could not be queued.")
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
	if account == nil && strings.TrimSpace(in.AccountID) != "" {
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
	p := domain.Project{ID: pid, AccountID: *account, Title: title, Stage: domain.StageTopic, CreatedAt: now, UpdatedAt: now}
	if e := store.NewProjectRepository(h.repo.DB()).CreateProject(r.Context(), p); e != nil {
		writeError(w, 409, "project_create_failed", "Project could not be created from this candidate.")
		return
	}
	writeJSON(w, 201, map[string]any{"project": p, "candidate": c})
}
