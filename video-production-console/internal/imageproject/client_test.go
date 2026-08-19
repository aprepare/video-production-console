package imageproject

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientGenerateSSEStreamResult(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString(tinyPNG(t, 2, 2))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["stream"] != true || !strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			t.Errorf("stream=%v accept=%q", req["stream"], r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		f, _ := w.(http.Flusher)
		_, _ = w.Write([]byte(": stream-open\n\ndata: {\"object\":\"progress\",\"message\":\"working\"}\n\ndata: {\"object\":\"image.generation.result\",\"data\":[{" + "\"b64_json\":\"" + payload + "\"}] }\n\ndata: [DONE]\n\n"))
		if f != nil {
			f.Flush()
		}
	}))
	defer server.Close()
	res, err := NewClient(server.Client()).Generate(context.Background(), GenerateRequest{BaseURL: server.URL, APIKey: "key", Model: "m", Prompt: "p", Stream: true})
	if err != nil || res.Width != 2 || res.Height != 2 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestClientGenerateSSEMultilineDataJSON(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString(tinyPNG(t, 2, 2))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"object\":\"image.generation.result\",\n")
		_, _ = io.WriteString(w, "data: \"data\":[{\"b64_json\":\"")
		_, _ = io.WriteString(w, payload+"\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	result, err := NewClient(server.Client()).Generate(context.Background(), GenerateRequest{BaseURL: server.URL, APIKey: "key", Model: "m", Prompt: "p", Stream: true})
	if err != nil || result.Width != 2 || result.Height != 2 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestClientGenerateSSEMalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {not-json}\n\n")
	}))
	defer server.Close()
	_, err := NewClient(server.Client()).Generate(context.Background(), GenerateRequest{BaseURL: server.URL, APIKey: "key", Model: "m", Prompt: "p", Stream: true})
	if err == nil || err.Error() != "image API SSE event is invalid" {
		t.Fatalf("err=%v", err)
	}
}

func TestClientGenerateSSEDoneWithoutResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"object\":\"progress\"}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	_, err := NewClient(server.Client()).Generate(context.Background(), GenerateRequest{BaseURL: server.URL, APIKey: "key", Model: "m", Prompt: "p", Stream: true})
	if err == nil || err.Error() != "image API SSE result missing" {
		t.Fatalf("err=%v", err)
	}
}

func TestClientGenerateRejectsOversizedSSEResponse(t *testing.T) {
	client := NewClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", maxImageResponseSize+1)))}, nil
	})})
	_, err := client.Generate(context.Background(), GenerateRequest{BaseURL: "https://api.example.com/v1", APIKey: "key", Model: "m", Prompt: "p", Stream: true})
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("err=%v", err)
	}
}

func TestClientGenerateDefaultRequestOmitsStream(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString(tinyPNG(t, 1, 1))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if _, ok := req["stream"]; ok {
			t.Errorf("stream field present: %v", req["stream"])
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept=%q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"b64_json":"`+payload+`"}]}`)
	}))
	defer server.Close()
	if _, err := NewClient(server.Client()).Generate(context.Background(), GenerateRequest{BaseURL: server.URL, APIKey: "key", Model: "m", Prompt: "p"}); err != nil {
		t.Fatal(err)
	}
}

func TestClientGenerateSSEErrorIsSafe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"error\":{\"message\":\"secret\"}}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()
	_, err := NewClient(server.Client()).Generate(context.Background(), GenerateRequest{BaseURL: server.URL, APIKey: "secret-key", Model: "m", Prompt: "private-prompt"})
	if err == nil || strings.Contains(err.Error(), "secret-key") || strings.Contains(err.Error(), "private-prompt") {
		t.Fatalf("err=%v", err)
	}
}

func tinyPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	var buffer strings.Builder
	_ = buffer
	imageValue := image.NewRGBA(image.Rect(0, 0, width, height))
	imageValue.Set(0, 0, color.White)
	var data byteBuffer
	if err := png.Encode(&data, imageValue); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

type byteBuffer struct{ bytes []byte }

func (b *byteBuffer) Write(value []byte) (int, error) {
	b.bytes = append(b.bytes, value...)
	return len(value), nil
}
func (b *byteBuffer) Bytes() []byte { return b.bytes }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestNewClientDefaultTimeoutAllowsLongImageGeneration(t *testing.T) {
	client := NewClient(nil)
	if client.http.Timeout != DefaultGenerateTimeout || DefaultGenerateTimeout < 5*time.Minute {
		t.Fatalf("generate timeout=%s", client.http.Timeout)
	}
}

func TestRatioSizeUsesSupportedOpenAIImageSizes(t *testing.T) {
	for ratio, want := range map[string]string{
		"3:4":  "1024x1536",
		"4:3":  "1536x1024",
		"9:16": "1024x1536",
		"1:1":  "1024x1024",
	} {
		if got := ratioSize(ratio); got != want {
			t.Fatalf("ratio %s size=%s want=%s", ratio, got, want)
		}
	}
}

func TestClientGenerateAcceptsBase64AndSendsOpenAIRequest(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString(tinyPNG(t, 3, 4))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/generations" || r.Header.Get("Authorization") != "Bearer secret" || r.Header.Get("User-Agent") != outboundUserAgent {
			t.Fatalf("request path=%s auth=%q ua=%q", r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("User-Agent"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"` + payload + `"}]}`))
	}))
	defer server.Close()
	result, err := NewClient(server.Client()).Generate(context.Background(), GenerateRequest{BaseURL: server.URL + "/v1", APIKey: "secret", Model: "gpt-image-2", Prompt: "prompt", Ratio: "3:4"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Width != 3 || result.Height != 4 || result.MIMEType != "image/png" || len(result.Bytes) == 0 {
		t.Fatalf("result=%+v", result)
	}
}

func TestPartnerRuntimeImageRequestUsesGatewaySession(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString(tinyPNG(t, 1, 1))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/generations" {
			t.Fatalf("request path=%s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer opaque-session" {
			t.Fatalf("Authorization=%q", got)
		}
		var request GenerateRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Model != "gpt-image-2" {
			t.Fatalf("model=%q", request.Model)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"b64_json":"`+payload+`"}]}`)
	}))
	defer server.Close()

	result, err := NewClient(server.Client()).Generate(t.Context(), GenerateRequest{
		BaseURL: server.URL + "/v1",
		APIKey:  "opaque-session",
		Model:   "gpt-image-2",
		Prompt:  "partner prompt",
		Ratio:   "1:1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Width != 1 || result.Height != 1 {
		t.Fatalf("result=%+v", result)
	}
}

func TestGenerateBatchHonorsConcurrencyLimit(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString(tinyPNG(t, 3, 4))
	var active atomic.Int32
	var peak atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			previous := peak.Load()
			if current <= previous || peak.CompareAndSwap(previous, current) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"` + payload + `"}]}`))
	}))
	defer server.Close()
	requests := make([]GenerateRequest, 6)
	for index := range requests {
		requests[index] = GenerateRequest{BaseURL: server.URL, APIKey: "key", Model: "gpt-image-2", Prompt: "p", Ratio: "3:4"}
	}
	results := GenerateBatch(context.Background(), NewClient(server.Client()), requests, 2, 2)
	if len(results) != 6 {
		t.Fatalf("results=%d", len(results))
	}
	if peak.Load() > 2 {
		t.Fatalf("peak concurrency=%d", peak.Load())
	}
}

func TestClientGenerateAcceptsFirstPublicURL(t *testing.T) {
	firstBytes := tinyPNG(t, 7, 9)
	secondBytes := tinyPNG(t, 5, 5)
	downloaded := ""
	client := NewClient(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.Method {
		case http.MethodPost:
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":[{"url":"https://93.184.216.34/first.png"},{"url":"https://93.184.216.34/second.png"}]}`))}, nil
		case http.MethodGet:
			downloaded = request.URL.String()
			if request.URL.String() != "https://93.184.216.34/first.png" {
				t.Fatalf("download URL=%s", request.URL)
			}
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(firstBytes)))}, nil
		default:
			t.Fatalf("method=%s", request.Method)
			return nil, nil
		}
	})})

	result, err := client.Generate(context.Background(), GenerateRequest{BaseURL: "https://api.example.com/v1", APIKey: "secret", Model: "gpt-image-2", Prompt: "prompt", Ratio: "1:1"})
	if err != nil {
		t.Fatal(err)
	}
	if downloaded != "https://93.184.216.34/first.png" || !bytes.Equal(result.Bytes, firstBytes) || bytes.Equal(result.Bytes, secondBytes) {
		t.Fatalf("downloaded=%s result=%+v", downloaded, result)
	}
}

func TestClientGenerateAcceptsPublicURLResponse(t *testing.T) {
	imageBytes := tinyPNG(t, 7, 9)
	requests := 0
	client := NewClient(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		switch request.Method {
		case http.MethodPost:
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":[{"url":"https://93.184.216.34/generated.png"}]}`))}, nil
		case http.MethodGet:
			if request.URL.String() != "https://93.184.216.34/generated.png" {
				t.Fatalf("download URL=%s", request.URL)
			}
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(imageBytes)))}, nil
		default:
			t.Fatalf("method=%s", request.Method)
			return nil, nil
		}
	})})

	result, err := client.Generate(context.Background(), GenerateRequest{BaseURL: "https://api.example.com/v1", APIKey: "secret", Model: "gpt-image-2", Prompt: "prompt", Ratio: "1:1"})
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 || result.Width != 7 || result.Height != 9 || result.MIMEType != "image/png" {
		t.Fatalf("requests=%d result=%+v", requests, result)
	}
}

func TestClientGenerateRejectsPrivateDownloadURLBeforeFetching(t *testing.T) {
	getRequests := 0
	client := NewClient(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method == http.MethodGet {
			getRequests++
			t.Fatalf("private URL was fetched: %s", request.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":[{"url":"http://127.0.0.1/private.png"}]}`))}, nil
	})})

	_, err := client.Generate(context.Background(), GenerateRequest{BaseURL: "https://api.example.com/v1", APIKey: "secret", Model: "gpt-image-2", Prompt: "prompt", Ratio: "1:1"})
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("error=%v", err)
	}
	if getRequests != 0 {
		t.Fatalf("private image URL fetched %d times", getRequests)
	}
}

func TestPublicImageIPRejectsSpecialPurposeNetworks(t *testing.T) {
	for _, raw := range []string{"100.64.0.1", "100.100.100.200", "192.0.2.1", "198.18.0.1", "203.0.113.1", "2001:db8::1"} {
		if isPublicImageIP(net.ParseIP(raw)) {
			t.Fatalf("special-purpose address accepted: %s", raw)
		}
	}
	if !isPublicImageIP(net.ParseIP("93.184.216.34")) {
		t.Fatal("public address rejected")
	}
}

func TestClientGenerateDoesNotLeakAPIKeyInTransportErrors(t *testing.T) {
	const apiKey = "sk-sensitive-value"
	client := NewClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("transport rejected Authorization: Bearer " + apiKey)
	})})

	_, err := client.Generate(context.Background(), GenerateRequest{BaseURL: "https://api.example.com/v1", APIKey: apiKey, Model: "gpt-image-2", Prompt: "prompt", Ratio: "1:1"})
	if err == nil {
		t.Fatal("Generate() error=nil")
	}
	if strings.Contains(err.Error(), apiKey) {
		t.Fatalf("API key leaked in error: %v", err)
	}
}

func TestClientGeneratePreservesTransportErrorClassificationWithoutSensitiveDetails(t *testing.T) {
	const apiKey = "sk-sensitive-value"
	const prompt = "prompt containing sensitive details"
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "eof", err: io.EOF},
		{name: "deadline", err: context.DeadlineExceeded},
		{name: "transport", err: errors.New("transport rejected request")},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := NewClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, test.err
			})})
			_, err := client.Generate(context.Background(), GenerateRequest{BaseURL: "https://api.example.com/v1", APIKey: apiKey, Model: "gpt-image-2", Prompt: prompt, Ratio: "1:1"})
			if err == nil || !strings.Contains(err.Error(), "image API request failed") {
				t.Fatalf("error=%v", err)
			}
			if !errors.Is(err, test.err) {
				t.Fatalf("error=%v does not preserve cause %v", err, test.err)
			}
			if strings.Contains(err.Error(), apiKey) || strings.Contains(err.Error(), prompt) {
				t.Fatalf("sensitive detail leaked in error: %v", err)
			}
		})
	}
}

func TestClientGenerateRejectsOversizedGenerationResponse(t *testing.T) {
	client := NewClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			Header:        make(http.Header),
			ContentLength: maxImageResponseSize + 1,
			Body:          io.NopCloser(strings.NewReader(strings.Repeat("x", maxImageResponseSize+1))),
		}, nil
	})})

	_, err := client.Generate(context.Background(), GenerateRequest{BaseURL: "https://api.example.com/v1", APIKey: "secret", Model: "gpt-image-2", Prompt: "prompt", Ratio: "1:1"})
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("error=%v", err)
	}
}

type retryGenerator struct {
	calls atomic.Int32
	errs  []error
}

func (g *retryGenerator) Generate(context.Context, GenerateRequest) (GenerateResult, error) {
	i := int(g.calls.Add(1)) - 1
	if i < len(g.errs) {
		return GenerateResult{}, g.errs[i]
	}
	return GenerateResult{Bytes: []byte("ok")}, nil
}

type countingGenerator struct {
	failuresBeforeSuccess int
	calls                 atomic.Int32
}

func (g *countingGenerator) Generate(context.Context, GenerateRequest) (GenerateResult, error) {
	n := int(g.calls.Add(1))
	if n <= g.failuresBeforeSuccess {
		return GenerateResult{}, &HTTPStatusError{StatusCode: 503, Operation: "image API"}
	}
	return GenerateResult{Bytes: []byte("ok")}, nil
}

func (g *countingGenerator) Calls() int { return int(g.calls.Load()) }

func TestGenerateBatchUsesConfiguredTotalAttemptsPerItem(t *testing.T) {
	for _, totalAttempts := range []int{1, 2, 4} {
		t.Run(strconv.Itoa(totalAttempts), func(t *testing.T) {
			generator := &countingGenerator{failuresBeforeSuccess: 10}
			results := GenerateBatch(t.Context(), generator, []GenerateRequest{{Prompt: "p"}}, 1, totalAttempts)
			if generator.Calls() != totalAttempts || results[0].Attempts != totalAttempts {
				t.Fatalf("calls=%d result=%+v", generator.Calls(), results[0])
			}
		})
	}
}

func TestGenerateBatchRetriesTransientHTTPStatusOncePerItem(t *testing.T) {
	g := &retryGenerator{errs: []error{&HTTPStatusError{StatusCode: 503, Operation: "image API"}}}
	results := GenerateBatch(context.Background(), g, []GenerateRequest{{}}, 1, 2)
	if got := g.calls.Load(); got != 2 {
		t.Fatalf("calls=%d want 2", got)
	}
	if results[0].Error != nil || results[0].Attempts != 2 {
		t.Fatalf("error=%v attempts=%d", results[0].Error, results[0].Attempts)
	}
}

func TestGenerateBatchDoesNotRetryPermanentHTTPStatus(t *testing.T) {
	g := &retryGenerator{errs: []error{&HTTPStatusError{StatusCode: 400, Operation: "image API"}}}
	results := GenerateBatch(context.Background(), g, []GenerateRequest{{}}, 1, 4)
	if got := g.calls.Load(); got != 1 {
		t.Fatalf("calls=%d want 1", got)
	}
	if results[0].Error == nil || results[0].Attempts != 1 {
		t.Fatal("error=nil")
	}
}

func TestGenerateBatchRetriesTransientThenReturnsFinalError(t *testing.T) {
	err503 := &HTTPStatusError{StatusCode: 503, Operation: "image API"}
	g := &retryGenerator{errs: []error{err503, err503}}
	results := GenerateBatch(context.Background(), g, []GenerateRequest{{}}, 1, 2)
	if got := g.calls.Load(); got != 2 {
		t.Fatalf("calls=%d want 2", got)
	}
	if results[0].Error == nil || !errors.As(results[0].Error, &err503) || results[0].Attempts != 2 {
		t.Fatalf("error=%v", results[0].Error)
	}
}

func TestGenerateBatchDoesNotRetryCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	g := &retryGenerator{errs: []error{&HTTPStatusError{StatusCode: 503, Operation: "image API"}}}
	results := GenerateBatch(ctx, g, []GenerateRequest{{}}, 1, 4)
	if got := g.calls.Load(); got != 0 {
		t.Fatalf("calls=%d want 0", got)
	}
	if !errors.Is(results[0].Error, context.Canceled) {
		t.Fatalf("error=%v", results[0].Error)
	}
}
