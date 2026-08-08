package montage

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"video-production-console/internal/domain"
)

func TestFrozenDraftDisplayNameReadsReadableName(t *testing.T) {
	taskID := "984c42ec-67b8-4d3f-99e3-d3d7a4b66205"
	want := "财富觉醒02_存款大搬家_b66205"
	manifest := filepath.Join(t.TempDir(), "task_manifest.json")
	data, err := json.Marshal(map[string]any{
		"task_id": taskID,
		"job_id":  taskID,
		"non_secret_settings": map[string]any{
			"draft_display_name": want,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, data, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := frozenDraftDisplayName(manifest, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("display name=%q, want %q", got, want)
	}
}

func TestCoordinatorRunStopsWhenQueueIsClosed(t *testing.T) {
	queue := make(chan registrationJob)
	close(queue)
	coordinator := &Coordinator{queue: queue}
	done := make(chan struct{})
	coordinator.wg.Add(1)
	go func() { coordinator.run(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("coordinator did not stop after its queue closed")
	}
}

func TestCoordinatorCloseRejectsNewJobsAndDrainsAcceptedJobs(t *testing.T) {
	coordinator := &Coordinator{queue: make(chan registrationJob, 8)}
	var consumed atomic.Int32
	coordinator.wg.Add(1)
	go func() {
		defer coordinator.wg.Done()
		for range coordinator.queue {
			consumed.Add(1)
		}
	}()
	if err := coordinator.enqueue(context.Background(), domain.RegistrationAttempt{ID: "accepted"}); err != nil {
		t.Fatal(err)
	}
	coordinator.Close()
	if got := consumed.Load(); got != 1 {
		t.Fatalf("consumed=%d, want 1", got)
	}
	if err := coordinator.enqueue(context.Background(), domain.RegistrationAttempt{ID: "rejected"}); err == nil {
		t.Fatal("enqueue succeeded after Close")
	}
	if len(coordinator.queue) != 0 {
		t.Fatalf("closed coordinator retained %d jobs", len(coordinator.queue))
	}
}

func TestCoordinatorConcurrentCloseNeverStrandsAcceptedJob(t *testing.T) {
	for range 100 {
		coordinator := &Coordinator{queue: make(chan registrationJob, 1)}
		var consumed atomic.Int32
		coordinator.wg.Add(1)
		go func() {
			defer coordinator.wg.Done()
			for range coordinator.queue {
				consumed.Add(1)
			}
		}()
		result := make(chan error, 1)
		go func() { result <- coordinator.enqueue(context.Background(), domain.RegistrationAttempt{ID: "racing"}) }()
		coordinator.Close()
		err := <-result
		if err == nil && consumed.Load() != 1 {
			t.Fatal("accepted job was not drained")
		}
		if len(coordinator.queue) != 0 {
			t.Fatal("job remained buffered after Close")
		}
	}
}

func TestCoordinatorCloseCancelsWorkerContext(t *testing.T) {
	workerCtx, cancel := context.WithCancel(context.Background())
	coordinator := &Coordinator{queue: make(chan registrationJob), ctx: workerCtx, cancel: cancel}
	coordinator.wg.Add(1)
	go func() { defer coordinator.wg.Done(); <-workerCtx.Done() }()
	done := make(chan struct{})
	go func() { coordinator.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close did not cancel the worker context")
	}
}

func TestCanonicalNoFollowRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.json")
	if err := os.WriteFile(target, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := canonicalNoFollow(link, false); err == nil {
		t.Fatal("symlink was accepted as a retained path")
	}
}

func TestTrustedPythonBinaryRejectsArbitraryProgram(t *testing.T) {
	if _, err := trustedPythonBinary("powershell.exe"); err == nil {
		t.Fatal("arbitrary program was accepted as Python")
	}
}

func TestPlaintextWorkspaceDigestDetectsInPlaceTampering(t *testing.T) {
	workspace := t.TempDir()
	content := filepath.Join(workspace, "draft_content.json")
	if err := os.WriteFile(content, []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := hashPlaintextWorkspace(context.Background(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(content, []byte(`{"version":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	after, err := hashPlaintextWorkspace(context.Background(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("in-place workspace mutation did not change the durable digest")
	}
}
