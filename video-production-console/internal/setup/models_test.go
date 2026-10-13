package setup

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"video-production-console/internal/domain"
	"video-production-console/internal/settings"
)

func TestModelsDeduplicateCredentialsAndCacheForFiveMinutes(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer model-test-key" {
			w.WriteHeader(401)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"z-model"},{"id":"a-model"},{"id":"a-model"}]}`))
	}))
	defer server.Close()
	var elapsed atomic.Int64
	now := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
	cfg := &testSettings{public: domain.PublicSettings{RemixBaseURL: server.URL + "/v1/", GrokBaseURL: server.URL + "/v1"}, secrets: map[string]string{settings.SecretRemixAPIKey: "model-test-key", settings.SecretGrokAPIKey: "model-test-key"}}
	service := NewService(cfg, Options{Now: func() time.Time { return now.Add(time.Duration(elapsed.Load())) }})
	for i := 0; i < 2; i++ {
		result, err := service.Models(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		want := []Model{{ID: "a-model", Sources: []string{"remix", "grok"}}, {ID: "z-model", Sources: []string{"remix", "grok"}}}
		if !reflect.DeepEqual(result.Models, want) {
			t.Fatalf("模型来源未正确合并：%+v", result.Models)
		}
		if len(result.Errors) != 2 || result.Errors[0].Source != "vision" || result.Errors[0].Message != "未配置" || result.Errors[1].Source != "image_text" {
			t.Fatalf("未配置来源=%+v", result.Errors)
		}
		if i == 0 {
			result.Models[0].Sources[0] = "mutated"
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("重复请求=%d", requests.Load())
	}
	elapsed.Store(int64(5*time.Minute - time.Nanosecond))
	if _, err := service.Models(t.Context()); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatal("缓存提前过期")
	}
	elapsed.Store(int64(5 * time.Minute))
	if _, err := service.Models(t.Context()); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 {
		t.Fatal("缓存未在五分钟后刷新")
	}
}

func TestModelsCacheSeparatesChangedSecretsAndConcurrentCallers(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") == "Bearer key-two" {
			_, _ = w.Write([]byte(`{"data":[{"id":"second"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"first"}]}`))
	}))
	defer server.Close()
	cfg := &testSettings{public: domain.PublicSettings{RemixBaseURL: server.URL, GrokBaseURL: server.URL}, secrets: map[string]string{settings.SecretRemixAPIKey: "key-one", settings.SecretGrokAPIKey: "key-two"}}
	service := NewService(cfg, Options{})
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := service.Models(t.Context())
			if err != nil || len(result.Models) != 2 {
				t.Error("并发模型请求失败")
			}
		}()
	}
	wg.Wait()
	if requests.Load() != 2 {
		t.Fatalf("不同密钥各应请求一次，实际=%d", requests.Load())
	}
	cfg.secrets[settings.SecretRemixAPIKey] = "key-three"
	if _, err := service.Models(t.Context()); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 3 {
		t.Fatal("密钥变化未使用独立缓存")
	}
}

func TestModelsRejectsRedirectsAndSanitizesErrors(t *testing.T) {
	var redirected atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
	defer destination.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+"?signature=private-model-marker", http.StatusFound)
	}))
	defer server.Close()
	cfg := &testSettings{public: domain.PublicSettings{RemixBaseURL: server.URL + "?signature=private-model-marker"}, secrets: map[string]string{settings.SecretRemixAPIKey: "private-key-marker"}}
	result, err := NewService(cfg, Options{}).Models(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Models) != 0 || len(result.Errors) != 4 || result.Errors[0].Source != "remix" {
		t.Fatal("请求失败未归入来源错误")
	}
	if redirected.Load() != 0 {
		t.Fatal("探测请求不应跟随重定向")
	}
	raw, _ := json.Marshal(result)
	for _, forbidden := range []string{"private-model-marker", "private-key-marker", "signature="} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatal("模型列表错误暴露了敏感内容")
		}
	}
}

func TestModelsHonorRequestCancellation(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-release:
		case <-r.Context().Done():
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()
	defer close(release)
	cfg := &testSettings{public: domain.PublicSettings{RemixBaseURL: server.URL}, secrets: map[string]string{settings.SecretRemixAPIKey: "key"}}
	service := NewService(cfg, Options{})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { _, _ = service.Models(ctx); close(done) }()
	<-started
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("取消后仍阻塞等待模型服务")
	}
}
