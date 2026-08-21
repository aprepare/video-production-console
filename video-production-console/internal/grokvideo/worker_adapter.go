package grokvideo

import (
	"context"
	"io"
	"strings"

	"video-production-console/internal/imagevideo"
)

// WorkerProvider adapts the provider-specific client to the imagevideo worker
// contract without making the domain package depend on this transport layer.
type WorkerProvider struct {
	Client *HTTPClient
}

func (p WorkerProvider) Submit(ctx context.Context, input imagevideo.VideoSubmitInput) (imagevideo.VideoRequest, error) {
	if p.Client == nil {
		return imagevideo.VideoRequest{}, &ProviderError{Operation: "submit Grok video", Code: "not_configured", Message: "Grok video client is not configured"}
	}
	prompt := strings.TrimSpace(input.Prompt)
	if prompt == "" {
		var err error
		prompt, err = BuildMotionPrompt(MotionPromptInput{Paragraph: input.Paragraph, ImageTitle: input.ImageTitle, MotionHint: input.MotionHint})
		if err != nil {
			return imagevideo.VideoRequest{}, &ProviderError{Operation: "build Grok video prompt", Code: "invalid_prompt", Message: err.Error()}
		}
	}
	request, err := p.Client.Submit(ctx, SubmitRequest{Prompt: prompt, Seconds: input.Seconds, ImageMIMEType: input.ImageMIMEType, Image: input.Image})
	if err != nil {
		return imagevideo.VideoRequest{}, err
	}
	return imagevideo.VideoRequest{ID: request.ID, Status: request.Status}, nil
}

func (p WorkerProvider) Poll(ctx context.Context, requestID string) (imagevideo.VideoStatus, error) {
	if p.Client == nil {
		return imagevideo.VideoStatus{}, &ProviderError{Operation: "poll Grok video", Code: "not_configured", Message: "Grok video client is not configured"}
	}
	status, err := p.Client.Poll(ctx, requestID)
	if err != nil {
		return imagevideo.VideoStatus{}, err
	}
	result := imagevideo.VideoStatus{VideoURL: status.VideoURL, DurationSeconds: status.DurationSeconds, ErrorCode: status.ErrorCode, ErrorMessage: status.ErrorMessage}
	switch status.State {
	case StateDone:
		result.State = imagevideo.VideoStateDone
	case StateFailed:
		result.State = imagevideo.VideoStateFailed
		result.Retryable = status.ErrorCode != "invalid_response" && status.ErrorCode != "invalid_request"
	default:
		result.State = imagevideo.VideoStatePending
	}
	return result, nil
}

func (p WorkerProvider) Download(ctx context.Context, rawURL string, destination io.Writer) error {
	if p.Client == nil {
		return &ProviderError{Operation: "download Grok video", Code: "not_configured", Message: "Grok video client is not configured"}
	}
	return p.Client.Download(ctx, rawURL, destination)
}

func (p WorkerProvider) Retryable(err error) bool { return IsRetryable(err) }
