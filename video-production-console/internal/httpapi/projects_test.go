package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
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
	"video-production-console/internal/taskmodel"
	"video-production-console/internal/workflow"
)

type recordingRemixStarter struct {
	run   domain.ProjectWorkflowRun
	err   error
	calls []workflow.StartRemix
}

func (s *recordingRemixStarter) Start(_ context.Context, in workflow.StartRemix) (domain.ProjectWorkflowRun, error) {
	s.calls = append(s.calls, in)
	return s.run, s.err
}

type repositoryWorkflowLauncher struct {
	db    *sql.DB
	calls int
}

func (l *repositoryWorkflowLauncher) LaunchTopicCommit(ctx context.Context, in workflow.LaunchTask) (domain.CodexTask, error) {
	return l.launch(ctx, in, "topic_commit")
}

func (l *repositoryWorkflowLauncher) LaunchRemixFromTopicCard(ctx context.Context, in workflow.LaunchTask) (domain.CodexTask, error) {
	return l.launch(ctx, in, "remix")
}

func (l *repositoryWorkflowLauncher) launch(ctx context.Context, in workflow.LaunchTask, typ string) (domain.CodexTask, error) {
	l.calls++
	projectID := in.Project.ID
	task := domain.CodexTask{ID: uuid.NewString(), ProjectID: &projectID, AccountID: in.Project.AccountID, Type: typ, SkillName: "test-skill", Status: domain.TaskQueued, PromptSnapshot: "prompt", ModelName: in.ModelName, ReasoningEffort: in.ReasoningEffort, CreatedAt: in.Now}
	return task, store.NewTaskRepository(l.db).Create(ctx, task)
}

type fixedProjectModelResolver struct {
	want taskmodel.Selection
}

func (r fixedProjectModelResolver) ResolveTaskModel(_ context.Context, _ taskmodel.Selection) (taskmodel.Selection, error) {
	return r.want, nil
}

func TestProjectRemixUsesDatabaseIdentityDefaultsAndIsIdempotent(t *testing.T) {
	_, db, root, accountID := newProjectsTestHandler(t, "active")
	projectID := uuid.NewString()
	now := time.Now().UTC()
	if err := store.NewProjectRepository(db).CreateProject(context.Background(), domain.Project{ID: projectID, AccountID: accountID, Title: "remix", Stage: domain.StageScript, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	taskID := uuid.NewString()
	pid := projectID
	if err := store.NewTaskRepository(db).Create(context.Background(), domain.CodexTask{ID: taskID, ProjectID: &pid, AccountID: accountID, Type: "topic_commit", SkillName: "finance-topic-selector", Status: domain.TaskQueued, PromptSnapshot: "prompt", ModelName: "gpt-fixed", ReasoningEffort: "high", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	starter := &recordingRemixStarter{run: domain.ProjectWorkflowRun{ID: uuid.NewString(), ProjectID: projectID, AccountID: accountID, Kind: domain.WorkflowRemix, State: domain.WorkflowRunning, CurrentStep: domain.WorkflowStepTopicCard, TopicTaskID: &taskID, ModelName: "gpt-fixed", ReasoningEffort: "high", CreatedAt: now, UpdatedAt: now}}
	handler := NewProjectsHandler(db, assets.NewService(root), starter, fixedProjectModelResolver{want: taskmodel.Selection{Model: "gpt-fixed", ReasoningEffort: "high"}})

	for i := 0; i < 2; i++ {
		response := performJSON(t, handler, http.MethodPost, "/api/projects/"+projectID+"/remix", map[string]any{})
		if response.StatusCode != http.StatusOK {
			t.Fatalf("attempt %d status=%d body=%s", i+1, response.StatusCode, readResponseBody(t, response))
		}
		var body struct {
			Workflow struct {
				ID              string `json:"id"`
				AccountID       string `json:"account_id"`
				Model           string `json:"model"`
				ReasoningEffort string `json:"reasoning_effort"`
			} `json:"workflow"`
			CurrentTask taskView `json:"current_task"`
		}
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Workflow.ID != starter.run.ID || body.Workflow.AccountID != accountID || body.CurrentTask.ID != taskID {
			t.Fatalf("unexpected remix response: %+v", body)
		}
	}
	if len(starter.calls) != 2 {
		t.Fatalf("Start calls=%d", len(starter.calls))
	}
	for _, call := range starter.calls {
		if call.ProjectID != projectID || call.AccountID != accountID || call.ModelName != "gpt-fixed" || call.ReasoningEffort != "high" {
			t.Fatalf("coordinator input=%+v", call)
		}
	}

	tampered := performJSON(t, handler, http.MethodPost, "/api/projects/"+projectID+"/remix", map[string]any{"account_id": uuid.NewString()})
	if tampered.StatusCode != http.StatusBadRequest {
		t.Fatalf("tampered identity status=%d body=%s", tampered.StatusCode, readResponseBody(t, tampered))
	}
}

func TestProjectRemixInternalErrorIsGeneric(t *testing.T) {
	_, db, root, accountID := newProjectsTestHandler(t, "active")
	projectID := uuid.NewString()
	now := time.Now().UTC()
	if err := store.NewProjectRepository(db).CreateProject(context.Background(), domain.Project{ID: projectID, AccountID: accountID, Title: "remix", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	starter := &recordingRemixStarter{err: errors.New(`open C:\\secret\\manifest.json: SQL unavailable`)}
	response := performJSON(t, NewProjectsHandler(db, assets.NewService(root), starter, nil), http.MethodPost, "/api/projects/"+projectID+"/remix", nil)
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusInternalServerError || !strings.Contains(string(body), `"code":"project_remix_failed"`) {
		t.Fatalf("status=%d body=%s", response.StatusCode, body)
	}
	if strings.Contains(string(body), "secret") || strings.Contains(string(body), "SQL") {
		t.Fatalf("internal error leaked: %s", body)
	}
}

func TestProjectRemixRealCoordinatorCreatesOneWorkflowAndTaskOnRetry(t *testing.T) {
	_, db, root, accountID := newProjectsTestHandler(t, "active")
	projectID := uuid.NewString()
	now := time.Now().UTC()
	if err := store.NewProjectRepository(db).CreateProject(context.Background(), domain.Project{ID: projectID, AccountID: accountID, Title: "remix", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	launcher := &repositoryWorkflowLauncher{db: db}
	coordinator := workflow.NewRemixCoordinator(store.NewWorkflowRepository(db), store.NewProjectRepository(db), store.NewAssetRepository(db), launcher)
	handler := NewProjectsHandler(db, assets.NewService(root), coordinator, nil)
	var workflowID, taskID string
	for i := 0; i < 2; i++ {
		response := performJSON(t, handler, http.MethodPost, "/api/projects/"+projectID+"/remix", nil)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("attempt %d status=%d body=%s", i+1, response.StatusCode, readResponseBody(t, response))
		}
		var body struct {
			Workflow struct {
				ID string `json:"id"`
			} `json:"workflow"`
			CurrentTask struct {
				ID string `json:"id"`
			} `json:"current_task"`
		}
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			workflowID, taskID = body.Workflow.ID, body.CurrentTask.ID
		} else if body.Workflow.ID != workflowID || body.CurrentTask.ID != taskID {
			t.Fatalf("retry changed workflow/task: %+v", body)
		}
	}
	var workflows, tasks int
	_ = db.QueryRow(`SELECT COUNT(*) FROM project_workflow_runs WHERE project_id=?`, projectID).Scan(&workflows)
	_ = db.QueryRow(`SELECT COUNT(*) FROM codex_tasks WHERE project_id=?`, projectID).Scan(&tasks)
	if workflows != 1 || tasks != 1 || launcher.calls != 1 {
		t.Fatalf("workflows=%d tasks=%d launcher calls=%d", workflows, tasks, launcher.calls)
	}
}

func TestProjectDetailIncludesActiveWorkflowAndCurrentTask(t *testing.T) {
	_, db, root, accountID := newProjectsTestHandler(t, "active")
	projectID := uuid.NewString()
	now := time.Now().UTC()
	if err := store.NewProjectRepository(db).CreateProject(context.Background(), domain.Project{ID: projectID, AccountID: accountID, Title: "active", Stage: domain.StageScript, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	taskID := uuid.NewString()
	pid := projectID
	if err := store.NewTaskRepository(db).Create(context.Background(), domain.CodexTask{ID: taskID, ProjectID: &pid, AccountID: accountID, Type: "topic_commit", SkillName: "finance-topic-selector", Status: domain.TaskRunning, PromptSnapshot: "prompt", ModelName: "gpt-5.4", ReasoningEffort: "high", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	run, err := store.NewWorkflowRepository(db).BeginRemix(context.Background(), domain.ProjectWorkflowRun{ID: uuid.NewString(), ProjectID: projectID, AccountID: accountID, Kind: domain.WorkflowRemix, State: domain.WorkflowRunning, CurrentStep: domain.WorkflowStepTopicCard, ModelName: "gpt-5.4", ReasoningEffort: "high", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.NewWorkflowRepository(db).BindTopicTask(context.Background(), run.ID, taskID, now); err != nil {
		t.Fatal(err)
	}

	response := performJSON(t, NewProjectsHandler(db, assets.NewService(root), nil, nil), http.MethodGet, "/api/projects/"+projectID, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.StatusCode, readResponseBody(t, response))
	}
	var body struct {
		ActiveWorkflow *struct {
			ID          string   `json:"id"`
			CurrentTask taskView `json:"current_task"`
		} `json:"active_workflow"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.ActiveWorkflow == nil || body.ActiveWorkflow.ID != run.ID || body.ActiveWorkflow.CurrentTask.ID != taskID {
		t.Fatalf("active workflow=%+v", body.ActiveWorkflow)
	}
}

func TestProjectViewsUseStoredPublicationStatus(t *testing.T) {
	_, db, root, accountID := newProjectsTestHandler(t, "active")
	projectID := uuid.NewString()
	now := time.Now().UTC()
	_, _ = db.Exec(`INSERT INTO projects(id,account_id,title,stage,publication_status,created_at,updated_at) VALUES(?,?,'status','script','producing',?,?)`, projectID, accountID, now, now)
	handler := NewProjectsHandler(db, assets.NewService(root), nil, nil)
	list := performJSON(t, handler, http.MethodGet, "/api/projects", nil)
	var projects []projectView
	if err := json.NewDecoder(list.Body).Decode(&projects); err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].PublicationStatus != domain.ProjectProducing {
		t.Fatalf("list=%+v", projects)
	}
	detail := performJSON(t, handler, http.MethodGet, "/api/projects/"+projectID, nil)
	var body struct {
		Project projectView `json:"project"`
	}
	if err := json.NewDecoder(detail.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Project.PublicationStatus != domain.ProjectProducing {
		t.Fatalf("detail=%+v", body.Project)
	}
}

func TestProjectPublishResponsesAndNoRemixSideEffect(t *testing.T) {
	_, db, root, accountID := newProjectsTestHandler(t, "active")
	repo := store.NewProjectRepository(db)
	starter := &recordingRemixStarter{}
	handler := NewProjectsHandler(db, assets.NewService(root), starter, nil)
	create := func(stage domain.ProjectStage) string {
		id := uuid.NewString()
		now := time.Now().UTC()
		if err := repo.CreateProject(context.Background(), domain.Project{ID: id, AccountID: accountID, Title: "publish", Stage: domain.StageScript, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
		if stage != domain.StageScript {
			if _, err := db.Exec(`UPDATE projects SET stage=? WHERE id=?`, stage, id); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}

	notReview := performJSON(t, handler, http.MethodPost, "/api/projects/"+create(domain.StageScript)+"/publish", nil)
	assertErrorCode(t, notReview, http.StatusConflict, "project_not_in_review")
	reviewID := create(domain.StageReview)
	published := performJSON(t, handler, http.MethodPost, "/api/projects/"+reviewID+"/publish", nil)
	if published.StatusCode != http.StatusOK {
		t.Fatalf("publish status=%d body=%s", published.StatusCode, readResponseBody(t, published))
	}
	var body struct {
		Stage             domain.ProjectStage  `json:"stage"`
		PublicationStatus domain.ProjectStatus `json:"publication_status"`
		PublishedAt       *time.Time           `json:"published_at"`
	}
	if err := json.NewDecoder(published.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Stage != domain.StagePublished || body.PublicationStatus != domain.ProjectPublished || body.PublishedAt == nil {
		t.Fatalf("published response=%+v", body)
	}
	if len(starter.calls) != 0 {
		t.Fatalf("publish called remix coordinator %d times", len(starter.calls))
	}
}

func TestProjectDetailReviewDoesNotRequireFinalVideo(t *testing.T) {
	id := uuid.NewString()
	handler := newProjectsHandler(&failingProjectStore{stage: domain.StageReview}, assets.NewService(t.TempDir()))
	response := performJSON(t, handler, http.MethodGet, "/api/projects/"+id, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", response.StatusCode)
	}
	var body struct {
		Missing []string `json:"missing_assets"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Missing) != 0 {
		t.Fatalf("missing=%v", body.Missing)
	}
}

func TestProjectDetailIncludesCurrentAssetAndBackgroundStates(t *testing.T) {
	handler, db, root, accountID := newProjectsTestHandler(t, "active")
	if err := os.WriteFile(filepath.Join(root, "bg.png"), []byte("background"), 0o600); err != nil {
		t.Fatal(err)
	}
	created := projectJSON(t, performJSON(t, handler, http.MethodPost, "/api/projects", map[string]any{"account_id": accountID, "title": "states"}))
	for _, file := range []struct {
		typ, name string
		data      []byte
	}{
		{"continuous_script", "script.txt", []byte("script")},
		{"audio", "voice.mp3", validProjectMP3()},
		{"subtitle", "captions.srt", []byte("1\n00:00:00,000 --> 00:00:01,000\nsubtitle\n")},
	} {
		if response := uploadProjectFile(t, handler, created.ID, file.typ, file.name, file.data); response.StatusCode != http.StatusCreated {
			t.Fatalf("upload %s=%d", file.typ, response.StatusCode)
		}
	}
	if _, err := db.Exec(`UPDATE asset_versions SET state='stale' WHERE project_id=? AND type='narration'`, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE asset_versions SET state='failed' WHERE project_id=? AND type='subtitle_srt'`, created.ID); err != nil {
		t.Fatal(err)
	}

	type stateView struct {
		State string `json:"state"`
	}
	readDetail := func() struct {
		Assets              map[string]stateView `json:"assets"`
		BackgroundReference stateView            `json:"background_reference"`
	} {
		t.Helper()
		response := performJSON(t, handler, http.MethodGet, "/api/projects/"+created.ID, nil)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("detail status=%d", response.StatusCode)
		}
		var body struct {
			Assets              map[string]stateView `json:"assets"`
			BackgroundReference stateView            `json:"background_reference"`
		}
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		return body
	}

	body := readDetail()
	if body.Assets["continuous_script"].State != "ready" || body.Assets["narration"].State != "stale" || body.Assets["subtitle_srt"].State != "failed" {
		t.Fatalf("asset states=%+v", body.Assets)
	}
	if body.BackgroundReference.State != "ready" {
		t.Fatalf("background state=%q", body.BackgroundReference.State)
	}
	if _, err := db.Exec(`UPDATE asset_versions SET state='stale' WHERE account_id=? AND type='account_background'`, accountID); err != nil {
		t.Fatal(err)
	}
	if body = readDetail(); body.BackgroundReference.State != "stale" {
		t.Fatalf("stale background state=%q", body.BackgroundReference.State)
	}
}

func TestProjectDetailUsesNewestSourceScriptVersion(t *testing.T) {
	handler, _, _, accountID := newProjectsTestHandler(t, "active")
	created := projectJSON(t, performJSON(t, handler, http.MethodPost, "/api/projects", map[string]any{"account_id": accountID, "title": "source history"}))
	for _, upload := range []struct {
		filename string
		data     []byte
	}{
		{filename: "source-v1.txt", data: []byte("第一版同行原文")},
		{filename: "source-v2.md", data: []byte("# 第二版同行原文")},
	} {
		response := uploadProjectFile(t, handler, created.ID, "source_script", upload.filename, upload.data)
		if response.StatusCode != http.StatusCreated {
			t.Fatalf("upload %q status=%d body=%s", upload.filename, response.StatusCode, readResponseBody(t, response))
		}
	}

	response := performJSON(t, handler, http.MethodGet, "/api/projects/"+created.ID, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("detail status=%d body=%s", response.StatusCode, readResponseBody(t, response))
	}
	var body struct {
		Assets       map[string]assetView   `json:"assets"`
		AssetHistory map[string][]assetView `json:"asset_history"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	current, ok := body.Assets["source_script"]
	if !ok || current.Version != 2 || current.Filename != "source-v2.md" {
		t.Fatalf("current source script=%+v", current)
	}
	if got := len(body.AssetHistory["source_script"]); got != 2 {
		t.Fatalf("source script history length=%d, want 2", got)
	}
}

func TestProjectMoveRejectsDeprecatedTopicAndReadyStages(t *testing.T) {
	handler, _, _, accountID := newProjectsTestHandler(t, "active")
	created := projectJSON(t, performJSON(t, handler, http.MethodPost, "/api/projects", map[string]any{"account_id": accountID}))
	for _, stage := range []string{"topic", "ready"} {
		response := performJSON(t, handler, http.MethodPost, "/api/projects/"+created.ID+"/move", map[string]string{"stage": stage})
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("move %s status=%d", stage, response.StatusCode)
		}
	}
}

func assertErrorCode(t *testing.T, response *http.Response, status int, code string) {
	t.Helper()
	defer response.Body.Close()
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != status || body.Code != code {
		t.Fatalf("status=%d code=%q, want %d %q", response.StatusCode, body.Code, status, code)
	}
}

func readResponseBody(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	b, _ := io.ReadAll(response.Body)
	return string(b)
}

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
	if created.Stage != "script" || created.Title == "" {
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
	response := performJSON(t, handler, http.MethodGet, "/api/projects?account_id="+activeID+"&stage=script&q=alp", nil)
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
	return NewProjectsHandler(db, assets.NewService(root), nil, nil), db, root, id
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
func TestUploadAcceptsCurrentNarrationAndSubtitleTypes(t *testing.T) {
	handler, _, root, accountID := newProjectsTestHandler(t, "active")
	created := projectJSON(t, performJSON(t, handler, http.MethodPost, "/api/projects", map[string]any{"account_id": accountID, "title": "current-upload-types"}))
	for _, file := range []struct {
		typ, name string
		data      []byte
	}{
		{"narration", "voice.mp3", validProjectMP3()},
		{"subtitle_srt", "captions.srt", []byte("1\n00:00:00,000 --> 00:00:01,000\n字幕\n")},
	} {
		response := uploadProjectFile(t, handler, created.ID, file.typ, file.name, file.data)
		if response.StatusCode != http.StatusCreated {
			t.Fatalf("upload %s status=%d", file.typ, response.StatusCode)
		}
		if _, err := os.Stat(filepath.Join(root, "projects", created.ID, file.typ)); err != nil {
			t.Fatalf("missing %s directory: %v", file.typ, err)
		}
	}
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
