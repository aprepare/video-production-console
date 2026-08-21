package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/domain"
	"video-production-console/internal/imageproject"
	"video-production-console/internal/imagevideo"
	"video-production-console/internal/narration"
	"video-production-console/internal/store"
)

func TestImageVideosCreatePlansImagesFromNarrationTiming(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "image-videos.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	accountID := insertVideoAccount(t, db)
	script := "第一句旁白内容。第二句旁白内容。"
	handler := newImageProjectsHandler(db, quickRuntime(t), succeedingImageGenerator(t), failingVideoPlanner{}, nil, nil, fakeVideoNarration(script))
	body, _ := json.Marshal(map[string]any{
		"script": script, "account_id": accountID, "output_mode": "image_slideshow",
		"idempotency_key": uuid.NewString(), "concurrency": 2,
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/image-videos", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	repo := store.NewImageProjectRepository(db)
	project, items := waitForImageRun(t, repo, payload["project_id"], 3*time.Second, func(got domain.ImageProject, gotItems []domain.ImageProjectItem) bool {
		return got.RunStatus == "completed" && len(gotItems) >= 2
	})
	if project.RunMode != "video" || project.Ratio != "9:16" || project.Style != "red_ink" {
		t.Fatalf("project=%+v", project)
	}
	if project.ImageCount < 2 || len(items) < 2 {
		t.Fatalf("expected timed scenes, got %d items", len(items))
	}
	listed := httptest.NewRecorder()
	handler.ServeHTTP(listed, httptest.NewRequest(http.MethodGet, "/api/image-projects", nil))
	var zipProjects []domain.ImageProject
	if err := json.Unmarshal(listed.Body.Bytes(), &zipProjects); err != nil {
		t.Fatal(err)
	}
	if len(zipProjects) != 0 {
		t.Fatalf("zip list leaked video projects: %+v", zipProjects)
	}
	videos := httptest.NewRecorder()
	handler.ServeHTTP(videos, httptest.NewRequest(http.MethodGet, "/api/image-videos", nil))
	var videoProjects []domain.ImageProject
	if err := json.Unmarshal(videos.Body.Bytes(), &videoProjects); err != nil {
		t.Fatal(err)
	}
	if len(videoProjects) != 1 || videoProjects[0].ID != project.ID {
		t.Fatalf("video list=%+v", videoProjects)
	}
}

func TestImageVideosGetHidesZipProjects(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "image-videos-hide.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := store.NewImageProjectRepository(db)
	now := time.Now().UTC()
	id := uuid.NewString()
	if err := repo.Create(context.Background(), domain.ImageProject{ID: id, Title: "zip", Script: "文案。", ImageCount: 1, Ratio: "3:4", Style: "red_ink", Concurrency: 1, Status: "draft", CreatedAt: now, UpdatedAt: now}, nil); err != nil {
		t.Fatal(err)
	}
	handler := NewImageProjectsHandler(db, quickRuntime(t), succeedingImageGenerator(t))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/image-videos/"+id, nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

type failingVideoPlanner struct{}

func (failingVideoPlanner) Complete(context.Context, imageproject.ChatRequest) (string, error) {
	return "", errString("planner unavailable")
}

func insertVideoAccount(t *testing.T, db *sql.DB) string {
	t.Helper()
	id := uuid.NewString()
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,'#fff','active',?,?)`, id, "video-"+id[:8], now, now); err != nil {
		t.Fatal(err)
	}
	return id
}

func fakeVideoNarration(script string) func(context.Context, domain.ImageProject) (imagevideo.NarrationArtifact, error) {
	return func(_ context.Context, project domain.ImageProject) (imagevideo.NarrationArtifact, error) {
		words := []narration.Word{
			{Text: "第一句旁白内容。", StartTime: 0, EndTime: 4.45},
			{Text: "第二句旁白内容。", StartTime: 4.5, EndTime: 8.95},
		}
		doc, err := narration.NewWordTimingDocument(script, "test", "", words)
		if err != nil {
			return imagevideo.NarrationArtifact{}, err
		}
		if project.Script != script {
			return imagevideo.NarrationArtifact{}, errString("unexpected script")
		}
		return imagevideo.NarrationArtifact{TimingDocument: doc, DurationUS: int64(doc.Duration * 1_000_000)}, nil
	}
}
