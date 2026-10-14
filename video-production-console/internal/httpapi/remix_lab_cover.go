package httpapi

import (
	"errors"
	"net/http"
	"os"

	"video-production-console/internal/cover"
)

func (h *remixLabHandler) postCover(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Line1 string `json:"line1"`
		Line2 string `json:"line2"`
	}
	if err := decodeJSON(w, r, maxNormalJSONRequest, &in); err != nil {
		writeDecodeError(w, err, "invalid_cover", "请填写封面两行标题。")
		return
	}
	view, err := h.svc.GenerateCover(r.Context(), r.PathValue("id"), in.Line1, in.Line2)
	switch {
	case errors.Is(err, cover.ErrInvalidTitle):
		writeError(w, http.StatusBadRequest, "invalid_cover_title", "封面标题清理标点后每行须为1至12个字，不能含空格或符号。")
	case errors.Is(err, cover.ErrNoTemplate):
		writeError(w, http.StatusConflict, "cover_template_missing", "这个账号还没有封面模板，请先在技能里为它生成一张参考封面")
	case err != nil:
		writeRemixLabError(w, err)
	default:
		writeJSON(w, http.StatusOK, view)
	}
}

func (h *remixLabHandler) getCover(w http.ResponseWriter, r *http.Request) {
	path, err := h.svc.RunCoverPath(r.Context(), r.PathValue("id"))
	if errors.Is(err, os.ErrNotExist) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		writeRemixLabError(w, err)
		return
	}
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		writeRemixLabError(w, err)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, "cover.png", info.ModTime(), file)
}

func (h *remixLabHandler) getPackage(w http.ResponseWriter, r *http.Request) {
	pkg, err := h.svc.RunPackage(r.Context(), r.PathValue("id"))
	if err != nil {
		writeRemixLabError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pkg)
}
