package aishorts

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
	"image"
	"image/jpeg"
	_ "image/png"
	"io"
	"math"
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
	return c.GenerateImageSize(ctx, model, prompt, references, "1792x1024")
}

func (c *GenClient) GenerateImageSize(ctx context.Context, model, prompt string, references []string, size string) ([]byte, error) {
	body := map[string]any{"model": model, "prompt": prompt, "n": 1, "size": size}
	// Grok uses aspect_ratio; keep size for the existing compatible relay.
	// https://docs.x.ai/developers/model-capabilities/images/generation#aspect-ratio
	if strings.Contains(strings.ToLower(model), "grok-imagine-image") {
		if size == "1152x2048" {
			body["aspect_ratio"] = "9:16"
		} else {
			body["aspect_ratio"] = "16:9"
		}
	}
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
	return c.StartVideoAspect(ctx, model, prompt, seconds, firstFrame, "16:9")
}

func videoReference(firstFrame, aspect string) (string, error) {
	if !strings.HasPrefix(firstFrame, "data:image/") {
		return firstFrame, nil
	}
	_, encoded, ok := strings.Cut(firstFrame, ",")
	if !ok {
		return "", errors.New("invalid first frame")
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 40_000_000 {
		return "", errors.New("first frame dimensions too large")
	}
	im, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	maxW, maxH := 1280.0, 720.0
	if aspect == "9:16" {
		maxW, maxH = 720, 1280
	}
	factor := math.Min(1, math.Min(maxW/float64(cfg.Width), maxH/float64(cfg.Height)))
	width, height := max(1, int(float64(cfg.Width)*factor)), max(1, int(float64(cfg.Height)*factor))
	resized := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.CatmullRom.Scale(resized, resized.Bounds(), im, im.Bounds(), draw.Src, nil)
	var out bytes.Buffer
	if err = jpeg.Encode(&out, resized, &jpeg.Options{Quality: 85}); err != nil {
		return "", err
	}
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(out.Bytes()), nil
}

var ErrVideoSubmissionUncertain = errors.New("video submission result unknown")
var ErrVideoTaskTerminal = errors.New("video task ended without output")

func (c *GenClient) StartVideoAspect(ctx context.Context, model, prompt string, seconds int, firstFrame, aspect string) (string, error) {
	if seconds != 6 && seconds != 10 && seconds != 15 {
		seconds = 6
	}
	if aspect != "9:16" {
		aspect = "16:9"
	}
	body := map[string]any{
		"model": model, "prompt": prompt,
		"seconds": seconds, "aspect_ratio": aspect, "resolution": "720p",
	}
	if firstFrame != "" {
		reference, err := videoReference(firstFrame, aspect)
		if err != nil {
			return "", err
		}
		body["input_reference"] = map[string]any{"image_url": reference}
	}
	status, raw, err := c.do(ctx, http.MethodPost, "/videos/generations", body)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrVideoSubmissionUncertain, err)
	}
	if status < 200 || status >= 300 {
		if status >= 500 || status == http.StatusRequestTimeout {
			return "", fmt.Errorf("%w: %v", ErrVideoSubmissionUncertain, apiError(status, raw))
		}
		return "", apiError(status, raw)
	}
	var parsed struct {
		RequestID string `json:"request_id"`
		ID        string `json:"id"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("%w: %v", ErrVideoSubmissionUncertain, err)
	}
	id := parsed.RequestID
	if id == "" {
		id = parsed.ID
	}
	if id == "" {
		return "", fmt.Errorf("%w: response has no request_id", ErrVideoSubmissionUncertain)
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
			if status == "failed" {
				return nil, fmt.Errorf("%w: %v", ErrVideoTaskTerminal, err)
			}
			return nil, err
		}
		switch strings.ToLower(status) {
		case "done", "completed", "succeeded", "success":
			if url == "" {
				return nil, errors.New("video done but no url")
			}
			return c.download(ctx, url)
		case "failed", "error", "cancelled", "canceled", "moderated", "expired":
			return nil, fmt.Errorf("%w: %s", ErrVideoTaskTerminal, status)
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
	mime := http.DetectContentType(jpeg)
	if !strings.HasPrefix(mime, "image/") {
		mime = "image/jpeg"
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(jpeg)
}
