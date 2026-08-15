package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"

	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/domain"
	"video-production-console/internal/imageproject"
	consoleSettings "video-production-console/internal/settings"
	"video-production-console/internal/store"
)

type imageRuntimeStub struct{ runtime consoleSettings.Runtime }

func (s imageRuntimeStub) Runtime(context.Context) (consoleSettings.Runtime, error) {
	if strings.TrimSpace(s.runtime.ImageTextBaseURL) == "" {
		s.runtime.ImageTextBaseURL = "https://text.example.test/v1"
	}
	if strings.TrimSpace(s.runtime.ImageTextModel) == "" {
		s.runtime.ImageTextModel = "planner-test"
	}
	if strings.TrimSpace(s.runtime.ImageTextAPIKey) == "" {
		s.runtime.ImageTextAPIKey = "test-only-key"
	}
	return s.runtime, nil
}

type imageGeneratorStub struct {
	result   imageproject.GenerateResult
	err      error
	requests []imageproject.GenerateRequest
}

func (s imageGeneratorStub) Generate(context.Context, imageproject.GenerateRequest) (imageproject.GenerateResult, error) {
	return s.result, s.err
}

type capturingImageGenerator struct {
	requests []imageproject.GenerateRequest
}

func (g *capturingImageGenerator) Generate(_ context.Context, req imageproject.GenerateRequest) (imageproject.GenerateResult, error) {
	g.requests = append(g.requests, req)
	return imageproject.GenerateResult{Bytes: []byte("bad")}, errors.New("capture")
}

func TestImageProjectsGeneratePropagatesImageStream(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	gen := &capturingImageGenerator{}
	runtime := imageRuntimeStub{runtime: consoleSettings.Runtime{PublicSettings: domain.PublicSettings{DataRoot: t.TempDir(), ImageBaseURL: "https://img.test/v1", ImageModel: "m", ImageStream: true, MaxImageConcurrency: 1}, ImageAPIKey: "k"}}
	h := testImageHandler(db, runtime, gen)
	create := httptest.NewRequest(http.MethodPost, "/api/image-projects", bytes.NewReader(imageCreatePayload("t", "s", map[string]any{"image_count": 1})))
	create.Header.Set("Content-Type", "application/json")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, create)
	if r.Code != http.StatusCreated {
		t.Fatalf("create=%d", r.Code)
	}
	var d domain.ImageProjectDetail
	_ = json.Unmarshal(r.Body.Bytes(), &d)
	if d.Project.ImageAttempts != 2 {
		t.Fatalf("default image attempts=%d", d.Project.ImageAttempts)
	}
	r = httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodPost, "/api/image-projects/"+d.Project.ID+"/generate", nil))
	if len(gen.requests) != 1 || !gen.requests[0].Stream {
		t.Fatalf("requests=%+v", gen.requests)
	}
}

func TestImageProjectsPublishingGeneratePersistsFiveCandidates(t *testing.T) {
	db, _ := store.Open(filepath.Join(t.TempDir(), "p.db"))
	defer db.Close()
	now := time.Now().UTC()
	p := domain.ImageProject{ID: uuid.NewString(), Title: "t", Script: "hello", ImageCount: 1, Ratio: "3:4", Style: "red_ink", Concurrency: 1, Status: "draft", CreatedAt: now, UpdatedAt: now}
	repo := store.NewImageProjectRepository(db)
	item := domain.ImageProjectItem{ID: uuid.NewString(), ProjectID: p.ID, Sequence: 1, SourceText: "hello", Title: "h", Prompt: "p", Status: "pending", CreatedAt: now, UpdatedAt: now}
	if err := repo.Create(context.Background(), p, []domain.ImageProjectItem{item}); err != nil {
		t.Fatal(err)
	}
	h := testImageHandler(db, imageRuntimeStub{}, nil)
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodPost, "/api/image-projects/"+p.ID+"/publishing-candidates/generate", nil))
	if r.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", r.Code, r.Body.String())
	}
}

type imagePlannerStub struct {
	last imageproject.ChatRequest
}

func (s *imagePlannerStub) Complete(_ context.Context, input imageproject.ChatRequest) (string, error) {
	s.last = input
	if strings.HasPrefix(input.ResponseSchemaName, "image_segments") {
		script := input.User
		if idx := strings.LastIndex(script, "原文：\n"); idx >= 0 {
			script = script[idx+len("原文：\n"):]
		}
		if idx := strings.LastIndex(script, "\n"); idx >= 0 {
			var decoded string
			if json.Unmarshal([]byte(script[idx+1:]), &decoded) == nil {
				script = decoded
			}
		}
		if input.ResponseSchemaName == "image_segments_and_publishing" {
			segments := testSegmentsForScript(script)
			pubs := []map[string]any{{"position": 1, "title": "标题一", "description": "描述一 #存款 #财富管理 #思维提升"}, {"position": 2, "title": "标题二", "description": "描述二 #存款 #理财 #认知"}, {"position": 3, "title": "标题三", "description": "描述三 #存款 #财富 #干货"}, {"position": 4, "title": "标题四", "description": "描述四 #复利 #理财 #思维"}, {"position": 5, "title": "标题五", "description": "描述五 #风险 #存款 #认知"}}
			out, _ := json.Marshal(map[string]any{"segments": segments, "publishing_candidates": pubs})
			return string(out), nil
		}
		return defaultTestSegmentJSON(script), nil
	}
	raw := input.User
	if idx := strings.LastIndex(raw, "卡片：\n"); idx >= 0 {
		raw = raw[idx+len("卡片：\n"):]
	}
	var segments []imageproject.Segment
	if err := json.Unmarshal([]byte(raw), &segments); err != nil || len(segments) == 0 {
		segments = make([]imageproject.Segment, 0)
		for _, s := range testSegmentsForScript(input.User) {
			b, _ := json.Marshal(s)
			var seg imageproject.Segment
			_ = json.Unmarshal(b, &seg)
			segments = append(segments, seg)
		}
		pubs := []map[string]any{{"position": 1, "title": "标题一", "description": "描述一 #存款 #财富管理 #思维提升"}, {"position": 2, "title": "标题二", "description": "描述二 #存款 #理财 #认知"}, {"position": 3, "title": "标题三", "description": "描述三 #存款 #财富 #干货"}, {"position": 4, "title": "标题四", "description": "描述四 #复利 #理财 #思维"}, {"position": 5, "title": "标题五", "description": "描述五 #风险 #存款 #认知"}}
		out, _ := json.Marshal(map[string]any{"segments": segments, "publishing_candidates": pubs})
		return string(out), nil
	}
	prompts := make([]map[string]any, 0, len(segments))
	for _, segment := range segments {
		prompt := "内容图提示词"
		if segment.Role == imageproject.RoleCover || segment.Sequence == 1 {
			prompt = "封面图，强冲突"
		}
		prompts = append(prompts, map[string]any{"sequence": segment.Sequence, "prompt": prompt})
	}
	encoded, _ := json.Marshal(map[string]any{"prompts": prompts})
	return string(encoded), nil
}

func defaultTestSegmentJSON(script string) string {
	segments := testSegmentsForScript(script)
	encoded, _ := json.Marshal(map[string]any{"segments": segments})
	return string(encoded)
}

func testSegmentsForScript(script string) []map[string]any {
	if first, second, ok := strings.Cut(script, "第二句。"); ok && strings.Contains(first, "第一句。") {
		return []map[string]any{
			{"sequence": 1, "role": "cover", "title": "第一句", "source_text": first, "rationale": "封面"},
			{"sequence": 2, "role": "content", "title": "第二句", "source_text": "第二句。" + second, "rationale": "正文"},
		}
	}
	return []map[string]any{{"sequence": 1, "role": "cover", "title": "封面", "source_text": script, "rationale": "封面"}}
}

func imageCreatePayload(title, script string, extra map[string]any) []byte {
	body := map[string]any{
		"title": title, "script": script, "image_count": 0, "ratio": "3:4",
		"style": "finance_documentary", "concurrency": 1, "segments": testSegmentsForScript(script),
	}
	for key, value := range extra {
		body[key] = value
	}
	encoded, _ := json.Marshal(body)
	return encoded
}

func testImageHandler(db *sql.DB, runtime imageRuntimeProvider, generator imageproject.Generator) http.Handler {
	return NewImageProjectsHandlerWithPlanner(db, runtime, generator, &imagePlannerStub{})
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
	handler := testImageHandler(db, runtime, imageproject.NewClient(vendor.Client()))

	create := httptest.NewRequest(http.MethodPost, "/api/image-projects", bytes.NewReader(imageCreatePayload("养老现金流", "第一句。第二句。", map[string]any{"image_count": 2, "style": "ledger_investigation", "concurrency": 2})))
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
	handler := testImageHandler(db, imageRuntimeStub{runtime: consoleSettings.Runtime{PublicSettings: domain.PublicSettings{DataRoot: t.TempDir(), ImageModel: "gpt-image-2", MaxImageConcurrency: 2}}}, imageproject.NewClient(nil))
	request := httptest.NewRequest(http.MethodPost, "/api/image-projects", bytes.NewReader(imageCreatePayload("x", "有效文案。", map[string]any{"image_count": 1})))
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
	handler := testImageHandler(db, imageRuntimeStub{runtime: consoleSettings.Runtime{PublicSettings: domain.PublicSettings{DataRoot: t.TempDir(), GrokBaseURL: "http://127.0.0.1:3030", GrokModel: "grok-test", ImageModel: "gpt-image-2", MaxImageConcurrency: 1}, GrokAPIKey: "configured"}}, nil)
	script := "  第一句。\r\n\r\n第二句。  "
	body := imageCreatePayload("原文", script, map[string]any{"image_count": 2})
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

func TestImageProjectsHTTPCreateAcceptsSelectedPublishingPosition(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	handler := testImageHandler(db, imageRuntimeStub{}, nil)
	candidates := []domain.PublishingCandidate{
		{Position: 1, Title: "标题一", Description: "描述一"},
		{Position: 2, Title: "标题二", Description: "描述二"},
		{Position: 3, Title: "标题三", Description: "描述三"},
		{Position: 4, Title: "标题四", Description: "描述四"},
		{Position: 5, Title: "标题五", Description: "描述五"},
	}
	body := imageCreatePayload("原文", "第一句。第二句。", map[string]any{
		"publishing_candidates": candidates,
		"selected_position":     1,
	})
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
	if detail.Project.SelectedPosition == nil || *detail.Project.SelectedPosition != 1 {
		t.Fatalf("selected position=%v", detail.Project.SelectedPosition)
	}
}

func TestImageProjectsHTTPSegmentPreviewUsesReasoningEffort(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	planner := &imagePlannerStub{}
	runtime := imageRuntimeStub{runtime: consoleSettings.Runtime{PublicSettings: domain.PublicSettings{
		DataRoot: t.TempDir(), ImageModel: "gpt-image-2", MaxImageConcurrency: 1, ImageTextReasoningEffort: "medium",
	}}}
	handler := NewImageProjectsHandlerWithPlanner(db, runtime, nil, planner)

	request := httptest.NewRequest(http.MethodPost, "/api/image-projects/segment-preview", bytes.NewReader(imageCreatePayload("养老", "第一句。第二句。", map[string]any{"reasoning_effort": "high"})))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", response.Code, response.Body.String())
	}
	if planner.last.ReasoningEffort != "high" {
		t.Fatalf("request effort not forwarded: %+v", planner.last)
	}
	if planner.last.System == "" || !strings.Contains(planner.last.User, "原文数据") {
		t.Fatalf("segment preview bypassed planner instructions: system=%q user=%q", planner.last.System, planner.last.User)
	}
	if planner.last.ResponseSchemaName != "image_segments_and_publishing" || planner.last.ResponseSchema == nil {
		t.Fatalf("segment preview omitted structured response schema: name=%q schema=%#v", planner.last.ResponseSchemaName, planner.last.ResponseSchema)
	}

	fallback := httptest.NewRequest(http.MethodPost, "/api/image-projects/segment-preview", bytes.NewReader(imageCreatePayload("养老", "第一句。第二句。", nil)))
	fallback.Header.Set("Content-Type", "application/json")
	fallbackResponse := httptest.NewRecorder()
	handler.ServeHTTP(fallbackResponse, fallback)
	if fallbackResponse.Code != http.StatusOK || planner.last.ReasoningEffort != "medium" {
		t.Fatalf("settings default effort not used: status=%d effort=%q body=%s", fallbackResponse.Code, planner.last.ReasoningEffort, fallbackResponse.Body.String())
	}

	invalid := httptest.NewRequest(http.MethodPost, "/api/image-projects/segment-preview", bytes.NewReader(imageCreatePayload("养老", "第一句。第二句。", map[string]any{"reasoning_effort": "impossible"})))
	invalid.Header.Set("Content-Type", "application/json")
	invalidResponse := httptest.NewRecorder()
	handler.ServeHTTP(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid effort status=%d body=%s", invalidResponse.Code, invalidResponse.Body.String())
	}
}

func TestImageProjectsHTTPRejectsEmptyCustomStyle(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	handler := testImageHandler(db, nil, nil)
	request := httptest.NewRequest(http.MethodPost, "/api/image-projects", strings.NewReader(`{"title":"自定义","script":"有效文案。","image_count":1,"ratio":"3:4","style":"custom","custom_style":"  ","concurrency":1}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("empty custom style status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestImageProjectsHTTPPersistsExplicitImageAttempts(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	runtime := imageRuntimeStub{runtime: consoleSettings.Runtime{PublicSettings: domain.PublicSettings{DataRoot: t.TempDir(), ImageBaseURL: "https://api.example.com/v1", ImageModel: "gpt-image-2", MaxImageConcurrency: 2, ImageGenerationAttempts: 2}, ImageAPIKey: "configured"}}
	handler := testImageHandler(db, runtime, imageGeneratorStub{err: errors.New("fail")})
	request := httptest.NewRequest(http.MethodPost, "/api/image-projects", bytes.NewReader(imageCreatePayload("x", "第一句。", map[string]any{"image_count": 1, "image_attempts": 3})))
	request.Header.Set("Content-Type", "application/json")
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, request)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var detail domain.ImageProjectDetail
	if err := json.Unmarshal(created.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Project.ImageAttempts != 3 {
		t.Fatalf("image attempts=%d", detail.Project.ImageAttempts)
	}
	generated := httptest.NewRecorder()
	handler.ServeHTTP(generated, httptest.NewRequest(http.MethodPost, "/api/image-projects/"+detail.Project.ID+"/generate", nil))
	if generated.Code != http.StatusOK {
		t.Fatalf("partial generate status=%d body=%s", generated.Code, generated.Body.String())
	}
	if err := json.Unmarshal(generated.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Items[0].AttemptCount < 1 || detail.Items[0].Status != "failed" {
		t.Fatalf("item=%+v", detail.Items[0])
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
	handler := testImageHandler(db, runtime, imageGeneratorStub{result: imageproject.GenerateResult{Bytes: imageBytes, MIMEType: "image/png", Width: 3, Height: 4}})
	request := httptest.NewRequest(http.MethodPost, "/api/image-projects", bytes.NewReader(imageCreatePayload("x", "第一句。第二句。", map[string]any{"image_count": 2})))
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
