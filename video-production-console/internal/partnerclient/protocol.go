package partnerclient

import "time"

type ActivateRequest struct {
	ActivationKey string `json:"activation_key"`
	DeviceHash    string `json:"device_hash"`
	AppVersion    string `json:"app_version"`
	Edition       string `json:"edition"`
}

type VerifyRequest struct {
	PartnerID    string `json:"partner_id"`
	DeviceSecret string `json:"device_secret"`
	DeviceHash   string `json:"device_hash"`
	AppVersion   string `json:"app_version"`
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

type AuthResponse struct {
	PartnerID        string       `json:"partner_id"`
	DisplayName      string       `json:"display_name"`
	DeviceSecret     string       `json:"device_secret"`
	SessionToken     string       `json:"session_token"`
	SessionExpiresAt time.Time    `json:"session_expires_at"`
	Capabilities     Capabilities `json:"capabilities"`
	Aura             AuraRuntime  `json:"aura"`
}

type Credentials struct {
	PartnerID    string
	DeviceSecret string
	AuraAPIKey   string
}
