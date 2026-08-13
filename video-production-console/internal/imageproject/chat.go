package imageproject

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type HTTPChatClient struct {
	HTTP *http.Client
}

func NewHTTPChatClient(client *http.Client) *HTTPChatClient {
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	return &HTTPChatClient{HTTP: client}
}

func (c *HTTPChatClient) Complete(ctx context.Context, input ChatRequest) (string, error) {
	return CompleteChat(ctx, c.HTTP, "", "", input)
}

type ConfiguredChatClient struct {
	HTTP    *http.Client
	BaseURL string
	APIKey  string
	Model   string
}

func (c ConfiguredChatClient) Complete(ctx context.Context, input ChatRequest) (string, error) {
	if strings.TrimSpace(input.Model) == "" {
		input.Model = c.Model
	}
	return CompleteChat(ctx, c.HTTP, c.BaseURL, c.APIKey, input)
}

func CompleteChat(ctx context.Context, client *http.Client, baseURL, apiKey string, input ChatRequest) (string, error) {
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	endpoint, err := chatCompletionsURL(baseURL)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(apiKey) == "" || strings.TrimSpace(input.Model) == "" {
		return "", errors.New("text model configuration is incomplete")
	}
	payload := map[string]any{
		"model": input.Model,
		"messages": []map[string]string{
			{"role": "system", "content": input.System},
			{"role": "user", "content": input.User},
		},
		"temperature": 0.2,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return "", errors.New("text model request failed")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxPlannerBodySize+1))
	if err != nil {
		return "", err
	}
	if len(body) > maxPlannerBodySize {
		return "", errors.New("text model response is too large")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("text model returned HTTP %d", response.StatusCode)
	}
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &result); err != nil || len(result.Choices) == 0 || strings.TrimSpace(result.Choices[0].Message.Content) == "" {
		return "", errors.New("text model response is invalid")
	}
	return result.Choices[0].Message.Content, nil
}

func chatCompletionsURL(base string) (string, error) {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return "", errors.New("text model base url is required")
	}
	parsed, err := url.Parse(base)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return "", errors.New("text model base url is invalid")
	}
	if strings.HasSuffix(base, "/chat/completions") {
		return base, nil
	}
	if strings.HasSuffix(base, "/v1") {
		return base + "/chat/completions", nil
	}
	return base + "/v1/chat/completions", nil
}
