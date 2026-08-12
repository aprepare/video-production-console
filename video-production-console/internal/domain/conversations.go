package domain

import "time"

type ChatKind string

const (
	ChatGeneral ChatKind = "general"
	ChatIdea    ChatKind = "idea"
	ChatProject ChatKind = "project"
	ChatHistory ChatKind = "history"
)

type DeliveryMode string

const (
	DeliveryAuto  DeliveryMode = "auto"
	DeliverySteer DeliveryMode = "steer"
	DeliveryQueue DeliveryMode = "queue"
)

type ChatStatus string

const (
	ChatIdle          ChatStatus = "idle"
	ChatRunning       ChatStatus = "running"
	ChatAwaitingInput ChatStatus = "awaiting_input"
	ChatFailed        ChatStatus = "failed"
)

type ChatSession struct {
	ID               string
	Title            string
	Source           string
	Kind             ChatKind
	Status           ChatStatus
	ProjectID        *string
	IdeaSessionID    *string
	CodexThreadID    *string
	WorkingDirectory string
	Model            string
	ReasoningEffort  string
	SkillNames       []string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type ChatMessage struct {
	ID             string    `json:"id"`
	SessionID      string    `json:"session_id"`
	Role           string    `json:"role"`
	Kind           string    `json:"kind"`
	Content        string    `json:"content"`
	DeliveryStatus string    `json:"delivery_status"`
	ClientKey      string    `json:"client_key"`
	CodexItemID    *string   `json:"codex_item_id"`
	TurnID         *string   `json:"turn_id"`
	Sequence       int64     `json:"sequence"`
	CreatedAt      time.Time `json:"created_at"`
}

type ChatTurnStatus string

const (
	ChatTurnIdle          ChatTurnStatus = "idle"
	ChatTurnRunning       ChatTurnStatus = "running"
	ChatTurnAwaitingInput ChatTurnStatus = "awaiting_input"
	ChatTurnCompleted     ChatTurnStatus = "completed"
	ChatTurnFailed        ChatTurnStatus = "failed"
)

type ChatOutboxStatus string

const (
	ChatOutboxPending  ChatOutboxStatus = "pending"
	ChatOutboxSending  ChatOutboxStatus = "sending"
	ChatOutboxAccepted ChatOutboxStatus = "accepted"
	ChatOutboxQueued   ChatOutboxStatus = "queued"
	ChatOutboxFailed   ChatOutboxStatus = "failed"
)

type ChatTurn struct {
	ID             string
	SessionID      string
	CodexTurnID    *string
	Status         ChatTurnStatus
	Delivery       DeliveryMode
	InputMessageID *string
	ErrorCode      *string
	ErrorMessage   *string
	StartedAt      *time.Time
	FinishedAt     *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type ChatOutbox struct {
	ID             string
	SessionID      string
	MessageID      string
	ClientKey      string
	Content        string
	Delivery       DeliveryMode
	DeliveryStatus ChatOutboxStatus
	ExpectedTurnID *string
	CodexTurnID    *string
	Attempts       int
	AvailableAt    time.Time
	ClaimedAt      *time.Time
	LastError      *string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type ChatCompletionInboxStatus string

const (
	ChatCompletionPending    ChatCompletionInboxStatus = "pending"
	ChatCompletionProcessing ChatCompletionInboxStatus = "processing"
	ChatCompletionDone       ChatCompletionInboxStatus = "done"
)

type ChatCompletionInbox struct {
	ID          string
	SessionID   string
	CodexTurnID string
	Status      ChatCompletionInboxStatus
	Attempts    int
	AvailableAt time.Time
	ClaimedAt   *time.Time
	LastError   *string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	CompletedAt *time.Time
}

type SemanticEvent struct {
	ID        string
	SessionID *string
	TaskID    *string
	Sequence  int64
	Kind      string
	Phase     string
	Level     string
	Title     string
	Detail    string
	RawJSON   string
	CreatedAt time.Time
}

type ThreadLease struct {
	ThreadID  string
	SessionID string
	OwnerID   string
	ExpiresAt time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}
