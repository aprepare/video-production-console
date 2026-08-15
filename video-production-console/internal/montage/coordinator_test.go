package montage

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"video-production-console/internal/domain"
	"video-production-console/internal/store"
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

func TestFrozenDraftDisplayNameFallsBackOnlyWhenFieldIsMissing(t *testing.T) {
	taskID := "984c42ec-67b8-4d3f-99e3-d3d7a4b66205"
	writeManifest := func(t *testing.T, settings map[string]any) string {
		t.Helper()
		manifest := filepath.Join(t.TempDir(), "task_manifest.json")
		data, err := json.Marshal(map[string]any{
			"task_id":             taskID,
			"job_id":              taskID,
			"non_secret_settings": settings,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(manifest, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return manifest
	}

	got, err := frozenDraftDisplayName(writeManifest(t, map[string]any{}), taskID)
	if err != nil {
		t.Fatalf("missing legacy display field was rejected: %v", err)
	}
	if got != taskID {
		t.Fatalf("legacy display=%q, want task UUID", got)
	}
	if _, err := frozenDraftDisplayName(writeManifest(t, map[string]any{"draft_display_name": ""}), taskID); err == nil {
		t.Fatal("explicit empty display name was accepted")
	}
	if _, err := frozenDraftDisplayName(writeManifest(t, map[string]any{"draft_display_name": nil}), taskID); err == nil {
		t.Fatal("explicit null display name was accepted")
	}
	for _, invalid := range []string{"unsafe/name", "trailing.", strings.Repeat("长", 69)} {
		if _, err := frozenDraftDisplayName(writeManifest(t, map[string]any{"draft_display_name": invalid}), taskID); err == nil {
			t.Fatalf("explicit invalid display name %q was accepted", invalid)
		}
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

func TestCoordinatorQueuesBackfillDisplayOnRegistrationSerialQueue(t *testing.T) {
	coordinator := &Coordinator{queue: make(chan registrationJob, 1)}
	candidate := domain.DraftDisplayReconcileCandidate{AssetVersionID: "version", TaskID: "task", DisplayName: "readable"}
	if err := coordinator.enqueueReconciliation(context.Background(), candidate); err != nil {
		t.Fatal(err)
	}
	job := <-coordinator.queue
	if job.Reconcile == nil || job.Reconcile.AssetVersionID != candidate.AssetVersionID || job.Attempt.ID != "" {
		t.Fatalf("job=%#v", job)
	}
}

func TestWithTrustedReconciliationSkillUsesCurrentInstalledSnapshot(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "scripts", "run_montage_job.py")
	lockModule := filepath.Join(root, "scripts", "jianying_concurrency_lock.py")
	if err := os.MkdirAll(filepath.Dir(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("current installed reconciliation runtime"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockModule, []byte("trusted lock dependency"), 0o600); err != nil {
		t.Fatal(err)
	}
	hash, err := hashFile(script)
	if err != nil {
		t.Fatal(err)
	}
	lockHash, err := hashFile(lockModule)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := WithTrustedReconciliationSkill(TrustedRuntime{}, domain.SkillSnapshot{
		Name: "jianying-montage-draft", Path: root,
		Files: []domain.SkillFileSnapshot{{Path: "scripts/run_montage_job.py", SHA256: hash}, {Path: "scripts/jianying_concurrency_lock.py", SHA256: lockHash}},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolvedRoot, resolvedScript, err := trustedReconciliationSkill(runtime)
	if err != nil {
		t.Fatalf("resolve current installed reconciliation runtime: %v", err)
	}
	if !samePath(resolvedRoot, root) || !samePath(resolvedScript, script) {
		t.Fatalf("resolved root=%q script=%q", resolvedRoot, resolvedScript)
	}
}

func TestTrustedReconciliationSkillRejectsChangedCurrentScript(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "scripts", "run_montage_job.py")
	lockModule := filepath.Join(root, "scripts", "jianying_concurrency_lock.py")
	if err := os.MkdirAll(filepath.Dir(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("trusted"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockModule, []byte("trusted lock dependency"), 0o600); err != nil {
		t.Fatal(err)
	}
	hash, err := hashFile(script)
	if err != nil {
		t.Fatal(err)
	}
	lockHash, err := hashFile(lockModule)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := WithTrustedReconciliationSkill(TrustedRuntime{}, domain.SkillSnapshot{
		Name: "jianying-montage-draft", Path: root,
		Files: []domain.SkillFileSnapshot{{Path: "scripts/run_montage_job.py", SHA256: hash}, {Path: "scripts/jianying_concurrency_lock.py", SHA256: lockHash}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("changed after trusted resolution"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := trustedReconciliationSkill(runtime); err == nil {
		t.Fatal("changed current installed reconciliation script was accepted")
	}
}

func TestTrustedReconciliationSkillRejectsChangedLockDependency(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "scripts", "run_montage_job.py")
	lockModule := filepath.Join(root, "scripts", "jianying_concurrency_lock.py")
	if err := os.MkdirAll(filepath.Dir(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("import jianying_concurrency_lock"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockModule, []byte("trusted lock dependency"), 0o600); err != nil {
		t.Fatal(err)
	}
	scriptHash, _ := hashFile(script)
	lockHash, _ := hashFile(lockModule)
	runtime, err := WithTrustedReconciliationSkill(TrustedRuntime{}, domain.SkillSnapshot{
		Name: "jianying-montage-draft", Path: root,
		Files: []domain.SkillFileSnapshot{
			{Path: "scripts/run_montage_job.py", SHA256: scriptHash},
			{Path: "scripts/jianying_concurrency_lock.py", SHA256: lockHash},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockModule, []byte("changed after startup"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := trustedReconciliationSkill(runtime); err == nil {
		t.Fatal("changed current lock dependency was accepted")
	}
}

func TestTrustedReconciliationSkillRejectsNewUnboundPythonDependency(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "scripts", "run_montage_job.py")
	lockModule := filepath.Join(root, "scripts", "jianying_concurrency_lock.py")
	if err := os.MkdirAll(filepath.Dir(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("import jianying_concurrency_lock"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockModule, []byte("trusted lock dependency"), 0o600); err != nil {
		t.Fatal(err)
	}
	scriptHash, _ := hashFile(script)
	lockHash, _ := hashFile(lockModule)
	runtime, err := WithTrustedReconciliationSkill(TrustedRuntime{}, domain.SkillSnapshot{
		Name: "jianying-montage-draft", Path: root,
		Files: []domain.SkillFileSnapshot{
			{Path: "scripts/run_montage_job.py", SHA256: scriptHash},
			{Path: "scripts/jianying_concurrency_lock.py", SHA256: lockHash},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "scripts", "new_dependency.py"), []byte("new dependency"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := trustedReconciliationSkill(runtime); err == nil {
		t.Fatal("new unbound Python dependency was accepted")
	}
}

func TestResolveReconciliationRuntimeIgnoresChangedHistoricalTaskScript(t *testing.T) {
	base := t.TempDir()
	taskID := "984c42ec-67b8-4d3f-99e3-d3d7a4b66205"
	manifest := filepath.Join(base, "task_manifest.json")
	output := filepath.Join(base, "output")
	workspace := filepath.Join(output, "workspace", taskID)
	profile := filepath.Join(base, "machine-profile.json")
	python := filepath.Join(base, "python.exe")
	jianyingRoot := filepath.Join(base, "jianying")
	registered := filepath.Join(jianyingRoot, taskID)
	currentRoot := filepath.Join(base, "current-skill")
	currentScript := filepath.Join(currentRoot, "scripts", "run_montage_job.py")
	currentLockModule := filepath.Join(currentRoot, "scripts", "jianying_concurrency_lock.py")
	historicalRoot := filepath.Join(base, "historical-skill")
	historicalScript := filepath.Join(historicalRoot, "scripts", "run_montage_job.py")
	for _, directory := range []string{workspace, registered, filepath.Dir(currentScript), filepath.Dir(historicalScript)} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for path, content := range map[string]string{
		profile: "{}", python: "python", currentScript: "current trusted", currentLockModule: "trusted lock", historicalScript: "changed historical",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	manifestData, err := json.Marshal(map[string]any{
		"task_id": taskID, "job_id": taskID, "output_dir": output,
		"non_secret_settings": map[string]any{"draft_display_name": "readable", "machine_profile_path": profile},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, manifestData, 0o600); err != nil {
		t.Fatal(err)
	}
	currentHash, _ := hashFile(currentScript)
	currentLockHash, _ := hashFile(currentLockModule)
	profileHash, _ := hashFile(profile)
	runtime, err := WithTrustedReconciliationSkill(TrustedRuntime{
		MachineProfilePath: profile, MachineProfileSHA256: profileHash, PythonBinary: python, JianyingRoot: jianyingRoot,
	}, domain.SkillSnapshot{Name: "jianying-montage-draft", Path: currentRoot, Files: []domain.SkillFileSnapshot{{Path: "scripts/run_montage_job.py", SHA256: currentHash}, {Path: "scripts/jianying_concurrency_lock.py", SHA256: currentLockHash}}})
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(base, "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('account','a','#fff','active',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO skill_snapshots(id,name,path,sha256,files_json,modified_at,created_at) VALUES('historical','jianying-montage-draft',?,'snapshot','[{"path":"scripts/run_montage_job.py","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]',?,?)`, historicalRoot, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO codex_tasks(id,account_id,type,skill_name,action,status,completion_phase,transport,prompt_snapshot,skill_snapshot_id,manifest_path,created_at) VALUES(?,'account','montage','jianying-montage-draft',?,'completed','registered','legacy_exec','prompt','historical',?,?)`, taskID, domain.ActionMontageExecute, manifest, now); err != nil {
		t.Fatal(err)
	}
	coordinator := &Coordinator{tasks: store.NewTaskRepository(db), runtime: runtime}
	request, err := coordinator.resolveReconciliationRuntime(domain.DraftDisplayReconcileCandidate{
		TaskID: taskID, ManifestPath: manifest, WorkspacePath: workspace, RegisteredPath: registered,
		DisplayName: "readable", CurrentSHA256: strings.Repeat("b", 64),
	})
	if err != nil {
		t.Fatalf("current trusted reconciliation was blocked by historical script mismatch: %v", err)
	}
	if !samePath(request.ScriptPath, currentScript) {
		t.Fatalf("script=%q, want current installed %q", request.ScriptPath, currentScript)
	}
}

func TestResolveRuntimeMovieSkillUsesTrustedDraftScripts(t *testing.T) {
	taskID := "984c42ec-67b8-4d3f-99e3-d3d7a4b66205"
	base := t.TempDir()
	manifest := filepath.Join(base, "task_manifest.json")
	output := filepath.Join(base, "output")
	workspace := filepath.Join(output, "workspace", taskID)
	profile := filepath.Join(base, "machine-profile.json")
	python := filepath.Join(base, "python.exe")
	jianyingRoot := filepath.Join(base, "jianying")
	currentRoot := filepath.Join(base, "current-skill")
	currentScript := filepath.Join(currentRoot, "scripts", "run_montage_job.py")
	currentLockModule := filepath.Join(currentRoot, "scripts", "jianying_concurrency_lock.py")
	movieRoot := filepath.Join(base, "movie-skill")
	for _, directory := range []string{workspace, jianyingRoot, filepath.Dir(currentScript), movieRoot} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for path, content := range map[string]string{
		profile: "{}", python: "python", currentScript: "current trusted", currentLockModule: "trusted lock",
		filepath.Join(movieRoot, "SKILL.md"): "movie skill",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	manifestData, err := json.Marshal(map[string]any{
		"task_id": taskID, "job_id": taskID, "output_dir": output,
		"non_secret_settings": map[string]any{"draft_display_name": "readable", "machine_profile_path": profile},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, manifestData, 0o600); err != nil {
		t.Fatal(err)
	}
	currentHash, _ := hashFile(currentScript)
	currentLockHash, _ := hashFile(currentLockModule)
	profileHash, _ := hashFile(profile)
	runtime, err := WithTrustedReconciliationSkill(TrustedRuntime{
		MachineProfilePath: profile, MachineProfileSHA256: profileHash, PythonBinary: python, JianyingRoot: jianyingRoot,
	}, domain.SkillSnapshot{Name: "jianying-montage-draft", Path: currentRoot, Files: []domain.SkillFileSnapshot{{Path: "scripts/run_montage_job.py", SHA256: currentHash}, {Path: "scripts/jianying_concurrency_lock.py", SHA256: currentLockHash}}})
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(base, "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('account','a','#fff','active',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO skill_snapshots(id,name,path,sha256,files_json,modified_at,created_at) VALUES('movie','jianying-movie-montage',?,'snapshot','[]',?,?)`, movieRoot, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO codex_tasks(id,account_id,type,skill_name,action,status,completion_phase,transport,prompt_snapshot,skill_snapshot_id,manifest_path,created_at) VALUES(?,'account','movie_montage','jianying-movie-montage',?,'completed','plaintext_ready','legacy_exec','prompt','movie',?,?)`, taskID, domain.ActionMontageExecute, manifest, now); err != nil {
		t.Fatal(err)
	}
	coordinator := &Coordinator{tasks: store.NewTaskRepository(db), runtime: runtime}
	request, err := coordinator.resolveRuntime(domain.RegistrationAttempt{
		TaskID: taskID, ManifestPath: manifest, WorkspacePath: workspace,
	})
	if err != nil {
		t.Fatalf("movie montage registration should reuse landscape scripts: %v", err)
	}
	if !samePath(request.ScriptPath, currentScript) {
		t.Fatalf("script=%q, want current landscape %q", request.ScriptPath, currentScript)
	}
	if !samePath(request.SkillRoot, currentRoot) {
		t.Fatalf("skill root=%q, want current landscape %q", request.SkillRoot, currentRoot)
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

func TestCoordinatorRequeuesBusyReconciliationOnSameSerialQueue(t *testing.T) {
	workerCtx, cancel := context.WithCancel(context.Background())
	tick := make(chan time.Time, 1)
	called := make(chan int, 2)
	var calls atomic.Int32
	coordinator := &Coordinator{
		queue: make(chan registrationJob, 2), ctx: workerCtx, cancel: cancel,
		retryAfter: func(time.Duration) <-chan time.Time { return tick },
		reconcileJob: func(context.Context, domain.DraftDisplayReconcileCandidate) error {
			call := int(calls.Add(1))
			called <- call
			if call == 1 {
				return &ReconcileBusyError{RetryAfter: 30 * time.Second, OwnerJobID: "other"}
			}
			return nil
		},
	}
	coordinator.wg.Add(1)
	go coordinator.run()
	if err := coordinator.enqueueReconciliation(context.Background(), domain.DraftDisplayReconcileCandidate{TaskID: "task"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("first reconciliation did not run")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("calls=%d before retry clock advanced", got)
	}
	tick <- time.Now()
	select {
	case call := <-called:
		if call != 2 {
			t.Fatalf("retry call=%d", call)
		}
	case <-time.After(time.Second):
		t.Fatal("busy reconciliation was not requeued")
	}
	coordinator.Close()
}

func TestCoordinatorCloseCancelsPendingReconciliationRetry(t *testing.T) {
	workerCtx, cancel := context.WithCancel(context.Background())
	tick := make(chan time.Time, 1)
	first := make(chan struct{}, 1)
	var calls atomic.Int32
	coordinator := &Coordinator{
		queue: make(chan registrationJob, 2), ctx: workerCtx, cancel: cancel,
		retryAfter: func(time.Duration) <-chan time.Time { return tick },
		reconcileJob: func(context.Context, domain.DraftDisplayReconcileCandidate) error {
			calls.Add(1)
			first <- struct{}{}
			return &ReconcileBusyError{RetryAfter: 30 * time.Second}
		},
	}
	coordinator.wg.Add(1)
	go coordinator.run()
	if err := coordinator.enqueueReconciliation(context.Background(), domain.DraftDisplayReconcileCandidate{TaskID: "task"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-first:
	case <-time.After(time.Second):
		t.Fatal("first reconciliation did not run")
	}
	coordinator.Close()
	tick <- time.Now()
	time.Sleep(20 * time.Millisecond)
	if got := calls.Load(); got != 1 {
		t.Fatalf("calls=%d after close, want 1", got)
	}
}

func TestCoordinatorBoundsBusyReconciliationRetries(t *testing.T) {
	workerCtx, cancel := context.WithCancel(context.Background())
	tick := make(chan time.Time, maxReconcileRetries)
	called := make(chan int, maxReconcileRetries+2)
	var calls atomic.Int32
	coordinator := &Coordinator{
		queue: make(chan registrationJob, 2), ctx: workerCtx, cancel: cancel,
		retryAfter: func(time.Duration) <-chan time.Time { return tick },
		reconcileJob: func(context.Context, domain.DraftDisplayReconcileCandidate) error {
			call := int(calls.Add(1))
			called <- call
			return &ReconcileBusyError{RetryAfter: time.Second}
		},
	}
	coordinator.wg.Add(1)
	go coordinator.run()
	if err := coordinator.enqueueReconciliation(context.Background(), domain.DraftDisplayReconcileCandidate{TaskID: "task"}); err != nil {
		t.Fatal(err)
	}
	for expected := 1; expected <= maxReconcileRetries+1; expected++ {
		select {
		case got := <-called:
			if got != expected {
				t.Fatalf("call=%d, want %d", got, expected)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for call %d", expected)
		}
		if expected <= maxReconcileRetries {
			tick <- time.Now()
		}
	}
	tick <- time.Now()
	select {
	case got := <-called:
		t.Fatalf("unexpected retry call %d beyond bound", got)
	case <-time.After(30 * time.Millisecond):
	}
	coordinator.Close()
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
