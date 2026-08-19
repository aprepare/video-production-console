package httpapi

import (
	"context"
	"net/http"
	"strings"

	"video-production-console/internal/partnerclient"
)

type partnerAPI interface {
	Snapshot() partnerclient.Snapshot
	Activate(context.Context, string) error
}

type partnerHandler struct {
	manager partnerAPI
}

// NewPartnerHandler exposes the sanitized partner status and one-time
// activation exchange.
func NewPartnerHandler(manager partnerAPI) http.Handler {
	handler := &partnerHandler{manager: manager}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/partner/status", handler.status)
	mux.HandleFunc("POST /api/partner/activate", handler.activate)
	return mux
}

func (h *partnerHandler) status(response http.ResponseWriter, _ *http.Request) {
	writeJSON(response, http.StatusOK, h.manager.Snapshot())
}

func (h *partnerHandler) activate(response http.ResponseWriter, request *http.Request) {
	var input struct {
		ActivationKey string `json:"activation_key"`
	}
	if err := decodeJSON(response, request, maxSmallJSONRequest, &input); err != nil {
		writeDecodeError(response, err, "invalid_activation", "A valid activation key is required.")
		return
	}
	input.ActivationKey = strings.TrimSpace(input.ActivationKey)
	if input.ActivationKey == "" {
		writeError(response, http.StatusBadRequest, "invalid_activation", "A valid activation key is required.")
		return
	}
	if err := h.manager.Activate(request.Context(), input.ActivationKey); err != nil {
		code := h.manager.Snapshot().ErrorCode
		if code == "" {
			code = "activation_failed"
		}
		writeError(response, http.StatusUnauthorized, code, "Partner activation could not be completed.")
		return
	}
	writeJSON(response, http.StatusOK, h.manager.Snapshot())
}
