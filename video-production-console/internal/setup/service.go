// Package setup 编排首次配置和依赖体检，所有诊断只返回可公开的中文说明。
package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"video-production-console/internal/domain"
	"video-production-console/internal/jianyingdraft"
	"video-production-console/internal/settings"
)

const (
	checkTimeout    = 4 * time.Second
	modelTimeout    = 5 * time.Second
	modelCacheTTL   = 5 * time.Minute
	maxResponseSize = 1 << 20
)

type Settings interface {
	Get(context.Context) (settings.View, error)
	Runtime(context.Context) (settings.Runtime, error)
	ReadSecret(context.Context, string) (string, error)
	Update(context.Context, domain.PublicSettings, map[string]string) (settings.View, error)
}

type Options struct {
	HTTPClient *http.Client
	Detect     func() (jianyingdraft.Detection, error)
	Now        func() time.Time
}

type Service struct {
	settings     Settings
	http         *http.Client
	detect       func() (jianyingdraft.Detection, error)
	now          func() time.Time
	modelMu      sync.Mutex
	modelCache   map[[32]byte]modelCacheEntry
	modelFlights map[[32]byte]*modelFlight
	applyMu      sync.Mutex
}

func NewService(config Settings, options Options) *Service {
	client := http.Client{Timeout: modelTimeout}
	if options.HTTPClient != nil {
		client = *options.HTTPClient
	}
	// 探测只访问已配置的服务，禁止重定向把凭据转发到另一个地址。
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if options.Detect == nil {
		options.Detect = jianyingdraft.Detect
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Service{settings: config, http: &client, detect: options.Detect, now: options.Now}
}

func endpoint(base, suffix string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" {
		return "", errors.New("服务地址无效，请检查设置中的接口地址。")
	}
	u.Path = strings.TrimRight(u.Path, "/") + suffix
	u.RawPath = ""
	return u.String(), nil
}

func (s *Service) requestJSON(ctx context.Context, base, suffix, key string, target any) error {
	address, err := endpoint(base, suffix)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return errors.New("请求创建失败，请检查服务地址。")
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	response, err := s.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return errors.New("检查超时或已取消，请检查网络后重试。")
		}
		return errors.New("暂时无法连接服务，请检查网络和接口地址后重试。")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("服务返回 HTTP %d，请检查地址、密钥或联系服务商。", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseSize+1))
	if err != nil {
		return errors.New("服务响应读取失败，请检查网络后重试。")
	}
	if len(raw) > maxResponseSize {
		return errors.New("服务响应过大，请联系服务商检查接口。")
	}
	if json.Unmarshal(raw, target) != nil {
		return errors.New("服务响应格式不正确，请联系服务商检查接口。")
	}
	return nil
}

func (s *Service) requestModels(ctx context.Context, base, key string) ([]string, error) {
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := s.requestJSON(ctx, base, "/models", key, &payload); err != nil {
		return nil, err
	}
	if payload.Data == nil {
		return nil, errors.New("服务未返回模型列表，请联系服务商检查接口。")
	}
	ids := make([]string, 0, len(payload.Data))
	seen := map[string]bool{}
	for _, model := range payload.Data {
		id := strings.TrimSpace(model.ID)
		if id != "" && !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	return ids, nil
}
