package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"video-production-console/internal/monitor"
	"video-production-console/internal/store"
)

type monitorsHandler struct{ service *monitor.Service }

func NewMonitorsHandler(service *monitor.Service) http.Handler {
	h := &monitorsHandler{service: service}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/monitors", h.list)
	mux.HandleFunc("POST /api/monitors", h.create)
	mux.HandleFunc("GET /api/monitors/feed", h.feed)
	mux.HandleFunc("GET /api/monitors/{id}", h.get)
	mux.HandleFunc("PATCH /api/monitors/{id}", h.patch)
	mux.HandleFunc("DELETE /api/monitors/{id}", h.delete)
	mux.HandleFunc("POST /api/monitors/{id}/run", h.run)
	mux.HandleFunc("GET /api/monitors/{id}/runs", h.runs)
	mux.HandleFunc("PATCH /api/monitors/items/{item_id}", h.patchItem)
	mux.HandleFunc("POST /api/monitors/items/read-all", h.readAll)
	mux.HandleFunc("POST /api/monitors/items/{item_id}/collect", h.collect)
	mux.HandleFunc("POST /api/monitors/items/{item_id}/score", h.score)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, r)
	})
}
func (h *monitorsHandler) error(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, monitor.ErrInvalid):
		writeError(w, 400, "invalid_monitor", err.Error())
	case errors.Is(err, store.ErrMonitorNotFound):
		writeError(w, 404, "monitor_not_found", "监控任务不存在")
	case errors.Is(err, store.ErrMonitorItemNotFound):
		writeError(w, 404, "monitor_item_not_found", "监控条目不存在")
	case errors.Is(err, store.ErrMonitorRunning):
		writeError(w, 409, "monitor_running", "这个任务正在运行")
	case errors.Is(err, monitor.ErrClosed):
		writeError(w, 503, "monitor_unavailable", "监控服务正在关闭")
	default:
		writeError(w, 500, "monitor_failed", "监控操作失败，请稍后重试")
	}
}
func (h *monitorsHandler) list(w http.ResponseWriter, r *http.Request) {
	out, err := h.service.List(r.Context())
	if err != nil {
		h.error(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func (h *monitorsHandler) create(w http.ResponseWriter, r *http.Request) {
	var input monitor.TaskPatch
	if err := decodeMonitorTaskJSON(w, r, &input); err != nil {
		writeDecodeError(w, err, "invalid_monitor", "监控任务格式不正确")
		return
	}
	task := monitor.DefaultTask()
	input.Apply(&task)
	out, err := h.service.Create(r.Context(), task)
	if err != nil {
		h.error(w, err)
		return
	}
	writeJSON(w, 201, out)
}
func (h *monitorsHandler) get(w http.ResponseWriter, r *http.Request) {
	out, err := h.service.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		h.error(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func (h *monitorsHandler) patch(w http.ResponseWriter, r *http.Request) {
	var input monitor.TaskPatch
	if err := decodeMonitorTaskJSON(w, r, &input); err != nil {
		writeDecodeError(w, err, "invalid_monitor", "监控修改格式不正确")
		return
	}
	out, err := h.service.Patch(r.Context(), r.PathValue("id"), input)
	if err != nil {
		h.error(w, err)
		return
	}
	writeJSON(w, 200, out)
}

func decodeMonitorTaskJSON(w http.ResponseWriter, r *http.Request, out any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxNormalJSONRequest))
	if err := decoder.Decode(out); err != nil {
		return err
	}
	return ensureJSONEOF(decoder)
}
func (h *monitorsHandler) delete(w http.ResponseWriter, r *http.Request) {
	if err := h.service.Delete(r.Context(), r.PathValue("id")); err != nil {
		h.error(w, err)
		return
	}
	w.WriteHeader(204)
}
func (h *monitorsHandler) run(w http.ResponseWriter, r *http.Request) {
	out, err := h.service.Start(r.Context(), r.PathValue("id"))
	if err != nil {
		h.error(w, err)
		return
	}
	writeJSON(w, 202, out)
}
func monitorLimit(r *http.Request, defaultValue int) (int, error) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return defaultValue, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, &monitor.ValidationError{Message: "limit 需要是正整数"}
	}
	return n, nil
}
func (h *monitorsHandler) runs(w http.ResponseWriter, r *http.Request) {
	limit, err := monitorLimit(r, 20)
	if err != nil {
		h.error(w, err)
		return
	}
	out, err := h.service.Runs(r.Context(), r.PathValue("id"), limit)
	if err != nil {
		h.error(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func (h *monitorsHandler) feed(w http.ResponseWriter, r *http.Request) {
	limit, err := monitorLimit(r, 100)
	if err != nil {
		h.error(w, err)
		return
	}
	out, err := h.service.Feed(r.Context(), strings.TrimSpace(r.URL.Query().Get("task_id")), r.URL.Query().Get("filter"), limit)
	if err != nil {
		h.error(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func (h *monitorsHandler) patchItem(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Read      *bool `json:"read"`
		Dismissed *bool `json:"dismissed"`
	}
	if err := decodeJSON(w, r, maxNormalJSONRequest, &input); err != nil {
		writeDecodeError(w, err, "invalid_monitor", "条目修改格式不正确")
		return
	}
	if input.Read == nil && input.Dismissed == nil {
		h.error(w, &monitor.ValidationError{Message: "请提供 read 或 dismissed 字段"})
		return
	}
	out, err := h.service.PatchItem(r.Context(), r.PathValue("item_id"), input.Read, input.Dismissed)
	if err != nil {
		h.error(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func (h *monitorsHandler) readAll(w http.ResponseWriter, r *http.Request) {
	var input struct {
		TaskID string `json:"task_id"`
	}
	if err := decodeJSON(w, r, maxNormalJSONRequest, &input); err != nil {
		writeDecodeError(w, err, "invalid_monitor", "已读请求格式不正确")
		return
	}
	if err := h.service.ReadAll(r.Context(), strings.TrimSpace(input.TaskID)); err != nil {
		h.error(w, err)
		return
	}
	w.WriteHeader(204)
}
func (h *monitorsHandler) collect(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Transcribe bool `json:"transcribe"`
	}
	if err := decodeJSON(w, r, maxNormalJSONRequest, &input); err != nil {
		writeDecodeError(w, err, "invalid_monitor", "采集请求格式不正确")
		return
	}
	out, err := h.service.Collect(r.Context(), r.PathValue("item_id"), input.Transcribe)
	if err != nil {
		h.error(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func (h *monitorsHandler) score(w http.ResponseWriter, r *http.Request) {
	out, err := h.service.Score(r.Context(), r.PathValue("item_id"))
	if err != nil {
		h.error(w, err)
		return
	}
	writeJSON(w, 200, out)
}
