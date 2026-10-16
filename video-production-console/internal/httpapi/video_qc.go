package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"video-production-console/internal/store"
	"video-production-console/internal/videoqc"
)

const maxVideoQCJSONBytes = 64 << 10

type videoQCAPI interface {
	Start(context.Context, videoqc.Input) (videoqc.Check, error)
	StartUpload(context.Context, videoqc.Input, string, io.Reader) (videoqc.Check, error)
	List(context.Context, int) ([]store.VideoQCCheck, error)
	Get(context.Context, string) (videoqc.Check, error)
	OpenFrame(context.Context, string, int) (*os.File, error)
	OpenVideo(context.Context, string) (*os.File, error)
	Delete(context.Context, string) error
}

type videoQCHandler struct{ service videoQCAPI }

func NewVideoQCHandler(service *videoqc.Service) http.Handler {
	return newVideoQCHandler(service)
}

func newVideoQCHandler(service videoQCAPI) http.Handler {
	h := &videoQCHandler{service: service}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/video-qc", h.start)
	mux.HandleFunc("GET /api/video-qc", h.list)
	mux.HandleFunc("GET /api/video-qc/{id}", h.get)
	mux.HandleFunc("GET /api/video-qc/{id}/frames/{n}", h.frame)
	mux.HandleFunc("GET /api/video-qc/{id}/video", h.video)
	mux.HandleFunc("DELETE /api/video-qc/{id}", h.delete)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if unsafeVideoQCPath(r.URL) {
			h.writeError(w, videoqc.ErrUnsafePath)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func unsafeVideoQCPath(u *url.URL) bool {
	path := u.Path
	if escaped := u.EscapedPath(); escaped != "" {
		if decoded, err := url.PathUnescape(escaped); err == nil {
			path = decoded
		}
	}
	for _, part := range strings.Split(path, "/") {
		if part == "." || part == ".." || strings.Contains(part, `\`) {
			return true
		}
	}
	return false
}

func (h *videoQCHandler) start(w http.ResponseWriter, r *http.Request) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_video_qc", "审核请求格式不正确。")
		return
	}
	switch mediaType {
	case "application/json":
		var input videoqc.Input
		if err := decodeVideoQCJSON(w, r, &input); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_video_qc", "请提供合法的 UTF-8 JSON 审核参数。")
			return
		}
		check, err := h.service.Start(r.Context(), input)
		if err != nil {
			h.writeError(w, err)
			return
		}
		if check.Frames == nil {
			check.Frames = []videoqc.Frame{}
		}
		writeJSON(w, http.StatusAccepted, check)
	case "multipart/form-data":
		h.startUpload(w, r)
	default:
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_video_format", "请上传 MP4、MOV、WebM 或 MKV 视频，或提交 JSON 审核参数。")
	}
}

func decodeVideoQCJSON(w http.ResponseWriter, r *http.Request, out any) error {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxVideoQCJSONBytes))
	if err != nil {
		return err
	}
	if !utf8.Valid(raw) {
		return errors.New("request is not valid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	return ensureJSONEOF(decoder)
}

func (h *videoQCHandler) startUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, videoqc.MaxUploadBytes)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "video_qc_too_large", "视频不能超过 300 MB。")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_video_qc", "无法读取视频上传表单。")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_video_qc", "请选择要审核的视频文件。")
		return
	}
	defer file.Close()
	input := videoqc.Input{
		Source:  "upload",
		Product: r.FormValue("product"),
		Model:   r.FormValue("model"),
	}
	if !utf8.ValidString(input.Product) || !utf8.ValidString(input.Model) || !utf8.ValidString(header.Filename) {
		writeError(w, http.StatusBadRequest, "invalid_video_qc", "上传字段必须是合法的 UTF-8 文本。")
		return
	}
	check, err := h.service.StartUpload(r.Context(), input, header.Filename, file)
	if err != nil {
		h.writeError(w, err)
		return
	}
	if check.Frames == nil {
		check.Frames = []videoqc.Frame{}
	}
	writeJSON(w, http.StatusAccepted, check)
}

func (h *videoQCHandler) list(w http.ResponseWriter, r *http.Request) {
	limit := 30
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		var err error
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 {
			writeError(w, http.StatusBadRequest, "invalid_video_qc", "limit 必须是正整数。")
			return
		}
	}
	checks, err := h.service.List(r.Context(), limit)
	if err != nil {
		h.writeError(w, err)
		return
	}
	if checks == nil {
		checks = []store.VideoQCCheck{}
	}
	writeJSON(w, http.StatusOK, checks)
}

func (h *videoQCHandler) get(w http.ResponseWriter, r *http.Request) {
	check, err := h.service.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeError(w, err)
		return
	}
	if check.Frames == nil {
		check.Frames = []videoqc.Frame{}
	}
	writeJSON(w, http.StatusOK, check)
}

func (h *videoQCHandler) frame(w http.ResponseWriter, r *http.Request) {
	index, err := strconv.Atoi(r.PathValue("n"))
	if err != nil || index < 1 {
		h.writeError(w, videoqc.ErrInvalidInput)
		return
	}
	file, err := h.service.OpenFrame(r.Context(), r.PathValue("id"), index)
	if err != nil {
		h.writeError(w, err)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		h.writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	http.ServeContent(w, r, "frame.jpg", info.ModTime(), file)
}

func (h *videoQCHandler) video(w http.ResponseWriter, r *http.Request) {
	file, err := h.service.OpenVideo(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeError(w, err)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		h.writeError(w, err)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
}

func (h *videoQCHandler) delete(w http.ResponseWriter, r *http.Request) {
	if err := h.service.Delete(r.Context(), r.PathValue("id")); err != nil {
		h.writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *videoQCHandler) writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, videoqc.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_video_qc", "请提供有效的视频审核参数。")
	case errors.Is(err, videoqc.ErrNotConfigured):
		writeError(w, http.StatusConflict, "video_qc_not_configured", "还没配置看图模型，请到设置里配置")
	case errors.Is(err, videoqc.ErrUnsupportedFormat):
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_video_format", "只支持 MP4、MOV、WebM 或 MKV 视频。")
	case errors.Is(err, videoqc.ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "video_qc_too_large", "视频不能超过 300 MB。")
	case errors.Is(err, videoqc.ErrUnsafePath):
		writeError(w, http.StatusForbidden, "unsafe_video_qc_path", "视频审核文件路径不安全。")
	case errors.Is(err, videoqc.ErrMediaNotReady), errors.Is(err, store.ErrReferenceNotFound):
		writeError(w, http.StatusBadRequest, "invalid_video_qc", "对标视频不存在或还没下载完成。")
	case errors.Is(err, videoqc.ErrUnsupportedSource):
		writeError(w, http.StatusBadRequest, "invalid_video_qc", "图生视频任务暂不支持直接审核，请先导出视频后上传。")
	case errors.Is(err, store.ErrVideoQCNotFound), errors.Is(err, videoqc.ErrMediaNotFound), errors.Is(err, os.ErrNotExist):
		writeError(w, http.StatusNotFound, "video_qc_not_found", "视频审核记录或文件不存在。")
	default:
		writeError(w, http.StatusInternalServerError, "video_qc_failed", "视频审核操作失败，请稍后重试。")
	}
}
