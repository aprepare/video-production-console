package aigc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"video-production-console/internal/domain"
	consoleSettings "video-production-console/internal/settings"
	"video-production-console/internal/store"
)

func TestRestyleConcurrencyAndURLDownloadLimit(t *testing.T) {
	s, _, merchantID := fixture(t)
	ids := []string{}
	for i := 0; i < 4; i++ {
		ids = append(ids, material(t, s, merchantID, "image", fmt.Sprintf("%d.png", i)).ID)
	}
	var active, maxActive atomic.Int32
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if r.Header.Get("Authorization") != "" {
				t.Error("download received API key")
			}
			if strings.HasSuffix(r.URL.Path, "oversize") {
				block := strings.Repeat("x", 1<<20)
				for i := 0; i < 31; i++ {
					if _, err := io.WriteString(w, block); err != nil {
						return
					}
				}
				return
			}
			io.WriteString(w, "downloaded png")
			return
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
			return
		}
		defer r.MultipartForm.RemoveAll()
		n := active.Add(1)
		defer active.Add(-1)
		for old := maxActive.Load(); n > old && !maxActive.CompareAndSwap(old, n); old = maxActive.Load() {
		}
		entered <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		f, header, err := r.FormFile("image")
		if err != nil {
			t.Error(err)
			return
		}
		f.Close()
		path := "/download"
		if header.Filename == "3.png" {
			path = "/oversize"
		}
		fmt.Fprintf(w, `{"data":[{"url":%q}]}`, server.URL+path)
	}))
	defer server.Close()
	s.HTTPClient = server.Client()
	s.runtime = runtimeStub{consoleSettings.Runtime{PublicSettings: domain.PublicSettings{ImageBaseURL: server.URL, ImageModel: "fake"}, ImageAPIKey: "fake"}}
	job, err := s.Restyle(context.Background(), merchantID, ids, "暖光", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			close(release)
			t.Fatal("two edits did not start concurrently")
		}
	}
	select {
	case <-entered:
		t.Error("more than two edits started")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	job = waitJob(t, s, job.ID)
	if job.Status != "completed" || len(job.OutputMaterialIDs) != 3 || maxActive.Load() != 2 || !strings.Contains(job.Error, ids[3]) {
		t.Fatalf("job=%+v max=%d", job, maxActive.Load())
	}
}

func TestVideoFailureRedactsBeforeTailAndKeepsConfirmationBoundary(t *testing.T) {
	s, _, merchantID := fixture(t)
	m := material(t, s, merchantID, "image", "source.png")
	job, err := s.PrepareImageToVideo(context.Background(), merchantID, []string{m.ID}, "镜头推进", 3, "1:1")
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("SecretToken", 60)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/openapi/v2/media/upload/binary" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintf(w, `{"code":"AUTH_FAILED","msg":%q}`, token+" tail")
	}))
	defer server.Close()
	s.HTTPClient = routedTLSTestClient(server)
	s.runtime = runtimeStub{consoleSettings.Runtime{RunningHubAPIKeys: token}}
	if _, err = s.ConfirmImageToVideo(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	job = waitJob(t, s, job.ID)
	if job.Status != "failed" || !strings.Contains(job.Error, "auth_failed") || strings.Contains(job.Error, "SecretToken") {
		t.Fatalf("%+v", job)
	}
	prepared, err := s.PrepareImageToVideo(context.Background(), merchantID, []string{m.ID}, "镜头推进", 3, "1:1")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Resume(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetJob(context.Background(), prepared.ID)
	if err != nil || got.Status != "awaiting_confirm" {
		t.Fatalf("%+v %v", got, err)
	}
	var p videoParams
	_ = json.Unmarshal([]byte(prepared.ParamsJSON), &p)
	shots := []shot{{Name: "shot-01", Image: "../../outside.png", Duration: 3, Prompt: "镜头推进"}}
	raw, _ := json.Marshal(shots)
	if err = os.WriteFile(p.ShotsPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ConfirmImageToVideo(context.Background(), prepared.ID); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("manifest escape: %v", err)
	}
}

func TestLineageDepthAndInputBounds(t *testing.T) {
	s, _, merchantID := fixture(t)
	m := material(t, s, merchantID, "image", "source.png")
	for i := 0; i < 11; i++ {
		derived, err := s.merchants.CreateDerivedMaterial(context.Background(), store.MerchantMaterial{MerchantID: merchantID, Kind: "image", Filename: "derived.png", Path: m.Path}, m.ID, "restyle", nil, "fake")
		if err != nil {
			t.Fatal(err)
		}
		m = derived
	}
	chain, err := s.Lineage(context.Background(), m.ID)
	if err != nil || len(chain) != 10 || chain[9].ID != m.ID {
		t.Fatalf("%+v %v", chain, err)
	}
	if _, _, err = s.ExtractFrames(context.Background(), merchantID, m.ID, 0); !errors.Is(err, ErrInvalidInput) {
		t.Fatal(err)
	}
	if _, _, err = s.ExtractFrames(context.Background(), merchantID, m.ID, 1); !errors.Is(err, ErrInvalidInput) {
		t.Fatal(err)
	}
	if _, err = s.Restyle(context.Background(), merchantID, []string{m.ID, m.ID}, "暖光", ""); !errors.Is(err, ErrInvalidInput) {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		duration int
		aspect   string
	}{{2, "9:16"}, {11, "9:16"}, {5, "4:3"}} {
		if _, err = s.PrepareImageToVideo(context.Background(), merchantID, []string{m.ID}, "推进", tc.duration, tc.aspect); !errors.Is(err, ErrInvalidInput) {
			t.Fatal(err)
		}
	}
	foreign, err := s.merchants.Create(context.Background(), store.Merchant{Name: "其他商户"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Restyle(context.Background(), foreign.ID, []string{m.ID}, "暖光", ""); !errors.Is(err, ErrInvalidInput) {
		t.Fatal(err)
	}
}

func TestImageToVideoRequiresRunningHubKeysBeforeClaimingJob(t *testing.T) {
	s, _, merchantID := fixture(t)
	m := material(t, s, merchantID, "image", "source.png")
	job, err := s.PrepareImageToVideo(context.Background(), merchantID, []string{m.ID}, "镜头推进", 5, "16:9")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ConfirmImageToVideo(context.Background(), job.ID); err == nil || !strings.Contains(err.Error(), "视频接口暂不可用") {
		t.Fatalf("missing key error=%v", err)
	}
	job, err = s.GetJob(context.Background(), job.ID)
	if err != nil || job.Status != "awaiting_confirm" {
		t.Fatalf("job=%+v err=%v", job, err)
	}
}

func TestSafeMaterialPathRejectsLinkedMerchantDirectory(t *testing.T) {
	s, _, merchantID := fixture(t)
	root := filepath.Join(s.dataRoot, "merchants")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, merchantID)); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := s.safeMaterialPath(merchantID, filepath.Join(root, merchantID, "materials", "file.png")); !errors.Is(err, ErrUnsafePath) {
		t.Fatal(err)
	}
}
