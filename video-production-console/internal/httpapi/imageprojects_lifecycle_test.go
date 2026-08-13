package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"video-production-console/internal/domain"
	"video-production-console/internal/imageproject"
	consoleSettings "video-production-console/internal/settings"
	"video-production-console/internal/store"
)

func TestImageProjectsHTTPRegenerationKeepsOldImageOnFailureAndRemovesItOnSuccess(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	imageBytes, err := base64.StdEncoding.DecodeString(testImagePNG(t))
	if err != nil {
		t.Fatal(err)
	}
	runtime := imageRuntimeStub{runtime: consoleSettings.Runtime{PublicSettings: domain.PublicSettings{DataRoot: t.TempDir(), ImageBaseURL: "https://api.example.com/v1", ImageModel: "gpt-image-2", MaxImageConcurrency: 2}, ImageAPIKey: "secret"}}
	success := imageGeneratorStub{result: imageproject.GenerateResult{Bytes: imageBytes, MIMEType: "image/png", Width: 3, Height: 4}}
	handler := NewImageProjectsHandler(db, runtime, success)
	request := httptest.NewRequest(http.MethodPost, "/api/image-projects", strings.NewReader(`{"title":"x","script":"第一句。","image_count":1,"ratio":"3:4","style":"finance_documentary","concurrency":1}`))
	request.Header.Set("Content-Type", "application/json")
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, request)
	var detail domain.ImageProjectDetail
	if err := json.Unmarshal(created.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	itemURL := "/api/image-projects/" + detail.Project.ID + "/items/" + detail.Items[0].ID + "/generate"
	generated := httptest.NewRecorder()
	handler.ServeHTTP(generated, httptest.NewRequest(http.MethodPost, itemURL, nil))
	if generated.Code != http.StatusOK {
		t.Fatalf("initial generation status=%d body=%s", generated.Code, generated.Body.String())
	}
	var oldPath string
	if err := db.QueryRow(`SELECT image_path FROM image_project_items WHERE id=?`, detail.Items[0].ID).Scan(&oldPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("old image missing after initial generation: %v", err)
	}

	failing := NewImageProjectsHandler(db, runtime, imageGeneratorStub{err: errors.New("vendor unavailable")})
	failed := httptest.NewRecorder()
	failing.ServeHTTP(failed, httptest.NewRequest(http.MethodPost, itemURL, nil))
	if failed.Code != http.StatusOK {
		t.Fatalf("failed regeneration status=%d body=%s", failed.Code, failed.Body.String())
	}
	if err := json.Unmarshal(failed.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Items[0].Status != "ready" || detail.Items[0].ErrorMessage == nil {
		t.Fatalf("failed regeneration did not preserve ready image: %+v", detail.Items[0])
	}
	var preservedPath string
	if err := db.QueryRow(`SELECT image_path FROM image_project_items WHERE id=?`, detail.Items[0].ID).Scan(&preservedPath); err != nil {
		t.Fatal(err)
	}
	if preservedPath != oldPath {
		t.Fatalf("path changed after failed regeneration: old=%q new=%q", oldPath, preservedPath)
	}
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("old image removed after failed regeneration: %v", err)
	}

	updateBody := strings.NewReader(`{"source_text":"第一句。","title":"新名称","prompt":"新提示词"}`)
	updateRequest := httptest.NewRequest(http.MethodPatch, "/api/image-projects/"+detail.Project.ID+"/items/"+detail.Items[0].ID, updateBody)
	updateRequest.Header.Set("Content-Type", "application/json")
	updated := httptest.NewRecorder()
	handler.ServeHTTP(updated, updateRequest)
	if updated.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", updated.Code, updated.Body.String())
	}
	regenerated := httptest.NewRecorder()
	handler.ServeHTTP(regenerated, httptest.NewRequest(http.MethodPost, itemURL, nil))
	if regenerated.Code != http.StatusOK {
		t.Fatalf("successful regeneration status=%d body=%s", regenerated.Code, regenerated.Body.String())
	}
	var newPath string
	if err := db.QueryRow(`SELECT image_path FROM image_project_items WHERE id=?`, detail.Items[0].ID).Scan(&newPath); err != nil {
		t.Fatal(err)
	}
	if newPath == oldPath {
		t.Fatalf("successful regeneration reused old path %q", oldPath)
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("new image missing: %v", err)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("superseded image still exists: err=%v", err)
	}
}

func TestImageProjectsHTTPEditedReadyItemBecomesPendingAndFailureDoesNotServeOldImage(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	imageBytes, err := base64.StdEncoding.DecodeString(testImagePNG(t))
	if err != nil {
		t.Fatal(err)
	}
	runtime := imageRuntimeStub{runtime: consoleSettings.Runtime{PublicSettings: domain.PublicSettings{DataRoot: t.TempDir(), ImageBaseURL: "https://api.example.com/v1", ImageModel: "gpt-image-2", MaxImageConcurrency: 2}, ImageAPIKey: "secret"}}
	handler := NewImageProjectsHandler(db, runtime, imageGeneratorStub{result: imageproject.GenerateResult{Bytes: imageBytes, MIMEType: "image/png", Width: 3, Height: 4}})
	createRequest := httptest.NewRequest(http.MethodPost, "/api/image-projects", strings.NewReader(`{"title":"x","script":"  第一句。\n","image_count":1,"ratio":"3:4","style":"finance_documentary","concurrency":1}`))
	createRequest.Header.Set("Content-Type", "application/json")
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, createRequest)
	var detail domain.ImageProjectDetail
	if err := json.Unmarshal(created.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	itemURL := "/api/image-projects/" + detail.Project.ID + "/items/" + detail.Items[0].ID
	generated := httptest.NewRecorder()
	handler.ServeHTTP(generated, httptest.NewRequest(http.MethodPost, itemURL+"/generate", nil))
	updateRequest := httptest.NewRequest(http.MethodPatch, itemURL, strings.NewReader(`{"source_text":"  新原文。\n","title":"新名称","prompt":"新提示词"}`))
	updateRequest.Header.Set("Content-Type", "application/json")
	updated := httptest.NewRecorder()
	handler.ServeHTTP(updated, updateRequest)
	if updated.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", updated.Code, updated.Body.String())
	}
	if err := json.Unmarshal(updated.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Project.Status != "draft" || detail.Items[0].Status != "pending" || detail.Items[0].SourceText != "  新原文。\n" {
		t.Fatalf("edited ready item not invalidated without rewriting: %+v", detail)
	}
	preview := httptest.NewRecorder()
	handler.ServeHTTP(preview, httptest.NewRequest(http.MethodGet, itemURL+"/image", nil))
	if preview.Code != http.StatusNotFound {
		t.Fatalf("stale edited image preview status=%d", preview.Code)
	}
	failing := NewImageProjectsHandler(db, runtime, imageGeneratorStub{err: errors.New("vendor unavailable")})
	failed := httptest.NewRecorder()
	failing.ServeHTTP(failed, httptest.NewRequest(http.MethodPost, itemURL+"/generate", nil))
	if err := json.Unmarshal(failed.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Items[0].Status != "pending" || detail.Items[0].ErrorMessage == nil {
		t.Fatalf("edited regeneration failure exposed old result: %+v", detail.Items[0])
	}
}
