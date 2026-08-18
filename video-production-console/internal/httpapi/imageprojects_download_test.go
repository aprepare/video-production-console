package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"video-production-console/internal/domain"
	consoleSettings "video-production-console/internal/settings"
	"video-production-console/internal/store"
)

func TestImageProjectsHTTPRejectsIncompleteDownload(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	handler := testImageHandler(db, imageRuntimeStub{runtime: consoleSettings.Runtime{PublicSettings: domain.PublicSettings{GrokBaseURL: "http://127.0.0.1:3030", GrokModel: "grok-test"}, GrokAPIKey: "configured"}}, nil)
	request := httptest.NewRequest(http.MethodPost, "/api/image-projects", bytes.NewReader(imageCreatePayload("x", "第一句。", map[string]any{"image_count": 1})))
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
