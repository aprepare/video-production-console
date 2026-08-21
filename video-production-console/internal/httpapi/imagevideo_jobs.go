package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"video-production-console/internal/imagevideo"
	"video-production-console/internal/store"
)

type imageVideoJobStarter interface {
	Kick(context.Context, string) error
}

type imageVideoJobsHandler struct {
	repo    *store.ImageVideoJobRepository
	starter imageVideoJobStarter
	db      *sql.DB
	runtime imageRuntimeProvider
	service *imagevideo.Service
}

func NewImageVideoJobsHandler(db *sql.DB, runtime imageRuntimeProvider, starter imageVideoJobStarter, services ...*imagevideo.Service) http.Handler {
	var service *imagevideo.Service
	if len(services) > 0 {
		service = services[0]
	}
	h := &imageVideoJobsHandler{repo: store.NewImageVideoJobRepository(db), starter: starter, db: db, runtime: runtime, service: service}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/image-projects/{id}/image-video-jobs", h.create)
	mux.HandleFunc("GET /api/image-video-jobs/{id}", h.get)
	mux.HandleFunc("POST /api/image-video-jobs/{id}/retry-failed", h.retryFailed)
	mux.HandleFunc("POST /api/image-video-jobs/{id}/cancel", h.cancel)
	mux.HandleFunc("POST /api/image-video-jobs/{id}/retry-registration", h.retryRegistration)
	return mux
}

type createImageVideoJobRequest struct {
	OutputMode     imagevideo.OutputMode `json:"output_mode"`
	AccountID      string                `json:"account_id"`
	IdempotencyKey string                `json:"idempotency_key"`
}

func (h *imageVideoJobsHandler) create(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.repo == nil {
		writeError(w, http.StatusServiceUnavailable, "image_video_service_unavailable", "Image video jobs are unavailable.")
		return
	}
	var input createImageVideoJobRequest
	if err := decodeJSON(w, r, maxTaskJSONRequest, &input); err != nil {
		writeDecodeError(w, err, "invalid_image_video_job", "A valid image video job is required.")
		return
	}
	if input.OutputMode == "" {
		input.OutputMode = imagevideo.ModeSlideshow
	}
	if strings.TrimSpace(input.AccountID) == "" || strings.TrimSpace(input.IdempotencyKey) == "" || h.service == nil {
		writeError(w, http.StatusBadRequest, "invalid_image_video_job", "Output mode, account, and idempotency key are required.")
		return
	}
	result, err := h.service.Start(r.Context(), imagevideo.StartRequest{ProjectID: r.PathValue("id"), AccountID: input.AccountID, OutputMode: input.OutputMode, IdempotencyKey: input.IdempotencyKey})
	if err != nil {
		writeImageVideoRepoError(w, err)
		return
	}
	job := result.Job
	writeJSON(w, http.StatusAccepted, map[string]any{"job_id": job.ID, "created": result.Created, "output_mode": job.OutputMode, "status": job.Status, "template_version": job.TemplateVersion})
}

func (h *imageVideoJobsHandler) get(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.repo == nil {
		writeError(w, http.StatusServiceUnavailable, "image_video_service_unavailable", "Image video jobs are unavailable.")
		return
	}
	detail, err := h.repo.GetDetail(r.Context(), r.PathValue("id"))
	if err != nil {
		writeImageVideoRepoError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toPublicImageVideoDetail(detail))
}

func (h *imageVideoJobsHandler) retryFailed(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.repo == nil {
		writeError(w, http.StatusServiceUnavailable, "image_video_service_unavailable", "Image video jobs are unavailable.")
		return
	}
	ids, err := h.repo.StartRetryRound(r.Context(), r.PathValue("id"), time.Now().UTC())
	if err != nil {
		writeImageVideoRepoError(w, err)
		return
	}
	if h.starter != nil {
		if err := h.starter.Kick(r.Context(), r.PathValue("id")); err != nil {
			writeError(w, http.StatusInternalServerError, "image_video_worker_unavailable", "The image video worker could not be started.")
			return
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"job_id": r.PathValue("id"), "item_ids": ids})
}

func (h *imageVideoJobsHandler) cancel(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.repo == nil {
		writeError(w, http.StatusServiceUnavailable, "image_video_service_unavailable", "Image video jobs are unavailable.")
		return
	}
	if err := h.repo.Cancel(r.Context(), r.PathValue("id"), time.Now().UTC()); err != nil {
		writeImageVideoRepoError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"job_id": r.PathValue("id"), "status": string(imagevideo.JobCanceled)})
}

func (h *imageVideoJobsHandler) retryRegistration(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		writeError(w, http.StatusServiceUnavailable, "image_video_registration_unavailable", "Draft registration is unavailable.")
		return
	}
	if err := h.service.RetryRegistration(r.Context(), r.PathValue("id")); err != nil {
		writeImageVideoRepoError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"job_id": r.PathValue("id"), "registration_status": "pending"})
}

type publicImageVideoDetail struct {
	Job      publicImageVideoJob       `json:"job"`
	Items    []publicImageVideoItem    `json:"items"`
	Attempts []publicImageVideoAttempt `json:"attempts"`
}

type publicImageVideoJob struct {
	ID                 string                `json:"id"`
	ProjectID          string                `json:"project_id"`
	AccountID          string                `json:"account_id"`
	TemplateVersion    string                `json:"template_version"`
	OutputMode         imagevideo.OutputMode `json:"output_mode"`
	Status             imagevideo.JobStatus  `json:"status"`
	Model              string                `json:"model"`
	Resolution         string                `json:"resolution"`
	Phase              string                `json:"phase"`
	DraftStatus        string                `json:"draft_status"`
	RegistrationStatus string                `json:"registration_status"`
	RetryRound         int                   `json:"retry_round"`
	Concurrency        int                   `json:"concurrency"`
	Version            int                   `json:"version"`
	ErrorCode          string                `json:"error_code,omitempty"`
	ErrorMessage       string                `json:"error_message,omitempty"`
	DraftName          string                `json:"draft_name,omitempty"`
}

type publicImageVideoItem struct {
	ID                       string `json:"id"`
	ImageProjectItemID       string `json:"image_project_item_id"`
	Ordinal                  int    `json:"ordinal"`
	RetryRound               int    `json:"retry_round"`
	Attempt                  int    `json:"attempt"`
	MaxAttempts              int    `json:"max_attempts"`
	Status                   string `json:"status"`
	TimelineDurationUS       int64  `json:"timeline_duration_us"`
	RequestedDurationSeconds *int   `json:"requested_duration_seconds,omitempty"`
	ActualDurationUS         *int64 `json:"actual_duration_us,omitempty"`
	ErrorCode                string `json:"error_code,omitempty"`
	ErrorMessage             string `json:"error_message,omitempty"`
}

type publicImageVideoAttempt struct {
	ID           string `json:"id"`
	JobItemID    string `json:"job_item_id"`
	RetryRound   int    `json:"retry_round"`
	Attempt      int    `json:"attempt"`
	Status       string `json:"status"`
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}

func toPublicImageVideoDetail(detail imagevideo.JobDetail) publicImageVideoDetail {
	draftName := ""
	if detail.Job.RegistrationStatus == "succeeded" && detail.Job.ID != "" {
		draftName = "图文视频-" + detail.Job.ID
	}
	result := publicImageVideoDetail{Job: publicImageVideoJob{ID: detail.Job.ID, ProjectID: detail.Job.ProjectID, AccountID: detail.Job.AccountID, TemplateVersion: detail.Job.TemplateVersion, OutputMode: detail.Job.OutputMode, Status: detail.Job.Status, Model: detail.Job.Model, Resolution: detail.Job.Resolution, Phase: detail.Job.Phase, DraftStatus: detail.Job.DraftStatus, RegistrationStatus: detail.Job.RegistrationStatus, RetryRound: detail.Job.RetryRound, Concurrency: detail.Job.Concurrency, Version: detail.Job.Version, ErrorCode: detail.Job.ErrorCode, ErrorMessage: detail.Job.ErrorMessage, DraftName: draftName}, Items: make([]publicImageVideoItem, 0, len(detail.Items)), Attempts: make([]publicImageVideoAttempt, 0, len(detail.Attempts))}
	for _, item := range detail.Items {
		result.Items = append(result.Items, publicImageVideoItem{ID: item.ID, ImageProjectItemID: item.ImageProjectItemID, Ordinal: item.Ordinal, RetryRound: item.RetryRound, Attempt: item.Attempt, MaxAttempts: item.MaxAttempts, Status: item.Status, TimelineDurationUS: item.TimelineDurationUS, RequestedDurationSeconds: item.RequestedDurationSeconds, ActualDurationUS: item.ActualDurationUS, ErrorCode: item.ErrorCode, ErrorMessage: item.ErrorMessage})
	}
	for _, attempt := range detail.Attempts {
		result.Attempts = append(result.Attempts, publicImageVideoAttempt{ID: attempt.ID, JobItemID: attempt.JobItemID, RetryRound: attempt.RetryRound, Attempt: attempt.Attempt, Status: attempt.Status, ErrorCode: attempt.ErrorCode, ErrorMessage: attempt.ErrorMessage})
	}
	return result
}

func writeImageVideoRepoError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, imagevideo.ErrNotFound):
		writeError(w, http.StatusNotFound, "image_video_job_not_found", "The image video job was not found.")
	case errors.Is(err, imagevideo.ErrNoRetryableItem):
		writeError(w, http.StatusConflict, "image_video_no_failed_item", "There is no failed item to retry.")
	case errors.Is(err, imagevideo.ErrConflict):
		writeError(w, http.StatusConflict, "image_video_conflict", "The image video job cannot be changed in its current state.")
	default:
		writeError(w, http.StatusInternalServerError, "image_video_job_failed", "The image video job could not be updated.")
	}
}
