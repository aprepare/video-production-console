package httpapi

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/codex"
	"video-production-console/internal/domain"
	consoleSettings "video-production-console/internal/settings"
	"video-production-console/internal/store"
)

type manifestTestSettings struct{ runtime consoleSettings.Runtime }

func (s manifestTestSettings) Runtime(context.Context) (consoleSettings.Runtime, error) {
	return s.runtime, nil
}

type manifestTestSkills struct{ snapshot domain.SkillSnapshot }

func (s manifestTestSkills) Latest(context.Context, string) (domain.SkillSnapshot, error) {
	return s.snapshot, nil
}

type manifestTestScheduler struct{ enqueued int }

func (s *manifestTestScheduler) Enqueue(context.Context, domain.CodexTask) error {
	s.enqueued++
	return nil
}
func (*manifestTestScheduler) Resume(context.Context, string, string) error { return nil }
func (*manifestTestScheduler) Cancel(context.Context, string) error         { return nil }
func (*manifestTestScheduler) SetLimit(int) error                           { return nil }
func (*manifestTestScheduler) Snapshot() codex.SchedulerSnapshot            { return codex.SchedulerSnapshot{} }
func (*manifestTestScheduler) Close()                                       {}

func setupManifestTask(t *testing.T, withSource bool) (*sqlDBForManifestTest, string, string, string) {
	t.Helper()
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	accountID, projectID := uuid.NewString(), uuid.NewString()
	now := time.Now().UTC()
	background := filepath.Join(root, "accounts", accountID, "background", "bg.png")
	if err := os.MkdirAll(filepath.Dir(background), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(background, []byte("background"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.NewAccountRepository(db).CreateWithBackground(context.Background(), domain.Account{ID: accountID, Name: "manifest", Color: "#fff", Status: "active", CreatedAt: now, UpdatedAt: now}, store.NewBackground{ID: uuid.NewString(), Path: background, Filename: "bg.png", MIMEType: "image/png", Size: 10, SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}); err != nil {
		t.Fatal(err)
	}
	if err := store.NewProjectRepository(db).CreateProject(context.Background(), domain.Project{ID: projectID, AccountID: accountID, Title: "test", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if !withSource {
		return &sqlDBForManifestTest{db: db, root: root}, accountID, projectID, root
	}
	path := filepath.Join(root, "projects", projectID, "source_script", "source.txt")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data := []byte("source script")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(data)
	if _, err := store.NewAssetRepository(db).AddVersion(context.Background(), store.AddAssetVersion{ProjectID: &projectID, AccountID: accountID, Type: domain.AssetSourceScript, Path: path, Filename: "source.txt", MIMEType: "text/plain", Size: int64(len(data)), SHA256: hex.EncodeToString(hash[:])}); err != nil {
		t.Fatal(err)
	}
	return &sqlDBForManifestTest{db: db, root: root}, accountID, projectID, root
}

// Small wrapper keeps the helper return values readable without leaking the
// concrete database type into unrelated test fixtures.
type sqlDBForManifestTest struct {
	db   *sql.DB
	root string
}

func TestTaskManifestPreparerWritesEnhancedRemixManifest(t *testing.T) {
	db, accountID, projectID, root := setupManifestTask(t, true)
	preparer := &taskManifestPreparer{projects: store.NewProjectRepository(db.db), assets: store.NewAssetRepository(db.db), settings: manifestTestSettings{runtime: consoleSettings.Runtime{PublicSettings: domain.PublicSettings{DataRoot: root, MaxCodexConcurrency: 2}}}, skills: manifestTestSkills{snapshot: domain.SkillSnapshot{ID: uuid.NewString(), Name: "finance-viral-remix"}}}
	task := domain.CodexTask{ID: uuid.NewString(), ProjectID: &projectID, AccountID: accountID, Action: domain.ActionRemixEnhanced, Type: "remix"}
	if err := preparer.Prepare(context.Background(), task, TaskManifestRequest{}); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, "projects", projectID, "tasks", task.ID, "task_manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest codex.TaskManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Action != domain.ActionRemixEnhanced || manifest.Skill != "finance-viral-remix" || len(manifest.Inputs) != 1 || manifest.Inputs[0].Role != "primary_source" {
		t.Fatalf("manifest=%+v", manifest)
	}
}

func TestTaskHTTPRejectsMissingRemixAssetBeforeEnqueue(t *testing.T) {
	db, accountID, projectID, root := setupManifestTask(t, false)
	preparer := &taskManifestPreparer{projects: store.NewProjectRepository(db.db), assets: store.NewAssetRepository(db.db), settings: manifestTestSettings{runtime: consoleSettings.Runtime{PublicSettings: domain.PublicSettings{DataRoot: root, MaxCodexConcurrency: 2}}}, skills: manifestTestSkills{snapshot: domain.SkillSnapshot{ID: uuid.NewString(), Name: "finance-viral-remix"}}}
	scheduler := &manifestTestScheduler{}
	handler := NewTasksHandler(db.db, scheduler, preparer)
	req := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/tasks", strings.NewReader(`{"account_id":"`+accountID+`","type":"remix","action":"remix.enhanced","prompt":"go"}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusConflict || scheduler.enqueued != 0 {
		t.Fatalf("status=%d enqueued=%d body=%s", recorder.Code, scheduler.enqueued, recorder.Body.String())
	}
}
