package settings

import (
	"testing"
	"video-production-console/internal/store"
)

func TestRuntimeKeepsActiveSnapshotUntilRestart(t *testing.T) {
	service, _, _, configured := newSettingsTestService(t, Options{})
	if _, err := service.PutPublic(t.Context(), configured); err != nil {
		t.Fatal(err)
	}
	active, err := service.Runtime(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	configured.AppServerEnabled = !configured.AppServerEnabled
	view, err := service.Update(t.Context(), configured, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if !view.RestartRequired {
		t.Fatal("restart_required=false after restart-sensitive change")
	}
	stillActive, err := service.Runtime(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if stillActive.AppServerEnabled != active.AppServerEnabled {
		t.Fatal("Runtime adopted configured app_server_enabled before restart")
	}
	if view.ActivePublic.AppServerEnabled != active.AppServerEnabled || view.Public.AppServerEnabled != configured.AppServerEnabled {
		t.Fatalf("configured/active view=%+v", view)
	}
}

func TestHotSettingsDoNotRequireRestart(t *testing.T) {
	service, _, _, configured := newSettingsTestService(t, Options{})
	if _, err := service.PutPublic(t.Context(), configured); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Runtime(t.Context()); err != nil {
		t.Fatal(err)
	}
	configured.MaxCodexConcurrency++
	view, err := service.Update(t.Context(), configured, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if view.RestartRequired {
		t.Fatal("restart_required=true for hot-only settings")
	}
}

func TestImageStreamDefaultsFalseAndHotUpdates(t *testing.T) {
	service, repo, _, configured := newSettingsTestService(t, Options{})
	if _, err := service.PutPublic(t.Context(), configured); err != nil {
		t.Fatal(err)
	}
	rt, err := service.Runtime(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if rt.ImageStream {
		t.Fatal("default image_stream=true")
	}
	configured.ImageStream = true
	view, err := service.Update(t.Context(), configured, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if view.RestartRequired || !view.Public.ImageStream || !view.ActivePublic.ImageStream {
		t.Fatalf("image_stream update: %+v", view)
	}
	rt, _ = service.Runtime(t.Context())
	if !rt.ImageStream {
		t.Fatal("runtime image_stream=false")
	}
	vals, _, err := store.NewSettingsRepository(repo).Public(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if vals["image_stream"] != "true" {
		t.Fatalf("persisted=%q", vals["image_stream"])
	}
}

func TestImageConcurrencyIsHotButEndpointAndSecretRequireRestart(t *testing.T) {
	service, _, _, configured := newSettingsTestService(t, Options{})
	if _, err := service.PutPublic(t.Context(), configured); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Runtime(t.Context()); err != nil {
		t.Fatal(err)
	}

	configured.MaxImageConcurrency = 5
	view, err := service.Update(t.Context(), configured, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if view.RestartRequired || view.ActivePublic.MaxImageConcurrency != 5 {
		t.Fatalf("image concurrency was not hot-applied: %+v", view)
	}

	configured.ImageBaseURL = "https://images.example.test/v1"
	view, err = service.Update(t.Context(), configured, map[string]string{SecretImageAPIKey: "replacement-test-key"})
	if err != nil {
		t.Fatal(err)
	}
	if !view.RestartRequired || view.ActivePublic.ImageBaseURL == configured.ImageBaseURL {
		t.Fatalf("image endpoint/secret change did not require restart: %+v", view)
	}
}

func TestAuraSTDVoiceSettingsApplyWithoutRestart(t *testing.T) {
	service, _, _, configured := newSettingsTestService(t, Options{})
	if _, err := service.PutPublic(t.Context(), configured); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Runtime(t.Context()); err != nil {
		t.Fatal(err)
	}

	configured.TTSProvider = "aurastd"
	configured.AuraSTDVoiceID = "moss_audio_hot"
	configured.AuraSTDSpeed = 1.21
	configured.AuraSTDVolume = 1.4
	configured.AuraSTDPitch = 1
	configured.AuraSTDModifyIntensity = 5
	configured.AuraSTDModifyTimbre = 6
	view, err := service.Update(t.Context(), configured, map[string]string{SecretAuraSTDTTsAPIKey: "aurastd-hot-key"})
	if err != nil {
		t.Fatal(err)
	}
	if view.RestartRequired {
		t.Fatalf("voice settings required restart: %+v", view)
	}
	runtime, err := service.Runtime(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.AuraSTDVoiceID != "moss_audio_hot" || runtime.AuraSTDSpeed != 1.21 || runtime.AuraSTDModifyTimbre != 6 {
		t.Fatalf("runtime voice settings=%+v", runtime.PublicSettings)
	}
	if runtime.AuraSTDTTsAPIKey != "aurastd-hot-key" {
		t.Fatal("runtime did not pick up the Aura Studio key")
	}
}

func TestImageGenerationAttemptsIsHotApplied(t *testing.T) {
	service, _, _, configured := newSettingsTestService(t, Options{})
	configured.ImageGenerationAttempts = 2
	if _, err := service.PutPublic(t.Context(), configured); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Runtime(t.Context()); err != nil {
		t.Fatal(err)
	}

	configured.ImageGenerationAttempts = 4
	view, err := service.Update(t.Context(), configured, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if view.RestartRequired || view.ActivePublic.ImageGenerationAttempts != 4 {
		t.Fatalf("image generation attempts was not hot-applied: %+v", view)
	}
}
