package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"video-production-console/internal/commentinsight"
	"video-production-console/internal/refanalysis"
	"video-production-console/internal/store"
)

type commentInsightsAPI interface {
	Start(context.Context, string, string) (store.CommentInsight, bool, error)
	Get(context.Context, string) (commentinsight.Insight, error)
	List(context.Context, string) ([]store.CommentInsight, error)
	Delete(context.Context, string) error
}

type commentInsightsHandler struct{ service commentInsightsAPI }

func NewCommentInsightsHandler(service commentInsightsAPI) http.Handler {
	h := &commentInsightsHandler{service: service}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/comment-insights", h.start)
	mux.HandleFunc("GET /api/comment-insights", h.list)
	mux.HandleFunc("GET /api/comment-insights/{id}", h.get)
	mux.HandleFunc("DELETE /api/comment-insights/{id}", h.delete)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, r)
	})
}

func (h *commentInsightsHandler) start(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ReferenceID string `json:"reference_id"`
		Model       string `json:"model"`
	}
	if err := decodeJSON(w, r, maxNormalJSONRequest, &input); err != nil {
		writeDecodeError(w, err, "invalid_comment_insight", "评论洞察请求格式不正确。")
		return
	}
	item, created, err := h.service.Start(r.Context(), input.ReferenceID, input.Model)
	if err != nil {
		h.writeError(w, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusAccepted
	}
	writeJSON(w, status, item)
}

func (h *commentInsightsHandler) list(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.List(r.Context(), strings.TrimSpace(r.URL.Query().Get("reference_id")))
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (h *commentInsightsHandler) get(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h *commentInsightsHandler) delete(w http.ResponseWriter, r *http.Request) {
	if err := h.service.Delete(r.Context(), r.PathValue("id")); err != nil {
		h.writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *commentInsightsHandler) writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, commentinsight.ErrUnsupported):
		writeError(w, http.StatusBadRequest, "insight_unsupported", "视频号接口不提供评论")
	case errors.Is(err, commentinsight.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_comment_insight", "请提供有效的对标编号和模型参数。")
	case errors.Is(err, refanalysis.ErrNotConfigured):
		writeError(w, http.StatusConflict, "analysis_not_configured", "请先在设置里配置拆解模型")
	case errors.Is(err, store.ErrReferenceNotFound):
		writeError(w, http.StatusNotFound, "reference_not_found", "对标内容不存在。")
	case errors.Is(err, store.ErrCommentInsightNotFound):
		writeError(w, http.StatusNotFound, "comment_insight_not_found", "评论洞察不存在。")
	default:
		writeError(w, http.StatusInternalServerError, "comment_insight_failed", "评论洞察操作失败，请稍后重试。")
	}
}
