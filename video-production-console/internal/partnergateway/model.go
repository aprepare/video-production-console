package partnergateway

import "time"

type PartnerStatus string

const (
	PartnerActive   PartnerStatus = "active"
	PartnerDisabled PartnerStatus = "disabled"
)

type Partner struct {
	ID, DisplayName, KeyPrefix                         string
	KeyHash                                            []byte
	Status                                             PartnerStatus
	DeviceHash                                         string
	DeviceSecretHash                                   []byte
	SessionVersion                                     int64
	TextCalls, ImageCalls, VerifyFailures, RateLimited int64
	CreatedAt, UpdatedAt, LastVerifiedAt               time.Time
}

type Session struct {
	TokenHash            []byte
	PartnerID            string
	SessionVersion       int64
	ExpiresAt, CreatedAt time.Time
}

type Capabilities struct {
	Features         []string `json:"features"`
	TextModels       []string `json:"text_models"`
	ReasoningEfforts []string `json:"reasoning_efforts"`
	ImageModel       string   `json:"image_model"`
}

type AuraRuntime struct {
	BaseURL string  `json:"base_url"`
	APIKey  string  `json:"api_key"`
	Model   string  `json:"model"`
	VoiceID string  `json:"voice_id"`
	Speed   float64 `json:"speed"`
	Volume  float64 `json:"volume"`
}
