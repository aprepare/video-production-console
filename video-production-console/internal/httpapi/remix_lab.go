package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"video-production-console/internal/domain"
	"video-production-console/internal/remixlab"
	"video-production-console/internal/store"
)

// scenicProjectLookup is reserved for adopt (Task 6); Task 5 only accepts it for wiring.
type scenicProjectLookup interface {
	GetProject(context.Context, string) (domain.Project, error)
}

type remixLabHandler struct {
	svc      *remixlab.Service
	projects scenicProjectLookup
	prompts  remixlab.Store
}

func NewRemixLabHandler(svc *remixlab.Service, projects scenicProjectLookup) http.Handler {
	h := &remixLabHandler{svc: svc, projects: projects, prompts: remixlab.Store{DataRoot: svc.DataRoot()}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/remix-lab/defaults", h.defaults)
	mux.HandleFunc("GET /api/remix-lab/experiments", h.list)
	mux.HandleFunc("POST /api/remix-lab/experiments", h.create)
	mux.HandleFunc("GET /api/remix-lab/experiments/{id}", h.get)
	mux.HandleFunc("PATCH /api/remix-lab/runs/{id}", h.patchComment)
	mux.HandleFunc("POST /api/remix-lab/runs/{id}/adopt", h.adopt)
	mux.HandleFunc("GET /api/remix-lab/prompts", h.listPrompts)
	mux.HandleFunc("GET /api/remix-lab/active-prompt", h.getActive)
	mux.HandleFunc("PUT /api/remix-lab/active-prompt", h.putActive)
	return mux
}

func (h *remixLabHandler) defaults(w http.ResponseWriter, r *http.Request) {
	view, err := h.svc.Defaults(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "remix_lab_defaults_failed", "Remix lab defaults could not be loaded.")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *remixLabHandler) list(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.ListExperiments(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "remix_lab_list_failed", "Remix lab experiments could not be listed.")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type remixLabSlotPOST struct {
	BaseURL         string `json:"base_url"`
	Model           string `json:"model"`
	APIKey          string `json:"api_key"`
	ReasoningEffort string `json:"reasoning_effort"`
	RunCount        int    `json:"run_count"`
	PresetIndex     *int   `json:"preset_index"`
}

type remixLabCreatePOST struct {
	Source string              `json:"source"`
	Slots  []remixLabSlotPOST  `json:"slots"`
}

func (h *remixLabHandler) create(w http.ResponseWriter, r *http.Request) {
	var in remixLabCreatePOST
	if err := decodeJSON(w, r, maxMessageJSONRequest, &in); err != nil {
		writeDecodeError(w, err, "invalid_remix_lab", "A valid remix lab experiment is required.")
		return
	}
	slots := make([]remixlab.SlotInput, 0, len(in.Slots))
	for _, slot := range in.Slots {
		slots = append(slots, remixlab.SlotInput{
			BaseURL:         slot.BaseURL,
			Model:           slot.Model,
			APIKey:          slot.APIKey,
			ReasoningEffort: slot.ReasoningEffort,
			RunCount:        slot.RunCount,
			PresetIndex:     slot.PresetIndex,
		})
	}
	exp, err := h.svc.CreateExperiment(r.Context(), in.Source, slots)
	if err != nil {
		writeRemixLabError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, exp)
}

func (h *remixLabHandler) get(w http.ResponseWriter, r *http.Request) {
	exp, err := h.svc.GetExperiment(r.Context(), r.PathValue("id"))
	if err != nil {
		writeRemixLabError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, exp)
}

func (h *remixLabHandler) patchComment(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Comment string `json:"comment"`
	}
	if err := decodeJSON(w, r, maxMessageJSONRequest, &in); err != nil {
		writeDecodeError(w, err, "invalid_remix_lab", "A valid comment payload is required.")
		return
	}
	if err := h.svc.PatchComment(r.Context(), r.PathValue("id"), in.Comment); err != nil {
		writeRemixLabError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *remixLabHandler) adopt(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ProjectID string `json:"project_id"`
	}
	if err := decodeJSON(w, r, maxMessageJSONRequest, &in); err != nil {
		writeDecodeError(w, err, "invalid_remix_lab", "A valid adopt payload is required.")
		return
	}
	if err := h.svc.Adopt(r.Context(), r.PathValue("id"), in.ProjectID); err != nil {
		writeRemixLabError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func writeRemixLabError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrRemixLabNotFound):
		writeError(w, http.StatusNotFound, "remix_lab_not_found", "Remix lab record was not found.")
	case errors.Is(err, store.ErrProjectNotFound):
		writeError(w, http.StatusNotFound, "project_not_found", "The project was not found.")
	case errors.Is(err, remixlab.ErrMissingAPIKey):
		writeError(w, http.StatusBadRequest, "remix_key_missing", err.Error())
	case errors.Is(err, remixlab.ErrRunNotAdoptable):
		writeError(w, http.StatusBadRequest, "run_not_adoptable", err.Error())
	case errors.Is(err, remixlab.ErrAdoptUnavailable):
		writeError(w, http.StatusInternalServerError, "adopt_unavailable", err.Error())
	case errors.Is(err, remixlab.ErrInvalidSource),
		errors.Is(err, remixlab.ErrInvalidSlots),
		errors.Is(err, remixlab.ErrInvalidRunCount),
		errors.Is(err, remixlab.ErrMissingModel),
		errors.Is(err, remixlab.ErrInvalidComment):
		writeError(w, http.StatusBadRequest, "invalid_remix_lab", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "remix_lab_failed", "Remix lab request could not be completed.")
	}
}

const maxRemixLabRequestSize = 512 << 10

func (h *remixLabHandler) listPrompts(w http.ResponseWriter, _ *http.Request) {
	catalog := remixlab.Catalog()
	out := make([]remixlab.PromptTemplate, 0, len(catalog))
	for _, p := range catalog {
		out = append(out, remixlab.ResolvePrompt(p.ID, "", ""))
	}
	writeJSON(w, http.StatusOK, map[string]any{"prompts": out})
}

func (h *remixLabHandler) getActive(w http.ResponseWriter, _ *http.Request) {
	active, ok, err := h.prompts.GetActive()
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
		if err := h.prompts.SetActive(remixlab.ActivePrompt{}); err != nil {
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
	active, err := h.prompts.AdoptFromTemplate(id, body.System, body.User)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "active_prompt_save_failed", "保存系统提示词失败。")
		return
	}
	hasCustom := strings.TrimSpace(active.System) != "" || strings.TrimSpace(active.User) != ""
	writeJSON(w, http.StatusOK, map[string]any{"active": hasCustom, "prompt": active})
}
