package httpapi

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"video-production-console/internal/platformapi"
)

func (h *referencesHandler) preview(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	platform, kind, rawURL := q.Get("platform"), q.Get("kind"), strings.TrimSpace(q.Get("url"))
	switch platform {
	case string(platformapi.Douyin), string(platformapi.XHS), string(platformapi.Channels):
	default:
		writeError(w, http.StatusBadRequest, "invalid_platform", "预览平台不支持。")
		return
	}
	if kind != "cover" && kind != "video" {
		writeError(w, http.StatusBadRequest, "invalid_preview_kind", "预览类型只支持 cover 或 video。")
		return
	}
	if rawURL == "" {
		writeError(w, http.StatusBadRequest, "preview_host_not_allowed", "预览地址无效。")
		return
	}
	if strings.HasPrefix(rawURL, "/api/") || strings.HasPrefix(rawURL, "/cache/") {
		if platform != string(platformapi.XHS) || kind != "cover" {
			writeError(w, http.StatusBadRequest, "preview_host_not_allowed", "该网关地址不能用于此预览。")
			return
		}
	}
	if kind == "cover" {
		cover, err := h.service.PreviewCover(r.Context(), platform, rawURL)
		if err != nil {
			h.previewError(w, err)
			return
		}
		w.Header().Set("Content-Type", cover.ContentType)
		w.Header().Set("Cache-Control", "private, max-age=3600")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(cover.Data)
		return
	}
	resp, err := h.service.OpenPreview(r.Context(), platform, rawURL, r.Header.Get("Range"))
	if err != nil {
		h.previewError(w, err)
		return
	}
	defer resp.Body.Close()
	for _, name := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges"} {
		if value := resp.Header.Get(name); value != "" {
			w.Header().Set(name, value)
		}
	}
	w.Header().Set("Cache-Control", "private, max-age=600")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (h *referencesHandler) previewError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, platformapi.ErrMediaHostNotAllowed), errors.Is(err, platformapi.ErrMediaRedirect):
		writeError(w, http.StatusBadRequest, "preview_host_not_allowed", "预览地址不在该平台允许的域名内。")
	case errors.Is(err, platformapi.ErrMediaTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "preview_too_large", "封面超过 15MB。")
	default:
		h.platformError(w, err)
	}
}
