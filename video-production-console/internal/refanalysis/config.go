// Package refanalysis 将对标视频的镜头、关键帧和口播组织成可复刻的拆解报告。
package refanalysis

import (
	"errors"
	"net/url"
	"strings"

	"video-production-console/internal/settings"
)

var (
	ErrNotConfigured = errors.New("请先在设置里配置拆解模型")
	ErrInvalidInput  = errors.New("拆解参数不正确")
	ErrMediaNotReady = errors.New("原视频还没下载好，先等下载完成。")
	ErrUnsafePath    = errors.New("分析文件路径不安全")
)

// ModelConfig 的地址和密钥始终取自同一组设置，不能序列化密钥。
type ModelConfig struct {
	BaseURL string `json:"-"`
	APIKey  string `json:"-"`
	Model   string
}

func ResolveConfig(rt settings.Runtime, requestedModel string) (ModelConfig, error) {
	cfg := ModelConfig{}
	for _, model := range []string{requestedModel, rt.AnalysisModel, rt.VisionModel, rt.GrokModel} {
		if model = strings.TrimSpace(model); model != "" {
			cfg.Model = model
			break
		}
	}
	for _, pair := range []struct{ base, key string }{
		{rt.AnalysisBaseURL, rt.AnalysisAPIKey}, {rt.VisionBaseURL, rt.VisionAPIKey},
		{rt.GrokBaseURL, rt.GrokAPIKey}, {rt.RemixBaseURL, rt.RemixAPIKey},
	} {
		if base := strings.TrimSpace(pair.base); base != "" {
			cfg.BaseURL, cfg.APIKey = strings.TrimRight(base, "/"), pair.key
			break
		}
	}
	if cfg.BaseURL == "" || cfg.Model == "" {
		return ModelConfig{}, ErrNotConfigured
	}
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(cfg.Model) > 256 {
		return ModelConfig{}, ErrInvalidInput
	}
	return cfg, nil
}
