package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"video-production-console/internal/domain"
	"video-production-console/internal/monitorai"
	"video-production-console/internal/platformapi"
	"video-production-console/internal/settings"
	"video-production-console/internal/store"
)

func auditModel(t *testing.T) settings.Runtime {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct {
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || len(request.Messages) != 2 {
			t.Error("invalid request", err)
			w.WriteHeader(500)
			return
		}
		var text string
		if err := json.Unmarshal(request.Messages[1].Content, &text); err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		var input struct {
			Items []struct {
				ExternalID string `json:"external_id"`
			} `json:"items"`
		}
		if err := json.Unmarshal([]byte(text), &input); err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		items := []map[string]any{}
		for _, item := range input.Items {
			items = append(items, map[string]any{"external_id": item.ExternalID, "score": 82, "reason": "可借鉴", "tags": []string{}})
		}
		content, _ := json.Marshal(map[string]any{"items": items})
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": string(content)}}}})
	}))
	t.Cleanup(server.Close)
	return settings.Runtime{PublicSettings: domain.PublicSettings{AnalysisBaseURL: server.URL, AnalysisModel: "fake"}}
}

func auditRun(t *testing.T, s *Service, task store.MonitorTask, id string) store.MonitorRun {
	t.Helper()
	run, err := s.repo.CreateRun(t.Context(), store.MonitorRun{ID: id, TaskID: task.ID, Status: "running", Trigger: "manual", StartedAt: stamp(s.options.Now())})
	if err != nil {
		t.Fatal(err)
	}
	job := &queuedRun{run: run, ctx: t.Context()}
	s.finish(job, s.execute(job))
	return job.run
}

func TestScoringDrainsBacklogAcrossRuns(t *testing.T) {
	for _, rediscovered := range []bool{true, false} {
		t.Run(fmt.Sprint(rediscovered), func(t *testing.T) {
			posts := make([]platformapi.Post, 45)
			for i := range posts {
				posts[i] = platformapi.Post{ExternalID: fmt.Sprintf("post-%02d", i)}
			}
			calls := 0
			refs := referenceStub{search: func(context.Context, string, string, string, string, string) (platformapi.SearchPage, error) {
				calls++
				if calls > 1 && !rediscovered {
					return platformapi.SearchPage{}, nil
				}
				return platformapi.SearchPage{Items: posts}, nil
			}}
			s, repo := fixture(t, refs, auditModel(t), Options{})
			task := createTask(t, s, "backlog")
			task.ScoreEnabled, task.AutoCollect = true, true
			if _, err := s.Update(t.Context(), task); err != nil {
				t.Fatal(err)
			}
			first := auditRun(t, s, task, "first")
			second := auditRun(t, s, task, "second")
			if first.ScoredCount != 30 || second.ScoredCount != 15 || second.CollectedCount != 15 || second.Status != "completed" {
				t.Fatalf("first=%+v second=%+v", first, second)
			}
			feed, err := repo.Feed(t.Context(), task.ID, "all", 100)
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range feed.Items {
				if item.ScoreStatus != "scored" || item.ReferenceID == "" {
					t.Fatalf("unprocessed item=%+v", item)
				}
			}
		})
	}
}

func TestScoringResumesPendingAndPrioritizesCurrentThenOldest(t *testing.T) {
	now := time.Now().UTC()
	s, repo := fixture(t, referenceStub{search: func(context.Context, string, string, string, string, string) (platformapi.SearchPage, error) {
		return platformapi.SearchPage{Items: []platformapi.Post{{ExternalID: "current"}}}, nil
	}}, auditModel(t), Options{Now: func() time.Time { return now }})
	task := createTask(t, s, "pending")
	task.ScoreEnabled = true
	if _, err := s.Update(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateRun(t.Context(), store.MonitorRun{ID: "interrupted", TaskID: task.ID, Status: "running", Trigger: "manual", StartedAt: stamp(now.Add(-time.Hour))}); err != nil {
		t.Fatal(err)
	}
	var pendingID string
	for i := 0; i < 31; i++ {
		item, _, err := repo.UpsertItem(t.Context(), task.ID, "interrupted", platformapi.Post{Platform: platformapi.Douyin, ExternalID: fmt.Sprintf("old-%02d", i)}, now.Add(time.Duration(i-60)*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			pendingID = item.ID
			if err := repo.SetScore(t.Context(), item.ID, "pending", 0, "", nil, ""); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := s.Resume(t.Context()); err != nil {
		t.Fatal(err)
	}
	run := auditRun(t, s, task, "recovered")
	if run.ScoredCount != 30 {
		t.Fatalf("run=%+v", run)
	}
	pending, err := repo.GetItem(t.Context(), pendingID)
	if err != nil || pending.ScoreStatus != "scored" {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
	feed, err := repo.Feed(t.Context(), task.ID, "all", 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range feed.Items {
		want := "scored"
		if item.ExternalID == "old-29" || item.ExternalID == "old-30" {
			want = "none"
		}
		if item.ScoreStatus != want {
			t.Fatalf("priority: %s=%s want %s", item.ExternalID, item.ScoreStatus, want)
		}
	}
}

func TestPaginationFailureSavesAndProcessesSuccessfulPages(t *testing.T) {
	for _, failedPage := range []int{2, 3} {
		t.Run(fmt.Sprint(failedPage), func(t *testing.T) {
			now := time.Now().UTC()
			calls := 0
			refs := referenceStub{search: func(context.Context, string, string, string, string, string) (platformapi.SearchPage, error) {
				calls++
				if calls == failedPage {
					return platformapi.SearchPage{}, errors.New("HTTP 461 secret-platform-token")
				}
				return platformapi.SearchPage{Items: []platformapi.Post{{ExternalID: fmt.Sprint(calls), Likes: 1000}}, HasMore: true, Cursor: fmt.Sprint(calls)}, nil
			}}
			s, repo := fixture(t, refs, auditModel(t), Options{Now: func() time.Time { return now }, Sleep: func(context.Context, time.Duration) error { return nil }})
			task := createTask(t, s, "partial")
			task.Pages, task.ScoreEnabled = 3, true
			if _, err := s.Update(t.Context(), task); err != nil {
				t.Fatal(err)
			}
			if _, _, err := repo.UpsertItem(t.Context(), task.ID, "prior", platformapi.Post{Platform: platformapi.Douyin, ExternalID: "1", Likes: 100}, now.Add(-30*time.Minute)); err != nil {
				t.Fatal(err)
			}
			run := auditRun(t, s, task, "partial")
			if run.Status != "failed" || run.Found != int64(failedPage-1) || run.ScoredCount != int64(failedPage-1) || run.SurgeCount != 1 || calls != failedPage || !strings.Contains(run.Error, fmt.Sprintf("第 %d 页失败：", failedPage)) || !strings.Contains(run.Error, fmt.Sprintf("已保存前 %d 页", failedPage-1)) || strings.Contains(run.Error, "secret") {
				t.Fatalf("run=%+v calls=%d", run, calls)
			}
			feed, err := repo.Feed(t.Context(), task.ID, "all", 100)
			if err != nil || len(feed.Items) != failedPage-1 {
				t.Fatalf("feed=%+v err=%v", feed, err)
			}
		})
	}
}

func TestMissingCountsSkipSurgeAndUseLastValidSnapshot(t *testing.T) {
	for _, mode := range []string{"rule", "ai", "both"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Now().UTC()
			post := platformapi.Post{ExternalID: "one", CountsMissing: true}
			s, repo := fixture(t, referenceStub{search: func(context.Context, string, string, string, string, string) (platformapi.SearchPage, error) {
				return platformapi.SearchPage{Items: []platformapi.Post{post}}, nil
			}}, settings.Runtime{PublicSettings: domain.PublicSettings{AnalysisBaseURL: "https://model.invalid", AnalysisModel: "fake"}}, Options{Now: func() time.Time { return now }})
			calls := 0
			s.model = monitorai.NewClient(auditHTTPClient(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 500, Body: io.NopCloser(strings.NewReader("unexpected"))}, nil
			}))
			task := createTask(t, s, "valid-snapshot")
			task.SurgeMode = mode
			if _, err := s.Update(t.Context(), task); err != nil {
				t.Fatal(err)
			}
			seed, _, err := repo.UpsertItem(t.Context(), task.ID, "seed", platformapi.Post{Platform: platformapi.Douyin, ExternalID: "one", Likes: 1000, Comments: 100}, now.Add(-85*time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			if err := repo.SetSurge(t.Context(), seed.ID, true, "previous hit", seed.LastSeenAt, 100, 10, 200); err != nil {
				t.Fatal(err)
			}
			read := true
			if _, err := repo.PatchItem(t.Context(), seed.ID, &read, nil); err != nil {
				t.Fatal(err)
			}
			run := auditRun(t, s, task, "missing")
			item, err := repo.GetItem(t.Context(), seed.ID)
			if err != nil || run.SurgeCount != 0 || calls != 0 || len(item.History) != 1 || !item.Surge || !item.Read || item.Likes != 1000 || item.LikeDelta != 100 {
				t.Fatalf("missing run=%+v item=%+v calls=%d err=%v", run, item, calls, err)
			}
			// First valid observation is 90 minutes after the baseline, not 5.
			now = now.Add(5 * time.Minute)
			post.CountsMissing, post.Likes, post.Comments = false, 1500, 100
			task.SurgeMode = "rule"
			if _, err := s.Update(t.Context(), task); err != nil {
				t.Fatal(err)
			}
			run = auditRun(t, s, task, "valid")
			item, err = repo.GetItem(t.Context(), seed.ID)
			if err != nil || run.SurgeCount != 0 || item.LikeDelta != 500 || item.LikeRatePerHour < 333 || item.LikeRatePerHour > 334 || len(item.History) != 2 || !item.Surge {
				t.Fatalf("last valid baseline run=%+v item=%+v err=%v", run, item, err)
			}
			now = now.Add(30 * time.Minute)
			auditRun(t, s, task, "second-miss")
			item, err = repo.GetItem(t.Context(), seed.ID)
			if err != nil || item.Surge {
				t.Fatalf("second real miss failed to clear: %+v %v", item, err)
			}
		})
	}
}

func TestFirstKnownCountsAfterMissingObservationDoNotSurge(t *testing.T) {
	now := time.Now().UTC()
	s, repo := fixture(t, referenceStub{search: func(context.Context, string, string, string, string, string) (platformapi.SearchPage, error) {
		return platformapi.SearchPage{Items: []platformapi.Post{{ExternalID: "one", Likes: 10000}}}, nil
	}}, settings.Runtime{}, Options{Now: func() time.Time { return now }})
	task := createTask(t, s, "no-baseline")
	if _, _, err := repo.UpsertItem(t.Context(), task.ID, "seed", platformapi.Post{Platform: platformapi.Douyin, ExternalID: "one", CountsMissing: true}, now.Add(-30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	run := auditRun(t, s, task, "first-known")
	if run.NewCount != 0 || run.SurgeCount != 0 {
		t.Fatalf("no valid baseline: %+v", run)
	}
}
