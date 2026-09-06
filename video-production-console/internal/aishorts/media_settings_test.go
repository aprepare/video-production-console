package aishorts

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testMediaProtector struct{}

func (testMediaProtector) Protect(raw []byte) ([]byte, error) {
	return []byte(base64.StdEncoding.EncodeToString(raw)), nil
}
func (testMediaProtector) Unprotect(raw []byte) ([]byte, error) {
	return base64.StdEncoding.DecodeString(string(raw))
}

func TestMediaSettingsRoutingEncryptionAndURLChanges(t *testing.T) {
	svc := NewService(t.TempDir(), func(context.Context) (Runtime, error) {
		return Runtime{BaseURL: "https://text.example/v1", APIKey: "text-fixture"}, nil
	}, nil)
	svc.media.protector = testMediaProtector{}
	in := MediaSettingsInput{Image: MediaEndpointInput{BaseURL: "https://image.example/v1/", APIKey: "image-fixture"}, Video: MediaEndpointInput{BaseURL: "https://video.example/v1", APIKey: "video-fixture"}, ImageConcurrency: 7, VideoConcurrency: 3}
	out, err := svc.UpdateMediaSettings(in)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Image.HasAPIKey || !out.Video.HasAPIKey || out.ImageConcurrency != 7 {
		t.Fatalf("settings: %+v", out)
	}
	raw, err := os.ReadFile(svc.media.path)
	if err != nil {
		t.Fatal(err)
	}
	public, _ := json.Marshal(out)
	for _, key := range []string{"image-fixture", "video-fixture"} {
		if strings.Contains(string(raw), key) || strings.Contains(string(public), key) {
			t.Fatal("plaintext key exposed")
		}
	}
	rt, imageClient, _, err := svc.clients(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	video := videoClient(rt, imageClient)
	if imageClient.BaseURL != "https://image.example/v1" || imageClient.APIKey != "image-fixture" || video.BaseURL != "https://video.example/v1" || video.APIKey != "video-fixture" || rt.VideoConcurrency != 3 {
		t.Fatal("media clients not independent")
	}
	in.Image.BaseURL = "https://other.example/v1"
	in.Image.APIKey = ""
	in.Video.APIKey = ""
	out, err = svc.UpdateMediaSettings(in)
	if err != nil {
		t.Fatal(err)
	}
	if out.Image.HasAPIKey || !out.Video.HasAPIKey {
		t.Fatal("URL change leaked key or unchanged URL lost key")
	}
	_, client, _, err := svc.clients(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if client.APIKey != "" {
		t.Fatal("writing key forwarded to custom endpoint")
	}
	_, err = svc.UpdateMediaSettings(MediaSettingsInput{})
	if err != nil {
		t.Fatal(err)
	}
	_, client, _, err = svc.clients(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if client.BaseURL != "https://text.example/v1" || client.APIKey != "text-fixture" {
		t.Fatal("default inheritance failed")
	}
}

func TestMediaSettingsRejectsMalformedURLAndConcurrency(t *testing.T) {
	svc := NewService(t.TempDir(), nil, nil)
	for _, url := range []string{"ftp://example.com", "https://user:secret@example.com/v1", "https://example.com/v1?key=secret", "https://example.com/v1/videos/generations"} {
		if _, err := svc.UpdateMediaSettings(MediaSettingsInput{Image: MediaEndpointInput{BaseURL: url}}); err == nil {
			t.Errorf("accepted %s", url)
		}
	}
	if _, err := svc.UpdateMediaSettings(MediaSettingsInput{VideoConcurrency: 65}); err == nil {
		t.Fatal("accepted invalid concurrency")
	}
}

func TestGenerationLimiterEnforcesConfiguredConcurrency(t *testing.T) {
	var limiter generationLimiter
	var active, max atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 15; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := limiter.acquire(context.Background(), 3)
			if err != nil {
				t.Error(err)
				return
			}
			n := active.Add(1)
			for old := max.Load(); n > old && !max.CompareAndSwap(old, n); old = max.Load() {
			}
			time.Sleep(5 * time.Millisecond)
			active.Add(-1)
			release()
		}()
	}
	wg.Wait()
	if max.Load() != 3 {
		t.Fatalf("peak=%d want3", max.Load())
	}
	release, err := limiter.acquire(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = limiter.acquire(ctx, 1); err == nil {
		t.Fatal("cancelled waiter acquired")
	}
	release()
}
