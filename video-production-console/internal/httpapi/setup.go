package httpapi

import (
	"context"
	"errors"
	"net/http"

	"video-production-console/internal/settings"
	"video-production-console/internal/setup"
)

const maxActivationRequestSize = (64 << 10) + 1024

type setupAPI interface {
	Status(context.Context) (setup.Status, error)
	Models(context.Context) (setup.Models, error)
	Detect(context.Context) (setup.Detection, error)
	ApplyJianying(context.Context, string) (setup.ApplyResult, error)
}

type activationAPI interface {
	ActivateCode(context.Context, string) (settings.ActivationResult, error)
	ExportCode(context.Context, settings.ExportCodeRequest) (settings.ExportCodeResult, error)
}

type setupHandler struct {
	service    setupAPI
	activation activationAPI
}

func NewSetupHandler(service setupAPI, activation activationAPI) http.Handler {
	h := &setupHandler{service: service, activation: activation}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/setup/status", h.status)
	mux.HandleFunc("GET /api/setup/models", h.models)
	mux.HandleFunc("POST /api/setup/activate", h.activate)
	mux.HandleFunc("POST /api/setup/export-code", h.exportCode)
	mux.HandleFunc("GET /api/jianying-drafts/detect", h.detect)
	mux.HandleFunc("POST /api/jianying-drafts/detect/apply", h.applyJianying)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, r)
	})
}

func (h *setupHandler) status(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.Status(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "setup_status_failed", "配置体检暂时不可用，请稍后重试。")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *setupHandler) models(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.Models(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "setup_models_failed", "模型列表暂时不可用，请稍后重试。")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *setupHandler) activate(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Code string `json:"code"`
	}
	if err := decodeJSON(w, r, maxActivationRequestSize, &input); err != nil {
		setupDecodeError(w, err)
		return
	}
	result, err := h.activation.ActivateCode(r.Context(), input.Code)
	if errors.Is(err, settings.ErrInvalidActivationCode) {
		writeError(w, http.StatusBadRequest, "invalid_activation_code", err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "activation_failed", "配置码应用失败，请检查本机设置后重试。")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *setupHandler) exportCode(w http.ResponseWriter, r *http.Request) {
	var input settings.ExportCodeRequest
	if err := decodeJSON(w, r, maxSmallJSONRequest, &input); err != nil {
		setupDecodeError(w, err)
		return
	}
	result, err := h.activation.ExportCode(r.Context(), input)
	if errors.Is(err, settings.ErrInvalidExportRequest) {
		writeError(w, http.StatusBadRequest, "invalid_export_request", err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "export_code_failed", "配置码导出失败，请检查已保存配置后重试。")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *setupHandler) detect(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.Detect(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "jianying_detect_failed", "剪映检测失败，请确认已安装剪映后重试。")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *setupHandler) applyJianying(w http.ResponseWriter, r *http.Request) {
	var input struct {
		DraftRoot string `json:"draft_root"`
	}
	if err := decodeJSON(w, r, maxSmallJSONRequest, &input); err != nil {
		setupDecodeError(w, err)
		return
	}
	result, err := h.service.ApplyJianying(r.Context(), input.DraftRoot)
	if errors.Is(err, setup.ErrInvalidDraftRoot) {
		writeError(w, http.StatusBadRequest, "invalid_jianying_root", err.Error())
		return
	}
	if err != nil {
		// setup 的应用错误均为固定中文说明，保留“已保存但档案同步失败”的状态。
		writeError(w, http.StatusUnprocessableEntity, "jianying_apply_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func setupDecodeError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "请求内容过大，请缩小配置后重试。")
		return
	}
	writeError(w, http.StatusBadRequest, "invalid_setup_request", "请求格式无效，请检查填写内容后重试。")
}
