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
		client = &http.Client{
			Transport: transport,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
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

	if !strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
		var buffered bytes.Buffer
		var truncated bool
		result.responseBytes, truncated, err = copyResponseWithLimit(&buffered, response.Body, u.maxResponseBytes)
		if err != nil {
			return result, fmt.Errorf("read upstream response")
		}
		if truncated {
			return result, fmt.Errorf("upstream response exceeds configured maximum")
		}

		copySafeResponseHeaders(writer.Header(), response.Header)
		writer.WriteHeader(response.StatusCode)
		result.responseStarted = true
		written, writeErr := writer.Write(buffered.Bytes())
		result.responseBytes = int64(written)
		if writeErr != nil {
			return result, fmt.Errorf("copy upstream response")
		}
		if written != buffered.Len() {
			return result, fmt.Errorf("copy upstream response")
		}
		return result, nil
	}

	copySafeResponseHeaders(writer.Header(), response.Header)
	writer.WriteHeader(response.StatusCode)
	result.responseStarted = true
	_ = http.NewResponseController(writer).Flush()
	var truncated bool
	result.responseBytes, truncated, err = copyResponseWithLimit(
		streamingWriter{writer: writer},
		response.Body,
		u.maxResponseBytes,
	)
	if err != nil {
		return result, fmt.Errorf("copy upstream response")
	}
	if truncated {
		const terminalErrorEvent = "\n\nevent: error\ndata: {\"error\":{\"code\":\"upstream_unavailable\",\"message\":\"upstream service unavailable\"}}\n\n"
		written, writeErr := io.WriteString(streamingWriter{writer: writer}, terminalErrorEvent)
		result.responseBytes += int64(written)
		if writeErr != nil {
			return result, fmt.Errorf("write streaming error")
		}
		return result, fmt.Errorf("upstream response exceeds configured maximum")
	}
	return result, nil
}

func copyResponseWithLimit(destination io.Writer, source io.Reader, maxBytes int64) (int64, bool, error) {
	written, err := io.CopyN(destination, source, maxBytes)
	if err != nil {
		if err == io.EOF {
			return written, false, nil
		}
		return written, false, err
	}

	var extra [1]byte
	extraBytes, err := io.ReadFull(source, extra[:])
	if extraBytes != 0 {
		return written, true, nil
	}
	if err != nil && err != io.EOF {
		return written, false, err
	}
	return written, false, nil
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
