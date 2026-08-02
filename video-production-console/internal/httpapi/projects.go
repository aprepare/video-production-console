package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"video-production-console/internal/assets"
	"video-production-console/internal/domain"
	"video-production-console/internal/store"
)

type projectStore interface {
	CreateProject(context.Context, domain.Project) error
	ListProjects(context.Context, string, domain.ProjectStage, string) ([]domain.Project, error)
	GetProject(context.Context, string) (domain.Project, error)
	MoveProject(context.Context, string, domain.ProjectStage, domain.ProjectStage, time.Time) (domain.Project, error)
	SetTopicCardPath(context.Context, string, string, time.Time) error
	AddAsset(context.Context, *domain.Asset) (store.CommitState, error)
	ListAssets(context.Context, string) ([]domain.Asset, error)
	Background(context.Context, string) (domain.Asset, error)
}
type projectsHandler struct {
	repository projectStore
	assets     *assets.Service
}

func NewProjectsHandler(db *sql.DB, service *assets.Service) http.Handler {
	return newProjectsHandler(store.NewProjectRepository(db), service)
}
func newProjectsHandler(repository projectStore, service *assets.Service) http.Handler {
	h := &projectsHandler{repository: repository, assets: service}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/projects", h.create)
	mux.HandleFunc("GET /api/projects", h.list)
	mux.HandleFunc("GET /api/projects/{id}", h.get)
	mux.HandleFunc("POST /api/projects/{id}/assets/{type}", h.upload)
	mux.HandleFunc("POST /api/projects/{id}/move", h.move)
	return mux
}

type projectView struct {
	ID            string              `json:"id"`
	AccountID     string              `json:"account_id"`
	Title         string              `json:"title"`
	Stage         domain.ProjectStage `json:"stage"`
	TopicCardPath *string             `json:"topic_card_path,omitempty"`
	CreatedAt     time.Time           `json:"created_at"`
	UpdatedAt     time.Time           `json:"updated_at"`
	ReadyAt       *time.Time          `json:"ready_at"`
	PublishedAt   *time.Time          `json:"published_at"`
	PublishNote   *string             `json:"publish_note"`
}
type assetView struct {
	ID        string           `json:"id"`
	Type      domain.AssetType `json:"type"`
	Path      string           `json:"path"`
	Filename  string           `json:"filename"`
	MIMEType  string           `json:"mime_type"`
	Size      int64            `json:"size"`
	SHA256    string           `json:"sha256"`
	Version   int              `json:"version"`
	CreatedAt time.Time        `json:"created_at"`
}

func (h *projectsHandler) create(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AccountID string `json:"account_id"`
		Title     string `json:"title"`
	}
	if decodeJSON(r, &in) != nil {
		writeError(w, 400, "invalid_json", "A JSON project is required.")
		return
	}
	accountID, err := uuid.Parse(in.AccountID)
	if err != nil {
		writeError(w, 400, "invalid_account_id", "Account ID must be a UUID.")
		return
	}
	id := uuid.NewString()
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = "Untitled-" + id[:8]
	}
	now := time.Now().UTC()
	p := domain.Project{ID: id, AccountID: accountID.String(), Title: title, Stage: domain.StageTopic, CreatedAt: now, UpdatedAt: now}
	err = h.repository.CreateProject(r.Context(), p)
	if errors.Is(err, store.ErrAccountInactive) {
		writeError(w, http.StatusConflict, "account_inactive", "An active account is required.")
		return
	}
	if err != nil {
		writeError(w, 500, "project_create_failed", "Project could not be created.")
		return
	}
	writeJSON(w, 201, toProjectView(p))
}
func (h *projectsHandler) list(w http.ResponseWriter, r *http.Request) {
	projects, err := h.repository.ListProjects(r.Context(), r.URL.Query().Get("account_id"), domain.ProjectStage(r.URL.Query().Get("stage")), strings.TrimSpace(r.URL.Query().Get("q")))
	if err != nil {
		writeError(w, 500, "projects_list_failed", "Projects could not be listed.")
		return
	}
	out := make([]projectView, 0, len(projects))
	for _, p := range projects {
		out = append(out, toProjectView(p))
	}
	writeJSON(w, 200, out)
}
func (h *projectsHandler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := projectID(w, r.PathValue("id"))
	if !ok {
		return
	}
	p, err := h.repository.GetProject(r.Context(), id)
	if errors.Is(err, store.ErrProjectNotFound) || errors.Is(err, sql.ErrNoRows) {
		writeError(w, 404, "project_not_found", "The project was not found.")
		return
	}
	if err != nil {
		writeError(w, 500, "project_read_failed", "Project could not be read.")
		return
	}
	if err != nil {
		writeError(w, 500, "project_read_failed", "Project could not be read.")
		return
	}
	all, err := h.repository.ListAssets(r.Context(), id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		writeError(w, 500, "project_assets_failed", "Project assets could not be read.")
		return
	}
	background, backgroundErr := h.repository.Background(r.Context(), id)
	if backgroundErr != nil && !errors.Is(backgroundErr, sql.ErrNoRows) {
		writeError(w, 500, "project_background_failed", "Project background could not be read.")
		return
	}
	current := map[string]assetView{}
	history := map[string][]assetView{}
	available := map[domain.AssetType]bool{}
	for _, a := range all {
		v := toAssetView(a)
		history[string(a.Type)] = append(history[string(a.Type)], v)
		if _, ok := current[string(a.Type)]; !ok {
			current[string(a.Type)] = v
		}
		available[a.Type] = true
	}
	var bg any = nil
	if background.ID != "" {
		available[domain.AssetAccountBackground] = true
		v := toAssetView(background)
		bg = v
	}
	missing := missingForStage(p.Stage, available)
	writeJSON(w, 200, map[string]any{"project": toProjectView(p), "assets": current, "asset_history": history, "background_reference": bg, "missing_assets": missing})
}

func (h *projectsHandler) upload(w http.ResponseWriter, r *http.Request) {
	id, ok := projectID(w, r.PathValue("id"))
	if !ok {
		return
	}
	if _, err := h.repository.GetProject(r.Context(), id); errors.Is(err, store.ErrProjectNotFound) || errors.Is(err, sql.ErrNoRows) {
		writeError(w, 404, "project_not_found", "The project was not found.")
		return
	} else if err != nil {
		writeError(w, 500, "project_read_failed", "Project could not be read.")
		return
	}
	typ := domain.AssetType(r.PathValue("type"))
	if !uploadableType(typ) {
		writeError(w, 400, "invalid_asset_type", "The asset type is not uploadable.")
		return
	}
	maxSize := assets.MaxSizeForType(typ)
	r.Body = http.MaxBytesReader(w, r.Body, maxSize+(1<<20))
	if err := r.ParseMultipartForm(2 << 20); err != nil {
		var large *http.MaxBytesError
		if errors.As(err, &large) {
			writeError(w, 413, "payload_too_large", "The upload is too large.")
		} else {
			writeError(w, 400, "invalid_multipart", "The multipart form could not be read.")
		}
		return
	}
	defer r.MultipartForm.RemoveAll()
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, 400, "file_required", "An asset file is required.")
		return
	}
	defer file.Close()
	saved, err := h.assets.SaveProjectAsset(id, typ, header.Filename, file)
	if err != nil {
		if errors.Is(err, assets.ErrProjectAssetTooBig) {
			writeError(w, 413, "payload_too_large", "The upload is too large.")
		} else if errors.Is(err, assets.ErrInvalidProjectAsset) {
			writeError(w, 400, "invalid_asset", "File extension and actual content must match the asset type.")
		} else {
			writeError(w, 500, "asset_save_failed", "Asset could not be saved.")
		}
		return
	}
	now := time.Now().UTC()
	a := domain.Asset{ID: uuid.NewString(), ProjectID: &id, Type: typ, Path: saved.Path, Filename: safeFilename(header.Filename), MIMEType: saved.MIMEType, Size: saved.Size, SHA256: saved.SHA256, Status: "active", CreatedAt: now}
	state, err := h.repository.AddAsset(r.Context(), &a)
	if err != nil {
		if state == store.CommitNotCommitted {
			if removeErr := os.Remove(saved.Path); removeErr != nil {
				log.Printf("remove uncommitted project asset: %v", removeErr)
			}
		}
		writeError(w, 500, "asset_store_failed", "Asset could not be recorded.")
		return
	}
	writeJSON(w, 201, toAssetView(a))
}

func (h *projectsHandler) move(w http.ResponseWriter, r *http.Request) {
	id, ok := projectID(w, r.PathValue("id"))
	if !ok {
		return
	}
	var in struct {
		Stage domain.ProjectStage `json:"stage"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	decodeErr := decodeJSON(r, &in)
	var tooLarge *http.MaxBytesError
	if errors.As(decodeErr, &tooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "The request is too large.")
		return
	}
	if decodeErr != nil || !validStage(in.Stage) {
		writeError(w, 400, "invalid_stage", "A valid target stage is required.")
		return
	}
	p, err := h.repository.GetProject(r.Context(), id)
	if errors.Is(err, store.ErrProjectNotFound) || errors.Is(err, sql.ErrNoRows) {
		writeError(w, 404, "project_not_found", "The project was not found.")
		return
	}
	if err != nil {
		writeError(w, 500, "project_read_failed", "Project could not be read.")
		return
	}
	all, err := h.repository.ListAssets(r.Context(), id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		writeError(w, 500, "project_assets_failed", "Project assets could not be read.")
		return
	}
	available := map[domain.AssetType]bool{}
	for _, a := range all {
		available[a.Type] = true
	}
	bg, err := h.repository.Background(r.Context(), id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		writeError(w, 500, "project_background_failed", "Project background could not be read.")
		return
	}
	if bg.ID != "" {
		available[domain.AssetAccountBackground] = true
	}
	if err := domain.CanMove(p.Stage, in.Stage, available); err != nil {
		var missing *domain.MissingAssetsError
		if errors.As(err, &missing) {
			items := make([]string, len(missing.Missing))
			for i, v := range missing.Missing {
				items[i] = string(v)
			}
			writeJSON(w, 409, map[string]any{"code": "missing_assets", "message": "Required assets are missing.", "details": map[string]any{"missing_assets": items}})
		} else {
			writeError(w, 409, "stage_move_conflict", err.Error())
		}
		return
	}
	p, err = h.repository.MoveProject(r.Context(), id, p.Stage, in.Stage, time.Now().UTC())
	if errors.Is(err, store.ErrProjectNotFound) {
		writeError(w, http.StatusNotFound, "project_not_found", "The project was not found.")
		return
	}
	if errors.Is(err, store.ErrProjectStageConflict) {
		writeError(w, http.StatusConflict, "project_stage_conflict", "Project stage changed; refresh and retry.")
		return
	}
	if err != nil {
		writeError(w, 500, "stage_move_failed", "Project stage could not be updated.")
		return
	}
	writeJSON(w, 200, toProjectView(p))
}

func decodeJSON(r *http.Request, out any) error {
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return errors.New("request must contain one JSON value")
	}
	return nil
}
func projectID(w http.ResponseWriter, value string) (string, bool) {
	id, err := uuid.Parse(value)
	if err != nil {
		writeError(w, 400, "invalid_project_id", "Project ID must be a UUID.")
		return "", false
	}
	return id.String(), true
}
func validStage(s domain.ProjectStage) bool {
	switch s {
	case domain.StageTopic, domain.StageScript, domain.StageAssets, domain.StageMixing, domain.StageReview, domain.StageReady, domain.StagePublished, domain.StageArchived:
		return true
	}
	return false
}
func uploadableType(t domain.AssetType) bool {
	switch t {
	case domain.AssetContinuousScript, domain.AssetSpokenScript, domain.AssetAudio, domain.AssetSubtitle, domain.AssetMixDraft, domain.AssetFinalVideo:
		return true
	}
	return false
}
func toProjectView(p domain.Project) projectView {
	return projectView{ID: p.ID, AccountID: p.AccountID, Title: p.Title, Stage: p.Stage, TopicCardPath: p.TopicCardPath, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt, ReadyAt: p.ReadyAt, PublishedAt: p.PublishedAt, PublishNote: p.PublishNote}
}
func toAssetView(a domain.Asset) assetView {
	return assetView{a.ID, a.Type, a.Path, a.Filename, a.MIMEType, a.Size, a.SHA256, a.Version, a.CreatedAt}
}
func missingForStage(stage domain.ProjectStage, a map[domain.AssetType]bool) []string {
	var to domain.ProjectStage
	switch stage {
	case domain.StageAssets:
		to = domain.StageMixing
	case domain.StageReview:
		to = domain.StageReady
	case domain.StageReady:
		to = domain.StagePublished
	default:
		return []string{}
	}
	err := domain.CanMove(stage, to, a)
	var m *domain.MissingAssetsError
	if errors.As(err, &m) {
		out := make([]string, len(m.Missing))
		for i, v := range m.Missing {
			out[i] = string(v)
		}
		return out
	}
	return []string{}
}
