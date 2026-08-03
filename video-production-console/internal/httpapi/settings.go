package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"video-production-console/internal/domain"
	"video-production-console/internal/security"
	consoleSettings "video-production-console/internal/settings"
)

const maxSettingsRequestSize = 64 << 10

type settingsAPI interface {
	Get(context.Context) (consoleSettings.View, error)
	Update(context.Context, domain.PublicSettings, map[string]string) (consoleSettings.View, error)
	TestDependency(context.Context, string) consoleSettings.Health
	RepairBaokuanMCP(context.Context) consoleSettings.Health
}

type settingsHandler struct{ service settingsAPI }

func NewSettingsHandler(service settingsAPI) http.Handler {
	handler := &settingsHandler{service: service}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/settings", handler.get)
	mux.HandleFunc("PUT /api/settings", handler.put)
	mux.HandleFunc("POST /api/settings/test/{dependency}", handler.testDependency)
	mux.HandleFunc("POST /api/settings/repair/baokuan-mcp", handler.repairBaokuanMCP)
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(response, request)
	})
}

func (h *settingsHandler) get(response http.ResponseWriter, request *http.Request) {
	view, err := h.service.Get(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "settings_read_failed", "Settings could not be read.")
		return
	}
	writeJSON(response, http.StatusOK, view)
}

func (h *settingsHandler) put(response http.ResponseWriter, request *http.Request) {
	var input struct {
		Public  domain.PublicSettings `json:"public"`
		Secrets map[string]string     `json:"secrets"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, maxSettingsRequestSize))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_settings", "The settings request is invalid.")
		return
	}
	if err := ensureJSONEOF(decoder); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_settings", "The settings request is invalid.")
		return
	}
	if input.Secrets == nil {
		input.Secrets = map[string]string{}
	}
	view, err := h.service.Update(request.Context(), input.Public, input.Secrets)
	switch {
	case errors.Is(err, consoleSettings.ErrInvalidSettings), errors.Is(err, consoleSettings.ErrUnknownSecret), errors.Is(err, security.ErrSecretTooLarge), errors.Is(err, security.ErrSecretEmpty):
		writeError(response, http.StatusBadRequest, "invalid_settings", "The settings request is invalid.")
		return
	case err != nil:
		writeError(response, http.StatusInternalServerError, "settings_update_failed", "Settings could not be updated.")
		return
	default:
		writeJSON(response, http.StatusOK, view)
	}
}

func (h *settingsHandler) testDependency(response http.ResponseWriter, request *http.Request) {
	dependency := strings.TrimSpace(request.PathValue("dependency"))
	if dependency == "" || strings.Contains(dependency, "/") {
		writeError(response, http.StatusNotFound, "dependency_not_found", "The dependency was not found.")
		return
	}
	writeJSON(response, http.StatusOK, h.service.TestDependency(request.Context(), dependency))
}

func (h *settingsHandler) repairBaokuanMCP(response http.ResponseWriter, request *http.Request) {
	writeJSON(response, http.StatusOK, h.service.RepairBaokuanMCP(request.Context()))
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("multiple JSON values")
	}
	return err
}
