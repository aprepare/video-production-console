package runninghub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"video-production-console/internal/security"
)

const (
	defaultBaseURL  = "https://www.runninghub.ai"
	uploadPath      = "/openapi/v2/media/upload/binary"
	runPath         = "/task/openapi/ai-app/run"
	queryPath       = "/openapi/v2/query"
	maxResponseBody = 8 << 20
)

type Client struct {
	HTTP    *http.Client
	BaseURL string
}

type Result struct {
	OutputType string `json:"outputType"`
	URL        string `json:"url"`
}

type Status struct {
	State     string          `json:"status"`
	Results   []Result        `json:"results"`
	ErrorCode string          `json:"errorCode"`
	Usage     json.RawMessage `json:"usage"`
}

func (s Status) VideoURL() string {
	for _, result := range s.Results {
		if strings.EqualFold(result.OutputType, "mp4") {
			return result.URL
		}
	}
	return ""
}

type ProviderError struct {
	Operation  string
	StatusCode int
	Code       string
	DisableKey bool
	message    string
}

func (e *ProviderError) Error() string {
	parts := []string{"RunningHub " + e.Operation + " failed"}
	if e.StatusCode != 0 {
		parts = append(parts, fmt.Sprintf("HTTP %d", e.StatusCode))
	}
	if e.Code != "" {
		parts = append(parts, "code "+e.Code)
	}
	if e.message != "" {
		parts = append(parts, e.message)
	}
	return strings.Join(parts, ": ")
}

func ShouldDisable(err error) bool {
	var provider *ProviderError
	return errors.As(err, &provider) && provider.DisableKey
}

func (c Client) Upload(ctx context.Context, key, filename string, data []byte) (string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", path.Base(strings.ReplaceAll(filename, `\`, "/")))
	if err != nil {
		return "", errors.New("RunningHub upload request could not be created")
	}
	if _, err = part.Write(data); err != nil {
		return "", errors.New("RunningHub upload request could not be created")
	}
	if err = writer.Close(); err != nil {
		return "", errors.New("RunningHub upload request could not be created")
	}
	var response struct {
		Data struct {
			FileName string `json:"fileName"`
			Filename string `json:"filename"`
		} `json:"data"`
	}
	if err = c.doJSON(ctx, http.MethodPost, uploadPath, key, &body, writer.FormDataContentType(), &response); err != nil {
		return "", err
	}
	name := response.Data.FileName
	if name == "" {
		name = response.Data.Filename
	}
	if strings.TrimSpace(name) == "" {
		return "", &ProviderError{Operation: "upload", message: "response omitted file name"}
	}
	return name, nil
}

func (c Client) RunApp(ctx context.Context, key, webappID string, nodes []Node, instanceType string) (string, error) {
	if strings.TrimSpace(instanceType) == "" {
		instanceType = "default"
	}
	payload, err := json.Marshal(struct {
		WebappID     string `json:"webappId"`
		APIKey       string `json:"apiKey"`
		InstanceType string `json:"instanceType"`
		Nodes        []Node `json:"nodeInfoList"`
	}{webappID, key, instanceType, nodes})
	if err != nil {
		return "", errors.New("RunningHub run request could not be created")
	}
	var response struct {
		Data struct {
			TaskID string `json:"taskId"`
		} `json:"data"`
	}
	if err = c.doJSON(ctx, http.MethodPost, runPath, key, bytes.NewReader(payload), "application/json", &response); err != nil {
		return "", err
	}
	if strings.TrimSpace(response.Data.TaskID) == "" {
		return "", &ProviderError{Operation: "run", message: "response omitted task id"}
	}
	return response.Data.TaskID, nil
}

func (c Client) Query(ctx context.Context, key, taskID string) (Status, error) {
	payload, _ := json.Marshal(struct {
		TaskID string `json:"taskId"`
	}{taskID})
	var status Status
	if err := c.doJSON(ctx, http.MethodPost, queryPath, key, bytes.NewReader(payload), "application/json", &status); err != nil {
		return Status{}, err
	}
	if len(status.Usage) != 0 {
		status.Usage = json.RawMessage(redactString(string(status.Usage), key))
	}
	if status.ErrorCode != "" {
		status.ErrorCode = safeProviderCode(0, status.ErrorCode, "")
		return status, &ProviderError{Operation: "query", Code: status.ErrorCode, DisableKey: disableProviderCode(status.ErrorCode)}
	}
	return status, nil
}

func (c Client) Download(ctx context.Context, rawURL string, destination io.Writer, limit int64) error {
	if limit <= 0 {
		return errors.New("RunningHub download limit must be positive")
	}
	if err := validatePublicURL(rawURL); err != nil {
		return errors.New("RunningHub download URL is unsafe")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return errors.New("RunningHub download URL is invalid")
	}
	client := c.httpClient(true)
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("RunningHub download failed: %w", safeTransportError(err))
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &ProviderError{Operation: "download", StatusCode: response.StatusCode}
	}
	written, err := io.Copy(destination, io.LimitReader(response.Body, limit+1))
	if err != nil {
		return errors.New("RunningHub download failed while reading response")
	}
	if written > limit {
		return errors.New("RunningHub download exceeded size limit")
	}
	return nil
}

func (c Client) doJSON(ctx context.Context, method, endpoint, key string, body io.Reader, contentType string, target any) error {
	base := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	if base == "" {
		base = defaultBaseURL
	}
	request, err := http.NewRequestWithContext(ctx, method, base+endpoint, body)
	if err != nil {
		return errors.New("RunningHub request could not be created")
	}
	request.Header.Set("Authorization", "Bearer "+key)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := c.httpClient(false).Do(request)
	if err != nil {
		return fmt.Errorf("RunningHub request failed: %w", redactError(err, key))
	}
	defer response.Body.Close()
	raw, tooLarge, err := readLimited(response.Body, maxResponseBody)
	if err != nil {
		return errors.New("RunningHub response could not be read")
	}
	if tooLarge {
		return errors.New("RunningHub response too large")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var upstream struct {
			Code      any    `json:"code"`
			ErrorCode any    `json:"errorCode"`
			Message   string `json:"msg"`
		}
		_ = json.Unmarshal(raw, &upstream)
		code := fmt.Sprint(upstream.ErrorCode)
		if code == "<nil>" || code == "" {
			code = fmt.Sprint(upstream.Code)
		}
		if code == "<nil>" {
			code = ""
		}
		return providerError(strings.Trim(endpoint, "/"), response.StatusCode, code, redactString(upstream.Message, key))
	}
	if businessErr := decodeBusinessError(raw, strings.Trim(endpoint, "/"), key); businessErr != nil {
		return businessErr
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return errors.New("RunningHub response was invalid JSON")
	}
	return nil
}

func (c Client) httpClient(download bool) *http.Client {
	base := c.HTTP
	if base == nil {
		base = &http.Client{}
	}
	clone := *base
	if download {
		clone.Transport = publicDownloadTransport(base.Transport)
		clone.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			return validatePublicURL(req.URL.String())
		}
	} else {
		clone.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("redirect blocked") }
	}
	return &clone
}

func readLimited(reader io.Reader, limit int64) ([]byte, bool, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, limit+1))
	return raw, int64(len(raw)) > limit, err
}

func validatePublicURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.Hostname() == "" {
		return errors.New("invalid URL")
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if host == "localhost" {
		return errors.New("private host")
	}
	if ip := net.ParseIP(host); ip != nil {
		if !publicIP(ip) {
			return errors.New("private IP")
		}
		return nil
	}
	lookupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	addresses, err := net.DefaultResolver.LookupIPAddr(lookupCtx, host)
	if err != nil || len(addresses) == 0 {
		return errors.New("host could not be resolved")
	}
	for _, address := range addresses {
		if !publicIP(address.IP) {
			return errors.New("host resolves to private IP")
		}
	}
	return nil
}

func publicIP(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	for _, network := range nonPublicNetworks {
		if network.Contains(ip) {
			return false
		}
	}
	return true
}

var nonPublicNetworks = mustNetworks(
	"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24",
	"198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4",
	"2001:db8::/32", "2001:10::/28", "2002::/16", "fc00::/7", "fe80::/10",
)

func mustNetworks(values ...string) []*net.IPNet {
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

func providerError(operation string, status int, code, message string) error {
	safeCode := safeProviderCode(status, code, message)
	return &ProviderError{Operation: operation, StatusCode: status, Code: safeCode, DisableKey: disableProviderCode(safeCode)}
}

func decodeBusinessError(raw []byte, operation, key string) error {
	var envelope struct {
		Code    json.RawMessage `json:"code"`
		Msg     string          `json:"msg"`
		Message string          `json:"message"`
	}
	if json.Unmarshal(raw, &envelope) != nil || len(envelope.Code) == 0 {
		return nil
	}
	code := strings.Trim(strings.TrimSpace(string(envelope.Code)), `"`)
	switch strings.ToUpper(code) {
	case "", "0", "200", "SUCCESS", "OK":
		return nil
	}
	message := envelope.Msg
	if message == "" {
		message = envelope.Message
	}
	return providerError(operation, 0, code, redactString(message, key))
}

func safeProviderCode(status int, code, message string) string {
	numericCode := strings.Trim(strings.TrimSpace(code), `"`)
	if status == http.StatusUnauthorized || status == http.StatusForbidden || numericCode == "401" || numericCode == "403" {
		return "auth_failed"
	}
	if status == http.StatusPaymentRequired || numericCode == "402" {
		return "balance_insufficient"
	}
	lower := strings.ToLower(code + " " + message)
	switch {
	case strings.Contains(lower, "auth") || strings.Contains(lower, "unauthorized") || strings.Contains(lower, "invalid_key") || strings.Contains(lower, "api key") || strings.Contains(lower, "apikey") || strings.Contains(lower, "鉴权") || strings.Contains(lower, "密钥"):
		return "auth_failed"
	case strings.Contains(lower, "balance") || strings.Contains(lower, "insufficient") || strings.Contains(lower, "quota") || strings.Contains(lower, "余额") || strings.Contains(lower, "额度"):
		return "balance_insufficient"
	case status == http.StatusTooManyRequests || strings.Contains(lower, "rate") || strings.Contains(lower, "频率"):
		return "rate_limited"
	case strings.Contains(lower, "prompt_too_long") || strings.Contains(lower, "prompt too long") || strings.Contains(lower, "提示词过长") || (strings.Contains(lower, "prompt") && strings.Contains(lower, "length")):
		return "prompt_too_long"
	case status == http.StatusRequestTimeout || status == http.StatusGatewayTimeout || strings.Contains(lower, "timeout") || strings.Contains(lower, "timed out") || strings.Contains(lower, "超时"):
		return "timeout"
	case strings.Contains(lower, "content") || strings.Contains(lower, "moderation") || strings.Contains(lower, "违规"):
		return "content_rejected"
	default:
		return "provider_error"
	}
}

func disableProviderCode(code string) bool {
	return code == "auth_failed" || code == "balance_insufficient"
}

func publicDownloadTransport(base http.RoundTripper) http.RoundTripper {
	transport, ok := base.(*http.Transport)
	if !ok && base != nil {
		return rejectingDownloadTransport{}
	}
	if transport == nil {
		transport = http.DefaultTransport.(*http.Transport)
	}
	clone := transport.Clone()
	clone.Proxy = nil
	clone.DialTLS = nil
	clone.DialTLSContext = nil
	baseDial := clone.DialContext
	if baseDial == nil {
		baseDial = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	}
	clone.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, errors.New("RunningHub download address is invalid")
		}
		addresses, err := resolvePublicIPs(ctx, host)
		if err != nil {
			return nil, err
		}
		var lastErr error
		for _, resolved := range addresses {
			connection, dialErr := baseDial(ctx, network, net.JoinHostPort(resolved.String(), port))
			if dialErr == nil {
				return connection, nil
			}
			lastErr = dialErr
		}
		return nil, fmt.Errorf("RunningHub download connection failed: %w", safeTransportError(lastErr))
	}
	return clone
}

type rejectingDownloadTransport struct{}

func (rejectingDownloadTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("RunningHub download transport is not supported")
}

func resolvePublicIPs(ctx context.Context, host string) ([]net.IP, error) {
	addresses := []net.IP{}
	if parsed := net.ParseIP(host); parsed != nil {
		addresses = append(addresses, parsed)
	} else {
		resolved, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
		if err != nil || len(resolved) == 0 {
			return nil, errors.New("RunningHub download host could not be resolved")
		}
		addresses = resolved
	}
	for _, address := range addresses {
		if !publicIP(address) {
			return nil, errors.New("RunningHub download host is not public")
		}
	}
	return addresses, nil
}

func redactString(value, key string) string {
	redactor := security.NewRedactor()
	redactor.Register(key)
	return redactor.Redact(value)
}

func redactError(err error, key string) error { return errors.New(redactString(err.Error(), key)) }

func safeTransportError(err error) error {
	return errors.New(security.NewRedactor().Redact(err.Error()))
}
