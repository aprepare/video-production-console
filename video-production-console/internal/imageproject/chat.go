package imageproject

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

const DefaultChatTimeout = 10 * time.Minute

// outboundUserAgent matches the identifier CPA already accepts from the
// OpenAI Python SDK. The default Go-http-client/1.1 is treated differently.
const outboundUserAgent = "OpenAI/Python 2.16.0"

type HTTPChatClient struct {
	HTTP *http.Client
}

func NewHTTPChatClient(client *http.Client) *HTTPChatClient {
	return &HTTPChatClient{HTTP: chatHTTPClient(client)}
}

// chatHTTPClient always returns a dedicated client. Reasoning models can sit
// silent after the first SSE byte for tens of seconds; a shared/short client
// or inherited Transport deadline will look like CPA "context canceled".
func chatHTTPClient(_ *http.Client) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 0
	transport.IdleConnTimeout = 90 * time.Second
	transport.ExpectContinueTimeout = 1 * time.Second
	return &http.Client{
		Timeout:   DefaultChatTimeout,
		Transport: transport,
	}
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
	_ = ctx
	client = chatHTTPClient(client)
	endpoint, err := chatCompletionsURL(baseURL)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(apiKey) == "" || strings.TrimSpace(input.Model) == "" {
		return "", errors.New("text model configuration is incomplete")
	}
	payload := map[string]any{
		"model":  input.Model,
		"stream": false,
		"messages": []map[string]string{
			{"role": "system", "content": input.System},
			{"role": "user", "content": input.User},
		},
		"temperature": 0.2,
	}
	if effort := strings.TrimSpace(input.ReasoningEffort); effort != "" {
		payload["reasoning_effort"] = effort
	}
	if input.ResponseSchema != nil {
		name := strings.TrimSpace(input.ResponseSchemaName)
		if name == "" {
			name = "structured_output"
		}
		payload["response_format"] = map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   name,
				"strict": true,
				"schema": input.ResponseSchema,
			},
		}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	// Detach from the browser request so a 30s reverse-proxy in front of the
	// console cannot cancel the CPA call. Only our own budget may stop it.
	workCtx, cancel := context.WithTimeout(context.Background(), DefaultChatTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(workCtx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", outboundUserAgent)
	started := time.Now()
	slog.Default().Info("text model request start",
		"endpoint", endpoint,
		"model", input.Model,
		"timeout", DefaultChatTimeout.String(),
		"client_timeout", client.Timeout.String(),
		"bytes", len(encoded),
	)
	response, err := client.Do(request)
	elapsed := time.Since(started)
	if err != nil {
		slog.Default().Warn("text model request failed",
			"elapsed", elapsed.String(),
			"work_ctx_err", fmt.Sprint(workCtx.Err()),
			"error", err.Error(),
		)
		if workCtx.Err() != nil {
			return "", fmt.Errorf("text model request canceled after %s: %w", elapsed.Round(time.Millisecond), workCtx.Err())
		}
		return "", fmt.Errorf("text model request failed after %s: %v", elapsed.Round(time.Millisecond), err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxPlannerBodySize+1))
	if err != nil {
		slog.Default().Warn("text model body read failed", "elapsed", elapsed.String(), "status", response.StatusCode, "error", err.Error())
		return "", err
	}
	if len(body) > maxPlannerBodySize {
		return "", errors.New("text model response is too large")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		slog.Default().Warn("text model returned error status",
			"elapsed", elapsed.String(),
			"status", response.StatusCode,
			"body", previewText(body),
			"work_ctx_err", fmt.Sprint(workCtx.Err()),
		)
		return "", fmt.Errorf("text model returned HTTP %d after %s: %s", response.StatusCode, elapsed.Round(time.Millisecond), previewText(body))
	}
	content, err := extractChatContent(body)
	if err != nil {
		slog.Default().Warn("text model response could not be read", "elapsed", elapsed.String(), "body", previewText(body), "error", err)
		return "", err
	}
	slog.Default().Info("text model request ok", "elapsed", elapsed.String(), "content_bytes", len(content))
	return content, nil
}

func extractChatContent(body []byte) (string, error) {
	var result struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		OutputText string `json:"output_text"`
		Choices    []struct {
			Text    string `json:"text"`
			Message struct {
				Content          json.RawMessage `json:"content"`
				ReasoningContent json.RawMessage `json:"reasoning_content"`
				Reasoning        json.RawMessage `json:"reasoning"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("text model response is invalid: %s", previewText(body))
	}
	if msg := strings.TrimSpace(result.Error.Message); msg != "" && len(result.Choices) == 0 && strings.TrimSpace(result.OutputText) == "" {
		return "", fmt.Errorf("text model error: %s", msg)
	}
	candidates := make([]string, 0, 4)
	if text := strings.TrimSpace(result.OutputText); text != "" {
		candidates = append(candidates, text)
	}
	if len(result.Choices) > 0 {
		choice := result.Choices[0]
		if text := strings.TrimSpace(choice.Text); text != "" {
			candidates = append(candidates, text)
		}
		for _, raw := range []json.RawMessage{choice.Message.Content, choice.Message.ReasoningContent, choice.Message.Reasoning} {
			if text := flattenChatText(raw); text != "" {
				candidates = append(candidates, text)
			}
		}
	}
	for _, candidate := range candidates {
		if strings.Contains(candidate, "{") {
			return candidate, nil
		}
	}
	if len(candidates) > 0 {
		return candidates[0], nil
	}
	return "", fmt.Errorf("text model response is empty: %s", previewText(body))
}

func flattenChatText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	if raw[0] == '"' {
		var text string
		if err := json.Unmarshal(raw, &text); err == nil {
			return strings.TrimSpace(text)
		}
	}
	if raw[0] == '[' {
		var parts []struct {
			Text    string `json:"text"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(raw, &parts); err == nil {
			var builder strings.Builder
			for _, part := range parts {
				builder.WriteString(part.Text)
				builder.WriteString(part.Content)
			}
			return strings.TrimSpace(builder.String())
		}
	}
	var object struct {
		Text    string `json:"text"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &object); err == nil {
		if text := strings.TrimSpace(object.Text); text != "" {
			return text
		}
		return strings.TrimSpace(object.Content)
	}
	return ""
}

func previewText(body []byte) string {
	text := strings.Join(strings.Fields(string(body)), " ")
	if utf8.RuneCountInString(text) <= 400 {
		return text
	}
	return string([]rune(text)[:400]) + "…"
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
