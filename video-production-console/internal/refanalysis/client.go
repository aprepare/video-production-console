package refanalysis

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

	"video-production-console/internal/store"
)

type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type Client struct{ http HTTPClient }

func NewClient(client HTTPClient) *Client {
	if client == nil {
		client = &http.Client{Timeout: 6 * time.Minute}
	}
	if concrete, ok := client.(*http.Client); ok {
		copy := *concrete
		copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		client = &copy
	}
	return &Client{http: client}
}

func (c *Client) Analyze(ctx context.Context, cfg ModelConfig, post store.ReferencePost, info VideoInfo, shots []Shot, frames []Frame, focus string) (Report, error) {
	payload, err := buildRequest(cfg, post, info, shots, frames, focus)
	if err != nil {
		return Report{}, err
	}
	callCtx, cancel := context.WithTimeout(ctx, 6*time.Minute)
	defer cancel()
	request, err := http.NewRequestWithContext(callCtx, http.MethodPost, strings.TrimRight(cfg.BaseURL, "/")+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return Report{}, errors.New("拆解模型请求地址无效")
	}
	request.Header.Set("Content-Type", "application/json")
	if cfg.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	response, err := c.http.Do(request)
	if err != nil {
		if callCtx.Err() != nil {
			return Report{}, errors.New("拆解模型请求超时或已取消")
		}
		return Report{}, errors.New("拆解模型连接失败，请检查模型配置或网络")
	}
	defer response.Body.Close()
	// 上游错误响应可能包含 URL、密钥或请求正文，只保留状态码。
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Report{}, fmt.Errorf("拆解模型返回 HTTP %d", response.StatusCode)
	}
	const maxResponseSize = 8 << 20
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseSize+1))
	if err != nil || len(body) > maxResponseSize {
		return Report{}, errors.New("拆解模型响应读取失败或内容过大")
	}
	var envelope struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || len(envelope.Choices) == 0 {
		return Report{}, errors.New("拆解模型响应格式无效")
	}
	return ParseReport(envelope.Choices[0].Message.Content)
}
