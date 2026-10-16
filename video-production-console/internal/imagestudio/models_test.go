package imagestudio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"video-production-console/internal/domain"
	"video-production-console/internal/settings"
)

type mutableModelRuntime struct {
	mu    sync.RWMutex
	value settings.Runtime
}

func (r *mutableModelRuntime) Runtime(context.Context) (settings.Runtime, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.value, nil
}

func (r *mutableModelRuntime) update(update func(*settings.Runtime)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	update(&r.value)
}

func TestModelCatalogFiltersGatewayModelsAndAppliesMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("path=%q, want /v1/models", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer effective-text-key" {
			t.Errorf("Authorization=%q", got)
		}
		if got := r.Header.Get("User-Agent"); got != "OpenAI/Python 2.16.0" {
			t.Errorf("User-Agent=%q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{
			{"id": "grok-imagine-image"},
			{"id": "grok-imagine-image-quality"},
			{"id": "grok-imagine-image-2.0"},
			{"id": "gemini-3.1-flash-image"},
			{"id": "dall-e-3"},
			{"id": "flux-pro"},
			{"id": "seedream-v4"},
			{"id": "imagen-4"},
			{"id": "text-only"},
			{"id": "image-video-combined"},
			{"id": "VIDEO-IMAGINE"},
			{"id": "flux-pro"},
		}})
	}))
	defer server.Close()

	runtime := &mutableModelRuntime{value: settings.Runtime{
		PublicSettings: domain.PublicSettings{
			ImageBaseURL:    server.URL + "/v1/images/generations",
			ImageModel:      "configured-image-model",
			ImageUseTextKey: true,
		},
		ImageAPIKey:     "effective-text-key",
		ImageTextAPIKey: "effective-text-key",
	}}
	response, err := NewModelCatalog(runtime, server.Client()).Models(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if response.DefaultModel != "configured-image-model" || !response.KeyConfigured || response.KeySource != "image_text" || response.ListError != "" {
		t.Fatalf("response metadata=%+v", response)
	}

	options := make(map[string]ImageModelOption, len(response.Models))
	for _, option := range response.Models {
		if _, duplicate := options[option.ID]; duplicate {
			t.Fatalf("duplicate model %q", option.ID)
		}
		options[option.ID] = option
	}
	for _, excluded := range []string{"text-only", "image-video-combined", "VIDEO-IMAGINE"} {
		if _, ok := options[excluded]; ok {
			t.Errorf("excluded model present: %q", excluded)
		}
	}
	for _, included := range []string{"grok-imagine-image", "grok-imagine-image-quality", "grok-imagine-image-2.0", "gemini-3.1-flash-image", "dall-e-3", "flux-pro", "seedream-v4", "imagen-4"} {
		if _, ok := options[included]; !ok {
			t.Errorf("image model missing: %q", included)
		}
	}
	for id, want := range map[string]string{
		"grok-imagine-image":         "快 · 约 7 秒",
		"grok-imagine-image-quality": "快 · 约 7 秒 · 画质增强",
		"grok-imagine-image-2.0":     "细节最好 · 约 15 秒",
	} {
		if got := options[id]; !got.Available || got.Note != want {
			t.Errorf("option %q=%+v, want available with note %q", id, got, want)
		}
	}
	if got := options["gemini-3.1-flash-image"]; got.Available || got.Note != "网关暂不支持生图接口" {
		t.Errorf("gemini option=%+v", got)
	}
	if got := options["flux-pro"]; !got.Available || got.Note != "" {
		t.Errorf("generic option=%+v", got)
	}
	if got := options["configured-image-model"]; got.Available || got.Note != "" {
		t.Errorf("configured missing model=%+v", got)
	}
	if len(response.Models) == 0 || response.Models[0].ID != response.DefaultModel {
		t.Fatalf("default model is not first: %+v", response.Models)
	}
}

func TestModelCatalogCachesTenMinutesAndInvalidatesOnImageSettings(t *testing.T) {
	var callsA atomic.Int32
	var callsB atomic.Int32
	var authMu sync.Mutex
	authHeaders := []string{}
	newGateway := func(calls *atomic.Int32, model string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			authMu.Lock()
			authHeaders = append(authHeaders, r.Header.Get("Authorization"))
			authMu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": model}}})
		}))
	}
	serverA := newGateway(&callsA, "flux-a")
	defer serverA.Close()
	serverB := newGateway(&callsB, "flux-b")
	defer serverB.Close()

	runtime := &mutableModelRuntime{value: settings.Runtime{
		PublicSettings: domain.PublicSettings{ImageBaseURL: serverA.URL, ImageModel: "current-a"},
		ImageAPIKey:    "key-a",
	}}
	catalog := NewModelCatalog(runtime, serverA.Client())
	before := time.Now()
	first, err := catalog.Models(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = catalog.Models(t.Context()); err != nil {
		t.Fatal(err)
	}
	if callsA.Load() != 1 {
		t.Fatalf("cached calls=%d, want 1", callsA.Load())
	}
	if ttl := catalog.expires.Sub(before); ttl < 9*time.Minute+59*time.Second || ttl > 10*time.Minute+time.Second {
		t.Fatalf("cache ttl=%s, want 10m", ttl)
	}
	if first.DefaultModel != "current-a" {
		t.Fatalf("first=%+v", first)
	}

	runtime.update(func(rt *settings.Runtime) { rt.ImageModel = "current-b" })
	second, err := catalog.Models(t.Context())
	if err != nil || callsA.Load() != 2 || second.DefaultModel != "current-b" {
		t.Fatalf("model switch calls=%d response=%+v err=%v", callsA.Load(), second, err)
	}

	runtime.update(func(rt *settings.Runtime) { rt.ImageAPIKey = "key-b" })
	if _, err = catalog.Models(t.Context()); err != nil || callsA.Load() != 3 {
		t.Fatalf("key switch calls=%d err=%v", callsA.Load(), err)
	}

	runtime.update(func(rt *settings.Runtime) { rt.ImageBaseURL = serverB.URL })
	third, err := catalog.Models(t.Context())
	if err != nil || callsB.Load() != 1 || !containsModel(third.Models, "flux-b") || containsModel(third.Models, "flux-a") {
		t.Fatalf("base switch callsB=%d response=%+v err=%v", callsB.Load(), third, err)
	}
	authMu.Lock()
	gotAuth := append([]string{}, authHeaders...)
	authMu.Unlock()
	wantAuth := []string{"Bearer key-a", "Bearer key-a", "Bearer key-b", "Bearer key-b"}
	if strings.Join(gotAuth, "|") != strings.Join(wantAuth, "|") {
		t.Fatalf("authorization headers=%q, want %q", gotAuth, wantAuth)
	}
}

func TestModelCatalogFailureReturnsBuiltinsAndDoesNotLeakKey(t *testing.T) {
	const secret = "key-that-must-not-leak"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+secret {
			t.Errorf("Authorization=%q", got)
		}
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":"gateway echoed ` + secret + `"}`))
	}))
	defer server.Close()

	runtime := runtimeStub{value: settings.Runtime{
		PublicSettings: domain.PublicSettings{ImageBaseURL: server.URL, ImageModel: "custom-image-model"},
		ImageAPIKey:    secret,
	}}
	response, err := NewModelCatalog(runtime, server.Client()).Models(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(response.Models))
	for _, option := range response.Models {
		ids = append(ids, option.ID)
	}
	sort.Strings(ids)
	want := []string{"custom-image-model", "grok-imagine-image", "grok-imagine-image-2.0", "grok-imagine-image-quality"}
	sort.Strings(want)
	if strings.Join(ids, "|") != strings.Join(want, "|") {
		t.Fatalf("fallback models=%q, want %q", ids, want)
	}
	encoded, marshalErr := json.Marshal(response)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if response.ListError == "" || !strings.Contains(response.ListError, "模型") || strings.Contains(string(encoded), secret) {
		t.Fatalf("fallback response=%s", encoded)
	}
}

func containsModel(options []ImageModelOption, id string) bool {
	for _, option := range options {
		if option.ID == id {
			return true
		}
	}
	return false
}
