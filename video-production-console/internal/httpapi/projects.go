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
	"video-production-console/internal/taskmodel"
	"video-production-console/internal/workflow"
)

// RemixCoordinator is the narrow project-workflow dependency used by the HTTP API.
type RemixCoordinator interface {
	Start(context.Context, workflow.StartRemix) (domain.ProjectWorkflowRun, error)
}

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
	db         *sql.DB
	remix      RemixCoordinator
	models     TaskModelResolver
	workflows  *store.WorkflowRepository
	tasks      *store.TaskRepository
}

func NewProjectsHandler(db *sql.DB, service *assets.Service, remix RemixCoordinator, models TaskModelResolver) http.Handler {
	return newProjectsHandlerWithDB(store.NewProjectRepository(db), service, db, remix, models)
}
func newProjectsHandler(repository projectStore, service *assets.Service) http.Handler {
	return newProjectsHandlerWithDB(repository, service, nil, nil, nil)
}
func newProjectsHandlerWithDB(repository projectStore, service *assets.Service, db *sql.DB, remix RemixCoordinator, models TaskModelResolver) http.Handler {
	h := &projectsHandler{repository: repository, assets: service, db: db, remix: remix, models: models}
	if db != nil {
		h.workflows = store.NewWorkflowRepository(db)
		h.tasks = store.NewTaskRepository(db)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/projects", h.create)
	mux.HandleFunc("GET /api/projects", h.list)
	mux.HandleFunc("GET /api/projects/{id}", h.get)
	mux.HandleFunc("DELETE /api/projects/{id}", h.delete)
	mux.HandleFunc("POST /api/projects/{id}/assets/{type}", h.upload)
	mux.HandleFunc("POST /api/projects/{id}/move", h.move)
	mux.HandleFunc("POST /api/projects/{id}/remix", h.startRemix)
	mux.HandleFunc("POST /api/projects/{id}/publish", h.publish)
	return mux
}

func (h *projectsHandler) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := projectID(w, r.PathValue("id"))
	if !ok {
		return
	}
	deleter, ok := h.repository.(interface {
		DeleteProject(context.Context, string) error
	})
	if !ok {
		writeError(w, http.StatusNotImplemented, "project_delete_unavailable", "Project deletion is unavailable.")
		return
	}
	err := deleter.DeleteProject(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrProjectNotFound):
		writeError(w, http.StatusNotFound, "project_not_found", "The project was not found.")
		return
	case errors.Is(err, store.ErrProjectBusy):
		writeError(w, http.StatusConflict, "project_active_task", "Stop the active Codex task before deleting this project.")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "project_delete_failed", "Project could not be deleted.")
		return
	}
	if h.assets != nil {
		if err := h.assets.DeleteProjectData(id); err != nil {
			log.Printf("remove deleted project data %s: %v", id, err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

type projectView struct {
	ID                string               `json:"id"`
	AccountID         string               `json:"account_id"`
	Title             string               `json:"title"`
	Stage             domain.ProjectStage  `json:"stage"`
	CreatedAt         time.Time            `json:"created_at"`
	UpdatedAt         time.Time            `json:"updated_at"`
	ReadyAt           *time.Time           `json:"ready_at"`
	PublishedAt       *time.Time           `json:"published_at"`
	PublishNote       *string              `json:"publish_note"`
	PublicationStatus domain.ProjectStatus `json:"publication_status"`
}

type workflowView struct {
	ID              string               `json:"id"`
	ProjectID       string               `json:"project_id"`
	AccountID       string               `json:"account_id"`
	Kind            domain.WorkflowKind  `json:"kind"`
	State           domain.WorkflowState `json:"state"`
	CurrentStep     domain.WorkflowStep  `json:"current_step"`
	TopicTaskID     *string              `json:"topic_task_id,omitempty"`
	RemixTaskID     *string              `json:"remix_task_id,omitempty"`
	Model           string               `json:"model"`
	ReasoningEffort string               `json:"reasoning_effort"`
	CreatedAt       time.Time            `json:"created_at"`
	UpdatedAt       time.Time            `json:"updated_at"`
	CurrentTask     *taskView            `json:"current_task"`
}
type assetView struct {
	ID        string            `json:"id"`
	Type      domain.AssetType  `json:"type"`
	State     domain.AssetState `json:"state"`
	Filename  string            `json:"filename"`
	MIMEType  string            `json:"mime_type"`
	Size      int64             `json:"size"`
	SHA256    string            `json:"sha256"`
	Version   int               `json:"version"`
	CreatedAt time.Time         `json:"created_at"`
}

func (h *projectsHandler) create(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AccountID string `json:"account_id"`
		Title     string `json:"title"`
	}
	if err := decodeJSON(w, r, maxNormalJSONRequest, &in); err != nil {
		writeDecodeError(w, err, "invalid_json", "A JSON project is required.")
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
	p := domain.Project{ID: id, AccountID: accountID.String(), Title: title, Stage: domain.StageScript, Status: domain.ProjectDraft, CreatedAt: now, UpdatedAt: now}
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
	latest := map[string]domain.Asset{}
	for _, a := range all {
		v := toAssetView(a)
		history[string(a.Type)] = append(history[string(a.Type)], v)
		key := string(a.Type)
		if previous, ok := latest[key]; !ok ||
			a.Version > previous.Version ||
			a.Version == previous.Version && (a.CreatedAt.After(previous.CreatedAt) ||
				a.CreatedAt.Equal(previous.CreatedAt) && a.ID > previous.ID) {
			latest[key] = a
		}
	}
	for _, a := range latest {
		current[string(a.Type)] = toAssetView(a)
		if a.Status != string(domain.AssetReady) {
			continue
		}
		available[a.Type] = true
		if a.Type == domain.AssetNarration {
			available[domain.AssetAudio] = true
		}
		if a.Type == domain.AssetSubtitleSRT {
			available[domain.AssetSubtitle] = true
		}
	}
	var bg any = nil
	if background.ID != "" {
		backgroundAvailable := true
		if h.assets != nil {
			file, _, openErr := h.assets.OpenAsset(background)
			if openErr != nil {
				backgroundAvailable = false
				background.Status = "missing"
			} else {
				_ = file.Close()
			}
		}
		if backgroundAvailable {
			available[domain.AssetAccountBackground] = true
		}
		v := toAssetView(background)
		bg = v
		if background.Status != string(domain.AssetReady) || !backgroundAvailable {
			delete(available, domain.AssetAccountBackground)
		}
	}
	missing := missingForStage(p.Stage, available)
	var topicContext any = nil
	if h.db != nil {
		if selection, topicErr := findProjectTopicSelection(r.Context(), h.db, p); topicErr == nil {
			topicContext = selection.Candidate
		}
	}
	var activeWorkflow any = nil
	if h.workflows != nil {
		run, workflowErr := h.workflows.ActiveForProject(r.Context(), id, domain.WorkflowRemix)
		if workflowErr == nil {
			view, viewErr := h.toWorkflowView(r.Context(), run)
			if viewErr != nil {
				writeError(w, http.StatusInternalServerError, "project_workflow_failed", "Project workflow could not be read.")
				return
			}
			activeWorkflow = view
		} else if !errors.Is(workflowErr, store.ErrWorkflowNotFound) {
			writeError(w, http.StatusInternalServerError, "project_workflow_failed", "Project workflow could not be read.")
			return
		}
	}
	writeJSON(w, 200, map[string]any{"project": toProjectView(p), "assets": current, "asset_history": history, "background_reference": bg, "missing_assets": missing, "topic_context": topicContext, "active_workflow": activeWorkflow})
}

func (h *projectsHandler) startRemix(w http.ResponseWriter, r *http.Request) {
	id, ok := projectID(w, r.PathValue("id"))
	if !ok {
		return
	}
	var in taskModelRequest
	if err := decodeJSON(w, r, maxNormalJSONRequest, &in); err != nil && !errors.Is(err, io.EOF) {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "The request is too large.")
		} else {
			writeError(w, http.StatusBadRequest, "invalid_remix", "Only model and reasoning_effort are accepted.")
		}
		return
	}
	project, err := h.repository.GetProject(r.Context(), id)
	if errors.Is(err, store.ErrProjectNotFound) || errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "project_not_found", "The project was not found.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "project_read_failed", "Project could not be read.")
		return
	}
	if h.remix == nil {
		writeError(w, http.StatusServiceUnavailable, "project_remix_unavailable", "Project remix is unavailable.")
		return
	}
	selection, err := resolveTaskModel(r.Context(), h.models, taskmodel.Selection{Model: in.Model, ReasoningEffort: in.ReasoningEffort})
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_task_model", "Task model selection is invalid.")
		return
	}
	run, err := h.remix.Start(r.Context(), workflow.StartRemix{ProjectID: project.ID, AccountID: project.AccountID, ModelName: selection.Model, ReasoningEffort: selection.ReasoningEffort, Now: time.Now().UTC()})
	if errors.Is(err, store.ErrProjectNotFound) {
		writeError(w, http.StatusNotFound, "project_not_found", "The project was not found.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "project_remix_failed", "Project remix could not be started.")
		return
	}
	view, err := h.toWorkflowView(r.Context(), run)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "project_workflow_failed", "Project workflow could not be read.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflow": view, "current_task": view.CurrentTask})
}

func (h *projectsHandler) publish(w http.ResponseWriter, r *http.Request) {
	id, ok := projectID(w, r.PathValue("id"))
	if !ok {
		return
	}
	publisher, ok := h.repository.(interface {
		PublishProject(context.Context, string, time.Time) (domain.Project, error)
	})
	if !ok {
		writeError(w, http.StatusNotImplemented, "project_publish_unavailable", "Project publishing is unavailable.")
		return
	}
	project, err := publisher.PublishProject(r.Context(), id, time.Now().UTC())
	switch {
	case errors.Is(err, store.ErrProjectNotFound):
		writeError(w, http.StatusNotFound, "project_not_found", "The project was not found.")
	case errors.Is(err, store.ErrFinalVideoMissing):
		writeError(w, http.StatusConflict, "final_video_missing", "A ready final video is required before publishing.")
	case errors.Is(err, store.ErrProjectNotInReview):
		writeError(w, http.StatusConflict, "project_not_in_review", "Only a project in review can be published.")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "project_publish_failed", "Project could not be published.")
	default:
		writeJSON(w, http.StatusOK, toProjectView(project))
	}
}

func (h *projectsHandler) toWorkflowView(ctx context.Context, run domain.ProjectWorkflowRun) (workflowView, error) {
	view := workflowView{ID: run.ID, ProjectID: run.ProjectID, AccountID: run.AccountID, Kind: run.Kind, State: run.State, CurrentStep: run.CurrentStep, TopicTaskID: run.TopicTaskID, RemixTaskID: run.RemixTaskID, Model: run.ModelName, ReasoningEffort: run.ReasoningEffort, CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt}
	var taskID *string
	if run.CurrentStep == domain.WorkflowStepRemix {
		taskID = run.RemixTaskID
	} else {
		taskID = run.TopicTaskID
	}
	if taskID == nil || h.tasks == nil {
		return view, nil
	}
	task, err := h.tasks.Get(ctx, *taskID)
	if err != nil {
		return workflowView{}, err
	}
	taskSummary := viewTask(task)
	view.CurrentTask = &taskSummary
	return view, nil
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
		if state == store.CommitUnknown {
			writeError(w, http.StatusServiceUnavailable, "asset_commit_unknown", "The asset may have been recorded. Refresh before retrying.")
			return
		}
		writeError(w, 500, "asset_store_failed", "Asset could not be recorded.")
		return
	}
	if syncer, ok := h.repository.(interface {
		SyncStageFromAssets(context.Context, string, time.Time) (domain.Project, error)
	}); ok {
		if _, syncErr := syncer.SyncStageFromAssets(r.Context(), id, time.Now().UTC()); syncErr != nil {
			log.Printf("sync project stage after upload %s: %v", id, syncErr)
		}
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
	decodeErr := decodeJSON(w, r, maxNormalJSONRequest, &in)
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
		if a.Type == domain.AssetNarration {
			available[domain.AssetAudio] = true
		}
		if a.Type == domain.AssetSubtitleSRT {
			available[domain.AssetSubtitle] = true
		}
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

const (
	maxSmallJSONRequest   int64 = 16 << 10
	maxNormalJSONRequest  int64 = 64 << 10
	maxMessageJSONRequest int64 = 256 << 10
	maxTaskJSONRequest    int64 = 2 << 20
	maxBundleJSONRequest  int64 = 1 << 20
)

func decodeJSON(w http.ResponseWriter, r *http.Request, limit int64, out any) error {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	return ensureJSONEOF(d)
}

func ensureJSONEOF(d *json.Decoder) error {
	var extra any
	if err := d.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return err
	}
	return errors.New("request must contain one JSON value")
}

func writeDecodeError(w http.ResponseWriter, err error, code, message string) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "The request body is too large.")
		return
	}
	writeError(w, http.StatusBadRequest, code, message)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"code": code, "message": message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
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
	case domain.StageScript, domain.StageAssets, domain.StageMixing, domain.StageReview, domain.StagePublished, domain.StageArchived:
		return true
	}
	return false
}
func uploadableType(t domain.AssetType) bool {
	switch t {
	case domain.AssetSourceScript,
		domain.AssetContinuousScript,
		domain.AssetSpokenScript,
		domain.AssetNarration,
		domain.AssetSubtitleSRT,
		domain.AssetMixDraft,
		domain.AssetFinalVideo,
		// Legacy aliases retained for older clients/tests.
		domain.AssetAudio,
		domain.AssetSubtitle:
		return true
	}
	return false
}
func toProjectView(p domain.Project) projectView {
	status := p.Status
	if status == "" {
		switch p.Stage {
		case domain.StageMixing, domain.StageReview:
			status = domain.ProjectProducing
		case domain.StageReady:
			status = domain.ProjectReadyToPublish
		case domain.StagePublished:
			status = domain.ProjectPublished
		case domain.StageArchived:
			status = domain.ProjectArchived
		default:
			status = domain.ProjectDraft
		}
	}
	return projectView{ID: p.ID, AccountID: p.AccountID, Title: p.Title, Stage: p.Stage, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt, ReadyAt: p.ReadyAt, PublishedAt: p.PublishedAt, PublishNote: p.PublishNote, PublicationStatus: status}
}
func toAssetView(a domain.Asset) assetView {
	return assetView{ID: a.ID, Type: a.Type, State: domain.AssetState(a.Status), Filename: a.Filename, MIMEType: a.MIMEType, Size: a.Size, SHA256: a.SHA256, Version: a.Version, CreatedAt: a.CreatedAt}
}
func missingForStage(stage domain.ProjectStage, a map[domain.AssetType]bool) []string {
	var to domain.ProjectStage
	switch stage {
	case domain.StageAssets:
		to = domain.StageMixing
	case domain.StageReview:
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
