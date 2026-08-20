package imageproject

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewHTTPChatClientDefaultTimeoutAllowsLongPlanning(t *testing.T) {
	client := NewHTTPChatClient(nil)
	if client.HTTP.Timeout != DefaultChatTimeout || DefaultChatTimeout < 10*time.Minute {
		t.Fatalf("chat timeout=%s", client.HTTP.Timeout)
	}
}

func TestChatHTTPClientIgnoresShortSharedClient(t *testing.T) {
	got := chatHTTPClient(&http.Client{Timeout: 30 * time.Second})
	if got.Timeout != DefaultChatTimeout {
		t.Fatalf("timeout=%s, want %s", got.Timeout, DefaultChatTimeout)
	}
}

func TestChatHTTPClientKeepsInjectedPinnedRoots(t *testing.T) {
	pool := x509.NewCertPool()
	base := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: "23.138.12.112"}}}
	got := chatHTTPClient(base)
	transport, ok := got.Transport.(*http.Transport)
	if !ok || transport.TLSClientConfig == nil || transport.TLSClientConfig.RootCAs != pool {
		t.Fatal("planner client must keep the injected partner CA")
	}
	if got := transport.TLSClientConfig.ServerName; got != "23.138.12.112" {
		t.Fatalf("server name=%q", got)
	}
}

func TestCompleteChatDisablesStreaming(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"ok\":true}"}}]}`))
	}))
	defer server.Close()

	if _, err := CompleteChat(context.Background(), nil, server.URL+"/v1", "test-only-key", ChatRequest{
		Model: "planner-test", System: "sys", User: "user",
	}); err != nil {
		t.Fatal(err)
	}
	if got["stream"] != false {
		t.Fatalf("stream=%v, want false", got["stream"])
	}
}

func TestCompleteChatSendsMessagesAndStrictJSONSchema(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"segments\":[]}"}}]}`))
	}))
	defer server.Close()

	schema := map[string]any{
		"type":                 "object",
		"properties":           map[string]any{"segments": map[string]any{"type": "array"}},
		"required":             []string{"segments"},
		"additionalProperties": false,
	}
	_, err := CompleteChat(context.Background(), server.Client(), server.URL+"/v1", "test-only-key", ChatRequest{
		Model: "planner-test", System: "system rules", User: "user rules and source",
		ResponseSchemaName: "image_segments", ResponseSchema: schema,
	})
	if err != nil {
		t.Fatal(err)
	}
	messages, ok := got["messages"].([]any)
	if !ok || len(messages) != 2 {
		t.Fatalf("messages=%#v", got["messages"])
	}
	first, _ := messages[0].(map[string]any)
	second, _ := messages[1].(map[string]any)
	if first["role"] != "system" || first["content"] != "system rules" || second["role"] != "user" || second["content"] != "user rules and source" {
		t.Fatalf("messages=%#v", messages)
	}
	responseFormat, ok := got["response_format"].(map[string]any)
	if !ok || responseFormat["type"] != "json_schema" {
		t.Fatalf("response_format=%#v", got["response_format"])
	}
	jsonSchema, ok := responseFormat["json_schema"].(map[string]any)
	sentSchema, _ := json.Marshal(jsonSchema["schema"])
	wantSchema, _ := json.Marshal(schema)
	if !ok || jsonSchema["name"] != "image_segments" || jsonSchema["strict"] != true || string(sentSchema) != string(wantSchema) {
		t.Fatalf("json_schema=%#v", responseFormat["json_schema"])
	}
}

func TestChatCompletionsURLRejectsUnsafeOrInvalidBases(t *testing.T) {
	for _, base := range []string{
		"file:///tmp/model",
		"javascript:alert(1)",
		"https://user:password@example.com/v1",
		"http://",
	} {
		if _, err := chatCompletionsURL(base); err == nil {
			t.Fatalf("chatCompletionsURL(%q) accepted unsafe base", base)
		}
	}
}

func TestChatCompletionsURLNormalizesOpenAICompatibleBases(t *testing.T) {
	for base, want := range map[string]string{
		"https://example.com":                  "https://example.com/v1/chat/completions",
		"https://example.com/v1/":              "https://example.com/v1/chat/completions",
		"https://example.com/chat/completions": "https://example.com/chat/completions",
	} {
		got, err := chatCompletionsURL(base)
		if err != nil {
			t.Fatalf("chatCompletionsURL(%q): %v", base, err)
		}
		if got != want {
			t.Fatalf("chatCompletionsURL(%q)=%q, want %q", base, got, want)
		}
	}
}

func TestCompleteChatSendsReasoningEffortAndReadsReasoningContent(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != outboundUserAgent {
			t.Fatalf("user-agent=%q", r.Header.Get("User-Agent"))
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"","reasoning_content":"{\"ok\":true}"}}]}`))
	}))
	defer server.Close()

	content, err := CompleteChat(context.Background(), server.Client(), server.URL+"/v1", "test-only-key", ChatRequest{
		Model: "planner-test", System: "sys", User: "user", ReasoningEffort: "high",
	})
	if err != nil {
		t.Fatal(err)
	}
	if content != `{"ok":true}` {
		t.Fatalf("content=%q", content)
	}
	if got["reasoning_effort"] != "high" || got["model"] != "planner-test" {
		t.Fatalf("payload=%v", got)
	}
}

func TestCompleteChatOmitsReasoningEffortWhenEmpty(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"plain"}}]}`))
	}))
	defer server.Close()

	content, err := CompleteChat(context.Background(), server.Client(), server.URL+"/v1", "test-only-key", ChatRequest{
		Model: "planner-test", System: "sys", User: "user",
	})
	if err != nil || content != "plain" {
		t.Fatalf("content=%q err=%v", content, err)
	}
	if _, ok := got["reasoning_effort"]; ok {
		t.Fatalf("empty effort should be omitted: %v", got)
	}
}
