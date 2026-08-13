package domain

import "time"

type ImageProject struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Script      string    `json:"script"`
	ImageCount  int       `json:"image_count"`
	Ratio       string    `json:"ratio"`
	Style       string    `json:"style"`
	CustomStyle string    `json:"custom_style"`
	Concurrency int       `json:"concurrency"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
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
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type ImageProjectDetail struct {
	Project ImageProject       `json:"project"`
	Items   []ImageProjectItem `json:"items"`
}
