package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"video-production-console/internal/aishorts"
)

func TestAIShortImageModelCreatePatchAndDefaults(t *testing.T) {
	h := &aiShortsHandler{svc: aishorts.NewService(t.TempDir(), nil, nil), runtime: func(context.Context) (aishorts.Runtime, error) {
		return aishorts.Runtime{Models: aishorts.Models{Image: "relay-image-default", Text: "text-default"}}, nil
	}}
	call := func(method, body, id string, handler http.HandlerFunc) map[string]any {
		t.Helper()
		r := httptest.NewRequest(method, "/api/ai-shorts", strings.NewReader(body))
		r.SetPathValue("id", id)
		w := httptest.NewRecorder()
		handler(w, r)
		if w.Code < 200 || w.Code >= 300 {
			t.Fatalf("%s: %d %s", method, w.Code, w.Body.String())
		}
		var got map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	got := call("POST", `{"mode":"explainer","story":"一家人辛苦攒下来的钱，存款到期之后先把条件问清楚。","image_model":"  custom-image-v1  "}`, "", h.create)
	id := got["id"].(string)
	if got["image_model"] != "custom-image-v1" {
		t.Fatalf("model not saved: %v", got["image_model"])
	}
	got = call("PATCH", `{"headline":"新标题"}`, id, h.update)
	if got["image_model"] != "custom-image-v1" {
		t.Fatal("omitted model changed saved selection")
	}
	got = call("PATCH", `{"image_model":"custom-image-v2"}`, id, h.update)
	if got["image_model"] != "custom-image-v2" {
		t.Fatal("model update ignored")
	}
	got = call("GET", "", id, h.get)
	if got["image_model"] != "custom-image-v2" {
		t.Fatal("model not persisted")
	}
	got = call("PATCH", `{"image_model":""}`, id, h.update)
	if value, exists := got["image_model"]; exists && value != "" {
		t.Fatal("empty model did not restore default")
	}
	meta := call("GET", "", "", h.styles)
	if meta["default_image_model"] != "relay-image-default" {
		t.Fatalf("default not exposed: %v", meta)
	}
}
