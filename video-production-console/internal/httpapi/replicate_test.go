package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"video-production-console/internal/replicate"
	"video-production-console/internal/store"
	"video-production-console/internal/videoqc"
)

type fakeReplicateAPI struct {
	called    string
	err       error
	input     any
	id        string
	filename  string
	fileBytes []byte
	openPath  string
	project   *replicate.Project
}

func replicateFixture() replicate.Project {
	now := time.Date(2026, 10, 16, 8, 30, 0, 0, time.UTC)
	return replicate.Project{
		ID: "project-1", Title: "海鲜复刻", ReferenceID: "ref-1", AnalysisID: "analysis-1", Product: "海鲜礼盒",
		StyleLock: "朱砂水墨", Ratio: "9:16", VideoProvider: "runninghub_h3", ImageModel: "image-1", Variants: 2,
		Status: "stitched", FinalURL: "/api/replicate/project-1/final?v=1", FinalDurationS: 6, CreatedAt: now, UpdatedAt: now,
		Shots: []replicate.Shot{{
			ID: "shot-1", Position: 1, Purpose: "钩子", SourceStartS: 0, SourceEndS: 2.4, DurationS: 6,
			FirstFramePrompt: "首帧", MotionPrompt: "推进", Negative: "乱码", Keep: "红色包装",
			FirstFrameURL: "/api/replicate/shots/shot-1/first-frame?v=1", FirstFrameSource: "generated", FirstFrameStatus: "ready",
			SelectedTakeID: "take-1", Takes: []replicate.Take{{ID: "take-1", Provider: "runninghub_h3", Status: "succeeded", DurationS: 6, Width: 720, Height: 1280, URL: "/api/replicate/takes/take-1/video", CreatedAt: now}},
		}},
		Progress: replicate.Progress{TakesTotal: 1, TakesDone: 1},
	}
}

func (f *fakeReplicateAPI) result() (replicate.Project, error) {
	if f.err != nil {
		return replicate.Project{}, f.err
	}
	if f.project != nil {
		return *f.project, nil
	}
	return replicateFixture(), nil
}

func (f *fakeReplicateAPI) Create(_ context.Context, in replicate.CreateRequest) (replicate.Project, error) {
	f.called, f.input = "Create", in
	return f.result()
}
func (f *fakeReplicateAPI) Get(_ context.Context, id string) (replicate.Project, error) {
	f.called, f.id = "Get", id
	return f.result()
}
func (f *fakeReplicateAPI) List(_ context.Context, limit int) ([]replicate.Project, error) {
	f.called, f.input = "List", limit
	if f.err != nil {
		return nil, f.err
	}
	return []replicate.Project{replicateFixture()}, nil
}
func (f *fakeReplicateAPI) Patch(_ context.Context, id string, in replicate.ProjectPatch) (replicate.Project, error) {
	f.called, f.id, f.input = "Patch", id, in
	return f.result()
}
func (f *fakeReplicateAPI) Delete(_ context.Context, id string) error {
	f.called, f.id = "Delete", id
	return f.err
}
func (f *fakeReplicateAPI) AddShot(_ context.Context, id string, in replicate.ShotPatch) (replicate.Project, error) {
	f.called, f.id, f.input = "AddShot", id, in
	return f.result()
}
func (f *fakeReplicateAPI) PatchShot(_ context.Context, id string, in replicate.ShotPatch) (replicate.Project, error) {
	f.called, f.id, f.input = "PatchShot", id, in
	return f.result()
}
func (f *fakeReplicateAPI) DeleteShot(_ context.Context, id string) error {
	f.called, f.id = "DeleteShot", id
	return f.err
}
func (f *fakeReplicateAPI) Reorder(_ context.Context, id string, shotIDs []string) (replicate.Project, error) {
	f.called, f.id, f.input = "Reorder", id, shotIDs
	return f.result()
}
func (f *fakeReplicateAPI) GenerateFirstFrames(_ context.Context, id string, shotIDs []string) (replicate.Project, error) {
	f.called, f.id, f.input = "GenerateFirstFrames", id, shotIDs
	return f.result()
}
func (f *fakeReplicateAPI) UploadFirstFrame(_ context.Context, id, filename string, r io.Reader) (replicate.Project, error) {
	f.called, f.id, f.filename = "UploadFirstFrame", id, filename
	f.fileBytes, _ = io.ReadAll(r)
	return f.result()
}
func (f *fakeReplicateAPI) UseReferenceFrame(_ context.Context, id string, index int) (replicate.Project, error) {
	f.called, f.id, f.input = "UseReferenceFrame", id, index
	return f.result()
}
func (f *fakeReplicateAPI) OpenFirstFrame(_ context.Context, id string) (*os.File, error) {
	f.called, f.id = "OpenFirstFrame", id
	return f.open()
}
func (f *fakeReplicateAPI) Generate(_ context.Context, id string, in replicate.GenerateRequest) (replicate.GenerateResponse, error) {
	f.called, f.id, f.input = "Generate", id, in
	if f.err != nil {
		return replicate.GenerateResponse{}, f.err
	}
	return replicate.GenerateResponse{Project: replicateFixture(), Skipped: []replicate.Skipped{{ShotID: "shot-2", Reason: "先出首帧图或上传图片"}}}, nil
}
func (f *fakeReplicateAPI) SelectTake(_ context.Context, id string) (replicate.Project, error) {
	f.called, f.id = "SelectTake", id
	return f.result()
}
func (f *fakeReplicateAPI) CancelTake(_ context.Context, id string) (replicate.Project, error) {
	f.called, f.id = "CancelTake", id
	return f.result()
}
func (f *fakeReplicateAPI) DeleteTake(_ context.Context, id string) error {
	f.called, f.id = "DeleteTake", id
	return f.err
}
func (f *fakeReplicateAPI) OpenTake(_ context.Context, id string) (*os.File, error) {
	f.called, f.id = "OpenTake", id
	return f.open()
}
func (f *fakeReplicateAPI) Stitch(_ context.Context, id, audio string) (replicate.Project, error) {
	f.called, f.id, f.input = "Stitch", id, audio
	return f.result()
}
func (f *fakeReplicateAPI) OpenFinal(_ context.Context, id string) (*os.File, error) {
	f.called, f.id = "OpenFinal", id
	return f.open()
}
func (f *fakeReplicateAPI) StartQC(_ context.Context, id, model string) (videoqc.Check, error) {
	f.called, f.id, f.input = "StartQC", id, model
	if f.err != nil {
		return videoqc.Check{}, f.err
	}
	return videoqc.Check{VideoQCCheck: store.VideoQCCheck{ID: "qc-1", Source: "replicate", SourceID: id, Status: "queued"}, Frames: []videoqc.Frame{}}, nil
}
func (f *fakeReplicateAPI) Providers(context.Context) (replicate.ProvidersResponse, error) {
	f.called = "Providers"
	if f.err != nil {
		return replicate.ProvidersResponse{}, f.err
	}
	return replicate.ProvidersResponse{Providers: []replicate.Provider{{ID: "runninghub_h3", Label: "MiniMax H3（RunningHub）", Available: true, Keys: 2, Durations: []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, DefaultDuration: 6}}, DefaultProvider: "runninghub_h3"}, nil
}
func (f *fakeReplicateAPI) open() (*os.File, error) {
	if f.err != nil {
		return nil, f.err
	}
	return os.Open(f.openPath)
}

func TestReplicateRoutesSuccess(t *testing.T) {
	temp := filepath.Join(t.TempDir(), "复刻片段.mp4")
	if err := os.WriteFile(temp, []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, method, target, body, contentType, call, contains string
		status                                                  int
	}{
		{"providers", "GET", "/api/replicate/providers", "", "", "Providers", `"default_provider":"runninghub_h3"`, 200},
		{"create", "POST", "/api/replicate", `{"reference_id":"ref-1","analysis_id":"analysis-1","product":"海鲜礼盒","ratio":"9:16","video_provider":"grok","image_model":"img","variants":2}`, "application/json", "Create", `"id":"project-1"`, 201},
		{"list", "GET", "/api/replicate?limit=12", "", "", "List", `"id":"project-1"`, 200},
		{"get", "GET", "/api/replicate/project-1", "", "", "Get", `"shots":[`, 200},
		{"patch", "PATCH", "/api/replicate/project-1", `{"title":"新标题","variants":3}`, "application/json", "Patch", `"id":"project-1"`, 200},
		{"delete", "DELETE", "/api/replicate/project-1", "", "", "Delete", "", 204},
		{"add shot", "POST", "/api/replicate/project-1/shots", `{"purpose":"证据","duration_s":6,"after_position":1}`, "application/json", "AddShot", `"id":"project-1"`, 200},
		{"patch shot", "PATCH", "/api/replicate/shots/shot-1", `{"motion_prompt":"慢慢推进"}`, "application/json", "PatchShot", `"id":"project-1"`, 200},
		{"delete shot", "DELETE", "/api/replicate/shots/shot-1", "", "", "DeleteShot", "", 204},
		{"reorder", "POST", "/api/replicate/project-1/shots/reorder", `{"shot_ids":["shot-2","shot-1"]}`, "application/json", "Reorder", `"id":"project-1"`, 200},
		{"first frames", "POST", "/api/replicate/project-1/first-frames", `{"shot_ids":["shot-1"]}`, "application/json", "GenerateFirstFrames", `"id":"project-1"`, 202},
		{"reference frame", "POST", "/api/replicate/shots/shot-1/first-frame/reference", `{"frame_index":3}`, "application/json", "UseReferenceFrame", `"id":"project-1"`, 200},
		{"get first frame", "GET", "/api/replicate/shots/shot-1/first-frame", "", "", "OpenFirstFrame", "0123456789", 200},
		{"generate", "POST", "/api/replicate/project-1/generate", `{"shot_ids":["shot-1"],"variants":3,"provider":"grok"}`, "application/json", "Generate", `"skipped":[`, 202},
		{"select take", "POST", "/api/replicate/takes/take-1/select", "", "", "SelectTake", `"id":"project-1"`, 200},
		{"cancel take", "POST", "/api/replicate/takes/take-1/cancel", "", "", "CancelTake", `"id":"project-1"`, 200},
		{"delete take", "DELETE", "/api/replicate/takes/take-1", "", "", "DeleteTake", "", 204},
		{"get take", "GET", "/api/replicate/takes/take-1/video", "", "", "OpenTake", "0123456789", 200},
		{"stitch", "POST", "/api/replicate/project-1/stitch", `{"audio":"none"}`, "application/json", "Stitch", `"id":"project-1"`, 202},
		{"get final", "GET", "/api/replicate/project-1/final", "", "", "OpenFinal", "0123456789", 200},
		{"qc", "POST", "/api/replicate/project-1/qc", `{"model":"vision-1"}`, "application/json", "StartQC", `"id":"qc-1"`, 202},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			service := &fakeReplicateAPI{openPath: temp}
			req := httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body))
			if tc.contentType != "" {
				req.Header.Set("Content-Type", tc.contentType)
			}
			w := httptest.NewRecorder()
			newReplicateHandler(service).ServeHTTP(w, req)
			if w.Code != tc.status || service.called != tc.call || !strings.Contains(w.Body.String(), tc.contains) {
				t.Fatalf("status=%d call=%q body=%s", w.Code, service.called, w.Body.String())
			}
			if got := w.Header().Get("Cache-Control"); got != "no-store" {
				t.Fatalf("Cache-Control=%q", got)
			}
			if tc.name == "list" && strings.Contains(w.Body.String(), `"shots"`) {
				t.Fatalf("list response leaked shots: %s", w.Body.String())
			}
		})
	}
}

func TestReplicateUploadFirstFrame(t *testing.T) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", "首帧.png")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("fake-png"))
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	multipartBody := append([]byte(nil), body.Bytes()...)
	service := &fakeReplicateAPI{}
	req := httptest.NewRequest(http.MethodPost, "/api/replicate/shots/shot-1/first-frame", bytes.NewReader(multipartBody))
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	newReplicateHandler(service).ServeHTTP(w, req)
	if w.Code != 200 || service.called != "UploadFirstFrame" || service.id != "shot-1" || service.filename != "首帧.png" || string(service.fileBytes) != "fake-png" {
		t.Fatalf("status=%d call=%q id=%q filename=%q bytes=%q body=%s", w.Code, service.called, service.id, service.filename, service.fileBytes, w.Body.String())
	}

	service = &fakeReplicateAPI{err: &replicate.Error{Code: "replicate_not_found", Message: "镜头不存在"}}
	req = httptest.NewRequest(http.MethodPost, "/api/replicate/shots/missing/first-frame", bytes.NewReader(multipartBody))
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w = httptest.NewRecorder()
	newReplicateHandler(service).ServeHTTP(w, req)
	if w.Code != 404 || !strings.Contains(w.Body.String(), `"code":"replicate_not_found"`) {
		t.Fatalf("failure status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestReplicateDetailKeepsEmptyShotsArray(t *testing.T) {
	project := replicateFixture()
	project.Shots = nil
	service := &fakeReplicateAPI{project: &project}
	w := httptest.NewRecorder()
	newReplicateHandler(service).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/replicate/project-1", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"shots":[]`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestReplicateRoutesMapServiceErrors(t *testing.T) {
	tests := []struct{ method, target, body, contentType string }{
		{"GET", "/api/replicate/providers", "", ""}, {"POST", "/api/replicate", `{}`, "application/json"},
		{"GET", "/api/replicate", "", ""}, {"GET", "/api/replicate/missing", "", ""},
		{"PATCH", "/api/replicate/missing", `{}`, "application/json"}, {"DELETE", "/api/replicate/missing", "", ""},
		{"POST", "/api/replicate/missing/shots", `{}`, "application/json"}, {"PATCH", "/api/replicate/shots/missing", `{}`, "application/json"},
		{"DELETE", "/api/replicate/shots/missing", "", ""}, {"POST", "/api/replicate/missing/shots/reorder", `{"shot_ids":[]}`, "application/json"},
		{"POST", "/api/replicate/missing/first-frames", `{}`, "application/json"}, {"POST", "/api/replicate/shots/missing/first-frame/reference", `{"frame_index":1}`, "application/json"},
		{"GET", "/api/replicate/shots/missing/first-frame", "", ""}, {"POST", "/api/replicate/missing/generate", `{}`, "application/json"},
		{"POST", "/api/replicate/takes/missing/select", "", ""}, {"POST", "/api/replicate/takes/missing/cancel", "", ""},
		{"DELETE", "/api/replicate/takes/missing", "", ""}, {"GET", "/api/replicate/takes/missing/video", "", ""},
		{"POST", "/api/replicate/missing/stitch", `{}`, "application/json"}, {"GET", "/api/replicate/missing/final", "", ""},
		{"POST", "/api/replicate/missing/qc", `{}`, "application/json"},
	}
	for _, tc := range tests {
		name := tc.method + " " + tc.target
		t.Run(name, func(t *testing.T) {
			service := &fakeReplicateAPI{err: &replicate.Error{Code: "replicate_not_found", Message: "复刻项目不存在"}}
			req := httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body))
			if tc.contentType != "" {
				req.Header.Set("Content-Type", tc.contentType)
			}
			w := httptest.NewRecorder()
			newReplicateHandler(service).ServeHTTP(w, req)
			if w.Code != 404 || !strings.Contains(w.Body.String(), `"code":"replicate_not_found"`) {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestReplicateErrorStatusMappings(t *testing.T) {
	for _, tc := range []struct {
		code   string
		status int
	}{
		{"invalid_replicate", 400}, {"analysis_has_no_prompts", 400}, {"stitch_missing_takes", 400},
		{"video_provider_not_configured", 409}, {"replicate_conflict", 409}, {"replicate_too_large", 413},
		{"unsupported_first_frame", 415}, {"unsafe_replicate_path", 403}, {"upstream_failed", 502},
	} {
		t.Run(tc.code, func(t *testing.T) {
			service := &fakeReplicateAPI{err: &replicate.Error{Code: tc.code, Message: "中文错误"}}
			w := httptest.NewRecorder()
			newReplicateHandler(service).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/replicate/project-1", nil))
			if w.Code != tc.status || !strings.Contains(w.Body.String(), `"message":"中文错误"`) {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestReplicateRejectsInvalidJSONLimitAndUnsafePaths(t *testing.T) {
	service := &fakeReplicateAPI{}
	h := newReplicateHandler(service)
	for _, tc := range []struct {
		name, target, body string
		status             int
	}{
		{"unknown field", "/api/replicate", `{"blank":true,"extra":1}`, 400},
		{"trailing JSON", "/api/replicate", `{"blank":true}{}`, 400},
		{"invalid utf8", "/api/replicate", string([]byte{'{', '"', 't', 'i', 't', 'l', 'e', '"', ':', '"', 0xff, '"', '}'}), 400},
		{"too large", "/api/replicate", `{"title":"` + strings.Repeat("x", maxReplicateJSONBytes) + `"}`, 413},
		{"unsafe slash", "/api/replicate/shots/..%5Csecret/first-frame", "", 403},
		{"unsafe dot", "/api/replicate/shots/%2e%2e/first-frame", "", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			method := http.MethodPost
			if tc.body == "" {
				method = http.MethodGet
			}
			req := httptest.NewRequest(method, tc.target, strings.NewReader(tc.body))
			if tc.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != tc.status || service.called != "" {
				t.Fatalf("status=%d called=%q body=%s", w.Code, service.called, w.Body.String())
			}
		})
	}
}

func TestReplicateValidatesLimitAndMultipart(t *testing.T) {
	h := newReplicateHandler(&fakeReplicateAPI{})
	for _, target := range []string{"/api/replicate?limit=0", "/api/replicate?limit=nope", "/api/replicate?limit=101"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
		if w.Code != 400 {
			t.Fatalf("%s status=%d body=%s", target, w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/replicate/shots/shot-1/first-frame", strings.NewReader("not multipart"))
	req.Header.Set("Content-Type", "text/plain")
	h.ServeHTTP(w, req)
	if w.Code != 415 {
		t.Fatalf("content type status=%d body=%s", w.Code, w.Body.String())
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, _ := mw.CreateFormFile("file", "too-large.png")
	_, _ = io.Copy(part, io.LimitReader(zeroReplicateReader{}, maxFirstFrameUploadBytes+(128<<10)))
	_ = mw.Close()
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/replicate/shots/shot-1/first-frame", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	h.ServeHTTP(w, req)
	if w.Code != 413 {
		t.Fatalf("oversize status=%d body=%s", w.Code, w.Body.String())
	}
}

type zeroReplicateReader struct{}

func (zeroReplicateReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func TestReplicateDownloadsSupportRangeAndChineseFilename(t *testing.T) {
	path := filepath.Join(t.TempDir(), "成片.mp4")
	if err := os.WriteFile(path, []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := &fakeReplicateAPI{openPath: path}
	h := newReplicateHandler(service)
	for _, target := range []string{"/api/replicate/takes/take-1/video", "/api/replicate/project-1/final"} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.Header.Set("Range", "bytes=2-5")
		h.ServeHTTP(w, req)
		if w.Code != http.StatusPartialContent || w.Body.String() != "2345" || w.Header().Get("Content-Range") != "bytes 2-5/10" {
			t.Fatalf("%s status=%d range=%q body=%q", target, w.Code, w.Header().Get("Content-Range"), w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/replicate/project-1/final?download=1", nil))
	disposition := w.Header().Get("Content-Disposition")
	if w.Code != 200 || !strings.Contains(disposition, "filename*=UTF-8''") || !strings.Contains(disposition, "%E5%A4%8D%E5%88%BB") {
		t.Fatalf("status=%d Content-Disposition=%q", w.Code, disposition)
	}
}

func TestReplicateRequestBodiesReachService(t *testing.T) {
	service := &fakeReplicateAPI{}
	h := newReplicateHandler(service)
	request := func(method, target, body string) {
		t.Helper()
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code >= 300 {
			t.Fatalf("%s %s status=%d body=%s", method, target, w.Code, w.Body.String())
		}
	}
	request("POST", "/api/replicate", `{"blank":true,"title":"空白项目"}`)
	if in := service.input.(replicate.CreateRequest); !in.Blank || in.Title != "空白项目" {
		t.Fatalf("create input=%+v", in)
	}
	request("PATCH", "/api/replicate/project-1", `{"title":"新标题","variants":4}`)
	if in := service.input.(replicate.ProjectPatch); in.Title == nil || *in.Title != "新标题" || in.Variants == nil || *in.Variants != 4 {
		t.Fatalf("patch input=%+v", in)
	}
	request("POST", "/api/replicate/project-1/shots", `{"purpose":"展示","duration_s":10,"after_position":2}`)
	if in := service.input.(replicate.ShotPatch); in.Purpose == nil || *in.Purpose != "展示" || in.AfterPosition == nil || *in.AfterPosition != 2 {
		t.Fatalf("shot input=%+v", in)
	}
	request("POST", "/api/replicate/project-1/generate", `{"shot_ids":["shot-1"],"variants":4,"provider":"grok"}`)
	if in := service.input.(replicate.GenerateRequest); in.Variants != 4 || in.Provider != "grok" || len(in.ShotIDs) != 1 {
		t.Fatalf("generate input=%+v", in)
	}
	request("POST", "/api/replicate/project-1/qc", `{"model":"vision-1"}`)
	if service.input != "vision-1" {
		t.Fatalf("qc model=%#v", service.input)
	}
}

func TestReplicateErrorEnvelopeIsJSON(t *testing.T) {
	service := &fakeReplicateAPI{err: errors.New("database password must not leak")}
	w := httptest.NewRecorder()
	newReplicateHandler(service).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/replicate/project-1", nil))
	var envelope map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if w.Code != 500 || envelope["code"] != "replicate_failed" || strings.Contains(w.Body.String(), "password") {
		t.Fatalf("status=%d envelope=%v", w.Code, envelope)
	}
}
