package domain

import "time"

// ImageGeneration is the public image-studio history record.
type ImageGeneration struct {
	ID          string                  `json:"id"`
	Prompt      string                  `json:"prompt"`
	FinalPrompt string                  `json:"final_prompt"`
	Enhance     bool                    `json:"enhance"`
	Style       string                  `json:"style"`
	CustomStyle string                  `json:"custom_style"`
	Model       string                  `json:"model"`
	Ratio       string                  `json:"ratio"`
	Count       int                     `json:"count"`
	Status      string                  `json:"status"`
	Error       string                  `json:"error"`
	Favorite    bool                    `json:"favorite"`
	CreatedAt   time.Time               `json:"created_at"`
	UpdatedAt   time.Time               `json:"updated_at"`
	Outputs     []ImageGenerationOutput `json:"outputs"`
}

type ImageGenerationOutput struct {
	ID           string    `json:"id"`
	Position     int       `json:"position"`
	Status       string    `json:"status"`
	MIMEType     string    `json:"mime_type"`
	Width        int       `json:"width"`
	Height       int       `json:"height"`
	Bytes        int64     `json:"bytes"`
	Error        string    `json:"error"`
	URL          string    `json:"url"`
	GenerationID string    `json:"-"`
	Path         string    `json:"-"`
	CreatedAt    time.Time `json:"-"`
	UpdatedAt    time.Time `json:"-"`
}
