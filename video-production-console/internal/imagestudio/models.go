package imagestudio

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"video-production-console/internal/settings"
)

type ImageModelOption struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Note      string `json:"note"`
	Available bool   `json:"available"`
}
type ImageModelsResponse struct {
	DefaultModel  string             `json:"default_model"`
	Models        []ImageModelOption `json:"models"`
	KeyConfigured bool               `json:"key_configured"`
	KeySource     string             `json:"key_source"`
	ListError     string             `json:"list_error,omitempty"`
}
type ModelCatalog struct {
	runtime     RuntimeProvider
	client      *http.Client
	mu          sync.Mutex
	fingerprint [32]byte
	expires     time.Time
	cached      ImageModelsResponse
}

func NewModelCatalog(runtime RuntimeProvider, client *http.Client) *ModelCatalog {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &ModelCatalog{runtime: runtime, client: &copyClient}
}
func (c *ModelCatalog) Models(ctx context.Context) (ImageModelsResponse, error) {
	rt, err := c.runtime.Runtime(ctx)
	if err != nil {
		return ImageModelsResponse{}, err
	}
	// Hash the settings snapshot and secrets; never expose credentials in a cache key.
	public, _ := json.Marshal(rt.PublicSettings)
	fingerprint := sha256.Sum256(append(public, []byte("\x00"+rt.ImageAPIKey+"\x00"+rt.ImageTextAPIKey)...))
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fingerprint == fingerprint && time.Now().Before(c.expires) {
		return cloneModels(c.cached), nil
	}
	response := ImageModelsResponse{DefaultModel: rt.ImageModel, KeyConfigured: strings.TrimSpace(rt.ImageAPIKey) != "", KeySource: "image"}
	if response.DefaultModel == "" {
		response.DefaultModel = "grok-imagine-image-2.0"
	}
	if rt.ImageUseTextKey && strings.TrimSpace(rt.ImageTextAPIKey) != "" {
		response.KeySource = "image_text"
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ids, err := c.fetch(ctx, rt)
	if err != nil {
		response.ListError = "暂时无法获取网关模型列表，已显示内置模型，可稍后刷新"
		ids = []string{"grok-imagine-image-2.0", "grok-imagine-image", "grok-imagine-image-quality"}
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] || !isImageModel(id) {
			continue
		}
		seen[id] = true
		response.Models = append(response.Models, modelOption(id, true))
	}
	if !seen[response.DefaultModel] {
		response.Models = append(response.Models, modelOption(response.DefaultModel, false))
	}
	sort.SliceStable(response.Models, func(i, j int) bool {
		iDefault := response.Models[i].ID == response.DefaultModel
		jDefault := response.Models[j].ID == response.DefaultModel
		if iDefault != jDefault {
			return iDefault
		}
		return response.Models[i].ID < response.Models[j].ID
	})
	c.fingerprint = fingerprint
	c.expires = time.Now().Add(10 * time.Minute)
	c.cached = response
	return cloneModels(response), nil
}
func cloneModels(r ImageModelsResponse) ImageModelsResponse {
	r.Models = append([]ImageModelOption{}, r.Models...)
	return r
}
func (c *ModelCatalog) fetch(ctx context.Context, rt settings.Runtime) ([]string, error) {
	if strings.TrimSpace(rt.ImageAPIKey) == "" {
		return nil, ErrNotConfigured
	}
	base := strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(rt.ImageBaseURL), "/"), "/images/generations")
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, ErrNotConfigured
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+rt.ImageAPIKey)
	req.Header.Set("User-Agent", "OpenAI/Python 2.16.0")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, errors.New("模型列表请求失败")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, errors.New("模型列表网关不可用")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		return nil, errors.New("模型列表响应无效")
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err = json.Unmarshal(body, &payload); err != nil || payload.Data == nil {
		return nil, errors.New("模型列表响应无效")
	}
	ids := make([]string, 0, len(payload.Data))
	for _, m := range payload.Data {
		ids = append(ids, m.ID)
	}
	return ids, nil
}
func isImageModel(id string) bool {
	id = strings.ToLower(id)
	if strings.Contains(id, "video") {
		return false
	}
	for _, part := range []string{"image", "imagine", "dall", "flux", "seedream", "imagen"} {
		if strings.Contains(id, part) {
			return true
		}
	}
	return false
}
func modelOption(id string, available bool) ImageModelOption {
	m := ImageModelOption{ID: id, Label: id, Available: available}
	switch id {
	case "grok-imagine-image":
		m.Label = "Grok Imagine"
		m.Note = "快 · 约 7 秒"
	case "grok-imagine-image-quality":
		m.Label = "Grok Imagine Quality"
		m.Note = "快 · 约 7 秒 · 画质增强"
	case "grok-imagine-image-2.0":
		m.Label = "Grok Imagine 2.0"
		m.Note = "细节最好 · 约 15 秒"
	case "gemini-3.1-flash-image":
		m.Available = false
		m.Note = "网关暂不支持生图接口"
	}
	return m
}
