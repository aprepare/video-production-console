package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"video-production-console/internal/domain"
	"video-production-console/internal/history"
)

type historyStub struct{ detail history.ThreadDetail }

func (historyStub) List(context.Context, int, string) ([]history.ThreadSummary, error) {
	return nil, errors.New("not used")
}
func (s historyStub) Read(context.Context, string) (history.ThreadDetail, error) {
	return s.detail, nil
}
func (historyStub) Resume(context.Context, string) (domain.ChatSession, error) {
	return domain.ChatSession{}, errors.New("not used")
}
func (historyStub) Fork(context.Context, string) (domain.ChatSession, error) {
	return domain.ChatSession{}, errors.New("not used")
}

func TestHistoryDetailReturnsSnakeCaseMessages(t *testing.T) {
	created := time.Date(2026, 8, 12, 7, 30, 0, 0, time.UTC)
	turn := "turn-1"
	detail := history.ThreadDetail{
		ThreadSummary: history.ThreadSummary{ID: "thread-1", Title: "T", Source: "cli", Recency: created},
		Messages:      []domain.ChatMessage{{ID: "message-1", SessionID: "session-1", Role: "assistant", Kind: "message", Content: "hello", DeliveryStatus: "delivered", ClientKey: "key", TurnID: &turn, Sequence: 3, CreatedAt: created}},
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/codex/history/thread-1", nil)
	NewHistoryHandler(historyStub{detail: detail}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	assertSnakeCaseKeys(t, "/api/codex/history/thread-1", recorder.Body.Bytes())
	var body struct {
		Messages []struct {
			ID             string  `json:"id"`
			SessionID      string  `json:"session_id"`
			DeliveryStatus string  `json:"delivery_status"`
			ClientKey      string  `json:"client_key"`
			TurnID         *string `json:"turn_id"`
			Sequence       int64   `json:"sequence"`
			CreatedAt      string  `json:"created_at"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Messages) != 1 {
		t.Fatalf("messages=%s", recorder.Body.String())
	}
	message := body.Messages[0]
	if message.ID != "message-1" || message.SessionID != "session-1" || message.DeliveryStatus != "delivered" || message.ClientKey != "key" || message.TurnID == nil || *message.TurnID != turn || message.Sequence != 3 || message.CreatedAt == "" {
		t.Fatalf("message=%+v raw=%s", message, recorder.Body.String())
	}
}
