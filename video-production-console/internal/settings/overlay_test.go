package settings

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"video-production-console/internal/domain"
	"video-production-console/internal/security"
	"video-production-console/internal/taskmodel"
)

func TestOverlayPublicKeepsStoredValuesAndIgnoresUnknownKeys(t *testing.T) {
	base := domain.PublicSettings{
		ListenAddr:          "127.0.0.1:2030",
		RemixBaseURL:        "http://127.0.0.1:2001/v1",
		RemixModel:          "old-model",
		ImageBaseURL:        "http://127.0.0.1:8320/v1",
		ImageModel:          "gpt-image-2",
		MaxImageConcurrency: 12,
	}
	merged, err := OverlayPublic(base, json.RawMessage(`{"remix_model":"gpt-5.6-sol","unknown_field":"drop-me","max_image_concurrency":8}`))
	if err != nil {
		t.Fatal(err)
	}
	if merged.RemixModel != "gpt-5.6-sol" {
		t.Fatalf("remix_model=%q", merged.RemixModel)
	}
	if merged.RemixBaseURL != base.RemixBaseURL || merged.ImageBaseURL != base.ImageBaseURL || merged.ImageModel != base.ImageModel {
		t.Fatalf("overlay dropped stored values: %+v", merged)
	}
	if merged.MaxImageConcurrency != 8 {
		t.Fatalf("max_image_concurrency=%d", merged.MaxImageConcurrency)
	}
}

func TestOverlayPublicRejectsBrokenJSON(t *testing.T) {
	_, err := OverlayPublic(domain.PublicSettings{}, json.RawMessage(`{"remix_model":`))
	if !errors.Is(err, ErrInvalidSettings) {
		t.Fatalf("error=%v", err)
	}
}

func TestUserMessageNamesInvalidField(t *testing.T) {
	got := UserMessage(invalid("remix_base_url"))
	if !strings.Contains(got, "二创服务地址") {
		t.Fatalf("message=%q", got)
	}
	if got := UserMessage(ErrUnknownSecret); !strings.Contains(got, "密钥") {
		t.Fatalf("unknown secret message=%q", got)
	}
	if got := UserMessage(security.ErrSecretTooLarge); !strings.Contains(got, "过长") {
		t.Fatalf("too large message=%q", got)
	}
}

func TestUpdateAcceptsGptRemixModelWithoutCursorPrefix(t *testing.T) {
	service, _, _, public := newSettingsTestService(t, Options{})
	public.RemixModel = "  gpt-5.6-sol  "
	view, err := service.Update(t.Context(), public, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if view.Public.RemixModel != "gpt-5.6-sol" {
		t.Fatalf("remix_model=%q", view.Public.RemixModel)
	}
}

func TestResolveTaskModelAllowsEmptyRemixEffort(t *testing.T) {
	service, _, _, public := newSettingsTestService(t, Options{})
	public.RemixModel = "gpt-5.6-sol"
	public.RemixReasoningEffort = ""
	if _, err := service.PutPublic(t.Context(), public); err != nil {
		t.Fatal(err)
	}
	selection, err := service.ResolveTaskModel(t.Context(), taskmodel.Selection{Kind: taskmodel.KindRemix})
	if err != nil {
		t.Fatal(err)
	}
	if selection.Model != "gpt-5.6-sol" || selection.ReasoningEffort != "" {
		t.Fatalf("selection=%+v", selection)
	}

	public.RemixReasoningEffort = "high"
	if _, err := service.PutPublic(t.Context(), public); err != nil {
		t.Fatal(err)
	}
	selection, err = service.ResolveTaskModel(t.Context(), taskmodel.Selection{Kind: taskmodel.KindRemix})
	if err != nil {
		t.Fatal(err)
	}
	if selection.ReasoningEffort != "high" {
		t.Fatalf("selection=%+v", selection)
	}
}
