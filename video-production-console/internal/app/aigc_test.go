package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"video-production-console/internal/config"
	"video-production-console/internal/store"
)

func TestAIGCRoutesMountedAndInterruptedJobsResumed(t *testing.T) {
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
	jobs := store.NewAIGCJobRepository(db)
	job, err := jobs.CreateJob(context.Background(), store.AIGCJob{MerchantID: merchant.ID, Kind: "restyle", Status: "running"})
	if err != nil {
		t.Fatal(err)
	}
	application := New(Options{DB: db, Config: config.Config{DataRoot: root}})
	defer application.Close()

	got, err := jobs.GetJob(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "failed" || got.Error == "" {
		t.Fatalf("resumed job = %+v", got)
	}

	recorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/aigc/jobs/"+uuid.NewString(), nil))
	if recorder.Code != http.StatusNotFound || !strings.Contains(recorder.Body.String(), "aigc_job_not_found") {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestAIGCRestyleWithoutSettingsReturnsConfigurationConflict(t *testing.T) {
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
	material, err := repo.CreateMaterial(context.Background(), store.MerchantMaterial{MerchantID: merchant.ID, Kind: "image", Filename: "source.png", Path: materialPath})
	if err != nil {
		t.Fatal(err)
	}
	application := New(Options{DB: db, Config: config.Config{DataRoot: root}})
	defer application.Close()

	body := `{"merchant_id":"` + merchant.ID + `","material_ids":["` + material.ID + `"],"prompt":"夜景"}`
	recorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/aigc/restyle", strings.NewReader(body)))
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "image_api_not_configured") {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}
