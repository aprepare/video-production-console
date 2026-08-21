package imagevideo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	defaultWorkerLease = 30 * time.Minute
	maxImageInputBytes = 32 << 20
)

// processVideoSemaphore is shared by every Worker in this process. The
// repository also enforces the same limit transactionally, but this guard
// prevents a burst of locally claimed items from issuing more than six
// provider/media calls while claims are being processed concurrently.
var processVideoSemaphore = make(chan struct{}, MaxVideoConcurrency)

var imageVideoPrivateURLPattern = regexp.MustCompile(`(?i)https?://[^\s"'<>]+`)

type ImageVideoJobRepository interface {
	ClaimNextItem(context.Context, string, time.Time, time.Duration) (ClaimedItem, error)
	BeginAttempt(context.Context, ClaimedItem, string) (Attempt, error)
	SaveProviderRequestID(context.Context, ClaimedItem, Attempt, string) (Attempt, error)
	CompleteAttempt(context.Context, AttemptSuccess) error
	CompleteSlideshowItem(context.Context, ClaimedItem) error
	FailAttempt(context.Context, AttemptFailure) (bool, error)
	RequeueExpired(context.Context, time.Time) (int64, error)
	ReconcileMediaJobs(context.Context, time.Time) ([]string, error)
}

type WorkerConfig struct {
	Repository     ImageVideoJobRepository
	Provider       VideoProvider
	Media          MediaProcessor
	DataRoot       string
	Owner          string
	Lease          time.Duration
	MaxConcurrency int
	PollFirstDelay time.Duration
	PollNextDelay  time.Duration
	Sleep          func(context.Context, time.Duration) error
	Now            func() time.Time
	DraftReady     func(context.Context, string) error
}

type Worker struct {
	repository     ImageVideoJobRepository
	provider       VideoProvider
	media          MediaProcessor
	dataRoot       string
	owner          string
	lease          time.Duration
	maxConcurrency int
	pollFirst      time.Duration
	pollNext       time.Duration
	sleep          func(context.Context, time.Duration) error
	now            func() time.Time
	draftReady     func(context.Context, string) error
}

func NewWorker(config WorkerConfig) (*Worker, error) {
	if config.Repository == nil || strings.TrimSpace(config.DataRoot) == "" || strings.TrimSpace(config.Owner) == "" {
		return nil, fmt.Errorf("image video worker repository, data root, and owner are required")
	}
	if config.Lease <= 0 {
		config.Lease = defaultWorkerLease
	}
	if config.MaxConcurrency < 1 || config.MaxConcurrency > MaxVideoConcurrency {
		config.MaxConcurrency = MaxVideoConcurrency
	}
	if config.PollFirstDelay <= 0 {
		config.PollFirstDelay = 2 * time.Second
	}
	if config.PollNextDelay <= 0 {
		config.PollNextDelay = 5 * time.Second
	}
	if config.Sleep == nil {
		config.Sleep = waitForDuration
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Worker{
		repository: config.Repository, provider: config.Provider, media: config.Media,
		dataRoot: config.DataRoot, owner: config.Owner, lease: config.Lease,
		maxConcurrency: config.MaxConcurrency, pollFirst: config.PollFirstDelay,
		pollNext: config.PollNextDelay, sleep: config.Sleep, now: config.Now,
		draftReady: config.DraftReady,
	}, nil
}

func waitForDuration(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (w *Worker) Run(ctx context.Context) error {
	for {
		didWork, err := w.RunOnce(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !didWork {
			if err := w.sleep(ctx, time.Second); err != nil {
				return err
			}
		}
	}
}

func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	now := w.now()
	if _, err := w.repository.RequeueExpired(ctx, now); err != nil {
		return false, err
	}
	ready, err := w.repository.ReconcileMediaJobs(ctx, now)
	if err != nil {
		return false, err
	}
	if err := w.notifyDraftReady(ctx, ready); err != nil {
		return false, err
	}
	didWork := len(ready) > 0
	claims := make([]ClaimedItem, 0, w.maxConcurrency)
	for len(claims) < w.maxConcurrency {
		claim, claimErr := w.repository.ClaimNextItem(ctx, w.owner, w.now(), w.lease)
		if errors.Is(claimErr, ErrNoClaimableItem) {
			break
		}
		if claimErr != nil {
			return didWork, claimErr
		}
		claims = append(claims, claim)
	}
	if len(claims) == 0 {
		return didWork, nil
	}
	didWork = true
	var wait sync.WaitGroup
	errCh := make(chan error, len(claims))
	for _, claim := range claims {
		claim := claim
		wait.Add(1)
		go func() {
			defer wait.Done()
			if err := w.processClaim(ctx, claim); err != nil && !errors.Is(err, context.Canceled) {
				errCh <- err
			}
		}()
	}
	wait.Wait()
	close(errCh)
	for err := range errCh {
		return didWork, err
	}
	ready, err = w.repository.ReconcileMediaJobs(ctx, w.now())
	if err != nil {
		return didWork, err
	}
	if err := w.notifyDraftReady(ctx, ready); err != nil {
		return didWork, err
	}
	return true, nil
}

func (w *Worker) notifyDraftReady(ctx context.Context, jobIDs []string) error {
	if w.draftReady == nil {
		return nil
	}
	for _, jobID := range jobIDs {
		if err := w.draftReady(ctx, jobID); err != nil {
			return err
		}
	}
	return nil
}

func (w *Worker) processClaim(ctx context.Context, claim ClaimedItem) error {
	if claim.Job.OutputMode == ModeSlideshow {
		return w.repository.CompleteSlideshowItem(ctx, claim)
	}
	if claim.Job.OutputMode != ModeImageToVideo {
		return fmt.Errorf("unsupported image video output mode %q", claim.Job.OutputMode)
	}
	if w.provider == nil || w.media == nil {
		return w.failWithoutAttempt(ctx, claim, "image_video_provider_not_configured", "图生视频服务未配置", false)
	}
	select {
	case processVideoSemaphore <- struct{}{}:
		defer func() { <-processVideoSemaphore }()
	case <-ctx.Done():
		return ctx.Err()
	}
	requestFingerprint := videoRequestFingerprint(claim)
	attempt, err := w.repository.BeginAttempt(ctx, claim, requestFingerprint)
	if err != nil {
		return err
	}
	image, mimeType, readErr := readManagedImage(w.dataRoot, claim.Job.ProjectID, claim.Item)
	if readErr != nil {
		return w.failAttempt(ctx, claim, attempt, readErr, false)
	}
	requestID := strings.TrimSpace(attempt.ProviderRequestID)
	if requestID == "" {
		request, submitErr := w.provider.Submit(ctx, VideoSubmitInput{
			Paragraph:     claim.Item.SourceText,
			ImageTitle:    claim.Item.Title,
			MotionHint:    claim.Item.Motion,
			Seconds:       claim.Item.RequestedDurationSecondsValue(),
			ImageMIMEType: mimeType,
			Image:         image,
		})
		if submitErr != nil {
			return w.failAttempt(ctx, claim, attempt, submitErr, w.provider.Retryable(submitErr))
		}
		requestID = strings.TrimSpace(request.ID)
		if requestID == "" {
			return w.failAttempt(ctx, claim, attempt, errors.New("provider returned no request id"), false)
		}
		attempt, err = w.repository.SaveProviderRequestID(ctx, claim, attempt, requestID)
		if err != nil {
			return err
		}
	}
	status, pollErr := w.pollUntilDone(ctx, requestID)
	if pollErr != nil {
		return w.failAttempt(ctx, claim, attempt, pollErr, w.provider.Retryable(pollErr))
	}
	if status.State == VideoStateFailed {
		return w.failAttempt(ctx, claim, attempt, fmt.Errorf("%s: %s", status.ErrorCode, status.ErrorMessage), status.Retryable)
	}
	if status.State != VideoStateDone || strings.TrimSpace(status.VideoURL) == "" {
		return w.failAttempt(ctx, claim, attempt, errors.New("provider returned no completed video"), false)
	}

	rawRelative := filepath.ToSlash(filepath.Join(claim.Job.ID, "raw", claim.Item.ID+fmt.Sprintf("-%d-%d.video", attempt.RetryRound, attempt.Attempt)))
	_, rawPath, err := ResolveManagedPath(w.dataRoot, claim.Job.ProjectID, rawRelative)
	if err != nil {
		return w.failAttempt(ctx, claim, attempt, err, false)
	}
	if err := os.MkdirAll(filepath.Dir(rawPath), 0o700); err != nil {
		return w.failAttempt(ctx, claim, attempt, err, false)
	}
	rawFile, err := os.OpenFile(rawPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return w.failAttempt(ctx, claim, attempt, err, false)
	}
	downloadErr := w.provider.Download(ctx, status.VideoURL, rawFile)
	if closeErr := rawFile.Close(); downloadErr == nil {
		downloadErr = closeErr
	}
	if downloadErr != nil {
		_ = os.Remove(rawPath)
		return w.failAttempt(ctx, claim, attempt, downloadErr, w.provider.Retryable(downloadErr))
	}
	defer os.Remove(rawPath)

	outputRelative := filepath.ToSlash(filepath.Join(claim.Job.ID, "media", claim.Item.ID+".mp4"))
	_, outputPath, err := ResolveManagedPath(w.dataRoot, claim.Job.ProjectID, outputRelative)
	if err != nil {
		return w.failAttempt(ctx, claim, attempt, err, false)
	}
	probe, normalizeErr := w.media.NormalizeAndVerify(ctx, rawPath, outputPath, claim.Item.RequestedDurationSecondsValue())
	if normalizeErr != nil {
		return w.failAttempt(ctx, claim, attempt, normalizeErr, true)
	}
	outputSHA, hashErr := sha256File(outputPath)
	if hashErr != nil {
		return w.failAttempt(ctx, claim, attempt, hashErr, true)
	}
	return w.repository.CompleteAttempt(ctx, AttemptSuccess{
		Claim: claim, Attempt: attempt, ProviderRequestID: requestID,
		OutputVideoRelativePath: outputRelative, OutputVideoSHA256: outputSHA,
		ActualDurationUS: probe.DurationMS * 1000,
	})
}

func (w *Worker) pollUntilDone(ctx context.Context, requestID string) (VideoStatus, error) {
	first := true
	for {
		status, err := w.provider.Poll(ctx, requestID)
		if err != nil {
			return status, err
		}
		switch status.State {
		case VideoStateDone, VideoStateFailed:
			return status, nil
		case VideoStatePending, "queued", "processing", "running", "submitted", "":
			if first {
				first = false
				if err := w.sleep(ctx, w.pollFirst); err != nil {
					return status, err
				}
			} else if err := w.sleep(ctx, w.pollNext); err != nil {
				return status, err
			}
		default:
			return status, fmt.Errorf("provider returned unknown video status %q", status.State)
		}
	}
}

func (w *Worker) failAttempt(ctx context.Context, claim ClaimedItem, attempt Attempt, err error, retryable bool) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) && ctx.Err() != nil {
		return err
	}
	code := "image_video_attempt_failed"
	message := strings.ReplaceAll(err.Error(), w.dataRoot, "<data-root>")
	message = strings.ReplaceAll(message, "Authorization: Bearer ", "Authorization: Bearer [REDACTED]")
	message = imageVideoPrivateURLPattern.ReplaceAllString(message, "<provider-url>")
	var providerFailure interface{ ErrorCode() string }
	if errors.As(err, &providerFailure) && providerFailure.ErrorCode() != "" {
		code = providerFailure.ErrorCode()
	}
	_, failErr := w.repository.FailAttempt(ctx, AttemptFailure{Claim: claim, Attempt: attempt, ErrorCode: code, ErrorMessage: message, Retryable: retryable})
	return failErr
}

func (w *Worker) failWithoutAttempt(ctx context.Context, claim ClaimedItem, code, message string, retryable bool) error {
	attempt, err := w.repository.BeginAttempt(ctx, claim, videoRequestFingerprint(claim))
	if err != nil {
		return err
	}
	return w.failAttempt(ctx, claim, attempt, errors.New(message), retryable)
}

func videoRequestFingerprint(claim ClaimedItem) string {
	payload, _ := json.Marshal(struct {
		Project, Item, ImageSHA, Text, Title, Motion string
		Seconds                                      *int
	}{claim.Job.ProjectID, claim.Item.ImageProjectItemID, claim.Item.InputImageSHA256, claim.Item.SourceText, claim.Item.Title, claim.Item.Motion, claim.Item.RequestedDurationSeconds})
	hash := sha256.Sum256(payload)
	return hex.EncodeToString(hash[:])
}

func readManagedImage(dataRoot, projectID string, item JobItem) ([]byte, string, error) {
	_, path, err := ResolveProjectPath(dataRoot, projectID, item.InputImageRelativePath)
	if err != nil {
		return nil, "", err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer file.Close()
	limited := io.LimitReader(file, maxImageInputBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, "", err
	}
	if len(data) == 0 || len(data) > maxImageInputBytes {
		return nil, "", fmt.Errorf("image input exceeds the size limit")
	}
	actualSHA := sha256.Sum256(data)
	if strings.TrimSpace(item.InputImageSHA256) != "" && !strings.EqualFold(hex.EncodeToString(actualSHA[:]), item.InputImageSHA256) {
		return nil, "", fmt.Errorf("image input fingerprint does not match the job snapshot")
	}
	if len(data) < 12 {
		return nil, "", fmt.Errorf("image input is invalid")
	}
	detectedMIME := strings.ToLower(strings.TrimSpace(http.DetectContentType(data[:minInt(len(data), 512)])))
	declaredMIME := strings.ToLower(strings.TrimSpace(item.InputImageMIMEType))
	if declaredMIME != "" && declaredMIME != detectedMIME {
		return nil, "", fmt.Errorf("image input MIME type does not match its bytes")
	}
	mimeType := detectedMIME
	switch detectedMIME {
	case "image/png", "image/jpeg", "image/webp":
	default:
		return nil, "", fmt.Errorf("image input type %q is unsupported", mimeType)
	}
	return data, mimeType, nil
}

func sha256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (item JobItem) RequestedDurationSecondsValue() int {
	if item.RequestedDurationSeconds == nil {
		return 0
	}
	return *item.RequestedDurationSeconds
}
