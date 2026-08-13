package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"

	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"

	"path/filepath"
	"strings"
	"testing"

	"video-production-console/internal/domain"
	"video-production-console/internal/imageproject"
	consoleSettings "video-production-console/internal/settings"
	"video-production-console/internal/store"
)

type imageRuntimeStub struct{ runtime consoleSettings.Runtime }

func (s imageRuntimeStub) Runtime(context.Context) (consoleSettings.Runtime, error) {
	return s.runtime, nil
}

type imageGeneratorStub struct {
	result imageproject.GenerateResult
	err    error
}

func (s imageGeneratorStub) Generate(context.Context, imageproject.GenerateRequest) (imageproject.GenerateResult, error) {
	return s.result, s.err
}

func testImagePNG(t *testing.T) string {
	t.Helper()
	value := image.NewRGBA(image.Rect(0, 0, 3, 4))
	value.Set(0, 0, color.White)
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, value); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(buffer.Bytes())
}

func TestImageProjectsHTTPCreateGeneratePreviewAndDownload(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	imagePayload := testImagePNG(t)
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/generations" {
			t.Fatalf("vendor path=%s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"` + imagePayload + `"}]}`))
	}))
	defer vendor.Close()
	runtime := imageRuntimeStub{runtime: consoleSettings.Runtime{PublicSettings: domain.PublicSettings{DataRoot: t.TempDir(), ImageBaseURL: vendor.URL + "/v1", ImageModel: "gpt-image-2", MaxImageConcurrency: 2}, ImageAPIKey: "secret"}}
	handler := NewImageProjectsHandler(db, runtime, imageproject.NewClient(vendor.Client()))

	create := httptest.NewRequest(http.MethodPost, "/api/image-projects", strings.NewReader(`{"title":"养老现金流","script":"第一句。第二句。","image_count":2,"ratio":"3:4","style":"ledger_investigation","concurrency":2}`))
	create.Header.Set("Content-Type", "application/json")
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, create)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var detail domain.ImageProjectDetail
	if err := json.Unmarshal(created.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.Items) != 2 || detail.Items[0].Sequence != 1 {
		t.Fatalf("detail=%+v", detail)
	}

	generate := httptest.NewRecorder()
	handler.ServeHTTP(generate, httptest.NewRequest(http.MethodPost, "/api/image-projects/"+detail.Project.ID+"/generate", nil))
	if generate.Code != http.StatusOK {
		t.Fatalf("generate status=%d body=%s", generate.Code, generate.Body.String())
	}
	if err := json.Unmarshal(generate.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Items[0].Status != "ready" || detail.Items[0].Width != 3 || detail.Items[0].Height != 4 {
		t.Fatalf("generated=%+v", detail.Items[0])
	}

	preview := httptest.NewRecorder()
	handler.ServeHTTP(preview, httptest.NewRequest(http.MethodGet, "/api/image-projects/"+detail.Project.ID+"/items/"+detail.Items[0].ID+"/image", nil))
	if preview.Code != http.StatusOK || preview.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("preview status=%d type=%s", preview.Code, preview.Header().Get("Content-Type"))
	}

	download := httptest.NewRecorder()
	handler.ServeHTTP(download, httptest.NewRequest(http.MethodGet, "/api/image-projects/"+detail.Project.ID+"/download", nil))
	if download.Code != http.StatusOK || !strings.Contains(download.Header().Get("Content-Disposition"), ".zip") {
		t.Fatalf("download status=%d headers=%v", download.Code, download.Header())
	}
	archive, err := zip.NewReader(bytes.NewReader(download.Body.Bytes()), int64(download.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(archive.File))
	for _, file := range archive.File {
		names = append(names, file.Name)
		reader, _ := file.Open()
		_, _ = io.Copy(io.Discard, reader)
		_ = reader.Close()
	}
	joined := strings.Join(names, "|")
	if !strings.Contains(joined, "001_") || !strings.Contains(joined, "002_") || !strings.Contains(joined, "manifest.json") {
		t.Fatalf("zip names=%v", names)
	}
	for _, file := range archive.File {
		if file.Name != "manifest.json" {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		manifest, err := io.ReadAll(reader)
		_ = reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		for _, required := range []string{`"ratio": "3:4"`, `"style": "ledger_investigation"`, `"source_text"`, `"prompt"`} {
			if !bytes.Contains(manifest, []byte(required)) {
				t.Fatalf("manifest missing %s: %s", required, manifest)
			}
		}
	}
}

func TestImageProjectsHTTPRejectsMissingConfiguration(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	handler := NewImageProjectsHandler(db, imageRuntimeStub{runtime: consoleSettings.Runtime{PublicSettings: domain.PublicSettings{DataRoot: t.TempDir(), ImageModel: "gpt-image-2", MaxImageConcurrency: 2}}}, imageproject.NewClient(nil))
	request := httptest.NewRequest(http.MethodPost, "/api/image-projects", strings.NewReader(`{"title":"x","script":"有效文案。","image_count":1,"ratio":"3:4","style":"finance_documentary","concurrency":1}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status=%d", response.Code)
	}
	var detail domain.ImageProjectDetail
	_ = json.Unmarshal(response.Body.Bytes(), &detail)
	generated := httptest.NewRecorder()
	handler.ServeHTTP(generated, httptest.NewRequest(http.MethodPost, "/api/image-projects/"+detail.Project.ID+"/generate", nil))
	if generated.Code != http.StatusConflict {
		t.Fatalf("generate status=%d body=%s", generated.Code, generated.Body.String())
	}
}

func TestImageProjectsHTTPCreatePreservesFinalScriptExactly(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	handler := NewImageProjectsHandler(db, nil, nil)
	script := "  第一句。\r\n\r\n第二句。  "
	body, _ := json.Marshal(map[string]any{"title": "原文", "script": script, "image_count": 2, "ratio": "3:4", "style": "finance_documentary", "concurrency": 1})
	request := httptest.NewRequest(http.MethodPost, "/api/image-projects", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", response.Code, response.Body.String())
	}
	var detail domain.ImageProjectDetail
	if err := json.Unmarshal(response.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Project.Script != script || strings.Join([]string{detail.Items[0].SourceText, detail.Items[1].SourceText}, "") != script {
		t.Fatalf("final script was rewritten: project=%q items=%q", detail.Project.Script, detail.Items[0].SourceText+detail.Items[1].SourceText)
	}
}

func TestImageProjectsHTTPRejectsEmptyCustomStyle(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	handler := NewImageProjectsHandler(db, nil, nil)
	request := httptest.NewRequest(http.MethodPost, "/api/image-projects", strings.NewReader(`{"title":"自定义","script":"有效文案。","image_count":1,"ratio":"3:4","style":"custom","custom_style":"  ","concurrency":1}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("empty custom style status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestImageProjectsHTTPSingleGenerationKeepsAggregateProjectStatus(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	imageBytes, err := base64.StdEncoding.DecodeString(testImagePNG(t))
	if err != nil {
		t.Fatal(err)
	}
	runtime := imageRuntimeStub{runtime: consoleSettings.Runtime{PublicSettings: domain.PublicSettings{DataRoot: t.TempDir(), ImageBaseURL: "https://api.example.com/v1", ImageModel: "gpt-image-2", MaxImageConcurrency: 2}, ImageAPIKey: "configured"}}
	handler := NewImageProjectsHandler(db, runtime, imageGeneratorStub{result: imageproject.GenerateResult{Bytes: imageBytes, MIMEType: "image/png", Width: 3, Height: 4}})
	request := httptest.NewRequest(http.MethodPost, "/api/image-projects", strings.NewReader(`{"title":"x","script":"第一句。第二句。","image_count":2,"ratio":"3:4","style":"finance_documentary","concurrency":1}`))
	request.Header.Set("Content-Type", "application/json")
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, request)
	var detail domain.ImageProjectDetail
	_ = json.Unmarshal(created.Body.Bytes(), &detail)
	generated := httptest.NewRecorder()
	handler.ServeHTTP(generated, httptest.NewRequest(http.MethodPost, "/api/image-projects/"+detail.Project.ID+"/items/"+detail.Items[0].ID+"/generate", nil))
	if generated.Code != http.StatusOK {
		t.Fatalf("generate status=%d body=%s", generated.Code, generated.Body.String())
	}
	if err := json.Unmarshal(generated.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Project.Status != "partial" || detail.Items[0].Status != "ready" || detail.Items[1].Status != "pending" {
		t.Fatalf("detail=%+v", detail)
	}
}
