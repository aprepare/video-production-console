package domain

import "time"

type IdeaSession struct {
	ID              string    `json:"id"`
	AccountID       *string   `json:"account_id,omitempty"`
	Title           string    `json:"title"`
	Status          string    `json:"status"`
	SelectedID      *string   `json:"selected_id,omitempty"`
	TopicCardPath   *string   `json:"topic_card_path,omitempty"`
	TopicCardState  *string   `json:"topic_card_state,omitempty"`
	TopicCardSHA256 *string   `json:"topic_card_sha256,omitempty"`
	ProjectID       *string   `json:"project_id,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type IdeaMessage struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id"`
	TaskID    *string   `json:"task_id,omitempty"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

type IdeaCandidate struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id"`
	TaskID    *string   `json:"task_id,omitempty"`
	Position  int       `json:"position"`
	Title     string    `json:"title"`
	Summary   string    `json:"summary"`
	Score     float64   `json:"score"`
	Source    string    `json:"source"`
	Selected  bool      `json:"selected"`
	CreatedAt time.Time `json:"created_at"`
}

type IdeaSessionDetail struct {
	Session    IdeaSession     `json:"session"`
	Messages   []IdeaMessage   `json:"messages"`
	Candidates []IdeaCandidate `json:"candidates"`
}
