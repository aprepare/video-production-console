package setup

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"video-production-console/internal/domain"
	"video-production-console/internal/settings"
)

type testSettings struct {
	public     domain.PublicSettings
	runtime    settings.Runtime
	secrets    map[string]string
	secretErr  error
	runtimeErr error
}

func (s *testSettings) Get(context.Context) (settings.View, error) {
	return settings.View{Public: s.public, ConfiguredPublic: s.public}, nil
}

func (s *testSettings) Runtime(context.Context) (settings.Runtime, error) {
	return s.runtime, s.runtimeErr
}

func (s *testSettings) ReadSecret(_ context.Context, key string) (string, error) {
	return s.secrets[key], s.secretErr
}

func (s *testSettings) Update(_ context.Context, public domain.PublicSettings, _ map[string]string) (settings.View, error) {
	s.public = public
	return settings.View{Public: public, RestartRequired: true}, nil
}

func writeTestFile(t *testing.T, path string, data []byte) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func statusFixture(t *testing.T, base string) *testSettings {
	t.Helper()
	root := t.TempDir()
	python := writeTestFile(t, filepath.Join(root, "python.exe"), nil)
	ffmpeg := writeTestFile(t, filepath.Join(root, "ffmpeg.exe"), nil)
	ffprobe := writeTestFile(t, filepath.Join(root, "ffprobe.exe"), nil)
	cache := writeTestFile(t, filepath.Join(root, "cache", "effect.dat"), nil)
	drafts := filepath.Join(root, "drafts")
	writeTestFile(t, filepath.Join(drafts, "root_meta_info.json"), []byte(`{"draft_new_version":"11.1.0.1","all_draft_store":[]}`))
	profile, err := json.Marshal(map[string]any{"python_binary": python, "media_root": root, "cache_paths": map[string]string{"effect": cache}})
	if err != nil {
		t.Fatal(err)
	}
	profilePath := writeTestFile(t, filepath.Join(root, "profile.json"), profile)
	public := domain.PublicSettings{
		DataRoot: root, RemixBaseURL: base, RemixModel: "writer", RemixCheckModel: "review",
		GrokBaseURL: base, GrokModel: "search", ImageBaseURL: base, ImageModel: "image",
		PlatformAPIBaseURL: base, TTSProvider: "aurastd", AuraSTDVoiceID: "voice",
		JianyingRoot: drafts, MachineProfilePath: profilePath, MediaRoot: root,
	}
	return &testSettings{public: public, runtime: settings.Runtime{PublicSettings: domain.PublicSettings{FFmpegPath: ffmpeg, FFprobePath: ffprobe}}, secrets: map[string]string{
		settings.SecretRemixAPIKey: "test-credential", settings.SecretGrokAPIKey: "test-credential",
		settings.SecretImageAPIKey: "test-credential", settings.SecretPlatformAPIToken: "test-credential", settings.SecretAuraSTDTTsAPIKey: "test-credential",
	}}
}

func statusServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-credential" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"writer"},{"id":"search"}]}`))
		case "/api/status":
			_, _ = w.Write([]byte(`{"ok":true,"apps":[{"id":"douyin","name":"抖音","ready":true},{"id":"xhs","name":"小红书","ready":true,"need_you":true},{"id":"channels","name":"视频号","ready":false,"need_you":true,"msg":"private-upstream-marker"}]}`))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func checkItem(t *testing.T, result Status, id, want string) Item {
	t.Helper()
	for _, item := range result.Items {
		if item.ID == id {
			if item.Status != want {
				t.Fatalf("%s 状态=%s，期望 %s；说明=%s", id, item.Status, want, item.Detail)
			}
			if item.Title == "" || item.Detail == "" || item.Fix == "" {
				t.Fatalf("%s 缺少面向用户的说明", id)
			}
			return item
		}
	}
	t.Fatalf("缺少检查项 %s", id)
	return Item{}
}

func TestStatusReadyUsesEveryRequiredItemAndReportsPlatforms(t *testing.T) {
	server := statusServer(t)
	cfg := statusFixture(t, server.URL)
	result, err := NewService(cfg, Options{}).Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Ready || len(result.Items) != 11 {
		t.Fatalf("ready=%v items=%d", result.Ready, len(result.Items))
	}
	for _, id := range []string{"writer_model", "review_model", "search_model", "image_model", "platform_api", "tts", "jianying", "ffmpeg", "python", "jianying_assets", "media_library"} {
		checkItem(t, result, id, "ok")
	}
	platform := checkItem(t, result, "platform_api", "ok")
	for _, phrase := range []string{"抖音 可用", "小红书 可用", "视频号 需维护"} {
		if !strings.Contains(platform.Detail, phrase) {
			t.Fatalf("平台说明缺少 %s", phrase)
		}
	}
	if strings.Contains(platform.Detail, "private-upstream-marker") {
		t.Fatal("回显了上游原始说明")
	}
}

func TestStatusMissingAndOptionalWarnings(t *testing.T) {
	cfg := &testSettings{secrets: map[string]string{}}
	result, err := NewService(cfg, Options{}).Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if result.Ready {
		t.Fatal("空配置不应就绪")
	}
	for _, id := range []string{"writer_model", "search_model", "image_model", "platform_api", "tts", "jianying", "ffmpeg", "python", "media_library"} {
		checkItem(t, result, id, "missing")
	}
	checkItem(t, result, "review_model", "warn")
	checkItem(t, result, "jianying_assets", "warn")
	if checkItem(t, result, "jianying", "missing").Fix != "jianying_detect" {
		t.Fatal("剪映应引导自动检测")
	}
}

func TestStatusWarningsDoNotMistakeUnlistedWriterForServiceFailure(t *testing.T) {
	server := statusServer(t)
	cfg := statusFixture(t, server.URL)
	cfg.public.RemixModel = "unlisted-writer"
	cfg.public.RemixCheckModel = ""
	writeTestFile(t, filepath.Join(cfg.public.JianyingRoot, "root_meta_info.json"), []byte(`{"draft_new_version":"12.2.0.1"}`))
	if err := os.Remove(filepath.Join(cfg.public.DataRoot, "cache", "effect.dat")); err != nil {
		t.Fatal(err)
	}
	result, err := NewService(cfg, Options{}).Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	checkItem(t, result, "writer_model", "warn")
	checkItem(t, result, "review_model", "warn")
	checkItem(t, result, "jianying", "warn")
	assets := checkItem(t, result, "jianying_assets", "warn")
	if !strings.Contains(assets.Detail, "1 个") {
		t.Fatal("未说明缺失缓存数量")
	}
	if result.Ready {
		t.Fatal("必需项警告不应视为已就绪")
	}
}

func TestStatusErrorsAreIndependentAndDoNotLeakUpstreamData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("private-upstream-marker https://example.invalid/?signature=private-test-marker"))
	}))
	defer server.Close()
	cfg := statusFixture(t, server.URL+"?signature=private-test-marker")
	writeTestFile(t, cfg.public.MachineProfilePath, []byte("invalid-json"))
	cfg.public.TTSProvider = "unknown"
	cfg.runtime.FFprobePath = cfg.public.DataRoot
	cfg.public.MediaRoot = filepath.Join(cfg.public.DataRoot, "missing-media")
	if err := os.Remove(filepath.Join(cfg.public.JianyingRoot, "root_meta_info.json")); err != nil {
		t.Fatal(err)
	}
	result, err := NewService(cfg, Options{}).Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"writer_model", "search_model", "platform_api", "tts", "jianying", "ffmpeg", "python", "jianying_assets", "media_library"} {
		checkItem(t, result, id, "error")
	}
	checkItem(t, result, "review_model", "ok")
	checkItem(t, result, "image_model", "ok")
	data, _ := json.Marshal(result)
	for _, forbidden := range []string{"private-test-marker", "private-upstream-marker", "signature=", "test-credential"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatal("体检暴露了敏感内容")
		}
	}
}

func TestStatusVoiceProvidersAndUnreadableSecrets(t *testing.T) {
	server := statusServer(t)
	cfg := statusFixture(t, server.URL)
	cfg.public.TTSProvider = "volc"
	cfg.public.VolcSpeechSpeakerID = "voice"
	cfg.secrets[settings.SecretVolcSpeechAPIKey] = "test-credential"
	service := NewService(cfg, Options{})
	result, err := service.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	checkItem(t, result, "tts", "ok")
	cfg.public.VolcSpeechSpeakerID = ""
	result, _ = service.Status(t.Context())
	checkItem(t, result, "tts", "missing")
	cfg.secretErr = errors.New("private-test-marker")
	result, _ = service.Status(t.Context())
	for _, id := range []string{"writer_model", "search_model", "image_model", "platform_api", "tts"} {
		checkItem(t, result, id, "error")
	}
	for _, item := range result.Items {
		if strings.Contains(item.Detail, "private-test-marker") {
			t.Fatal("回显了解密错误")
		}
	}
}

func TestStatusRunsNetworkChecksConcurrentlyWithFourSecondDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	cfg := statusFixture(t, server.URL)
	start := time.Now()
	result, err := NewService(cfg, Options{}).Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	if elapsed < 3500*time.Millisecond || elapsed >= 6*time.Second {
		t.Fatalf("并发体检耗时=%s", elapsed)
	}
	for _, id := range []string{"writer_model", "search_model", "platform_api"} {
		checkItem(t, result, id, "error")
	}
	checkItem(t, result, "python", "ok")
}
