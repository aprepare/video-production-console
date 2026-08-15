package imageproject

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"time"

	_ "golang.org/x/image/webp"
)

const (
	maxImageResponseSize   = 32 << 20
	DefaultGenerateTimeout = 5 * time.Minute
	GenerateBatchBudget    = 15 * time.Minute
)

type GenerateRequest struct {
	BaseURL string
	APIKey  string
	Model   string
	Prompt  string
	Ratio   string
	Stream  bool
}

type GenerateResult struct {
	Bytes    []byte
	MIMEType string
	Width    int
	Height   int
	Attempts int
	Error    error
}

type Generator interface {
	Generate(context.Context, GenerateRequest) (GenerateResult, error)
}

type Client struct {
	http           *http.Client
	downloadClient *http.Client
}

type imageAPIRequestError struct {
	cause error
	text  string
}

type HTTPStatusError struct {
	StatusCode int
	Operation  string
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("%s returned HTTP %d", e.Operation, e.StatusCode)
}

func (e imageAPIRequestError) Error() string { return e.text }
func (e imageAPIRequestError) Unwrap() error { return e.cause }

func NewClient(client *http.Client) *Client {
	if client == nil {
		client = &http.Client{Timeout: DefaultGenerateTimeout}
	}
	apiClient := *client
	apiClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	downloadClient := apiClient
	downloadClient.Transport = publicImageTransport(client.Transport)
	return &Client{http: &apiClient, downloadClient: &downloadClient}
}

func (c *Client) Generate(ctx context.Context, input GenerateRequest) (GenerateResult, error) {
	endpoint, err := generationURL(input.BaseURL)
	if err != nil {
		return GenerateResult{}, err
	}
	if strings.TrimSpace(input.APIKey) == "" || strings.TrimSpace(input.Model) == "" || strings.TrimSpace(input.Prompt) == "" {
		return GenerateResult{}, errors.New("image generation configuration is incomplete")
	}
	payload := map[string]any{"model": input.Model, "prompt": input.Prompt, "n": 1, "size": ratioSize(input.Ratio), "response_format": "b64_json"}
	if input.Stream {
		payload["stream"] = true
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return GenerateResult{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return GenerateResult{}, err
	}
	request.Header.Set("Authorization", "Bearer "+input.APIKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	if input.Stream {
		request.Header.Set("Accept", "text/event-stream, application/json")
	}
	request.Header.Set("User-Agent", outboundUserAgent)
	response, err := c.http.Do(request)
	if err != nil {
		safe := strings.ReplaceAll(err.Error(), input.APIKey, "[redacted]")
		safe = strings.ReplaceAll(safe, input.Prompt, "[redacted]")
		return GenerateResult{}, imageAPIRequestError{cause: err, text: "image API request failed: " + safe}
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxImageResponseSize+1))
	if err != nil {
		return GenerateResult{}, err
	}
	if len(body) > maxImageResponseSize {
		return GenerateResult{}, errors.New("image response is too large")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return GenerateResult{}, &HTTPStatusError{StatusCode: response.StatusCode, Operation: "image API"}
	}
	if strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
		body, err = parseSSE(body)
		if err != nil {
			return GenerateResult{}, err
		}
	}
	var streamErr struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(body, &streamErr) == nil && len(streamErr.Error) > 0 && string(streamErr.Error) != "null" {
		return GenerateResult{}, errors.New("image API returned an error")
	}
	var result struct {
		Data []struct {
			Base64 string `json:"b64_json"`
			URL    string `json:"url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil || len(result.Data) == 0 {
		return GenerateResult{}, errors.New("image API response is invalid")
	}
	data := result.Data[0]
	var imageBytes []byte
	if data.Base64 != "" {
		imageBytes, err = base64.StdEncoding.DecodeString(data.Base64)
		if err != nil {
			return GenerateResult{}, errors.New("image API returned invalid base64")
		}
	} else if data.URL != "" {
		imageBytes, err = c.download(ctx, data.URL)
		if err != nil {
			return GenerateResult{}, err
		}
	} else {
		return GenerateResult{}, errors.New("image API returned no image")
	}
	configuration, format, err := image.DecodeConfig(bytes.NewReader(imageBytes))
	if err != nil || configuration.Width < 1 || configuration.Height < 1 {
		return GenerateResult{}, errors.New("generated image could not be decoded")
	}
	return GenerateResult{Bytes: imageBytes, MIMEType: formatMIME(format), Width: configuration.Width, Height: configuration.Height}, nil
}

func parseSSE(body []byte) ([]byte, error) {
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 1024), maxImageResponseSize+1)
	var data strings.Builder
	flush := func() []byte {
		if data.Len() == 0 {
			return nil
		}
		v := strings.TrimSuffix(data.String(), "\n")
		data.Reset()
		return []byte(v)
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if v := flush(); len(v) > 0 {
				var ev struct {
					Object string          `json:"object"`
					Error  json.RawMessage `json:"error"`
				}
				if json.Unmarshal(v, &ev) != nil {
					return nil, errors.New("image API SSE event is invalid")
				}
				if len(ev.Error) > 0 && string(ev.Error) != "null" {
					return nil, errors.New("image API returned an error")
				}
				if ev.Object == "image.generation.result" {
					return v, nil
				}
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			val := strings.TrimPrefix(line, "data:")
			if strings.HasPrefix(val, " ") {
				val = val[1:]
			}
			if val == "[DONE]" {
				return nil, errors.New("image API SSE result missing")
			}
			data.WriteString(val)
			data.WriteByte('\n')
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, errors.New("image API SSE stream failed")
	}
	return nil, errors.New("image API SSE result missing")
}

func (c *Client) download(ctx context.Context, raw string) ([]byte, error) {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return nil, errors.New("image URL is invalid")
	}
	if err := validatePublicImageHost(ctx, net.DefaultResolver, parsed.Hostname()); err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, errors.New("image download request is invalid")
	}
	response, err := c.downloadClient.Do(request)
	if err != nil {
		return nil, errors.New("image download failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, &HTTPStatusError{StatusCode: response.StatusCode, Operation: "image download"}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxImageResponseSize+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxImageResponseSize {
		return nil, errors.New("downloaded image is too large")
	}
	return body, nil
}

type ipResolver interface {
	LookupIP(context.Context, string, string) ([]net.IP, error)
}

func validatePublicImageHost(ctx context.Context, resolver ipResolver, hostname string) error {
	_, err := publicImageIPs(ctx, resolver, hostname)
	return err
}

func publicImageIPs(ctx context.Context, resolver ipResolver, hostname string) ([]net.IP, error) {
	addresses := []net.IP{}
	if address := net.ParseIP(hostname); address != nil {
		addresses = append(addresses, address)
	} else {
		resolved, err := resolver.LookupIP(ctx, "ip", hostname)
		if err != nil || len(resolved) == 0 {
			return nil, errors.New("image URL host could not be resolved")
		}
		addresses = resolved
	}
	for _, address := range addresses {
		if !isPublicImageIP(address) {
			return nil, errors.New("image URL host is not allowed")
		}
	}
	return addresses, nil
}

func publicImageTransport(base http.RoundTripper) http.RoundTripper {
	transport, ok := base.(*http.Transport)
	if !ok && base != nil {
		return publicImageRoundTripper{base: base}
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
			return nil, errors.New("image download address is invalid")
		}
		addresses, err := publicImageIPs(ctx, net.DefaultResolver, hostname)
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
		if lastErr != nil {
			return nil, errors.New("image download connection failed")
		}
		return nil, errors.New("image URL host could not be resolved")
	}
	return clone
}

type publicImageRoundTripper struct {
	base http.RoundTripper
}

func (transport publicImageRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if err := validatePublicImageHost(request.Context(), net.DefaultResolver, request.URL.Hostname()); err != nil {
		return nil, err
	}
	return transport.base.RoundTrip(request)
}

func isPublicImageIP(address net.IP) bool {
	if address == nil || address.IsPrivate() || address.IsLoopback() ||
		address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() ||
		address.IsUnspecified() || address.IsMulticast() {
		return false
	}
	for _, network := range specialImageNetworks {
		if network.Contains(address) {
			return false
		}
	}
	return true
}

var specialImageNetworks = mustImageNetworks(
	"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24",
	"198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4",
	"2001:db8::/32", "2001:10::/28", "2002::/16", "fc00::/7", "fe80::/10",
)

func mustImageNetworks(values ...string) []*net.IPNet {
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

func GenerateBatch(ctx context.Context, generator Generator, requests []GenerateRequest, concurrency, totalAttempts int) []GenerateResult {
	if concurrency < 1 {
		concurrency = 1
	}
	if concurrency > MaxImages {
		concurrency = MaxImages
	}
	if totalAttempts < 1 {
		totalAttempts = 1
	}
	if totalAttempts > 4 {
		totalAttempts = 4
	}
	results := make([]GenerateResult, len(requests))
	jobs := make(chan int)
	var workers sync.WaitGroup
	for range concurrency {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				result, err := generateWithRetry(ctx, generator, requests[index], totalAttempts)
				result.Error = err
				results[index] = result
			}
		}()
	}
	for index := range requests {
		jobs <- index
	}
	close(jobs)
	workers.Wait()
	return results
}

func generateWithRetry(ctx context.Context, generator Generator, request GenerateRequest, totalAttempts int) (GenerateResult, error) {
	if totalAttempts < 1 {
		totalAttempts = 1
	}
	if totalAttempts > 4 {
		totalAttempts = 4
	}
	var result GenerateResult
	var err error
	for attempt := 0; attempt < totalAttempts; attempt++ {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		result, err = generator.Generate(ctx, request)
		result.Attempts = attempt + 1
		if err == nil {
			return result, nil
		}
		if attempt == totalAttempts-1 || !retryableGenerateError(ctx, err) {
			return result, err
		}
	}
	return result, err
}

func retryableGenerateError(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return false
	}
	var statusErr *HTTPStatusError
	if errors.As(err, &statusErr) {
		switch statusErr.StatusCode {
		case 429, 502, 503, 504:
			return true
		}
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, net.ErrClosed) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return errors.Is(err, context.DeadlineExceeded)
}

func generationURL(base string) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(base), "/"))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("image base URL is invalid")
	}
	if strings.HasSuffix(parsed.Path, "/images/generations") {
		return parsed.String(), nil
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/images/generations"
	return parsed.String(), nil
}

func ratioSize(ratio string) string {
	switch ratio {
	case "3:4", "9:16":
		return "1024x1536"
	case "4:3":
		return "1536x1024"
	default:
		return "1024x1024"
	}
}

func formatMIME(format string) string {
	switch strings.ToLower(format) {
	case "jpeg":
		return "image/jpeg"
	case "gif":
		return "image/gif"
	case "webp":
		return "image/webp"
	default:
		return "image/png"
	}
}
