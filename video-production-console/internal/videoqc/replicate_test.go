package videoqc

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReplicateSourceSnapshotAndMapping(t *testing.T) {
	original := filepath.Join(t.TempDir(), "final.mp4")
	if e := os.WriteFile(original, []byte("completed-video"), 0600); e != nil {
		t.Fatal(e)
	}
	recorded := ""
	s, _, _, _ := serviceFixture(t, func(o *Options) {
		o.ReplicateSource = func(ctx context.Context, source, id string) (ReplicateMedia, error) {
			f, e := os.Open(original)
			return ReplicateMedia{File: f, Title: "复刻", Spans: []ShotRange{{Position: 1, StartS: 0, EndS: 1}, {Position: 2, StartS: 1, EndS: 3}}, RecordQC: func(ctx context.Context, id string) error { recorded = id; return nil }}, e
		}
		o.HTTPClient = doer(func(r *http.Request) (*http.Response, error) {
			raw := `{"verdict":"warn","score":80,"summary":"手部异常","issues":[{"frame_index":2,"time_s":999,"category":"hands","severity":"low","description":"手指异常","suggestion":"重做","shot_position":99}],"checked":["hands","body","face","product","text","artifact","watermark","quality"]}`
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(qcEnvelope(t, raw)))}, nil
		})
	})
	c, e := s.Start(t.Context(), Input{Source: "replicate", SourceID: "project"})
	if e != nil {
		t.Fatal(e)
	}
	if recorded != c.ID {
		t.Fatalf("recorded %q", recorded)
	}
	os.Remove(original)
	waitCheck(t, s.repo, c.ID, "completed")
	c, e = s.Get(t.Context(), c.ID)
	if e != nil {
		t.Fatal(e)
	}
	if c.Report.Issues[0].ShotPosition != 2 || c.Report.Issues[0].TimeS != 1 {
		t.Fatalf("%+v", c.Report)
	}
	f, e := s.OpenVideo(t.Context(), c.ID)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	raw, _ := io.ReadAll(f)
	if string(raw) != "completed-video" {
		t.Fatal("source was not snapshotted")
	}
}
