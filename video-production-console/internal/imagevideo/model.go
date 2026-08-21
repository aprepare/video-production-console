package imagevideo

import (
	"context"
	"errors"
	"io"
	"time"
)

type OutputMode string

const (
	ModeSlideshow            OutputMode = "image_slideshow"
	ModeImageToVideo         OutputMode = "image_to_video"
	OutputModeImageSlideshow OutputMode = "image_slideshow"
	OutputModeImageToVideo   OutputMode = "image_to_video"
)

var (
	ErrNotFound        = errors.New("image video job not found")
	ErrConflict        = errors.New("image video conflict")
	ErrLeaseLost       = errors.New("image video lease lost")
	ErrNoClaimableItem = errors.New("no claimable image video item")
	ErrNoRetryableItem = errors.New("no failed image video item")
)

type CreateItem struct {
	ID, ImageProjectItemID, InputImageRelativePath, InputImageSHA256 string
	Ordinal                                                          int
	TimelineDurationUS                                               int64
	RequestedDurationSeconds                                         *int
	MaxAttempts                                                      int
}
type CreateJob struct {
	ID, ProjectID, AccountID, TemplateVersion, TemplateFingerprint, IdempotencyKey string
	OutputMode                                                                     OutputMode
	Concurrency                                                                    int
	Items                                                                          []CreateItem
}
type Job struct {
	ID, ProjectID, AccountID, TemplateVersion, TemplateFingerprint string
	Model, Resolution, Phase, DraftStatus, RegistrationStatus      string
	NarrationRelativePath, NarrationFingerprint                    string
	TimingRelativePath, TimingFingerprint                          string
	DraftRelativePath, DraftFingerprint                            string
	ManifestRelativePath, ManifestFingerprint                      string
	ReceiptRelativePath, ReceiptFingerprint                        string
	LeaseOwner, ErrorCode, ErrorMessage                            string
	OutputMode                                                     OutputMode
	Status                                                         JobStatus
	RetryRound, Concurrency, Version                               int
	LeaseExpiresAt, StartedAt, FinishedAt                          *time.Time
	CreatedAt, UpdatedAt                                           time.Time
}
type JobItem struct {
	ID, JobID, ImageProjectItemID, InputImageRelativePath, InputImageSHA256 string
	OutputVideoRelativePath, OutputVideoSHA256, ProviderRequestID           string
	SourceText, Title, InputImageMIMEType, Motion                           string
	Status, ErrorCode, ErrorMessage, LeaseOwner                             string
	Ordinal, RetryRound, Attempt, MaxAttempts, Version                      int
	TimelineDurationUS                                                      int64
	RequestedDurationSeconds                                                *int
	ActualDurationUS                                                        *int64
	LeaseExpiresAt, StartedAt, FinishedAt                                   *time.Time
	CreatedAt, UpdatedAt                                                    time.Time
}
type JobDetail struct {
	Job      Job
	Items    []JobItem
	Attempts []Attempt
}
type ClaimedItem struct {
	Job  Job
	Item JobItem
}
type Attempt struct {
	ID, JobItemID, RequestFingerprint, Status, ErrorCode, ErrorMessage, ProviderRequestID, OutputVideoRelativePath, OutputVideoSHA256, IdempotencyKey string
	RetryRound, Attempt, ItemVersion                                                                                                                  int
	StartedAt, FinishedAt                                                                                                                             *time.Time
	CreatedAt, UpdatedAt                                                                                                                              time.Time
}
type AttemptSuccess struct {
	Claim                                                         ClaimedItem
	Attempt                                                       Attempt
	OutputVideoRelativePath, OutputVideoSHA256, ProviderRequestID string
	ActualDurationUS                                              int64
}
type AttemptFailure struct {
	Claim                   ClaimedItem
	Attempt                 Attempt
	ErrorCode, ErrorMessage string
	Retryable               bool
}

type MediaDisposition string

const (
	MediaWaiting       MediaDisposition = "waiting"
	MediaReadyForDraft MediaDisposition = "ready_for_draft"
	MediaFailed        MediaDisposition = "failed"
	MediaCanceled      MediaDisposition = "canceled"
)

type VideoSubmitInput struct {
	Prompt, Paragraph, ImageTitle, MotionHint string
	Seconds                                   int
	ImageMIMEType                             string
	Image                                     []byte
}

type VideoRequest struct {
	ID     string
	Status string
}

type VideoStatus struct {
	State           string
	VideoURL        string
	ErrorCode       string
	ErrorMessage    string
	Retryable       bool
	DurationSeconds float64
}

const (
	VideoStatePending = "pending"
	VideoStateDone    = "done"
	VideoStateFailed  = "failed"
)

type VideoProvider interface {
	Submit(context.Context, VideoSubmitInput) (VideoRequest, error)
	Poll(context.Context, string) (VideoStatus, error)
	Download(context.Context, string, io.Writer) error
	Retryable(error) bool
}

type JobStatus string

const (
	JobPending         JobStatus = "pending"
	JobRunning         JobStatus = "running"
	JobSucceeded       JobStatus = "succeeded"
	JobFailed          JobStatus = "failed"
	JobCanceled        JobStatus = "canceled"
	JobStatusPending   JobStatus = "pending"
	JobStatusRunning   JobStatus = "running"
	JobStatusSucceeded JobStatus = "succeeded"
	JobStatusFailed    JobStatus = "failed"
	JobStatusCanceled  JobStatus = "canceled"
)
const (
	VideoModel          = "grok-imagine-video-1.5"
	VideoResolution     = "480p"
	VideoWidth          = 480
	VideoHeight         = 848
	VideoFPS            = 24
	MaxVideoConcurrency = 6
	MaxAttemptsPerRound = 3
)
