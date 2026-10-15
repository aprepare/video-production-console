package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"video-production-console/internal/commentinsight"
	"video-production-console/internal/refanalysis"
	"video-production-console/internal/store"
)

type commentInsightsStub struct {
	created bool
	err     error
}

func (s *commentInsightsStub) Start(context.Context, string, string) (store.CommentInsight, bool, error) {
	return store.CommentInsight{ID: "insight", ReferenceID: "ref", Status: "queued", Model: "m", CreatedAt: time.Unix(1, 0).UTC(), UpdatedAt: time.Unix(1, 0).UTC()}, s.created, s.err
}
func (s *commentInsightsStub) Get(context.Context, string) (commentinsight.Insight, error) {
	if s.err != nil {
		return commentinsight.Insight{}, s.err
	}
	return commentinsight.Insight{CommentInsight: store.CommentInsight{ID: "insight", ReferenceID: "ref", Status: "completed"}, Report: &commentinsight.Report{Summary: "ok"}}, nil
}
func (s *commentInsightsStub) List(context.Context, string) ([]store.CommentInsight, error) {
	if s.err != nil {
		return nil, s.err
	}
	return []store.CommentInsight{{ID: "insight", ReferenceID: "ref", Status: "completed"}}, nil
}
func (s *commentInsightsStub) Delete(context.Context, string) error { return s.err }

func serveCommentInsights(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	h.ServeHTTP(w, r)
	return w
}

func TestCommentInsightsHTTPRoutes(t *testing.T) {
	service := &commentInsightsStub{created: true}
	h := NewCommentInsightsHandler(service)
	if got := serveCommentInsights(h, "POST", "/api/comment-insights", `{"reference_id":"ref","model":"m"}`); got.Code != 202 || !strings.Contains(got.Body.String(), `"status":"queued"`) {
		t.Fatalf("POST: %d %s", got.Code, got.Body.String())
	}
	service.created = false
	if got := serveCommentInsights(h, "POST", "/api/comment-insights", `{"reference_id":"ref"}`); got.Code != 200 {
		t.Fatalf("deduplicated POST: %d %s", got.Code, got.Body.String())
	}
	if got := serveCommentInsights(h, "GET", "/api/comment-insights?reference_id=ref", ""); got.Code != 200 || !strings.HasPrefix(got.Body.String(), "[") || strings.Contains(got.Body.String(), `"report"`) {
		t.Fatalf("LIST: %d %s", got.Code, got.Body.String())
	}
	if got := serveCommentInsights(h, "GET", "/api/comment-insights/insight", ""); got.Code != 200 || !strings.Contains(got.Body.String(), `"report":{"summary":"ok"`) {
		t.Fatalf("GET: %d %s", got.Code, got.Body.String())
	}
	if got := serveCommentInsights(h, "DELETE", "/api/comment-insights/insight", ""); got.Code != 204 {
		t.Fatalf("DELETE: %d %s", got.Code, got.Body.String())
	}
}

func TestCommentInsightsHTTPErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		method string
		path   string
		body   string
		status int
		code   string
	}{
		{"unsupported", commentinsight.ErrUnsupported, "POST", "/api/comment-insights", `{"reference_id":"ref"}`, 400, "insight_unsupported"},
		{"invalid", commentinsight.ErrInvalidInput, "POST", "/api/comment-insights", `{"reference_id":"ref"}`, 400, "invalid_comment_insight"},
		{"not configured", refanalysis.ErrNotConfigured, "POST", "/api/comment-insights", `{"reference_id":"ref"}`, 409, "analysis_not_configured"},
		{"reference missing", store.ErrReferenceNotFound, "POST", "/api/comment-insights", `{"reference_id":"ref"}`, 404, "reference_not_found"},
		{"insight missing", store.ErrCommentInsightNotFound, "GET", "/api/comment-insights/missing", "", 404, "comment_insight_not_found"},
		{"delete missing", store.ErrCommentInsightNotFound, "DELETE", "/api/comment-insights/missing", "", 404, "comment_insight_not_found"},
		{"internal", errors.New("secret details"), "GET", "/api/comment-insights?reference_id=ref", "", 500, "comment_insight_failed"},
		{"bad json", nil, "POST", "/api/comment-insights", `{`, 400, "invalid_comment_insight"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := serveCommentInsights(NewCommentInsightsHandler(&commentInsightsStub{err: tc.err}), tc.method, tc.path, tc.body)
			if got.Code != tc.status || !strings.Contains(got.Body.String(), tc.code) || strings.Contains(got.Body.String(), "secret details") {
				t.Fatalf("response: %d %s", got.Code, got.Body.String())
			}
		})
	}
}
