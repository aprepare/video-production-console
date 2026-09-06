package aishorts

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpeningVideoSelectionAndStorage(t *testing.T) {
	var short Short
	if err := json.Unmarshal([]byte(`{"id":"opening","mode":"explainer","visual_settings":{"layout":"portrait_full","opening_video_seconds":60},"shots":[{"index":0,"start_s":0,"end_s":4,"image_status":"done","image_path":"a.png","video_path":"a.mp4","video_status":"done"},{"index":1,"start_s":59,"end_s":64,"image_status":"done","image_path":"b.png"},{"index":2,"start_s":64,"end_s":69,"image_status":"done","image_path":"c.png"}]}`), &short); err != nil {
		t.Fatal(err)
	}
	if !short.NeedsVideo(short.Shots[0]) || !short.NeedsVideo(short.Shots[1]) || short.NeedsVideo(short.Shots[2]) {
		t.Fatal("opening selection must follow actual narration timing")
	}
	store := &Store{DataRoot: t.TempDir()}
	if err := store.Save(&short); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(short.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Shots[0].VideoPath != "a.mp4" || !got.ShotReady(got.Shots[0]) || got.ShotReady(got.Shots[1]) || !got.ShotReady(got.Shots[2]) {
		t.Fatal("opening asset readiness or persistence broken")
	}
}

func TestVideoRelayAcceptsCompressedReferenceAndPortrait(t *testing.T) {
	var frame bytes.Buffer
	if err := png.Encode(&frame, image.NewRGBA(image.Rect(0, 0, 1440, 2560))); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if r.URL.Path != "/videos/generations" || body["seconds"] != float64(6) || body["aspect_ratio"] != "9:16" || body["model"] != "grok-imagine-video-1.5" {
			t.Errorf("wrong relay contract: %s %+v", r.URL.Path, body["seconds"])
		}
		reference, ok := body["input_reference"].(map[string]any)
		if !ok || !strings.HasPrefix(reference["image_url"].(string), "data:image/jpeg;base64,") {
			t.Error("missing compressed reference")
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"request_id":"offline-video"}`))
	}))
	defer server.Close()
	client := &GenClient{BaseURL: server.URL}
	id, err := client.StartVideoAspect(context.Background(), "grok-imagine-video-1.5", "城市夜景", 6, DataURL(frame.Bytes()), "9:16")
	if err != nil || id != "offline-video" {
		t.Fatalf("submit: %s %v", id, err)
	}
}

func TestOpeningVideoBoundaryKeepsExactTimeline(t *testing.T) {
	s := &Short{Mode: ModeExplainer, VisualSettings: &VisualSettings{OpeningVideoSeconds: 60}}
	shot := Shot{StartS: 59, EndS: 64, ImagePath: "still.png", VideoPath: "motion.mp4"}
	items := explainerJobShots(s, shot, [2]float64{59, 64})
	if len(items) != 2 || items[0].Video != "motion.mp4" || items[0].StartS != 59 || items[0].EndS != 60 || items[1].Image != "still.png" || items[1].StartS != 60 || items[1].EndS != 64 {
		t.Fatalf("boundary: %+v", items)
	}
}

func TestOpeningVideoResumePreservesOldAsset(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("resume resubmitted a paid job")
		}
		if r.URL.Path == "/videos/existing-request" {
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "done", "video": map[string]string{"url": server.URL + "/fixture.mp4"}})
			return
		}
		if r.URL.Path == "/fixture.mp4" {
			_, _ = w.Write([]byte("offline video"))
			return
		}
		t.Errorf("unexpected path %s", r.URL.Path)
	}))
	defer server.Close()
	svc := NewService(t.TempDir(), nil, nil)
	dir := svc.store.AssetDir("resume")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(dir, "old.mp4")
	frame := filepath.Join(dir, "first.png")
	for path, raw := range map[string]string{old: "old video", frame: "offline reference"} {
		if err := os.WriteFile(path, []byte(raw), 0644); err != nil {
			t.Fatal(err)
		}
	}
	short := &Short{ID: "resume", Mode: ModeExplainer, VisualSettings: &VisualSettings{OpeningVideoSeconds: 60}, DraftPath: "previous-draft", Shots: []Shot{{Index: 0, StartS: 0, EndS: 4, Scene: "城市夜景", ImagePath: frame, ImageStatus: ShotDone, VideoPath: old, VideoStatus: ShotFailed, VideoRequestID: "existing-request"}}}
	if err := svc.store.Save(short); err != nil {
		t.Fatal(err)
	}
	if err := svc.generateShots(context.Background(), Runtime{VideoBaseURL: server.URL}, &GenClient{BaseURL: server.URL + "/wrong-image-route"}, short.ID, []int{0}); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(short.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.ShotReady(got.Shots[0]) || got.Shots[0].VideoPath == old || !got.DraftStale {
		t.Fatalf("resume state: %+v", got)
	}
	if raw, err := os.ReadFile(old); err != nil || string(raw) != "old video" {
		t.Fatal("previous video overwritten")
	}
}

func TestInterruptedVideoRecoveryPreservesRequest(t *testing.T) {
	svc := NewService(t.TempDir(), nil, nil)
	short := &Short{ID: "interrupted", Mode: ModeExplainer, Status: StatusGenerating, Shots: []Shot{{Index: 0, VideoStatus: ShotRunning, VideoRequestID: "paid-job", VideoPath: "previous.mp4"}}}
	if err := svc.store.Save(short); err != nil {
		t.Fatal(err)
	}
	if !svc.tryLockShot(short.ID, 0) {
		t.Fatal("lock")
	}
	active, err := svc.Get(short.ID)
	if err != nil || active.Status != StatusGenerating {
		t.Fatal("active task incorrectly recovered", err)
	}
	svc.unlockShot(short.ID, 0)
	got, err := svc.Get(short.ID)
	if err != nil || got.Status != StatusFailed || got.Shots[0].VideoStatus != ShotFailed || got.Shots[0].VideoRequestID != "paid-job" || got.Shots[0].VideoPath != "previous.mp4" {
		t.Fatalf("recovery: %+v %v", got, err)
	}
}

func TestUncertainVideoSubmissionDoesNotAutomaticallyResubmit(t *testing.T) {
	svc := NewService(t.TempDir(), nil, nil)
	dir := svc.store.AssetDir("uncertain")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	frame := filepath.Join(dir, "first.jpg")
	if err := os.WriteFile(frame, []byte("fixture"), 0644); err != nil {
		t.Fatal(err)
	}
	short := &Short{ID: "uncertain", Mode: ModeExplainer, VisualSettings: &VisualSettings{OpeningVideoSeconds: 60}, Shots: []Shot{{Index: 0, StartS: 0, EndS: 4, ImagePath: frame, ImageStatus: ShotDone, VideoSubmitUncertain: true}}}
	if err := svc.store.Save(short); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("uncertain submission retried automatically") }))
	defer server.Close()
	svc.generateOneShot(context.Background(), Runtime{}, &GenClient{BaseURL: server.URL}, short.ID, dir, 0, short.Shots[0], short, nil, nil)
	got, err := svc.Get(short.ID)
	if err != nil || !got.Shots[0].VideoSubmitUncertain || got.Shots[0].VideoStatus != ShotFailed {
		t.Fatal("uncertain state lost", err)
	}
}

func TestEnablingOpeningVideoRequiresAssetsWithoutDeletingDraft(t *testing.T) {
	settings := DefaultVisualSettings()
	short := &Short{Mode: ModeExplainer, Status: StatusAssembled, VisualSettings: settings, DraftPath: "previous-draft", Shots: []Shot{{Index: 0, StartS: 0, EndS: 4, ImagePath: "first.jpg", ImageStatus: ShotDone}}}
	next := *settings
	next.OpeningVideoSeconds = 60
	if err := applyVisualSettings(short, &next); err != nil {
		t.Fatal(err)
	}
	if short.Status != StatusStoryboard || !short.DraftStale || short.DraftPath != "previous-draft" {
		t.Fatalf("settings: %+v", short)
	}
}
