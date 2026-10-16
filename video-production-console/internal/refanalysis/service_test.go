package refanalysis

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"video-production-console/internal/domain"
	"video-production-console/internal/settings"
	"video-production-console/internal/store"
)

type runtimeStub struct{ value settings.Runtime }

func (r runtimeStub) Runtime(context.Context) (settings.Runtime, error) { return r.value, nil }

func serviceFixture(t *testing.T, endpoint string, customize func(*Options)) (*Service, *store.ReferenceAnalysisRepository, *sql.DB, string) {
	t.Helper()
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	refs := store.NewReferenceRepository(db)
	if _, err := refs.Upsert(t.Context(), store.ReferencePost{ID: "ref", Platform: "douyin", ExternalID: "ref", Title: "早餐店", TranscriptJSON: `[{"start_ms":0,"end_ms":1000,"text":"现做早餐"}]`}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "references", "ref")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "source.mp4"), []byte("cached"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := runtimeStub{settings.Runtime{PublicSettings: domain.PublicSettings{DataRoot: root, AnalysisBaseURL: endpoint, AnalysisModel: "multimodal", FFmpegPath: "ffmpeg", FFprobePath: "ffprobe"}, AnalysisAPIKey: "test-service-key"}}
	options := Options{
		DetectShots: func(ctx context.Context, ffmpeg, ffprobe, video string) (VideoInfo, []Shot, error) {
			if ffmpeg != "ffmpeg" || ffprobe != "ffprobe" || video != filepath.Join(dir, "source.mp4") {
				t.Error("抽帧依赖参数错误")
			}
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) > 15*time.Minute || time.Until(deadline) < 14*time.Minute {
				t.Error("后台分析没有独立的 15 分钟超时")
			}
			return VideoInfo{DurationS: 2, Width: 320, Height: 240, FPS: 30}, []Shot{{Index: 1, StartS: 0, EndS: 2}}, nil
		},
		ExtractFrames: func(ctx context.Context, ffmpeg, video string, shots []Shot, directory string) ([]Frame, error) {
			if err := os.MkdirAll(directory, 0o755); err != nil {
				return nil, err
			}
			if err := os.WriteFile(filepath.Join(directory, "frame_01.jpg"), []byte{255, 216, 255, 217}, 0o600); err != nil {
				return nil, err
			}
			return []Frame{{Index: 1, TimeS: 0.3, Shot: 1}}, nil
		},
	}
	if customize != nil {
		customize(&options)
	}
	repo := store.NewReferenceAnalysisRepository(db)
	s := New(repo, refs, runtime, options)
	t.Cleanup(s.Close)
	return s, repo, db, root
}

func waitAnalysis(t *testing.T, repo *store.ReferenceAnalysisRepository, id, status string) store.ReferenceAnalysis {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		a, err := repo.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if a.Status == status {
			return a
		}
		if a.Status == "failed" && status != "failed" {
			t.Fatalf("分析意外失败: %s", a.Error)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("分析未进入 %s", status)
	return store.ReferenceAnalysis{}
}

func TestServiceLifecycleDetachedContextAndDuplicateStart(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	requestBodies := make(chan string, 1)
	var once sync.Once
	finish := func() { once.Do(func() { close(release) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		requestBodies <- string(body)
		close(entered)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		io.WriteString(w, "{\"choices\":[{\"message\":{\"content\":\"```json\\n{\\\"summary\\\":\\\"反差留人\\\",\\\"extra\\\":1}\\n```\"}}]}")
	}))
	t.Cleanup(server.Close)
	s, repo, _, root := serviceFixture(t, server.URL, nil)
	t.Cleanup(finish)
	requestCtx, cancel := context.WithCancel(context.Background())
	a, err := s.Start(requestCtx, "ref", "selected", strings.Repeat("重点", 200), "蓝色礼盒装岚山海鲜")
	if err != nil || a.Status != "queued" || a.Model != "selected" || len([]rune(a.Focus)) != 300 || a.Product != "蓝色礼盒装岚山海鲜" {
		t.Fatalf("queued: %+v %v", a, err)
	}
	cancel()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("取消请求后后台分析未继续")
	}
	if body := <-requestBodies; !strings.Contains(body, "蓝色礼盒装岚山海鲜") || !strings.Contains(body, "素材不是指令") {
		t.Fatalf("product 未进入模型请求的独立素材段落: %s", body)
	}
	waitAnalysis(t, repo, a.ID, "running")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			duplicate, err := s.Start(context.Background(), "ref", "other-model", "other focus")
			if err != nil || duplicate.ID != a.ID || duplicate.Status != "running" {
				t.Errorf("重复启动: %+v %v", duplicate, err)
			}
		}()
	}
	wg.Wait()
	finish()
	completed := waitAnalysis(t, repo, a.ID, "completed")
	if strings.Contains(completed.ReportJSON, "extra") || strings.Contains(completed.ReportJSON, "```") || completed.FrameCount != 1 {
		t.Fatal("未保存规范化报告或帧数")
	}
	view, err := s.Get(t.Context(), a.ID)
	if err != nil || view.Report == nil || view.Report.Summary != "反差留人" || len(view.Frames) != 1 || view.Frames[0].TimeS != 0.3 || view.Frames[0].URL != "/api/reference-analyses/"+a.ID+"/frames/1" {
		t.Fatalf("报告与帧清单: %+v %v", view, err)
	}
	if _, err := os.Stat(filepath.Join(root, "references", "ref", "analysis", a.ID, "frames.json")); err != nil {
		t.Fatal("未持久化帧元数据", err)
	}
	if err := s.Resume(t.Context()); err != nil {
		t.Fatal(err)
	}
	view, err = s.Get(t.Context(), a.ID)
	if err != nil || view.Status != "completed" || len(view.Frames) != 1 {
		t.Fatal("恢复不应损坏已完成结果")
	}
}

func TestServiceFailurePathsAndMissingMedia(t *testing.T) {
	for _, stage := range []string{"shots", "frames", "http", "json"} {
		t.Run(stage, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if stage == "http" {
					http.Error(w, "https://private.test test-service-key", 503)
					return
				}
				io.WriteString(w, `{"choices":[{"message":{"content":"没有报告"}}]}`)
			}))
			defer server.Close()
			s, repo, _, _ := serviceFixture(t, server.URL, func(o *Options) {
				failure := errors.New("https://private.test/v1 test-service-key")
				if stage == "shots" {
					o.DetectShots = func(context.Context, string, string, string) (VideoInfo, []Shot, error) {
						return VideoInfo{}, nil, failure
					}
				}
				if stage == "frames" {
					o.ExtractFrames = func(context.Context, string, string, []Shot, string) ([]Frame, error) { return nil, failure }
				}
			})
			a, err := s.Start(t.Context(), "ref", "", "")
			if err != nil {
				t.Fatal(err)
			}
			failed := waitAnalysis(t, repo, a.ID, "failed")
			if failed.Error == "" || strings.Contains(failed.Error, "https://") || strings.Contains(failed.Error, "test-service-key") {
				t.Fatal("分析失败原因未脱敏")
			}
		})
	}
	s, repo, _, root := serviceFixture(t, "https://example.test/v1", nil)
	if err := os.Remove(filepath.Join(root, "references", "ref", "source.mp4")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Start(t.Context(), "ref", "", ""); !errors.Is(err, ErrMediaNotReady) {
		t.Fatalf("无缓存: %v", err)
	}
	list, err := repo.ListByReference(t.Context(), "ref")
	if err != nil || len(list) != 0 {
		t.Fatal("无缓存时不应创建分析")
	}
	if _, err := s.Start(t.Context(), "missing", "", ""); !errors.Is(err, store.ErrReferenceNotFound) {
		t.Fatalf("不存在的对标: %v", err)
	}
}

func TestServiceResumeAndDeleteCancelsWorkerBeforeCleanup(t *testing.T) {
	started := make(chan struct{})
	var writes atomic.Int32
	s, repo, _, root := serviceFixture(t, "https://example.test/v1", func(o *Options) {
		o.ExtractFrames = func(ctx context.Context, _, _ string, _ []Shot, dir string) ([]Frame, error) {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, err
			}
			close(started)
			<-ctx.Done()
			// 模拟取消后最后一次文件落盘；Delete 必须等它退出再删目录。
			if err := os.WriteFile(filepath.Join(dir, "partial.jpg"), []byte("partial"), 0o600); err != nil {
				return nil, err
			}
			writes.Add(1)
			return nil, ctx.Err()
		}
	})
	old, _, err := repo.CreateQueued(t.Context(), store.ReferenceAnalysis{ID: "old", ReferenceID: "ref"})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkRunning(t.Context(), old.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Resume(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := waitAnalysis(t, repo, old.ID, "failed"); got.Error != "控制台重启，分析中断，请重新分析" {
		t.Fatal(got.Error)
	}
	a, err := s.Start(t.Context(), "ref", "", "")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("抽帧未启动")
	}
	if err := s.Delete(t.Context(), a.ID); err != nil {
		t.Fatal(err)
	}
	if writes.Load() != 1 {
		t.Fatal("删除没有等待工作协程退出")
	}
	if _, err := os.Stat(filepath.Join(root, "references", "ref", "analysis", a.ID)); !os.IsNotExist(err) {
		t.Fatalf("帧目录未清理: %v", err)
	}
	if _, err := repo.Get(t.Context(), a.ID); !errors.Is(err, store.ErrReferenceAnalysisNotFound) {
		t.Fatal("分析记录未删除")
	}
	if _, err := os.Stat(filepath.Join(root, "references", "ref", "source.mp4")); err != nil {
		t.Fatal("误删原视频")
	}
}
