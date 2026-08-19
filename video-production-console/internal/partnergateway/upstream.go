package partnergateway

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultMaxResponseBytes      = int64(32 << 20)
	defaultResponseHeaderTimeout = 30 * time.Second
)

type Upstream struct {
	baseURL          *url.URL
	apiKey           string
	client           *http.Client
	maxResponseBytes int64
}

type upstreamResult struct {
	statusCode      int
	requestBytes    int64
	responseBytes   int64
	responseStarted bool
}

func newUpstream(baseURL *url.URL, apiKey string, client *http.Client, maxResponseBytes int64) (*Upstream, error) {
	if baseURL == nil || (baseURL.Scheme != "http" && baseURL.Scheme != "https") || baseURL.Host == "" {
		return nil, fmt.Errorf("valid upstream base URL is required")
	}
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("upstream API key is required")
	}
	if client == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.ResponseHeaderTimeout = defaultResponseHeaderTimeout
		client = &http.Client{Transport: transport}
	}
	if maxResponseBytes <= 0 {
		maxResponseBytes = defaultMaxResponseBytes
	}
	clonedBaseURL := *baseURL
	return &Upstream{
		baseURL:          &clonedBaseURL,
		apiKey:           apiKey,
		client:           client,
		maxResponseBytes: maxResponseBytes,
	}, nil
}

func (u *Upstream) forward(
	ctx context.Context,
	writer http.ResponseWriter,
	source *http.Request,
	route string,
	body []byte,
) (upstreamResult, error) {
	result := upstreamResult{requestBytes: int64(len(body))}
	target := *u.baseURL
	target.Path = strings.TrimRight(target.Path, "/") + "/" + strings.TrimLeft(route, "/")
	target.RawPath = ""
	target.RawQuery = ""
	target.Fragment = ""

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(body))
	if err != nil {
		return result, fmt.Errorf("create upstream request")
	}
	for _, header := range []string{"Content-Type", "Accept"} {
		if value := source.Header.Get(header); value != "" {
			request.Header.Set(header, value)
		}
	}
	request.Header.Set("Authorization", "Bearer "+u.apiKey)

	response, err := u.client.Do(request)
	if err != nil {
		return result, fmt.Errorf("send upstream request")
	}
	defer response.Body.Close()
	result.statusCode = response.StatusCode
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return result, fmt.Errorf("upstream returned an error")
	}

	copySafeResponseHeaders(writer.Header(), response.Header)
	result.responseStarted = true
	writer.WriteHeader(response.StatusCode)

	destination := io.Writer(writer)
	if strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
		destination = streamingWriter{writer: writer}
		_ = http.NewResponseController(writer).Flush()
	}
	result.responseBytes, err = io.Copy(destination, io.LimitReader(response.Body, u.maxResponseBytes))
	if err != nil {
		return result, fmt.Errorf("copy upstream response")
	}
	return result, nil
}

func copySafeResponseHeaders(destination, source http.Header) {
	for _, header := range []string{"Content-Type", "Cache-Control", "X-Request-ID", "Request-ID"} {
		if values := source.Values(header); len(values) != 0 {
			destination[http.CanonicalHeaderKey(header)] = append([]string(nil), values...)
		}
	}
}

type streamingWriter struct {
	writer http.ResponseWriter
}

func (w streamingWriter) Write(body []byte) (int, error) {
	written, err := w.writer.Write(body)
	if err == nil {
		_ = http.NewResponseController(w.writer).Flush()
	}
	return written, err
}
