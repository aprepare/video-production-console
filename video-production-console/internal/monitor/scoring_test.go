package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"video-production-console/internal/domain"
	"video-production-console/internal/platformapi"
	"video-production-console/internal/settings"
	"video-production-console/internal/store"
)

func TestAISurgeModesLimitAndRulePriority(t *testing.T) {
	for _, mode := range []string{"ai", "both"} {
		t.Run(mode, func(t *testing.T) {
			var called atomic.Int32
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called.Add(1)
				var body struct {
					Messages []struct {
						Content string `json:"content"`
					} `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				var input struct {
					Items []struct {
						ExternalID string `json:"external_id"`
						LikeDelta  int64  `json:"like_delta"`
					} `json:"items"`
				}
				if len(body.Messages) != 2 {
					t.Error("invalid messages")
					return
				}
				if err := json.Unmarshal([]byte(body.Messages[1].Content), &input); err != nil {
					t.Error(err)
				}
				if len(input.Items) != 20 {
					t.Errorf("AI candidates=%d", len(input.Items))
				}
				var prev int64 = 1 << 62
				for _, it := range input.Items {
					if it.LikeDelta > prev {
						t.Error("candidates not sorted")
					}
					prev = it.LikeDelta
					if mode == "both" && it.ExternalID == "rule" {
						t.Error("rule hit sent to AI")
					}
					if it.ExternalID == "new" || it.ExternalID == "no-growth" {
						t.Error("ineligible candidate sent")
					}
				}
				out, _ := json.Marshal(map[string]any{"items": []any{map[string]any{"external_id": input.Items[0].ExternalID, "surge": true, "reason": "30分钟互动异常增长"}, map[string]any{"external_id": "outside", "surge": true, "reason": "忽略规则"}}})
				json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": string(out)}}}})
			}))
			defer model.Close()
			s, _ := fixture(t, referenceStub{}, settings.Runtime{PublicSettings: domain.PublicSettings{AnalysisBaseURL: model.URL, AnalysisModel: "fake"}}, Options{})
			task := DefaultTask()
			task.SurgeMode = mode
			now := time.Now()
			previous := store.MonitorItem{Likes: 100, LastSeenAt: stamp(now.Add(-30 * time.Minute))}
			observed := []observedItem{}
			for i := 0; i < 25; i++ {
				item := store.MonitorItem{ExternalID: fmt.Sprint(i), Likes: 100 + int64(i)}
				d := evaluateSurge(task, item, &previous, now)
				observed = append(observed, observedItem{item: item, previous: &previous, decision: d})
			}
			rule := store.MonitorItem{ExternalID: "rule", Likes: 1000}
			d := evaluateSurge(task, rule, &previous, now)
			if mode == "ai" {
				d.Hit = false
				d.Reason = ""
			}
			observed = append(observed, observedItem{item: rule, previous: &previous, decision: d}, observedItem{item: store.MonitorItem{ExternalID: "new", Likes: 10000}}, observedItem{item: store.MonitorItem{ExternalID: "no-growth", Likes: 100}, previous: &previous})
			s.detectAISurges(t.Context(), task, observed)
			hits := 0
			for _, o := range observed {
				if o.decision.Hit {
					hits++
				}
			}
			want := 1
			if mode == "both" {
				want = 2
			}
			if called.Load() != 1 || hits != want {
				t.Fatalf("calls=%d hits=%d want=%d", called.Load(), hits, want)
			}
		})
	}
}

func TestScoringFailuresDoNotFailRunOrSuppressSurgeCollection(t *testing.T) {
	for _, response := range []string{"http500", "missing", "invalid-score", "malformed"} {
		t.Run(response, func(t *testing.T) {
			var calls atomic.Int32
			var collections atomic.Int32
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if response == "http500" {
					w.WriteHeader(500)
					w.Write([]byte("secret-model-token"))
					return
				}
				content := `{"items":[]}`
				if response == "invalid-score" {
					content = `{"items":[{"external_id":"new","score":101,"reason":"越界"}]}`
				}
				if response == "malformed" {
					content = "no JSON secret-model-token"
				}
				json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}}}})
			}))
			defer model.Close()
			refs := referenceStub{search: func(context.Context, string, string, string, string, string) (platformapi.SearchPage, error) {
				return platformapi.SearchPage{Items: []platformapi.Post{{ExternalID: "old", Likes: 1000}, {ExternalID: "new", Likes: 1}}}, nil
			}, collect: func(_ context.Context, p *platformapi.Post, _ bool) (store.ReferencePost, error) {
				collections.Add(1)
				return store.ReferencePost{ID: "collected-" + p.ExternalID}, nil
			}}
			s, repo := fixture(t, refs, settings.Runtime{PublicSettings: domain.PublicSettings{AnalysisBaseURL: model.URL, AnalysisModel: "fake"}}, Options{})
			task := createTask(t, s, "failure")
			task.ScoreEnabled = true
			task.AutoCollect = true
			if _, err := s.Update(t.Context(), task); err != nil {
				t.Fatal(err)
			}
			if _, err := repo.CreateRun(t.Context(), store.MonitorRun{ID: "previous", TaskID: task.ID, Status: "completed", Trigger: "manual", StartedAt: stamp(time.Now().Add(-time.Hour))}); err != nil {
				t.Fatal(err)
			}
			if _, _, err := repo.UpsertItem(t.Context(), task.ID, "previous", platformapi.Post{Platform: platformapi.Douyin, ExternalID: "old", Likes: 100}, time.Now().Add(-time.Hour)); err != nil {
				t.Fatal(err)
			}
			go s.Run(t.Context())
			if _, err := s.Start(t.Context(), task.ID); err != nil {
				t.Fatal(err)
			}
			runs := waitRuns(t, repo, task.ID, 2)
			if runs[0].Status != "completed" || runs[0].ScoredCount != 0 || runs[0].CollectedCount != 1 || collections.Load() != 1 || calls.Load() != 1 {
				t.Fatalf("run=%+v collects=%d model=%d", runs[0], collections.Load(), calls.Load())
			}
			feed, err := repo.Feed(t.Context(), task.ID, "all", 100)
			if err != nil {
				t.Fatal(err)
			}
			for _, it := range feed.Items {
				if it.ExternalID == "new" && (it.ScoreStatus != "failed" || it.ScoreError == "" || strings.Contains(it.ScoreError, "secret-model-token")) {
					t.Fatalf("failure state=%+v", it)
				}
			}
		})
	}
}

func TestAutoActionsCarryTaskModelAndDoNotFailRun(t *testing.T) {
	var analyses, insights atomic.Int32
	refs := referenceStub{search: func(context.Context, string, string, string, string, string) (platformapi.SearchPage, error) {
		return platformapi.SearchPage{Items: []platformapi.Post{{ExternalID: "video", VideoURL: "http://sns-video-v4.xhscdn.com/a.mp4"}, {ExternalID: "image"}}}, nil
	}, collect: func(_ context.Context, p *platformapi.Post, tr bool) (store.ReferencePost, error) {
		if !tr {
			t.Error("auto_transcribe not forwarded")
		}
		return store.ReferencePost{ID: p.ExternalID}, nil
	}}
	s, repo := fixture(t, refs, settings.Runtime{}, Options{Analyze: func(_ context.Context, id, model string) error {
		analyses.Add(1)
		if id != "video" || model != "chosen" {
			t.Error("analysis parameters")
		}
		return errors.New("not ready")
	}, Insight: func(_ context.Context, _, model string) error {
		insights.Add(1)
		if model != "chosen" {
			t.Error("insight model")
		}
		return errors.New("no comments")
	}})
	task := createTask(t, s, "auto")
	task.AutoCollect = true
	task.ScoreThreshold = 0
	task.AutoAnalyze = true
	task.AutoInsight = true
	task.AutoTranscribe = true
	task.Model = "chosen"
	task.Platform = "xhs"
	if _, err := s.Update(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	go s.Run(t.Context())
	if _, err := s.Start(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}
	runs := waitRuns(t, repo, task.ID, 1)
	if runs[0].Status != "completed" || runs[0].CollectedCount != 2 || analyses.Load() != 1 || insights.Load() != 2 {
		t.Fatalf("actions run=%+v analysis=%d insight=%d", runs[0], analyses.Load(), insights.Load())
	}
}

func TestAutoAnalysisWaitsForFreshMediaWithoutBlockingMonitor(t *testing.T) {
	started := make(chan string, 2)
	var svc *Service
	refs := referenceStub{
		search: func(context.Context, string, string, string, string, string) (platformapi.SearchPage, error) {
			return platformapi.SearchPage{Items: []platformapi.Post{{ExternalID: "fresh", VideoURL: "https://v.douyinvod.com/a.mp4"}}}, nil
		},
		collect: func(ctx context.Context, p *platformapi.Post, _ bool) (store.ReferencePost, error) {
			return svc.referenceRepo.Upsert(ctx, store.ReferencePost{ID: "fresh-ref", Platform: "douyin", ExternalID: p.ExternalID, VideoURL: p.VideoURL, MediaStatus: "downloading"})
		},
	}
	s, repo := fixture(t, refs, settings.Runtime{}, Options{Analyze: func(_ context.Context, id, model string) error {
		started <- id + ":" + model
		return nil
	}})
	svc = s
	task := createTask(t, s, "cache")
	task.AutoCollect, task.AutoAnalyze, task.ScoreThreshold, task.Model = true, true, 0, "chosen"
	if _, err := s.Update(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	go s.Run(t.Context())
	if _, err := s.Start(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}
	runs := waitRuns(t, repo, task.ID, 1)
	if runs[0].Status != "completed" {
		t.Fatalf("run=%+v", runs[0])
	}
	select {
	case <-started:
		t.Fatal("analysis started before original video finished downloading")
	default:
	}
	if err := s.referenceRepo.CompleteMedia(t.Context(), "fresh-ref", "source.mp4", 10); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-started:
		if got != "fresh-ref:chosen" {
			t.Fatalf("analysis=%s", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("analysis was never started after download")
	}
}
