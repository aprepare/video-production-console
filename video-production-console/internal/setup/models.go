package setup

import (
	"context"
	"crypto/sha256"
	"errors"
	"sort"
	"strings"
	"time"

	"video-production-console/internal/settings"
)

type Model struct {
	ID      string   `json:"id"`
	Sources []string `json:"sources"`
}

type ModelError struct {
	Source  string `json:"source"`
	Message string `json:"message"`
}

type Models struct {
	Models []Model      `json:"models"`
	Errors []ModelError `json:"errors"`
}

type modelResult struct {
	ids []string
	err error
}
type modelCacheEntry struct {
	result  modelResult
	expires time.Time
}
type modelFlight struct {
	done   chan struct{}
	result modelResult
}

func (s *Service) Models(ctx context.Context) (Models, error) {
	ctx, cancel := context.WithTimeout(ctx, modelTimeout)
	defer cancel()
	view, err := s.settings.Get(ctx)
	if err != nil {
		return Models{}, errors.New("配置读取失败，请稍后重试。")
	}
	p := view.Public
	sources := []struct{ name, base, secret string }{
		{"remix", p.RemixBaseURL, settings.SecretRemixAPIKey},
		{"grok", p.GrokBaseURL, settings.SecretGrokAPIKey},
		{"vision", p.VisionBaseURL, settings.SecretVisionAPIKey},
		{"image_text", p.ImageTextBaseURL, settings.SecretImageTextAPIKey},
	}
	type sourceResult struct {
		index int
		modelResult
	}
	finished := make(chan sourceResult, len(sources))
	for i, source := range sources {
		go func() {
			key, err := s.settings.ReadSecret(ctx, source.secret)
			result := modelResult{}
			switch {
			case err != nil:
				result.err = errors.New("密钥读取失败，请重新填写或激活配置码。")
			case strings.TrimSpace(source.base) == "" || strings.TrimSpace(key) == "":
				result.err = errors.New("未配置")
			default:
				result = s.cachedModels(ctx, source.base, key)
			}
			finished <- sourceResult{i, result}
		}()
	}
	results := make([]modelResult, len(sources))
	for i := range results {
		results[i].err = errors.New("模型列表检查超时或已取消，请稍后重试。")
	}
	for pending := len(sources); pending > 0; pending-- {
		select {
		case done := <-finished:
			results[done.index] = done.modelResult
		case <-ctx.Done():
			pending = 1
		}
	}
	out := Models{Models: []Model{}, Errors: []ModelError{}}
	byID := map[string][]string{}
	for i, result := range results {
		if result.err != nil {
			out.Errors = append(out.Errors, ModelError{Source: sources[i].name, Message: result.err.Error()})
			continue
		}
		for _, id := range result.ids {
			byID[id] = append(byID[id], sources[i].name)
		}
	}
	for id, sources := range byID {
		out.Models = append(out.Models, Model{ID: id, Sources: sources})
	}
	sort.Slice(out.Models, func(i, j int) bool { return out.Models[i].ID < out.Models[j].ID })
	return out, nil
}

func (s *Service) cachedModels(ctx context.Context, base, secret string) modelResult {
	address, err := endpoint(base, "/models")
	if err != nil {
		return modelResult{err: err}
	}
	// 缓存键不保存明文密钥；同地址更换密钥后立即使用独立的结果。
	key := sha256.Sum256([]byte(address + "\x00" + secret))
	s.modelMu.Lock()
	if s.modelCache == nil {
		s.modelCache = map[[32]byte]modelCacheEntry{}
		s.modelFlights = map[[32]byte]*modelFlight{}
	}
	now := s.now()
	for key, cached := range s.modelCache {
		if !now.Before(cached.expires) {
			delete(s.modelCache, key)
		}
	}
	if cached, ok := s.modelCache[key]; ok {
		s.modelMu.Unlock()
		return cached.result
	}
	flight, exists := s.modelFlights[key]
	if !exists {
		flight = &modelFlight{done: make(chan struct{})}
		s.modelFlights[key] = flight
		// 多个页面同时查询只发一次请求；一个调用者取消不影响其他等待者。
		go func() {
			probeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), modelTimeout)
			defer cancel()
			ids, err := s.requestModels(probeCtx, base, secret)
			s.modelMu.Lock()
			flight.result = modelResult{ids: ids, err: err}
			s.modelCache[key] = modelCacheEntry{result: flight.result, expires: s.now().Add(modelCacheTTL)}
			delete(s.modelFlights, key)
			close(flight.done)
			s.modelMu.Unlock()
		}()
	}
	s.modelMu.Unlock()
	select {
	case <-flight.done:
		return flight.result
	case <-ctx.Done():
		return modelResult{err: errors.New("模型列表检查超时或已取消，请稍后重试。")}
	}
}
