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
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"video-production-console/internal/replicate"
	"video-production-console/internal/videoqc"
)

const (
	maxReplicateJSONBytes     = 64 << 10
	maxFirstFrameUploadBytes  = 20 << 20
	maxFirstFrameRequestBytes = maxFirstFrameUploadBytes + (64 << 10)
)

var errReplicateJSONTooLarge = errors.New("replicate JSON is too large")

type replicateAPI interface {
	Create(context.Context, replicate.CreateRequest) (replicate.Project, error)
	Get(context.Context, string) (replicate.Project, error)
	List(context.Context, int) ([]replicate.Project, error)
	Patch(context.Context, string, replicate.ProjectPatch) (replicate.Project, error)
	Delete(context.Context, string) error
	AddShot(context.Context, string, replicate.ShotPatch) (replicate.Project, error)
	PatchShot(context.Context, string, replicate.ShotPatch) (replicate.Project, error)
	DeleteShot(context.Context, string) error
	Reorder(context.Context, string, []string) (replicate.Project, error)
	GenerateFirstFrames(context.Context, string, []string) (replicate.Project, error)
	UploadFirstFrame(context.Context, string, string, io.Reader) (replicate.Project, error)
	UseReferenceFrame(context.Context, string, int) (replicate.Project, error)
	OpenFirstFrame(context.Context, string) (*os.File, error)
	Generate(context.Context, string, replicate.GenerateRequest) (replicate.GenerateResponse, error)
	SelectTake(context.Context, string) (replicate.Project, error)
	CancelTake(context.Context, string) (replicate.Project, error)
	DeleteTake(context.Context, string) error
	OpenTake(context.Context, string) (*os.File, error)
	Stitch(context.Context, string, string) (replicate.Project, error)
	OpenFinal(context.Context, string) (*os.File, error)
	StartQC(context.Context, string, string) (videoqc.Check, error)
	Providers(context.Context) (replicate.ProvidersResponse, error)
}

type replicateHandler struct{ service replicateAPI }

// replicateListProject suppresses the embedded project's shots field for the
// collection endpoint while the detail endpoints keep a stable shots array.
type replicateListProject struct {
	replicate.Project
	Shots any `json:"shots,omitempty"`
}

func NewReplicateHandler(service *replicate.Service) http.Handler {
	return newReplicateHandler(service)
}

func newReplicateHandler(service replicateAPI) http.Handler {
	h := &replicateHandler{service: service}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/replicate/providers", h.providers)
	mux.HandleFunc("POST /api/replicate", h.create)
	mux.HandleFunc("GET /api/replicate", h.list)
	mux.HandleFunc("GET /api/replicate/{id}", h.get)
	mux.HandleFunc("PATCH /api/replicate/{id}", h.patch)
	mux.HandleFunc("DELETE /api/replicate/{id}", h.delete)
	mux.HandleFunc("POST /api/replicate/{id}/shots", h.addShot)
	mux.HandleFunc("PATCH /api/replicate/shots/{shot_id}", h.patchShot)
	mux.HandleFunc("DELETE /api/replicate/shots/{shot_id}", h.deleteShot)
	mux.HandleFunc("POST /api/replicate/{id}/shots/reorder", h.reorder)
	mux.HandleFunc("POST /api/replicate/{id}/first-frames", h.firstFrames)
	mux.HandleFunc("POST /api/replicate/shots/{shot_id}/first-frame", h.uploadFirstFrame)
	mux.HandleFunc("POST /api/replicate/shots/{shot_id}/first-frame/reference", h.referenceFrame)
	mux.HandleFunc("GET /api/replicate/shots/{shot_id}/first-frame", h.openFirstFrame)
	mux.HandleFunc("POST /api/replicate/{id}/generate", h.generate)
	mux.HandleFunc("POST /api/replicate/takes/{take_id}/select", h.selectTake)
	mux.HandleFunc("POST /api/replicate/takes/{take_id}/cancel", h.cancelTake)
	mux.HandleFunc("DELETE /api/replicate/takes/{take_id}", h.deleteTake)
	mux.HandleFunc("GET /api/replicate/takes/{take_id}/video", h.openTake)
	mux.HandleFunc("POST /api/replicate/{id}/stitch", h.stitch)
	mux.HandleFunc("GET /api/replicate/{id}/final", h.openFinal)
	mux.HandleFunc("POST /api/replicate/{id}/qc", h.startQC)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if unsafeReplicatePath(r.URL) {
			writeError(w, http.StatusForbidden, "unsafe_replicate_path", "复刻项目文件路径不安全。")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func unsafeReplicatePath(u *url.URL) bool {
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

func decodeReplicateJSON(w http.ResponseWriter, r *http.Request, out any, optional bool) error {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxReplicateJSONBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return errReplicateJSONTooLarge
		}
		return err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		if optional {
			return nil
		}
		return io.ErrUnexpectedEOF
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

func (h *replicateHandler) decode(w http.ResponseWriter, r *http.Request, out any, optional bool) bool {
	if err := decodeReplicateJSON(w, r, out, optional); err != nil {
		if errors.Is(err, errReplicateJSONTooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "replicate_too_large", "请求内容过大。")
		} else {
			writeError(w, http.StatusBadRequest, "invalid_replicate", "请提供合法的 UTF-8 JSON 复刻参数。")
		}
		return false
	}
	return true
}

func normalizeReplicateProject(project *replicate.Project) {
	if project.Shots == nil {
		project.Shots = []replicate.Shot{}
	}
	for i := range project.Shots {
		if project.Shots[i].Takes == nil {
			project.Shots[i].Takes = []replicate.Take{}
		}
	}
}

func (h *replicateHandler) providers(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.Providers(r.Context())
	if err != nil {
		h.writeError(w, err)
		return
	}
	if result.Providers == nil {
		result.Providers = []replicate.Provider{}
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *replicateHandler) create(w http.ResponseWriter, r *http.Request) {
	var request replicate.CreateRequest
	if !h.decode(w, r, &request, false) {
		return
	}
	project, err := h.service.Create(r.Context(), request)
	if err != nil {
		h.writeError(w, err)
		return
	}
	normalizeReplicateProject(&project)
	writeJSON(w, http.StatusCreated, project)
}

func (h *replicateHandler) list(w http.ResponseWriter, r *http.Request) {
	limit := 30
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			writeError(w, http.StatusBadRequest, "invalid_replicate", "limit 必须是 1 到 100 的整数。")
			return
		}
		limit = parsed
	}
	projects, err := h.service.List(r.Context(), limit)
	if err != nil {
		h.writeError(w, err)
		return
	}
	if projects == nil {
		projects = []replicate.Project{}
	}
	items := make([]replicateListProject, len(projects))
	for i := range projects {
		projects[i].Shots = nil
		items[i] = replicateListProject{Project: projects[i]}
	}
	writeJSON(w, http.StatusOK, items)
}

func (h *replicateHandler) get(w http.ResponseWriter, r *http.Request) {
	project, err := h.service.Get(r.Context(), r.PathValue("id"))
	h.writeProject(w, r, http.StatusOK, project, err)
}

func (h *replicateHandler) patch(w http.ResponseWriter, r *http.Request) {
	var request replicate.ProjectPatch
	if !h.decode(w, r, &request, false) {
		return
	}
	project, err := h.service.Patch(r.Context(), r.PathValue("id"), request)
	h.writeProject(w, r, http.StatusOK, project, err)
}

func (h *replicateHandler) delete(w http.ResponseWriter, r *http.Request) {
	if err := h.service.Delete(r.Context(), r.PathValue("id")); err != nil {
		h.writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *replicateHandler) addShot(w http.ResponseWriter, r *http.Request) {
	var request replicate.ShotPatch
	if !h.decode(w, r, &request, false) {
		return
	}
	project, err := h.service.AddShot(r.Context(), r.PathValue("id"), request)
	h.writeProject(w, r, http.StatusOK, project, err)
}

func (h *replicateHandler) patchShot(w http.ResponseWriter, r *http.Request) {
	var request replicate.ShotPatch
	if !h.decode(w, r, &request, false) {
		return
	}
	project, err := h.service.PatchShot(r.Context(), r.PathValue("shot_id"), request)
	h.writeProject(w, r, http.StatusOK, project, err)
}

func (h *replicateHandler) deleteShot(w http.ResponseWriter, r *http.Request) {
	if err := h.service.DeleteShot(r.Context(), r.PathValue("shot_id")); err != nil {
		h.writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *replicateHandler) reorder(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ShotIDs []string `json:"shot_ids"`
	}
	if !h.decode(w, r, &request, false) {
		return
	}
	project, err := h.service.Reorder(r.Context(), r.PathValue("id"), request.ShotIDs)
	h.writeProject(w, r, http.StatusOK, project, err)
}

func (h *replicateHandler) firstFrames(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ShotIDs []string `json:"shot_ids"`
	}
	if !h.decode(w, r, &request, true) {
		return
	}
	project, err := h.service.GenerateFirstFrames(r.Context(), r.PathValue("id"), request.ShotIDs)
	h.writeProject(w, r, http.StatusAccepted, project, err)
}

func (h *replicateHandler) uploadFirstFrame(w http.ResponseWriter, r *http.Request) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_first_frame", "请上传 PNG、JPEG 或 WebP 图片。")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFirstFrameRequestBytes)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "replicate_too_large", "首帧图片不能超过 20 MB。")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_replicate", "无法读取首帧上传表单。")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_replicate", "请选择首帧图片。")
		return
	}
	defer file.Close()
	if header.Size > maxFirstFrameUploadBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "replicate_too_large", "首帧图片不能超过 20 MB。")
		return
	}
	if !utf8.ValidString(header.Filename) {
		writeError(w, http.StatusBadRequest, "invalid_replicate", "文件名必须是合法的 UTF-8 文本。")
		return
	}
	project, err := h.service.UploadFirstFrame(r.Context(), r.PathValue("shot_id"), header.Filename, file)
	h.writeProject(w, r, http.StatusOK, project, err)
}

func (h *replicateHandler) referenceFrame(w http.ResponseWriter, r *http.Request) {
	var request struct {
		FrameIndex int `json:"frame_index"`
	}
	if !h.decode(w, r, &request, false) {
		return
	}
	project, err := h.service.UseReferenceFrame(r.Context(), r.PathValue("shot_id"), request.FrameIndex)
	h.writeProject(w, r, http.StatusOK, project, err)
}

func (h *replicateHandler) openFirstFrame(w http.ResponseWriter, r *http.Request) {
	file, err := h.service.OpenFirstFrame(r.Context(), r.PathValue("shot_id"))
	h.serveFile(w, r, file, err, "", false)
}

func (h *replicateHandler) generate(w http.ResponseWriter, r *http.Request) {
	var request replicate.GenerateRequest
	if !h.decode(w, r, &request, true) {
		return
	}
	response, err := h.service.Generate(r.Context(), r.PathValue("id"), request)
	if err != nil {
		h.writeError(w, err)
		return
	}
	normalizeReplicateProject(&response.Project)
	if response.Skipped == nil {
		response.Skipped = []replicate.Skipped{}
	}
	writeJSON(w, http.StatusAccepted, response)
}

func (h *replicateHandler) selectTake(w http.ResponseWriter, r *http.Request) {
	project, err := h.service.SelectTake(r.Context(), r.PathValue("take_id"))
	h.writeProject(w, r, http.StatusOK, project, err)
}

func (h *replicateHandler) cancelTake(w http.ResponseWriter, r *http.Request) {
	project, err := h.service.CancelTake(r.Context(), r.PathValue("take_id"))
	h.writeProject(w, r, http.StatusOK, project, err)
}

func (h *replicateHandler) deleteTake(w http.ResponseWriter, r *http.Request) {
	if err := h.service.DeleteTake(r.Context(), r.PathValue("take_id")); err != nil {
		h.writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *replicateHandler) openTake(w http.ResponseWriter, r *http.Request) {
	file, err := h.service.OpenTake(r.Context(), r.PathValue("take_id"))
	h.serveFile(w, r, file, err, "video/mp4", false)
}

func (h *replicateHandler) stitch(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Audio string `json:"audio"`
	}
	if !h.decode(w, r, &request, true) {
		return
	}
	project, err := h.service.Stitch(r.Context(), r.PathValue("id"), request.Audio)
	h.writeProject(w, r, http.StatusAccepted, project, err)
}

func (h *replicateHandler) openFinal(w http.ResponseWriter, r *http.Request) {
	file, err := h.service.OpenFinal(r.Context(), r.PathValue("id"))
	h.serveFile(w, r, file, err, "video/mp4", r.URL.Query().Get("download") == "1")
}

func (h *replicateHandler) startQC(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Model string `json:"model"`
	}
	if !h.decode(w, r, &request, true) {
		return
	}
	check, err := h.service.StartQC(r.Context(), r.PathValue("id"), request.Model)
	if err != nil {
		h.writeError(w, err)
		return
	}
	if check.Frames == nil {
		check.Frames = []videoqc.Frame{}
	}
	writeJSON(w, http.StatusAccepted, check)
}

func (h *replicateHandler) writeProject(w http.ResponseWriter, _ *http.Request, status int, project replicate.Project, err error) {
	if err != nil {
		h.writeError(w, err)
		return
	}
	normalizeReplicateProject(&project)
	writeJSON(w, status, project)
}

func (h *replicateHandler) serveFile(w http.ResponseWriter, r *http.Request, file *os.File, err error, contentType string, download bool) {
	if err != nil {
		h.writeError(w, err)
		return
	}
	if file == nil {
		h.writeError(w, os.ErrNotExist)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		h.writeError(w, err)
		return
	}
	if contentType == "" {
		contentType = mime.TypeByExtension(strings.ToLower(filepath.Ext(info.Name())))
	}
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if download {
		w.Header().Set("Content-Disposition", `attachment; filename="replicate.mp4"; filename*=UTF-8''`+url.PathEscape("复刻成片.mp4"))
	}
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
}

func (h *replicateHandler) writeError(w http.ResponseWriter, err error) {
	var apiError *replicate.Error
	if !errors.As(err, &apiError) {
		writeError(w, http.StatusInternalServerError, "replicate_failed", "复刻操作失败，请稍后重试。")
		return
	}
	status := http.StatusInternalServerError
	switch apiError.Code {
	case "replicate_not_found":
		status = http.StatusNotFound
	case "invalid_replicate", "analysis_has_no_prompts", "stitch_missing_takes":
		status = http.StatusBadRequest
	case "video_provider_not_configured", "replicate_conflict":
		status = http.StatusConflict
	case "replicate_too_large", "first_frame_too_large":
		status = http.StatusRequestEntityTooLarge
	case "unsupported_first_frame":
		status = http.StatusUnsupportedMediaType
	case "unsafe_replicate_path":
		status = http.StatusForbidden
	case "upstream_failed", "runninghub_failed", "grok_failed":
		status = http.StatusBadGateway
	}
	code := apiError.Code
	if code == "" {
		code = "replicate_failed"
	}
	message := apiError.Message
	if message == "" {
		message = "复刻操作失败，请稍后重试。"
	}
	writeError(w, status, code, message)
}
