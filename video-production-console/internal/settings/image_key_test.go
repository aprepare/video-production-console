package settings

import (
	"bytes"
	"testing"

	"video-production-console/internal/store"
)

func TestImageRuntimeUsesConfiguredKeyPolicyAndBaseFallback(t *testing.T) {
	service, _, _, public := newSettingsTestService(t, Options{})
	public.ImageUseTextKey = true
	public.ImageBaseURL = ""
	public.ImageTextBaseURL = "https://text-images.example.test/v1"
	public.ImageModel = "grok-imagine-image-2.0"
	public.ImageTextModel = "text-model-a"

	view, err := service.Update(t.Context(), public, map[string]string{
		SecretImageAPIKey:     "own-image-key",
		SecretImageTextAPIKey: "shared-text-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	if view.RestartRequired {
		t.Fatal("initial image configuration required restart")
	}
	runtime, err := service.Runtime(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.ImageAPIKey != "shared-text-key" || runtime.ImageTextAPIKey != "shared-text-key" {
		t.Fatal("shared runtime key policy was not applied")
	}
	if runtime.ImageBaseURL != public.ImageTextBaseURL {
		t.Fatalf("image base=%q, want text fallback %q", runtime.ImageBaseURL, public.ImageTextBaseURL)
	}

	public.ImageUseTextKey = false
	public.ImageBaseURL = "https://dedicated-images.example.test/v1"
	public.ImageModel = "grok-imagine-image"
	public.ImageTextBaseURL = "https://new-text.example.test/v1"
	public.ImageTextModel = "text-model-b"
	view, err = service.Update(t.Context(), public, nil)
	if err != nil {
		t.Fatal(err)
	}
	if view.RestartRequired {
		t.Fatal("image endpoint, model, text endpoint/model, or key policy required restart")
	}
	runtime, err = service.Runtime(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.ImageAPIKey != "own-image-key" {
		t.Fatal("original dedicated image key was not restored")
	}
	if runtime.ImageBaseURL != public.ImageBaseURL || runtime.ImageModel != public.ImageModel || runtime.ImageTextBaseURL != public.ImageTextBaseURL || runtime.ImageTextModel != public.ImageTextModel {
		t.Fatalf("image settings were not hot-applied: %+v", runtime.PublicSettings)
	}
}

func TestImageRuntimeFallsBackToDedicatedKeyWhenSharedKeyEmpty(t *testing.T) {
	service, _, _, public := newSettingsTestService(t, Options{})
	public.ImageUseTextKey = true
	if _, err := service.Update(t.Context(), public, map[string]string{SecretImageAPIKey: "fallback-image-key"}); err != nil {
		t.Fatal(err)
	}
	runtime, err := service.Runtime(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.ImageAPIKey != "fallback-image-key" {
		t.Fatal("effective image key did not use the dedicated fallback")
	}
}

func TestImageKeysRotateWithoutRestartOrIndependentKeyPollution(t *testing.T) {
	service, _, _, public := newSettingsTestService(t, Options{})
	public.ImageUseTextKey = true
	if _, err := service.Update(t.Context(), public, map[string]string{
		SecretImageAPIKey:     "image-key-a",
		SecretImageTextAPIKey: "text-key-a",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Runtime(t.Context()); err != nil {
		t.Fatal(err)
	}

	view, err := service.Update(t.Context(), public, map[string]string{
		SecretImageAPIKey:     "image-key-b",
		SecretImageTextAPIKey: "text-key-b",
	})
	if err != nil {
		t.Fatal(err)
	}
	if view.RestartRequired {
		t.Fatal("image key rotation required restart")
	}
	runtime, _ := service.Runtime(t.Context())
	if runtime.ImageAPIKey != "text-key-b" || runtime.ImageTextAPIKey != "text-key-b" {
		t.Fatal("rotated shared key was not applied")
	}

	public.ImageUseTextKey = false
	view, err = service.Update(t.Context(), public, nil)
	if err != nil {
		t.Fatal(err)
	}
	if view.RestartRequired {
		t.Fatal("key source switch required restart")
	}
	runtime, _ = service.Runtime(t.Context())
	if runtime.ImageAPIKey != "image-key-b" {
		t.Fatal("dedicated key was polluted by the effective shared key")
	}
}

type canonicalizingProtector struct{}

func (canonicalizingProtector) Protect(plain []byte) ([]byte, error) {
	return bytes.ToUpper(bytes.Clone(plain)), nil
}

func (canonicalizingProtector) Unprotect(ciphertext []byte) ([]byte, error) {
	return bytes.Clone(ciphertext), nil
}

func TestImageKeyHotUpdateUsesDecryptedStoredSecret(t *testing.T) {
	_, db, _, public := newSettingsTestService(t, Options{})
	service := NewService(store.NewSettingsRepository(db), canonicalizingProtector{})
	if _, err := service.PutPublic(t.Context(), public); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Runtime(t.Context()); err != nil {
		t.Fatal(err)
	}
	view, err := service.Update(t.Context(), public, map[string]string{
		SecretImageAPIKey:     "image-lowercase",
		SecretImageTextAPIKey: "text-lowercase",
	})
	if err != nil {
		t.Fatal(err)
	}
	if view.RestartRequired {
		t.Fatal("decrypted image secret hot update required restart")
	}
	runtime, err := service.Runtime(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.ImageAPIKey != "TEXT-LOWERCASE" || runtime.ImageTextAPIKey != "TEXT-LOWERCASE" {
		t.Fatal("runtime did not use the decrypted stored image secrets")
	}
}

func TestImageUseTextKeyDefaultsTrueWhenMissing(t *testing.T) {
	service, db, _, public := newSettingsTestService(t, Options{})
	if _, err := service.PutPublic(t.Context(), public); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM settings WHERE key='image_use_text_key'`); err != nil {
		t.Fatal(err)
	}
	view, err := service.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !view.Public.ImageUseTextKey {
		t.Fatal("missing image_use_text_key defaulted false")
	}
}
