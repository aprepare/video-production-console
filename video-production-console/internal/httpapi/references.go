package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"video-production-console/internal/platformapi"
	"video-production-console/internal/references"
	"video-production-console/internal/store"
)

// referencesHandler serves the 对标采集 workspace: platform search, link
// collection, 通义听悟 transcription and the stored reference library.
type referencesHandler struct {
	service *references.Service
	repo    *store.ReferenceRepository
}

func NewReferencesHandler(service *references.Service, repo *store.ReferenceRepository) http.Handler {
	h := &referencesHandler{service: service, repo: repo}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/references/status", h.status)
	mux.HandleFunc("GET /api/references/search", h.search)
	mux.HandleFunc("GET /api/references", h.list)
	mux.HandleFunc("POST /api/references", h.collect)
	mux.HandleFunc("GET /api/references/{id}", h.get)
	mux.HandleFunc("PATCH /api/references/{id}", h.update)
	mux.HandleFunc("DELETE /api/references/{id}", h.delete)
	mux.HandleFunc("POST /api/references/{id}/transcribe", h.transcribe)
	mux.HandleFunc("GET /api/references/{id}/comments", h.comments)
	mux.HandleFunc("GET /api/references/{id}/media", h.media)
	mux.HandleFunc("POST /api/references/{id}/media", h.cacheMedia)
	mux.HandleFunc("GET /api/references/{id}/cover", h.cover)
	return mux
}

func (h *referencesHandler) platformError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, platformapi.ErrNotConfigured):
		writeError(w, http.StatusConflict, "platform_api_not_configured", "请先在设置里填写平台接口地址和 Token。")
	case errors.Is(err, store.ErrReferenceNotFound):
		writeError(w, http.StatusNotFound, "reference_not_found", "对标内容不存在。")
	case errors.Is(err, references.ErrNoTranscribableVideo):
		writeError(w, http.StatusConflict, "no_transcribable_video", "这条内容没有视频，无法转写口播。")
	case errors.Is(err, references.ErrNoReferenceVideo):
		writeError(w, http.StatusConflict, "no_reference_video", "这条内容没有视频。")
	default:
		writeError(w, http.StatusBadGateway, "platform_api_failed", err.Error())
	}
}

func (h *referencesHandler) status(w http.ResponseWriter, r *http.Request) {
	apps, err := h.service.Status(r.Context())
	if errors.Is(err, platformapi.ErrNotConfigured) {
		writeJSON(w, http.StatusOK, map[string]any{"configured": false, "apps": []platformapi.AppStatus{}})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"configured": true, "reachable": false, "message": err.Error(), "apps": []platformapi.AppStatus{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"configured": true, "reachable": true, "apps": apps})
}

func (h *referencesHandler) search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	platform := q.Get("platform")
	if platform != string(platformapi.Douyin) && platform != string(platformapi.XHS) {
		writeError(w, http.StatusBadRequest, "invalid_platform", "搜索只支持抖音和小红书。")
		return
	}
	keyword := strings.TrimSpace(q.Get("keyword"))
	if keyword == "" || len([]rune(keyword)) > 50 {
		writeError(w, http.StatusBadRequest, "invalid_keyword", "请输入 1–50 个字的关键词。")
		return
	}
	page, err := h.service.Search(r.Context(), platform, keyword, q.Get("cursor"), q.Get("search_id"), q.Get("session_id"))
	if err != nil {
		h.platformError(w, err)
		return
	}
	if page.Items == nil {
		page.Items = []platformapi.Post{}
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *referencesHandler) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	posts, err := h.repo.List(r.Context(), q.Get("q"), q.Get("platform"), q.Get("merchant_id"), q.Get("status"), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "references_list_failed", "对标列表读取失败。")
		return
	}
	writeJSON(w, http.StatusOK, posts)
}

func (h *referencesHandler) collect(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Link       string            `json:"link"`
		Post       *platformapi.Post `json:"post"`
		Keyword    string            `json:"keyword"`
		MerchantID string            `json:"merchant_id"`
		Transcribe bool              `json:"transcribe"`
	}
	if err := decodeJSON(w, r, maxNormalJSONRequest, &in); err != nil {
		writeDecodeError(w, err, "invalid_reference", "请提供分享链接或搜索结果。")
		return
	}
	if strings.TrimSpace(in.Link) == "" && in.Post == nil {
		writeError(w, http.StatusBadRequest, "invalid_reference", "请提供分享链接或搜索结果。")
		return
	}
	if in.Post != nil {
		switch in.Post.Platform {
		case platformapi.Douyin, platformapi.XHS, platformapi.Channels:
		default:
			writeError(w, http.StatusBadRequest, "invalid_platform", "不支持的平台。")
			return
		}
	}
	post, err := h.service.Collect(r.Context(), in.Link, in.Post, in.Keyword, in.MerchantID, in.Transcribe)
	if err != nil {
		h.platformError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, post)
}

func (h *referencesHandler) get(w http.ResponseWriter, r *http.Request) {
	post, err := h.repo.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		h.platformError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, post)
}

func (h *referencesHandler) update(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Transcript *string `json:"transcript"`
		Notes      *string `json:"notes"`
		MerchantID *string `json:"merchant_id"`
	}
	if err := decodeJSON(w, r, maxNormalJSONRequest, &in); err != nil {
		writeDecodeError(w, err, "invalid_reference", "修改内容格式不对。")
		return
	}
	current, err := h.repo.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		h.platformError(w, err)
		return
	}
	transcript, notes, merchant := current.Transcript, current.Notes, current.MerchantID
	if in.Transcript != nil {
		transcript = strings.TrimSpace(*in.Transcript)
	}
	if in.Notes != nil {
		notes = strings.TrimSpace(*in.Notes)
	}
	if in.MerchantID != nil {
		merchant = strings.TrimSpace(*in.MerchantID)
	}
	if err := h.repo.UpdateEditable(r.Context(), current.ID, transcript, notes, merchant); err != nil {
		h.platformError(w, err)
		return
	}
	h.get(w, r)
}

func (h *referencesHandler) delete(w http.ResponseWriter, r *http.Request) {
	if err := h.service.Delete(r.Context(), r.PathValue("id")); err != nil {
		h.platformError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *referencesHandler) transcribe(w http.ResponseWriter, r *http.Request) {
	post, err := h.service.Transcribe(r.Context(), r.PathValue("id"))
	if err != nil {
		h.platformError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, post)
}

func (h *referencesHandler) comments(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	comments, err := h.service.Comments(r.Context(), r.PathValue("id"), limit)
	if err != nil {
		h.platformError(w, err)
		return
	}
	if comments == nil {
		comments = []platformapi.Comment{}
	}
	writeJSON(w, http.StatusOK, comments)
}

func (h *referencesHandler) cacheMedia(w http.ResponseWriter, r *http.Request) {
	post, err := h.service.StartMediaCache(r.Context(), r.PathValue("id"))
	if err != nil {
		h.platformError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, post)
}

func (h *referencesHandler) media(w http.ResponseWriter, r *http.Request) {
	h.serveMedia(w, r, false)
}

func (h *referencesHandler) cover(w http.ResponseWriter, r *http.Request) {
	h.serveMedia(w, r, true)
}

func (h *referencesHandler) serveMedia(w http.ResponseWriter, r *http.Request, cover bool) {
	post, err := h.repo.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		h.platformError(w, err)
		return
	}
	notReady := func() {
		if cover {
			writeError(w, http.StatusNotFound, "reference_cover_not_ready", "封面还没下载好。")
		} else {
			writeError(w, http.StatusNotFound, "reference_media_not_ready", "原视频还没下载好。")
		}
	}
	path := post.MediaPath
	if cover {
		path = post.CoverPath
	} else if post.MediaStatus != "ready" {
		notReady()
		return
	}
	file, err := h.service.OpenMedia(r.Context(), path)
	if err != nil {
		notReady()
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		notReady()
		return
	}
	if cover {
		var head [512]byte
		n, err := file.ReadAt(head[:], 0)
		if n == 0 && err != nil {
			notReady()
			return
		}
		w.Header().Set("Content-Type", http.DetectContentType(head[:n]))
	} else {
		w.Header().Set("Content-Type", "video/mp4")
		if r.URL.Query().Get("download") == "1" {
			filename := strings.ReplaceAll(url.QueryEscape(referenceMediaFilename(post)), "+", "%20")
			w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+filename)
		}
	}
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
}

func referenceMediaFilename(post store.ReferencePost) string {
	clean := func(value string) string {
		value = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) || strings.ContainsRune(`/\:*?"<>|`, r) {
				return -1
			}
			return r
		}, value)
		return strings.Trim(value, " .")
	}
	title := []rune(strings.TrimSpace(post.Title))
	if len(title) > 30 {
		title = title[:30]
	}
	name := clean(string(title))
	if name == "" {
		name = clean(post.Platform + "-" + post.ExternalID)
	}
	return name + ".mp4"
}
