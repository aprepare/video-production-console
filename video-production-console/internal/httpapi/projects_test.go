package httpapi

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"video-production-console/internal/assets"
	"video-production-console/internal/store"
)

func TestProjectLifecycleUploadVersionsAndGates(t *testing.T) {
	handler, db, root, accountID := newProjectsTestHandler(t, "active")
	created := projectJSON(t, performJSON(t, handler, http.MethodPost, "/api/projects", map[string]any{"account_id": accountID}))
	if created.Stage != "topic" || created.Title == "" {
		t.Fatalf("created = %+v", created)
	}

	first := uploadProjectFile(t, handler, created.ID, "continuous_script", "script.md", []byte("# 第一版"))
	second := uploadProjectFile(t, handler, created.ID, "continuous_script", "script.txt", []byte("第二版"))
	if first.StatusCode != http.StatusCreated || second.StatusCode != http.StatusCreated {
		t.Fatalf("upload statuses = %d, %d", first.StatusCode, second.StatusCode)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM assets WHERE project_id=? AND type='continuous_script'`, created.ID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("versions count=%d err=%v", count, err)
	}
	var path string
	_ = db.QueryRow(`SELECT path FROM assets WHERE project_id=? ORDER BY version DESC LIMIT 1`, created.ID).Scan(&path)
	wantRoot := filepath.Join(root, "projects", created.ID, "continuous_script")
	if filepath.Dir(path) != wantRoot {
		t.Fatalf("asset path=%q want root=%q", path, wantRoot)
	}

	for _, stage := range []string{"script", "assets"} {
		response := performJSON(t, handler, http.MethodPost, "/api/projects/"+created.ID+"/move", map[string]string{"stage": stage})
		if response.StatusCode != http.StatusOK {
			t.Fatalf("move %s status=%d", stage, response.StatusCode)
		}
	}
	blocked := performJSON(t, handler, http.MethodPost, "/api/projects/"+created.ID+"/move", map[string]string{"stage": "mixing"})
	if blocked.StatusCode != http.StatusConflict {
		t.Fatalf("blocked status=%d", blocked.StatusCode)
	}
	var failure struct {
		Details struct {
			Missing []string `json:"missing_assets"`
		} `json:"details"`
	}
	_ = json.NewDecoder(blocked.Body).Decode(&failure)
	if len(failure.Details.Missing) != 2 {
		t.Fatalf("missing=%v", failure.Details.Missing)
	}

	for _, file := range []struct {
		typ, name string
		data      []byte
	}{{"audio", "voice.mp3", append([]byte("ID3"), make([]byte, 16)...)}, {"subtitle", "captions.srt", []byte("1\n00:00:00,000 --> 00:00:01,000\n字幕\n")}} {
		if r := uploadProjectFile(t, handler, created.ID, file.typ, file.name, file.data); r.StatusCode != http.StatusCreated {
			t.Fatalf("upload %s=%d", file.typ, r.StatusCode)
		}
	}
	if _, err := db.Exec(`UPDATE accounts SET status='inactive' WHERE id=?`, accountID); err != nil {
		t.Fatal(err)
	}
	if r := performJSON(t, handler, http.MethodPost, "/api/projects/"+created.ID+"/move", map[string]string{"stage": "mixing"}); r.StatusCode != http.StatusOK {
		t.Fatalf("move mixing with inherited inactive-account background=%d", r.StatusCode)
	}

	detail := performJSON(t, handler, http.MethodGet, "/api/projects/"+created.ID, nil)
	if detail.StatusCode != http.StatusOK {
		t.Fatalf("detail=%d", detail.StatusCode)
	}
	var body struct {
		Assets              map[string]json.RawMessage   `json:"assets"`
		AssetHistory        map[string][]json.RawMessage `json:"asset_history"`
		BackgroundReference json.RawMessage              `json:"background_reference"`
		Missing             []string                     `json:"missing_assets"`
	}
	_ = json.NewDecoder(detail.Body).Decode(&body)
	if len(body.AssetHistory["continuous_script"]) != 2 || len(body.BackgroundReference) == 0 {
		t.Fatalf("detail=%+v", body)
	}

	bad := uploadProjectFile(t, handler, created.ID, "final_video", "fake.mp4", []byte("not video"))
	if bad.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad MIME status=%d", bad.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(root, "projects", created.ID, "final_video")); err == nil {
		entries, _ := os.ReadDir(filepath.Join(root, "projects", created.ID, "final_video"))
		if len(entries) != 0 {
			t.Fatalf("invalid upload artifacts=%v", entries)
		}
	}
}

func TestCreateAndFilterProjectsRequireActiveAccount(t *testing.T) {
	handler, db, _, activeID := newProjectsTestHandler(t, "active")
	a := projectJSON(t, performJSON(t, handler, http.MethodPost, "/api/projects", map[string]any{"account_id": activeID, "title": "Alpha"}))
	_ = a
	response := performJSON(t, handler, http.MethodGet, "/api/projects?account_id="+activeID+"&stage=topic&q=alp", nil)
	var list []projectResponse
	_ = json.NewDecoder(response.Body).Decode(&list)
	if response.StatusCode != http.StatusOK || len(list) != 1 {
		t.Fatalf("filter status=%d list=%+v", response.StatusCode, list)
	}
	inactive := uuid.NewString()
	now := time.Now().UTC()
	_, _ = db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,'#fff','inactive',?,?)`, inactive, "inactive", now, now)
	rejected := performJSON(t, handler, http.MethodPost, "/api/projects", map[string]any{"account_id": inactive})
	if rejected.StatusCode != http.StatusConflict {
		t.Fatalf("inactive create status=%d", rejected.StatusCode)
	}
}

func newProjectsTestHandler(t *testing.T, status string) (http.Handler, *sql.DB, string, string) {
	t.Helper()
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	id := uuid.NewString()
	now := time.Now().UTC()
	bgID := uuid.NewString()
	bgPath := filepath.Join(root, "bg.png")
	_, err = db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,'#fff',?,?,?)`, id, "account", status, now, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO assets(id,project_id,account_id,type,path,filename,mime_type,size,sha256,version,status,created_at) VALUES(?,NULL,?,'account_background',?,'bg.png','image/png',1,'sha',1,'active',?)`, bgID, id, bgPath, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`UPDATE accounts SET background_asset_id=? WHERE id=?`, bgID, id)
	if err != nil {
		t.Fatal(err)
	}
	return NewProjectsHandler(db, assets.NewService(root)), db, root, id
}

type projectResponse struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Stage string `json:"stage"`
}

func projectJSON(t *testing.T, r *http.Response) projectResponse {
	t.Helper()
	defer r.Body.Close()
	if r.StatusCode != http.StatusCreated {
		t.Fatalf("create status=%d", r.StatusCode)
	}
	var p projectResponse
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		t.Fatal(err)
	}
	return p
}
func performJSON(t *testing.T, h http.Handler, method, target string, value any) *http.Response {
	t.Helper()
	var body bytes.Buffer
	if value != nil {
		_ = json.NewEncoder(&body).Encode(value)
	}
	req := httptest.NewRequest(method, target, &body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w.Result()
}
func uploadProjectFile(t *testing.T, h http.Handler, id, typ, name string, data []byte) *http.Response {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", name)
	_, _ = part.Write(data)
	_ = writer.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/projects/"+id+"/assets/"+typ, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w.Result()
}
