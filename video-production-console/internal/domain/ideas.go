package domain

import "time"

type IdeaSession struct {
	ID              string
	AccountID       *string
	Title           string
	Status          string
	SelectedID      *string
	TopicCardPath   *string
	TopicCardState  *string
	TopicCardSHA256 *string
	ProjectID       *string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type IdeaMessage struct {
	ID        string
	SessionID string
	TaskID    *string
	Role      string
	Content   string
	CreatedAt time.Time
}

type IdeaCandidate struct {
	ID        string
	SessionID string
	TaskID    *string
	Position  int
	Title     string
	Summary   string
	Score     float64
	Source    string
	Selected  bool
	CreatedAt time.Time
}

type IdeaSessionDetail struct {
	Session    IdeaSession
	Messages   []IdeaMessage
	Candidates []IdeaCandidate
}
