package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"video-production-console/internal/domain"
	"video-production-console/internal/store"
)

type timingPreparer struct {
	repo *store.TaskRepository
	err  error
}

func (p timingPreparer) Prepare(ctx context.Context, task domain.CodexTask, req TaskManifestRequest) error {
	if p.err != nil {
		return p.err
	}
	_, err := p.repo.EnsurePreparedTaskAt(ctx, task, "timing-snapshot", "manifests/"+task.ID+".json", req.PreparationStartedAt)
	return err
}

type unpreparedTimingPreparer struct{ repo *store.TaskRepository }

func (p unpreparedTimingPreparer) Prepare(ctx context.Context, task domain.CodexTask, _ TaskManifestRequest) error {
	return p.repo.CreateV2(ctx, task)
}

func TestTimingHTTPContractSummaryRunsAndTaskDetail(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/timing-http.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	created := time.Date(2026, 8, 8, 8, 0, 0, 0, time.UTC)
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('a','A','#fff','active',?,?)`, created, created); err != nil {
		t.Fatal(err)
	}
	started, finished := created.Add(5*time.Second), created.Add(15*time.Second)
	task := domain.CodexTask{ID: "timing-http-task", AccountID: "a", Type: "remix", SkillName: "finance-viral-remix", Action: domain.ActionRemixEnhanced, Status: domain.TaskCompleted, PromptSnapshot: "p", CreatedAt: created, StartedAt: &started, FinishedAt: &finished}
	tasks := store.NewTaskRepository(db)
	if err := tasks.CreateV2(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE codex_tasks SET status='completed',started_at=?,finished_at=? WHERE id=?`, started, finished, task.ID); err != nil {
		t.Fatal(err)
	}
	timings := store.NewTaskTimingRepository(db)
	phase, err := timings.StartPhase(context.Background(), store.StartPhase{TaskID: task.ID, Attempt: 1, Key: "codex_execution", DisplayName: "Codex 执行", Source: domain.PhaseSourceHost, At: started})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := timings.FinishPhase(context.Background(), store.FinishPhase{ID: phase.ID, State: domain.PhaseCompleted, At: created.Add(13 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	handler := NewTasksHandler(db, nil, nil, nil)
	for _, path := range []string{"/api/tasks/" + task.ID + "/timing/summary", "/api/tasks/" + task.ID + "/timing/runs", "/api/tasks/" + task.ID} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, recorder.Code, recorder.Body.String())
		}
		var body any
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s invalid JSON: %v", path, err)
		}
		if path == "/api/tasks/"+task.ID+"/timing/summary" {
			var summary struct {
				TaskID      string `json:"task_id"`
				TotalMS     int64  `json:"total_ms"`
				ExecutionMS int64  `json:"execution_ms"`
				Phases      []struct {
					PhaseKey    string `json:"phase_key"`
					DisplayName string `json:"display_name"`
					DurationMS  *int64 `json:"duration_ms"`
				} `json:"phases"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &summary); err != nil {
				t.Fatal(err)
			}
			if summary.TaskID != task.ID || summary.TotalMS != 15000 || summary.ExecutionMS != 10000 || len(summary.Phases) != 1 || summary.Phases[0].PhaseKey != "codex_execution" || summary.Phases[0].DisplayName != "Codex 执行" || summary.Phases[0].DurationMS == nil || *summary.Phases[0].DurationMS != 8000 {
				t.Fatalf("summary=%+v", summary)
			}
			assertSnakeCaseKeys(t, path, recorder.Body.Bytes())
		}
		if path == "/api/tasks/"+task.ID+"/timing/runs" {
			var runs []struct {
				TaskID     string `json:"task_id"`
				PhaseKey   string `json:"phase_key"`
				State      string `json:"state"`
				DurationMS *int64 `json:"duration_ms"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &runs); err != nil {
				t.Fatal(err)
			}
			if len(runs) != 1 || runs[0].TaskID != task.ID || runs[0].PhaseKey != "codex_execution" || runs[0].State != "completed" || runs[0].DurationMS == nil || *runs[0].DurationMS != 8000 {
				t.Fatalf("runs=%+v", runs)
			}
			assertSnakeCaseKeys(t, path, recorder.Body.Bytes())
		}
		if path == "/api/tasks/"+task.ID {
			var detail struct {
				TimingSummary *struct {
					TaskID string `json:"task_id"`
					Phases []struct {
						PhaseKey string `json:"phase_key"`
					} `json:"phases"`
				} `json:"timing_summary"`
				TimingRuns []struct {
					PhaseKey string `json:"phase_key"`
				} `json:"timing_runs"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &detail); err != nil {
				t.Fatal(err)
			}
			if detail.TimingSummary == nil || detail.TimingSummary.TaskID != task.ID || len(detail.TimingSummary.Phases) != 1 || detail.TimingSummary.Phases[0].PhaseKey != "codex_execution" || len(detail.TimingRuns) != 1 || detail.TimingRuns[0].PhaseKey != "codex_execution" {
				t.Fatalf("detail=%+v", detail)
			}
			assertSnakeCaseKeys(t, path, recorder.Body.Bytes())
		}
	}
}

func TestTimingResponsesExposeExpectedSnakeCaseKeys(t *testing.T) {
	summary := domain.TaskTimingSummary{Phases: []domain.TaskPhaseRun{{}}, SlowestPhase: &domain.TaskPhaseRun{}}
	encodedSummary, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	var summaryKeys map[string]json.RawMessage
	if err := json.Unmarshal(encodedSummary, &summaryKeys); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"task_id", "total_ms", "preparation_ms", "queue_ms", "execution_ms", "queue_estimated", "slowest_phase", "slowest_phase_percent", "phases", "legacy_without_phases"} {
		if _, ok := summaryKeys[key]; !ok {
			t.Fatalf("summary is missing key %q: %s", key, encodedSummary)
		}
	}
	if len(summaryKeys) != 10 {
		t.Fatalf("summary has unexpected keys: %s", encodedSummary)
	}
	encodedPhase, err := json.Marshal(domain.TaskPhaseRun{})
	if err != nil {
		t.Fatal(err)
	}
	var phaseKeys map[string]json.RawMessage
	if err := json.Unmarshal(encodedPhase, &phaseKeys); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"id", "task_id", "phase_key", "display_name", "external_id", "detail_json", "attempt", "source", "state", "started_at", "running_at", "finished_at", "duration_ms", "created_at"} {
		if _, ok := phaseKeys[key]; !ok {
			t.Fatalf("phase run is missing key %q: %s", key, encodedPhase)
		}
	}
	if len(phaseKeys) != 14 {
		t.Fatalf("phase run has unexpected keys: %s", encodedPhase)
	}
}

// assertSnakeCaseKeys fails when any object key in the payload still carries a
// Go exported field name, which is how untagged structs leak PascalCase.
func assertSnakeCaseKeys(t *testing.T, path string, payload []byte) {
	t.Helper()
	var decoded any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("%s invalid JSON: %v", path, err)
	}
	walkJSONKeys(decoded, func(key string) {
		if !isSnakeCaseKey(key) {
			t.Fatalf("%s returned non snake_case key %q: %s", path, key, payload)
		}
	})
}

func isSnakeCaseKey(key string) bool {
	return key != "" && key == strings.ToLower(key)
}

func TestSnakeCaseKeyGuardRejectsExportedGoFieldNames(t *testing.T) {
	for _, key := range []string{"TaskID", "PhaseKey", "DurationMS", "Phases", ""} {
		if isSnakeCaseKey(key) {
			t.Fatalf("guard accepted non snake_case key %q", key)
		}
	}
	for _, key := range []string{"task_id", "phase_key", "duration_ms", "phases"} {
		if !isSnakeCaseKey(key) {
			t.Fatalf("guard rejected snake_case key %q", key)
		}
	}
}

func walkJSONKeys(value any, visit func(string)) {
	switch node := value.(type) {
	case map[string]any:
		for key, child := range node {
			visit(key)
			walkJSONKeys(child, visit)
		}
	case []any:
		for _, child := range node {
			walkJSONKeys(child, visit)
		}
	}
}

func TestTimingHTTPContractNotFoundAndEmptyRuns(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/timing-http-empty.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 8, 8, 8, 0, 0, 0, time.UTC)
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('a','A','#fff','active',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	task := domain.CodexTask{ID: "timing-empty-task", AccountID: "a", Type: "remix", SkillName: "finance-viral-remix", Action: domain.ActionRemixEnhanced, Status: domain.TaskQueued, PromptSnapshot: "p", CreatedAt: now}
	if err := store.NewTaskRepository(db).CreateV2(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	handler := NewTasksHandler(db, nil, nil, nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/tasks/"+task.ID+"/timing/runs", nil))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "[]\n" {
		t.Fatalf("empty runs status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	for _, path := range []string{"/api/tasks/missing/timing/summary", "/api/tasks/missing/timing/runs", "/api/tasks/missing"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("%s status=%d body=%s", path, recorder.Code, recorder.Body.String())
		}
	}
}
func TestPrepareAndPublishRequiresPreparedManifest(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/unprepared.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('a','A','#fff','active',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	repo := store.NewTaskRepository(db)
	task := domain.CodexTask{ID: "unprepared", AccountID: "a", Type: "remix", SkillName: "finance-viral-remix", Action: domain.ActionRemixEnhanced, Status: domain.TaskQueued, PromptSnapshot: "p", CreatedAt: now}
	published := false
	_, err = prepareAndPublishTask(context.Background(), db, unpreparedTimingPreparer{repo: repo}, task, TaskManifestRequest{}, now, func(context.Context, domain.CodexTask) error { published = true; return nil }, nil)
	if err == nil || published {
		t.Fatalf("err=%v published=%v", err, published)
	}
	if _, _, manifestErr := repo.PreparedManifest(context.Background(), task.ID); !errors.Is(manifestErr, sql.ErrNoRows) {
		t.Fatalf("manifest err=%v", manifestErr)
	}
	phases, readErr := store.NewTaskTimingRepository(db).ForTask(context.Background(), task.ID)
	if readErr != nil || len(phases) != 0 {
		t.Fatalf("phases=%+v err=%v", phases, readErr)
	}
}

func TestPreparationFailureRollsBackTaskWhenTimingPersistenceFails(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/prepare-rollback.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	created := time.Date(2026, 8, 8, 9, 0, 0, 0, time.UTC)
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('a','A','#fff','active',?,?)`, created, created); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER reject_prepare_timing BEFORE INSERT ON task_phase_runs WHEN NEW.phase_key='task_prepare' BEGIN SELECT RAISE(ABORT, 'timing rejected'); END`); err != nil {
		t.Fatal(err)
	}
	task := domain.CodexTask{ID: "task-rollback", AccountID: "a", Type: "remix", SkillName: "finance-viral-remix", Action: domain.ActionRemixEnhanced, Status: domain.TaskQueued, PromptSnapshot: "p", CreatedAt: created}
	_, err = prepareAndPublishTask(context.Background(), db, timingPreparer{repo: store.NewTaskRepository(db), err: errors.New("manifest invalid")}, task, TaskManifestRequest{}, created, func(context.Context, domain.CodexTask) error { return nil }, func() time.Time { return created.Add(time.Second) })
	if err == nil {
		t.Fatal("expected preparation persistence failure")
	}
	if _, readErr := store.NewTaskRepository(db).Get(context.Background(), task.ID); !errors.Is(readErr, sql.ErrNoRows) {
		t.Fatalf("task survived rolled back failure: %v", readErr)
	}
}

func TestTaskPrepareTimingSharedHelperSuccessAndFailure(t *testing.T) {
	for _, test := range []struct {
		name          string
		prepareErr    error
		wantState     domain.TaskPhaseState
		wantScheduled int
	}{
		{name: "success", wantState: domain.PhaseCompleted, wantScheduled: 1},
		{name: "failure", prepareErr: errors.New("manifest invalid"), wantState: domain.PhaseFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, err := store.Open(t.TempDir() + "/prepare.db")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			created := time.Date(2026, 8, 8, 8, 0, 0, 0, time.UTC)
			finished := created.Add(1500 * time.Millisecond)
			if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('a','A','#fff','active',?,?)`, created, created); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO skill_snapshots(id,name,path,sha256,files_json,modified_at,created_at) VALUES('timing-snapshot','finance-viral-remix','skill','abc','[]',?,?)`, created, created); err != nil {
				t.Fatal(err)
			}
			repo := store.NewTaskRepository(db)
			task := domain.CodexTask{ID: "task-" + test.name, AccountID: "a", Type: "remix", SkillName: "finance-viral-remix", Action: domain.ActionRemixEnhanced, Status: domain.TaskQueued, PromptSnapshot: "p", CreatedAt: created}
			scheduled := 0
			_, err = prepareAndPublishTask(context.Background(), db, timingPreparer{repo: repo, err: test.prepareErr}, task, TaskManifestRequest{}, created, func(context.Context, domain.CodexTask) error { scheduled++; return nil }, func() time.Time { return finished })
			if test.prepareErr == nil && err != nil || test.prepareErr != nil && err == nil {
				t.Fatalf("prepare err=%v", err)
			}
			if scheduled != test.wantScheduled {
				t.Fatalf("scheduled=%d", scheduled)
			}
			persisted, readErr := repo.Get(context.Background(), task.ID)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if test.prepareErr != nil {
				if persisted.Status != domain.TaskFailed || persisted.ErrorCode == nil || *persisted.ErrorCode != "task_prepare_failed" || persisted.ErrorMessage == nil || *persisted.ErrorMessage != test.prepareErr.Error() {
					t.Fatalf("task=%+v", persisted)
				}
			}
			phases, readErr := store.NewTaskTimingRepository(db).ForTask(context.Background(), task.ID)
			if readErr != nil || len(phases) != 1 || phases[0].PhaseKey != "task_prepare" || phases[0].DisplayName != "任务准备" || phases[0].State != test.wantState || !phases[0].StartedAt.Equal(created) || phases[0].DurationMS == nil {
				t.Fatalf("phases=%+v err=%v", phases, readErr)
			}
			if test.prepareErr != nil && *phases[0].DurationMS != 1500 {
				t.Fatalf("failed preparation duration=%d", *phases[0].DurationMS)
			}
		})
	}
}
