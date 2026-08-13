package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"video-production-console/internal/domain"
	"video-production-console/internal/store"
)

func TestImageProjectsHTTPRejectsIncompleteDownload(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	handler := NewImageProjectsHandler(db, imageRuntimeStub{}, nil)
	request := httptest.NewRequest(http.MethodPost, "/api/image-projects", strings.NewReader(`{"title":"x","script":"第一句。","image_count":1,"ratio":"3:4","style":"finance_documentary","concurrency":1}`))
	request.Header.Set("Content-Type", "application/json")
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, request)
	var detail domain.ImageProjectDetail
	if err := json.Unmarshal(created.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	download := httptest.NewRecorder()
	handler.ServeHTTP(download, httptest.NewRequest(http.MethodGet, "/api/image-projects/"+detail.Project.ID+"/download", nil))
	if download.Code != http.StatusConflict || !strings.Contains(download.Body.String(), "image_project_incomplete") {
		t.Fatalf("download status=%d body=%s", download.Code, download.Body.String())
	}
}
