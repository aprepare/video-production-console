package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"
	"video-production-console/internal/aishorts"
)

func TestAIShortMediaSettingsRoundTrip(t *testing.T) {
	h := &aiShortsHandler{svc: aishorts.NewService(t.TempDir(), nil, nil)}
	w := httptest.NewRecorder()
	h.saveMediaSettings(w, httptest.NewRequest("PUT", "/api/ai-shorts/media-settings", strings.NewReader(`{"image":{"base_url":"https://images.example/v1"},"video":{"base_url":"https://videos.example/v1"},"image_concurrency":8,"video_concurrency":4}`)))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	h.mediaSettings(w, httptest.NewRequest("GET", "/api/ai-shorts/media-settings", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"video_concurrency":4`) || !strings.Contains(w.Body.String(), "https://images.example/v1") {
		t.Fatal(w.Code, w.Body.String())
	}
}
