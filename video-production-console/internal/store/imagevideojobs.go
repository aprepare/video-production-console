package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"video-production-console/internal/imagevideo"
)

const imageVideoJobSelectColumns = `id,project_id,output_mode,account_id,template_version,template_fingerprint,status,model,resolution,concurrency_limit,retry_round,phase,draft_status,registration_status,lease_owner,lease_expires_at,version,narration_relative_path,narration_fingerprint,timing_relative_path,timing_fingerprint,draft_relative_path,draft_fingerprint,manifest_relative_path,manifest_fingerprint,receipt_relative_path,receipt_fingerprint,error_code,error_message,started_at,finished_at,created_at,updated_at`
const imageVideoItemSelectColumns = `id,job_id,image_project_item_id,ordinal,status,retry_round,attempt,max_attempts,timeline_duration_us,requested_duration_seconds,actual_duration_us,input_image_relative_path,input_image_sha256,output_video_relative_path,output_video_sha256,provider_request_id,lease_owner,lease_expires_at,version,error_code,error_message,started_at,finished_at,created_at,updated_at`
const imageVideoAttemptSelectColumns = `id,job_item_id,retry_round,attempt,idempotency_key,provider_request_id,request_fingerprint,status,error_code,error_message,output_video_relative_path,output_video_sha256,started_at,finished_at,created_at,updated_at`

var (
	imageVideoSecretPattern  = regexp.MustCompile(`(?i)(authorization|bearer|api[_-]?key)["']?\s*[:= ]\s*["']?[^,;\s"']+`)
	imageVideoDataURLPattern = regexp.MustCompile(`(?i)data:[^,\s]+;base64,[a-z0-9+/=_-]+`)
)

type ImageVideoJobRepository struct {
	db     *sql.DB
	redact func(string) string
}

func NewImageVideoJobRepository(db *sql.DB) *ImageVideoJobRepository {
	return NewImageVideoJobRepositoryWithRedactor(db, nil)
}

func NewImageVideoJobRepositoryWithRedactor(db *sql.DB, redactor func(string) string) *ImageVideoJobRepository {
	if redactor == nil {
		redactor = defaultImageVideoRedactor
	}
	return &ImageVideoJobRepository{db: db, redact: redactor}
}

func defaultImageVideoRedactor(value string) string {
	value = imageVideoSecretPattern.ReplaceAllString(value, "$1=[REDACTED]")
	value = imageVideoDataURLPattern.ReplaceAllString(value, "[DATA_URL_REDACTED]")
	runes := []rune(value)
	if len(runes) > 1000 {
		value = string(runes[:1000]) + "…"
	}
	return value
}

func safeImageVideoErrorCode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "image_video_failed"
	}
	var result strings.Builder
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '_' || character == '-' {
			result.WriteRune(character)
		}
	}
	if result.Len() == 0 {
		return "image_video_failed"
	}
	return result.String()
}

func (r *ImageVideoJobRepository) immediate(ctx context.Context, operation string, fn func(assetDBTX, time.Time) error) error {
	return runImmediate(ctx, r.db, operation, nil, fn)
}

func (r *ImageVideoJobRepository) CreateAndLock(ctx context.Context, input imagevideo.CreateJob) (job imagevideo.Job, existed bool, returnErr error) {
	if err := validateCreateImageVideoJob(input); err != nil {
		return job, false, err
	}
	_, builtInFingerprint, err := imagevideo.BuiltInTemplate()
	if err != nil {
		return job, false, fmt.Errorf("load image video template: %w", err)
	}
	if input.TemplateVersion != imagevideo.BuiltInTemplateVersion || !strings.EqualFold(input.TemplateFingerprint, builtInFingerprint) {
		return job, false, fmt.Errorf("%w: template does not match the built-in contract", imagevideo.ErrConflict)
	}
	returnErr = r.immediate(ctx, "create image video job", func(q assetDBTX, now time.Time) error {
		var existingID string
		err := q.QueryRowContext(ctx, `SELECT id FROM image_video_jobs WHERE idempotency_key=?`, input.IdempotencyKey).Scan(&existingID)
		if err == nil {
			if err := r.getJob(ctx, q, existingID, &job); err != nil {
				return err
			}
			itemsMatch, err := sameCreateItems(ctx, q, job.ID, input.Items)
			if err != nil {
				return err
			}
			if !sameCreateRequest(job, input) || !itemsMatch {
				return fmt.Errorf("%w: idempotency key belongs to another image video job", imagevideo.ErrConflict)
			}
			existed = true
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var accountExists int
		if err := q.QueryRowContext(ctx, `SELECT 1 FROM accounts WHERE id=?`, input.AccountID).Scan(&accountExists); errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: account does not exist", imagevideo.ErrConflict)
		} else if err != nil {
			return err
		}
		var projectMode string
		var projectAccount, projectTemplateVersion, projectTemplateFingerprint sql.NullString
		var lockedAt sql.NullTime
		err = q.QueryRowContext(ctx, `SELECT output_mode,account_id,template_version,template_fingerprint,output_mode_locked_at FROM image_projects WHERE id=?`, input.ProjectID).Scan(&projectMode, &projectAccount, &projectTemplateVersion, &projectTemplateFingerprint, &lockedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return imagevideo.ErrNotFound
		}
		if err != nil {
			return err
		}
		if lockedAt.Valid {
			if projectMode != string(input.OutputMode) || projectAccount.String != input.AccountID || projectTemplateVersion.String != input.TemplateVersion || !strings.EqualFold(projectTemplateFingerprint.String, input.TemplateFingerprint) {
				return fmt.Errorf("%w: image project output settings are locked", imagevideo.ErrConflict)
			}
		} else {
			result, err := q.ExecContext(ctx, `UPDATE image_projects SET output_mode=?,account_id=?,template_version=?,template_fingerprint=?,output_mode_locked_at=?,updated_at=? WHERE id=? AND output_mode_locked_at IS NULL`, input.OutputMode, input.AccountID, input.TemplateVersion, strings.ToLower(input.TemplateFingerprint), now, now, input.ProjectID)
			if err != nil {
				return err
			}
			affected, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if affected != 1 {
				return fmt.Errorf("%w: image project was locked concurrently", imagevideo.ErrConflict)
			}
		}
		for _, item := range input.Items {
			var projectID, status string
			var imagePath sql.NullString
			err := q.QueryRowContext(ctx, `SELECT project_id,status,image_path FROM image_project_items WHERE id=?`, item.ImageProjectItemID).Scan(&projectID, &status, &imagePath)
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: image project item does not exist", imagevideo.ErrConflict)
			}
			if err != nil {
				return err
			}
			if projectID != input.ProjectID || status != "ready" || !imagePath.Valid || strings.TrimSpace(imagePath.String) == "" {
				return fmt.Errorf("%w: image project item is not ready for this project", imagevideo.ErrConflict)
			}
		}
		jobID := strings.TrimSpace(input.ID)
		if jobID == "" {
			jobID = uuid.NewString()
		}
		_, err = q.ExecContext(ctx, `INSERT INTO image_video_jobs(id,project_id,output_mode,account_id,template_version,template_fingerprint,idempotency_key,status,model,resolution,concurrency_limit,retry_round,phase,version,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, jobID, input.ProjectID, input.OutputMode, input.AccountID, input.TemplateVersion, strings.ToLower(input.TemplateFingerprint), input.IdempotencyKey, imagevideo.JobPending, imagevideo.VideoModel, imagevideo.VideoResolution, input.Concurrency, 0, "preparing", 1, now, now)
		if err != nil {
			return err
		}
		for _, item := range input.Items {
			itemID := strings.TrimSpace(item.ID)
			if itemID == "" {
				itemID = uuid.NewString()
			}
			maxAttempts := item.MaxAttempts
			if maxAttempts == 0 {
				maxAttempts = imagevideo.MaxAttemptsPerRound
			}
			_, err = q.ExecContext(ctx, `INSERT INTO image_video_job_items(id,job_id,image_project_item_id,ordinal,status,retry_round,attempt,max_attempts,timeline_duration_us,requested_duration_seconds,input_image_relative_path,input_image_sha256,version,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, itemID, jobID, item.ImageProjectItemID, item.Ordinal, imagevideo.JobPending, 0, 0, maxAttempts, item.TimelineDurationUS, item.RequestedDurationSeconds, normalizeRelativeArtifactPath(item.InputImageRelativePath), strings.ToLower(item.InputImageSHA256), 1, now, now)
			if err != nil {
				return err
			}
		}
		return r.getJob(ctx, q, jobID, &job)
	})
	return job, existed, returnErr
}

func (r *ImageVideoJobRepository) GetDetail(ctx context.Context, jobID string) (imagevideo.JobDetail, error) {
	detail := imagevideo.JobDetail{Items: []imagevideo.JobItem{}, Attempts: []imagevideo.Attempt{}}
	if err := r.getJob(ctx, r.db, jobID, &detail.Job); err != nil {
		return detail, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+imageVideoItemSelectColumns+` FROM image_video_job_items WHERE job_id=? ORDER BY ordinal,id`, jobID)
	if err != nil {
		return detail, err
	}
	for rows.Next() {
		var item imagevideo.JobItem
		if err := scanImageVideoItem(rows, &item); err != nil {
			rows.Close()
			return detail, err
		}
		detail.Items = append(detail.Items, item)
	}
	if err := rows.Close(); err != nil {
		return detail, err
	}
	if err := rows.Err(); err != nil {
		return detail, err
	}
	attemptRows, err := r.db.QueryContext(ctx, `SELECT a.id,a.job_item_id,a.retry_round,a.attempt,a.idempotency_key,a.provider_request_id,a.request_fingerprint,a.status,a.error_code,a.error_message,a.output_video_relative_path,a.output_video_sha256,a.started_at,a.finished_at,a.created_at,a.updated_at FROM image_video_job_attempts AS a JOIN image_video_job_items AS item ON item.id=a.job_item_id WHERE item.job_id=? ORDER BY item.ordinal,a.retry_round,a.attempt,a.created_at,a.id`, jobID)
	if err != nil {
		return detail, err
	}
	for attemptRows.Next() {
		var attempt imagevideo.Attempt
		if err := scanImageVideoAttempt(attemptRows, &attempt); err != nil {
			attemptRows.Close()
			return detail, err
		}
		detail.Attempts = append(detail.Attempts, attempt)
	}
	if err := attemptRows.Close(); err != nil {
		return detail, err
	}
	return detail, attemptRows.Err()
}

func (r *ImageVideoJobRepository) GetByIdempotencyKey(ctx context.Context, key string) (imagevideo.Job, error) {
	var job imagevideo.Job
	err := scanImageVideoJob(r.db.QueryRowContext(ctx, `SELECT `+imageVideoJobSelectColumns+` FROM image_video_jobs WHERE idempotency_key=?`, strings.TrimSpace(key)), &job)
	if errors.Is(err, sql.ErrNoRows) {
		return job, imagevideo.ErrNotFound
	}
	return job, err
}

// GetLatestForProject returns the active job first, otherwise the most recent
// completed/failed/canceled job. It is used only to restore project detail UI
// state; callers must still use GetDetail for the authoritative job payload.
func (r *ImageVideoJobRepository) GetLatestForProject(ctx context.Context, projectID string) (imagevideo.Job, error) {
	var job imagevideo.Job
	err := scanImageVideoJob(r.db.QueryRowContext(ctx, `SELECT `+imageVideoJobSelectColumns+` FROM image_video_jobs WHERE project_id=? ORDER BY CASE WHEN status IN ('pending','running') THEN 0 ELSE 1 END,updated_at DESC,created_at DESC,id DESC LIMIT 1`, strings.TrimSpace(projectID)), &job)
	if errors.Is(err, sql.ErrNoRows) {
		return job, imagevideo.ErrNotFound
	}
	return job, err
}

func (r *ImageVideoJobRepository) SaveNarration(ctx context.Context, jobID string, artifact imagevideo.NarrationArtifact) error {
	return r.immediate(ctx, "save image video narration", func(q assetDBTX, now time.Time) error {
		result, err := q.ExecContext(ctx, `UPDATE image_video_jobs SET status='pending',phase='preparing',narration_relative_path=?,narration_fingerprint=?,timing_relative_path=?,timing_fingerprint=?,error_code=NULL,error_message=NULL,finished_at=NULL,updated_at=?,version=version+1 WHERE id=? AND status IN ('pending','failed') AND phase='preparing' AND (narration_relative_path IS NULL OR (narration_relative_path=? AND narration_fingerprint=? AND timing_relative_path=? AND timing_fingerprint=?))`, normalizeRelativeArtifactPath(artifact.AudioRelativePath), strings.ToLower(artifact.AudioSHA256), normalizeRelativeArtifactPath(artifact.TimingRelativePath), strings.ToLower(artifact.TimingSHA256), now, jobID, normalizeRelativeArtifactPath(artifact.AudioRelativePath), strings.ToLower(artifact.AudioSHA256), normalizeRelativeArtifactPath(artifact.TimingRelativePath), strings.ToLower(artifact.TimingSHA256))
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			if err != nil {
				return err
			}
			return imagevideo.ErrConflict
		}
		return nil
	})
}

func (r *ImageVideoJobRepository) FailPreparation(ctx context.Context, jobID, message string) error {
	return r.immediate(ctx, "fail image video preparation", func(q assetDBTX, now time.Time) error {
		result, err := q.ExecContext(ctx, `UPDATE image_video_jobs SET status='failed',phase='preparing',error_code='image_video_preparation_failed',error_message=?,finished_at=?,updated_at=?,version=version+1 WHERE id=? AND phase='preparing' AND status IN ('pending','failed')`, r.redact(message), now, now, jobID)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			if err != nil {
				return err
			}
			return imagevideo.ErrConflict
		}
		return nil
	})
}

func (r *ImageVideoJobRepository) BeginDraft(ctx context.Context, jobID string, now time.Time) (imagevideo.JobDetail, error) {
	detail, err := r.GetDetail(ctx, jobID)
	if err != nil {
		return detail, err
	}
	if detail.Job.Phase != "draft" && detail.Job.Phase != "registration" {
		return detail, fmt.Errorf("%w: media is not ready for draft", imagevideo.ErrConflict)
	}
	for _, item := range detail.Items {
		if item.Status != string(imagevideo.JobSucceeded) {
			return detail, fmt.Errorf("%w: image video items are incomplete", imagevideo.ErrConflict)
		}
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	_, err = r.db.ExecContext(ctx, `UPDATE image_video_jobs SET status='running',phase='draft',draft_status='running',registration_status=NULL,error_code=NULL,error_message=NULL,finished_at=NULL,updated_at=?,version=version+1 WHERE id=? AND phase IN ('draft','registration') AND status IN ('running','failed')`, now, jobID)
	return detail, err
}

func (r *ImageVideoJobRepository) SaveDraft(ctx context.Context, jobID, draftRelativePath, draftFingerprint, manifestRelativePath, manifestFingerprint string, now time.Time) error {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	result, err := r.db.ExecContext(ctx, `UPDATE image_video_jobs SET phase='registration',draft_status='succeeded',registration_status='pending',draft_relative_path=?,draft_fingerprint=?,manifest_relative_path=?,manifest_fingerprint=?,error_code=NULL,error_message=NULL,updated_at=?,version=version+1 WHERE id=? AND status='running' AND phase='draft'`, normalizeRelativeArtifactPath(draftRelativePath), strings.ToLower(draftFingerprint), normalizeRelativeArtifactPath(manifestRelativePath), strings.ToLower(manifestFingerprint), now, jobID)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return err
		}
		return imagevideo.ErrConflict
	}
	return nil
}

func (r *ImageVideoJobRepository) FailDraftOrRegistration(ctx context.Context, jobID, phase, code, message string, now time.Time) error {
	if phase != "draft" && phase != "registration" {
		return imagevideo.ErrConflict
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	draftStatus, registrationStatus := "failed", ""
	if phase == "registration" {
		draftStatus, registrationStatus = "succeeded", "failed"
	}
	_, err := r.db.ExecContext(ctx, `UPDATE image_video_jobs SET status='failed',phase=?,draft_status=?,registration_status=?,error_code=?,error_message=?,finished_at=?,updated_at=?,version=version+1 WHERE id=? AND phase IN ('draft','registration')`, phase, draftStatus, registrationStatus, safeImageVideoErrorCode(code), r.redact(message), now, now, jobID)
	return err
}

func (r *ImageVideoJobRepository) CompleteRegistration(ctx context.Context, jobID, receiptRelativePath, receiptFingerprint string, now time.Time) error {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	result, err := r.db.ExecContext(ctx, `UPDATE image_video_jobs SET status='succeeded',phase='completed',draft_status='succeeded',registration_status='succeeded',receipt_relative_path=?,receipt_fingerprint=?,error_code=NULL,error_message=NULL,finished_at=?,updated_at=?,version=version+1 WHERE id=? AND status='running' AND phase='registration'`, normalizeRelativeArtifactPath(receiptRelativePath), strings.ToLower(receiptFingerprint), now, now, jobID)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return err
		}
		return imagevideo.ErrConflict
	}
	return nil
}

func (r *ImageVideoJobRepository) ClaimNextItem(ctx context.Context, owner string, now time.Time, lease time.Duration) (claimed imagevideo.ClaimedItem, returnErr error) {
	owner = strings.TrimSpace(owner)
	if owner == "" || lease <= 0 {
		return claimed, fmt.Errorf("%w: invalid lease request", imagevideo.ErrConflict)
	}
	returnErr = r.immediate(ctx, "claim image video item", func(q assetDBTX, transactionNow time.Time) error {
		if now.IsZero() {
			now = transactionNow
		}
		var itemID, jobID string
		err := q.QueryRowContext(ctx, `SELECT item.id,job.id FROM image_video_job_items AS item JOIN image_video_jobs AS job ON job.id=item.job_id WHERE item.status='pending' AND job.status IN ('pending','running') AND (SELECT COUNT(*) FROM image_video_job_items AS active JOIN image_video_jobs AS active_job ON active_job.id=active.job_id WHERE active.status='running' AND active_job.status='running') < ? AND (SELECT COUNT(*) FROM image_video_job_items AS active_for_job WHERE active_for_job.job_id=job.id AND active_for_job.status='running') < job.concurrency_limit ORDER BY job.created_at,job.id,item.ordinal,item.id LIMIT 1`, imagevideo.MaxVideoConcurrency).Scan(&itemID, &jobID)
		if errors.Is(err, sql.ErrNoRows) {
			return imagevideo.ErrNoClaimableItem
		}
		if err != nil {
			return err
		}
		if err := r.getJob(ctx, q, jobID, &claimed.Job); err != nil {
			return err
		}
		if err := r.getItem(ctx, q, itemID, &claimed.Item); err != nil {
			return err
		}
		var mimeType sql.NullString
		if err := q.QueryRowContext(ctx, `SELECT source_text,title,mime_type FROM image_project_items WHERE id=? AND project_id=?`, claimed.Item.ImageProjectItemID, claimed.Job.ProjectID).Scan(&claimed.Item.SourceText, &claimed.Item.Title, &mimeType); err != nil {
			return err
		}
		claimed.Item.InputImageMIMEType = mimeType.String
		expiresAt := now.Add(lease)
		itemResult, err := q.ExecContext(ctx, `UPDATE image_video_job_items SET status='running',lease_owner=?,lease_expires_at=?,started_at=COALESCE(started_at,?),updated_at=?,version=version+1 WHERE id=? AND status='pending' AND version=?`, owner, expiresAt, now, now, itemID, claimed.Item.Version)
		if err != nil {
			return err
		}
		if affected, err := itemResult.RowsAffected(); err != nil || affected != 1 {
			if err != nil {
				return err
			}
			return imagevideo.ErrConflict
		}
		jobWasPending := claimed.Job.Status == imagevideo.JobPending
		jobResult, err := q.ExecContext(ctx, `UPDATE image_video_jobs SET status='running',phase='media',lease_owner=?,lease_expires_at=?,started_at=COALESCE(started_at,?),updated_at=?,version=version+CASE WHEN status='pending' THEN 1 ELSE 0 END WHERE id=? AND status IN ('pending','running') AND version=?`, owner, expiresAt, now, now, jobID, claimed.Job.Version)
		if err != nil {
			return err
		}
		if affected, err := jobResult.RowsAffected(); err != nil || affected != 1 {
			if err != nil {
				return err
			}
			return imagevideo.ErrConflict
		}
		claimed.Item.Status = string(imagevideo.JobRunning)
		claimed.Item.LeaseOwner, claimed.Item.LeaseExpiresAt = owner, timePointer(expiresAt)
		if claimed.Item.StartedAt == nil {
			claimed.Item.StartedAt = timePointer(now)
		}
		claimed.Item.Version++
		claimed.Job.Status, claimed.Job.Phase = imagevideo.JobRunning, "media"
		claimed.Job.LeaseOwner, claimed.Job.LeaseExpiresAt = owner, timePointer(expiresAt)
		if claimed.Job.StartedAt == nil {
			claimed.Job.StartedAt = timePointer(now)
		}
		if jobWasPending {
			claimed.Job.Version++
		}
		return nil
	})
	return claimed, returnErr
}

func (r *ImageVideoJobRepository) BeginAttempt(ctx context.Context, claim imagevideo.ClaimedItem, requestFingerprint string) (attempt imagevideo.Attempt, returnErr error) {
	if claim.Item.ID == "" || claim.Job.ID == "" || strings.TrimSpace(requestFingerprint) == "" {
		return attempt, fmt.Errorf("%w: incomplete attempt claim", imagevideo.ErrConflict)
	}
	returnErr = r.immediate(ctx, "begin image video attempt", func(q assetDBTX, now time.Time) error {
		var currentJob imagevideo.Job
		if err := r.getJob(ctx, q, claim.Job.ID, &currentJob); err != nil {
			return err
		}
		if currentJob.Status != imagevideo.JobRunning || currentJob.LeaseOwner != claim.Job.LeaseOwner || currentJob.Version != claim.Job.Version || currentJob.LeaseExpiresAt == nil || !currentJob.LeaseExpiresAt.After(now) {
			return imagevideo.ErrLeaseLost
		}
		var current imagevideo.JobItem
		if err := r.getItem(ctx, q, claim.Item.ID, &current); err != nil {
			return err
		}
		if current.JobID != claim.Job.ID || current.RetryRound != claim.Item.RetryRound {
			return imagevideo.ErrLeaseLost
		}
		if current.Status != string(imagevideo.JobRunning) || current.LeaseOwner != claim.Item.LeaseOwner || current.LeaseExpiresAt == nil || !current.LeaseExpiresAt.After(now) {
			return imagevideo.ErrLeaseLost
		}
		if current.ProviderRequestID != "" && current.Attempt > 0 {
			err := scanImageVideoAttempt(q.QueryRowContext(ctx, `SELECT `+imageVideoAttemptSelectColumns+` FROM image_video_job_attempts WHERE job_item_id=? AND retry_round=? AND attempt=? AND status='running'`, current.ID, current.RetryRound, current.Attempt), &attempt)
			if err != nil {
				return err
			}
			if attempt.ProviderRequestID != current.ProviderRequestID || attempt.RequestFingerprint != requestFingerprint {
				return fmt.Errorf("%w: persisted provider request does not match the claimed attempt", imagevideo.ErrConflict)
			}
			attempt.ItemVersion = current.Version
			return nil
		}
		nextAttempt := claim.Item.Attempt + 1
		err := scanImageVideoAttempt(q.QueryRowContext(ctx, `SELECT `+imageVideoAttemptSelectColumns+` FROM image_video_job_attempts WHERE job_item_id=? AND retry_round=? AND attempt=?`, current.ID, current.RetryRound, nextAttempt), &attempt)
		if err == nil {
			if attempt.RequestFingerprint != requestFingerprint {
				return fmt.Errorf("%w: attempt fingerprint changed", imagevideo.ErrConflict)
			}
			attempt.ItemVersion = current.Version
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if current.Status != string(imagevideo.JobRunning) || current.LeaseOwner != claim.Item.LeaseOwner || current.Version != claim.Item.Version || current.Attempt != claim.Item.Attempt || current.LeaseExpiresAt == nil || !current.LeaseExpiresAt.After(now) {
			return imagevideo.ErrLeaseLost
		}
		if nextAttempt > current.MaxAttempts {
			return fmt.Errorf("%w: attempt limit reached", imagevideo.ErrConflict)
		}
		attempt = imagevideo.Attempt{ID: uuid.NewString(), JobItemID: current.ID, RetryRound: current.RetryRound, Attempt: nextAttempt, IdempotencyKey: fmt.Sprintf("%s:%d:%d", current.ID, current.RetryRound, nextAttempt), RequestFingerprint: requestFingerprint, Status: string(imagevideo.JobRunning), StartedAt: timePointer(now), CreatedAt: now, UpdatedAt: now}
		_, err = q.ExecContext(ctx, `INSERT INTO image_video_job_attempts(id,job_item_id,retry_round,attempt,idempotency_key,request_fingerprint,status,started_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, attempt.ID, attempt.JobItemID, attempt.RetryRound, attempt.Attempt, attempt.IdempotencyKey, attempt.RequestFingerprint, attempt.Status, now, now, now)
		if err != nil {
			return err
		}
		result, err := q.ExecContext(ctx, `UPDATE image_video_job_items SET attempt=?,updated_at=?,version=version+1 WHERE id=? AND job_id=? AND status='running' AND lease_owner=? AND version=? AND retry_round=? AND attempt=?`, nextAttempt, now, current.ID, claim.Job.ID, claim.Item.LeaseOwner, current.Version, current.RetryRound, current.Attempt)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			if err != nil {
				return err
			}
			return imagevideo.ErrLeaseLost
		}
		attempt.ItemVersion = current.Version + 1
		return nil
	})
	return attempt, returnErr
}

// SaveProviderRequestID closes the crash window between a successful submit
// and the next poll. The request ID is immutable and stored on both the item
// and attempt before the worker performs any further provider operation.
func (r *ImageVideoJobRepository) SaveProviderRequestID(ctx context.Context, claim imagevideo.ClaimedItem, attempt imagevideo.Attempt, requestID string) (updated imagevideo.Attempt, returnErr error) {
	requestID = strings.TrimSpace(requestID)
	if attempt.ID == "" || claim.Item.ID == "" || !validProviderRequestID(requestID) {
		return updated, fmt.Errorf("%w: invalid provider request", imagevideo.ErrConflict)
	}
	returnErr = r.immediate(ctx, "save image video provider request", func(q assetDBTX, now time.Time) error {
		var current imagevideo.JobItem
		if err := r.getItem(ctx, q, claim.Item.ID, &current); err != nil {
			return err
		}
		var persisted imagevideo.Attempt
		if err := scanImageVideoAttempt(q.QueryRowContext(ctx, `SELECT `+imageVideoAttemptSelectColumns+` FROM image_video_job_attempts WHERE id=?`, attempt.ID), &persisted); err != nil {
			return err
		}
		if current.ProviderRequestID != "" || persisted.ProviderRequestID != "" {
			if current.ProviderRequestID == requestID && persisted.ProviderRequestID == requestID {
				persisted.ItemVersion = current.Version
				updated = persisted
				return nil
			}
			return fmt.Errorf("%w: provider request id is immutable", imagevideo.ErrConflict)
		}
		if !attemptMatchesClaim(attempt, claim, current, now) || persisted.Status != string(imagevideo.JobRunning) || persisted.JobItemID != current.ID || persisted.RetryRound != current.RetryRound || persisted.Attempt != current.Attempt {
			return imagevideo.ErrLeaseLost
		}
		result, err := q.ExecContext(ctx, `UPDATE image_video_job_attempts SET provider_request_id=?,updated_at=? WHERE id=? AND job_item_id=? AND status='running' AND provider_request_id IS NULL`, requestID, now, persisted.ID, current.ID)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			if err != nil {
				return err
			}
			return imagevideo.ErrLeaseLost
		}
		result, err = q.ExecContext(ctx, `UPDATE image_video_job_items SET provider_request_id=?,updated_at=?,version=version+1 WHERE id=? AND job_id=? AND status='running' AND lease_owner=? AND version=? AND retry_round=? AND attempt=? AND provider_request_id IS NULL`, requestID, now, current.ID, claim.Job.ID, current.LeaseOwner, current.Version, current.RetryRound, current.Attempt)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			if err != nil {
				return err
			}
			return imagevideo.ErrLeaseLost
		}
		persisted.ProviderRequestID = requestID
		persisted.ItemVersion = current.Version + 1
		persisted.UpdatedAt = now
		updated = persisted
		return nil
	})
	return updated, returnErr
}

func (r *ImageVideoJobRepository) CompleteAttempt(ctx context.Context, input imagevideo.AttemptSuccess) error {
	if input.Attempt.ID == "" || input.Claim.Item.ID == "" || input.ActualDurationUS <= 0 || !validSHA256(input.OutputVideoSHA256) || !validRelativeArtifactPath(input.OutputVideoRelativePath) {
		return fmt.Errorf("%w: invalid successful attempt", imagevideo.ErrConflict)
	}
	return r.immediate(ctx, "complete image video attempt", func(q assetDBTX, now time.Time) error {
		var current imagevideo.JobItem
		if err := r.getItem(ctx, q, input.Claim.Item.ID, &current); err != nil {
			return err
		}
		path, sha := normalizeRelativeArtifactPath(input.OutputVideoRelativePath), strings.ToLower(input.OutputVideoSHA256)
		if current.Status == string(imagevideo.JobSucceeded) {
			if current.OutputVideoRelativePath == path && strings.EqualFold(current.OutputVideoSHA256, sha) && current.ActualDurationUS != nil && *current.ActualDurationUS == input.ActualDurationUS {
				return nil
			}
			return fmt.Errorf("%w: successful output is immutable", imagevideo.ErrConflict)
		}
		if !attemptMatchesClaim(input.Attempt, input.Claim, current, now) {
			return imagevideo.ErrLeaseLost
		}
		result, err := q.ExecContext(ctx, `UPDATE image_video_job_attempts SET status='succeeded',provider_request_id=?,output_video_relative_path=?,output_video_sha256=?,finished_at=?,updated_at=? WHERE id=? AND job_item_id=? AND retry_round=? AND attempt=? AND status='running'`, input.ProviderRequestID, path, sha, now, now, input.Attempt.ID, current.ID, current.RetryRound, current.Attempt)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			if err != nil {
				return err
			}
			return imagevideo.ErrLeaseLost
		}
		result, err = q.ExecContext(ctx, `UPDATE image_video_job_items SET status='succeeded',output_video_relative_path=?,output_video_sha256=?,provider_request_id=?,actual_duration_us=?,error_code=NULL,error_message=NULL,lease_owner=NULL,lease_expires_at=NULL,finished_at=?,updated_at=?,version=version+1 WHERE id=? AND job_id=? AND status='running' AND lease_owner=? AND version=? AND retry_round=? AND attempt=?`, path, sha, input.ProviderRequestID, input.ActualDurationUS, now, now, current.ID, input.Claim.Job.ID, current.LeaseOwner, current.Version, current.RetryRound, current.Attempt)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			if err != nil {
				return err
			}
			return imagevideo.ErrLeaseLost
		}
		return nil
	})
}

func (r *ImageVideoJobRepository) FailAttempt(ctx context.Context, input imagevideo.AttemptFailure) (retry bool, returnErr error) {
	if input.Attempt.ID == "" || input.Claim.Item.ID == "" {
		return false, fmt.Errorf("%w: invalid failed attempt", imagevideo.ErrConflict)
	}
	errorCode, errorMessage := r.redact(strings.TrimSpace(input.ErrorCode)), r.redact(strings.TrimSpace(input.ErrorMessage))
	returnErr = r.immediate(ctx, "fail image video attempt", func(q assetDBTX, now time.Time) error {
		var current imagevideo.JobItem
		if err := r.getItem(ctx, q, input.Claim.Item.ID, &current); err != nil {
			return err
		}
		if !attemptMatchesClaim(input.Attempt, input.Claim, current, now) {
			return imagevideo.ErrLeaseLost
		}
		result, err := q.ExecContext(ctx, `UPDATE image_video_job_attempts SET status='failed',error_code=?,error_message=?,finished_at=?,updated_at=? WHERE id=? AND job_item_id=? AND retry_round=? AND attempt=? AND status='running'`, errorCode, errorMessage, now, now, input.Attempt.ID, current.ID, current.RetryRound, current.Attempt)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			if err != nil {
				return err
			}
			return imagevideo.ErrLeaseLost
		}
		retry = input.Retryable && current.Attempt < current.MaxAttempts
		status := string(imagevideo.JobFailed)
		var finishedAt any = now
		if retry {
			status, finishedAt = string(imagevideo.JobPending), nil
		}
		result, err = q.ExecContext(ctx, `UPDATE image_video_job_items SET status=?,provider_request_id=CASE WHEN ?='pending' THEN NULL ELSE provider_request_id END,error_code=?,error_message=?,lease_owner=NULL,lease_expires_at=NULL,finished_at=?,updated_at=?,version=version+1 WHERE id=? AND job_id=? AND status='running' AND lease_owner=? AND version=? AND retry_round=? AND attempt=?`, status, status, errorCode, errorMessage, finishedAt, now, current.ID, input.Claim.Job.ID, current.LeaseOwner, current.Version, current.RetryRound, current.Attempt)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			if err != nil {
				return err
			}
			return imagevideo.ErrLeaseLost
		}
		return nil
	})
	return retry, returnErr
}

func (r *ImageVideoJobRepository) CompleteSlideshowItem(ctx context.Context, claim imagevideo.ClaimedItem) error {
	if claim.Job.OutputMode != imagevideo.ModeSlideshow || claim.Item.ID == "" {
		return fmt.Errorf("%w: invalid slideshow completion", imagevideo.ErrConflict)
	}
	return r.immediate(ctx, "complete image slideshow item", func(q assetDBTX, now time.Time) error {
		var current imagevideo.JobItem
		if err := r.getItem(ctx, q, claim.Item.ID, &current); err != nil {
			return err
		}
		if current.Status == string(imagevideo.JobSucceeded) {
			return nil
		}
		if current.JobID != claim.Job.ID || current.Status != string(imagevideo.JobRunning) || current.LeaseOwner != claim.Item.LeaseOwner || current.Version != claim.Item.Version || current.LeaseExpiresAt == nil || !current.LeaseExpiresAt.After(now) {
			return imagevideo.ErrLeaseLost
		}
		result, err := q.ExecContext(ctx, `UPDATE image_video_job_items SET status='succeeded',error_code=NULL,error_message=NULL,lease_owner=NULL,lease_expires_at=NULL,finished_at=?,updated_at=?,version=version+1 WHERE id=? AND job_id=? AND status='running' AND lease_owner=? AND version=?`, now, now, current.ID, current.JobID, current.LeaseOwner, current.Version)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			if err != nil {
				return err
			}
			return imagevideo.ErrLeaseLost
		}
		return nil
	})
}

// ReconcileMediaJobs is the durable all-scenes gate. It advances a job to the
// draft phase only when every item succeeded; any terminal item failure marks
// the job failed and prevents a partial draft.
func (r *ImageVideoJobRepository) ReconcileMediaJobs(ctx context.Context, now time.Time) (readyJobIDs []string, returnErr error) {
	returnErr = r.immediate(ctx, "reconcile image video media", func(q assetDBTX, transactionNow time.Time) error {
		if now.IsZero() {
			now = transactionNow
		}
		rows, err := q.QueryContext(ctx, `SELECT job.id FROM image_video_jobs AS job WHERE job.status IN ('pending','running') AND job.phase IN ('preparing','media') AND NOT EXISTS (SELECT 1 FROM image_video_job_items AS item WHERE item.job_id=job.id AND item.status IN ('pending','running')) ORDER BY job.created_at,job.id`)
		if err != nil {
			return err
		}
		jobIDs := []string{}
		for rows.Next() {
			var jobID string
			if err := rows.Scan(&jobID); err != nil {
				rows.Close()
				return err
			}
			jobIDs = append(jobIDs, jobID)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for _, jobID := range jobIDs {
			var total, succeeded, failed, canceled int
			if err := q.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(CASE WHEN status='succeeded' THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN status='failed' THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN status='canceled' THEN 1 ELSE 0 END),0) FROM image_video_job_items WHERE job_id=?`, jobID).Scan(&total, &succeeded, &failed, &canceled); err != nil {
				return err
			}
			if total > 0 && succeeded == total {
				result, err := q.ExecContext(ctx, `UPDATE image_video_jobs SET status='running',phase='draft',draft_status=COALESCE(draft_status,'pending'),lease_owner=NULL,lease_expires_at=NULL,error_code=NULL,error_message=NULL,updated_at=?,version=version+1 WHERE id=? AND status IN ('pending','running') AND phase IN ('preparing','media')`, now, jobID)
				if err != nil {
					return err
				}
				if affected, err := result.RowsAffected(); err != nil || affected != 1 {
					if err != nil {
						return err
					}
					return imagevideo.ErrConflict
				}
				readyJobIDs = append(readyJobIDs, jobID)
				continue
			}
			if failed > 0 || canceled > 0 {
				var code, message sql.NullString
				if err := q.QueryRowContext(ctx, `SELECT COALESCE(error_code,'item_canceled'),COALESCE(error_message,'an image video item was canceled') FROM image_video_job_items WHERE job_id=? AND status IN ('failed','canceled') ORDER BY ordinal,id LIMIT 1`, jobID).Scan(&code, &message); err != nil {
					return err
				}
				result, err := q.ExecContext(ctx, `UPDATE image_video_jobs SET status='failed',phase='media',lease_owner=NULL,lease_expires_at=NULL,error_code=?,error_message=?,finished_at=?,updated_at=?,version=version+1 WHERE id=? AND status IN ('pending','running') AND phase IN ('preparing','media')`, code, message, now, now, jobID)
				if err != nil {
					return err
				}
				if affected, err := result.RowsAffected(); err != nil || affected != 1 {
					if err != nil {
						return err
					}
					return imagevideo.ErrConflict
				}
			}
		}
		return nil
	})
	return readyJobIDs, returnErr
}

func (r *ImageVideoJobRepository) StartRetryRound(ctx context.Context, jobID string, now time.Time) (itemIDs []string, returnErr error) {
	returnErr = r.immediate(ctx, "start image video retry round", func(q assetDBTX, transactionNow time.Time) error {
		if now.IsZero() {
			now = transactionNow
		}
		var job imagevideo.Job
		if err := r.getJob(ctx, q, jobID, &job); err != nil {
			return err
		}
		if job.Status != imagevideo.JobFailed {
			return fmt.Errorf("%w: only a failed job can start a retry round", imagevideo.ErrConflict)
		}
		type failedItem struct {
			id      string
			version int
		}
		failed := []failedItem{}
		rows, err := q.QueryContext(ctx, `SELECT id,version FROM image_video_job_items WHERE job_id=? AND status='failed' ORDER BY ordinal,id`, jobID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var item failedItem
			if err := rows.Scan(&item.id, &item.version); err != nil {
				rows.Close()
				return err
			}
			failed = append(failed, item)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(failed) == 0 {
			return imagevideo.ErrNoRetryableItem
		}
		newRound := job.RetryRound + 1
		result, err := q.ExecContext(ctx, `UPDATE image_video_jobs SET retry_round=?,status='pending',phase='media',error_code=NULL,error_message=NULL,lease_owner=NULL,lease_expires_at=NULL,finished_at=NULL,updated_at=?,version=version+1 WHERE id=? AND status='failed' AND version=?`, newRound, now, jobID, job.Version)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			if err != nil {
				return err
			}
			return imagevideo.ErrConflict
		}
		for _, item := range failed {
			result, err := q.ExecContext(ctx, `UPDATE image_video_job_items SET status='pending',retry_round=?,attempt=0,provider_request_id=NULL,output_video_relative_path=NULL,output_video_sha256=NULL,actual_duration_us=NULL,error_code=NULL,error_message=NULL,lease_owner=NULL,lease_expires_at=NULL,started_at=NULL,finished_at=NULL,updated_at=?,version=version+1 WHERE id=? AND job_id=? AND status='failed' AND version=?`, newRound, now, item.id, jobID, item.version)
			if err != nil {
				return err
			}
			if affected, err := result.RowsAffected(); err != nil || affected != 1 {
				if err != nil {
					return err
				}
				return imagevideo.ErrConflict
			}
			itemIDs = append(itemIDs, item.id)
		}
		return nil
	})
	return itemIDs, returnErr
}

func (r *ImageVideoJobRepository) Cancel(ctx context.Context, jobID string, now time.Time) error {
	return r.immediate(ctx, "cancel image video job", func(q assetDBTX, transactionNow time.Time) error {
		if now.IsZero() {
			now = transactionNow
		}
		var job imagevideo.Job
		if err := r.getJob(ctx, q, jobID, &job); err != nil {
			return err
		}
		if job.Status == imagevideo.JobCanceled || job.Status == imagevideo.JobSucceeded {
			return nil
		}
		if job.Status != imagevideo.JobPending && job.Status != imagevideo.JobRunning {
			return fmt.Errorf("%w: job cannot be canceled from %s", imagevideo.ErrConflict, job.Status)
		}
		result, err := q.ExecContext(ctx, `UPDATE image_video_jobs SET status='canceled',lease_owner=NULL,lease_expires_at=NULL,finished_at=?,updated_at=?,version=version+1 WHERE id=? AND version=? AND status IN ('pending','running')`, now, now, jobID, job.Version)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			if err != nil {
				return err
			}
			return imagevideo.ErrConflict
		}
		type cancelItem struct {
			id      string
			version int
		}
		items := []cancelItem{}
		rows, err := q.QueryContext(ctx, `SELECT id,version FROM image_video_job_items WHERE job_id=? AND status IN ('pending','running','failed')`, jobID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var item cancelItem
			if err := rows.Scan(&item.id, &item.version); err != nil {
				rows.Close()
				return err
			}
			items = append(items, item)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for _, item := range items {
			result, err := q.ExecContext(ctx, `UPDATE image_video_job_items SET status='canceled',lease_owner=NULL,lease_expires_at=NULL,finished_at=?,updated_at=?,version=version+1 WHERE id=? AND job_id=? AND version=? AND status IN ('pending','running','failed')`, now, now, item.id, jobID, item.version)
			if err != nil {
				return err
			}
			if affected, err := result.RowsAffected(); err != nil || affected != 1 {
				if err != nil {
					return err
				}
				return imagevideo.ErrConflict
			}
			if _, err := q.ExecContext(ctx, `UPDATE image_video_job_attempts SET status='canceled',error_code='job_canceled',error_message='job canceled',finished_at=?,updated_at=? WHERE job_item_id=? AND status='running'`, now, now, item.id); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *ImageVideoJobRepository) RequeueExpired(ctx context.Context, now time.Time) (count int64, returnErr error) {
	returnErr = r.immediate(ctx, "requeue expired image video items", func(q assetDBTX, transactionNow time.Time) error {
		if now.IsZero() {
			now = transactionNow
		}
		type expiredItem struct {
			id, jobID, providerRequestID  string
			version, attempt, maxAttempts int
		}
		expired := []expiredItem{}
		rows, err := q.QueryContext(ctx, `SELECT id,job_id,COALESCE(provider_request_id,''),version,attempt,max_attempts FROM image_video_job_items WHERE status='running' AND lease_expires_at IS NOT NULL AND lease_expires_at<=? ORDER BY job_id,ordinal,id`, now)
		if err != nil {
			return err
		}
		for rows.Next() {
			var item expiredItem
			if err := rows.Scan(&item.id, &item.jobID, &item.providerRequestID, &item.version, &item.attempt, &item.maxAttempts); err != nil {
				rows.Close()
				return err
			}
			expired = append(expired, item)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}
		jobs := map[string]struct{}{}
		for _, item := range expired {
			status := string(imagevideo.JobPending)
			var finishedAt any
			if item.providerRequestID == "" && item.attempt >= item.maxAttempts {
				status, finishedAt = string(imagevideo.JobFailed), now
			}
			if item.providerRequestID == "" && item.attempt > 0 {
				if _, err := q.ExecContext(ctx, `UPDATE image_video_job_attempts SET status='failed',error_code='worker_lease_expired',error_message='worker lease expired before provider request was saved',finished_at=?,updated_at=? WHERE job_item_id=? AND attempt=? AND status='running'`, now, now, item.id, item.attempt); err != nil {
					return err
				}
			}
			result, err := q.ExecContext(ctx, `UPDATE image_video_job_items SET status=?,error_code=CASE WHEN ?='failed' THEN 'worker_lease_expired' ELSE error_code END,error_message=CASE WHEN ?='failed' THEN 'worker lease expired' ELSE error_message END,lease_owner=NULL,lease_expires_at=NULL,finished_at=?,updated_at=?,version=version+1 WHERE id=? AND status='running' AND version=?`, status, status, status, finishedAt, now, item.id, item.version)
			if err != nil {
				return err
			}
			if affected, err := result.RowsAffected(); err != nil || affected != 1 {
				if err != nil {
					return err
				}
				return imagevideo.ErrConflict
			}
			count++
			jobs[item.jobID] = struct{}{}
		}
		for jobID := range jobs {
			var version int
			err := q.QueryRowContext(ctx, `SELECT version FROM image_video_jobs WHERE id=? AND status='running'`, jobID).Scan(&version)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			result, err := q.ExecContext(ctx, `UPDATE image_video_jobs SET lease_owner=NULL,lease_expires_at=NULL,updated_at=?,version=version+1 WHERE id=? AND status='running' AND version=? AND (lease_expires_at IS NULL OR lease_expires_at<=?)`, now, jobID, version, now)
			if err != nil {
				return err
			}
			if _, err := result.RowsAffected(); err != nil {
				return err
			}
		}
		return nil
	})
	return count, returnErr
}

func (r *ImageVideoJobRepository) getJob(ctx context.Context, q assetDBTX, jobID string, job *imagevideo.Job) error {
	err := scanImageVideoJob(q.QueryRowContext(ctx, `SELECT `+imageVideoJobSelectColumns+` FROM image_video_jobs WHERE id=?`, jobID), job)
	if errors.Is(err, sql.ErrNoRows) {
		return imagevideo.ErrNotFound
	}
	return err
}

func (r *ImageVideoJobRepository) getItem(ctx context.Context, q assetDBTX, itemID string, item *imagevideo.JobItem) error {
	err := scanImageVideoItem(q.QueryRowContext(ctx, `SELECT `+imageVideoItemSelectColumns+` FROM image_video_job_items WHERE id=?`, itemID), item)
	if errors.Is(err, sql.ErrNoRows) {
		return imagevideo.ErrNotFound
	}
	return err
}

type imageVideoScanner interface{ Scan(...any) error }

func scanImageVideoJob(scanner imageVideoScanner, job *imagevideo.Job) error {
	var mode, status string
	var accountID, draftStatus, registrationStatus, leaseOwner sql.NullString
	var narrationPath, narrationFingerprint, timingPath, timingFingerprint, draftPath, draftFingerprint sql.NullString
	var manifestPath, manifestFingerprint, receiptPath, receiptFingerprint, errorCode, errorMessage sql.NullString
	var leaseExpiresAt, startedAt, finishedAt sql.NullTime
	if err := scanner.Scan(&job.ID, &job.ProjectID, &mode, &accountID, &job.TemplateVersion, &job.TemplateFingerprint, &status, &job.Model, &job.Resolution, &job.Concurrency, &job.RetryRound, &job.Phase, &draftStatus, &registrationStatus, &leaseOwner, &leaseExpiresAt, &job.Version, &narrationPath, &narrationFingerprint, &timingPath, &timingFingerprint, &draftPath, &draftFingerprint, &manifestPath, &manifestFingerprint, &receiptPath, &receiptFingerprint, &errorCode, &errorMessage, &startedAt, &finishedAt, &job.CreatedAt, &job.UpdatedAt); err != nil {
		return err
	}
	job.OutputMode, job.Status = imagevideo.OutputMode(mode), imagevideo.JobStatus(status)
	job.AccountID, job.DraftStatus, job.RegistrationStatus, job.LeaseOwner = accountID.String, draftStatus.String, registrationStatus.String, leaseOwner.String
	job.LeaseExpiresAt = nullTimePointer(leaseExpiresAt)
	job.NarrationRelativePath, job.NarrationFingerprint = narrationPath.String, narrationFingerprint.String
	job.TimingRelativePath, job.TimingFingerprint = timingPath.String, timingFingerprint.String
	job.DraftRelativePath, job.DraftFingerprint = draftPath.String, draftFingerprint.String
	job.ManifestRelativePath, job.ManifestFingerprint = manifestPath.String, manifestFingerprint.String
	job.ReceiptRelativePath, job.ReceiptFingerprint = receiptPath.String, receiptFingerprint.String
	job.ErrorCode, job.ErrorMessage = errorCode.String, errorMessage.String
	job.StartedAt, job.FinishedAt = nullTimePointer(startedAt), nullTimePointer(finishedAt)
	return nil
}

func scanImageVideoItem(scanner imageVideoScanner, item *imagevideo.JobItem) error {
	var requestedDuration, actualDuration sql.NullInt64
	var outputPath, outputSHA, providerRequestID, leaseOwner, errorCode, errorMessage sql.NullString
	var leaseExpiresAt, startedAt, finishedAt sql.NullTime
	if err := scanner.Scan(&item.ID, &item.JobID, &item.ImageProjectItemID, &item.Ordinal, &item.Status, &item.RetryRound, &item.Attempt, &item.MaxAttempts, &item.TimelineDurationUS, &requestedDuration, &actualDuration, &item.InputImageRelativePath, &item.InputImageSHA256, &outputPath, &outputSHA, &providerRequestID, &leaseOwner, &leaseExpiresAt, &item.Version, &errorCode, &errorMessage, &startedAt, &finishedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return err
	}
	if requestedDuration.Valid {
		value := int(requestedDuration.Int64)
		item.RequestedDurationSeconds = &value
	}
	if actualDuration.Valid {
		value := actualDuration.Int64
		item.ActualDurationUS = &value
	}
	item.OutputVideoRelativePath, item.OutputVideoSHA256 = outputPath.String, outputSHA.String
	item.ProviderRequestID, item.LeaseOwner = providerRequestID.String, leaseOwner.String
	item.LeaseExpiresAt = nullTimePointer(leaseExpiresAt)
	item.ErrorCode, item.ErrorMessage = errorCode.String, errorMessage.String
	item.StartedAt, item.FinishedAt = nullTimePointer(startedAt), nullTimePointer(finishedAt)
	return nil
}

func scanImageVideoAttempt(scanner imageVideoScanner, attempt *imagevideo.Attempt) error {
	var providerRequestID, errorCode, errorMessage, outputPath, outputSHA sql.NullString
	var startedAt, finishedAt sql.NullTime
	if err := scanner.Scan(&attempt.ID, &attempt.JobItemID, &attempt.RetryRound, &attempt.Attempt, &attempt.IdempotencyKey, &providerRequestID, &attempt.RequestFingerprint, &attempt.Status, &errorCode, &errorMessage, &outputPath, &outputSHA, &startedAt, &finishedAt, &attempt.CreatedAt, &attempt.UpdatedAt); err != nil {
		return err
	}
	attempt.ProviderRequestID, attempt.ErrorCode, attempt.ErrorMessage = providerRequestID.String, errorCode.String, errorMessage.String
	attempt.OutputVideoRelativePath, attempt.OutputVideoSHA256 = outputPath.String, outputSHA.String
	attempt.StartedAt, attempt.FinishedAt = nullTimePointer(startedAt), nullTimePointer(finishedAt)
	return nil
}

func validateCreateImageVideoJob(input imagevideo.CreateJob) error {
	if strings.TrimSpace(input.ProjectID) == "" || strings.TrimSpace(input.AccountID) == "" || strings.TrimSpace(input.TemplateVersion) == "" || !validSHA256(input.TemplateFingerprint) || strings.TrimSpace(input.IdempotencyKey) == "" {
		return fmt.Errorf("%w: incomplete image video job", imagevideo.ErrConflict)
	}
	if input.OutputMode != imagevideo.ModeSlideshow && input.OutputMode != imagevideo.ModeImageToVideo {
		return fmt.Errorf("%w: invalid output mode", imagevideo.ErrConflict)
	}
	if input.Concurrency < 1 || input.Concurrency > imagevideo.MaxVideoConcurrency || len(input.Items) == 0 {
		return fmt.Errorf("%w: invalid concurrency or empty items", imagevideo.ErrConflict)
	}
	ordinals := make(map[int]struct{}, len(input.Items))
	for _, item := range input.Items {
		if strings.TrimSpace(item.ImageProjectItemID) == "" || item.Ordinal <= 0 || item.TimelineDurationUS <= 0 || !validRelativeArtifactPath(item.InputImageRelativePath) || !validSHA256(item.InputImageSHA256) {
			return fmt.Errorf("%w: invalid image video item", imagevideo.ErrConflict)
		}
		if _, exists := ordinals[item.Ordinal]; exists {
			return fmt.Errorf("%w: duplicate item ordinal", imagevideo.ErrConflict)
		}
		ordinals[item.Ordinal] = struct{}{}
		if item.MaxAttempts < 0 || item.MaxAttempts > imagevideo.MaxAttemptsPerRound {
			return fmt.Errorf("%w: invalid max attempts", imagevideo.ErrConflict)
		}
		if input.OutputMode == imagevideo.ModeSlideshow && item.RequestedDurationSeconds != nil {
			return fmt.Errorf("%w: slideshow item cannot request generated video duration", imagevideo.ErrConflict)
		}
		if input.OutputMode == imagevideo.ModeImageToVideo && (item.RequestedDurationSeconds == nil || (*item.RequestedDurationSeconds != 6 && *item.RequestedDurationSeconds != 10 && *item.RequestedDurationSeconds != 15)) {
			return fmt.Errorf("%w: invalid generated video duration", imagevideo.ErrConflict)
		}
	}
	return nil
}

func sameCreateRequest(job imagevideo.Job, input imagevideo.CreateJob) bool {
	return (input.ID == "" || job.ID == input.ID) && job.ProjectID == input.ProjectID && job.OutputMode == input.OutputMode && job.AccountID == input.AccountID && job.TemplateVersion == input.TemplateVersion && strings.EqualFold(job.TemplateFingerprint, input.TemplateFingerprint) && job.Model == imagevideo.VideoModel && job.Resolution == imagevideo.VideoResolution && job.Concurrency == input.Concurrency
}

func sameCreateItems(ctx context.Context, q assetDBTX, jobID string, requested []imagevideo.CreateItem) (bool, error) {
	rows, err := q.QueryContext(ctx, `SELECT id,image_project_item_id,ordinal,timeline_duration_us,requested_duration_seconds,max_attempts,input_image_relative_path,input_image_sha256 FROM image_video_job_items WHERE job_id=? ORDER BY ordinal,id`, jobID)
	if err != nil {
		return false, err
	}
	type storedItem struct {
		id, projectItemID, path, sha string
		ordinal, maxAttempts         int
		timeline                     int64
		requested                    sql.NullInt64
	}
	stored := make(map[int]storedItem, len(requested))
	for rows.Next() {
		var item storedItem
		if err := rows.Scan(&item.id, &item.projectItemID, &item.ordinal, &item.timeline, &item.requested, &item.maxAttempts, &item.path, &item.sha); err != nil {
			rows.Close()
			return false, err
		}
		stored[item.ordinal] = item
	}
	if err := rows.Close(); err != nil {
		return false, err
	}
	if err := rows.Err(); err != nil || len(stored) != len(requested) {
		return false, err
	}
	for _, requestedItem := range requested {
		item, ok := stored[requestedItem.Ordinal]
		if !ok {
			return false, nil
		}
		maxAttempts := requestedItem.MaxAttempts
		if maxAttempts == 0 {
			maxAttempts = imagevideo.MaxAttemptsPerRound
		}
		if requestedItem.ID != "" && requestedItem.ID != item.id || requestedItem.ImageProjectItemID != item.projectItemID || requestedItem.TimelineDurationUS != item.timeline || maxAttempts != item.maxAttempts || normalizeRelativeArtifactPath(requestedItem.InputImageRelativePath) != item.path || !strings.EqualFold(requestedItem.InputImageSHA256, item.sha) {
			return false, nil
		}
		if requestedItem.RequestedDurationSeconds == nil {
			if item.requested.Valid {
				return false, nil
			}
		} else if !item.requested.Valid || int(item.requested.Int64) != *requestedItem.RequestedDurationSeconds {
			return false, nil
		}
	}
	return true, nil
}

func validSHA256(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validProviderRequestID(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len([]rune(value)) <= 256 && value != "." && value != ".." && !strings.ContainsAny(value, `/\\?#`)
}

func validRelativeArtifactPath(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || filepath.IsAbs(value) || filepath.VolumeName(value) != "" || strings.Contains(value, ":") {
		return false
	}
	normalized := strings.ReplaceAll(value, `\`, "/")
	if strings.HasPrefix(normalized, "/") {
		return false
	}
	for _, segment := range strings.Split(normalized, "/") {
		if segment == ".." {
			return false
		}
	}
	return filepath.Clean(filepath.FromSlash(normalized)) != "."
}

func normalizeRelativeArtifactPath(value string) string {
	return filepath.ToSlash(filepath.Clean(filepath.FromSlash(strings.ReplaceAll(strings.TrimSpace(value), `\`, "/"))))
}

func attemptMatchesClaim(attempt imagevideo.Attempt, claim imagevideo.ClaimedItem, current imagevideo.JobItem, now time.Time) bool {
	return current.JobID == claim.Job.ID && current.Status == string(imagevideo.JobRunning) && current.LeaseOwner == claim.Item.LeaseOwner && current.LeaseExpiresAt != nil && current.LeaseExpiresAt.After(now) && current.Version == attempt.ItemVersion && current.RetryRound == attempt.RetryRound && current.Attempt == attempt.Attempt && attempt.JobItemID == current.ID && claim.Item.ID == current.ID
}

func nullTimePointer(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time
	return &result
}

func timePointer(value time.Time) *time.Time {
	result := value
	return &result
}
