package httpapi

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"video-production-console/internal/refanalysis"
	"video-production-console/internal/store"
)

type referenceAnalysesHandler struct{ service *refanalysis.Service }

func NewReferenceAnalysesHandler(service *refanalysis.Service) http.Handler {
	h := &referenceAnalysesHandler{service: service}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/reference-analyses", h.start)
	mux.HandleFunc("GET /api/reference-analyses", h.list)
	mux.HandleFunc("GET /api/reference-analyses/{id}", h.get)
	mux.HandleFunc("GET /api/reference-analyses/{id}/frames/{n}", h.frame)
	mux.HandleFunc("DELETE /api/reference-analyses/{id}", h.delete)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		// 在 ServeMux 清理路径或重定向之前拒绝显式穿越，包括编码后的分隔符。
		for _, part := range strings.Split(r.URL.Path, "/") {
			if part == "." || part == ".." || strings.Contains(part, `\`) {
				h.error(w, refanalysis.ErrUnsafePath)
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}

func (h *referenceAnalysesHandler) error(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, refanalysis.ErrMediaNotReady):
		writeError(w, http.StatusConflict, "reference_media_not_ready", "原视频还没下载好，先等下载完成。")
	case errors.Is(err, refanalysis.ErrNotConfigured):
		writeError(w, http.StatusConflict, "analysis_not_configured", "请先在设置里配置拆解模型")
	case errors.Is(err, refanalysis.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_analysis", "请提供有效的对标编号和拆解参数。")
	case errors.Is(err, refanalysis.ErrUnsafePath):
		writeError(w, http.StatusForbidden, "invalid_frame_path", "分析文件路径不安全。")
	case errors.Is(err, store.ErrReferenceNotFound):
		writeError(w, http.StatusNotFound, "reference_not_found", "对标内容不存在。")
	case errors.Is(err, store.ErrReferenceAnalysisNotFound):
		writeError(w, http.StatusNotFound, "reference_analysis_not_found", "拆解分析不存在。")
	case errors.Is(err, refanalysis.ErrFrameNotFound):
		writeError(w, http.StatusNotFound, "reference_analysis_frame_not_found", "关键帧不存在。")
	default:
		writeError(w, http.StatusInternalServerError, "reference_analysis_failed", "拆解分析操作失败，请稍后重试。")
	}
}

func (h *referenceAnalysesHandler) start(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ReferenceID string `json:"reference_id"`
		Model       string `json:"model"`
		Focus       string `json:"focus"`
		Product     string `json:"product"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxNormalJSONRequest)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeDecodeError(w, err, "invalid_analysis", "拆解请求格式不正确。")
		return
	}
	if !utf8.Valid(raw) {
		writeError(w, http.StatusBadRequest, "invalid_analysis", "拆解请求格式不正确。")
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	if err := decodeJSON(w, r, maxNormalJSONRequest, &input); err != nil {
		writeDecodeError(w, err, "invalid_analysis", "拆解请求格式不正确。")
		return
	}
	a, err := h.service.Start(r.Context(), input.ReferenceID, input.Model, input.Focus, input.Product)
	if err != nil {
		h.error(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, a)
}

func (h *referenceAnalysesHandler) list(w http.ResponseWriter, r *http.Request) {
	analyses, err := h.service.ListByReference(r.Context(), strings.TrimSpace(r.URL.Query().Get("reference_id")))
	if err != nil {
		h.error(w, err)
		return
	}
	writeJSON(w, http.StatusOK, analyses)
}

func (h *referenceAnalysesHandler) get(w http.ResponseWriter, r *http.Request) {
	a, err := h.service.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		h.error(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (h *referenceAnalysesHandler) frame(w http.ResponseWriter, r *http.Request) {
	index, err := strconv.Atoi(r.PathValue("n"))
	if err != nil {
		h.error(w, refanalysis.ErrInvalidInput)
		return
	}
	f, err := h.service.OpenFrame(r.Context(), r.PathValue("id"), index)
	if err != nil {
		h.error(w, err)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		h.error(w, refanalysis.ErrFrameNotFound)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "frame.jpg", info.ModTime(), f)
}

func (h *referenceAnalysesHandler) delete(w http.ResponseWriter, r *http.Request) {
	if err := h.service.Delete(r.Context(), r.PathValue("id")); err != nil {
		h.error(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
