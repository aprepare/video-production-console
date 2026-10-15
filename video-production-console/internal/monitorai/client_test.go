package monitorai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"video-production-console/internal/refanalysis"
)

func TestCompleteSendsChatRequestAndReturnsContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("request: %s %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"role":"system"`) || !strings.Contains(string(body), `\"reference_id\":\"ref-1\"`) {
			t.Errorf("payload: %s", body)
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"{\"summary\":\"ok\"}"}}]}`)
	}))
	defer server.Close()

	got, err := NewClient(server.Client()).Complete(t.Context(), refanalysis.ModelConfig{
		BaseURL: server.URL + "/v1", APIKey: "secret", Model: "test-model",
	}, "system prompt", map[string]any{"reference_id": "ref-1"}, time.Second)
	if err != nil || got != `{"summary":"ok"}` {
		t.Fatalf("Complete() = %q, %v", got, err)
	}
}

func TestCompletePreservesStringAndMultimodalContentShapes(t *testing.T) {
	tests := []struct {
		name    string
		content any
		check   func(*testing.T, any)
	}{
		{
			name:    "string",
			content: "plain prompt",
			check: func(t *testing.T, got any) {
				if got != "plain prompt" {
					t.Fatalf("content = %#v", got)
				}
			},
		},
		{
			name: "parts",
			content: []ContentPart{
				{Type: "text", Text: "score this"},
				{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/jpeg;base64,abc"}},
			},
			check: func(t *testing.T, got any) {
				parts, ok := got.([]any)
				if !ok || len(parts) != 2 {
					t.Fatalf("content = %#v", got)
				}
				image, _ := parts[1].(map[string]any)
				imageURL, _ := image["image_url"].(map[string]any)
				if image["type"] != "image_url" || imageURL["url"] != "data:image/jpeg;base64,abc" {
					t.Fatalf("image part = %#v", image)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Messages []struct {
						Content any `json:"content"`
					} `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				} else if len(request.Messages) != 2 {
					t.Errorf("messages = %#v", request.Messages)
				} else {
					tc.check(t, request.Messages[1].Content)
				}
				io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
			}))
			defer server.Close()
			if _, err := NewClient(server.Client()).Complete(t.Context(), refanalysis.ModelConfig{BaseURL: server.URL, Model: "m"}, "system", tc.content, time.Second); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCompleteRejectsRedirectAndOnlyReportsHTTPStatus(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("redirect must not be followed")
	}))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, target.URL, http.StatusFound)
			return
		}
		w.Header().Set("Location", "/redirect")
		w.WriteHeader(http.StatusFound)
		io.WriteString(w, "secret-key https://private.example/path")
	}))
	defer server.Close()

	_, err := NewClient(server.Client()).Complete(t.Context(), refanalysis.ModelConfig{
		BaseURL: server.URL, APIKey: "secret-key", Model: "m",
	}, "s", map[string]string{"x": "y"}, time.Second)
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusFound {
		t.Fatalf("error = %#v", err)
	}
	if strings.Contains(err.Error(), "secret-key") || strings.Contains(err.Error(), "private.example") {
		t.Fatalf("sensitive error: %v", err)
	}
}

func TestCompleteCapsResponseAndHonorsCallerTimeout(t *testing.T) {
	t.Run("too large", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			io.WriteString(w, strings.Repeat("x", (8<<20)+1))
		}))
		defer server.Close()
		_, err := NewClient(server.Client()).Complete(t.Context(), refanalysis.ModelConfig{BaseURL: server.URL, Model: "m"}, "s", nil, time.Second)
		if err == nil || !strings.Contains(err.Error(), "过大") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(100 * time.Millisecond)
			io.WriteString(w, `{"choices":[]}`)
		}))
		defer server.Close()
		ctx := context.Background()
		_, err := NewClient(server.Client()).Complete(ctx, refanalysis.ModelConfig{BaseURL: server.URL, Model: "m"}, "s", nil, 10*time.Millisecond)
		if err == nil || !strings.Contains(err.Error(), "超时") {
			t.Fatalf("error = %v", err)
		}
	})
}
