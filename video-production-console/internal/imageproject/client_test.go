package imageproject

import (
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

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
		if r.URL.Path != "/v1/images/generations" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("request path=%s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
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
	results := GenerateBatch(context.Background(), NewClient(server.Client()), requests, 2)
	if len(results) != 6 {
		t.Fatalf("results=%d", len(results))
	}
	if peak.Load() > 2 {
		t.Fatalf("peak concurrency=%d", peak.Load())
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
