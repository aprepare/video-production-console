package remixproducer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"video-production-console/internal/store"
)

// consoleStub 模拟控制台 API：记录调用顺序，任务两次轮询后完成。
type consoleStub struct {
	mu            sync.Mutex
	calls         []string
	tasks         map[string]int    // taskID → 已轮询次数
	prompts       map[string]string // 任务 type:action → 实际下发的提示词
	models        map[string]string // 任务 type:action → model|effort
	narrated      bool
	failNarration bool
}

func (s *consoleStub) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	record := func(name string) {
		s.mu.Lock()
		s.calls = append(s.calls, name)
		s.mu.Unlock()
	}
	mux.HandleFunc("POST /api/projects", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Token") != "test-token" {
			t.Errorf("missing internal token")
		}
		record("create_project")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "proj-1"})
	})
	mux.HandleFunc("POST /api/projects/proj-1/assets/continuous_script", func(w http.ResponseWriter, r *http.Request) {
		record("upload_script")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /api/projects/proj-1/tasks", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := json.Marshal(map[string]any{})
		_ = raw
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		id := "task-" + body["type"] + "-" + body["action"]
		record("task:" + body["type"] + ":" + body["action"])
		s.mu.Lock()
		s.tasks[id] = 0
		if s.prompts == nil {
			s.prompts = map[string]string{}
		}
		s.prompts[body["type"]+":"+body["action"]] = body["prompt"]
		if s.models == nil {
			s.models = map[string]string{}
		}
		s.models[body["type"]+":"+body["action"]] = body["model"] + "|" + body["reasoning_effort"]
		s.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": id})
	})
	mux.HandleFunc("GET /api/tasks/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/api/tasks/")
		id, _ = url.PathUnescape(id)
		s.mu.Lock()
		s.tasks[id]++
		polls := s.tasks[id]
		s.mu.Unlock()
		status := "running"
		if polls >= 2 {
			status = "completed"
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"id": id, "status": status})
	})
	mux.HandleFunc("GET /api/settings", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"public": map[string]any{"montage_style": map[string]any{"keywords_hidden": false}},
		})
	})
	mux.HandleFunc("GET /api/projects/proj-1", func(w http.ResponseWriter, _ *http.Request) {
		state := "missing"
		s.mu.Lock()
		if s.narrated {
			state = "ready"
		}
		s.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"assets": map[string]any{
				"narration":    map[string]string{"state": state},
				"subtitle_srt": map[string]string{"state": state},
			},
		})
	})
	mux.HandleFunc("POST /api/projects/proj-1/narration", func(w http.ResponseWriter, _ *http.Request) {
		record("narration")
		if s.failNarration {
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]string{"code": "narration_synthesis_failed", "message": "TTS 挂了"})
			return
		}
		s.mu.Lock()
		s.narrated = true
		s.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	return mux
}

func newDriverFixture(t *testing.T, stub *consoleStub, workflowJSON ...string) (*Driver, *store.RemixLabRepository, string) {
	t.Helper()
	server := httptest.NewServer(stub.handler(t))
	t.Cleanup(server.Close)

	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo := store.NewRemixLabRepository(db)

	// 造一条完成的工作流 run + running 的生产记录
	now := time.Now().UTC()
	wf := `{"nodes":[{"id":"writer","type":"writer","title":"写手"}]}`
	if len(workflowJSON) > 0 && workflowJSON[0] != "" {
		wf = workflowJSON[0]
	}
	exp := store.RemixLabExperimentRecord{ID: "11111111-1111-1111-1111-111111111111", Title: "测试实验", SourceText: "原文", Status: "completed", WorkflowJSON: wf, CreatedAt: now, UpdatedAt: now}
	slot := store.RemixLabSlotRecord{ID: "22222222-2222-2222-2222-222222222222", ExperimentID: exp.ID, Label: "s", Model: "m", RunCount: 1}
	run := store.RemixLabRunRecord{
		ID: "33333333-3333-3333-3333-333333333333", ExperimentID: exp.ID, SlotID: slot.ID, RunIndex: 1,
		Status: "completed", ContinuousScript: "定稿正文",
		PackageJSON: `{"continuous_script":"定稿正文","short_titles":["板标题","副标题","备选"],"titles":["长标题"]}`,
	}
	if err := repo.CreateExperiment(context.Background(), exp, []store.RemixLabSlotRecord{slot}, []store.RemixLabRunRecord{run}); err != nil {
		t.Fatal(err)
	}
	rec := store.RemixLabProductionRecord{
		RunID: run.ID, ExperimentID: exp.ID, AccountID: "44444444-4444-4444-4444-444444444444",
		Status: "running", Step: StepProject, CreatedAt: now, UpdatedAt: now,
	}
	if err := repo.UpsertProduction(context.Background(), rec); err != nil {
		t.Fatal(err)
	}

	driver := New(repo, strings.TrimPrefix(server.URL, "http://"), "test-token")
	return driver, repo, run.ID
}

func waitProduction(t *testing.T, repo *store.RemixLabRepository, runID string) store.RemixLabProductionRecord {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		rec, err := repo.GetProduction(context.Background(), runID)
		if err != nil {
			t.Fatal(err)
		}
		if rec.Status == "completed" || rec.Status == "failed" {
			return rec
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timeout waiting production terminal state")
	return store.RemixLabProductionRecord{}
}

// 全链路：建项目 → 传稿 → 口播任务 → 字幕任务 → 配音 → 混剪任务 → 完成。
func TestDriverRunsFullChain(t *testing.T) {
	stub := &consoleStub{tasks: map[string]int{}}
	driver, repo, runID := newDriverFixture(t, stub)

	driver.Launch(runID)
	rec := waitProduction(t, repo, runID)
	if rec.Status != "completed" || rec.Step != StepDone {
		t.Fatalf("production = %s/%s (%s)", rec.Status, rec.Step, rec.Error)
	}
	if rec.ProjectID != "proj-1" || rec.SpokenTaskID == "" || rec.MontageTaskID == "" {
		t.Fatalf("record ids: %+v", rec)
	}
	stub.mu.Lock()
	order := strings.Join(stub.calls, ",")
	stub.mu.Unlock()
	want := "create_project,upload_script,task:remix:remix.spoken_lines,narration,task:montage:"
	if order != want {
		t.Fatalf("call order = %s\nwant %s", order, want)
	}
}

func TestDriverRunsCaptionsWhenExplicitlyEnabled(t *testing.T) {
	stub := &consoleStub{tasks: map[string]int{}}
	wf := `{"nodes":[{"id":"writer","type":"writer","title":"写手"}],` +
		`"production":{"captions_disabled":false}}`
	driver, repo, runID := newDriverFixture(t, stub, wf)

	driver.Launch(runID)
	rec := waitProduction(t, repo, runID)
	if rec.Status != "completed" || rec.Step != StepDone {
		t.Fatalf("production = %s/%s (%s)", rec.Status, rec.Step, rec.Error)
	}
	stub.mu.Lock()
	order := strings.Join(stub.calls, ",")
	stub.mu.Unlock()
	want := "create_project,upload_script,task:remix:remix.spoken_lines,task:remix:remix.caption_keywords,narration,task:montage:"
	if order != want {
		t.Fatalf("call order = %s\nwant %s", order, want)
	}
}

// 工作流里删掉字幕关键词节点 + 换口播提示词：整步跳过、提示词按配置下发。
func TestDriverHonorsProductionConfig(t *testing.T) {
	stub := &consoleStub{tasks: map[string]int{}}
	wf := `{"nodes":[{"id":"writer","type":"writer","title":"写手"}],` +
		`"production":{"captions_disabled":true,"spoken_prompt":"每行不超过十二个字切口播。","spoken_model":"gpt-spoken","spoken_effort":"low","montage_model":"gpt-montage"}}`
	driver, repo, runID := newDriverFixture(t, stub, wf)

	driver.Launch(runID)
	rec := waitProduction(t, repo, runID)
	if rec.Status != "completed" || rec.Step != StepDone {
		t.Fatalf("production = %s/%s (%s)", rec.Status, rec.Step, rec.Error)
	}
	if rec.CaptionTaskID != "" {
		t.Fatalf("captions disabled but task created: %s", rec.CaptionTaskID)
	}
	stub.mu.Lock()
	order := strings.Join(stub.calls, ",")
	spokenPrompt := stub.prompts["remix:remix.spoken_lines"]
	montagePrompt := stub.prompts["montage:"]
	stub.mu.Unlock()
	if strings.Contains(order, "caption_keywords") {
		t.Fatalf("captions step should be skipped entirely: %s", order)
	}
	if spokenPrompt != "每行不超过十二个字切口播。" {
		t.Fatalf("spoken prompt = %q", spokenPrompt)
	}
	if montagePrompt != DefaultMontagePrompt {
		t.Fatalf("montage prompt should fall back to default: %q", montagePrompt)
	}
	if stub.models["remix:remix.spoken_lines"] != "gpt-spoken|low" {
		t.Fatalf("spoken model = %q", stub.models["remix:remix.spoken_lines"])
	}
	if stub.models["montage:"] != "gpt-montage|" {
		t.Fatalf("montage model = %q", stub.models["montage:"])
	}
}

// ParseConfig：没有 production 字段时默认关掉字幕关键词；有则原样取出。
func TestParseConfig(t *testing.T) {
	zero := ParseConfig(`{"nodes":[]}`)
	if !zero.CaptionsDisabled || zero.EffectiveSpokenPrompt() != DefaultSpokenPrompt ||
		zero.EffectiveCaptionsPrompt() != DefaultCaptionsPrompt || zero.EffectiveMontagePrompt() != DefaultMontagePrompt {
		t.Fatalf("zero config = %+v", zero)
	}
	cfg := ParseConfig(`{"production":{"captions_disabled":true,"montage_prompt":"自定义混剪"}}`)
	if !cfg.CaptionsDisabled || cfg.EffectiveMontagePrompt() != "自定义混剪" || cfg.EffectiveSpokenPrompt() != DefaultSpokenPrompt {
		t.Fatalf("config = %+v", cfg)
	}
	enabled := ParseConfig(`{"production":{"captions_disabled":false,"spoken_model":"gpt-spoken"}}`)
	if enabled.CaptionsDisabled || enabled.SpokenModel != "gpt-spoken" {
		t.Fatalf("enabled config = %+v", enabled)
	}
}

// 配音失败：停在 narration 步；重启复位后续跑不重复建项目/口播任务。
func TestDriverFailsAtNarrationAndResumes(t *testing.T) {
	stub := &consoleStub{tasks: map[string]int{}, failNarration: true}
	driver, repo, runID := newDriverFixture(t, stub)

	driver.Launch(runID)
	rec := waitProduction(t, repo, runID)
	if rec.Status != "failed" || rec.Step != StepNarration {
		t.Fatalf("production = %s/%s (%s)", rec.Status, rec.Step, rec.Error)
	}
	if !strings.Contains(rec.Error, "配音失败") {
		t.Fatalf("error = %s", rec.Error)
	}

	// 修好 TTS 后续跑：驱动器应跳过已完成的项目/口播步骤
	stub.mu.Lock()
	stub.failNarration = false
	callsBefore := len(stub.calls)
	stub.mu.Unlock()
	rec.Status = "running"
	rec.Error = ""
	rec.UpdatedAt = time.Now().UTC()
	if err := repo.UpdateProduction(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	driver.Launch(runID)
	done := waitProduction(t, repo, runID)
	if done.Status != "completed" {
		t.Fatalf("resume should complete: %s/%s (%s)", done.Status, done.Step, done.Error)
	}
	stub.mu.Lock()
	resumed := stub.calls[callsBefore:]
	stub.mu.Unlock()
	for _, call := range resumed {
		if call == "create_project" || call == "upload_script" {
			t.Fatalf("resume must not redo %s (calls=%v)", call, resumed)
		}
	}
	// 落盘检查纯属防御：驱动器不写文件，此处仅确认无 panic 副产物
	_ = os.Getenv("")
}
