package imagestudio

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"image"
	"image/png"
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
	"video-production-console/internal/imageproject"
	"video-production-console/internal/settings"
	"video-production-console/internal/store"
)

type runtimeStub struct{ value settings.Runtime }

func (r runtimeStub) Runtime(context.Context) (settings.Runtime, error) { return r.value, nil }

func testPNG(t *testing.T) string {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 3))); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b.Bytes())
}
func replyImages(w http.ResponseWriter, b64 string, n int) {
	data := make([]map[string]string, n)
	for i := range data {
		data[i] = map[string]string{"b64_json": b64}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}
func testService(t *testing.T, handler http.HandlerFunc, enhance Enhancer) (*Service, *store.ImageStudioRepository) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := store.NewImageStudioRepository(db)
	rt := runtimeStub{settings.Runtime{PublicSettings: domain.PublicSettings{DataRoot: root, ImageBaseURL: server.URL, ImageModel: "grok-imagine-image-2.0", MaxImageConcurrency: 2, ImageGenerationAttempts: 1}, ImageAPIKey: "test-only"}}
	svc, err := NewService(repo, rt, imageproject.NewClient(server.Client()), enhance)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	return svc, repo
}
func waitGeneration(t *testing.T, s *Service, id string) domain.ImageGeneration {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		g, err := s.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if g.Status != "queued" && g.Status != "running" {
			return g
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("generation did not finish")
	return domain.ImageGeneration{}
}
func TestBatchGenerationPreservesPromptAndPublishesOutputs(t *testing.T) {
	b64 := testPNG(t)
	var calls atomic.Int32
	s, _ := testService(t, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			N      int    `json:"n"`
			Prompt string `json:"prompt"`
			Size   string `json:"size"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.N != 3 || req.Prompt != "  一只橘猫坐在窗台  " || req.Size != "1024x1536" {
			t.Errorf("unexpected payload n=%d prompt=%q size=%s", req.N, req.Prompt, req.Size)
		}
		calls.Add(1)
		replyImages(w, b64, 3)
	}, nil)
	g, err := s.Create(context.Background(), CreateRequest{Prompt: "  一只橘猫坐在窗台  ", Ratio: "3:4", Count: 3})
	if err != nil {
		t.Fatal(err)
	}
	if g.Status != "queued" || len(g.Outputs) != 3 {
		t.Fatalf("create=%+v", g)
	}
	g = waitGeneration(t, s, g.ID)
	if g.Status != "completed" || calls.Load() != 1 || g.FinalPrompt != g.Prompt {
		t.Fatalf("result=%+v calls=%d", g, calls.Load())
	}
	if strings.Contains(g.FinalPrompt, "45—65岁") || strings.Contains(g.FinalPrompt, "画面文字规则") {
		t.Fatal("finance prompt leaked")
	}
	for i, o := range g.Outputs {
		if o.Status != "ready" || o.Position != i || o.Width != 2 || o.Height != 3 || o.URL == "" {
			t.Fatalf("output=%+v", o)
		}
	}
}

func TestShortBatchIsFilledConcurrentlyAndReadyImagesAppearImmediately(t *testing.T) {
	b64 := testPNG(t)
	started := make(chan struct{}, 3)
	release := make(chan struct{})
	var active, peak, calls atomic.Int32
	s, _ := testService(t, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			N int `json:"n"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		calls.Add(1)
		if req.N == 4 {
			replyImages(w, b64, 1)
			return
		}
		if req.N != 1 {
			t.Errorf("supplement n=%d", req.N)
		}
		n := active.Add(1)
		defer active.Add(-1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		started <- struct{}{}
		select {
		case <-release:
			replyImages(w, b64, 1)
		case <-r.Context().Done():
		}
	}, nil)
	t.Cleanup(func() { close(release) })
	g, err := s.Create(context.Background(), CreateRequest{Prompt: "猫", Count: 4, Ratio: "1:1"})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("supplements did not run concurrently")
		}
	}
	g, err = s.Get(context.Background(), g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if g.Status != "running" || g.Outputs[0].Status != "ready" || g.Outputs[1].Status != "pending" {
		t.Fatalf("incremental state=%+v", g)
	}
	// Releasing each waiter keeps the channel reusable for the third supplement.
	release <- struct{}{}
	release <- struct{}{}
	release <- struct{}{}
	g = waitGeneration(t, s, g.ID)
	if g.Status != "completed" || calls.Load() != 4 || peak.Load() != 2 {
		t.Fatalf("status=%s calls=%d peak=%d", g.Status, calls.Load(), peak.Load())
	}
}

func TestTerminalGatewayErrorsFailAllWithoutRetry(t *testing.T) {
	for _, tc := range []struct {
		status  int
		message string
	}{{401, "生图 Key 无效或没有权限，请到「设置 → 图片」检查 Key"}, {403, "生图 Key 无效或没有权限，请到「设置 → 图片」检查 Key"}, {400, "图片模型「grok-imagine-image-2.0」网关不支持，换一个模型试试"}} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			var calls atomic.Int32
			s, _ := testService(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); http.Error(w, "test-only", tc.status) }, nil)
			g, err := s.Create(context.Background(), CreateRequest{Prompt: "猫", Count: 3, Ratio: "1:1"})
			if err != nil {
				t.Fatal(err)
			}
			g = waitGeneration(t, s, g.ID)
			if g.Status != "failed" || g.Error != tc.message || calls.Load() != 1 {
				t.Fatalf("status=%s message=%q calls=%d", g.Status, g.Error, calls.Load())
			}
			for _, o := range g.Outputs {
				if o.Status != "failed" || o.Error != tc.message || o.URL != "" {
					t.Fatalf("output=%+v", o)
				}
			}
		})
	}
}

func TestFailedOrEmptyBatchFallsBackToSingleImages(t *testing.T) {
	for _, status := range []int{200, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			b64 := testPNG(t)
			var batches, singles atomic.Int32
			s, _ := testService(t, func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					N int `json:"n"`
				}
				_ = json.NewDecoder(r.Body).Decode(&req)
				if req.N == 3 {
					batches.Add(1)
					if status == 503 {
						http.Error(w, "unavailable", 503)
					} else {
						replyImages(w, b64, 0)
					}
					return
				}
				if req.N != 1 {
					t.Errorf("supplement count=%d", req.N)
				}
				singles.Add(1)
				replyImages(w, b64, 1)
			}, nil)
			g, err := s.Create(context.Background(), CreateRequest{Prompt: "猫", Count: 3, Ratio: "1:1"})
			if err != nil {
				t.Fatal(err)
			}
			g = waitGeneration(t, s, g.ID)
			if g.Status != "completed" || batches.Load() != 1 || singles.Load() != 3 {
				t.Fatalf("status=%s batches=%d singles=%d", g.Status, batches.Load(), singles.Load())
			}
		})
	}
}

func TestPartialGenerationRetriesOnlyFailedOutputsWithOriginalSettings(t *testing.T) {
	b64 := testPNG(t)
	var calls, singles atomic.Int32
	var retry atomic.Bool
	s, repo := testService(t, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			N      int    `json:"n"`
			Prompt string `json:"prompt"`
			Model  string `json:"model"`
			Size   string `json:"size"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		calls.Add(1)
		if req.Prompt != "编辑后的最终提示词" || req.Model != "custom-image" || req.Size != "1536x1024" {
			t.Errorf("retry changed request %+v", req)
		}
		if req.N == 3 {
			replyImages(w, b64, 1)
			return
		}
		n := singles.Add(1)
		if !retry.Load() && n == 2 {
			http.Error(w, "unavailable", 503)
			return
		}
		replyImages(w, b64, 1)
	}, nil)
	g, err := s.Create(context.Background(), CreateRequest{Prompt: "猫", FinalPrompt: "编辑后的最终提示词", Enhance: true, Model: "custom-image", Count: 3, Ratio: "4:3"})
	if err != nil {
		t.Fatal(err)
	}
	g = waitGeneration(t, s, g.ID)
	if g.Status != "partial" || !strings.Contains(g.Error, "只出了 2 张") || calls.Load() != 3 {
		t.Fatalf("partial=%+v calls=%d", g, calls.Load())
	}
	ready := map[string]string{}
	for _, o := range g.Outputs {
		if o.Status == "ready" {
			stored, _ := repo.Output(context.Background(), o.ID)
			ready[o.ID] = stored.Path
		}
	}
	retry.Store(true)
	g, err = s.Retry(context.Background(), g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if g.Status != "queued" {
		t.Fatalf("retry=%s", g.Status)
	}
	g = waitGeneration(t, s, g.ID)
	if g.Status != "completed" || calls.Load() != 4 {
		t.Fatalf("retry status=%s calls=%d", g.Status, calls.Load())
	}
	for id, path := range ready {
		stored, _ := repo.Output(context.Background(), id)
		if stored.Path != path {
			t.Fatal("retry overwrote ready image")
		}
	}
}

func TestGenerationQueueLimitCancellationAndShutdown(t *testing.T) {
	started := make(chan struct{}, 8)
	release := make(chan struct{})
	var active, peak atomic.Int32
	s, _ := testService(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		n := active.Add(1)
		defer active.Add(-1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		started <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}, nil)
	t.Cleanup(func() { close(release) })
	ids := []string{}
	for range 4 {
		g, err := s.Create(context.Background(), CreateRequest{Prompt: "猫", Count: 1, Ratio: "1:1"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, g.ID)
	}
	for range 3 {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("three generations did not start")
		}
	}
	var running, queued string
	for _, id := range ids {
		g, _ := s.Get(context.Background(), id)
		if g.Status == "queued" {
			if queued != "" {
				t.Fatal("multiple unexpectedly queued")
			}
			queued = id
		} else if g.Status == "running" {
			running = id
		}
	}
	if queued == "" || peak.Load() != 3 {
		t.Fatalf("queue=%s peak=%d", queued, peak.Load())
	}
	if _, err := s.Retry(context.Background(), running); !errors.Is(err, store.ErrImageGenerationRunning) {
		t.Fatalf("running retry=%v", err)
	}
	if err := s.Delete(context.Background(), running); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(context.Background(), running); !errors.Is(err, store.ErrImageGenerationNotFound) {
		t.Fatalf("deleted get=%v", err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("queued generation did not start")
	}
	done := make(chan struct{})
	go func() { s.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not cancel and wait")
	}
	for _, id := range ids {
		if id == running {
			continue
		}
		g, _ := s.Get(context.Background(), id)
		if g.Status != "failed" {
			t.Fatalf("shutdown status=%s", g.Status)
		}
	}
	if _, err := s.Create(context.Background(), CreateRequest{Prompt: "猫", Count: 1, Ratio: "1:1"}); !errors.Is(err, ErrClosed) {
		t.Fatalf("create after close=%v", err)
	}
}

func TestRestartMarksInterruptedRecordsAndPreservesReadyImages(t *testing.T) {
	s, repo := testService(t, func(w http.ResponseWriter, r *http.Request) { t.Error("recovery must not call gateway") }, nil)
	s.Close()
	now := time.Now().UTC()
	for _, status := range []string{"queued", "running", "completed"} {
		g := domain.ImageGeneration{ID: uuid.NewString(), Prompt: "猫", FinalPrompt: "猫", Model: "image", Ratio: "1:1", Count: 2, Status: status, CreatedAt: now, UpdatedAt: now}
		g.Outputs = []domain.ImageGenerationOutput{{ID: uuid.NewString(), Position: 0, Status: "ready", Path: "kept.png"}, {ID: uuid.NewString(), Position: 1, Status: "pending"}}
		if err := repo.Create(context.Background(), g); err != nil {
			t.Fatal(err)
		}
	}
	restarted, err := NewService(repo, s.runtime, s.generator, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	items, _, err := restarted.List(context.Background(), store.ImageGenerationListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	failed := 0
	for _, g := range items {
		if g.Status == "completed" {
			continue
		}
		failed++
		if g.Status != "failed" || g.Error != "控制台重启中断" || g.Outputs[0].Status != "ready" || g.Outputs[1].Status != "failed" || g.Outputs[1].Error != "控制台重启中断" {
			t.Fatalf("recovered=%+v", g)
		}
	}
	if failed != 2 {
		t.Fatalf("interrupted=%d", failed)
	}
}

func TestOutputPathCannotEscapeRootAndDeleteRemovesDirectory(t *testing.T) {
	b64 := testPNG(t)
	s, repo := testService(t, func(w http.ResponseWriter, r *http.Request) { replyImages(w, b64, 1) }, nil)
	g, err := s.Create(context.Background(), CreateRequest{Prompt: "猫", Count: 1, Ratio: "1:1"})
	if err != nil {
		t.Fatal(err)
	}
	g = waitGeneration(t, s, g.ID)
	f, o, err := s.OpenOutput(context.Background(), g.Outputs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	rt, _ := s.runtime.Runtime(context.Background())
	full := filepath.Join(rt.DataRoot, "image-studio", o.Path)
	if _, err := os.Stat(full); err != nil {
		t.Fatal(err)
	}
	// Repository fixture imitates a corrupted path; rooted IO must still reject it.
	if err := os.WriteFile(filepath.Join(rt.DataRoot, "outside.png"), []byte("must not be served"), 0o600); err != nil {
		t.Fatal(err)
	}
	malicious := domain.ImageGeneration{ID: uuid.NewString(), Prompt: "bad", FinalPrompt: "bad", Model: "image", Ratio: "1:1", Count: 1, Status: "completed", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	malicious.Outputs = []domain.ImageGenerationOutput{{ID: uuid.NewString(), Position: 0, Status: "ready", Path: filepath.Join("..", "outside.png")}}
	if err := repo.Create(context.Background(), malicious); err != nil {
		t.Fatal(err)
	}
	if f, _, err := s.OpenOutput(context.Background(), malicious.Outputs[0].ID); err == nil {
		f.Close()
		t.Fatal("escaped studio root")
	}
	if err := s.Delete(context.Background(), g.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(full)); !os.IsNotExist(err) {
		t.Fatalf("generation directory still exists: %v", err)
	}
	if _, err := repo.Output(context.Background(), o.ID); !errors.Is(err, store.ErrImageGenerationNotFound) {
		t.Fatalf("cascade did not delete output: %v", err)
	}
}

func TestSupplementAuthorizationFailureKeepsActionableCause(t *testing.T) {
	b64 := testPNG(t)
	var singles atomic.Int32
	s, _ := testService(t, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			N int `json:"n"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.N == 3 {
			replyImages(w, b64, 1)
			return
		}
		if singles.Add(1) == 1 {
			<-r.Context().Done()
			return
		}
		http.Error(w, "not authorized", 401)
	}, nil)
	g, err := s.Create(context.Background(), CreateRequest{Prompt: "猫", Count: 3, Ratio: "1:1"})
	if err != nil {
		t.Fatal(err)
	}
	g = waitGeneration(t, s, g.ID)
	if g.Status != "partial" || !strings.Contains(g.Error, "生图 Key 无效或没有权限") || singles.Load() != 2 {
		t.Fatalf("result status=%s error=%q supplements=%d", g.Status, g.Error, singles.Load())
	}
}

func TestStyleAppendsOnlyDescriptionAndValidation(t *testing.T) {
	b64 := testPNG(t)
	s, _ := testService(t, func(w http.ResponseWriter, r *http.Request) { replyImages(w, b64, 1) }, nil)
	for _, tc := range []struct{ style, custom string }{{"red_ink", ""}, {"custom", "柔和自然光"}} {
		g, err := s.Create(context.Background(), CreateRequest{Prompt: "猫", Style: tc.style, CustomStyle: tc.custom, Ratio: "1:1", Count: 1})
		if err != nil {
			t.Fatal(err)
		}
		g = waitGeneration(t, s, g.ID)
		if !strings.HasPrefix(g.FinalPrompt, "猫\n") || strings.Contains(g.FinalPrompt, "45—65岁") || strings.Contains(g.FinalPrompt, "画面文字规则") {
			t.Fatalf("styled prompt=%q", g.FinalPrompt)
		}
		if tc.style == "custom" && g.FinalPrompt != "猫\n柔和自然光" {
			t.Fatalf("custom prompt=%q", g.FinalPrompt)
		}
	}
	for _, req := range []CreateRequest{{Prompt: "", Ratio: "1:1", Count: 1}, {Prompt: "猫", Ratio: "16:9", Count: 1}, {Prompt: "猫", Ratio: "1:1", Count: 0}, {Prompt: "猫", Ratio: "1:1", Count: 5}, {Prompt: "猫", Ratio: "1:1", Count: 1, Style: "unknown"}, {Prompt: "猫", Ratio: "1:1", Count: 1, Style: "custom"}, {Prompt: string([]byte{255}), Ratio: "1:1", Count: 1}, {Prompt: strings.Repeat("猫", 4001), Ratio: "1:1", Count: 1}, {Prompt: "猫", FinalPrompt: strings.Repeat("猫", 4001), Ratio: "1:1", Count: 1}, {Prompt: "猫", Style: "custom", CustomStyle: strings.Repeat("猫", 501), Ratio: "1:1", Count: 1}} {
		if err := Validate(req); err == nil {
			t.Fatalf("invalid request accepted: %v", req)
		}
	}
}
