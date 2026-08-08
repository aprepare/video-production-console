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

type manifestTestScheduler struct {
	enqueued int
	task     domain.CodexTask
}

func (s *manifestTestScheduler) Enqueue(_ context.Context, task domain.CodexTask) error {
	s.enqueued++
	s.task = task
	return nil
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
			DataRoot: root, MachineProfilePath: machineProfile,
		}}},
		skills: manifestTestSkills{snapshot: snapshot},
	}
	return preparer, task, filepath.Join(root, "projects", projectID, "tasks", task.ID, "task_manifest.json")
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
