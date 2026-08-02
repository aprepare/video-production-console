package codex

import (
	"bytes"
	"encoding/json"
	"fmt"
)

type Artifact struct {
	Type        string `json:"type"`
	Path        string `json:"path"`
	Description string `json:"description"`
}
type Question struct {
	Text    string   `json:"text"`
	Options []string `json:"options"`
}
type Result struct {
	Status                string     `json:"status"`
	Summary               string     `json:"summary"`
	Question              *Question  `json:"question,omitempty"`
	Artifacts             []Artifact `json:"artifacts"`
	TopicCardPath         *string    `json:"topic_card_path,omitempty"`
	NextRecommendedAction *string    `json:"next_recommended_action,omitempty"`
}
type Event struct {
	Kind        string
	Level       string
	DisplayText string
	SessionID   string
	RawJSON     json.RawMessage
	FinalResult *Result
}

func ParseLine(line []byte) (Event, error) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return Event{Kind: "parse_warning", Level: "warning", DisplayText: "empty JSONL line"}, nil
	}
	var envelope struct {
		Type     string `json:"type"`
		ThreadID string `json:"thread_id"`
		Item     struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"item"`
		Result *Result `json:"result"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil {
		return Event{Kind: "parse_warning", Level: "warning", DisplayText: fmt.Sprintf("invalid JSON: %v", err), RawJSON: json.RawMessage(append([]byte(nil), line...))}, nil
	}
	e := Event{Level: "info", RawJSON: json.RawMessage(append([]byte(nil), line...))}
	switch envelope.Type {
	case "thread.started":
		e.Kind, e.SessionID = "thread_started", envelope.ThreadID
	case "item.completed":
		if envelope.Item.Type == "agent_message" {
			e.Kind, e.DisplayText = "agent_message", envelope.Item.Text
		} else {
			e.Kind = "item_completed"
		}
	case "turn.completed":
		e.Kind, e.FinalResult = "turn_completed", envelope.Result
	default:
		e.Kind = "raw_event"
	}
	return e, nil
}
