package history

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"video-production-console/internal/domain"
)

type rpc interface {
	Call(context.Context, string, any, any) error
}
type AppServerSource struct{ rpc rpc }

func NewAppServerSource(r rpc) *AppServerSource { return &AppServerSource{rpc: r} }

func (s *AppServerSource) List(ctx context.Context, limit int) ([]ThreadSummary, error) {
	if s == nil || s.rpc == nil {
		return nil, errors.New("App Server history source is not configured")
	}
	var result struct {
		Threads []threadWire `json:"threads"`
		Data    []threadWire `json:"data"`
	}
	if err := s.rpc.Call(ctx, "thread/list", map[string]any{"limit": limit}, &result); err != nil {
		return nil, err
	}
	items := result.Threads
	if len(items) == 0 {
		items = result.Data
	}
	out := make([]ThreadSummary, 0, len(items))
	for _, item := range items {
		out = append(out, item.summary())
	}
	return out, nil
}
func (s *AppServerSource) Read(ctx context.Context, id string) (ThreadDetail, error) {
	if s == nil || s.rpc == nil {
		return ThreadDetail{}, errors.New("App Server history source is not configured")
	}
	var result struct {
		Thread   threadWire    `json:"thread"`
		Messages []messageWire `json:"messages"`
	}
	if err := s.rpc.Call(ctx, "thread/read", map[string]any{"threadId": id}, &result); err != nil {
		return ThreadDetail{}, err
	}
	detail := ThreadDetail{ThreadSummary: result.Thread.summary()}
	for _, item := range result.Messages {
		detail.Messages = append(detail.Messages, domain.ChatMessage{ID: item.ID, Role: item.Role, Kind: "message", Content: item.Content, CreatedAt: item.CreatedAt})
	}
	return detail, nil
}
func (s *AppServerSource) Resume(ctx context.Context, id string) error {
	if s == nil || s.rpc == nil {
		return errors.New("App Server history source is not configured")
	}
	return s.rpc.Call(ctx, "thread/resume", map[string]any{"threadId": id}, &struct{}{})
}
func (s *AppServerSource) Fork(ctx context.Context, id string) (string, error) {
	if s == nil || s.rpc == nil {
		return "", errors.New("App Server history source is not configured")
	}
	var result struct {
		ThreadID string `json:"threadId"`
		Thread   struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := s.rpc.Call(ctx, "thread/fork", map[string]any{"threadId": id}, &result); err != nil {
		return "", err
	}
	if result.ThreadID != "" {
		return result.ThreadID, nil
	}
	return result.Thread.ID, nil
}

type threadWire struct {
	ID              string          `json:"id"`
	Title           string          `json:"title"`
	Name            *string         `json:"name"`
	Preview         string          `json:"preview"`
	Source          json.RawMessage `json:"source"`
	ThreadSource    json.RawMessage `json:"threadSource"`
	Model           string          `json:"model"`
	ModelProvider   string          `json:"modelProvider"`
	ReasoningEffort string          `json:"reasoningEffort"`
	Archived        bool            `json:"archived"`
	Active          bool            `json:"active"`
	Status          json.RawMessage `json:"status"`
	UpdatedAt       json.RawMessage `json:"updatedAt"`
	RecencyAt       json.RawMessage `json:"recencyAt"`
}

func (w threadWire) summary() ThreadSummary {
	title := strings.TrimSpace(w.Title)
	if title == "" && w.Name != nil {
		title = strings.TrimSpace(*w.Name)
	}
	if title == "" {
		title = strings.TrimSpace(w.Preview)
	}
	source := decodeSource(w.Source)
	if source == "" {
		source = decodeSource(w.ThreadSource)
	}
	active := w.Active || decodeStatusActive(w.Status)
	recency := decodeTimestamp(w.UpdatedAt)
	if recency.IsZero() {
		recency = decodeTimestamp(w.RecencyAt)
	}
	return ThreadSummary{ID: strings.TrimSpace(w.ID), Title: title, Preview: w.Preview, Source: source, Model: firstNonEmpty(w.Model, w.ModelProvider), ReasoningEffort: w.ReasoningEffort, Archived: w.Archived, Active: active, Recency: recency}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func decodeSource(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return strings.TrimSpace(value)
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) == nil && len(object) > 0 {
		return "subagent"
	}
	return ""
}

func decodeStatusActive(raw json.RawMessage) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return false
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return strings.EqualFold(value, "active")
	}
	var object struct {
		Type string `json:"type"`
	}
	return json.Unmarshal(raw, &object) == nil && strings.EqualFold(object.Type, "active")
}

func decodeTimestamp(raw json.RawMessage) time.Time {
	if len(raw) == 0 || string(raw) == "null" {
		return time.Time{}
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil {
		seconds, err := number.Int64()
		if err == nil {
			return time.Unix(seconds, 0).UTC()
		}
		if decimal, decimalErr := strconv.ParseFloat(number.String(), 64); decimalErr == nil {
			whole := int64(decimal)
			nanos := int64((decimal - float64(whole)) * 1e9)
			return time.Unix(whole, nanos).UTC()
		}
	}
	var value time.Time
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	return time.Time{}
}

type messageWire struct {
	ID        string    `json:"id"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"createdAt"`
}
