package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"video-production-console/internal/domain"
	"video-production-console/internal/refanalysis"
	"video-production-console/internal/settings"
	"video-production-console/internal/store"
	"video-production-console/internal/videoqc"
)

type fakeVideoQCService struct {
	start       func(context.Context, videoqc.Input) (videoqc.Check, error)
	startUpload func(context.Context, videoqc.Input, string, io.Reader) (videoqc.Check, error)
	list        func(context.Context, int) ([]store.VideoQCCheck, error)
	get         func(context.Context, string) (videoqc.Check, error)
	openFrame   func(context.Context, string, int) (*os.File, error)
	openVideo   func(context.Context, string) (*os.File, error)
	delete      func(context.Context, string) error
}

func (f *fakeVideoQCService) Start(ctx context.Context, in videoqc.Input) (videoqc.Check, error) {
	if f.start == nil {
		return videoqc.Check{}, errors.New("unexpected Start")
	}
	return f.start(ctx, in)
}

func (f *fakeVideoQCService) StartUpload(ctx context.Context, in videoqc.Input, filename string, r io.Reader) (videoqc.Check, error) {
	if f.startUpload == nil {
		return videoqc.Check{}, errors.New("unexpected StartUpload")
	}
	return f.startUpload(ctx, in, filename, r)
}

func (f *fakeVideoQCService) List(ctx context.Context, limit int) ([]store.VideoQCCheck, error) {
	if f.list == nil {
		return nil, errors.New("unexpected List")
	}
	return f.list(ctx, limit)
}

func (f *fakeVideoQCService) Get(ctx context.Context, id string) (videoqc.Check, error) {
	if f.get == nil {
		return videoqc.Check{}, errors.New("unexpected Get")
	}
	return f.get(ctx, id)
}

func (f *fakeVideoQCService) OpenFrame(ctx context.Context, id string, index int) (*os.File, error) {
	if f.openFrame == nil {
		return nil, errors.New("unexpected OpenFrame")
	}
	return f.openFrame(ctx, id, index)
}

func (f *fakeVideoQCService) OpenVideo(ctx context.Context, id string) (*os.File, error) {
	if f.openVideo == nil {
		return nil, errors.New("unexpected OpenVideo")
	}
	return f.openVideo(ctx, id)
}

func (f *fakeVideoQCService) Delete(ctx context.Context, id string) error {
	if f.delete == nil {
		return errors.New("unexpected Delete")
	}
	return f.delete(ctx, id)
}

func TestVideoQCJSONStartAndDetailShapes(t *testing.T) {
	service := &fakeVideoQCService{}
	service.start = func(_ context.Context, in videoqc.Input) (videoqc.Check, error) {
		if in.Source != "reference" || in.SourceID != "ref-1" || in.Product != "红色茶叶盒" || in.Model != "vision-test" {
			t.Fatalf("input=%+v", in)
		}
		return videoqc.Check{VideoQCCheck: store.VideoQCCheck{ID: "qc-1", Source: "reference", SourceID: "ref-1", Status: "queued"}}, nil
	}
	service.get = func(_ context.Context, id string) (videoqc.Check, error) {
		if id != "qc-1" {
			t.Fatalf("id=%q", id)
		}
		return videoqc.Check{
			VideoQCCheck: store.VideoQCCheck{ID: id, Source: "reference", SourceID: "ref-1", Status: "completed", Verdict: "pass", Score: 96},
			Report:       &videoqc.Report{Summary: "画面稳定", Issues: []videoqc.Issue{}, Checked: []string{"hands", "body"}},
			Frames:       []videoqc.Frame{{Index: 1, TimeS: 0, URL: "/api/video-qc/qc-1/frames/1"}},
		}, nil
	}
	h := newVideoQCHandler(service)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/video-qc", strings.NewReader(`{"source":"reference","source_id":"ref-1","product":"红色茶叶盒","model":"vision-test"}`))
	r.Header.Set("Content-Type", "application/json; charset=utf-8")
	h.ServeHTTP(w, r)
	if w.Code != http.StatusAccepted {
		t.Fatalf("POST status=%d body=%s", w.Code, w.Body.String())
	}
	var started map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	if started["id"] != "qc-1" || started["report"] != nil {
		t.Fatalf("POST JSON=%v", started)
	}
	frames, ok := started["frames"].([]any)
	if !ok || len(frames) != 0 {
		t.Fatalf("POST frames=%#v", started["frames"])
	}

	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/video-qc/qc-1", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"summary":"画面稳定"`) || !strings.Contains(w.Body.String(), `"url":"/api/video-qc/qc-1/frames/1"`) {
		t.Fatalf("GET status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestVideoQCRejectsMalformedAndUnsupportedJSONRequests(t *testing.T) {
	called := false
	service := &fakeVideoQCService{start: func(_ context.Context, in videoqc.Input) (videoqc.Check, error) {
		called = true
		if in.Source == "image_video_job" {
			return videoqc.Check{}, videoqc.ErrUnsupportedSource
		}
		return videoqc.Check{}, nil
	}}
	h := newVideoQCHandler(service)
	for _, tc := range []struct {
		name        string
		contentType string
		body        []byte
		status      int
		code        string
		wantCall    bool
	}{
		{"invalid UTF-8", "application/json", []byte{'{', '"', 'm', 'o', 'd', 'e', 'l', '"', ':', '"', 0xff, '"', '}'}, 400, "invalid_video_qc", false},
		{"unknown JSON field", "application/json", []byte(`{"source":"reference","source_id":"x","extra":true}`), 400, "invalid_video_qc", false},
		{"unsupported source", "application/json", []byte(`{"source":"image_video_job","source_id":"job-1"}`), 400, "invalid_video_qc", true},
		{"unsupported content type", "text/plain", []byte("video"), 415, "unsupported_video_format", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called = false
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/video-qc", bytes.NewReader(tc.body))
			r.Header.Set("Content-Type", tc.contentType)
			h.ServeHTTP(w, r)
			if w.Code != tc.status || !strings.Contains(w.Body.String(), `"code":"`+tc.code+`"`) || called != tc.wantCall {
				t.Fatalf("status=%d body=%s called=%v", w.Code, w.Body.String(), called)
			}
		})
	}
}

func TestVideoQCMultipartUploadPassesBoundedFileToService(t *testing.T) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", "复刻版_海鲜.mp4")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("fake-mp4"))
	_ = mw.WriteField("product", "海鲜礼盒")
	_ = mw.WriteField("model", "vision-test")
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	service := &fakeVideoQCService{startUpload: func(_ context.Context, in videoqc.Input, filename string, r io.Reader) (videoqc.Check, error) {
		got, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		if filename != "复刻版_海鲜.mp4" || string(got) != "fake-mp4" || in.Source != "upload" || in.Product != "海鲜礼盒" || in.Model != "vision-test" {
			t.Fatalf("filename=%q bytes=%q input=%+v", filename, got, in)
		}
		return videoqc.Check{VideoQCCheck: store.VideoQCCheck{ID: "upload-1", Source: "upload", Title: filename, Status: "queued"}}, nil
	}}
	h := newVideoQCHandler(service)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/video-qc", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	h.ServeHTTP(w, r)
	if w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), `"id":"upload-1"`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestVideoQCUploadAndServiceErrorMappings(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"invalid", videoqc.ErrInvalidInput, 400, "invalid_video_qc"},
		{"not configured", videoqc.ErrNotConfigured, 409, "video_qc_not_configured"},
		{"unsupported format", videoqc.ErrUnsupportedFormat, 415, "unsupported_video_format"},
		{"too large", videoqc.ErrTooLarge, 413, "video_qc_too_large"},
		{"unsafe path", videoqc.ErrUnsafePath, 403, "unsafe_video_qc_path"},
		{"media not ready", videoqc.ErrMediaNotReady, 400, "invalid_video_qc"},
		{"reference not found", store.ErrReferenceNotFound, 400, "invalid_video_qc"},
		{"not found", store.ErrVideoQCNotFound, 404, "video_qc_not_found"},
		{"internal", errors.New("database unavailable"), 500, "video_qc_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &fakeVideoQCService{start: func(context.Context, videoqc.Input) (videoqc.Check, error) {
				return videoqc.Check{}, tc.err
			}}
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/video-qc", strings.NewReader(`{"source":"reference","source_id":"ref-1"}`))
			r.Header.Set("Content-Type", "application/json")
			newVideoQCHandler(service).ServeHTTP(w, r)
			if w.Code != tc.status || !strings.Contains(w.Body.String(), `"code":"`+tc.code+`"`) {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}

	service := &fakeVideoQCService{startUpload: func(context.Context, videoqc.Input, string, io.Reader) (videoqc.Check, error) {
		return videoqc.Check{}, videoqc.ErrUnsupportedFormat
	}}
	w := httptest.NewRecorder()
	r := multipartVideoQCRequest(t, "bad.exe", []byte("MZ"))
	newVideoQCHandler(service).ServeHTTP(w, r)
	if w.Code != 415 || !strings.Contains(w.Body.String(), `"code":"unsupported_video_format"`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestVideoQCUploadRejectsMissingEmptyAndInvalidUTF8Fields(t *testing.T) {
	called := false
	service := &fakeVideoQCService{startUpload: func(_ context.Context, _ videoqc.Input, _ string, r io.Reader) (videoqc.Check, error) {
		called = true
		content, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		if len(content) == 0 {
			return videoqc.Check{}, videoqc.ErrUnsupportedFormat
		}
		return videoqc.Check{}, nil
	}}
	h := newVideoQCHandler(service)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("product", "海鲜礼盒")
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/video-qc", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	h.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest || called {
		t.Fatalf("missing file status=%d body=%s called=%v", w.Code, w.Body.String(), called)
	}

	w = httptest.NewRecorder()
	h.ServeHTTP(w, multipartVideoQCRequest(t, "empty.mp4", nil))
	if w.Code != http.StatusUnsupportedMediaType || !called {
		t.Fatalf("empty file status=%d body=%s called=%v", w.Code, w.Body.String(), called)
	}

	called = false
	body.Reset()
	mw = multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", "video.mp4")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("fake"))
	_ = mw.WriteField("product", string([]byte{0xff}))
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPost, "/api/video-qc", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	h.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest || called {
		t.Fatalf("invalid UTF-8 status=%d body=%s called=%v", w.Code, w.Body.String(), called)
	}
}

type zeroVideoQCReader struct{}

func (zeroVideoQCReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func TestVideoQCMultipartBodyOver300MBIsRejectedAndTemporaryFileRemoved(t *testing.T) {
	tempRoot := t.TempDir()
	t.Setenv("TMPDIR", tempRoot)
	t.Setenv("TMP", tempRoot)
	t.Setenv("TEMP", tempRoot)
	before, err := filepath.Glob(filepath.Join(os.TempDir(), "multipart-*"))
	if err != nil {
		t.Fatal(err)
	}
	called := false
	service := &fakeVideoQCService{startUpload: func(context.Context, videoqc.Input, string, io.Reader) (videoqc.Check, error) {
		called = true
		return videoqc.Check{}, nil
	}}
	const boundary = "video-qc-limit-boundary"
	header := "--" + boundary + "\r\n" +
		"Content-Disposition: form-data; name=\"file\"; filename=\"large.mp4\"\r\n" +
		"Content-Type: video/mp4\r\n\r\n"
	tail := "\r\n--" + boundary + "--\r\n"
	body := io.MultiReader(
		strings.NewReader(header),
		io.LimitReader(zeroVideoQCReader{}, videoqc.MaxUploadBytes+1024),
		strings.NewReader(tail),
	)
	r := httptest.NewRequest(http.MethodPost, "/api/video-qc", body)
	r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	w := httptest.NewRecorder()
	newVideoQCHandler(service).ServeHTTP(w, r)
	if w.Code != http.StatusRequestEntityTooLarge || !strings.Contains(w.Body.String(), `"code":"video_qc_too_large"`) || called {
		t.Fatalf("status=%d body=%s called=%v", w.Code, w.Body.String(), called)
	}
	after, err := filepath.Glob(filepath.Join(os.TempDir(), "multipart-*"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Fatalf("multipart temporary files leaked: before=%v after=%v", before, after)
	}
}

func TestVideoQCListUsesDefaultAndExplicitLimitsWithoutDetailFields(t *testing.T) {
	var limits []int
	service := &fakeVideoQCService{list: func(_ context.Context, limit int) ([]store.VideoQCCheck, error) {
		limits = append(limits, limit)
		return []store.VideoQCCheck{{ID: "qc-1", Source: "upload", Status: "completed"}}, nil
	}}
	h := newVideoQCHandler(service)
	for _, rawURL := range []string{"/api/video-qc", "/api/video-qc?limit=7"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, rawURL, nil))
		if w.Code != 200 || strings.Contains(w.Body.String(), `"report"`) || strings.Contains(w.Body.String(), `"frames"`) {
			t.Fatalf("url=%s status=%d body=%s", rawURL, w.Code, w.Body.String())
		}
	}
	if len(limits) != 2 || limits[0] != 30 || limits[1] != 7 {
		t.Fatalf("limits=%v", limits)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/video-qc?limit=bad", nil))
	if w.Code != 400 {
		t.Fatalf("invalid limit status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestVideoQCListAndDetailFailures(t *testing.T) {
	service := &fakeVideoQCService{
		list: func(context.Context, int) ([]store.VideoQCCheck, error) {
			return nil, errors.New("database unavailable")
		},
		get: func(context.Context, string) (videoqc.Check, error) {
			return videoqc.Check{}, store.ErrVideoQCNotFound
		},
	}
	h := newVideoQCHandler(service)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/video-qc", nil))
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), `"code":"video_qc_failed"`) {
		t.Fatalf("list status=%d body=%s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/video-qc/missing", nil))
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), `"code":"video_qc_not_found"`) {
		t.Fatalf("detail status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestVideoQCFrameCachingVideoRangeDeleteAndFailures(t *testing.T) {
	dir := t.TempDir()
	framePath := filepath.Join(dir, "frame.jpg")
	videoPath := filepath.Join(dir, "source.mp4")
	if err := os.WriteFile(framePath, []byte{0xff, 0xd8, 0xff, 0xd9}, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(videoPath, []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	deleted := ""
	service := &fakeVideoQCService{
		openFrame: func(_ context.Context, id string, index int) (*os.File, error) {
			if id != "qc-1" || index != 1 {
				return nil, store.ErrVideoQCNotFound
			}
			return os.Open(framePath)
		},
		openVideo: func(_ context.Context, id string) (*os.File, error) {
			if id != "qc-1" {
				return nil, store.ErrVideoQCNotFound
			}
			return os.Open(videoPath)
		},
		delete: func(_ context.Context, id string) error {
			if id != "qc-1" {
				return store.ErrVideoQCNotFound
			}
			deleted = id
			return nil
		},
	}
	h := newVideoQCHandler(service)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/video-qc/qc-1/frames/1", nil))
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/jpeg" || w.Header().Get("Cache-Control") != "private, max-age=31536000, immutable" || !bytes.Equal(w.Body.Bytes(), []byte{0xff, 0xd8, 0xff, 0xd9}) {
		t.Fatalf("frame status=%d headers=%v body=%x", w.Code, w.Header(), w.Body.Bytes())
	}

	w = httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/video-qc/qc-1/video", nil)
	r.Header.Set("Range", "bytes=2-5")
	h.ServeHTTP(w, r)
	if w.Code != http.StatusPartialContent || w.Body.String() != "2345" || w.Header().Get("Content-Range") != "bytes 2-5/10" {
		t.Fatalf("video status=%d headers=%v body=%q", w.Code, w.Header(), w.Body.String())
	}
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodGet, "/api/video-qc/qc-1/video", nil)
	r.Header.Set("Range", "bytes=20-30")
	h.ServeHTTP(w, r)
	if w.Code != http.StatusRequestedRangeNotSatisfiable || w.Header().Get("Content-Range") != "bytes */10" {
		t.Fatalf("unsatisfied range status=%d headers=%v body=%q", w.Code, w.Header(), w.Body.String())
	}

	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/video-qc/qc-1", nil))
	if w.Code != http.StatusNoContent || deleted != "qc-1" {
		t.Fatalf("delete status=%d body=%s deleted=%q", w.Code, w.Body.String(), deleted)
	}

	for _, rawURL := range []string{"/api/video-qc/missing/frames/1", "/api/video-qc/missing/video"} {
		w = httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, rawURL, nil))
		if w.Code != 404 || !strings.Contains(w.Body.String(), `"code":"video_qc_not_found"`) {
			t.Fatalf("url=%s status=%d body=%s", rawURL, w.Code, w.Body.String())
		}
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/video-qc/missing", nil))
	if w.Code != 404 {
		t.Fatalf("missing delete status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestVideoQCDeleteWaitsForRunningServiceDeletion(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	service := &fakeVideoQCService{delete: func(context.Context, string) error {
		close(entered)
		<-release
		return nil
	}}
	h := newVideoQCHandler(service)
	response := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/video-qc/running", nil))
		response <- w
	}()
	<-entered
	select {
	case w := <-response:
		t.Fatalf("DELETE returned before service cleanup: status=%d", w.Code)
	default:
	}
	close(release)
	w := <-response
	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

type videoQCRuntimeProvider struct{ runtime settings.Runtime }

func (p videoQCRuntimeProvider) Runtime(context.Context) (settings.Runtime, error) {
	return p.runtime, nil
}

type videoQCHTTPClientFunc func(*http.Request) (*http.Response, error)

func (f videoQCHTTPClientFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

func TestVideoQCHTTPUploadLifecycleWithRealService(t *testing.T) {
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "qc.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	var jpegData bytes.Buffer
	if err := jpeg.Encode(&jpegData, image.NewRGBA(image.Rect(0, 0, 16, 10)), nil); err != nil {
		t.Fatal(err)
	}
	modelReport := `{"verdict":"pass","score":98,"summary":"画面稳定","issues":[],"checked":["hands","body","face","product","text","artifact","watermark","quality"]}`
	modelEnvelope, err := json.Marshal(map[string]any{
		"choices": []any{map[string]any{"message": map[string]string{"content": modelReport}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := videoQCRuntimeProvider{runtime: settings.Runtime{
		PublicSettings: domain.PublicSettings{
			DataRoot:        root,
			AnalysisBaseURL: "http://model.invalid/v1",
			AnalysisModel:   "fake-vision",
			FFmpegPath:      "ffmpeg",
			FFprobePath:     "ffprobe",
		},
		AnalysisAPIKey: "test-secret",
	}}
	service := videoqc.New(
		store.NewVideoQCRepository(db),
		store.NewReferenceRepository(db),
		runtime,
		videoqc.Options{
			Inspect: func(_ context.Context, _, _, video string) (refanalysis.VideoInfo, []float64, error) {
				raw, err := os.ReadFile(video)
				if err != nil {
					return refanalysis.VideoInfo{}, nil, err
				}
				if len(raw) < 8 || string(raw[4:8]) != "ftyp" {
					return refanalysis.VideoInfo{}, nil, errors.New("saved upload is not the expected MP4")
				}
				return refanalysis.VideoInfo{DurationS: 3, Width: 320, Height: 240, FPS: 25}, nil, nil
			},
			ExtractFrame: func(context.Context, string, string, float64) ([]byte, error) {
				return append([]byte(nil), jpegData.Bytes()...), nil
			},
			HTTPClient: videoQCHTTPClientFunc(func(r *http.Request) (*http.Response, error) {
				requestBody, err := io.ReadAll(r.Body)
				if err != nil {
					return nil, err
				}
				if !bytes.Contains(requestBody, []byte("海鲜礼盒")) {
					return nil, errors.New("model request omitted product")
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(modelEnvelope))}, nil
			}),
		},
	)
	t.Cleanup(service.Close)
	h := newVideoQCHandler(service)

	mp4 := []byte{0, 0, 0, 24, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0, 0, 0, 0, 'i', 's', 'o', '2', 'm', 'p', '4', '1'}
	request := multipartVideoQCRequestWithFields(t, "复刻版_海鲜.mp4", mp4, map[string]string{"product": "海鲜礼盒", "model": "fake-vision"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request)
	if w.Code != http.StatusAccepted {
		t.Fatalf("upload status=%d body=%s", w.Code, w.Body.String())
	}
	var queued videoqc.Check
	if err := json.Unmarshal(w.Body.Bytes(), &queued); err != nil || queued.ID == "" || queued.Status != "queued" || queued.Report != nil || len(queued.Frames) != 0 {
		t.Fatalf("queued=%+v err=%v", queued, err)
	}

	var completed videoqc.Check
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		w = httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/video-qc/"+queued.ID, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("poll status=%d body=%s", w.Code, w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), &completed); err != nil {
			t.Fatal(err)
		}
		if completed.Status == "completed" {
			break
		}
		if completed.Status == "failed" {
			t.Fatalf("quality check failed: %s", completed.Error)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if completed.Status != "completed" || completed.Report == nil || completed.Report.Summary != "画面稳定" || len(completed.Frames) == 0 || completed.FrameCount != len(completed.Frames) {
		t.Fatalf("completed=%+v", completed)
	}

	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, completed.Frames[0].URL, nil))
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "private, max-age=31536000, immutable" {
		t.Fatalf("frame status=%d headers=%v body=%x", w.Code, w.Header(), w.Body.Bytes())
	}
	if _, err := jpeg.Decode(bytes.NewReader(w.Body.Bytes())); err != nil {
		t.Fatalf("frame is not a JPEG: %v", err)
	}

	w = httptest.NewRecorder()
	videoRequest := httptest.NewRequest(http.MethodGet, "/api/video-qc/"+queued.ID+"/video", nil)
	videoRequest.Header.Set("Range", "bytes=4-7")
	h.ServeHTTP(w, videoRequest)
	if w.Code != http.StatusPartialContent || w.Body.String() != "ftyp" {
		t.Fatalf("video status=%d headers=%v body=%q", w.Code, w.Header(), w.Body.String())
	}

	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/video-qc/"+queued.ID, nil))
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "video-qc", queued.ID)); !os.IsNotExist(err) {
		t.Fatalf("quality check directory remains: %v", err)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/video-qc/"+queued.ID, nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("deleted detail status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestVideoQCRejectsPathTraversalBeforeService(t *testing.T) {
	service := &fakeVideoQCService{}
	h := newVideoQCHandler(service)
	for _, rawURL := range []string{
		"/api/video-qc/%2e%2e/video",
		"/api/video-qc/qc-1%5c..%5csecret/video",
		"/api/video-qc/../secret/video",
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, rawURL, nil))
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), `"code":"unsafe_video_qc_path"`) {
			t.Fatalf("url=%s status=%d body=%s", rawURL, w.Code, w.Body.String())
		}
	}
}

func multipartVideoQCRequest(t *testing.T, filename string, content []byte) *http.Request {
	return multipartVideoQCRequestWithFields(t, filename, content, nil)
}

func multipartVideoQCRequestWithFields(t *testing.T, filename string, content []byte, fields map[string]string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	for key, value := range fields {
		if err := w.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/video-qc", &body)
	r.Header.Set("Content-Type", w.FormDataContentType())
	return r
}
