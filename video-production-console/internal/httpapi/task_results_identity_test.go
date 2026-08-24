package httpapi

import (
	"context"
	"crypto/sha256"
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
	"video-production-console/internal/store"
)

func addPreparedMontageResultTask(t *testing.T, db *sqlDBForManifestTest, projectID, accountID, taskID, displayName string, snapshot domain.SkillSnapshot) domain.CodexTask {
	t.Helper()
	manifestPath := filepath.Join(db.root, "projects", projectID, "tasks", taskID, "task_manifest.json")
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(codex.TaskManifest{NonSecretSettings: codex.ManifestSettings{DraftDisplayName: displayName}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	task := domain.CodexTask{
		ID: taskID, ProjectID: &projectID, AccountID: accountID, Type: "montage", SkillName: "jianying-montage-draft",
		Action: domain.ActionMontageExecute, Status: domain.TaskQueued, CreatedAt: time.Now().UTC(),
	}
	if _, err := store.NewTaskRepository(db.db).EnsurePreparedTask(context.Background(), task, snapshot.ID, manifestPath); err != nil {
		t.Fatal(err)
	}
	return task
}

func montageResultTestSnapshot(t *testing.T, db *sqlDBForManifestTest) domain.SkillSnapshot {
	t.Helper()
	snapshot := domain.SkillSnapshot{
		ID: uuid.NewString(), Name: "jianying-montage-draft", Path: filepath.Join(db.root, "SKILL.md"),
		SHA256: strings.Repeat("d", 64), ModifiedAt: time.Now().UTC(), CreatedAt: time.Now().UTC(),
	}
	if err := store.NewSkillRepository(db.db).Save(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func addReadyMixDraft(t *testing.T, db *sqlDBForManifestTest, projectID, accountID, path string, sourceTaskID *string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := store.NewAssetRepository(db.db).AddVersion(context.Background(), store.AddAssetVersion{
		ProjectID: &projectID, AccountID: accountID, Type: domain.AssetMixDraft, StorageKind: domain.StorageDirectory,
		Path: path, Filename: filepath.Base(path), MIMEType: "application/x-jianying-draft", SHA256: strings.Repeat("e", 64), SourceTaskID: sourceTaskID,
	}); err != nil {
		t.Fatal(err)
	}
	if sourceTaskID != nil {
		now := time.Now().UTC()
		if _, err := db.db.Exec(`INSERT INTO montage_registration_attempts(id,task_id,manifest_path,workspace_path,state,attempt,registered_path,receipt_path,draft_id,started_at,finished_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, uuid.NewString(), *sourceTaskID, "manifest.json", "workspace", domain.RegistrationSucceeded, 1, path, "receipt.json", "draft-"+(*sourceTaskID)[:8], now, now); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMontageResultReturnsOnlyTheDraftProducedByTheRequestedHistoricalTask(t *testing.T) {
	db, accountID, projectID, _ := setupManifestTask(t, false)
	snapshot := montageResultTestSnapshot(t, db)
	firstID, secondID := uuid.NewString(), uuid.NewString()
	first := addPreparedMontageResultTask(t, db, projectID, accountID, firstID, "第一任务草稿", snapshot)
	_ = addPreparedMontageResultTask(t, db, projectID, accountID, secondID, "第二任务草稿", snapshot)
	firstPath := filepath.Join(db.root, "jianying", firstID)
	secondPath := filepath.Join(db.root, "jianying", secondID)
	addReadyMixDraft(t, db, projectID, accountID, firstPath, &firstID)
	addReadyMixDraft(t, db, projectID, accountID, secondPath, &secondID)

	view, err := (&taskResultsHandler{repo: store.NewTaskRepository(db.db)}).montageResult(httptest.NewRequest(http.MethodGet, "/", nil), first)
	if err != nil {
		t.Fatal(err)
	}
	registered, ok := view["registered_asset"].(map[string]any)
	if registered["draft_id"] != "draft-"+firstID[:8] {
		t.Fatalf("historical draft identity=%#v", registered["draft_id"])
	}
	if !ok || registered["path"] != firstPath || registered["storage_name"] != firstID || registered["display_name"] != "第一任务草稿" {
		t.Fatalf("historical registered asset=%#v", view["registered_asset"])
	}
}

func TestMontageResultAllowsOnlyUnambiguousLegacyDraftFallback(t *testing.T) {
	t.Run("one task and one draft", func(t *testing.T) {
		db, accountID, projectID, _ := setupManifestTask(t, false)
		snapshot := montageResultTestSnapshot(t, db)
		taskID := uuid.NewString()
		task := addPreparedMontageResultTask(t, db, projectID, accountID, taskID, "唯一草稿", snapshot)
		path := filepath.Join(db.root, "jianying", taskID)
		addReadyMixDraft(t, db, projectID, accountID, path, nil)

		view, err := (&taskResultsHandler{repo: store.NewTaskRepository(db.db)}).montageResult(httptest.NewRequest(http.MethodGet, "/", nil), task)
		if err != nil {
			t.Fatal(err)
		}
		if registered, ok := view["registered_asset"].(map[string]any); !ok || registered["path"] != path {
			t.Fatalf("legacy registered asset=%#v", view["registered_asset"])
		}
	})

	for _, tt := range []struct {
		name       string
		taskCount  int
		draftCount int
	}{
		{name: "multiple montage tasks", taskCount: 2, draftCount: 1},
		{name: "multiple ready drafts", taskCount: 1, draftCount: 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			db, accountID, projectID, _ := setupManifestTask(t, false)
			snapshot := montageResultTestSnapshot(t, db)
			requestedID := uuid.NewString()
			requested := addPreparedMontageResultTask(t, db, projectID, accountID, requestedID, "旧草稿", snapshot)
			for i := 1; i < tt.taskCount; i++ {
				otherID := uuid.NewString()
				_ = addPreparedMontageResultTask(t, db, projectID, accountID, otherID, "其他草稿", snapshot)
			}
			for i := 0; i < tt.draftCount; i++ {
				addReadyMixDraft(t, db, projectID, accountID, filepath.Join(db.root, "jianying", uuid.NewString()), nil)
			}

			view, err := (&taskResultsHandler{repo: store.NewTaskRepository(db.db)}).montageResult(httptest.NewRequest(http.MethodGet, "/", nil), requested)
			if err != nil {
				t.Fatal(err)
			}
			if view["registered_asset"] != nil || view["storage_name"] != "" {
				t.Fatalf("ambiguous legacy identity leaked=%#v", view)
			}
		})
	}
}

func TestRemixReviewResultIncludesPublishingPackage(t *testing.T) {
	db, accountID, projectID, _ := setupManifestTask(t, false)
	packageData := []byte(`{"short_titles":["三年前买三年后难卖","五个月法拍近四十万"]}`)
	packagePath := filepath.Join(db.root, "projects", projectID, "review", "publishing_package.json")
	if err := os.MkdirAll(filepath.Dir(packagePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(packagePath, packageData, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(packageData)
	task := domain.CodexTask{
		ID: uuid.NewString(), ProjectID: &projectID, AccountID: accountID, Type: "remix",
		SkillName: "finance-viral-remix", Action: domain.ActionRemixReview,
		Status: domain.TaskQueued, CreatedAt: time.Now().UTC(),
	}
	tasks := store.NewTaskRepository(db.db)
	if err := tasks.CreateV2(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if err := tasks.CompleteWithResult(context.Background(), task.ID, store.TaskResultWrite{Status: domain.TaskCompleted}, []store.TaskArtifact{{
		Kind: "publishing_package", Path: packagePath, Filename: "publishing_package.json",
		MIMEType: "application/json", Size: int64(len(packageData)), SHA256: hex.EncodeToString(digest[:]),
	}}, nil); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/tasks/"+task.ID+"/result", nil)
	req.SetPathValue("id", task.ID)
	res := httptest.NewRecorder()
	NewTaskResultsHandler(tasks).ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	var body struct {
		PublishingPackage *struct {
			ShortTitles []string `json:"short_titles"`
		} `json:"publishing_package"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.PublishingPackage == nil || len(body.PublishingPackage.ShortTitles) == 0 || body.PublishingPackage.ShortTitles[0] != "三年前买三年后难卖" {
		t.Fatalf("publishing_package=%s", res.Body.String())
	}
}

func TestFailedRemixResultIncludesCapturedScript(t *testing.T) {
	db, accountID, projectID, _ := setupManifestTask(t, false)
	taskID := uuid.NewString()
	outputDir := filepath.Join(db.root, "projects", projectID, "tasks", taskID, "output")
	if err := os.MkdirAll(outputDir, 0o700); err != nil {
		t.Fatal(err)
	}
	script := "三年定存利率从2.6%直接砍到1.25%。这不是吓你。去我主页橱窗找《财富觉醒方法论》。"
	if err := os.WriteFile(filepath.Join(outputDir, "continuous_script.txt"), []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outputDir, "model_raw.txt"), []byte(`{"continuous_script":"三年定存利率从2.6%直接砍到1.25%。"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outputDir, "remix_run.json"), []byte(`{"events":[{"event":"quality_failed"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(db.root, "projects", projectID, "tasks", taskID, "task_manifest.json")
	data, err := json.Marshal(map[string]any{"task_id": taskID, "action": "remix.standard", "output_dir": outputDir})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot := domain.SkillSnapshot{
		ID: uuid.NewString(), Name: "finance-viral-remix", Path: filepath.Join(db.root, "SKILL.md"),
		SHA256: strings.Repeat("a", 64), ModifiedAt: time.Now().UTC(), CreatedAt: time.Now().UTC(),
	}
	if err := store.NewSkillRepository(db.db).Save(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	task := domain.CodexTask{
		ID: taskID, ProjectID: &projectID, AccountID: accountID, Type: "remix",
		SkillName: "finance-viral-remix", Action: domain.ActionRemixStandard,
		Status: domain.TaskQueued, CreatedAt: time.Now().UTC(),
	}
	tasks := store.NewTaskRepository(db.db)
	if _, err := tasks.EnsurePreparedTask(context.Background(), task, snapshot.ID, manifestPath); err != nil {
		t.Fatal(err)
	}
	if err := tasks.CompleteWithResult(context.Background(), task.ID, store.TaskResultWrite{Status: domain.TaskFailed, Summary: "quality failed", ErrorCode: "result_failed", ErrorMessage: "quality failed"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/tasks/"+task.ID+"/result", nil)
	req.SetPathValue("id", task.ID)
	res := httptest.NewRecorder()
	NewTaskResultsHandler(tasks).ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	var body struct {
		ContinuousScript string          `json:"continuous_script"`
		ModelRaw         string          `json:"model_raw"`
		RemixRun         json.RawMessage `json:"remix_run"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body.ContinuousScript, "三年定存利率") || !strings.Contains(body.ModelRaw, "continuous_script") || !strings.Contains(string(body.RemixRun), "quality_failed") {
		t.Fatalf("failed remix capture missing: %s", res.Body.String())
	}
}
