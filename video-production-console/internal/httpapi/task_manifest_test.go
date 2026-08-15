package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
	"video-production-console/internal/logging"
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

type manifestTestScheduler struct {
	enqueued int
	task     domain.CodexTask
}

func (s *manifestTestScheduler) Enqueue(_ context.Context, task domain.CodexTask) error {
	s.enqueued++
	s.task = task
	return nil
}

func TestRemixReviewPersistsRevisionNotes(t *testing.T) {
	db, accountID, projectID, _ := setupManifestTask(t, false)
	now := time.Now().UTC()
	path := filepath.Join(db.root, "projects", projectID, "continuous_script", "script.txt")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data := []byte("continuous script")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(data)
	if _, err := store.NewAssetRepository(db.db).AddVersion(context.Background(), store.AddAssetVersion{
		ProjectID: &projectID, AccountID: accountID, Type: domain.AssetContinuousScript,
		Path: path, Filename: "script.txt", MIMEType: "text/plain", Size: int64(len(data)), SHA256: hex.EncodeToString(hash[:]),
	}); err != nil {
		t.Fatal(err)
	}
	_ = now
	scheduler := &manifestTestScheduler{}
	handler := NewTasksHandler(db.db, scheduler, nil, nil)
	body := `{"account_id":"` + accountID + `","type":"remix","action":"remix.review","prompt":"rewrite","revision_notes":"语气更口语","model":"gpt-5.6-sol"}`
	req := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/tasks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusCreated || scheduler.enqueued != 1 {
		t.Fatalf("status=%d enqueued=%d body=%s", res.Code, scheduler.enqueued, res.Body.String())
	}
	notes, err := store.NewProjectStepNotesRepository(db.db).Get(context.Background(), projectID, "remix")
	if err != nil {
		t.Fatal(err)
	}
	if notes.Notes != "语气更口语" {
		t.Fatalf("notes=%q", notes.Notes)
	}
	if scheduler.task.Action != domain.ActionRemixReview || scheduler.task.ModelName != "gpt-5.6-sol" {
		t.Fatalf("task=%+v", scheduler.task)
	}
}

func TestTaskHTTPResolvesModelSelectionBeforeEnqueue(t *testing.T) {
	db, accountID, projectID, _ := setupManifestTask(t, false)
	for _, tt := range []struct {
		name, extra, model, effort string
		status, enqueued           int
	}{
		{"defaults", "", "gpt-5.6-sol", "medium", 201, 1},
		{"single field", `,"reasoning_effort":"high"`, "gpt-5.6-sol", "high", 201, 1},
		{"both fields", `,"model":"openai/custom","reasoning_effort":"xhigh"`, "openai/custom", "xhigh", 201, 1},
		{"invalid", `,"model":"bad model"`, "", "", 400, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			scheduler := &manifestTestScheduler{}
			handler := NewTasksHandler(db.db, scheduler, nil, nil)
			body := `{"account_id":"` + accountID + `","type":"topic_select","prompt":"go"` + tt.extra + `}`
			req := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/tasks", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != tt.status || scheduler.enqueued != tt.enqueued {
				t.Fatalf("status=%d enqueued=%d body=%s", res.Code, scheduler.enqueued, res.Body.String())
			}
			if tt.enqueued == 1 && (scheduler.task.ModelName != tt.model || scheduler.task.ReasoningEffort != tt.effort) {
				t.Fatalf("task=%+v", scheduler.task)
			}
		})
	}
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

func prepareMontageManifestFixture(t *testing.T, withPublishingPackage bool) (*taskManifestPreparer, domain.CodexTask, string) {
	t.Helper()
	db, accountID, projectID, root := setupManifestTask(t, false)
	if _, err := db.db.Exec(`UPDATE accounts SET name=? WHERE id=?`, "财富觉醒02", accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`UPDATE projects SET title=? WHERE id=?`, "项目兜底", projectID); err != nil {
		t.Fatal(err)
	}
	backgroundDigest := sha256.Sum256([]byte("background"))
	if _, err := db.db.Exec(`UPDATE asset_versions SET sha256=? WHERE account_id=? AND type=?`, hex.EncodeToString(backgroundDigest[:]), accountID, domain.AssetAccountBackground); err != nil {
		t.Fatal(err)
	}
	assets := store.NewAssetRepository(db.db)
	for _, item := range []struct {
		typeName domain.AssetType
		name     string
		mime     string
	}{
		{domain.AssetContinuousScript, "script.txt", "text/plain"},
		{domain.AssetNarration, "voice.wav", "audio/wav"},
		{domain.AssetSubtitleSRT, "subtitles.srt", "application/x-subrip"},
	} {
		path := filepath.Join(root, "projects", projectID, string(item.typeName), item.name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		data := []byte(item.name)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(data)
		if _, err := assets.AddVersion(context.Background(), store.AddAssetVersion{
			ProjectID: &projectID, AccountID: accountID, Type: item.typeName,
			Path: path, Filename: item.name, MIMEType: item.mime, Size: int64(len(data)), SHA256: hex.EncodeToString(digest[:]),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if withPublishingPackage {
		packageData := []byte(`{"short_titles":["存款大搬家","不会使用第二条"]}`)
		packagePath := filepath.Join(root, "projects", projectID, "publishing_package.json")
		if err := os.WriteFile(packagePath, packageData, 0o600); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(packageData)
		remixTask := domain.CodexTask{
			ID: uuid.NewString(), ProjectID: &projectID, AccountID: accountID, Type: "remix",
			SkillName: "finance-viral-remix", Action: domain.ActionRemixEnhanced,
			Status: domain.TaskQueued, CreatedAt: time.Now().UTC().Add(-time.Minute),
		}
		tasks := store.NewTaskRepository(db.db)
		if err := tasks.CreateV2(context.Background(), remixTask); err != nil {
			t.Fatal(err)
		}
		if err := tasks.CompleteWithResult(context.Background(), remixTask.ID, store.TaskResultWrite{Status: domain.TaskCompleted}, []store.TaskArtifact{{
			Kind: "publishing_package", Path: packagePath, Filename: "publishing_package.json",
			MIMEType: "application/json", Size: int64(len(packageData)), SHA256: hex.EncodeToString(digest[:]),
		}}, nil); err != nil {
			t.Fatal(err)
		}
	}
	mediaRoot := filepath.Join(root, "media")
	if err := os.MkdirAll(mediaRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	clipPath := filepath.Join(mediaRoot, "clip.mp4")
	if err := os.WriteFile(clipPath, []byte("clip"), 0o600); err != nil {
		t.Fatal(err)
	}
	mediaIndex := filepath.Join(mediaRoot, "media-index.json")
	if err := os.WriteFile(mediaIndex, []byte(`[{"id":"clip-1","category":"Nature_Landscape","relative_path":"clip.mp4","duration_seconds":20}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	machineProfile := filepath.Join(root, "machine-profile.json")
	if err := os.WriteFile(machineProfile, []byte(`{"platform":"windows"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	task := domain.CodexTask{
		ID: uuid.NewString(), ProjectID: &projectID, AccountID: accountID, Type: "montage",
		SkillName: "jianying-montage-draft", Action: domain.ActionMontageExecute, Status: domain.TaskQueued,
	}
	snapshot := domain.SkillSnapshot{
		ID: uuid.NewString(), Name: "jianying-montage-draft", Path: filepath.Join(root, "SKILL.md"),
		SHA256: strings.Repeat("a", 64), ModifiedAt: time.Now().UTC(), CreatedAt: time.Now().UTC(),
	}
	if err := store.NewSkillRepository(db.db).Save(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	preparer := &taskManifestPreparer{
		db: db.db, projects: store.NewProjectRepository(db.db), assets: assets,
		settings: manifestTestSettings{runtime: consoleSettings.Runtime{PublicSettings: domain.PublicSettings{
			DataRoot: root, MachineProfilePath: machineProfile, MediaRoot: mediaRoot, MediaIndexPath: mediaIndex,
		}}},
		skills: manifestTestSkills{snapshot: snapshot},
	}
	return preparer, task, filepath.Join(root, "projects", projectID, "tasks", task.ID, "task_manifest.json")
}

// Manifest 冻结媒体智能的 endpoint/model/binary path，但任何 Runtime 密钥值、
// key/value 中的 Authorization 或 api_key 形态字段都不得出现在 manifest JSON 里。
func TestTaskManifestFreezesMediaIntelligenceSettingsWithoutSecretLeaks(t *testing.T) {
	preparer, task, manifestPath := prepareMontageManifestFixture(t, false)
	runtime := preparer.settings.(manifestTestSettings).runtime
	runtime.MediaCatalogPath = filepath.Join(runtime.MediaRoot, "catalog.db")
	runtime.FFmpegPath = filepath.Join(runtime.DataRoot, "tools", "ffmpeg.exe")
	runtime.FFprobePath = filepath.Join(runtime.DataRoot, "tools", "ffprobe.exe")
	runtime.VisionBaseURL = "https://vision.example.test/v1"
	runtime.VisionModel = "vision-model-x"
	runtime.EmbeddingBaseURL = "https://embedding.example.test/v1"
	runtime.EmbeddingModel = "embedding-model-y"
	secrets := []string{
		"grok-secret-value-1", "pexels-secret-value-2", "volc-secret-value-3",
		"image-secret-value-4", "imagetext-secret-value-5",
		"vision-secret-value-6", "embedding-secret-value-7", "pixabay-secret-value-8",
	}
	runtime.GrokAPIKey = secrets[0]
	runtime.PexelsAPIKey = secrets[1]
	runtime.VolcSpeechAPIKey = secrets[2]
	runtime.ImageAPIKey = secrets[3]
	runtime.ImageTextAPIKey = secrets[4]
	runtime.VisionAPIKey = secrets[5]
	runtime.EmbeddingAPIKey = secrets[6]
	runtime.PixabayAPIKey = secrets[7]
	preparer.settings = manifestTestSettings{runtime: runtime}

	if err := preparer.Prepare(context.Background(), task, TaskManifestRequest{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	settings, ok := decoded["non_secret_settings"].(map[string]any)
	if !ok {
		t.Fatalf("non_secret_settings missing: %s", data)
	}
	for key, want := range map[string]string{
		"media_catalog_path":   runtime.MediaCatalogPath,
		"ffmpeg_path":          runtime.FFmpegPath,
		"ffprobe_path":         runtime.FFprobePath,
		"vision_base_url":      runtime.VisionBaseURL,
		"vision_model":         runtime.VisionModel,
		"embedding_base_url":   runtime.EmbeddingBaseURL,
		"embedding_model":      runtime.EmbeddingModel,
		"montage_plan_version": "1.0",
	} {
		if settings[key] != want {
			t.Fatalf("frozen setting %s=%v want %q", key, settings[key], want)
		}
	}
	var scan func(path string, value any)
	scan = func(path string, value any) {
		switch typed := value.(type) {
		case map[string]any:
			for key, item := range typed {
				lower := strings.ToLower(key)
				// "non_secret_settings" 是白名单容器本身的合法命名。
				if lower != "non_secret_settings" {
					for _, forbidden := range []string{"api_key", "apikey", "authorization", "token", "password", "secret"} {
						if strings.Contains(lower, forbidden) {
							t.Fatalf("manifest key %s.%s is credential-shaped", path, key)
						}
					}
				}
				scan(path+"."+key, item)
			}
		case []any:
			for index, item := range typed {
				scan(fmt.Sprintf("%s[%d]", path, index), item)
			}
		case string:
			for _, secret := range secrets {
				if strings.Contains(typed, secret) {
					t.Fatalf("manifest value at %s leaked a runtime secret", path)
				}
			}
			if strings.Contains(typed, "Authorization") {
				t.Fatalf("manifest value at %s embeds an Authorization header", path)
			}
		}
	}
	scan("$", decoded)
}

func TestTaskManifestPreparerFreezesDraftDisplayNameFromFirstShortTitle(t *testing.T) {
	preparer, task, manifestPath := prepareMontageManifestFixture(t, true)
	if err := preparer.Prepare(context.Background(), task, TaskManifestRequest{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest codex.TaskManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.NonSecretSettings.DraftDisplayName != "财富觉醒02_存款大搬家_"+task.ID[len(task.ID)-6:] {
		t.Fatalf("draft display name=%q", manifest.NonSecretSettings.DraftDisplayName)
	}
}

func TestTaskManifestPreparerRejectsMissingIndexedClipBeforePersist(t *testing.T) {
	preparer, task, manifestPath := prepareMontageManifestFixture(t, false)
	runtime := preparer.settings.(manifestTestSettings).runtime
	if err := os.Remove(filepath.Join(runtime.MediaRoot, "clip.mp4")); err != nil {
		t.Fatal(err)
	}
	err := preparer.Prepare(context.Background(), task, TaskManifestRequest{})
	if err == nil || !strings.Contains(err.Error(), "montage media preflight") || !strings.Contains(err.Error(), "clip.mp4") {
		t.Fatalf("preflight error=%v", err)
	}
	if _, statErr := os.Stat(manifestPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("manifest must not be written after failed preflight: %v", statErr)
	}
}

func TestTaskManifestPreparerRejectsMissingMovieCatalog(t *testing.T) {
	preparer, task, manifestPath := prepareMontageManifestFixture(t, false)
	task.Type = "movie_montage"
	task.SkillName = "jianying-movie-montage"
	snapshot := preparer.skills.(manifestTestSkills).snapshot
	snapshot.Name = "jianying-movie-montage"
	preparer.skills = manifestTestSkills{snapshot: snapshot}
	err := preparer.Prepare(context.Background(), task, TaskManifestRequest{})
	if err == nil || !strings.Contains(err.Error(), "movie catalog preflight") {
		t.Fatalf("preflight error=%v", err)
	}
	if _, statErr := os.Stat(manifestPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("manifest must not be written after failed movie catalog preflight: %v", statErr)
	}
}

func TestTaskManifestPreparerAcceptsMovieCatalogFile(t *testing.T) {
	preparer, task, manifestPath := prepareMontageManifestFixture(t, false)
	task.Type = "movie_montage"
	task.SkillName = "jianying-movie-montage"
	snapshot := preparer.skills.(manifestTestSkills).snapshot
	snapshot.Name = "jianying-movie-montage"
	preparer.skills = manifestTestSkills{snapshot: snapshot}
	runtime := preparer.settings.(manifestTestSettings).runtime
	catalog := filepath.Join(runtime.MediaRoot, "catalog.db")
	if err := os.WriteFile(catalog, []byte("db"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime.MediaCatalogPath = catalog
	preparer.settings = manifestTestSettings{runtime: runtime}
	if err := preparer.Prepare(context.Background(), task, TaskManifestRequest{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest codex.TaskManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Skill != "jianying-movie-montage" {
		t.Fatalf("manifest skill=%q", manifest.Skill)
	}
}

func TestTaskManifestPreparerLogsPreflightRejectionWithTaskID(t *testing.T) {
	preparer, task, _ := prepareMontageManifestFixture(t, false)
	runtime := preparer.settings.(manifestTestSettings).runtime
	if err := os.Remove(filepath.Join(runtime.MediaRoot, "clip.mp4")); err != nil {
		t.Fatal(err)
	}
	restore := slog.Default()
	t.Cleanup(func() { slog.SetDefault(restore) })
	var records bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&records, nil)))

	ctx := logging.WithRequestID(context.Background(), "request-9")
	if err := preparer.Prepare(ctx, task, TaskManifestRequest{}); err == nil {
		t.Fatal("preflight must still reject the task")
	}

	var record struct {
		Message   string `json:"msg"`
		TaskID    string `json:"task_id"`
		RequestID string `json:"request_id"`
		Phase     string `json:"phase"`
	}
	if err := json.Unmarshal(records.Bytes(), &record); err != nil {
		t.Fatalf("decode log record %q: %v", records.String(), err)
	}
	if record.Message != "montage media preflight rejected" || record.TaskID != task.ID {
		t.Fatalf("unexpected record: %+v", record)
	}
	if record.RequestID != "request-9" || record.Phase != "preflight" {
		t.Fatalf("preflight record is not correlated: %+v", record)
	}
}

func TestTaskManifestPreparerFallsBackToProjectTitleWithoutPublishingPackage(t *testing.T) {
	preparer, task, manifestPath := prepareMontageManifestFixture(t, false)
	if err := preparer.Prepare(context.Background(), task, TaskManifestRequest{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest codex.TaskManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.NonSecretSettings.DraftDisplayName != "财富觉醒02_项目兜底_"+task.ID[len(task.ID)-6:] {
		t.Fatalf("draft display name=%q", manifest.NonSecretSettings.DraftDisplayName)
	}
}

func TestMontageResultSeparatesDisplayNameFromUUIDStorageName(t *testing.T) {
	db, accountID, projectID, root := setupManifestTask(t, false)
	taskID := uuid.NewString()
	displayName := "财富觉醒02_存款大搬家_" + taskID[len(taskID)-6:]
	manifestPath := filepath.Join(root, "projects", projectID, "tasks", taskID, "task_manifest.json")
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o700); err != nil {
		t.Fatal(err)
	}
	manifestData, err := json.Marshal(codex.TaskManifest{NonSecretSettings: codex.ManifestSettings{DraftDisplayName: displayName}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, manifestData, 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot := domain.SkillSnapshot{
		ID: uuid.NewString(), Name: "jianying-montage-draft", Path: filepath.Join(root, "SKILL.md"),
		SHA256: strings.Repeat("b", 64), ModifiedAt: time.Now().UTC(), CreatedAt: time.Now().UTC(),
	}
	if err := store.NewSkillRepository(db.db).Save(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	task := domain.CodexTask{
		ID: taskID, ProjectID: &projectID, AccountID: accountID, Type: "montage", SkillName: "jianying-montage-draft",
		Action: domain.ActionMontageExecute, Status: domain.TaskQueued, CreatedAt: time.Now().UTC(),
	}
	tasks := store.NewTaskRepository(db.db)
	if _, err := tasks.EnsurePreparedTask(context.Background(), task, snapshot.ID, manifestPath); err != nil {
		t.Fatal(err)
	}
	registeredPath := filepath.Join(root, "jianying", taskID)
	if err := os.MkdirAll(registeredPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := store.NewAssetRepository(db.db).AddVersion(context.Background(), store.AddAssetVersion{
		ProjectID: &projectID, AccountID: accountID, Type: domain.AssetMixDraft, StorageKind: domain.StorageDirectory,
		Path: registeredPath, Filename: taskID, MIMEType: "application/x-jianying-draft", SHA256: strings.Repeat("c", 64),
	}); err != nil {
		t.Fatal(err)
	}

	view, err := (&taskResultsHandler{repo: tasks}).montageResult(httptest.NewRequest(http.MethodGet, "/", nil), task)
	if err != nil {
		t.Fatal(err)
	}
	if view["display_name"] != displayName || view["storage_name"] != taskID {
		t.Fatalf("montage identity=%#v", view)
	}
	registered, ok := view["registered_asset"].(map[string]any)
	if !ok || registered["display_name"] != displayName || registered["storage_name"] != taskID || registered["path"] != registeredPath {
		t.Fatalf("registered asset=%#v", view["registered_asset"])
	}
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
	if manifest.NonSecretSettings.RemixPromptStyle != "rewrite" {
		t.Fatalf("default remix style=%q", manifest.NonSecretSettings.RemixPromptStyle)
	}
}

func TestTaskManifestPreparerFreezesWashRemixPromptStyle(t *testing.T) {
	db, accountID, projectID, root := setupManifestTask(t, true)
	preparer := &taskManifestPreparer{projects: store.NewProjectRepository(db.db), assets: store.NewAssetRepository(db.db), settings: manifestTestSettings{runtime: consoleSettings.Runtime{PublicSettings: domain.PublicSettings{DataRoot: root, MaxCodexConcurrency: 2}}}, skills: manifestTestSkills{snapshot: domain.SkillSnapshot{ID: uuid.NewString(), Name: "finance-viral-remix"}}}
	task := domain.CodexTask{ID: uuid.NewString(), ProjectID: &projectID, AccountID: accountID, Action: domain.ActionRemixStandard, Type: "remix"}
	if err := preparer.Prepare(context.Background(), task, TaskManifestRequest{RemixPromptStyle: "wash"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "projects", projectID, "tasks", task.ID, "task_manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest codex.TaskManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.NonSecretSettings.RemixPromptStyle != "wash" {
		t.Fatalf("remix style=%q", manifest.NonSecretSettings.RemixPromptStyle)
	}
}

func TestCreateRemixTaskRejectsUnknownPromptStyle(t *testing.T) {
	db, accountID, projectID, _ := setupManifestTask(t, true)
	handler := NewTasksHandler(db.db, &manifestTestScheduler{}, nil, nil)
	body := `{"account_id":"` + accountID + `","type":"remix","action":"remix.standard","prompt":"go","remix_prompt_style":"paraphrase"}`
	req := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/tasks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "invalid_remix_prompt_style") {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestTaskManifestPreparerBindsRequestedCurrentSourceVersion(t *testing.T) {
	db, accountID, projectID, root := setupManifestTask(t, true)
	assets := store.NewAssetRepository(db.db)
	current, err := assets.CurrentByProject(context.Background(), projectID)
	if err != nil || len(current) != 1 {
		t.Fatalf("current=%+v err=%v", current, err)
	}
	source := current[0]
	preparer := &taskManifestPreparer{
		projects: store.NewProjectRepository(db.db), assets: assets,
		settings: manifestTestSettings{runtime: consoleSettings.Runtime{PublicSettings: domain.PublicSettings{DataRoot: root}}},
		skills:   manifestTestSkills{snapshot: domain.SkillSnapshot{ID: uuid.NewString(), Name: "finance-viral-remix"}},
	}
	task := domain.CodexTask{ID: uuid.NewString(), ProjectID: &projectID, AccountID: accountID, Action: domain.ActionRemixStandard, Type: "remix"}
	if err := preparer.Prepare(context.Background(), task, TaskManifestRequest{SourceVersionID: source.ID}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "projects", projectID, "tasks", task.ID, "task_manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest codex.TaskManifest
	if err := json.Unmarshal(data, &manifest); err != nil || len(manifest.Inputs) != 1 || manifest.Inputs[0].VersionID != source.ID {
		t.Fatalf("manifest=%+v err=%v", manifest, err)
	}

	otherProject := uuid.NewString()
	now := time.Now().UTC()
	if _, err := db.db.Exec(`INSERT INTO projects(id,account_id,title,stage,created_at,updated_at) VALUES(?,?,?,?,?,?)`, otherProject, accountID, "other", domain.StageScript, now, now); err != nil {
		t.Fatal(err)
	}
	other := source
	other.ProjectID = &otherProject
	other.Path = filepath.Join(root, "projects", otherProject, "source.txt")
	if err := os.MkdirAll(filepath.Dir(other.Path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other.Path, []byte("other source"), 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("other source"))
	other, err = assets.AddVersion(context.Background(), store.AddAssetVersion{ProjectID: &otherProject, AccountID: accountID, Type: domain.AssetSourceScript, Path: other.Path, Filename: "source.txt", MIMEType: "text/plain", Size: 12, SHA256: hex.EncodeToString(digest[:])})
	if err != nil {
		t.Fatal(err)
	}
	for name, sourceVersion := range map[string]string{
		"unknown":       uuid.NewString(),
		"cross project": other.ID,
	} {
		t.Run(name, func(t *testing.T) {
			task := domain.CodexTask{ID: uuid.NewString(), ProjectID: &projectID, AccountID: accountID, Action: domain.ActionRemixStandard, Type: "remix"}
			if err := preparer.Prepare(context.Background(), task, TaskManifestRequest{SourceVersionID: sourceVersion}); err == nil {
				t.Fatal("expected requested version to be rejected")
			}
		})
	}
	if _, err := db.db.Exec(`UPDATE asset_versions SET state=? WHERE id=?`, domain.AssetStale, source.ID); err != nil {
		t.Fatal(err)
	}
	task = domain.CodexTask{ID: uuid.NewString(), ProjectID: &projectID, AccountID: accountID, Action: domain.ActionRemixStandard, Type: "remix"}
	if err := preparer.Prepare(context.Background(), task, TaskManifestRequest{SourceVersionID: source.ID}); err == nil {
		t.Fatal("expected non-ready source version to be rejected")
	}
	if _, err := db.db.Exec(`UPDATE asset_versions SET state=? WHERE id=?`, domain.AssetReady, source.ID); err != nil {
		t.Fatal(err)
	}
	replacementPath := filepath.Join(root, "projects", projectID, "source_script", "replacement.txt")
	if err := os.WriteFile(replacementPath, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	replacementDigest := sha256.Sum256([]byte("replacement"))
	if _, err := assets.AddVersion(context.Background(), store.AddAssetVersion{LogicalAssetID: source.AssetID, ProjectID: &projectID, AccountID: accountID, Type: domain.AssetSourceScript, Path: replacementPath, Filename: "replacement.txt", MIMEType: "text/plain", Size: 11, SHA256: hex.EncodeToString(replacementDigest[:])}); err != nil {
		t.Fatal(err)
	}
	task = domain.CodexTask{ID: uuid.NewString(), ProjectID: &projectID, AccountID: accountID, Action: domain.ActionRemixStandard, Type: "remix"}
	if err := preparer.Prepare(context.Background(), task, TaskManifestRequest{SourceVersionID: source.ID}); err == nil {
		t.Fatal("expected non-current source version to be rejected")
	}
}

func TestTaskHTTPReusesActiveStandardRemixOnlyForSameSourceVersion(t *testing.T) {
	db, accountID, projectID, root := setupManifestTask(t, true)
	assets, err := store.NewAssetRepository(db.db).CurrentByProject(context.Background(), projectID)
	if err != nil || len(assets) != 1 {
		t.Fatalf("assets=%+v err=%v", assets, err)
	}
	source := assets[0]
	active := domain.CodexTask{
		ID: uuid.NewString(), ProjectID: &projectID, AccountID: accountID, Type: "remix", SkillName: "finance-viral-remix",
		Action: domain.ActionRemixStandard, Status: domain.TaskQueued, PromptSnapshot: "active", CreatedAt: time.Now().UTC(),
	}
	repo := store.NewTaskRepository(db.db)
	if err := repo.CreateV2(context.Background(), active); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, "active-manifest.json")
	manifest, err := json.Marshal(codex.TaskManifest{Inputs: []codex.ManifestInput{{Type: domain.AssetSourceScript, VersionID: source.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot := domain.SkillSnapshot{ID: uuid.NewString(), Name: "finance-viral-remix", Path: filepath.Join(root, "SKILL.md"), SHA256: strings.Repeat("a", 64), ModifiedAt: time.Now().UTC(), CreatedAt: time.Now().UTC()}
	if err := store.NewSkillRepository(db.db).Save(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetPreparedManifest(context.Background(), active.ID, snapshot.ID, manifestPath); err != nil {
		t.Fatal(err)
	}
	scheduler := &manifestTestScheduler{}
	handler := NewTasksHandler(db.db, scheduler, nil, nil)
	for name, request := range map[string]struct {
		source string
		status int
	}{
		"same source":      {source.ID, http.StatusOK},
		"different source": {uuid.NewString(), http.StatusConflict},
	} {
		t.Run(name, func(t *testing.T) {
			body := `{"account_id":"` + accountID + `","type":"remix","action":"remix.standard","prompt":"go","source_version_id":"` + request.source + `"}`
			req := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/tasks", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			if recorder.Code != request.status || scheduler.enqueued != 0 {
				t.Fatalf("status=%d enqueued=%d body=%s", recorder.Code, scheduler.enqueued, recorder.Body.String())
			}
			if request.status == http.StatusConflict && !strings.Contains(recorder.Body.String(), "active_remix_conflict") {
				t.Fatalf("body=%s", recorder.Body.String())
			}
		})
	}
}

func TestTaskManifestPreparerPersistsFormalTaskForLegacyExecWhenAppServerIsEnabled(t *testing.T) {
	db, accountID, projectID, root := setupManifestTask(t, true)
	snapshot := domain.SkillSnapshot{
		ID: uuid.NewString(), Name: "finance-viral-remix", Path: filepath.Join(root, "SKILL.md"),
		SHA256: strings.Repeat("a", 64), ModifiedAt: time.Now().UTC(), CreatedAt: time.Now().UTC(),
	}
	if err := store.NewSkillRepository(db.db).Save(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	threadID := "project-thread"
	session := domain.ChatSession{
		ID: uuid.NewString(), Title: "project", Source: "console", Kind: domain.ChatProject,
		Status: domain.ChatIdle, ProjectID: &projectID, CodexThreadID: &threadID,
		WorkingDirectory: root, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := store.NewConversationRepository(db.db).CreateSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	preparer := &taskManifestPreparer{
		db: db.db, projects: store.NewProjectRepository(db.db), assets: store.NewAssetRepository(db.db),
		settings: manifestTestSettings{runtime: consoleSettings.Runtime{PublicSettings: domain.PublicSettings{
			DataRoot: root, MaxCodexConcurrency: 2, AppServerEnabled: true,
		}}},
		skills: manifestTestSkills{snapshot: snapshot},
	}
	task := domain.CodexTask{
		ID: uuid.NewString(), ProjectID: &projectID, AccountID: accountID,
		Action: domain.ActionRemixEnhanced, Type: "remix", SkillName: "finance-viral-remix",
		Status: domain.TaskQueued,
	}
	if err := preparer.Prepare(context.Background(), task, TaskManifestRequest{}); err != nil {
		t.Fatal(err)
	}
	persisted, err := store.NewTaskRepository(db.db).Get(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Transport != codex.TransportLegacyExec || persisted.ChatSessionID != nil || persisted.CodexThreadID != nil {
		t.Fatalf("formal task transport = %#v", persisted)
	}
	if !strings.Contains(persisted.PromptSnapshot, "VIDEO_CONSOLE_TASK_MANIFEST") {
		t.Fatalf("formal task prompt does not use the manifest environment: %q", persisted.PromptSnapshot)
	}
}

func TestTaskHTTPRejectsMissingRemixAssetBeforeEnqueue(t *testing.T) {
	db, accountID, projectID, root := setupManifestTask(t, false)
	preparer := &taskManifestPreparer{projects: store.NewProjectRepository(db.db), assets: store.NewAssetRepository(db.db), settings: manifestTestSettings{runtime: consoleSettings.Runtime{PublicSettings: domain.PublicSettings{DataRoot: root, MaxCodexConcurrency: 2}}}, skills: manifestTestSkills{snapshot: domain.SkillSnapshot{ID: uuid.NewString(), Name: "finance-viral-remix"}}}
	scheduler := &manifestTestScheduler{}
	handler := NewTasksHandler(db.db, scheduler, preparer, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/tasks", strings.NewReader(`{"account_id":"`+accountID+`","type":"remix","action":"remix.enhanced","prompt":"go"}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusConflict || scheduler.enqueued != 0 {
		t.Fatalf("status=%d enqueued=%d body=%s", recorder.Code, scheduler.enqueued, recorder.Body.String())
	}
	failed, err := store.NewTaskRepository(db.db).List(context.Background(), projectID, domain.TaskFailed)
	if err != nil || len(failed) != 1 {
		t.Fatalf("failed preparation tasks=%+v err=%v", failed, err)
	}
	phases, err := store.NewTaskTimingRepository(db.db).ForTask(context.Background(), failed[0].ID)
	if err != nil || len(phases) != 1 || phases[0].PhaseKey != "task_prepare" || phases[0].State != domain.PhaseFailed {
		t.Fatalf("failed preparation phases=%+v err=%v", phases, err)
	}
}

func TestTaskManifestPreparerWritesProjectlessTopicManifest(t *testing.T) {
	db, accountID, _, root := setupManifestTask(t, false)
	preparer := &taskManifestPreparer{
		projects: store.NewProjectRepository(db.db), assets: store.NewAssetRepository(db.db),
		settings: manifestTestSettings{runtime: consoleSettings.Runtime{PublicSettings: domain.PublicSettings{DataRoot: root, MaxCodexConcurrency: 2}}},
		skills:   manifestTestSkills{snapshot: domain.SkillSnapshot{ID: uuid.NewString(), Name: "finance-topic-selector"}},
	}
	task := domain.CodexTask{ID: uuid.NewString(), AccountID: accountID, Action: domain.ActionTopicBrainstorm, Type: "topic_select"}
	sessionID := uuid.NewString()
	if err := preparer.Prepare(context.Background(), task, TaskManifestRequest{SessionID: sessionID}); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, "projects", task.ID, "tasks", task.ID, "task_manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest codex.TaskManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Action != domain.ActionTopicBrainstorm || manifest.Skill != "finance-topic-selector" || manifest.NonSecretSettings.SessionID != sessionID {
		t.Fatalf("manifest=%+v", manifest)
	}
}

func TestTaskManifestPreparerSnapshotsExplicitBaokuanSources(t *testing.T) {
	db, accountID, _, root := setupManifestTask(t, false)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/channels/library/materials/bundle" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"code": 0,
			"data": map[string]any{"videos": []any{map[string]any{
				"record":     map[string]any{"feed_id": "14986230628414069221", "author_name": "每日说财经", "title": "认知觉醒"},
				"transcript": map[string]any{"feed_id": "14986230628414069221", "status": "completed", "text": "完整转写正文"},
			}}},
		})
	}))
	defer server.Close()
	preparer := &taskManifestPreparer{
		projects: store.NewProjectRepository(db.db), assets: store.NewAssetRepository(db.db),
		settings: manifestTestSettings{runtime: consoleSettings.Runtime{PublicSettings: domain.PublicSettings{DataRoot: root, BaokuanBaseURL: server.URL}}},
		skills:   manifestTestSkills{snapshot: domain.SkillSnapshot{ID: uuid.NewString(), Name: "finance-topic-selector"}},
	}
	task := domain.CodexTask{ID: uuid.NewString(), AccountID: accountID, Action: domain.ActionTopicBrainstorm, Type: "topic_select"}
	if err := preparer.Prepare(t.Context(), task, TaskManifestRequest{SessionID: uuid.NewString(), SourceFeedIDs: []string{"14986230628414069221"}}); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, "projects", task.ID, "tasks", task.ID, "task_manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest codex.TaskManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.EngineeringInputs) != 1 || manifest.EngineeringInputs[0].Type != "baokuan_source_bundle" {
		t.Fatalf("engineering inputs=%+v", manifest.EngineeringInputs)
	}
	bundle, err := os.ReadFile(manifest.EngineeringInputs[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(bundle, []byte("14986230628414069221")) || !bytes.Contains(bundle, []byte("完整转写正文")) {
		t.Fatalf("source snapshot=%s", bundle)
	}
}

func TestTaskManifestPreparerSnapshotsTopicCandidatesIntoCurrentProject(t *testing.T) {
	db, accountID, projectID, root := setupManifestTask(t, false)
	vault := filepath.Join(root, "vault")
	cards := filepath.Join(vault, "选题卡")
	if err := os.MkdirAll(cards, 0o755); err != nil {
		t.Fatal(err)
	}
	sourceTaskID := uuid.NewString()
	source := filepath.Join(root, "projects", sourceTaskID, "tasks", sourceTaskID, "output", "topic_candidates.json")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	wantCandidates := []byte(`{"schema_version":"2.0","session_id":"session-1","candidates":[]}`)
	if err := os.WriteFile(source, wantCandidates, 0o600); err != nil {
		t.Fatal(err)
	}
	preparer := &taskManifestPreparer{
		projects: store.NewProjectRepository(db.db), assets: store.NewAssetRepository(db.db),
		settings: manifestTestSettings{runtime: consoleSettings.Runtime{PublicSettings: domain.PublicSettings{
			DataRoot: root, MaxCodexConcurrency: 2, ObsidianVault: vault, TopicCardsDir: cards,
		}}},
		skills: manifestTestSkills{snapshot: domain.SkillSnapshot{ID: uuid.NewString(), Name: "finance-topic-selector"}},
	}
	task := domain.CodexTask{ID: uuid.NewString(), ProjectID: &projectID, AccountID: accountID, Action: domain.ActionTopicCommit, Type: "topic_commit"}
	request := TaskManifestRequest{SessionID: "session-1", CandidateID: "candidate-1", TopicCandidatesPath: source}

	if err := preparer.Prepare(context.Background(), task, request); err != nil {
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
	wantSnapshot := filepath.Join(root, "projects", projectID, "tasks", task.ID, "input", "topic_candidates.json")
	if len(manifest.EngineeringInputs) != 1 || manifest.EngineeringInputs[0].Path != wantSnapshot || manifest.NonSecretSettings.TopicCandidatesPath != wantSnapshot {
		t.Fatalf("topic candidate snapshot not bound to current task: %+v", manifest)
	}
	gotCandidates, err := os.ReadFile(wantSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotCandidates) != string(wantCandidates) {
		t.Fatalf("snapshot bytes = %q, want %q", gotCandidates, wantCandidates)
	}
}

func attachMontageCapabilities(t *testing.T, snapshot domain.SkillSnapshot, payload []byte) domain.SkillSnapshot {
	t.Helper()
	root := filepath.Dir(snapshot.Path)
	if strings.EqualFold(filepath.Base(snapshot.Path), "skill.md") {
		root = filepath.Dir(snapshot.Path)
	} else if info, err := os.Stat(snapshot.Path); err == nil && info.IsDir() {
		root = snapshot.Path
	} else {
		root = t.TempDir()
		snapshot.Path = root
	}
	path := filepath.Join(root, filepath.FromSlash("assets/capabilities.json"))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	snapshot.Files = append(snapshot.Files, domain.SkillFileSnapshot{
		Path: "assets/capabilities.json", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(payload)),
	})
	snapshot.Path = root
	return snapshot
}

func TestTaskManifestFreezesV2OnlyWhenSnapshotDeclaresCapability(t *testing.T) {
	preparer, task, manifestPath := prepareMontageManifestFixture(t, false)
	snapshot := attachMontageCapabilities(t, preparer.skills.(manifestTestSkills).snapshot, []byte(`{"contract_version":"1.0","production_plan_versions":["1.0","2.0"],"features":["highlight_captions","image_keyframes","style_policy_v2"]}`))
	preparer.skills = manifestTestSkills{snapshot: snapshot}

	if err := preparer.Prepare(context.Background(), task, TaskManifestRequest{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest codex.TaskManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.NonSecretSettings.MontagePlanVersion != "2.0" {
		t.Fatalf("montage_plan_version=%q, want 2.0; body=%s", manifest.NonSecretSettings.MontagePlanVersion, data)
	}
	if manifest.SkillSnapshotID != snapshot.ID {
		t.Fatalf("skill_snapshot_id=%q want %q", manifest.SkillSnapshotID, snapshot.ID)
	}
}

func TestTaskManifestMalformedCapabilityStaysV1(t *testing.T) {
	preparer, task, manifestPath := prepareMontageManifestFixture(t, false)
	snapshot := attachMontageCapabilities(t, preparer.skills.(manifestTestSkills).snapshot, []byte(`{"contract_version":`))
	preparer.skills = manifestTestSkills{snapshot: snapshot}
	if err := preparer.Prepare(context.Background(), task, TaskManifestRequest{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest codex.TaskManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.NonSecretSettings.MontagePlanVersion != "1.0" {
		t.Fatalf("malformed capability must freeze v1, got %q", manifest.NonSecretSettings.MontagePlanVersion)
	}
}

func TestTaskManifestHistoricalSnapshotIgnoresNewerLatest(t *testing.T) {
	preparer, task, manifestPath := prepareMontageManifestFixture(t, false)
	v1 := preparer.skills.(manifestTestSkills).snapshot
	if err := preparer.Prepare(context.Background(), task, TaskManifestRequest{}); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	v2 := attachMontageCapabilities(t, domain.SkillSnapshot{
		ID: uuid.NewString(), Name: v1.Name, Path: v1.Path,
		SHA256: strings.Repeat("d", 64), ModifiedAt: time.Now().UTC(), CreatedAt: time.Now().UTC(),
	}, []byte(`{"contract_version":"1.0","production_plan_versions":["1.0","2.0"],"features":["highlight_captions"]}`))
	if err := store.NewSkillRepository(preparer.db).Save(context.Background(), v2); err != nil {
		t.Fatal(err)
	}
	preparer.skills = manifestTestSkills{snapshot: v2}
	if err := preparer.Prepare(context.Background(), task, TaskManifestRequest{}); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var firstManifest, secondManifest codex.TaskManifest
	if err := json.Unmarshal(first, &firstManifest); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(second, &secondManifest); err != nil {
		t.Fatal(err)
	}
	if firstManifest.SkillSnapshotID != v1.ID || secondManifest.SkillSnapshotID != v1.ID {
		t.Fatalf("historical task rebound to latest snapshot: first=%q second=%q v1=%q v2=%q",
			firstManifest.SkillSnapshotID, secondManifest.SkillSnapshotID, v1.ID, v2.ID)
	}
	if secondManifest.NonSecretSettings.MontagePlanVersion == "2.0" {
		t.Fatalf("historical v1 task must not pick up a later v2 capability")
	}
}
