package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/domain"
	"video-production-console/internal/imageproject"
	consoleSettings "video-production-console/internal/settings"
	"video-production-console/internal/store"
)

func TestImageProjectsQuickGenerateReturns202BeforePlannerFinishes(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "quick.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	planner := newBlockingQuickPlanner(validQuickPlanJSON("第一句。第二句。"), promptJSON(2))
	handler := NewImageProjectsHandlerWithPlanner(db, quickRuntime(t), succeedingImageGenerator(t), planner)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/image-projects/quick-generate", bytes.NewReader(quickGenerateBody()))
		request.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(recorder, request)
		done <- recorder
	}()
	select {
	case recorder := <-done:
		if recorder.Code != http.StatusAccepted {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		var payload map[string]string
		if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if payload["project_id"] == "" || payload["run_status"] != "running" {
			t.Fatalf("payload=%v", payload)
		}
		repo := store.NewImageProjectRepository(db)
		project, _, err := repo.Get(context.Background(), payload["project_id"])
		if err != nil {
			t.Fatal(err)
		}
		if project.RunMode != "quick" || project.RunStatus != "running" {
			t.Fatalf("persisted=%+v", project)
		}
		if planner.planCalls.Load() != 0 && !planner.blocked.Load() {
			t.Fatal("planner finished before release")
		}
		planner.release()
		waitForImageRun(t, repo, payload["project_id"], 3*time.Second, func(got domain.ImageProject, _ []domain.ImageProjectItem) bool {
			return got.RunPhase == "completed" && got.RunStatus == "completed"
		})
	case <-time.After(2 * time.Second):
		t.Fatal("quick-generate blocked on planner")
	}
}

func TestImageProjectsQuickGenerateAdvancesPhasesAndPartialSuccess(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "quick-phases.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	planner := newBlockingQuickPlanner(validQuickPlanJSON("第一句。第二句。"), promptJSON(2))
	planner.release()
	handler := NewImageProjectsHandlerWithPlanner(db, quickRuntime(t), &partialImageGenerator{failSequence: 2, result: succeedingImageGenerator(t).result}, planner)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/image-projects/quick-generate", bytes.NewReader(quickGenerateBody()))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]string
	_ = json.Unmarshal(recorder.Body.Bytes(), &payload)
	project, items := waitForImageRun(t, store.NewImageProjectRepository(db), payload["project_id"], 3*time.Second, func(got domain.ImageProject, gotItems []domain.ImageProjectItem) bool {
		return got.RunPhase == "completed" && got.RunStatus == "completed" && len(gotItems) == 2
	})
	if project.SuccessCount != 1 || project.FailureCount != 1 || project.ImageCount != 2 {
		t.Fatalf("counts=%+v items=%+v", project, items)
	}
}

func TestImageProjectsQuickGenerateContinuesWhenPublishingIsInvalid(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "quick-pub.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	planner := newBlockingQuickPlanner(invalidPublishingQuickPlanJSON("第一句。"), promptJSON(1))
	planner.release()
	handler := NewImageProjectsHandlerWithPlanner(db, quickRuntime(t), succeedingImageGenerator(t), planner)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/image-projects/quick-generate", bytes.NewReader(quickGenerateBodyScript("第一句。")))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, request)
	var payload map[string]string
	_ = json.Unmarshal(recorder.Body.Bytes(), &payload)
	project, _ := waitForImageRun(t, store.NewImageProjectRepository(db), payload["project_id"], 3*time.Second, func(got domain.ImageProject, gotItems []domain.ImageProjectItem) bool {
		return got.RunPhase == "completed" && len(gotItems) == 1 && gotItems[0].Status == "ready"
	})
	if project.PublishingError == "" {
		t.Fatalf("expected publishing error: %+v", project)
	}
}

func TestImageProjectsResumeRejectsDuplicateWorker(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "quick-resume.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := store.NewImageProjectRepository(db)
	now := time.Now().UTC()
	project := domain.ImageProject{
		ID: uuid.NewString(), Title: "resume", Script: "第一句。",
		ImageCount: 1, Ratio: "3:4", Style: "finance_documentary", Concurrency: 1,
		Status: "draft", RunMode: "quick", RunPhase: "prompting", RunStatus: "failed",
		ImageAttempts: 2, CreatedAt: now, UpdatedAt: now,
	}
	item := domain.ImageProjectItem{ID: uuid.NewString(), ProjectID: project.ID, Sequence: 1, Role: "cover", SourceText: "第一句。", Title: "第一句", Status: "pending", CreatedAt: now, UpdatedAt: now}
	if err := repo.Create(context.Background(), project, []domain.ImageProjectItem{item}); err != nil {
		t.Fatal(err)
	}
	planner := newBlockingQuickPlanner("", promptJSON(1))
	handler := NewImageProjectsHandlerWithPlanner(db, quickRuntime(t), succeedingImageGenerator(t), planner)
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodPost, "/api/image-projects/"+project.ID+"/resume", nil))
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest(http.MethodPost, "/api/image-projects/"+project.ID+"/resume", nil))
	if first.Code != http.StatusAccepted && first.Code != http.StatusOK {
		t.Fatalf("first resume=%d body=%s", first.Code, first.Body.String())
	}
	if second.Code != http.StatusConflict {
		t.Fatalf("second resume=%d body=%s", second.Code, second.Body.String())
	}
	planner.release()
	waitForImageRun(t, repo, project.ID, 3*time.Second, func(got domain.ImageProject, _ []domain.ImageProjectItem) bool {
		return got.RunStatus == "completed"
	})
}

func TestImageProjectsResumeFromPromptingDoesNotReplan(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "quick-replan.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := store.NewImageProjectRepository(db)
	now := time.Now().UTC()
	project := domain.ImageProject{
		ID: uuid.NewString(), Title: "prompting", Script: "第一句。",
		ImageCount: 1, Ratio: "3:4", Style: "finance_documentary", Concurrency: 1,
		Status: "draft", RunMode: "quick", RunPhase: "prompting", RunStatus: "failed",
		ImageAttempts: 2, CreatedAt: now, UpdatedAt: now,
	}
	item := domain.ImageProjectItem{ID: uuid.NewString(), ProjectID: project.ID, Sequence: 1, Role: "cover", SourceText: "第一句。", Title: "第一句", Status: "pending", CreatedAt: now, UpdatedAt: now}
	if err := repo.Create(context.Background(), project, []domain.ImageProjectItem{item}); err != nil {
		t.Fatal(err)
	}
	planner := newBlockingQuickPlanner(validQuickPlanJSON("第一句。"), promptJSON(1))
	planner.release()
	handler := NewImageProjectsHandlerWithPlanner(db, quickRuntime(t), succeedingImageGenerator(t), planner)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/image-projects/"+project.ID+"/resume", nil))
	if recorder.Code != http.StatusAccepted && recorder.Code != http.StatusOK {
		t.Fatalf("resume=%d body=%s", recorder.Code, recorder.Body.String())
	}
	waitForImageRun(t, repo, project.ID, 3*time.Second, func(got domain.ImageProject, items []domain.ImageProjectItem) bool {
		return got.RunStatus == "completed" && len(items) == 1 && items[0].Prompt != ""
	})
	if planner.planCalls.Load() != 0 {
		t.Fatalf("planning was called again: %d", planner.planCalls.Load())
	}
	if planner.promptCalls.Load() == 0 {
		t.Fatal("prompting was not called")
	}
}

func TestImageProjectsConstructorMarksRunningQuickJobsInterrupted(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "quick-interrupt.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := store.NewImageProjectRepository(db)
	now := time.Now().UTC()
	quick := domain.ImageProject{
		ID: uuid.NewString(), Title: "running", Script: "第一句。",
		ImageCount: 1, Ratio: "3:4", Style: "finance_documentary", Concurrency: 1,
		Status: "draft", RunMode: "quick", RunPhase: "imaging", RunStatus: "running",
		ImageAttempts: 2, CreatedAt: now, UpdatedAt: now,
	}
	manual := domain.ImageProject{
		ID: uuid.NewString(), Title: "manual", Script: "第二句。",
		ImageCount: 1, Ratio: "3:4", Style: "finance_documentary", Concurrency: 1,
		Status: "draft", CreatedAt: now, UpdatedAt: now,
	}
	if err := repo.Create(context.Background(), quick, nil); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(context.Background(), manual, nil); err != nil {
		t.Fatal(err)
	}
	_ = NewImageProjectsHandlerWithPlanner(db, quickRuntime(t), succeedingImageGenerator(t), newBlockingQuickPlanner("", ""))
	gotQuick, _, err := repo.Get(context.Background(), quick.ID)
	if err != nil {
		t.Fatal(err)
	}
	gotManual, _, err := repo.Get(context.Background(), manual.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotQuick.RunStatus != "interrupted" {
		t.Fatalf("quick=%+v", gotQuick)
	}
	if gotManual.RunStatus != "idle" {
		t.Fatalf("manual=%+v", gotManual)
	}
}

func TestImageProjectsPatchTitleValidatesAndSaves(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "quick-title.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := store.NewImageProjectRepository(db)
	now := time.Now().UTC()
	project := domain.ImageProject{
		ID: uuid.NewString(), Title: "旧名称", Script: "第一句。",
		ImageCount: 1, Ratio: "3:4", Style: "finance_documentary", Concurrency: 1,
		Status: "draft", RunMode: "quick", RunPhase: "completed", RunStatus: "completed",
		ImageAttempts: 2, CreatedAt: now, UpdatedAt: now,
	}
	if err := repo.Create(context.Background(), project, nil); err != nil {
		t.Fatal(err)
	}
	handler := NewImageProjectsHandlerWithPlanner(db, quickRuntime(t), succeedingImageGenerator(t), newBlockingQuickPlanner("", ""))
	ok := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/image-projects/"+project.ID, strings.NewReader(`{"title":" 用户改名 "}`))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(ok, req)
	if ok.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", ok.Code, ok.Body.String())
	}
	var detail domain.ImageProjectDetail
	if err := json.Unmarshal(ok.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Project.Title != "用户改名" || detail.Project.RunStatus != "completed" {
		t.Fatalf("detail=%+v", detail.Project)
	}
	blank := httptest.NewRecorder()
	blankReq := httptest.NewRequest(http.MethodPatch, "/api/image-projects/"+project.ID, strings.NewReader(`{"title":"   "}`))
	blankReq.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(blank, blankReq)
	if blank.Code != http.StatusBadRequest {
		t.Fatalf("blank status=%d", blank.Code)
	}
	long := httptest.NewRecorder()
	longReq := httptest.NewRequest(http.MethodPatch, "/api/image-projects/"+project.ID, strings.NewReader(`{"title":"`+strings.Repeat("名", 121)+`"}`))
	longReq.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(long, longReq)
	if long.Code != http.StatusBadRequest {
		t.Fatalf("long status=%d", long.Code)
	}
}

type blockingQuickPlanner struct {
	planJSON    string
	promptJSON  string
	gate        chan struct{}
	blocked     atomic.Bool
	planCalls   atomic.Int32
	promptCalls atomic.Int32
}

func newBlockingQuickPlanner(planJSON, promptJSON string) *blockingQuickPlanner {
	return &blockingQuickPlanner{planJSON: planJSON, promptJSON: promptJSON, gate: make(chan struct{})}
}

func (p *blockingQuickPlanner) release() {
	select {
	case <-p.gate:
	default:
		close(p.gate)
	}
}

func (p *blockingQuickPlanner) Complete(_ context.Context, request imageproject.ChatRequest) (string, error) {
	p.blocked.Store(true)
	<-p.gate
	p.blocked.Store(false)
	switch request.ResponseSchemaName {
	case "image_quick_plan":
		p.planCalls.Add(1)
		return p.planJSON, nil
	case "image_prompts":
		p.promptCalls.Add(1)
		return p.promptJSON, nil
	default:
		return "", nil
	}
}

type succeedingGen struct {
	result imageproject.GenerateResult
}

func (g succeedingGen) Generate(context.Context, imageproject.GenerateRequest) (imageproject.GenerateResult, error) {
	return g.result, nil
}

func succeedingImageGenerator(t *testing.T) succeedingGen {
	t.Helper()
	imageBytes, err := decodeTestPNG(t)
	if err != nil {
		t.Fatal(err)
	}
	return succeedingGen{result: imageproject.GenerateResult{Bytes: imageBytes, MIMEType: "image/png", Width: 3, Height: 4}}
}

type partialImageGenerator struct {
	failSequence int
	result       imageproject.GenerateResult
	calls        atomic.Int32
}

func (g *partialImageGenerator) Generate(_ context.Context, request imageproject.GenerateRequest) (imageproject.GenerateResult, error) {
	n := int(g.calls.Add(1))
	if n == g.failSequence {
		return imageproject.GenerateResult{}, errQuickImageFailed
	}
	return g.result, nil
}

var errQuickImageFailed = errString("vendor unavailable")

type errString string

func (e errString) Error() string { return string(e) }

func quickRuntime(t *testing.T) imageRuntimeStub {
	t.Helper()
	return imageRuntimeStub{runtime: consoleSettings.Runtime{PublicSettings: domain.PublicSettings{
		DataRoot: t.TempDir(), ImageBaseURL: "https://img.test/v1", ImageModel: "gpt-image-2",
		MaxImageConcurrency: 2, ImageGenerationAttempts: 2, ImageTextModel: "planner-test",
		ImageTextBaseURL: "https://text.example.test/v1",
	}, ImageAPIKey: "k", ImageTextAPIKey: "tk"}}
}

func quickGenerateBody() []byte {
	return quickGenerateBodyScript("第一句。第二句。")
}

func quickGenerateBodyScript(script string) []byte {
	body, _ := json.Marshal(map[string]any{
		"script": script, "image_count": 0, "ratio": "3:4", "style": "finance_documentary",
		"custom_style": "", "concurrency": 3, "text_model": "gpt-5.6-sol",
		"reasoning_effort": "medium", "image_model": "gpt-image-2", "image_attempts": 2,
	})
	return body
}

func validQuickPlanJSON(script string) string {
	segments := testSegmentsForScript(script)
	payload, _ := json.Marshal(map[string]any{
		"project_title": "存款流向",
		"segments":      segments,
		"publishing_candidates": []map[string]any{
			{"position": 1, "title": "存款流向", "description": "说明一 #存款 #财富管理 #思维提升"},
			{"position": 2, "title": "钱去哪了", "description": "说明二 #存款 #理财 #认知"},
			{"position": 3, "title": "现金流", "description": "说明三 #存款 #财富 #干货"},
			{"position": 4, "title": "复利", "description": "说明四 #复利 #理财 #思维"},
			{"position": 5, "title": "风险", "description": "说明五 #风险 #存款 #认知"},
		},
	})
	return string(payload)
}

func invalidPublishingQuickPlanJSON(script string) string {
	segments := testSegmentsForScript(script)
	payload, _ := json.Marshal(map[string]any{
		"project_title":         "存款流向",
		"segments":              segments,
		"publishing_candidates": []map[string]any{},
	})
	return string(payload)
}

func promptJSON(count int) string {
	prompts := make([]map[string]any, 0, count)
	for i := 1; i <= count; i++ {
		prompts = append(prompts, map[string]any{"sequence": i, "prompt": "提示词"})
	}
	payload, _ := json.Marshal(map[string]any{"prompts": prompts})
	return string(payload)
}

func waitForImageRun(t *testing.T, repo *store.ImageProjectRepository, id string, timeout time.Duration, pred func(domain.ImageProject, []domain.ImageProjectItem) bool) (domain.ImageProject, []domain.ImageProjectItem) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last domain.ImageProject
	var items []domain.ImageProjectItem
	for time.Now().Before(deadline) {
		project, gotItems, err := repo.Get(context.Background(), id)
		if err == nil {
			last, items = project, gotItems
			if pred(project, gotItems) {
				return project, gotItems
			}
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for project %s: %+v items=%+v", id, last, items)
	return last, items
}

func decodeTestPNG(t *testing.T) ([]byte, error) {
	t.Helper()
	return base64.StdEncoding.DecodeString(testImagePNG(t))
}
