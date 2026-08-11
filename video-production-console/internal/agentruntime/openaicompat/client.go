package openaicompat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	Type     string         `json:"type"`
	Function ToolSpecFunc   `json:"function"`
}

type ToolSpecFunc struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type ChatRequest struct {
	Model    string     `json:"model"`
	Messages []Message  `json:"messages"`
	Tools    []ToolSpec `json:"tools,omitempty"`
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

// HTTPChatClient calls POST {base}/chat/completions.
type HTTPChatClient struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

func (c *HTTPChatClient) Chat(req ChatRequest) (ChatResponse, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	if !strings.HasSuffix(endpoint, "/chat/completions") {
		endpoint += "/chat/completions"
	}
	body, err := json.Marshal(req)
	if err != nil {
		return ChatResponse{}, err
	}
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 3 * time.Minute}
	}
	httpReq, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return ChatResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return ChatResponse{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return ChatResponse{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ChatResponse{}, fmt.Errorf("chat completions status %d: %s", resp.StatusCode, truncate(string(raw), 400))
	}
	var decoded ChatResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return ChatResponse{}, fmt.Errorf("decode chat response: %w", err)
	}
	return decoded, nil
}

func truncate(value string, n int) string {
	if len(value) <= n {
		return value
	}
	return value[:n] + "..."
}
