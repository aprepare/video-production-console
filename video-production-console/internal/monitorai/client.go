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
const maxErrorResponseSize = 64 << 10

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
	payload := map[string]any{
		"model": cfg.Model,
		"messages": []map[string]any{
			{"role": "system", "content": system},
			{"role": "user", "content": userContent},
		},
		"response_format": map[string]string{"type": "json_object"},
		"temperature":     0.2,
	}
	if timeout <= 0 {
		return "", errors.New("模型请求超时设置无效")
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for attempt := 0; attempt < 2; attempt++ {
		encodedPayload, err := json.Marshal(payload)
		if err != nil {
			return "", errors.New("模型请求无法编码")
		}
		req, err := http.NewRequestWithContext(callCtx, http.MethodPost, strings.TrimRight(cfg.BaseURL, "/")+"/chat/completions", bytes.NewReader(encodedPayload))
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
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			errorBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorResponseSize))
			resp.Body.Close()
			if attempt == 0 && resp.StatusCode == http.StatusBadRequest && rejectsResponseFormat(errorBody) {
				delete(payload, "response_format")
				continue
			}
			return "", &HTTPError{StatusCode: resp.StatusCode}
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
		resp.Body.Close()
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
	return "", errors.New("模型请求失败")
}

func rejectsResponseFormat(body []byte) bool {
	signal := strings.ToLower(string(body))
	var envelope struct {
		Error struct {
			Message string `json:"message"`
			Param   string `json:"param"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) == nil {
		if strings.EqualFold(strings.TrimSpace(envelope.Error.Param), "response_format") {
			return true
		}
		if detail := strings.TrimSpace(envelope.Error.Message + " " + envelope.Error.Code); detail != "" {
			signal = strings.ToLower(detail)
		}
	}
	if !strings.Contains(signal, "response_format") {
		return false
	}
	for _, marker := range []string{"not supported", "does not support", "unsupported", "unknown", "unrecognized", "not allowed", "invalid parameter", "不支持", "未知"} {
		if strings.Contains(signal, marker) {
			return true
		}
	}
	return false
}

// UnmarshalJSON accepts a plain JSON object or an object wrapped in model
// preamble/fencing text.
func UnmarshalJSON(raw string, target any) error {
	firstErr := json.Unmarshal([]byte(raw), target)
	if firstErr == nil {
		return nil
	}
	start, end := strings.Index(raw, "{"), strings.LastIndex(raw, "}")
	if start < 0 || end < start {
		return firstErr
	}
	return json.Unmarshal([]byte(raw[start:end+1]), target)
}
