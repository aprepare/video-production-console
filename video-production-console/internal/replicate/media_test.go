package replicate

import (
	"context"
	"github.com/google/uuid"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
	"video-production-console/internal/store"
)

func seedTake(t *testing.T, s *Service, repo *store.ReplicateRepository, pid, sid string, b []byte, d float64) string {
	t.Helper()
	id := uuid.NewString()
	take := store.ReplicateTake{ID: id, ShotID: sid, Provider: "grok", Prompt: "test", Status: "queued", KeySlot: -1}
	if e := repo.CreateTakes(t.Context(), []store.ReplicateTake{take}); e != nil {
		t.Fatal(e)
	}
	take.Status = "succeeded"
	take.Path = filepath.Join(pid, "takes", id+".mp4")
	take.DurationS = d
	take.Width = 320
	take.Height = 240
	if e := s.write(take.Path, b); e != nil {
		t.Fatal(e)
	}
	if e := repo.SaveTake(t.Context(), take); e != nil {
		t.Fatal(e)
	}
	return id
}
func TestStitchRealMedia(t *testing.T) {
	ff, e := exec.LookPath("ffmpeg")
	if e != nil {
		t.Skip("ffmpeg unavailable")
	}
	fp, e := exec.LookPath("ffprobe")
	if e != nil {
		t.Skip("ffprobe unavailable")
	}
	s, repo := fixture(t, nil)
	rt := s.runtime.(testRuntime)
	rt.rt.FFmpegPath = ff
	rt.rt.FFprobePath = fp
	s.runtime = rt
	s.opt.Probe = probeVideo
	p, e := s.Create(t.Context(), CreateRequest{Blank: true, Title: "拼接", VideoProvider: "grok", Ratio: "16:9"})
	if e != nil {
		t.Fatal(e)
	}
	for _, size := range []string{"160x120", "240x320", "320x180"} {
		p = addReady(t, s, p.ID)
		sh := p.Shots[len(p.Shots)-1]
		path := filepath.Join(t.TempDir(), "part.mp4")
		cmd := exec.Command(ff, "-v", "error", "-f", "lavfi", "-i", "testsrc=size="+size+":rate=24", "-t", "0.4", "-pix_fmt", "yuv420p", "-c:v", "libx264", "-y", path)
		if raw, e := cmd.CombinedOutput(); e != nil {
			t.Fatalf("fixture: %v %s", e, raw)
		}
		b, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		seedTake(t, s, repo, p.ID, sh.ID, b, .4)
	}
	_, e = s.Stitch(t.Context(), p.ID, "none")
	if e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		p, e = s.Get(t.Context(), p.ID)
		if e != nil {
			t.Fatal(e)
		}
		if p.Status == "stitched" || p.Status == "failed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if p.Status != "stitched" {
		t.Fatalf("%+v", p)
	}
	f, e := s.OpenFinal(t.Context(), p.ID)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	info, e := probeVideo(context.Background(), fp, f.Name())
	if e != nil || info.Width != 1280 || info.Height != 720 || math.Abs(info.DurationS-1.2) > .16 {
		t.Fatalf("%+v %v", info, e)
	}
}
