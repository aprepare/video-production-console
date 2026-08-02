package codex

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"video-production-console/internal/domain"
	"video-production-console/internal/store"
)

func TestRunnerKillsBlockedChildOnOversizedJSONL(t *testing.T) {
	repo, _ := newTestRunner(t, "completed")
	cmd := exec.Command("powershell", "-NoProfile", "-Command", "$x='{' + ('x' * (16*1024*1024+1)) + '}'; [Console]::Out.Write($x); [Console]::Out.Flush(); Start-Sleep -Seconds 30")
	runner := NewRunner(cmd, repo, "t1", t.TempDir(), nil)
	done := make(chan error, 1)
	go func() { done <- runner.Run(context.Background()) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected oversized stream failure")
		}
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("runner hung after oversized JSONL")
	}
	task, err := repo.Get(context.Background(), "t1")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != domain.TaskFailed {
		t.Fatalf("status=%s", task.Status)
	}
}

func newTestRunner(t *testing.T, mode string) (*store.TaskRepository, *Runner) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "task.db"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err = db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('a','A','#fff','active',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	repo := store.NewTaskRepository(db)
	t.Cleanup(func() { _ = db.Close() })
	if err := repo.Create(context.Background(), domain.CodexTask{ID: "t1", AccountID: "a", Type: "topic_select", SkillName: "x", Status: domain.TaskQueued, PromptSnapshot: "p", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	fake, err := filepath.Abs(filepath.Join("..", "..", "tests", "fakes", "fake-codex.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	return repo, NewRunner(exec.Command("powershell", "-NoProfile", "-File", fake, mode), repo, "t1", t.TempDir(), nil)
}

func TestRunnerPersistsFakeCodexOutputAndCompletedStatus(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "task.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	if _, err = db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('a','A','#fff','active',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	repo := store.NewTaskRepository(db)
	if err := repo.Create(context.Background(), domain.CodexTask{ID: "t1", AccountID: "a", Type: "topic_select", SkillName: "x", Status: domain.TaskQueued, PromptSnapshot: "p", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	fake, err := filepath.Abs(filepath.Join("..", "..", "tests", "fakes", "fake-codex.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("powershell", "-NoProfile", "-File", fake, "completed")
	var broadcasts []Event
	var callbackCounts []int
	if err := NewRunner(cmd, repo, "t1", t.TempDir(), func(e Event) {
		broadcasts = append(broadcasts, e)
		events, err := repo.Events(context.Background(), "t1")
		if err != nil {
			t.Errorf("read events in callback: %v", err)
			return
		}
		callbackCounts = append(callbackCounts, len(events))
	}).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	task, err := repo.Get(context.Background(), "t1")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != domain.TaskCompleted || task.CodexSessionID == nil {
		t.Fatalf("task=%+v", task)
	}
	events, err := repo.Events(context.Background(), "t1")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[0].Kind != "thread_started" || events[1].Kind != "agent_message" || events[2].Kind != "turn_completed" {
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
}

func TestRunnerResultStatusesAndExitFailures(t *testing.T) {
	for _, tc := range []struct {
		mode    string
		status  domain.TaskStatus
		wantErr bool
	}{
		{"needs_input", domain.TaskWaitingInput, false}, {"failed", domain.TaskFailed, true}, {"failed_result", domain.TaskFailed, true},
		{"malformed", domain.TaskWaitingInput, false}, {"large", domain.TaskWaitingInput, false}, {"stderr", domain.TaskWaitingInput, false}, {"interleave", domain.TaskWaitingInput, false}, {"delay", domain.TaskWaitingInput, false},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			repo, runner := newTestRunner(t, tc.mode)
			if err := runner.Run(context.Background()); (err != nil) != tc.wantErr {
				t.Fatalf("err=%v", err)
			}
			task, err := repo.Get(context.Background(), "t1")
			if err != nil {
				t.Fatal(err)
			}
			if task.Status != tc.status {
				t.Fatalf("status=%s", task.Status)
			}
			events, err := repo.Events(context.Background(), "t1")
			if err != nil {
				t.Fatal(err)
			}
			if tc.mode == "stderr" && len(events) == 0 {
				t.Fatal("stderr event missing")
			}
			if tc.mode == "interleave" {
				for i, event := range events {
					if event.Sequence != int64(i+1) {
						t.Fatalf("event sequence=%d at index=%d", event.Sequence, i)
					}
				}
			}
		})
	}
}

func TestRunnerRejectsSymlinkArtifactEscape(t *testing.T) {
	_, runner := newTestRunner(t, "completed")
	root := runner.AssetRoot
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := runner.registerArtifacts(context.Background(), []Artifact{{Type: "x", Path: "link.txt"}}); err == nil {
		t.Fatal("expected symlink escape rejection")
	}
}
