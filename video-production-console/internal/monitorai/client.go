// Package monitorai provides the shared OpenAI-compatible text model transport
// used by monitoring and comment insight jobs.
package monitorai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"video-production-console/internal/refanalysis"
)

const maxResponseSize = 8 << 20

// HTTPError deliberately retains only the status code from an upstream error.
type HTTPError struct{ StatusCode int }

func (e *HTTPError) Error() string { return fmt.Sprintf("模型返回 HTTP %d", e.StatusCode) }

type Client struct{ http refanalysis.HTTPClient }

// ImageURL and ContentPart model OpenAI-compatible multimodal user content.
// Text-only callers can pass a plain string; structured objects are serialized
// into a JSON string so the model sees one unambiguous data document.
type ImageURL struct {
	URL string `json:"url"`
}

type ContentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

func NewClient(client refanalysis.HTTPClient) *Client {
	if client == nil {
		client = &http.Client{}
	}
	if concrete, ok := client.(*http.Client); ok {
		copy := *concrete
		copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		client = &copy
	}
	return &Client{http: client}
}

// Complete submits one OpenAI-compatible chat completion. The caller owns the
// operation-specific timeout; response bodies and upstream error text are never
// included in returned errors.
func (c *Client) Complete(ctx context.Context, cfg refanalysis.ModelConfig, system string, content any, timeout time.Duration) (string, error) {
	var userContent any
	switch value := content.(type) {
	case string:
		userContent = value
	case []ContentPart:
		userContent = value
	case []map[string]any:
		userContent = value
	default:
		encoded, err := json.Marshal(content)
		if err != nil {
			return "", errors.New("模型请求内容无法编码")
		}
		userContent = string(encoded)
	}
	payload, err := json.Marshal(map[string]any{
		"model": cfg.Model,
		"messages": []map[string]any{
			{"role": "system", "content": system},
			{"role": "user", "content": userContent},
		},
	})
	if err != nil {
		return "", errors.New("模型请求无法编码")
	}
	if timeout <= 0 {
		return "", errors.New("模型请求超时设置无效")
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, strings.TrimRight(cfg.BaseURL, "/")+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", errors.New("模型请求地址无效")
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if callCtx.Err() != nil {
			return "", errors.New("模型请求超时或已取消")
		}
		return "", errors.New("模型连接失败，请检查模型配置或网络")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &HTTPError{StatusCode: resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil {
		return "", errors.New("模型响应读取失败")
	}
	if len(body) > maxResponseSize {
		return "", errors.New("模型响应内容过大")
	}
	var envelope struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || len(envelope.Choices) == 0 || strings.TrimSpace(envelope.Choices[0].Message.Content) == "" {
		return "", errors.New("模型响应格式无效")
	}
	return envelope.Choices[0].Message.Content, nil
}
