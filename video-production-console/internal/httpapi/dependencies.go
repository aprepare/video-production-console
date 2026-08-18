package httpapi

import (
	"context"
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
func (h *DependenciesHandler) dependencies(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	status := map[string]any{}
	if h.Baokuan != nil {
		status["baokuan_http"] = h.Baokuan.Health(ctx)
	}
	status["baokuan_mcp"] = codex.CheckBaokuanMCP(ctx, h.CodexBinary)
	writeJSON(w, http.StatusOK, map[string]any{"dependencies": status})
}
func (h *DependenciesHandler) configure(w http.ResponseWriter, r *http.Request) {
	err := codex.ConfigureBaokuanMCP(r.Context(), h.CodexBinary, h.MCPExecutable, h.Baokuan.BaseURL)
	if err != nil {
		code := http.StatusBadGateway
		if strings.HasPrefix(err.Error(), "mcp_config_conflict") {
			code = http.StatusConflict
		}
		writeError(w, code, strings.Split(err.Error(), ":")[0], "The dependency could not be configured.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "configured"})
}
func (h *DependenciesHandler) library(w http.ResponseWriter, r *http.Request) {
	if h.Baokuan == nil {
		writeError(w, http.StatusServiceUnavailable, "baokuan_unavailable", "Baokuan is not configured.")
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
			writeError(w, http.StatusServiceUnavailable, "baokuan_offline", "Baokuan is unavailable.")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"materials": materials})
	case r.URL.Path == "/api/library/materials/bundle" && r.Method == http.MethodPost:
		var req baokuan.BundleRequest
		if err := decodeJSON(w, r, maxBundleJSONRequest, &req); err != nil {
			writeDecodeError(w, err, "invalid_json", "The request is invalid.")
			return
		}
		if len(req.FeedIDs)+len(req.ObservationIDs)+len(req.SnippetIDs) > 20 {
			writeError(w, http.StatusBadRequest, "limit_exceeded", "Bundle supports at most 20 items.")
			return
		}
		bundle, err := h.Baokuan.GetMaterialBundle(r.Context(), req)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "baokuan_offline", "Baokuan is unavailable.")
			return
		}
		writeJSON(w, http.StatusOK, bundle)
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
