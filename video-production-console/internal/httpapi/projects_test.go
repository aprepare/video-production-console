package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"video-production-console/internal/assets"
	"video-production-console/internal/domain"
	"video-production-console/internal/store"
)

type failingProjectStore struct {
	getErr, listErr, backgroundErr, moveErr error
	stage                                   domain.ProjectStage
	addState                                store.CommitState
	addErr                                  error
}

func (s *failingProjectStore) CreateProject(context.Context, domain.Project) error { return nil }
func (s *failingProjectStore) ListProjects(context.Context, string, domain.ProjectStage, string) ([]domain.Project, error) {
	return nil, nil
}
func (s *failingProjectStore) GetProject(context.Context, string) (domain.Project, error) {
	stage := s.stage
	if stage == "" {
		stage = domain.StageAssets
	}
	return domain.Project{ID: uuid.NewString(), Stage: stage}, s.getErr
}
func (s *failingProjectStore) MoveProject(context.Context, string, domain.ProjectStage, domain.ProjectStage, time.Time) (domain.Project, error) {
	if s.moveErr != nil {
		return domain.Project{}, s.moveErr
	}
	panic("must not move after failed pre-read")
}
func (s *failingProjectStore) SetTopicCardPath(context.Context, string, string, time.Time) error {
	return nil
}
func (s *failingProjectStore) AddAsset(context.Context, *domain.Asset) (store.CommitState, error) {
	if s.addErr != nil {
		return s.addState, s.addErr
	}
	return store.CommitCommitted, nil
}
func (s *failingProjectStore) ListAssets(context.Context, string) ([]domain.Asset, error) {
	return nil, s.listErr
}
func (s *failingProjectStore) Background(context.Context, string) (domain.Asset, error) {
	return domain.Asset{}, s.backgroundErr
}

func TestProjectReadAndMoveReturn500ForDatabasePreReadFailures(t *testing.T) {
	id := uuid.NewString()
	dbErr := errors.New("database unavailable")
	for _, tt := range []struct {
		name  string
		store *failingProjectStore
		path  string
	}{
		{"detail get", &failingProjectStore{getErr: dbErr}, "/api/projects/" + id},
		{"detail assets", &failingProjectStore{listErr: dbErr}, "/api/projects/" + id},
		{"detail background", &failingProjectStore{backgroundErr: dbErr}, "/api/projects/" + id},
		{"move get", &failingProjectStore{getErr: dbErr}, "/api/projects/" + id + "/move"},
		{"move assets", &failingProjectStore{listErr: dbErr}, "/api/projects/" + id + "/move"},
		{"move background", &failingProjectStore{backgroundErr: dbErr}, "/api/projects/" + id + "/move"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newProjectsHandler(tt.store, assets.NewService(t.TempDir()))
			method := http.MethodGet
			var input any
			if strings.Contains(tt.path, "/move") {
				method = http.MethodPost
				input = map[string]string{"stage": "mixing"}
			}
			r := performJSON(t, h, method, tt.path, input)
			if r.StatusCode != http.StatusInternalServerError {
				t.Fatalf("status=%d", r.StatusCode)
			}
		})
	}
}

func TestProjectReadTreatsOnlySQLNoRowsAsMissingOptionalData(t *testing.T) {
	id := uuid.NewString()
	h := newProjectsHandler(&failingProjectStore{listErr: sql.ErrNoRows, backgroundErr: sql.ErrNoRows}, assets.NewService(t.TempDir()))
	response := performJSON(t, h, http.MethodGet, "/api/projects/"+id, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("detail status=%d", response.StatusCode)
	}
	move := performJSON(t, h, http.MethodPost, "/api/projects/"+id+"/move", map[string]string{"stage": "mixing"})
	if move.StatusCode != http.StatusConflict {
		t.Fatalf("move missing-assets status=%d", move.StatusCode)
	}
}

func TestMoveMapsConcurrentStageConflict(t *testing.T) {
	id := uuid.NewString()
	h := newProjectsHandler(&failingProjectStore{stage: domain.StageTopic, moveErr: store.ErrProjectStageConflict}, assets.NewService(t.TempDir()))
	r := performJSON(t, h, http.MethodPost, "/api/projects/"+id+"/move", map[string]string{"stage": "script"})
	if r.StatusCode != http.StatusConflict {
		t.Fatalf("status=%d", r.StatusCode)
	}
}

func TestProjectUploadKeepsFileAndReturnsRecoverableErrorForUnknownCommit(t *testing.T) {
	root := t.TempDir()
	id := uuid.NewString()
	repository := &failingProjectStore{addState: store.CommitUnknown, addErr: &store.CommitOutcomeError{Outcome: store.CommitUnknown, Err: errors.New("lost commit acknowledgement")}}
	h := newProjectsHandler(repository, assets.NewService(root))
	response := uploadProjectFile(t, h, id, "continuous_script", "script.txt", []byte("hello"))
	defer response.Body.Close()
	assertAPIError(t, response, http.StatusServiceUnavailable, "asset_commit_unknown")
	directory := filepath.Join(root, "projects", id, "continuous_script")
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 {
		t.Fatalf("saved file entries=%v err=%v", entries, err)
	}
}

func TestProjectViewIncludesLifecycleFields(t *testing.T) {
	now := time.Now().UTC()
	note := "published manually"
	view := toProjectView(domain.Project{ReadyAt: &now, PublishedAt: &now, PublishNote: &note})
	data, _ := json.Marshal(view)
	for _, field := range []string{"ready_at", "published_at", "publish_note"} {
		if !bytes.Contains(data, []byte(`"`+field+`"`)) {
			t.Fatalf("response missing %s: %s", field, data)
		}
	}
}

func TestDefaultProjectAndAssetViewsDoNotExposeStoragePaths(t *testing.T) {
	projectPath := "C:\\private\\topic.png"
	projectJSON, _ := json.Marshal(toProjectView(domain.Project{TopicCardPath: &projectPath}))
	assetJSON, _ := json.Marshal(toAssetView(domain.Asset{Path: "C:\\private\\video.mp4"}))
	if bytes.Contains(projectJSON, []byte("topic_card_path")) || bytes.Contains(projectJSON, []byte("C:\\\\private")) {
		t.Fatalf("project leaked path: %s", projectJSON)
	}
	if bytes.Contains(assetJSON, []byte(`"path"`)) || bytes.Contains(assetJSON, []byte("C:\\\\private")) {
		t.Fatalf("asset leaked path: %s", assetJSON)
	}
}

func TestMoveRejectsOversizedAndTrailingJSON(t *testing.T) {
	id := uuid.NewString()
	h := newProjectsHandler(&failingProjectStore{stage: domain.StageTopic}, assets.NewService(t.TempDir()))
	for _, tt := range []struct {
		name, body string
		want       int
	}{{"oversized", `{"stage":"script","padding":"` + strings.Repeat("x", 70<<10) + `"}`, http.StatusRequestEntityTooLarge}, {"trailing", `{"stage":"script"} {}`, http.StatusBadRequest}} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/projects/"+id+"/move", strings.NewReader(tt.body))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != tt.want {
				t.Fatalf("status=%d", w.Code)
			}
		})
	}
}

func TestProjectLifecycleUploadVersionsAndGates(t *testing.T) {
	handler, db, root, accountID := newProjectsTestHandler(t, "active")
	created := projectJSON(t, performJSON(t, handler, http.MethodPost, "/api/projects", map[string]any{"account_id": accountID}))
	if created.Stage != "topic" || created.Title == "" {
		t.Fatalf("created = %+v", created)
	}
	if oversized := uploadProjectFile(t, handler, created.ID, "continuous_script", "large.txt", bytes.Repeat([]byte("x"), (6<<20))); oversized.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized script status=%d", oversized.StatusCode)
	}

	first := uploadProjectFile(t, handler, created.ID, "continuous_script", "script.md", []byte("# 第一版"))
	second := uploadProjectFile(t, handler, created.ID, "continuous_script", "script.txt", []byte("第二版"))
	if first.StatusCode != http.StatusCreated || second.StatusCode != http.StatusCreated {
		t.Fatalf("upload statuses = %d, %d", first.StatusCode, second.StatusCode)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM asset_versions WHERE project_id=? AND type='continuous_script'`, created.ID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("versions count=%d err=%v", count, err)
	}
	var path string
	_ = db.QueryRow(`SELECT path FROM asset_versions WHERE project_id=? ORDER BY version DESC LIMIT 1`, created.ID).Scan(&path)
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
	}{{"audio", "voice.mp3", validProjectMP3()}, {"subtitle", "captions.srt", []byte("1\n00:00:00,000 --> 00:00:01,000\n字幕\n")}} {
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
	bgPath := filepath.Join(root, "bg.png")
	if _, err = store.NewAccountRepository(db).CreateWithBackground(context.Background(), domain.Account{ID: id, Name: "account", Color: "#fff", Status: "active", CreatedAt: now, UpdatedAt: now}, store.NewBackground{ID: uuid.NewString(), Path: bgPath, Filename: "bg.png", MIMEType: "image/png", Size: 1, SHA256: "sha"}); err != nil {
		t.Fatal(err)
	}
	if status != "active" {
		if _, err = db.Exec(`UPDATE accounts SET status=? WHERE id=?`, status, id); err != nil {
			t.Fatal(err)
		}
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

func validProjectMP3() []byte {
	frame := make([]byte, 417)
	copy(frame, []byte{0xff, 0xfb, 0x90, 0x64})
	return append(append([]byte{}, frame...), frame...)
}
