package codex

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/domain"
	"video-production-console/internal/security"
	"video-production-console/internal/store"
)

func init() {
	if os.Getenv("VIDEO_CONSOLE_RUNNER_HELPER") != "oversized" {
		return
	}
	_, _ = fmt.Fprintln(os.Stdout, strings.Repeat("x", 1025))
	_ = os.Stdout.Sync()
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

func TestRunnerOversizedJSONLHelper(t *testing.T) {}

func TestRunnerKillsBlockedChildOnOversizedJSONL(t *testing.T) {
	fixture := newTestRunner(t, "completed")
	cmd := exec.Command(os.Args[0], "-test.run=^TestRunnerOversizedJSONLHelper$", "-test.count=1")
	cmd.Env = append(os.Environ(), "VIDEO_CONSOLE_RUNNER_HELPER=oversized")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close() })
	runner := NewRunner(cmd, fixture.repo, fixture.taskID, fixture.root, nil)
	runner.maxJSONLBytes = 1024
	termination := make(chan error, 1)
	runner.terminate = func(child *exec.Cmd) {
		if child.Process == nil {
			termination <- fmt.Errorf("terminate called before child started")
			return
		}
		termination <- child.Process.Kill()
	}
	err = runner.Run(context.Background())
	if err == nil {
		t.Fatal("expected oversized stream failure")
	}
	if err := <-termination; err != nil {
		t.Fatalf("terminate blocked child: %v", err)
	}
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() || cmd.ProcessState.Success() {
		t.Fatalf("blocked child was not killed: state=%+v", cmd.ProcessState)
	}
	task, err := fixture.repo.Get(context.Background(), fixture.taskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != domain.TaskFailed {
		t.Fatalf("status=%s", task.Status)
	}
}

func TestRunnerStartFailureReleasesCleanupOnceWithNilProcess(t *testing.T) {
	fixture := newTestRunner(t, "completed")
	cmd := exec.Command(filepath.Join(t.TempDir(), "missing-codex"))
	runner := NewRunner(cmd, fixture.repo, fixture.taskID, fixture.root, nil)
	var cleanups atomic.Int32
	runner.Cleanup = func() error {
		cleanups.Add(1)
		return nil
	}
	if err := runner.Run(context.Background()); err == nil {
		t.Fatal("expected start failure")
	}
	if cmd.Process != nil {
		t.Fatalf("process unexpectedly exists: %+v", cmd.Process)
	}
	if got := cleanups.Load(); got != 1 {
		t.Fatalf("cleanup calls=%d want=1", got)
	}
}

type runnerFixture struct {
	db        *sql.DB
	repo      *store.TaskRepository
	runner    *Runner
	taskID    string
	projectID string
	root      string
	lastPath  string
}

func newTestRunner(t *testing.T, mode string) *runnerFixture {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "task.db"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	accountID := uuid.NewString()
	projectID := uuid.NewString()
	taskID := uuid.NewString()
	if _, err = db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,?,?,?,?)`, accountID, "A", "#fff", "active", now, now); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO projects(id,account_id,title,stage,created_at,updated_at) VALUES(?,?,?,?,?,?)`, projectID, accountID, "P", domain.StageScript, now, now); err != nil {
		t.Fatal(err)
	}
	repo := store.NewTaskRepository(db)
	t.Cleanup(func() { _ = db.Close() })
	if err := repo.Create(context.Background(), domain.CodexTask{ID: taskID, ProjectID: &projectID, AccountID: accountID, Type: "remix", SkillName: "finance-viral-remix", Status: domain.TaskQueued, PromptSnapshot: "p", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE codex_tasks SET action=? WHERE id=?`, domain.ActionRemixStandard, taskID); err != nil {
		t.Fatal(err)
	}
	fake, err := filepath.Abs(filepath.Join("..", "..", "tests", "fakes", "fake-codex.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	lastPath := filepath.Join(root, "output-last-message.json")
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-File", fake, mode, "--output-last-message", lastPath, "--task-id", taskID, "--action", string(domain.ActionRemixStandard), "--output-dir", root)
	return &runnerFixture{db: db, repo: repo, runner: NewRunner(cmd, repo, taskID, root, nil), taskID: taskID, projectID: projectID, root: root, lastPath: lastPath}
}

func TestRunnerPersistsFakeCodexOutputAndCompletedStatus(t *testing.T) {
	fixture := newTestRunner(t, "completed")
	var broadcasts []Event
	var callbackCounts []int
	fixture.runner.Broadcast = func(e Event) {
		broadcasts = append(broadcasts, e)
		events, err := fixture.repo.Events(context.Background(), fixture.taskID)
		if err != nil {
			t.Errorf("read events in callback: %v", err)
			return
		}
		callbackCounts = append(callbackCounts, len(events))
	}
	if err := fixture.runner.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	task, err := fixture.repo.Get(context.Background(), fixture.taskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != domain.TaskCompleted || task.CodexSessionID == nil {
		t.Fatalf("task=%+v", task)
	}
	events, err := fixture.repo.Events(context.Background(), fixture.taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 || events[0].Kind != "thread_started" || events[1].Kind != "agent_message" || events[2].Kind != "turn_completed" || events[3].Kind != "result_completed" {
		t.Fatalf("events=%+v", events)
	}
	if len(broadcasts) != 3 {
		t.Fatalf("broadcasts=%d", len(broadcasts))
	}
	for i, count := range callbackCounts {
		if count != i+1 {
			t.Fatalf("broadcast %d observed %d persisted events", i, count)
		}
	}
	var snapshot string
	if err := fixture.db.QueryRow(`SELECT config_snapshot_json FROM codex_tasks WHERE id=?`, fixture.taskID).Scan(&snapshot); err != nil || !strings.Contains(snapshot, "output-last-message") {
		t.Fatalf("snapshot=%q err=%v", snapshot, err)
	}
}

func TestRunnerRedactsEveryPersistedCodexValue(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mode    string
		secrets []string
	}{
		{name: "completed result and snapshot", mode: "completed", secrets: []string{"agent result"}},
		{name: "stderr and ignored failed result", mode: "failed", secrets: []string{"technical failure", "must not commit"}},
		{name: "invalid raw result", mode: "invalid_schema", secrets: []string{"completed"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newTestRunner(t, tc.mode)
			redactor := security.NewRedactor()
			for _, secret := range tc.secrets {
				redactor.Register(secret)
			}
			redactor.Register(fixture.root)
			fixture.runner.Redactor = redactor
			_ = fixture.runner.Run(context.Background())

			rows, err := fixture.db.Query(`
				SELECT config_snapshot_json FROM codex_tasks WHERE id=?
				UNION ALL SELECT COALESCE(result_summary,'') FROM codex_tasks WHERE id=?
				UNION ALL SELECT COALESCE(error_message,'') FROM codex_tasks WHERE id=?
				UNION ALL SELECT display_text FROM task_events WHERE task_id=?
				UNION ALL SELECT raw_json FROM task_events WHERE task_id=?
				UNION ALL SELECT content FROM task_messages WHERE task_id=?
				UNION ALL SELECT COALESCE(question_schema,'') FROM task_messages WHERE task_id=?`,
				fixture.taskID, fixture.taskID, fixture.taskID, fixture.taskID, fixture.taskID, fixture.taskID, fixture.taskID)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			for rows.Next() {
				var persisted string
				if err := rows.Scan(&persisted); err != nil {
					t.Fatal(err)
				}
				for _, secret := range append(tc.secrets, fixture.root) {
					if strings.Contains(persisted, secret) {
						t.Fatalf("persisted secret %q in %q", secret, persisted)
					}
				}
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRunnerResolvesFinalResultInAgentThenLastMessageOrder(t *testing.T) {
	for _, tc := range []struct {
		mode        string
		wantSummary string
	}{
		{"completed", "agent result"},
		{"agent_preferred", "agent result"},
		{"agent_only", "agent result"},
		{"last_message_only", "last result"},
		{"invalid_agent", "last result"},
		{"latest_invalid", "last result"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			fixture := newTestRunner(t, tc.mode)
			if err := fixture.runner.Run(context.Background()); err != nil {
				t.Fatal(err)
			}
			task, err := fixture.repo.Get(context.Background(), fixture.taskID)
			if err != nil {
				t.Fatal(err)
			}
			if task.Status != domain.TaskCompleted || task.ResultSummary == nil || *task.ResultSummary != tc.wantSummary {
				t.Fatalf("task=%+v", task)
			}
		})
	}
}

func TestRunnerPersistsAwaitingInputQuestionInOneConversation(t *testing.T) {
	fixture := newTestRunner(t, "awaiting_input")
	if err := fixture.runner.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	task, err := fixture.repo.Get(context.Background(), fixture.taskID)
	if err != nil || task.Status != domain.TaskAwaitingInput {
		t.Fatalf("task=%+v err=%v", task, err)
	}
	messages, err := fixture.repo.Messages(context.Background(), fixture.taskID)
	if err != nil || len(messages) != 1 || messages[0].QuestionSchema == nil || !strings.Contains(*messages[0].QuestionSchema, "pick") {
		t.Fatalf("messages=%+v err=%v", messages, err)
	}
}

func TestRunnerInvalidResultPersistsRawEngineeringArtifactOnly(t *testing.T) {
	fixture := newTestRunner(t, "invalid_schema")
	if err := fixture.runner.Run(context.Background()); err == nil {
		t.Fatal("expected output validation failure")
	}
	task, err := fixture.repo.Get(context.Background(), fixture.taskID)
	if err != nil || task.Status != domain.TaskFailed || task.ErrorCode == nil || *task.ErrorCode != "output_invalid" {
		t.Fatalf("task=%+v err=%v", task, err)
	}
	artifacts, err := fixture.repo.Artifacts(context.Background(), fixture.taskID)
	if err != nil || len(artifacts) != 1 || artifacts[0].Kind != "raw_output_last_message" || artifacts[0].Path != fixture.lastPath {
		t.Fatalf("artifacts=%+v err=%v", artifacts, err)
	}
	var formalAssets int
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM asset_versions WHERE source_task_id=?`, fixture.taskID).Scan(&formalAssets); err != nil || formalAssets != 0 {
		t.Fatalf("formal assets=%d err=%v", formalAssets, err)
	}
}

func TestRunnerRejectsUnsafeOutputLastMessagePath(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, *runnerFixture) string
	}{
		{
			name: "outside output directory",
			setup: func(t *testing.T, _ *runnerFixture) string {
				path := filepath.Join(t.TempDir(), "outside.json")
				if err := os.WriteFile(path, []byte(`{"status":"completed"}`), 0o600); err != nil {
					t.Fatal(err)
				}
				return path
			},
		},
		{
			name: "relative path",
			setup: func(_ *testing.T, _ *runnerFixture) string {
				return "output-last-message.json"
			},
		},
		{
			name: "directory",
			setup: func(_ *testing.T, fixture *runnerFixture) string {
				return fixture.root
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newTestRunner(t, "completed")
			fixture.runner.OutputLastMessagePath = tt.setup(t, fixture)
			if err := fixture.runner.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "output_invalid") {
				t.Fatalf("error=%v", err)
			}
			assertTaskFailedWithCode(t, fixture, "output_invalid")
		})
	}
}

func TestRunnerRejectsSymlinkAndOversizedOutputLastMessage(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		fixture := newTestRunner(t, "completed")
		target := filepath.Join(t.TempDir(), "outside.json")
		if err := os.WriteFile(target, []byte(`{"status":"completed"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(fixture.root, "linked.json")
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		fixture.runner.OutputLastMessagePath = link
		if err := fixture.runner.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "output_invalid") {
			t.Fatalf("error=%v", err)
		}
		assertTaskFailedWithCode(t, fixture, "output_invalid")
	})

	t.Run("oversized", func(t *testing.T) {
		fixture := newTestRunner(t, "last_message_only")
		fixture.runner.maxOutputLastMessageBytes = 64
		if err := fixture.runner.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "output_invalid") {
			t.Fatalf("error=%v", err)
		}
		assertTaskFailedWithCode(t, fixture, "output_invalid")
	})
}

func assertTaskFailedWithCode(t *testing.T, fixture *runnerFixture, code string) {
	t.Helper()
	task, err := fixture.repo.Get(context.Background(), fixture.taskID)
	if err != nil || task.Status != domain.TaskFailed || task.ErrorCode == nil || *task.ErrorCode != code {
		t.Fatalf("task=%+v err=%v", task, err)
	}
}

func TestRunnerPersistsEngineeringArtifactsAndFormalAssetsSeparately(t *testing.T) {
	fixture := newTestRunner(t, "completed_with_outputs")
	if err := fixture.runner.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	artifacts, err := fixture.repo.Artifacts(context.Background(), fixture.taskID)
	if err != nil || len(artifacts) != 1 || artifacts[0].Kind != "qc_report" {
		t.Fatalf("artifacts=%+v err=%v", artifacts, err)
	}
	var formalAssets int
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM asset_versions WHERE source_task_id=?`, fixture.taskID).Scan(&formalAssets); err != nil || formalAssets != 1 {
		t.Fatalf("formal assets=%d err=%v", formalAssets, err)
	}
}

func TestRunnerRejectsFormalAssetWhoseDigestDoesNotMatchFile(t *testing.T) {
	fixture := newTestRunner(t, "completed_with_bad_asset_hash")
	err := fixture.runner.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "output_invalid") {
		t.Fatalf("error=%v", err)
	}
	assertTaskFailedWithCode(t, fixture, "output_invalid")
	var formalAssets int
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM asset_versions WHERE source_task_id=?`, fixture.taskID).Scan(&formalAssets); err != nil || formalAssets != 0 {
		t.Fatalf("formal assets=%d err=%v", formalAssets, err)
	}
}

func TestRunnerPersistsDiagnosticFailureWhenResultTransactionFails(t *testing.T) {
	fixture := newTestRunner(t, "completed_with_outputs")
	if _, err := fixture.db.Exec(`CREATE TRIGGER fail_task_artifact BEFORE INSERT ON task_artifacts BEGIN SELECT RAISE(FAIL, 'artifact write rejected'); END`); err != nil {
		t.Fatal(err)
	}
	err := fixture.runner.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "artifact write rejected") {
		t.Fatalf("error=%v", err)
	}
	assertTaskFailedWithCode(t, fixture, "result_persistence_failed")
	var formalAssets int
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM asset_versions WHERE source_task_id=?`, fixture.taskID).Scan(&formalAssets); err != nil || formalAssets != 0 {
		t.Fatalf("formal assets=%d err=%v", formalAssets, err)
	}
}

func TestRunnerFailedProcessNeverRegistersFormalResultAssets(t *testing.T) {
	fixture := newTestRunner(t, "failed")
	if err := fixture.runner.Run(context.Background()); err == nil {
		t.Fatal("expected process failure")
	}
	task, err := fixture.repo.Get(context.Background(), fixture.taskID)
	if err != nil || task.Status != domain.TaskFailed {
		t.Fatalf("task=%+v err=%v", task, err)
	}
	var formalAssets int
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM asset_versions WHERE source_task_id=?`, fixture.taskID).Scan(&formalAssets); err != nil || formalAssets != 0 {
		t.Fatalf("formal assets=%d err=%v", formalAssets, err)
	}
}
