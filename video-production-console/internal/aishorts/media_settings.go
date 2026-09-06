package aishorts

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"video-production-console/internal/security"
)

// Media endpoints are shared by AI shorts, independently of the writing API.
type MediaEndpoint struct {
	BaseURL   string `json:"base_url"`
	HasAPIKey bool   `json:"has_api_key"`
}
type MediaSettings struct {
	ImageConcurrency int           `json:"image_concurrency"`
	VideoConcurrency int           `json:"video_concurrency"`
	Image            MediaEndpoint `json:"image"`
	Video            MediaEndpoint `json:"video"`
}
type MediaEndpointInput struct {
	BaseURL     string `json:"base_url"`
	APIKey      string `json:"api_key,omitempty"`
	ClearAPIKey bool   `json:"clear_api_key,omitempty"`
}
type MediaSettingsInput struct {
	ImageConcurrency int                `json:"image_concurrency"`
	VideoConcurrency int                `json:"video_concurrency"`
	Image            MediaEndpointInput `json:"image"`
	Video            MediaEndpointInput `json:"video"`
}
type storedEndpoint struct {
	BaseURL string `json:"base_url"`
	Cipher  []byte `json:"key_cipher,omitempty"`
}
type storedMediaSettings struct {
	ImageConcurrency int            `json:"image_concurrency"`
	VideoConcurrency int            `json:"video_concurrency"`
	Image            storedEndpoint `json:"image"`
	Video            storedEndpoint `json:"video"`
}
type mediaSettingsStore struct {
	path      string
	mu        sync.Mutex
	protector security.Protector
}

func (s *mediaSettingsStore) read() (storedMediaSettings, error) {
	var out storedMediaSettings
	raw, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(raw, &out)
	return out, err
}
func publicMediaSettings(in storedMediaSettings) MediaSettings {
	return MediaSettings{Image: MediaEndpoint{in.Image.BaseURL, len(in.Image.Cipher) > 0}, Video: MediaEndpoint{in.Video.BaseURL, len(in.Video.Cipher) > 0}, ImageConcurrency: concurrencyOr(in.ImageConcurrency, 20), VideoConcurrency: concurrencyOr(in.VideoConcurrency, 6)}
}
func concurrencyOr(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}
func (s *Service) MediaSettings() (MediaSettings, error) {
	s.media.mu.Lock()
	defer s.media.mu.Unlock()
	in, err := s.media.read()
	return publicMediaSettings(in), err
}
func normalizeMediaURL(raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("接口URL需为http(s)地址，不包含密钥、查询参数或片段")
	}
	for _, suffix := range []string{"/images/generations", "/videos/generations", "/chat/completions"} {
		if strings.HasSuffix(u.Path, suffix) {
			return "", errors.New("请填写Base URL（如https://example.com/v1），不要填写完整生成路径")
		}
	}
	return raw, nil
}
func (s *Service) UpdateMediaSettings(in MediaSettingsInput) (MediaSettings, error) {
	s.media.mu.Lock()
	defer s.media.mu.Unlock()
	out, err := s.media.read()
	if err != nil {
		return MediaSettings{}, err
	}
	if in.ImageConcurrency < 0 || in.ImageConcurrency > 64 || in.VideoConcurrency < 0 || in.VideoConcurrency > 64 {
		return MediaSettings{}, errors.New("图片、视频并发范围为1至64")
	}
	out.ImageConcurrency = concurrencyOr(in.ImageConcurrency, 20)
	out.VideoConcurrency = concurrencyOr(in.VideoConcurrency, 6)
	apply := func(dst *storedEndpoint, src MediaEndpointInput) error {
		base, err := normalizeMediaURL(src.BaseURL)
		if err != nil {
			return err
		}
		if base != dst.BaseURL || src.ClearAPIKey {
			dst.Cipher = nil
		}
		dst.BaseURL = base
		if key := strings.TrimSpace(src.APIKey); key != "" {
			dst.Cipher, err = s.media.protector.Protect([]byte(key))
			if err != nil {
				return err
			}
		}
		return nil
	}
	if err = apply(&out.Image, in.Image); err != nil {
		return MediaSettings{}, err
	}
	if err = apply(&out.Video, in.Video); err != nil {
		return MediaSettings{}, err
	}
	raw, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return MediaSettings{}, err
	}
	if err = os.MkdirAll(filepath.Dir(s.media.path), 0755); err != nil {
		return MediaSettings{}, err
	}
	if err = os.WriteFile(s.media.path+".tmp", raw, 0600); err != nil {
		return MediaSettings{}, err
	}
	if err = os.Rename(s.media.path+".tmp", s.media.path); err != nil {
		return MediaSettings{}, err
	}
	return publicMediaSettings(out), nil
}
func (s *Service) mediaRuntime(rt Runtime) (Runtime, error) {
	s.media.mu.Lock()
	defer s.media.mu.Unlock()
	in, err := s.media.read()
	if err != nil {
		return rt, err
	}
	rt.ImageConcurrency = concurrencyOr(in.ImageConcurrency, 20)
	rt.VideoConcurrency = concurrencyOr(in.VideoConcurrency, 6)
	rt.MediaResolved = true
	resolve := func(ep storedEndpoint) (string, string, error) {
		base := ep.BaseURL
		if base == "" {
			base = rt.BaseURL
		}
		if len(ep.Cipher) > 0 {
			key, err := s.media.protector.Unprotect(ep.Cipher)
			return base, string(key), err
		}
		// An explicitly different endpoint must never receive the writing API key.
		if strings.TrimRight(base, "/") == strings.TrimRight(rt.BaseURL, "/") {
			return base, rt.APIKey, nil
		}
		return base, "", nil
	}
	rt.ImageBaseURL, rt.ImageAPIKey, err = resolve(in.Image)
	if err != nil {
		return rt, err
	}
	rt.VideoBaseURL, rt.VideoAPIKey, err = resolve(in.Video)
	return rt, err
}

func videoClient(rt Runtime, fallback *GenClient) *GenClient {
	if rt.VideoBaseURL == "" && !rt.MediaResolved {
		return fallback
	}
	return &GenClient{BaseURL: rt.VideoBaseURL, APIKey: rt.VideoAPIKey, HTTP: fallback.HTTP}
}
