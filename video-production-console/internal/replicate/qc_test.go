package replicate

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"video-production-console/internal/refanalysis"
	"video-production-console/internal/settings"
	"video-production-console/internal/store"
	"video-production-console/internal/videoqc"
)

type qcDoer func(*http.Request) (*http.Response, error)

func (d qcDoer) Do(r *http.Request) (*http.Response, error) { return d(r) }
func TestQCProjectAndTakeSources(t *testing.T) {
	s, repo, root := lifecycleFixture(t, Options{}, func(rt *settings.Runtime) {
		rt.AnalysisBaseURL = "http://fake.invalid/v1"
		rt.AnalysisAPIKey = "fake"
		rt.AnalysisModel = "fake"
	})
	db, e := store.Open(filepath.Join(root, "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	var jpg bytes.Buffer
	jpeg.Encode(&jpg, image.NewRGBA(image.Rect(0, 0, 16, 16)), nil)
	qc := videoqc.New(store.NewVideoQCRepository(db), nil, s.runtime, videoqc.Options{ReplicateSource: s.QCSource, Inspect: func(context.Context, string, string, string) (refanalysis.VideoInfo, []float64, error) {
		return refanalysis.VideoInfo{DurationS: 2, Width: 320, Height: 240, FPS: 30}, nil, nil
	}, ExtractFrame: func(context.Context, string, string, float64) ([]byte, error) { return jpg.Bytes(), nil }, HTTPClient: qcDoer(func(r *http.Request) (*http.Response, error) {
		report := `{"verdict":"warn","score":80,"summary":"细节异常","issues":[{"frame_index":2,"time_s":999,"category":"text","severity":"low","description":"文字","suggestion":"重做","shot_position":99}],"checked":["hands","body","face","product","text","artifact","watermark","quality"]}`
		envelope, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": report}}}})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(envelope))}, nil
	})})
	defer qc.Close()
	s.SetQC(qc)
	p, e := s.Create(t.Context(), CreateRequest{Blank: true, VideoProvider: "grok"})
	if e != nil {
		t.Fatal(e)
	}
	p = addReady(t, s, p.ID)
	tid := seedTake(t, s, repo, p.ID, p.Shots[0].ID, []byte("video"), 2)
	stored, e := repo.Get(t.Context(), p.ID)
	if e != nil {
		t.Fatal(e)
	}
	stored.Status = "stitched"
	stored.FinalPath = filepath.Join(p.ID, "final.mp4")
	stored.FinalDurationS = 2
	if e = s.write(stored.FinalPath, []byte("video")); e != nil {
		t.Fatal(e)
	}
	if e = s.write(filepath.Join(p.ID, "timeline.json"), []byte(`[{"position":1,"start_s":0,"end_s":0.5},{"position":2,"start_s":0.5,"end_s":2}]`)); e != nil {
		t.Fatal(e)
	}
	if e = repo.SaveProject(t.Context(), stored); e != nil {
		t.Fatal(e)
	}
	for _, source := range []string{"replicate", "replicate_take"} {
		var c videoqc.Check
		if source == "replicate" {
			c, e = s.StartQC(t.Context(), p.ID, "")
		} else {
			c, e = qc.Start(t.Context(), videoqc.Input{Source: source, SourceID: tid})
		}
		if e != nil {
			t.Fatal(e)
		}
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			c, e = qc.Get(t.Context(), c.ID)
			if e != nil {
				t.Fatal(e)
			}
			if c.Status == "completed" || c.Status == "failed" {
				break
			}
			time.Sleep(time.Millisecond)
		}
		if c.Status != "completed" {
			t.Fatalf("%+v", c)
		}
		want := 0
		if source == "replicate" {
			want = 2
			got, _ := s.Get(t.Context(), p.ID)
			if got.QCID != c.ID {
				t.Fatalf("qc id missing: %+v", got)
			}
		}
		if c.Report.Issues[0].ShotPosition != want || c.Report.Issues[0].TimeS != 1 {
			t.Fatalf("%s: %+v", source, c.Report)
		}
	}
}
func TestQCPreflightErrorsAreReplicateErrors(t *testing.T) {
	s, _, _ := lifecycleFixture(t, Options{}, nil)
	_, e := s.StartQC(t.Context(), "missing", "")
	if errorCode(e) != "replicate_not_found" {
		t.Fatalf("missing project %v", e)
	}
	p, e := s.Create(t.Context(), CreateRequest{Blank: true})
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.StartQC(t.Context(), p.ID, "")
	if errorCode(e) != "invalid_replicate" || !strings.Contains(e.Error(), "拼接") {
		t.Fatalf("unstitched: %v", e)
	}
}
