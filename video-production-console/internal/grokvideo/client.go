package grokvideo

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"video-production-console/internal/imagevideo"
	"video-production-console/internal/security"
	consoleSettings "video-production-console/internal/settings"
)

const (
	maxAPIResponseBytes = 1 << 20
	maxVideoBytes       = 256 << 20
	defaultHTTPTimeout  = 2 * time.Minute
)

var privateProviderURLPattern = regexp.MustCompile(`(?i)https?://[^\s"'<>]+`)

type Client interface {
	Submit(context.Context, SubmitRequest) (Request, error)
	Poll(context.Context, string) (Status, error)
	Download(context.Context, string, io.Writer) error
}

type SubmitRequest struct {
	Prompt        string
	Seconds       int
	ImageMIMEType string
	Image         []byte
}

type Request struct {
	ID     string
	Status string
}

type State string

const (
	StatePending State = "pending"
	StateDone    State = "done"
	StateFailed  State = "failed"
)

type Status struct {
	RequestID       string
	State           State
	Progress        int
	VideoURL        string
	DurationSeconds float64
	ErrorCode       string
	ErrorMessage    string
}

type ProviderError struct {
	Operation  string
	Code       string
	Message    string
	StatusCode int
	Retryable  bool
	Cause      error
}

func (e *ProviderError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("%s: %s", e.Operation, e.Message)
	}
	if e.StatusCode != 0 {
		return fmt.Sprintf("%s returned HTTP %d", e.Operation, e.StatusCode)
	}
	return e.Operation + " failed"
}

func (e *ProviderError) Unwrap() error { return e.Cause }

type HTTPClient struct {
	baseURL        string
	apiKey         string
	http           *http.Client
	downloadClient *http.Client
	redactor       *security.Redactor
}

func NewFromRuntime(runtime consoleSettings.Runtime, client *http.Client, redactor *security.Redactor) (*HTTPClient, error) {
	return New(runtime.GrokBaseURL, runtime.GrokAPIKey, client, redactor)
}

func New(baseURL, apiKey string, client *http.Client, redactor *security.Redactor) (*HTTPClient, error) {
	baseURL = strings.TrimSpace(baseURL)
	apiKey = strings.TrimSpace(apiKey)
	if _, err := apiEndpoint(baseURL, "videos/generations"); err != nil {
		return nil, err
	}
	if apiKey == "" {
		return nil, fmt.Errorf("Grok video API key is not configured")
	}
	if redactor == nil {
		redactor = security.NewRedactor()
	}
	redactor.Register(apiKey)
	if client == nil {
		client = &http.Client{Timeout: defaultHTTPTimeout}
	}
	apiClient := *client
	apiClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	downloadClient := apiClient
	downloadClient.Transport = publicVideoTransport(client.Transport)
	return &HTTPClient{baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, http: &apiClient, downloadClient: &downloadClient, redactor: redactor}, nil
}

func (c *HTTPClient) Submit(ctx context.Context, input SubmitRequest) (Request, error) {
	prompt := strings.TrimSpace(input.Prompt)
	if prompt == "" || (input.Seconds != 6 && input.Seconds != 10 && input.Seconds != 15) || len(input.Image) == 0 {
		return Request{}, &ProviderError{Operation: "submit Grok video", Code: "invalid_request", Message: "prompt, image, and a 6/10/15 second duration are required"}
	}
	mimeType := strings.ToLower(strings.TrimSpace(input.ImageMIMEType))
	switch mimeType {
	case "image/png", "image/jpeg", "image/webp":
	default:
		return Request{}, &ProviderError{Operation: "submit Grok video", Code: "invalid_image_type", Message: "input image must be PNG, JPEG, or WebP"}
	}
	payload := struct {
		Model          string `json:"model"`
		Prompt         string `json:"prompt"`
		Seconds        int    `json:"seconds"`
		Resolution     string `json:"resolution"`
		InputReference struct {
			ImageURL string `json:"image_url"`
		} `json:"input_reference"`
	}{Model: imagevideo.VideoModel, Prompt: prompt, Seconds: input.Seconds, Resolution: imagevideo.VideoResolution}
	payload.InputReference.ImageURL = "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(input.Image)
	var response struct {
		RequestID string `json:"request_id"`
		ID        string `json:"id"`
		Status    string `json:"status"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "videos/generations", payload, &response, "submit Grok video"); err != nil {
		return Request{}, err
	}
	requestID := strings.TrimSpace(response.RequestID)
	if requestID == "" {
		requestID = strings.TrimSpace(response.ID)
	}
	if !validRequestID(requestID) {
		return Request{}, &ProviderError{Operation: "submit Grok video", Code: "invalid_response", Message: "provider returned no valid request id"}
	}
	return Request{ID: requestID, Status: strings.ToLower(strings.TrimSpace(response.Status))}, nil
}

func (c *HTTPClient) Poll(ctx context.Context, requestID string) (Status, error) {
	requestID = strings.TrimSpace(requestID)
	if !validRequestID(requestID) {
		return Status{}, &ProviderError{Operation: "poll Grok video", Code: "invalid_request_id", Message: "request id is invalid"}
	}
	var response struct {
		ID        string          `json:"id"`
		RequestID string          `json:"request_id"`
		Status    string          `json:"status"`
		Progress  int             `json:"progress"`
		Error     json.RawMessage `json:"error"`
		Video     struct {
			URL      string  `json:"url"`
			Duration float64 `json:"duration"`
		} `json:"video"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "videos/"+url.PathEscape(requestID), nil, &response, "poll Grok video"); err != nil {
		return Status{}, err
	}
	result := Status{RequestID: requestID, Progress: response.Progress, DurationSeconds: response.Video.Duration}
	switch strings.ToLower(strings.TrimSpace(response.Status)) {
	case "pending", "queued", "processing", "running", "submitted":
		result.State = StatePending
	case "done", "completed", "succeeded", "success":
		result.State = StateDone
		result.VideoURL = strings.TrimSpace(response.Video.URL)
		if result.VideoURL == "" {
			return Status{}, &ProviderError{Operation: "poll Grok video", Code: "invalid_response", Message: "completed response contains no video URL"}
		}
	case "failed", "error", "canceled", "cancelled":
		result.State = StateFailed
		result.ErrorCode, result.ErrorMessage = providerErrorFields(response.Error)
		result.ErrorCode = c.safe(result.ErrorCode)
		result.ErrorMessage = c.safe(result.ErrorMessage)
		if result.ErrorCode == "" {
			result.ErrorCode = "provider_failed"
		}
		if result.ErrorMessage == "" {
			result.ErrorMessage = "Grok video generation failed"
		}
	default:
		return Status{}, &ProviderError{Operation: "poll Grok video", Code: "invalid_response", Message: "provider returned an unknown status"}
	}
	return result, nil
}

func (c *HTTPClient) Download(ctx context.Context, rawURL string, destination io.Writer) error {
	if destination == nil {
		return &ProviderError{Operation: "download Grok video", Code: "invalid_destination", Message: "download destination is nil"}
	}
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return &ProviderError{Operation: "download Grok video", Code: "invalid_url", Message: "video URL is invalid"}
	}
	if err := validatePublicVideoHost(ctx, net.DefaultResolver, parsed.Hostname()); err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return &ProviderError{Operation: "download Grok video", Code: "invalid_url", Message: "video download request is invalid", Cause: err}
	}
	request.Header.Set("Accept", "video/*, application/octet-stream")
	request.Header.Set("User-Agent", "video-production-console/1.0")
	response, err := c.downloadClient.Do(request)
	if err != nil {
		return c.networkError("download Grok video", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &ProviderError{Operation: "download Grok video", Code: "http_status", StatusCode: response.StatusCode, Retryable: retryableStatus(response.StatusCode)}
	}
	if response.ContentLength > maxVideoBytes {
		return &ProviderError{Operation: "download Grok video", Code: "video_too_large", Message: "video response exceeds the size limit"}
	}
	limited := io.LimitReader(response.Body, maxVideoBytes+1)
	prefix := make([]byte, 512)
	n, readErr := io.ReadFull(limited, prefix)
	if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		return c.networkError("download Grok video", readErr)
	}
	prefix = prefix[:n]
	if !looksLikeVideo(prefix) {
		return &ProviderError{Operation: "download Grok video", Code: "invalid_video", Message: "download response is not a video"}
	}
	written, err := io.Copy(destination, io.MultiReader(bytes.NewReader(prefix), limited))
	if err != nil {
		return c.networkError("download Grok video", err)
	}
	if written > maxVideoBytes {
		return &ProviderError{Operation: "download Grok video", Code: "video_too_large", Message: "downloaded video exceeds the size limit"}
	}
	if written == 0 {
		return &ProviderError{Operation: "download Grok video", Code: "empty_video", Message: "downloaded video is empty"}
	}
	return nil
}

func (c *HTTPClient) doJSON(ctx context.Context, method, route string, payload any, output any, operation string) error {
	endpoint, err := apiEndpoint(c.baseURL, route)
	if err != nil {
		return &ProviderError{Operation: operation, Code: "invalid_endpoint", Message: err.Error()}
	}
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return &ProviderError{Operation: operation, Code: "encode_request", Message: "request could not be encoded", Cause: err}
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return &ProviderError{Operation: operation, Code: "invalid_request", Message: "request could not be created", Cause: err}
	}
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "video-production-console/1.0")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return c.networkError(operation, err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxAPIResponseBytes+1))
	if err != nil {
		return c.networkError(operation, err)
	}
	if len(responseBody) > maxAPIResponseBytes {
		return &ProviderError{Operation: operation, Code: "response_too_large", Message: "provider response exceeds the size limit"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, message := providerErrorFields(responseBody)
		return &ProviderError{Operation: operation, Code: "http_status", Message: c.safe(message), StatusCode: response.StatusCode, Retryable: retryableStatus(response.StatusCode)}
	}
	if err := json.Unmarshal(responseBody, output); err != nil {
		return &ProviderError{Operation: operation, Code: "invalid_response", Message: "provider returned invalid JSON", Cause: err}
	}
	return nil
}

func (c *HTTPClient) networkError(operation string, err error) error {
	// Transport errors may include the complete provider URL or query string;
	// persist only a stable, non-sensitive diagnostic.
	return &ProviderError{Operation: operation, Code: "network_error", Message: "provider network request failed", Retryable: retryableNetworkError(err), Cause: err}
}

func (c *HTTPClient) safe(value string) string {
	value = c.redactor.Redact(value)
	value = privateProviderURLPattern.ReplaceAllString(value, "<provider-url>")
	runes := []rune(value)
	if len(runes) > 2000 {
		value = string(runes[:2000]) + "…"
	}
	return value
}

func IsRetryable(err error) bool {
	var providerErr *ProviderError
	return errors.As(err, &providerErr) && providerErr.Retryable
}

func apiEndpoint(baseURL, route string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("Grok video base URL is invalid")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/" + strings.TrimLeft(route, "/")
	parsed.RawPath = ""
	return parsed.String(), nil
}

func validRequestID(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && value != "." && value != ".." && !strings.ContainsAny(value, `/\\?#`)
}

func providerErrorFields(raw json.RawMessage) (string, string) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", ""
	}
	var envelope struct {
		Code    string `json:"code"`
		Type    string `json:"type"`
		Message string `json:"message"`
		Error   struct {
			Code    string `json:"code"`
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &envelope) == nil {
		code, message := envelope.Code, envelope.Message
		if code == "" {
			code = envelope.Type
		}
		if envelope.Error.Code != "" {
			code = envelope.Error.Code
		} else if envelope.Error.Type != "" {
			code = envelope.Error.Type
		}
		if envelope.Error.Message != "" {
			message = envelope.Error.Message
		}
		return code, message
	}
	var message string
	if json.Unmarshal(raw, &message) == nil {
		return "", message
	}
	return "", "provider returned an error"
}

func retryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusRequestTimeout || status >= 500
}

func retryableNetworkError(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) {
		return true
	}
	var networkError net.Error
	return errors.As(err, &networkError) && (networkError.Timeout() || networkError.Temporary())
}

func looksLikeVideo(prefix []byte) bool {
	if len(prefix) >= 8 && string(prefix[4:8]) == "ftyp" {
		return true
	}
	if len(prefix) >= 4 && prefix[0] == 0x1a && prefix[1] == 0x45 && prefix[2] == 0xdf && prefix[3] == 0xa3 {
		return true
	}
	// Content-Type is attacker-controlled and cannot establish that the body is
	// a video. Require an ISO BMFF (ftyp) or EBML signature above instead.
	return false
}

type ipResolver interface {
	LookupIP(context.Context, string, string) ([]net.IP, error)
}

func validatePublicVideoHost(ctx context.Context, resolver ipResolver, hostname string) error {
	_, err := publicVideoIPs(ctx, resolver, hostname)
	return err
}

func publicVideoIPs(ctx context.Context, resolver ipResolver, hostname string) ([]net.IP, error) {
	addresses := []net.IP{}
	if address := net.ParseIP(hostname); address != nil {
		addresses = append(addresses, address)
	} else {
		resolved, err := resolver.LookupIP(ctx, "ip", hostname)
		if err != nil || len(resolved) == 0 {
			return nil, &ProviderError{Operation: "download Grok video", Code: "host_resolution", Message: "video URL host could not be resolved", Retryable: true, Cause: err}
		}
		addresses = resolved
	}
	for _, address := range addresses {
		if !isPublicVideoIP(address) {
			return nil, &ProviderError{Operation: "download Grok video", Code: "blocked_host", Message: "video URL host is not allowed"}
		}
	}
	return addresses, nil
}

func publicVideoTransport(base http.RoundTripper) http.RoundTripper {
	transport, ok := base.(*http.Transport)
	if !ok && base != nil {
		// A custom RoundTripper owns DNS resolution and connection reuse, so the
		// preflight lookup in Download cannot close the DNS-rebinding window.
		// Reject it rather than claiming the public-host guarantee is enforced.
		return publicVideoRoundTripper{base: base, reject: true}
	}
	if transport == nil {
		transport = http.DefaultTransport.(*http.Transport)
	}
	clone := transport.Clone()
	baseDialContext := clone.DialContext
	if baseDialContext == nil {
		baseDialContext = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	}
	clone.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		hostname, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, &ProviderError{Operation: "download Grok video", Code: "invalid_address", Message: "video download address is invalid"}
		}
		addresses, err := publicVideoIPs(ctx, net.DefaultResolver, hostname)
		if err != nil {
			return nil, err
		}
		var lastErr error
		for _, resolved := range addresses {
			connection, dialErr := baseDialContext(ctx, network, net.JoinHostPort(resolved.String(), port))
			if dialErr == nil {
				return connection, nil
			}
			lastErr = dialErr
		}
		return nil, &ProviderError{Operation: "download Grok video", Code: "connection_failed", Message: "video download connection failed", Retryable: true, Cause: lastErr}
	}
	return clone
}

type publicVideoRoundTripper struct {
	base   http.RoundTripper
	reject bool
}

func (transport publicVideoRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if transport.reject {
		return nil, &ProviderError{Operation: "download Grok video", Code: "insecure_transport", Message: "video download transport is not supported"}
	}
	if err := validatePublicVideoHost(request.Context(), net.DefaultResolver, request.URL.Hostname()); err != nil {
		return nil, err
	}
	return transport.base.RoundTrip(request)
}

func isPublicVideoIP(address net.IP) bool {
	if address == nil || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.IsUnspecified() || address.IsMulticast() {
		return false
	}
	for _, network := range specialVideoNetworks {
		if network.Contains(address) {
			return false
		}
	}
	return true
}

var specialVideoNetworks = mustVideoNetworks(
	"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24",
	"198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4",
	"2001:db8::/32", "2001:10::/28", "2002::/16", "fc00::/7", "fe80::/10",
)

func mustVideoNetworks(values ...string) []*net.IPNet {
	networks := make([]*net.IPNet, 0, len(values))
	for _, value := range values {
		_, network, err := net.ParseCIDR(value)
		if err != nil {
			panic(err)
		}
		networks = append(networks, network)
	}
	return networks
}
