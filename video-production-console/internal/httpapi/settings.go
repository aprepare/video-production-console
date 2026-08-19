package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"video-production-console/internal/domain"
	"video-production-console/internal/security"
	consoleSettings "video-production-console/internal/settings"
)

const maxSettingsRequestSize = 64 << 10

type settingsReaderWriter interface {
	Get(context.Context) (consoleSettings.View, error)
	Update(context.Context, domain.PublicSettings, map[string]string) (consoleSettings.View, error)
}

type settingsAPI interface {
	settingsReaderWriter
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
		Public  json.RawMessage   `json:"public"`
		Secrets map[string]string `json:"secrets"`
	}
	if err := decodeJSON(response, request, maxSettingsRequestSize, &input); err != nil {
		writeDecodeError(response, err, "invalid_settings", settingsDecodeMessage(err))
		return
	}
	if input.Secrets == nil {
		input.Secrets = map[string]string{}
	}
	current, err := h.service.Get(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "settings_read_failed", "当前设置读取失败，请刷新后重试。")
		return
	}
	merged, err := consoleSettings.OverlayPublic(current.Public, input.Public)
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid_settings", consoleSettings.UserMessage(err))
		return
	}
	view, err := h.service.Update(request.Context(), merged, input.Secrets)
	switch {
	case errors.Is(err, consoleSettings.ErrInvalidSettings), errors.Is(err, consoleSettings.ErrUnknownSecret), errors.Is(err, security.ErrSecretTooLarge), errors.Is(err, security.ErrSecretEmpty):
		writeError(response, http.StatusBadRequest, "invalid_settings", consoleSettings.UserMessage(err))
		return
	case err != nil:
		writeError(response, http.StatusInternalServerError, "settings_update_failed", "设置保存失败，请稍后重试。")
		return
	default:
		writeJSON(response, http.StatusOK, view)
	}
}

func settingsDecodeMessage(err error) string {
	if err != nil && strings.Contains(err.Error(), "unknown field") {
		return "请求里有不支持的设置项，请刷新页面后重试。"
	}
	return "设置请求格式无效，请刷新页面后重试。"
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
