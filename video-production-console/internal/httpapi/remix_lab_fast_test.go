package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"video-production-console/internal/remixlab"
)

func TestFastPresetAPIRoundTrip(t *testing.T) {
	handler := newRemixLabTestHandler(t)
	for _, tier := range []string{"priority", "default", "turbo"} {
		res := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/remix-lab/presets", strings.NewReader(`{"slots":[{"model":"chosen","reasoning_effort":"high","service_tier":"`+tier+`"}]}`))
		handler.ServeHTTP(res, req)
		if tier == "turbo" {
			if res.Code != http.StatusBadRequest {
				t.Fatalf("invalid tier status=%d", res.Code)
			}
			continue
		}
		if res.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
		}
		get := httptest.NewRecorder()
		handler.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/remix-lab/defaults", nil))
		var got remixlab.DefaultsView
		if err := json.Unmarshal(get.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Presets) != 1 || got.Presets[0].ServiceTier != tier || got.Presets[0].ReasoningEffort != "high" || got.Presets[0].Model != "chosen" {
			t.Fatalf("defaults=%+v", got)
		}
	}
}
