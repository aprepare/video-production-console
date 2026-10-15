package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"video-production-console/internal/monitorai"
	"video-production-console/internal/refanalysis"
	"video-production-console/internal/store"
)

type auditCoverReferences struct {
	referenceStub
	cover func(context.Context, string, string) ([]byte, error)
}

func (r auditCoverReferences) ScoreCover(ctx context.Context, platform, url string) ([]byte, error) {
	return r.cover(ctx, platform, url)
}

type auditHTTPClient func(*http.Request) (*http.Response, error)

func (f auditHTTPClient) Do(r *http.Request) (*http.Response, error) { return f(r) }
func auditModelResponse(content string) *http.Response {
	raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}}}})
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(raw)))}
}

func TestScoreCoverParallelBudgetAndFailureIsolation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var active, peak atomic.Int32
		start := time.Now()
		refs := auditCoverReferences{cover: func(ctx context.Context, _, url string) ([]byte, error) {
			n := active.Add(1)
			defer active.Add(-1)
			for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
			}
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) > 10*time.Second {
				t.Error("cover does not have a 10 second timeout")
			}
			if url == "cover-0" {
				time.Sleep(2 * time.Second)
				return []byte{255, 216, 255, 217}, nil
			}
			<-ctx.Done()
			return nil, ctx.Err()
		}}
		client := auditHTTPClient(func(r *http.Request) (*http.Response, error) {
			deadline, ok := r.Context().Deadline()
			if !ok || time.Until(deadline) != 90*time.Second {
				t.Errorf("model budget=%v", time.Until(deadline))
			}
			body, _ := io.ReadAll(r.Body)
			if strings.Count(string(body), "data:image/jpeg;base64,") != 1 {
				t.Errorf("successful cover lost or failed covers attached: %s", body)
			}
			return auditModelResponse("模型分析如下：\n```json\n{\"items\":[{\"external_id\":\"0\",\"score\":80,\"reason\":\"可用\"}]}\n```"), nil
		})
		s := &Service{references: refs, model: monitorai.NewClient(client)}
		items := make([]store.MonitorItem, 10)
		for i := range items {
			items[i] = store.MonitorItem{ExternalID: fmt.Sprint(i), CoverURL: fmt.Sprintf("cover-%d", i)}
		}
		result, err := s.scoreBatch(t.Context(), refanalysis.ModelConfig{BaseURL: "https://model.invalid", Model: "fake"}, DefaultTask(), items)
		if err != nil || len(result) != 1 {
			t.Fatalf("result=%+v err=%v", result, err)
		}
		if peak.Load() != 4 || active.Load() != 0 || time.Since(start) > 30*time.Second {
			t.Fatalf("peak=%d active=%d elapsed=%v", peak.Load(), active.Load(), time.Since(start))
		}
	})
}

func TestScoreCoverTotalBudgetAndParentCancellation(t *testing.T) {
	for _, cancelParent := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelParent), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				start := time.Now()
				ctx := t.Context()
				if cancelParent {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
					defer cancel()
				}
				var modelCalls atomic.Int32
				s := &Service{references: auditCoverReferences{cover: func(ctx context.Context, _, _ string) ([]byte, error) { <-ctx.Done(); return nil, ctx.Err() }}, model: monitorai.NewClient(auditHTTPClient(func(r *http.Request) (*http.Response, error) {
					modelCalls.Add(1)
					if err := r.Context().Err(); err != nil {
						return nil, err
					}
					return auditModelResponse(`{"items":[]}`), nil
				}))}
				items := make([]store.MonitorItem, 20)
				for i := range items {
					items[i] = store.MonitorItem{ExternalID: fmt.Sprint(i), CoverURL: "slow"}
				}
				_, err := s.scoreBatch(ctx, refanalysis.ModelConfig{BaseURL: "https://model.invalid", Model: "fake"}, DefaultTask(), items)
				budget := 30 * time.Second
				if cancelParent {
					budget = 5 * time.Second
					if err == nil {
						t.Error("parent cancellation ignored")
					}
				} else if err != nil {
					t.Fatal(err)
				}
				if time.Since(start) > budget {
					t.Fatalf("elapsed=%v budget=%v", time.Since(start), budget)
				}
			})
		})
	}
}

func TestScoreImageFallbackStatusesAndFreshContext(t *testing.T) {
	for _, status := range []int{400, 413, 415, 422, 401, 403, 404, 429, 500, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				calls := 0
				client := auditHTTPClient(func(r *http.Request) (*http.Response, error) {
					calls++
					deadline, _ := r.Context().Deadline()
					if time.Until(deadline) != 90*time.Second {
						t.Errorf("attempt %d has %v budget", calls, time.Until(deadline))
					}
					body, _ := io.ReadAll(r.Body)
					if calls == 1 {
						if !strings.Contains(string(body), "image_url") {
							t.Error("first call lacks cover")
						}
						time.Sleep(80 * time.Second)
						return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("image unsupported secret-token"))}, nil
					}
					if strings.Contains(string(body), "image_url") {
						t.Error("text retry still has image")
					}
					time.Sleep(20 * time.Second)
					if err := r.Context().Err(); err != nil {
						return nil, err
					}
					return auditModelResponse(`{"items":[{"external_id":"one","score":90,"reason":"可用"}]}`), nil
				})
				s := &Service{references: referenceStub{}, model: monitorai.NewClient(client)}
				_, err := s.scoreBatch(t.Context(), refanalysis.ModelConfig{BaseURL: "https://model.invalid", Model: "fake"}, DefaultTask(), []store.MonitorItem{{ExternalID: "one", CoverURL: "image"}})
				fallback := status == 400 || status == 413 || status == 415 || status == 422
				if fallback && (calls != 2 || err != nil) {
					t.Fatalf("calls=%d err=%v", calls, err)
				}
				if !fallback && (calls != 1 || err == nil) {
					t.Fatalf("unwanted fallback: calls=%d err=%v", calls, err)
				}
			})
		})
	}
}
