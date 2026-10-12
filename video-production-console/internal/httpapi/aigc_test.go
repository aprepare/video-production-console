package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"video-production-console/internal/aigc"
	consoleSettings "video-production-console/internal/settings"
	"video-production-console/internal/store"
)

type aigcRuntimeStub struct {
	runtime consoleSettings.Runtime
}

func (s aigcRuntimeStub) Runtime(context.Context) (consoleSettings.Runtime, error) {
	return s.runtime, nil
}

func TestAIGCHandlerRejectsMalformedUUIDs(t *testing.T) {
	handler := NewAIGCHandler(nil)
	for _, tc := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/api/aigc/extract-frames", `{"merchant_id":"bad","material_id":"also-bad","count":2}`},
		{http.MethodPost, "/api/aigc/restyle", `{"merchant_id":"bad","material_ids":["also-bad"],"prompt":"夜景"}`},
		{http.MethodPost, "/api/aigc/image-to-video", `{"merchant_id":"bad","material_ids":["also-bad"],"prompt":"运镜","duration_s":5,"aspect":"9:16"}`},
		{http.MethodPost, "/api/aigc/jobs/not-a-uuid/confirm", ""},
		{http.MethodGet, "/api/aigc/jobs/not-a-uuid", ""},
		{http.MethodGet, "/api/aigc/jobs?merchant_id=not-a-uuid", ""},
		{http.MethodGet, "/api/aigc/lineage/not-a-uuid", ""},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			if tc.body != "" {
				request.Header.Set("Content-Type", "application/json")
			}
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
			}
			if body := recorder.Body.String(); !strings.Contains(body, `"code":"invalid_id"`) || !strings.Contains(body, `"message":"`) {
				t.Fatalf("body = %s", body)
			}
		})
	}
}

func TestAIGCHandlerRejectsUnknownJSONFields(t *testing.T) {
	handler := NewAIGCHandler(nil)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/aigc/extract-frames", strings.NewReader(`{"merchant_id":"x","material_id":"y","count":1,"typo":true}`))
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestAIGCHandlerMapsMissingImageConfigurationToConflict(t *testing.T) {
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := store.NewMerchantRepository(db)
	merchant, err := repo.Create(context.Background(), store.Merchant{Name: "测试商户"})
	if err != nil {
		t.Fatal(err)
	}
	materialDir := filepath.Join(root, "merchants", merchant.ID, "materials")
	if err := os.MkdirAll(materialDir, 0o755); err != nil {
		t.Fatal(err)
	}
	materialPath := filepath.Join(materialDir, "source.png")
	if err := os.WriteFile(materialPath, []byte("fake image"), 0o644); err != nil {
		t.Fatal(err)
	}
	material, err := repo.CreateMaterial(context.Background(), store.MerchantMaterial{
		MerchantID: merchant.ID,
		Kind:       "image",
		Filename:   "source.png",
		Path:       materialPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewAIGCHandler(aigc.New(db, root, aigcRuntimeStub{}))
	body := `{"merchant_id":"` + merchant.ID + `","material_ids":["` + material.ID + `"],"prompt":"夜景"}`
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/aigc/restyle", strings.NewReader(body)))
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), `"code":"image_api_not_configured"`) {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestAIGCHandlerReadsJobsAndLineage(t *testing.T) {
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := store.NewMerchantRepository(db)
	merchant, err := repo.Create(context.Background(), store.Merchant{Name: "测试商户"})
	if err != nil {
		t.Fatal(err)
	}
	material, err := repo.CreateMaterial(context.Background(), store.MerchantMaterial{MerchantID: merchant.ID, Kind: "image", Filename: "source.png", Path: "source.png"})
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.NewAIGCJobRepository(db).CreateJob(context.Background(), store.AIGCJob{MerchantID: merchant.ID, Kind: "restyle", Status: "completed"})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewAIGCHandler(aigc.New(db, root, aigcRuntimeStub{}))

	for _, tc := range []struct {
		path       string
		wantID     string
		wantLength int
	}{
		{"/api/aigc/jobs/" + strings.ToUpper(job.ID), job.ID, 0},
		{"/api/aigc/jobs?merchant_id=" + strings.ToUpper(merchant.ID), job.ID, 1},
		{"/api/aigc/lineage/" + strings.ToUpper(material.ID), material.ID, 1},
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s: status = %d, body = %s", tc.path, recorder.Code, recorder.Body.String())
		}
		var decoded any
		if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(recorder.Body.String(), tc.wantID) {
			t.Fatalf("GET %s body = %s", tc.path, recorder.Body.String())
		}
		if tc.wantLength > 0 {
			items, ok := decoded.([]any)
			if !ok || len(items) != tc.wantLength {
				t.Fatalf("GET %s decoded = %#v", tc.path, decoded)
			}
		}
	}
}

func TestAIGCHandlerRejectsConfirmForCompletedJob(t *testing.T) {
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	merchant, err := store.NewMerchantRepository(db).Create(context.Background(), store.Merchant{Name: "测试商户"})
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.NewAIGCJobRepository(db).CreateJob(context.Background(), store.AIGCJob{MerchantID: merchant.ID, Kind: "image_to_video", Status: "completed"})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewAIGCHandler(aigc.New(db, root, aigcRuntimeStub{}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/aigc/jobs/"+job.ID+"/confirm", nil))
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), `"code":"aigc_job_conflict"`) {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}
