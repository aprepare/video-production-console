package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"video-production-console/internal/baokuan"
	"video-production-console/internal/codex"
)

type DependenciesHandler struct {
	Baokuan                    *baokuan.Client
	CodexBinary, MCPExecutable string
}

func NewDependenciesHandler(client *baokuan.Client, codexBinary, mcpExecutable string) http.Handler {
	return &DependenciesHandler{Baokuan: client, CodexBinary: codexBinary, MCPExecutable: mcpExecutable}
}
func (h *DependenciesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/dependencies" {
		h.dependencies(w, r)
		return
	}
	if r.URL.Path == "/api/dependencies/baokuan-mcp/configure" && r.Method == http.MethodPost {
		h.configure(w, r)
		return
	}
	h.library(w, r)
}
func writeDependencyJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func (h *DependenciesHandler) dependencies(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	status := map[string]any{}
	if h.Baokuan != nil {
		status["baokuan_http"] = h.Baokuan.Health(ctx)
	}
	status["baokuan_mcp"] = codex.CheckBaokuanMCP(ctx, h.CodexBinary)
	writeDependencyJSON(w, http.StatusOK, map[string]any{"dependencies": status})
}
func (h *DependenciesHandler) configure(w http.ResponseWriter, r *http.Request) {
	err := codex.ConfigureBaokuanMCP(r.Context(), h.CodexBinary, h.MCPExecutable, h.Baokuan.BaseURL)
	if err != nil {
		code := http.StatusBadGateway
		if strings.HasPrefix(err.Error(), "mcp_config_conflict") {
			code = http.StatusConflict
		}
		writeDependencyJSON(w, code, map[string]any{"code": strings.Split(err.Error(), ":")[0], "message": err.Error()})
		return
	}
	writeDependencyJSON(w, http.StatusOK, map[string]any{"status": "configured"})
}
func (h *DependenciesHandler) library(w http.ResponseWriter, r *http.Request) {
	if h.Baokuan == nil {
		writeDependencyJSON(w, 503, map[string]string{"code": "baokuan_unavailable", "message": "baokuan client is not configured"})
		return
	}
	switch {
	case r.URL.Path == "/api/library/materials/search" && r.Method == http.MethodGet:
		q := url.Values{}
		for k, v := range r.URL.Query() {
			if k == "q" || k == "sources" || k == "from" || k == "to" || k == "min_score" {
				q[k] = v
			}
		}
		q.Set("limit", stringLimit(r.URL.Query().Get("limit")))
		materials, err := h.Baokuan.SearchMaterials(r.Context(), q)
		if err != nil {
			writeDependencyJSON(w, 503, map[string]string{"code": "baokuan_offline", "message": err.Error()})
			return
		}
		writeDependencyJSON(w, 200, map[string]any{"materials": materials})
	case r.URL.Path == "/api/library/materials/bundle" && r.Method == http.MethodPost:
		var req baokuan.BundleRequest
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req) != nil {
			writeDependencyJSON(w, 400, map[string]string{"code": "invalid_json", "message": "invalid request"})
			return
		}
		if len(req.FeedIDs)+len(req.ObservationIDs)+len(req.SnippetIDs) > 20 {
			writeDependencyJSON(w, 400, map[string]string{"code": "limit_exceeded", "message": "bundle supports at most 20 items"})
			return
		}
		bundle, err := h.Baokuan.GetMaterialBundle(r.Context(), req)
		if err != nil {
			writeDependencyJSON(w, 503, map[string]string{"code": "baokuan_offline", "message": err.Error()})
			return
		}
		writeDependencyJSON(w, 200, bundle)
	default:
		http.NotFound(w, r)
	}
}
func stringLimit(raw string) string { n := baokuan.ParseLimit(raw); return fmtInt(n) }
func fmtInt(n int) string {
	if n < 1 {
		n = 1
	}
	if n > 20 {
		n = 20
	}
	return strconv.Itoa(n)
}
