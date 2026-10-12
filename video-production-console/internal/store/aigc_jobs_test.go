package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestAIGCJobRepositoryCRUDRunningAndClaim(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aigc-jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	merchantRepo := NewMerchantRepository(db)
	merchant, err := merchantRepo.Create(context.Background(), Merchant{Name: "测试商户"})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	repo := NewAIGCJobRepository(db)
	createdAt := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	job, err := repo.CreateJob(ctx, AIGCJob{
		MerchantID:       merchant.ID,
		OrderID:          "order-1",
		Kind:             "restyle",
		Status:           "queued",
		InputMaterialIDs: []string{"input-1", "input-2"},
		ParamsJSON:       `{"prompt":"warm"}`,
		CreatedAt:        createdAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if job.ID == "" || !job.CreatedAt.Equal(createdAt) || !job.UpdatedAt.Equal(createdAt) {
		t.Fatalf("created job metadata = %+v", job)
	}
	got, err := repo.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != "restyle" || got.Status != "queued" || len(got.InputMaterialIDs) != 2 || got.ParamsJSON != `{"prompt":"warm"}` {
		t.Fatalf("round-tripped job = %+v", got)
	}

	second, err := repo.CreateJob(ctx, AIGCJob{MerchantID: merchant.ID, Kind: "image_to_video", Status: "awaiting_confirm"})
	if err != nil {
		t.Fatal(err)
	}
	active, err := repo.CreateJob(ctx, AIGCJob{MerchantID: merchant.ID, Kind: "restyle", Status: "running", CreatedAt: createdAt.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	completed, err := repo.CreateJob(ctx, AIGCJob{MerchantID: merchant.ID, Kind: "extract_frames", Status: "completed"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := repo.ListJobs(ctx, merchant.ID)
	if err != nil || len(list) != 4 {
		t.Fatalf("ListJobs() = %d, %v", len(list), err)
	}
	running, err := repo.Running(ctx)
	if err != nil || len(running) != 2 || running[0].ID != job.ID || running[1].ID != active.ID {
		t.Fatalf("Running() = %+v, %v", running, err)
	}
	if err := repo.SetJobOutputs(ctx, job.ID, []string{"output-1", "output-2"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateJobStatus(ctx, job.ID, "failed", "provider failed"); err != nil {
		t.Fatal(err)
	}
	got, err = repo.GetJob(ctx, job.ID)
	if err != nil || got.Status != "failed" || got.Error != "provider failed" || len(got.OutputMaterialIDs) != 2 {
		t.Fatalf("updated job = %+v, %v", got, err)
	}
	if err := repo.ClaimJob(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	claimed, err := repo.GetJob(ctx, second.ID)
	if err != nil || claimed.Status != "running" {
		t.Fatalf("claimed job = %+v, %v", claimed, err)
	}
	if err := repo.ClaimJob(ctx, second.ID); !errors.Is(err, ErrAIGCJobConflict) {
		t.Fatalf("second ClaimJob error = %v", err)
	}
	if err := repo.ClaimJob(ctx, "missing"); !errors.Is(err, ErrAIGCJobNotFound) {
		t.Fatalf("missing ClaimJob error = %v", err)
	}
	if _, err := repo.GetJob(ctx, "missing"); !errors.Is(err, ErrAIGCJobNotFound) {
		t.Fatalf("missing GetJob error = %v", err)
	}
	if err := repo.UpdateJobStatus(ctx, "missing", "failed", "x"); !errors.Is(err, ErrAIGCJobNotFound) {
		t.Fatalf("missing UpdateJobStatus error = %v", err)
	}
	if err := repo.SetJobOutputs(ctx, completed.ID, nil); err != nil {
		t.Fatal(err)
	}
}

func TestMerchantRepositoryCreateDerivedMaterialInheritsLicenseAndRoot(t *testing.T) {
	repo, merchant, _ := merchantFixture(t)
	ctx := context.Background()
	root, err := repo.CreateMaterial(ctx, MerchantMaterial{
		MerchantID: merchant.ID,
		Kind:       "video", Filename: "root.mp4", Path: "materials/root.mp4",
		LicenseSource: "merchant_shot", LicenseScope: "全平台一年",
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := repo.CreateDerivedMaterial(ctx, MerchantMaterial{
		MerchantID: merchant.ID, Kind: "image", Filename: "frame.jpg", Path: "materials/frame.jpg",
	}, root.ID, "extract_frames", map[string]any{"frame": 1}, "ffmpeg")
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.CreateDerivedMaterial(ctx, MerchantMaterial{
		MerchantID: merchant.ID, Kind: "image", Filename: "restyled.png", Path: "materials/restyled.png",
	}, first.ID, "restyle", map[string]any{"prompt": "warm"}, "image-model")
	if err != nil {
		t.Fatal(err)
	}
	if second.ParentMaterialID != first.ID || second.LicenseSource != "ai_generated" || second.LicenseScope != root.LicenseScope {
		t.Fatalf("derived metadata = %+v", second)
	}
	var provenance struct {
		Step             string         `json:"step"`
		SourceMaterialID string         `json:"source_material_id"`
		RootMaterialID   string         `json:"root_material_id"`
		Params           map[string]any `json:"params"`
		Model            string         `json:"model"`
		CreatedAt        string         `json:"created_at"`
	}
	if err := json.Unmarshal([]byte(second.ProvenanceJSON), &provenance); err != nil {
		t.Fatal(err)
	}
	if provenance.Step != "restyle" || provenance.SourceMaterialID != first.ID || provenance.RootMaterialID != root.ID || provenance.Model != "image-model" || provenance.Params["prompt"] != "warm" || provenance.CreatedAt == "" {
		t.Fatalf("provenance = %+v", provenance)
	}

	other, err := repo.Create(ctx, Merchant{Name: "其他商户"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.CreateDerivedMaterial(ctx, MerchantMaterial{
		MerchantID: other.ID, Kind: "image", Filename: "bad.png", Path: "materials/bad.png",
	}, first.ID, "restyle", nil, "image-model")
	if !errors.Is(err, ErrInvalidMerchantData) {
		t.Fatalf("cross-merchant CreateDerivedMaterial error = %v", err)
	}
	_, err = repo.CreateDerivedMaterial(ctx, MerchantMaterial{
		MerchantID: merchant.ID, Kind: "image", Filename: "bad-step.png", Path: "materials/bad-step.png",
	}, first.ID, "unknown", nil, "image-model")
	if !errors.Is(err, ErrInvalidMerchantData) {
		t.Fatalf("invalid-step CreateDerivedMaterial error = %v", err)
	}
}
