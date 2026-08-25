package httpapi

import (
	"context"
	"errors"
	"net/http"

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
}

func NewRemixLabHandler(svc *remixlab.Service, projects scenicProjectLookup) http.Handler {
	h := &remixLabHandler{svc: svc, projects: projects}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/remix-lab/defaults", h.defaults)
	mux.HandleFunc("GET /api/remix-lab/experiments", h.list)
	mux.HandleFunc("POST /api/remix-lab/experiments", h.create)
	mux.HandleFunc("GET /api/remix-lab/experiments/{id}", h.get)
	mux.HandleFunc("PATCH /api/remix-lab/runs/{id}", h.patchComment)
	mux.HandleFunc("POST /api/remix-lab/runs/{id}/adopt", h.adopt)
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
