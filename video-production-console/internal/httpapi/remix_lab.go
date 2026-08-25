package httpapi

import (
	"net/http"
	"strings"

	"video-production-console/internal/remixlab"
)

const maxRemixLabRequestSize = 512 << 10

type remixLabHandler struct {
	store remixlab.Store
}

// NewRemixLabHandler exposes the evolution-lab prompt catalog and adopt API.
func NewRemixLabHandler(dataRoot string) http.Handler {
	h := &remixLabHandler{store: remixlab.Store{DataRoot: dataRoot}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/remix-lab/prompts", h.listPrompts)
	mux.HandleFunc("GET /api/remix-lab/active-prompt", h.getActive)
	mux.HandleFunc("PUT /api/remix-lab/active-prompt", h.putActive)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, r)
	})
}

func (h *remixLabHandler) listPrompts(w http.ResponseWriter, _ *http.Request) {
	catalog := remixlab.Catalog()
	out := make([]remixlab.PromptTemplate, 0, len(catalog))
	for _, p := range catalog {
		out = append(out, remixlab.ResolvePrompt(p.ID, "", ""))
	}
	writeJSON(w, http.StatusOK, map[string]any{"prompts": out})
}

func (h *remixLabHandler) getActive(w http.ResponseWriter, _ *http.Request) {
	active, ok, err := h.store.GetActive()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "active_prompt_read_failed", "当前采用的提示词读取失败。")
		return
	}
	if !ok {
		base := remixlab.ResolvePrompt("elder_stable", "", "")
		writeJSON(w, http.StatusOK, map[string]any{
			"active": false,
			"prompt": remixlab.ActivePrompt{
				ID: base.ID, Name: base.Name, Stamp: base.Stamp, Style: base.Style,
			},
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"active": true, "prompt": active})
}

func (h *remixLabHandler) putActive(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID     string `json:"id"`
		System string `json:"system"`
		User   string `json:"user"`
		Clear  bool   `json:"clear"`
	}
	if err := decodeJSON(w, r, maxRemixLabRequestSize, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "请求格式无效。")
		return
	}
	if body.Clear || (strings.TrimSpace(body.ID) == "" && strings.TrimSpace(body.System) == "" && strings.TrimSpace(body.User) == "") {
		if err := h.store.SetActive(remixlab.ActivePrompt{}); err != nil {
			writeError(w, http.StatusInternalServerError, "active_prompt_clear_failed", "恢复默认提示词失败。")
			return
		}
		base := remixlab.ResolvePrompt("elder_stable", "", "")
		writeJSON(w, http.StatusOK, map[string]any{
			"active": false,
			"prompt": remixlab.ActivePrompt{
				ID: base.ID, Name: base.Name, Stamp: base.Stamp, Style: base.Style,
			},
		})
		return
	}
	id := strings.TrimSpace(body.ID)
	if id == "" {
		id = "elder_stable"
	}
	active, err := h.store.AdoptFromTemplate(id, body.System, body.User)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "active_prompt_save_failed", "保存系统提示词失败。")
		return
	}
	hasCustom := strings.TrimSpace(active.System) != "" || strings.TrimSpace(active.User) != ""
	writeJSON(w, http.StatusOK, map[string]any{"active": hasCustom, "prompt": active})
}
