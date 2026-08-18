package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"video-production-console/internal/domain"
	"video-production-console/internal/skillregistry"
)

type skillsAPI interface {
	List(context.Context) ([]domain.SkillSnapshot, error)
	Latest(context.Context, string) (domain.SkillSnapshot, error)
	ScanAll(context.Context) ([]domain.SkillSnapshot, error)
}

type skillsHandler struct{ service skillsAPI }

func NewSkillsHandler(service skillsAPI) http.Handler {
	handler := &skillsHandler{service: service}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/skills", handler.list)
	mux.HandleFunc("POST /api/skills/scan", handler.scan)
	mux.HandleFunc("GET /api/skills/{name}", handler.get)
	return mux
}

func (h *skillsHandler) list(response http.ResponseWriter, request *http.Request) {
	snapshots, err := h.service.List(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "skills_read_failed", "Skills could not be read.")
		return
	}
	writeJSON(response, http.StatusOK, snapshots)
}

func (h *skillsHandler) scan(response http.ResponseWriter, request *http.Request) {
	snapshots, err := h.service.ScanAll(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "skills_scan_failed", "Skills could not be scanned.")
		return
	}
	writeJSON(response, http.StatusOK, snapshots)
}

func (h *skillsHandler) get(response http.ResponseWriter, request *http.Request) {
	name := strings.TrimSpace(request.PathValue("name"))
	if name == "" || strings.Contains(name, "/") {
		writeError(response, http.StatusNotFound, "skill_not_found", "The Skill was not found.")
		return
	}
	snapshot, err := h.service.Latest(request.Context(), name)
	if errors.Is(err, skillregistry.ErrSkillNotFound) {
		writeError(response, http.StatusNotFound, "skill_not_found", "The Skill was not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "skill_read_failed", "The Skill could not be read.")
		return
	}
	writeJSON(response, http.StatusOK, snapshot)
}
