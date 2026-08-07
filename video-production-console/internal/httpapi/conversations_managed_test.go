package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"video-production-console/internal/conversation"
	"video-production-console/internal/domain"
	"video-production-console/internal/store"
)

type managedConversationStub struct {
	createCalls int
	deleteErr   error
}

func (*managedConversationStub) List(context.Context) ([]domain.ChatSession, error) { return nil, nil }
func (*managedConversationStub) Get(context.Context, string) (domain.ChatSession, []domain.ChatMessage, error) {
	return domain.ChatSession{}, nil, nil
}
func (*managedConversationStub) Events(context.Context, string, int64, int) ([]domain.SemanticEvent, error) {
	return nil, nil
}
func (s *managedConversationStub) Create(context.Context, conversation.CreateSessionInput) (domain.ChatSession, error) {
	s.createCalls++
	return domain.ChatSession{}, nil
}
func (s *managedConversationStub) Delete(context.Context, string) error { return s.deleteErr }
func (*managedConversationStub) Send(context.Context, conversation.SendInput) (conversation.SendReceipt, error) {
	return conversation.SendReceipt{}, nil
}

func TestConversationHTTPReturnsTypedDeleteConflict(t *testing.T) {
	service := &managedConversationStub{deleteErr: store.ErrConversationActive}
	request := httptest.NewRequest(http.MethodDelete, "/api/chat/sessions/00000000-0000-0000-0000-000000000001", nil)
	response := httptest.NewRecorder()
	NewConversationsHandler(service).ServeHTTP(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"code":"chat_active"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
func (*managedConversationStub) Fork(context.Context, string) (domain.ChatSession, error) {
	return domain.ChatSession{}, nil
}

func TestConversationHTTPRejectsDirectProjectMainCreation(t *testing.T) {
	service := &managedConversationStub{}
	handler := NewConversationsHandler(service)
	request := httptest.NewRequest(http.MethodPost, "/api/chat/sessions", strings.NewReader(`{"kind":"project","project_id":"00000000-0000-0000-0000-000000000001"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if service.createCalls != 0 {
		t.Fatalf("service Create calls=%d", service.createCalls)
	}
}
