package aishorts

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// GenClient 走中转站的 OpenAI 兼容接口：
//   - 生图  POST /images/generations → data[0].b64_json（JPEG）
//   - 生视频 POST /videos → request_id；GET /videos/{id} 轮询到 status=done → video.url
//
// 视频尺寸必须用 aspect_ratio（16:9 / 9:16）+ resolution，直接传像素会被拒（实测）。
type GenClient struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

func (c *GenClient) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 4 * time.Minute}
}

func (c *GenClient) do(ctx context.Context, method, path string, body any) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, reader)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(c.APIKey))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, raw, nil
}

func apiError(status int, raw []byte) error {
	var parsed struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &parsed) == nil && parsed.Error.Message != "" {
		return fmt.Errorf("status %d: %s", status, parsed.Error.Message)
	}
	snippet := string(raw)
	if len(snippet) > 300 {
		snippet = snippet[:300]
	}
	return fmt.Errorf("status %d: %s", status, snippet)
}

// GenerateImage 出一张横图（16:9），返回 JPEG 字节。references 非空时作为参考图（data URL）。
func (c *GenClient) GenerateImage(ctx context.Context, model, prompt string, references []string) ([]byte, error) {
	body := map[string]any{"model": model, "prompt": prompt, "n": 1, "size": "1792x1024"}
	if len(references) > 0 {
		// 兼容两种常见写法：单张 image，多张 images。
		body["image"] = references[0]
		if len(references) > 1 {
			body["images"] = references
		}
	}
	status, raw, err := c.do(ctx, http.MethodPost, "/images/generations", body)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, apiError(status, raw)
	}
	var parsed struct {
		Data []struct {
			B64 string `json:"b64_json"`
			URL string `json:"url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil || len(parsed.Data) == 0 {
		return nil, errors.New("image response has no data")
	}
	if parsed.Data[0].B64 != "" {
		return base64.StdEncoding.DecodeString(parsed.Data[0].B64)
	}
	if parsed.Data[0].URL != "" {
		return c.download(ctx, parsed.Data[0].URL)
	}
	return nil, errors.New("image response has neither b64_json nor url")
}

// StartVideo 提交一段横屏（16:9）视频任务，返回 request_id。firstFrame 是 data URL，可空。
func (c *GenClient) StartVideo(ctx context.Context, model, prompt string, seconds int, firstFrame string) (string, error) {
	if seconds != 6 && seconds != 10 && seconds != 15 {
		seconds = 6
	}
	body := map[string]any{
		"model": model, "prompt": prompt,
		"seconds": fmt.Sprint(seconds), "aspect_ratio": "16:9", "resolution": "720p",
	}
	if firstFrame != "" {
		body["image"] = map[string]any{"url": firstFrame}
	}
	status, raw, err := c.do(ctx, http.MethodPost, "/videos", body)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", apiError(status, raw)
	}
	var parsed struct {
		RequestID string `json:"request_id"`
		ID        string `json:"id"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", err
	}
	id := parsed.RequestID
	if id == "" {
		id = parsed.ID
	}
	if id == "" {
		return "", errors.New("video response has no request_id")
	}
	return id, nil
}

// VideoStatus 查一次任务：done 时返回下载地址；pending 时 url 为空。
func (c *GenClient) VideoStatus(ctx context.Context, requestID string) (status string, url string, progress int, err error) {
	code, raw, err := c.do(ctx, http.MethodGet, "/videos/"+requestID, nil)
	if err != nil {
		return "", "", 0, err
	}
	if code != http.StatusOK {
		return "", "", 0, apiError(code, raw)
	}
	var parsed struct {
		Status   string `json:"status"`
		Progress int    `json:"progress"`
		Video    struct {
			URL string `json:"url"`
		} `json:"video"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", "", 0, err
	}
	if parsed.Error.Message != "" {
		return "failed", "", parsed.Progress, errors.New(parsed.Error.Message)
	}
	return parsed.Status, parsed.Video.URL, parsed.Progress, nil
}

// WaitVideo 轮询到完成并下载 mp4。
func (c *GenClient) WaitVideo(ctx context.Context, requestID string, timeout time.Duration) ([]byte, error) {
	deadline := time.Now().Add(timeout)
	for {
		status, url, _, err := c.VideoStatus(ctx, requestID)
		if err != nil {
			return nil, err
		}
		switch strings.ToLower(status) {
		case "done", "completed", "succeeded", "success":
			if url == "" {
				return nil, errors.New("video done but no url")
			}
			return c.download(ctx, url)
		case "failed", "error", "cancelled", "canceled", "moderated":
			return nil, fmt.Errorf("video task %s", status)
		}
		if time.Now().After(deadline) {
			return nil, errors.New("video task timed out")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

func (c *GenClient) download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: status %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 256<<20))
}

// DataURL 把 JPEG 字节包成 data URL 当参考图。
func DataURL(jpeg []byte) string {
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(jpeg)
}
