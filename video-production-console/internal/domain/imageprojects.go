package domain

import "time"

type ImageProjectOutputMode string

const (
	ImageProjectOutputModeImageSlideshow ImageProjectOutputMode = "image_slideshow"
	ImageProjectOutputModeImageToVideo   ImageProjectOutputMode = "image_to_video"
)

type ImageProject struct {
	ID                   string                 `json:"id"`
	Title                string                 `json:"title"`
	Script               string                 `json:"script"`
	ImageCount           int                    `json:"image_count"`
	Ratio                string                 `json:"ratio"`
	Style                string                 `json:"style"`
	CustomStyle          string                 `json:"custom_style"`
	Concurrency          int                    `json:"concurrency"`
	Status               string                 `json:"status"`
	RunMode              string                 `json:"run_mode"`
	RunPhase             string                 `json:"run_phase"`
	RunStatus            string                 `json:"run_status"`
	PhaseError           string                 `json:"phase_error,omitempty"`
	PublishingError      string                 `json:"publishing_error,omitempty"`
	SuccessCount         int                    `json:"success_count"`
	FailureCount         int                    `json:"failure_count"`
	ImageAttempts        int                    `json:"image_attempts"`
	TextModel            string                 `json:"text_model,omitempty"`
	ReasoningEffort      string                 `json:"reasoning_effort,omitempty"`
	ImageModel           string                 `json:"image_model,omitempty"`
	OutputMode           ImageProjectOutputMode `json:"output_mode"`
	OutputModeLockedAt   *time.Time             `json:"output_mode_locked_at,omitempty"`
	AccountID            *string                `json:"account_id,omitempty"`
	TemplateVersion      string                 `json:"template_version,omitempty"`
	TemplateFingerprint  string                 `json:"template_fingerprint,omitempty"`
	CreatedAt            time.Time              `json:"created_at"`
	UpdatedAt            time.Time              `json:"updated_at"`
	PublishingCandidates []PublishingCandidate  `json:"publishing_candidates,omitempty"`
	SelectedPosition     *int                   `json:"selected_position,omitempty"`
}

type ImageProjectItem struct {
	ID           string    `json:"id"`
	ProjectID    string    `json:"project_id"`
	Sequence     int       `json:"sequence"`
	Role         string    `json:"role"`
	SourceText   string    `json:"source_text"`
	Title        string    `json:"title"`
	Prompt       string    `json:"prompt"`
	Status       string    `json:"status"`
	ImagePath    *string   `json:"-"`
	MIMEType     *string   `json:"mime_type,omitempty"`
	Width        int       `json:"width,omitempty"`
	Height       int       `json:"height,omitempty"`
	ErrorMessage *string   `json:"error_message,omitempty"`
	AttemptCount int       `json:"attempt_count"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type ImageProjectDetail struct {
	Project    ImageProject       `json:"project"`
	Items      []ImageProjectItem `json:"items"`
	Publishing PublishingState    `json:"publishing,omitempty"`
}

type PublishingCandidate struct {
	Position    int    `json:"position"`
	Title       string `json:"title"`
	Description string `json:"description"`
}
type PublishingState struct {
	Current *PublishingCandidate  `json:"current,omitempty"`
	All     []PublishingCandidate `json:"all,omitempty"`
}
