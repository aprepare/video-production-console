package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/google/uuid"
	"video-production-console/internal/aigc"
	"video-production-console/internal/store"
)

const maxAIGCRequestBytes int64 = 1 << 20

type aigcHandler struct {
	service *aigc.Service
}

// NewAIGCHandler serves the AIGC workflow API.
func NewAIGCHandler(service *aigc.Service) http.Handler {
	h := &aigcHandler{service: service}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/aigc/extract-frames", h.extractFrames)
	mux.HandleFunc("POST /api/aigc/restyle", h.restyle)
	mux.HandleFunc("POST /api/aigc/image-to-video", h.prepareImageToVideo)
	mux.HandleFunc("POST /api/aigc/jobs/{id}/confirm", h.confirmImageToVideo)
	mux.HandleFunc("GET /api/aigc/jobs", h.listJobs)
	mux.HandleFunc("GET /api/aigc/jobs/{id}", h.getJob)
	mux.HandleFunc("GET /api/aigc/lineage/{materialID}", h.lineage)
	return mux
}

func decodeAIGCJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxAIGCRequestBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "请求内容格式不正确。")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_request", "请求只能包含一个 JSON 对象。")
		return false
	}
	return true
}

func normalizeAIGCID(w http.ResponseWriter, value string) (string, bool) {
	id, err := uuid.Parse(value)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "标识格式不正确。")
		return "", false
	}
	return id.String(), true
}

func normalizeAIGCIDs(w http.ResponseWriter, values []string) ([]string, bool) {
	result := make([]string, len(values))
	for i, value := range values {
		id, ok := normalizeAIGCID(w, value)
		if !ok {
			return nil, false
		}
		result[i] = id
	}
	return result, true
}

func writeAIGCError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, aigc.ErrInvalidInput), errors.Is(err, store.ErrInvalidMerchantData):
		writeError(w, http.StatusBadRequest, "invalid_input", "输入参数不正确。")
	case errors.Is(err, aigc.ErrUnsafePath):
		writeError(w, http.StatusBadRequest, "unsafe_path", "素材路径不安全。")
	case errors.Is(err, aigc.ErrImageAPINotConfigured):
		writeError(w, http.StatusConflict, "image_api_not_configured", "图片生成服务尚未配置。")
	case errors.Is(err, store.ErrAIGCJobConflict):
		writeError(w, http.StatusConflict, "aigc_job_conflict", "作业当前状态不允许此操作。")
	case errors.Is(err, store.ErrAIGCJobNotFound):
		writeError(w, http.StatusNotFound, "aigc_job_not_found", "AIGC 作业不存在。")
	case errors.Is(err, store.ErrMerchantNotFound):
		writeError(w, http.StatusNotFound, "merchant_not_found", "商户不存在。")
	case errors.Is(err, store.ErrMaterialNotFound):
		writeError(w, http.StatusNotFound, "material_not_found", "素材不存在。")
	case errors.Is(err, store.ErrOrderNotFound):
		writeError(w, http.StatusNotFound, "order_not_found", "订单不存在。")
	default:
		writeError(w, http.StatusInternalServerError, "aigc_operation_failed", "AIGC 操作失败。")
	}
}

func (h *aigcHandler) extractFrames(w http.ResponseWriter, r *http.Request) {
	var input struct {
		MerchantID string `json:"merchant_id"`
		MaterialID string `json:"material_id"`
		Count      int    `json:"count"`
	}
	if !decodeAIGCJSON(w, r, &input) {
		return
	}
	merchantID, ok := normalizeAIGCID(w, input.MerchantID)
	if !ok {
		return
	}
	materialID, ok := normalizeAIGCID(w, input.MaterialID)
	if !ok {
		return
	}
	job, materials, err := h.service.ExtractFrames(r.Context(), merchantID, materialID, input.Count)
	if err != nil {
		writeAIGCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": job, "materials": materials})
}

func (h *aigcHandler) restyle(w http.ResponseWriter, r *http.Request) {
	var input struct {
		MerchantID    string   `json:"merchant_id"`
		MaterialIDs   []string `json:"material_ids"`
		Prompt        string   `json:"prompt"`
		Preserve      string   `json:"preserve"`
		OrderID       string   `json:"order_id"`
		UnitCostCents int64    `json:"unit_cost_cents"`
	}
	if !decodeAIGCJSON(w, r, &input) {
		return
	}
	merchantID, ok := normalizeAIGCID(w, input.MerchantID)
	if !ok {
		return
	}
	materialIDs, ok := normalizeAIGCIDs(w, input.MaterialIDs)
	if !ok {
		return
	}
	if input.OrderID != "" {
		input.OrderID, ok = normalizeAIGCID(w, input.OrderID)
		if !ok {
			return
		}
	}
	job, err := h.service.Restyle(r.Context(), merchantID, materialIDs, input.Prompt, input.Preserve, aigc.JobOptions{OrderID: input.OrderID, UnitCostCents: input.UnitCostCents})
	if err != nil {
		writeAIGCError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (h *aigcHandler) prepareImageToVideo(w http.ResponseWriter, r *http.Request) {
	var input struct {
		MerchantID  string   `json:"merchant_id"`
		MaterialIDs []string `json:"material_ids"`
		Prompt      string   `json:"prompt"`
		DurationS   int      `json:"duration_s"`
		Aspect      string   `json:"aspect"`
		OrderID     string   `json:"order_id"`
	}
	if !decodeAIGCJSON(w, r, &input) {
		return
	}
	merchantID, ok := normalizeAIGCID(w, input.MerchantID)
	if !ok {
		return
	}
	materialIDs, ok := normalizeAIGCIDs(w, input.MaterialIDs)
	if !ok {
		return
	}
	if input.OrderID != "" {
		input.OrderID, ok = normalizeAIGCID(w, input.OrderID)
		if !ok {
			return
		}
	}
	job, err := h.service.PrepareImageToVideo(r.Context(), merchantID, materialIDs, input.Prompt, input.DurationS, input.Aspect, aigc.JobOptions{OrderID: input.OrderID})
	if err != nil {
		writeAIGCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (h *aigcHandler) confirmImageToVideo(w http.ResponseWriter, r *http.Request) {
	id, ok := normalizeAIGCID(w, r.PathValue("id"))
	if !ok {
		return
	}
	job, err := h.service.ConfirmImageToVideo(r.Context(), id)
	if err != nil {
		writeAIGCError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (h *aigcHandler) listJobs(w http.ResponseWriter, r *http.Request) {
	merchantID, ok := normalizeAIGCID(w, r.URL.Query().Get("merchant_id"))
	if !ok {
		return
	}
	jobs, err := h.service.ListJobs(r.Context(), merchantID)
	if err != nil {
		writeAIGCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, jobs)
}

func (h *aigcHandler) getJob(w http.ResponseWriter, r *http.Request) {
	id, ok := normalizeAIGCID(w, r.PathValue("id"))
	if !ok {
		return
	}
	job, err := h.service.GetJob(r.Context(), id)
	if err != nil {
		writeAIGCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (h *aigcHandler) lineage(w http.ResponseWriter, r *http.Request) {
	materialID, ok := normalizeAIGCID(w, r.PathValue("materialID"))
	if !ok {
		return
	}
	lineage, err := h.service.Lineage(r.Context(), materialID)
	if err != nil {
		writeAIGCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, lineage)
}
