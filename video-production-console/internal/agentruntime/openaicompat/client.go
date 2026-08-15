package openaicompat

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function ToolCallFunc `json:"function"`
}

type ToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolSpec struct {
	Type     string       `json:"type"`
	Function ToolSpecFunc `json:"function"`
}

type ToolSpecFunc struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// ChatRequest is the OpenAI-compatible chat body. reasoning_effort is omitted
// when empty so models that do not support it can be used unchanged.
type ChatRequest struct {
	Model           string     `json:"model"`
	Messages        []Message  `json:"messages"`
	Tools           []ToolSpec `json:"tools,omitempty"`
	Stream          bool       `json:"stream,omitempty"`
	ReasoningEffort string     `json:"reasoning_effort,omitempty"`
}

type ChatResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
}

// ChatClient is the OpenAI-compatible chat completions client.
type ChatClient interface {
	Chat(req ChatRequest) (ChatResponse, error)
}

// HTTPChatClient calls POST {base}/v1/chat/completions unless the base already
// includes the chat completions path.
type HTTPChatClient struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

func chatCompletionsURL(base string) (string, error) {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return "", errors.New("openai-compatible base url is required")
	}
	parsed, err := url.Parse(base)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return "", errors.New("openai-compatible base url is invalid")
	}
	if strings.HasSuffix(base, "/chat/completions") {
		return base, nil
	}
	if strings.HasSuffix(base, "/v1") {
		return base + "/chat/completions", nil
	}
	return base + "/v1/chat/completions", nil
}

func http2Client() *http.Client {
	return &http.Client{Timeout: 10 * time.Minute}
}

func http1Client() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ForceAttemptHTTP2 = false
	transport.TLSNextProto = map[string]func(authority string, c *tls.Conn) http.RoundTripper{}
	cfg := &tls.Config{NextProtos: []string{"http/1.1"}}
	if transport.TLSClientConfig != nil {
		cfg = transport.TLSClientConfig.Clone()
		cfg.NextProtos = []string{"http/1.1"}
	}
	transport.TLSClientConfig = cfg
	return &http.Client{Timeout: 10 * time.Minute, Transport: transport}
}

func isHTTPProtocolError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "malformed HTTP") || strings.Contains(msg, "HTTP/1.x transport connection broken")
}

func (c *HTTPChatClient) Chat(req ChatRequest) (ChatResponse, error) {
	if c.HTTPClient != nil {
		return c.do(c.HTTPClient, req)
	}
	resp, err := c.do(http2Client(), req)
	if err != nil && isHTTPProtocolError(err) {
		return c.do(http1Client(), req)
	}
	return resp, err
}

func (c *HTTPChatClient) do(httpClient *http.Client, req ChatRequest) (ChatResponse, error) {
	endpoint, err := chatCompletionsURL(c.BaseURL)
	if err != nil {
		return ChatResponse{}, err
	}
	body, err := json.Marshal(req)
	if err != nil {
		return ChatResponse{}, err
	}
	httpReq, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return ChatResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	if req.Stream {
		httpReq.Header.Set("Accept", "text/event-stream")
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return ChatResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		return ChatResponse{}, fmt.Errorf("chat completions status %d: %s", resp.StatusCode, truncate(string(raw), 400))
	}
	if req.Stream || strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		content, err := readSSEContent(resp.Body)
		if err != nil {
			return ChatResponse{}, err
		}
		return textResponse(content), nil
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return ChatResponse{}, err
	}
	var decoded ChatResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return ChatResponse{}, fmt.Errorf("decode chat response: %w", err)
	}
	return decoded, nil
}

func readSSEContent(r io.Reader) (string, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 16<<20)
	var content strings.Builder
	var lastMessage string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		if delta := chunk.Choices[0].Delta.Content; delta != "" {
			content.WriteString(delta)
		}
		if msg := chunk.Choices[0].Message.Content; msg != "" {
			lastMessage = msg
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if content.Len() > 0 {
		return content.String(), nil
	}
	return lastMessage, nil
}

func textResponse(content string) ChatResponse {
	var decoded ChatResponse
	decoded.Choices = []struct {
		Message Message `json:"message"`
	}{{Message: Message{Role: "assistant", Content: content}}}
	return decoded
}

func truncate(value string, n int) string {
	if len(value) <= n {
		return value
	}
	return value[:n] + "..."
}
