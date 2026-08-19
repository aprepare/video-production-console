package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"video-production-console/internal/domain"
	"video-production-console/internal/partnerclient"
	consoleSettings "video-production-console/internal/settings"
)

type fakePartnerSettingsService struct {
	view       consoleSettings.View
	runtime    partnerclient.RuntimeSnapshot
	updated    domain.PublicSettings
	updateCall int
}

func fakeSettings() *fakePartnerSettingsService {
	public := domain.PublicSettings{
		RemixBaseURL:        "https://23.138.12.112:2001/v1",
		ImageBaseURL:        "https://23.138.12.112:2001/v1",
		AuraSTDBaseURL:      "https://tts.aurastd.com",
		MediaRoot:           `D:\Media`,
		JianyingRoot:        `D:\Jianying`,
		MachineProfilePath:  `D:\profiles\machine.json`,
		DefaultImageRatio:   "9:16",
		DefaultImageStyle:   "photorealistic",
		AuraSTDSpeed:        1.1,
		AuraSTDVolume:       1.2,
		MaxCodexConcurrency: 2,
	}
	return &fakePartnerSettingsService{
		view: consoleSettings.View{
			Public:  public,
			Secrets: map[string]domain.SecretStatus{"remix_api_key": {Configured: true, Masked: "secret-mask"}},
		},
		runtime: partnerclient.RuntimeSnapshot{
			Capabilities: partnerclient.Capabilities{
				TextModels:       []string{"gpt-5.6-sol"},
				ReasoningEfforts: []string{"medium", "high"},
				ImageModel:       "gpt-image-2",
			},
			Aura: partnerclient.AuraRuntime{
				BaseURL: "https://23.138.12.112:2001/aura",
				APIKey:  "runtime-aura-secret",
				Model:   "speech-2.8-hd",
				VoiceID: "partner-voice",
				Speed:   1.5,
				Volume:  1.6,
			},
		},
	}
}

func (s *fakePartnerSettingsService) Get(context.Context) (consoleSettings.View, error) {
	return s.view, nil
}

func (s *fakePartnerSettingsService) Update(_ context.Context, public domain.PublicSettings, _ map[string]string) (consoleSettings.View, error) {
	s.updated = public
	s.updateCall++
	s.view.Public = public
	return s.view, nil
}

func (s *fakePartnerSettingsService) RuntimeSnapshot() partnerclient.RuntimeSnapshot {
	return s.runtime
}

func TestPartnerSettingsNeverReturnOrAcceptServiceFields(t *testing.T) {
	service := fakeSettings()
	handler := NewPartnerSettingsHandler(service)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", response.Code, response.Body.String())
	}
	for _, forbidden := range []string{"base_url", "api_key", "23.138.12.112", "aurastd_base_url", "secrets", "secret-mask", "runtime-aura-secret"} {
		if strings.Contains(strings.ToLower(response.Body.String()), forbidden) {
			t.Fatalf("leaked %q: %s", forbidden, response.Body.String())
		}
	}

	bad := `{"remix_base_url":"http://attacker","media_root":"D:\\\\Media"}`
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(bad)))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if service.updateCall != 0 {
		t.Fatalf("rejected update reached service %d times", service.updateCall)
	}
}

func TestPartnerSettingsReturnsFixedRuntimeChoicesAndAllowsOnlyLocalPresentationFields(t *testing.T) {
	service := fakeSettings()
	handler := NewPartnerSettingsHandler(service)
	response := httptest.NewRecorder()
	original := service.view.Public

	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/settings", nil))

	body := response.Body.String()
	for _, required := range []string{
		`"text_models":["gpt-5.6-sol"]`,
		`"reasoning_efforts":["medium","high"]`,
		`"image_model":"gpt-image-2"`,
		`"aura_model":"speech-2.8-hd"`,
		`"aura_voice_id":"partner-voice"`,
		`"media_root":"D:\\Media"`,
	} {
		if !strings.Contains(body, required) {
			t.Fatalf("GET body missing %q: %s", required, body)
		}
	}

	update := `{
		"media_root":"E:\\Media",
		"jianying_root":"E:\\Jianying",
		"machine_profile_path":"E:\\profiles\\machine.json",
		"default_image_ratio":"1:1",
		"default_image_style":"illustration",
		"aura_speed":1.25,
		"aura_volume":1.4
	}`
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(update)))
	if response.Code != http.StatusOK {
		t.Fatalf("PUT status=%d body=%s", response.Code, response.Body.String())
	}
	if service.updateCall != 1 {
		t.Fatalf("update calls=%d", service.updateCall)
	}
	if service.updated.MediaRoot != `E:\Media` ||
		service.updated.JianyingRoot != `E:\Jianying` ||
		service.updated.MachineProfilePath != `E:\profiles\machine.json` ||
		service.updated.DefaultImageRatio != "1:1" ||
		service.updated.DefaultImageStyle != "illustration" ||
		service.updated.AuraSTDSpeed != 1.25 ||
		service.updated.AuraSTDVolume != 1.4 {
		t.Fatalf("updated public settings=%+v", service.updated)
	}
	if service.updated.RemixBaseURL != original.RemixBaseURL ||
		service.updated.ImageBaseURL != original.ImageBaseURL ||
		service.updated.AuraSTDBaseURL != original.AuraSTDBaseURL {
		t.Fatalf("service-owned settings changed: %+v", service.updated)
	}
}
