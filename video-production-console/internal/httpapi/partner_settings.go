package httpapi

import (
	"errors"
	"net/http"

	"video-production-console/internal/domain"
	"video-production-console/internal/partnerclient"
	consoleSettings "video-production-console/internal/settings"
)

type partnerSettingsAPI interface {
	settingsReaderWriter
	RuntimeSnapshot() partnerclient.RuntimeSnapshot
}

// PartnerSettingsView is the complete settings surface available to partner
// users. Provider endpoints, credentials, and owner-only settings are omitted.
type PartnerSettingsView struct {
	TextModels         []string `json:"text_models,omitempty"`
	ReasoningEfforts   []string `json:"reasoning_efforts,omitempty"`
	ImageModel         string   `json:"image_model,omitempty"`
	AuraModel          string   `json:"aura_model,omitempty"`
	AuraVoiceID        string   `json:"aura_voice_id,omitempty"`
	MediaRoot          string   `json:"media_root,omitempty"`
	JianyingRoot       string   `json:"jianying_root,omitempty"`
	MachineProfilePath string   `json:"machine_profile_path,omitempty"`
	DefaultImageRatio  string   `json:"default_image_ratio,omitempty"`
	DefaultImageStyle  string   `json:"default_image_style,omitempty"`
	AuraSpeed          float64  `json:"aura_speed,omitempty"`
	AuraVolume         float64  `json:"aura_volume,omitempty"`
}

// PartnerSettingsUpdate accepts only machine-local paths and presentation
// preferences. Pointers preserve omitted fields during partial updates.
type PartnerSettingsUpdate struct {
	MediaRoot          *string  `json:"media_root,omitempty"`
	JianyingRoot       *string  `json:"jianying_root,omitempty"`
	MachineProfilePath *string  `json:"machine_profile_path,omitempty"`
	DefaultImageRatio  *string  `json:"default_image_ratio,omitempty"`
	DefaultImageStyle  *string  `json:"default_image_style,omitempty"`
	AuraSpeed          *float64 `json:"aura_speed,omitempty"`
	AuraVolume         *float64 `json:"aura_volume,omitempty"`
}

type partnerSettingsHandler struct {
	service partnerSettingsAPI
}

func NewPartnerSettingsHandler(service partnerSettingsAPI) http.Handler {
	handler := &partnerSettingsHandler{service: service}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/settings", handler.get)
	mux.HandleFunc("PUT /api/settings", handler.put)
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(response, request)
	})
}

func (h *partnerSettingsHandler) get(response http.ResponseWriter, request *http.Request) {
	settingsView, err := h.service.Get(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "settings_read_failed", "Settings could not be read.")
		return
	}
	writeJSON(response, http.StatusOK, newPartnerSettingsView(settingsView.Public, h.service.RuntimeSnapshot()))
}

func (h *partnerSettingsHandler) put(response http.ResponseWriter, request *http.Request) {
	var input PartnerSettingsUpdate
	if err := decodeJSON(response, request, maxSettingsRequestSize, &input); err != nil {
		writeDecodeError(response, err, "invalid_settings", settingsDecodeMessage(err))
		return
	}
	current, err := h.service.Get(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "settings_read_failed", "Settings could not be read.")
		return
	}
	updated := overlayPartnerSettings(current.Public, input)
	saved, err := h.service.Update(request.Context(), updated, nil)
	switch {
	case errors.Is(err, consoleSettings.ErrInvalidSettings):
		writeError(response, http.StatusBadRequest, "invalid_settings", consoleSettings.UserMessage(err))
	case err != nil:
		writeError(response, http.StatusInternalServerError, "settings_update_failed", "Settings could not be saved.")
	default:
		writeJSON(response, http.StatusOK, newPartnerSettingsView(saved.Public, h.service.RuntimeSnapshot()))
	}
}

func overlayPartnerSettings(current domain.PublicSettings, input PartnerSettingsUpdate) domain.PublicSettings {
	if input.MediaRoot != nil {
		current.MediaRoot = *input.MediaRoot
	}
	if input.JianyingRoot != nil {
		current.JianyingRoot = *input.JianyingRoot
	}
	if input.MachineProfilePath != nil {
		current.MachineProfilePath = *input.MachineProfilePath
	}
	if input.DefaultImageRatio != nil {
		current.DefaultImageRatio = *input.DefaultImageRatio
	}
	if input.DefaultImageStyle != nil {
		current.DefaultImageStyle = *input.DefaultImageStyle
	}
	if input.AuraSpeed != nil {
		current.AuraSTDSpeed = *input.AuraSpeed
	}
	if input.AuraVolume != nil {
		current.AuraSTDVolume = *input.AuraVolume
	}
	return current
}

func newPartnerSettingsView(public domain.PublicSettings, runtime partnerclient.RuntimeSnapshot) PartnerSettingsView {
	return PartnerSettingsView{
		TextModels:         append([]string(nil), runtime.Capabilities.TextModels...),
		ReasoningEfforts:   append([]string(nil), runtime.Capabilities.ReasoningEfforts...),
		ImageModel:         runtime.Capabilities.ImageModel,
		AuraModel:          runtime.Aura.Model,
		AuraVoiceID:        runtime.Aura.VoiceID,
		MediaRoot:          public.MediaRoot,
		JianyingRoot:       public.JianyingRoot,
		MachineProfilePath: public.MachineProfilePath,
		DefaultImageRatio:  public.DefaultImageRatio,
		DefaultImageStyle:  public.DefaultImageStyle,
		AuraSpeed:          public.AuraSTDSpeed,
		AuraVolume:         public.AuraSTDVolume,
	}
}
