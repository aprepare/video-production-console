package replicate

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/domain"
	"video-production-console/internal/grokvideo"
	"video-production-console/internal/imageproject"
	"video-production-console/internal/refanalysis"
	"video-production-console/internal/settings"
	"video-production-console/internal/store"
)

type mutableRuntime struct {
	mu sync.RWMutex
	rt settings.Runtime
}

func (r *mutableRuntime) Runtime(context.Context) (settings.Runtime, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.rt, nil
}

func lifecycleFixture(t *testing.T, opt Options, configure func(*settings.Runtime)) (*Service, *store.ReplicateRepository, string) {
	t.Helper()
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	rt := settings.Runtime{PublicSettings: domain.PublicSettings{DataRoot: root}, GrokAPIKey: "fake"}
	if configure != nil {
		configure(&rt)
	}
	repo := store.NewReplicateRepository(db)
	svc, err := New(repo, store.NewReferenceAnalysisRepository(db), &mutableRuntime{rt: rt}, opt)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	return svc, repo, root
}

func pngBytes(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := png.Encode(&out, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func errorCode(err error) string {
	var apiErr *Error
	if errors.As(err, &apiErr) {
		return apiErr.Code
	}
	return ""
}

func TestBlankProjectPatchAndShotLifecycle(t *testing.T) {
	svc, _, _ := lifecycleFixture(t, Options{}, nil)
	p, err := svc.Create(t.Context(), CreateRequest{Blank: true, Title: "", Ratio: "9:16", VideoProvider: "grok"})
	if err != nil || p.Title != "未命名复刻项目" || p.Status != "draft" || len(p.Shots) != 0 {
		t.Fatalf("blank=%+v err=%v", p, err)
	}
	if _, err = svc.Patch(t.Context(), p.ID, ProjectPatch{Ratio: ptr("2:3")}); errorCode(err) != "invalid_replicate" {
		t.Fatalf("invalid ratio err=%v", err)
	}
	p, err = svc.Patch(t.Context(), p.ID, ProjectPatch{Title: ptr("新品"), Product: ptr("蓝色礼盒"), Variants: ptr(3)})
	if err != nil || p.Title != "新品" || p.Product != "蓝色礼盒" || p.Variants != 3 {
		t.Fatalf("patch=%+v err=%v", p, err)
	}
	p, err = svc.AddShot(t.Context(), p.ID, ShotPatch{Purpose: ptr("开头"), DurationS: ptr(6)})
	if err != nil {
		t.Fatal(err)
	}
	p, err = svc.AddShot(t.Context(), p.ID, ShotPatch{Purpose: ptr("结尾"), DurationS: ptr(10)})
	if err != nil {
		t.Fatal(err)
	}
	p, err = svc.AddShot(t.Context(), p.ID, ShotPatch{Purpose: ptr("中段"), DurationS: ptr(6), AfterPosition: ptr(1)})
	if err != nil || len(p.Shots) != 3 || p.Shots[1].Purpose != "中段" {
		t.Fatalf("insert=%+v err=%v", p.Shots, err)
	}
	ids := []string{p.Shots[2].ID, p.Shots[0].ID, p.Shots[1].ID}
	p, err = svc.Reorder(t.Context(), p.ID, ids)
	if err != nil || p.Shots[0].ID != ids[0] || p.Shots[1].Position != 2 {
		t.Fatalf("reorder=%+v err=%v", p.Shots, err)
	}
	if err = svc.DeleteShot(t.Context(), ids[1]); err != nil {
		t.Fatal(err)
	}
	p, err = svc.Get(t.Context(), p.ID)
	if err != nil || len(p.Shots) != 2 || p.Shots[0].Position != 1 || p.Shots[1].Position != 2 {
		t.Fatalf("delete shot=%+v err=%v", p.Shots, err)
	}
}

func TestSelectTakeDeleteSelectionAndRejectStitchWithoutSelection(t *testing.T) {
	svc, repo, _ := lifecycleFixture(t, Options{}, nil)
	p, err := svc.Create(t.Context(), CreateRequest{Blank: true, Title: "选版", VideoProvider: "grok"})
	if err != nil {
		t.Fatal(err)
	}
	p = addReady(t, svc, p.ID)
	shotID := p.Shots[0].ID
	first := seedTake(t, svc, repo, p.ID, shotID, []byte("video-one"), 6)
	second := seedTake(t, svc, repo, p.ID, shotID, []byte("video-two"), 6)
	p, err = svc.SelectTake(t.Context(), second)
	if err != nil || p.Shots[0].SelectedTakeID != second || first == second {
		t.Fatalf("select=%+v err=%v", p.Shots[0], err)
	}
	if err = svc.DeleteTake(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	p, err = svc.Get(t.Context(), p.ID)
	if err != nil || p.Shots[0].SelectedTakeID != "" {
		t.Fatalf("selected take not cleared: %+v err=%v", p.Shots[0], err)
	}
	if _, err = svc.Stitch(t.Context(), p.ID, "none"); errorCode(err) != "stitch_missing_takes" {
		t.Fatalf("stitch without selection err=%v", err)
	}
}

func TestFirstFrameUploadRejectsDisguisedOversizeAndUnsafeStoredPath(t *testing.T) {
	svc, repo, _ := lifecycleFixture(t, Options{}, nil)
	p, err := svc.Create(t.Context(), CreateRequest{Blank: true, Title: "上传", VideoProvider: "grok"})
	if err != nil {
		t.Fatal(err)
	}
	p, err = svc.AddShot(t.Context(), p.ID, ShotPatch{})
	if err != nil {
		t.Fatal(err)
	}
	shotID := p.Shots[0].ID
	if _, err = svc.UploadFirstFrame(t.Context(), shotID, "fake.png", strings.NewReader("not a png")); errorCode(err) != "invalid_replicate" {
		t.Fatalf("disguised image err=%v", err)
	}
	if _, err = svc.UploadFirstFrame(t.Context(), shotID, "huge.png", bytes.NewReader(make([]byte, MaxImageBytes+1))); errorCode(err) != "invalid_replicate" {
		t.Fatalf("oversize image err=%v", err)
	}
	shot, err := repo.GetShot(t.Context(), shotID)
	if err != nil {
		t.Fatal(err)
	}
	shot.FirstFramePath = filepath.Join("..", "escape.png")
	shot.FirstFrameStatus = "ready"
	if err = repo.SaveShot(t.Context(), shot); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.OpenFirstFrame(t.Context(), shotID); err == nil {
		t.Fatal("unsafe persisted path was opened")
	}
}

func TestReferenceFrameCopiesSourceIntoProject(t *testing.T) {
	sourceDir := t.TempDir()
	source := filepath.Join(sourceDir, "frame.png")
	if err := os.WriteFile(source, pngBytes(t), 0o600); err != nil {
		t.Fatal(err)
	}
	opt := Options{ReferenceFrame: func(_ context.Context, analysisID string, index int) (*os.File, error) {
		if analysisID != "analysis" || index != 3 {
			return nil, os.ErrNotExist
		}
		return os.Open(source)
	}}
	svc, repo, _ := lifecycleFixture(t, opt, nil)
	now := time.Now().UTC()
	p := store.ReplicateProject{ID: uuid.NewString(), Title: "关键帧", AnalysisID: "analysis", Ratio: "9:16", VideoProvider: "grok", Variants: 1, Status: "draft", CreatedAt: now, UpdatedAt: now}
	shot := store.ReplicateShot{ID: uuid.NewString(), ProjectID: p.ID, Position: 1, DurationS: 6, FirstFrameStatus: "none", CreatedAt: now, UpdatedAt: now}
	if err := repo.Create(t.Context(), p, []store.ReplicateShot{shot}); err != nil {
		t.Fatal(err)
	}
	view, err := svc.UseReferenceFrame(t.Context(), shot.ID, 3)
	if err != nil || view.Shots[0].FirstFrameSource != "reference_frame" || view.Shots[0].FirstFrameStatus != "ready" {
		t.Fatalf("reference frame=%+v err=%v", view, err)
	}
	f, err := svc.OpenFirstFrame(t.Context(), shot.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got := make([]byte, len(pngBytes(t)))
	n, err := f.Read(got)
	if err != nil || n != len(got) || !bytes.Equal(got, pngBytes(t)) {
		t.Fatalf("copied bytes n=%d err=%v", n, err)
	}
}

type blockedImages struct {
	mu       sync.Mutex
	active   int
	maximum  int
	requests []imageproject.GenerateRequest
	release  chan struct{}
	image    []byte
}

func (f *blockedImages) GenerateMany(ctx context.Context, req imageproject.GenerateRequest) ([]imageproject.GenerateResult, error) {
	f.mu.Lock()
	f.active++
	if f.active > f.maximum {
		f.maximum = f.active
	}
	f.requests = append(f.requests, req)
	f.mu.Unlock()
	select {
	case <-f.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	f.mu.Lock()
	f.active--
	f.mu.Unlock()
	return []imageproject.GenerateResult{{Bytes: f.image, MIMEType: "image/png", Width: 8, Height: 8}}, nil
}

func TestGenerateFirstFramesHonorsConcurrencyModelAndPrompt(t *testing.T) {
	fake := &blockedImages{release: make(chan struct{}), image: pngBytes(t)}
	svc, _, _ := lifecycleFixture(t, Options{Images: fake}, func(rt *settings.Runtime) {
		rt.ImageAPIKey = "fake-image-key"
		rt.ImageBaseURL = "https://image.invalid"
		rt.ImageModel = "fallback-model"
		rt.MaxImageConcurrency = 2
	})
	p, err := svc.Create(t.Context(), CreateRequest{Blank: true, Title: "首帧", VideoProvider: "grok", ImageModel: "project-model"})
	if err != nil {
		t.Fatal(err)
	}
	p, err = svc.Patch(t.Context(), p.ID, ProjectPatch{StyleLock: ptr("暖色自然光")})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		p, err = svc.AddShot(t.Context(), p.ID, ShotPatch{FirstFramePrompt: ptr("礼盒特写"), Keep: ptr("蓝色包装")})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err = svc.GenerateFirstFrames(t.Context(), p.ID, nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		fake.mu.Lock()
		max, calls := fake.maximum, len(fake.requests)
		fake.mu.Unlock()
		if max == 2 && calls == 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	fake.mu.Lock()
	max := fake.maximum
	fake.mu.Unlock()
	if max != 2 {
		t.Fatalf("image concurrency max=%d want=2", max)
	}
	close(fake.release)
	ready := 0
	for time.Now().Before(deadline) {
		p, err = svc.Get(t.Context(), p.ID)
		if err != nil {
			t.Fatal(err)
		}
		ready = 0
		for _, shot := range p.Shots {
			if shot.FirstFrameStatus == "ready" {
				ready++
			}
		}
		if ready == 3 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if ready != 3 {
		t.Fatalf("ready frames=%d want=3: %+v", ready, p.Shots)
	}
	fake.mu.Lock()
	requests := append([]imageproject.GenerateRequest(nil), fake.requests...)
	fake.mu.Unlock()
	if len(requests) != 3 {
		t.Fatalf("image calls=%d", len(requests))
	}
	for _, req := range requests {
		if req.Model != "project-model" || req.Ratio != "9:16" || req.Count != 1 || !strings.Contains(req.Prompt, "礼盒特写") || !strings.Contains(req.Prompt, "暖色自然光") || !strings.Contains(req.Prompt, "蓝色包装") || !strings.Contains(req.Prompt, "不要出现文字、水印") {
			t.Fatalf("image request=%+v", req)
		}
	}
}

func waitTakeStatus(t *testing.T, svc *Service, projectID, status string) Project {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		p, err := svc.Get(t.Context(), projectID)
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Shots) > 0 && len(p.Shots[0].Takes) > 0 && p.Shots[0].Takes[0].Status == status {
			return p
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("take did not reach %s", status)
	return Project{}
}

func TestCancelAndDeleteRunningTakeCannotBeRevived(t *testing.T) {
	for _, action := range []string{"cancel", "delete"} {
		t.Run(action, func(t *testing.T) {
			grok := &blockedGrok{release: make(chan struct{})}
			svc, repo, _ := lifecycleFixture(t, Options{Grok: func(settings.Runtime) (grokvideo.Client, error) { return grok, nil }, PollInterval: time.Millisecond, Probe: func(context.Context, string, string) (refanalysis.VideoInfo, error) {
				return refanalysis.VideoInfo{DurationS: 6, Width: 320, Height: 240}, nil
			}}, nil)
			p, err := svc.Create(t.Context(), CreateRequest{Blank: true, Title: "竞态", VideoProvider: "grok"})
			if err != nil {
				t.Fatal(err)
			}
			p = addReady(t, svc, p.ID)
			out, err := svc.Generate(t.Context(), p.ID, GenerateRequest{Variants: 1})
			if err != nil {
				t.Fatal(err)
			}
			p = waitTakeStatus(t, svc, p.ID, "running")
			id := p.Shots[0].Takes[0].ID
			if action == "cancel" {
				if _, err = svc.CancelTake(t.Context(), id); err != nil {
					t.Fatal(err)
				}
				time.Sleep(20 * time.Millisecond)
				take, getErr := repo.GetTake(t.Context(), id)
				if getErr != nil || take.Status != "canceled" {
					t.Fatalf("canceled take revived: %+v err=%v initial=%+v", take, getErr, out)
				}
			} else {
				if err = svc.DeleteTake(t.Context(), id); err != nil {
					t.Fatal(err)
				}
				time.Sleep(20 * time.Millisecond)
				if _, getErr := repo.GetTake(t.Context(), id); !errors.Is(getErr, store.ErrReplicateNotFound) {
					t.Fatalf("deleted take revived: %v", getErr)
				}
			}
		})
	}
}
