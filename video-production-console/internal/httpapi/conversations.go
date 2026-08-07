package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/conversation"
	"video-production-console/internal/domain"
	"video-production-console/internal/store"
)

type conversationAPI interface {
	List(context.Context) ([]domain.ChatSession, error)
	Get(context.Context, string) (domain.ChatSession, []domain.ChatMessage, error)
	Events(context.Context, string, int64, int) ([]domain.SemanticEvent, error)
	Create(context.Context, conversation.CreateSessionInput) (domain.ChatSession, error)
	Delete(context.Context, string) error
	Send(context.Context, conversation.SendInput) (conversation.SendReceipt, error)
	Fork(context.Context, string) (domain.ChatSession, error)
}

type conversationsHandler struct{ service conversationAPI }

func NewConversationsHandler(service conversationAPI) http.Handler {
	h := &conversationsHandler{service: service}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/chat/sessions", h.list)
	mux.HandleFunc("POST /api/chat/sessions", h.create)
	mux.HandleFunc("GET /api/chat/sessions/{id}", h.get)
	mux.HandleFunc("GET /api/chat/sessions/{id}/events", h.events)
	mux.HandleFunc("DELETE /api/chat/sessions/{id}", h.delete)
	mux.HandleFunc("POST /api/chat/sessions/{id}/messages", h.message)
	mux.HandleFunc("POST /api/chat/sessions/{id}/fork", h.fork)
	return mux
}

func (h *conversationsHandler) events(w http.ResponseWriter, r *http.Request) {
	id, ok := conversationID(w, r)
	if !ok {
		return
	}
	after, err := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	if r.URL.Query().Get("after") == "" {
		after, err = 0, nil
	}
	if err != nil || after < 0 {
		writeError(w, 400, "invalid_event_cursor", "Event cursor must be a non-negative integer.")
		return
	}
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if r.URL.Query().Get("limit") == "" {
		limit, err = 100, nil
	}
	if err != nil || limit < 1 {
		writeError(w, 400, "invalid_event_limit", "Event limit must be a positive integer.")
		return
	}
	if limit > 100 {
		limit = 100
	}
	events, err := h.service.Events(r.Context(), id, after, limit)
	if err != nil {
		writeConversationError(w, err, "chat_events_failed", "Conversation events could not be read.")
		return
	}
	view := make([]semanticEventView, 0, len(events))
	for _, event := range events {
		view = append(view, semanticEventView{ID: event.ID, SessionID: event.SessionID, Sequence: event.Sequence, Kind: event.Kind, Phase: event.Phase, Level: event.Level, Title: event.Title, Detail: event.Detail, CreatedAt: event.CreatedAt.Format(time.RFC3339)})
	}
	writeJSON(w, 200, view)
}

type semanticEventView struct {
	ID        string  `json:"id"`
	SessionID *string `json:"session_id,omitempty"`
	Sequence  int64   `json:"sequence"`
	Kind      string  `json:"kind"`
	Phase     string  `json:"phase"`
	Level     string  `json:"level"`
	Title     string  `json:"title"`
	Detail    string  `json:"detail"`
	CreatedAt string  `json:"created_at"`
}

func (h *conversationsHandler) list(w http.ResponseWriter, r *http.Request) {
	sessions, err := h.service.List(r.Context())
	if err != nil {
		writeError(w, 500, "chat_list_failed", "Conversations could not be read.")
		return
	}
	view := make([]chatSessionView, 0, len(sessions))
	for _, session := range sessions {
		view = append(view, toChatSessionView(session))
	}
	writeJSON(w, 200, view)
}

func (h *conversationsHandler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := conversationID(w, r)
	if !ok {
		return
	}
	session, messages, err := h.service.Get(r.Context(), id)
	if err != nil {
		writeConversationError(w, err, "chat_read_failed", "Conversation could not be read.")
		return
	}
	view := make([]chatMessageView, 0, len(messages))
	for _, message := range messages {
		view = append(view, toChatMessageView(message))
	}
	writeJSON(w, 200, map[string]any{"session": toChatSessionView(session), "messages": view})
}

func (h *conversationsHandler) create(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind             domain.ChatKind `json:"kind"`
		Title            string          `json:"title"`
		WorkingDirectory string          `json:"working_directory"`
		Model            string          `json:"model"`
		ReasoningEffort  string          `json:"reasoning_effort"`
		Source           string          `json:"source"`
		SkillNames       []string        `json:"skill_names"`
		ProjectID        *string         `json:"project_id"`
		IdeaSessionID    *string         `json:"idea_session_id"`
	}
	if decodeJSON(r, &in) != nil {
		writeError(w, 400, "invalid_chat_session", "A valid conversation is required.")
		return
	}
	if !optionalUUID(in.ProjectID) || !optionalUUID(in.IdeaSessionID) {
		writeError(w, 400, "invalid_chat_reference", "Project and idea references must be UUIDs.")
		return
	}
	if in.Kind == domain.ChatProject {
		writeError(w, http.StatusConflict, "project_chat_managed", "Project conversations are created automatically when the first project task starts.")
		return
	}
	session, err := h.service.Create(r.Context(), conversation.CreateSessionInput{Kind: in.Kind, Title: in.Title, WorkingDirectory: in.WorkingDirectory, Model: in.Model, ReasoningEffort: in.ReasoningEffort, Source: in.Source, SkillNames: in.SkillNames, ProjectID: in.ProjectID, IdeaSessionID: in.IdeaSessionID})
	if err != nil {
		writeConversationError(w, err, "chat_create_failed", "Conversation could not be created.")
		return
	}
	writeJSON(w, http.StatusCreated, toChatSessionView(session))
}

func (h *conversationsHandler) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := conversationID(w, r)
	if !ok {
		return
	}
	if err := h.service.Delete(r.Context(), id); err != nil {
		writeConversationError(w, err, "chat_delete_failed", "Conversation could not be deleted.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *conversationsHandler) message(w http.ResponseWriter, r *http.Request) {
	id, ok := conversationID(w, r)
	if !ok {
		return
	}
	var in struct {
		Text      string              `json:"text"`
		Delivery  domain.DeliveryMode `json:"delivery"`
		ClientKey string              `json:"client_key"`
	}
	if decodeJSON(r, &in) != nil || strings.TrimSpace(in.Text) == "" || len([]byte(in.Text)) > 64<<10 || strings.TrimSpace(in.ClientKey) == "" {
		writeError(w, 400, "invalid_chat_message", "Message text and client key are required.")
		return
	}
	receipt, err := h.service.Send(r.Context(), conversation.SendInput{SessionID: id, ClientKey: in.ClientKey, Text: in.Text, Delivery: in.Delivery})
	if err != nil {
		writeConversationError(w, err, "chat_delivery_failed", "Message could not be delivered.")
		return
	}
	writeJSON(w, http.StatusAccepted, receipt)
}

func (h *conversationsHandler) fork(w http.ResponseWriter, r *http.Request) {
	id, ok := conversationID(w, r)
	if !ok {
		return
	}
	session, err := h.service.Fork(r.Context(), id)
	if err != nil {
		writeConversationError(w, err, "chat_fork_failed", "Conversation could not be forked.")
		return
	}
	writeJSON(w, http.StatusCreated, toChatSessionView(session))
}

type chatSessionView struct {
	ID              string            `json:"id"`
	Title           string            `json:"title"`
	Kind            domain.ChatKind   `json:"kind"`
	Status          domain.ChatStatus `json:"status"`
	ProjectID       *string           `json:"project_id,omitempty"`
	IdeaSessionID   *string           `json:"idea_session_id,omitempty"`
	Model           string            `json:"model,omitempty"`
	ReasoningEffort string            `json:"reasoning_effort,omitempty"`
	Source          string            `json:"source"`
	SkillNames      []string          `json:"skill_names"`
	CreatedAt       string            `json:"created_at"`
	UpdatedAt       string            `json:"updated_at"`
}

type chatMessageView struct {
	ID             string  `json:"id"`
	Role           string  `json:"role"`
	Kind           string  `json:"kind"`
	Content        string  `json:"content"`
	DeliveryStatus string  `json:"delivery_status"`
	TurnID         *string `json:"turn_id,omitempty"`
	CreatedAt      string  `json:"created_at"`
}

func toChatSessionView(session domain.ChatSession) chatSessionView {
	return chatSessionView{ID: session.ID, Title: session.Title, Kind: session.Kind, Status: session.Status, Source: session.Source, ProjectID: session.ProjectID, IdeaSessionID: session.IdeaSessionID, Model: session.Model, ReasoningEffort: session.ReasoningEffort, SkillNames: session.SkillNames, CreatedAt: session.CreatedAt.Format(time.RFC3339), UpdatedAt: session.UpdatedAt.Format(time.RFC3339)}
}

func toChatMessageView(message domain.ChatMessage) chatMessageView {
	return chatMessageView{ID: message.ID, Role: message.Role, Kind: message.Kind, Content: message.Content, DeliveryStatus: message.DeliveryStatus, TurnID: message.TurnID, CreatedAt: message.CreatedAt.Format(time.RFC3339)}
}

func conversationID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		writeError(w, 400, "invalid_chat_id", "Conversation ID must be a UUID.")
		return "", false
	}
	return id, true
}
func optionalUUID(id *string) bool {
	return id == nil || *id == "" || func() bool { _, err := uuid.Parse(*id); return err == nil }()
}
func writeConversationError(w http.ResponseWriter, err error, fallbackCode, fallbackMessage string) {
	if errors.Is(err, context.Canceled) {
		writeError(w, 409, "chat_request_cancelled", "Conversation request was cancelled.")
		return
	}
	if errors.Is(err, store.ErrConversationProtected) {
		writeError(w, http.StatusConflict, "project_chat_protected", "The project main conversation cannot be deleted.")
		return
	}
	if errors.Is(err, store.ErrConversationActive) {
		writeError(w, http.StatusConflict, "chat_active", "The conversation has active work and cannot be deleted.")
		return
	}
	if strings.Contains(err.Error(), "no rows") || strings.Contains(err.Error(), "not found") {
		writeError(w, 404, "chat_not_found", "Conversation was not found.")
		return
	}
	if strings.Contains(err.Error(), "outside") || strings.Contains(err.Error(), "not registered") || strings.Contains(err.Error(), "unsupported") || strings.Contains(err.Error(), "required") || strings.Contains(err.Error(), "invalid") {
		writeError(w, 400, "invalid_chat_session", "Conversation settings are invalid.")
		return
	}
	if strings.Contains(err.Error(), "conversation service is not configured") {
		writeError(w, http.StatusServiceUnavailable, "chat_service_unavailable", "The real-time Codex conversation service is not available in this console session.")
		return
	}
	if strings.Contains(err.Error(), "start Codex thread:") {
		writeError(w, http.StatusBadGateway, "chat_thread_start_failed", "Codex could not start a new conversation. Check the Codex connection and try again.")
		return
	}
	writeError(w, 500, fallbackCode, fallbackMessage)
}
