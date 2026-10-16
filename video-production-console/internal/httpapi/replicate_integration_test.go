package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"video-production-console/internal/domain"
	"video-production-console/internal/grokvideo"
	"video-production-console/internal/imageproject"
	"video-production-console/internal/refanalysis"
	"video-production-console/internal/replicate"
	"video-production-console/internal/settings"
	"video-production-console/internal/store"
)

type replicateIntegrationRuntime struct{ value settings.Runtime }

func (r replicateIntegrationRuntime) Runtime(context.Context) (settings.Runtime, error) {
	return r.value, nil
}

type replicateIntegrationImages struct{}

func (replicateIntegrationImages) GenerateMany(context.Context, imageproject.GenerateRequest) ([]imageproject.GenerateResult, error) {
	return nil, nil
}

type replicateIntegrationGrok struct {
	mu       sync.Mutex
	submits  []grokvideo.SubmitRequest
	download []byte
}

func (g *replicateIntegrationGrok) Submit(_ context.Context, request grokvideo.SubmitRequest) (grokvideo.Request, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.submits = append(g.submits, request)
	return grokvideo.Request{ID: "fake-grok-task", Status: "submitted"}, nil
}

func (*replicateIntegrationGrok) Poll(context.Context, string) (grokvideo.Status, error) {
	return grokvideo.Status{RequestID: "fake-grok-task", State: grokvideo.StateDone, VideoURL: "https://fake.invalid/video.mp4", DurationSeconds: 6}, nil
}

func (g *replicateIntegrationGrok) Download(_ context.Context, _ string, destination io.Writer) error {
	_, err := destination.Write(g.download)
	return err
}

func (g *replicateIntegrationGrok) submitted() []grokvideo.SubmitRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]grokvideo.SubmitRequest(nil), g.submits...)
}

func TestReplicateHTTPRealServiceLifecycle(t *testing.T) {
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	fakeVideo := []byte("0123456789abcdef")
	grok := &replicateIntegrationGrok{download: fakeVideo}
	runtime := replicateIntegrationRuntime{value: settings.Runtime{
		PublicSettings: domain.PublicSettings{DataRoot: root, ReplicateDefaultProvider: "grok", ReplicateDefaultVariants: 1},
		GrokAPIKey:     "fake-key-never-sent",
	}}
	service, err := replicate.New(
		store.NewReplicateRepository(db),
		store.NewReferenceAnalysisRepository(db),
		runtime,
		replicate.Options{
			Images: replicateIntegrationImages{},
			Grok: func(settings.Runtime) (grokvideo.Client, error) {
				return grok, nil
			},
			Probe: func(context.Context, string, string) (refanalysis.VideoInfo, error) {
				return refanalysis.VideoInfo{DurationS: 6, Width: 720, Height: 1280}, nil
			},
			PollInterval: time.Millisecond,
			TakeTimeout:  3 * time.Second,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Close)
	handler := NewReplicateHandler(service)

	created := replicate.Project{}
	replicateIntegrationJSON(t, handler, http.MethodPost, "/api/replicate", `{"blank":true,"title":"HTTP 闭环","ratio":"9:16","video_provider":"grok","variants":1}`, http.StatusCreated, &created)
	if created.ID == "" || created.Title != "HTTP 闭环" || created.VideoProvider != "grok" || len(created.Shots) != 0 {
		t.Fatalf("created project = %+v", created)
	}

	added := replicate.Project{}
	replicateIntegrationJSON(t, handler, http.MethodPost, "/api/replicate/"+created.ID+"/shots", `{"purpose":"钩子","duration_s":6,"first_frame_prompt":"红色礼盒特写","motion_prompt":"镜头缓慢推进","negative":"乱码","keep":"红色包装"}`, http.StatusOK, &added)
	if len(added.Shots) != 1 || added.Shots[0].ID == "" || added.Shots[0].FirstFrameStatus != "none" {
		t.Fatalf("project after add shot = %+v", added)
	}
	shotID := added.Shots[0].ID

	imageBytes := replicateIntegrationPNG(t)
	var upload bytes.Buffer
	multipartWriter := multipart.NewWriter(&upload)
	part, err := multipartWriter.CreateFormFile("file", "用户首帧.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(imageBytes); err != nil {
		t.Fatal(err)
	}
	if err = multipartWriter.Close(); err != nil {
		t.Fatal(err)
	}
	uploadRequest := httptest.NewRequest(http.MethodPost, "/api/replicate/shots/"+shotID+"/first-frame", bytes.NewReader(upload.Bytes()))
	uploadRequest.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	uploadResponse := httptest.NewRecorder()
	handler.ServeHTTP(uploadResponse, uploadRequest)
	if uploadResponse.Code != http.StatusOK {
		t.Fatalf("upload status=%d body=%s", uploadResponse.Code, uploadResponse.Body.String())
	}
	uploaded := replicate.Project{}
	if err = json.Unmarshal(uploadResponse.Body.Bytes(), &uploaded); err != nil {
		t.Fatal(err)
	}
	if len(uploaded.Shots) != 1 || uploaded.Shots[0].FirstFrameStatus != "ready" || uploaded.Shots[0].FirstFrameSource != "uploaded" || uploaded.Shots[0].FirstFrameURL == "" {
		t.Fatalf("project after upload = %+v", uploaded)
	}

	generated := replicate.GenerateResponse{}
	replicateIntegrationJSON(t, handler, http.MethodPost, "/api/replicate/"+created.ID+"/generate", `{"shot_ids":["`+shotID+`"],"variants":1,"provider":"grok"}`, http.StatusAccepted, &generated)
	if len(generated.Skipped) != 0 || generated.Project.Progress.TakesTotal != 1 {
		t.Fatalf("generate response = %+v", generated)
	}

	var ready replicate.Project
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		ready = replicate.Project{}
		replicateIntegrationJSON(t, handler, http.MethodGet, "/api/replicate/"+created.ID, "", http.StatusOK, &ready)
		if ready.Status == "ready" && ready.Progress.TakesDone == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if ready.Status != "ready" || len(ready.Shots) != 1 || len(ready.Shots[0].Takes) != 1 {
		t.Fatalf("project never became ready: %+v", ready)
	}
	take := ready.Shots[0].Takes[0]
	if take.Status != "succeeded" || take.URL == "" || take.Width != 720 || take.Height != 1280 || take.DurationS != 6 {
		t.Fatalf("completed take = %+v", take)
	}
	if requests := grok.submitted(); len(requests) != 1 || requests[0].Seconds != 6 || requests[0].ImageMIMEType != "image/png" || len(requests[0].Image) == 0 || !strings.Contains(requests[0].Prompt, "镜头缓慢推进") {
		t.Fatalf("fake Grok submissions = %+v", requests)
	}

	selected := replicate.Project{}
	replicateIntegrationJSON(t, handler, http.MethodPost, "/api/replicate/takes/"+take.ID+"/select", "", http.StatusOK, &selected)
	if selected.Shots[0].SelectedTakeID != take.ID {
		t.Fatalf("selected take id = %q, want %q", selected.Shots[0].SelectedTakeID, take.ID)
	}

	rangeRequest := httptest.NewRequest(http.MethodGet, "/api/replicate/takes/"+take.ID+"/video", nil)
	rangeRequest.Header.Set("Range", "bytes=3-7")
	rangeResponse := httptest.NewRecorder()
	handler.ServeHTTP(rangeResponse, rangeRequest)
	if rangeResponse.Code != http.StatusPartialContent || rangeResponse.Body.String() != string(fakeVideo[3:8]) || rangeResponse.Header().Get("Content-Range") != "bytes 3-7/16" {
		t.Fatalf("range status=%d content-range=%q body=%q", rangeResponse.Code, rangeResponse.Header().Get("Content-Range"), rangeResponse.Body.String())
	}

	deleteResponse := replicateIntegrationRequest(handler, http.MethodDelete, "/api/replicate/"+created.ID, "")
	if deleteResponse.Code != http.StatusNoContent || deleteResponse.Body.Len() != 0 {
		t.Fatalf("delete status=%d body=%s", deleteResponse.Code, deleteResponse.Body.String())
	}
	missing := replicateIntegrationRequest(handler, http.MethodGet, "/api/replicate/"+created.ID, "")
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), `"code":"replicate_not_found"`) {
		t.Fatalf("get deleted status=%d body=%s", missing.Code, missing.Body.String())
	}
	if _, err = os.Stat(filepath.Join(root, "replicate", created.ID)); !os.IsNotExist(err) {
		t.Fatalf("deleted project directory still exists or stat failed unexpectedly: %v", err)
	}
}

func replicateIntegrationJSON(t *testing.T, handler http.Handler, method, target, body string, wantStatus int, output any) {
	t.Helper()
	response := replicateIntegrationRequest(handler, method, target, body)
	if response.Code != wantStatus {
		t.Fatalf("%s %s status=%d want=%d body=%s", method, target, response.Code, wantStatus, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), output); err != nil {
		t.Fatalf("%s %s decode response: %v; body=%s", method, target, err, response.Body.String())
	}
}

func replicateIntegrationRequest(handler http.Handler, method, target, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func replicateIntegrationPNG(t *testing.T) []byte {
	t.Helper()
	canvas := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			canvas.Set(x, y, color.RGBA{R: 180, G: uint8(x * 8), B: uint8(y * 8), A: 255})
		}
	}
	var output bytes.Buffer
	if err := png.Encode(&output, canvas); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
