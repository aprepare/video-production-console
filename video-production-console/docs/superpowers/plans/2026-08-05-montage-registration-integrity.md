# Montage Registration Integrity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `montage.execute` truthful: Codex produces a validated plaintext workspace, the trusted Go host registers it into Jianying, and the task completes only after the registered directory and receipt pass independent verification.

**Architecture:** Split montage delivery into an untrusted build phase and a trusted host-registration phase. A single host coordinator persists registration attempts, invokes the existing Skill registrar outside the Codex sandbox, verifies the registered target against the manifest, machine profile, source content fingerprint, and Jianying root index, then atomically records the formal `mix_draft` asset and task completion.

**Tech Stack:** Go 1.25, SQLite, Python 3, existing `jianying-montage-draft` scripts, React 19, TypeScript, Vitest.

---

## File map

- `internal/domain/montage.go`: registration states, completion phases, attempts, requests, and receipts.
- `internal/store/montage.go`: registration-attempt persistence, completion/failure transactions, recovery, and historical integrity queries.
- `internal/montage/registrar.go`: bounded host subprocess invocation of the Skill `register` phase.
- `internal/montage/validator.go`: manifest/profile/receipt/path/index/fingerprint validation.
- `internal/montage/coordinator.go`: one-at-a-time host registration queue and retry/recovery lifecycle.
- `internal/montage/audit.go`: read-only filesystem audit followed by transactional stale/failure marking.
- `internal/taskcompletion/gate.go`: transport-neutral completion input and gate interface without package cycles.
- `internal/httpapi/montage.go`: retry-registration and controlled open-directory actions.
- `web/src/features/tasks/MontageResultCard.tsx`: plaintext, QC, registration, retry, and formal-draft presentation.
- `C:/Users/prepare/.codex/skills/jianying-montage-draft/*`: console-mode ownership contract and host-readable receipt.

### Task 1: Separate the Codex plaintext result from the formal draft contract

**Files:**
- Modify: `internal/codex/manifest.go`
- Modify: `internal/codex/manifest_test.go`
- Modify: `internal/codex/result_validator.go`
- Modify: `internal/codex/result_validator_test.go`
- Modify: `schemas/task-manifest.schema.json`
- Modify: `schemas/codex-result.schema.json`
- Modify: `schemas/schema_contract_test.go`
- Modify: `C:/Users/prepare/.codex/skills/jianying-montage-draft/scripts/run_montage_job.py`
- Modify: `C:/Users/prepare/.codex/skills/jianying-montage-draft/tests/test_run_montage_job.py`
- Modify: `C:/Users/prepare/.codex/skills/jianying-montage-draft/SKILL.md`
- Modify: `C:/Users/prepare/.codex/skills/jianying-montage-draft/references/console-contract.md`
- Modify: `C:/Users/prepare/.codex/skills/jianying-montage-draft/references/planning-execution-contract.md`
- Modify: `C:/Users/prepare/.codex/skills/jianying-montage-draft/references/new-user-workflow.md`

- [ ] **Step 1: Write failing contract tests**

Add Go tests proving that a completed Codex result for `montage.execute` must contain `production_plan` and `plaintext_workspace` artifacts, must contain no formal asset output, and cannot satisfy the task with `mix_draft` pointing at the workspace.

```go
func TestMontageExecuteRequiresPlaintextWorkspaceButNoFormalAsset(t *testing.T) {
	manifest := validManifest(domain.ActionMontageExecute)
	if got := outputTypes(manifest.ExpectedOutputs); !reflect.DeepEqual(got, []string{"production_plan", "plaintext_workspace"}) {
		t.Fatalf("outputs=%v", got)
	}
	result := validResult(domain.ActionMontageExecute)
	result.Artifacts = append(result.Artifacts, ArtifactOutput{Type: "plaintext_workspace", Path: workspace})
	result.AssetOutputs = []AssetOutput{{Type: domain.AssetMixDraft, Path: workspace, StorageKind: domain.StorageDirectory}}
	if err := ValidateResultEnvelope(result, manifest, outputDir); err == nil || !strings.Contains(err.Error(), "host registration") {
		t.Fatalf("error=%v", err)
	}
}
```

Extend the Python test so `execute_job` returns `asset_outputs == []`, publishes one `plaintext_workspace` directory artifact, and never calls the registration runner.

- [ ] **Step 2: Run the focused tests**

```powershell
go test ./internal/codex ./schemas -run 'MontageExecute|MontageResult' -count=1
python -m unittest C:/Users/prepare/.codex/skills/jianying-montage-draft/tests/test_run_montage_job.py
```

Expected: the new assertions fail because the current executor still returns a workspace-backed `mix_draft` asset.

- [ ] **Step 3: Implement the two-phase contract**

Change the manifest allowlist and required outputs to:

```go
domain.ActionMontageExecute: {"production_plan": true, "plaintext_workspace": true}
```

For the Codex result validator, set `allowedAssetTypes[domain.ActionMontageExecute]` to an empty map and require the `plaintext_workspace` artifact to resolve to exactly `output_dir/workspace/task_id`. Keep `mix_draft` as a domain asset type; only the host coordinator may create it.

In `run_montage_job.py`, change the successful `execute` envelope to:

```python
result = envelope(
    context,
    status="completed",
    summary="Plaintext montage draft completed and validated; host registration is pending.",
    artifacts=artifacts + [{
        "type": "plaintext_workspace",
        "path": str(context.workspace),
        "kind": "directory",
    }],
    asset_outputs=[],
    warnings=warnings,
)
```

Update the Skill documents consistently: in console mode Codex runs through `execute`, returns the validated plaintext workspace, and stops. The console host alone invokes `run_montage_job.py register`; standalone mode without a manifest retains direct registration. Do not weaken the existing registration lock, fresh validation, or no-UI-test rules.

- [ ] **Step 4: Verify and commit repository-owned files**

```powershell
go test ./internal/codex ./schemas -count=1
python -m unittest C:/Users/prepare/.codex/skills/jianying-montage-draft/tests/test_run_montage_job.py
git add internal/codex/manifest.go internal/codex/manifest_test.go internal/codex/result_validator.go internal/codex/result_validator_test.go schemas/task-manifest.schema.json schemas/codex-result.schema.json schemas/schema_contract_test.go
git diff --cached --name-only
git commit -m "fix: separate montage build from registration"
```

The Skill directory is outside this repository. Verify its changed files separately and do not try to stage them in the console repository.

### Task 2: Persist registration attempts and completion phases

**Files:**
- Create: `internal/domain/montage.go`
- Create: `internal/store/montage.go`
- Create: `internal/store/montage_test.go`
- Modify: `internal/domain/models.go`
- Modify: `internal/store/migrations.go`
- Modify: `internal/store/migrations_test.go`
- Modify: `internal/store/tasks.go`
- Modify: `internal/store/tasks_test.go`

- [ ] **Step 1: Write transactional state tests**

Test preparation, success, failure, retry, and duplicate completion. A task must remain non-terminal while registration is queued or running; no current `mix_draft` may exist before success.

```go
func TestCompleteRegistrationCreatesAssetAndCompletesTaskAtomically(t *testing.T) {
	repo := NewMontageRepository(db)
	attempt, err := repo.Begin(ctx, BeginRegistration{TaskID: taskID, ManifestPath: manifest, WorkspacePath: workspace})
	if err != nil { t.Fatal(err) }
	err = repo.Succeed(ctx, RegistrationSuccess{
		AttemptID: attempt.ID, RegisteredPath: registered, ReceiptPath: receipt,
		SHA256: strings.Repeat("a", 64), Filename: filepath.Base(registered),
	})
	if err != nil { t.Fatal(err) }
	assertTaskPhase(t, db, taskID, domain.TaskCompleted, domain.CompletionRegistered)
	assertCurrentAsset(t, db, projectID, domain.AssetMixDraft, registered, domain.AssetReady)
}
```

Also inject a failure after asset insertion and assert the transaction rolls back both the asset and task status.

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/store ./internal/domain -run 'Registration|CompletionPhase' -count=1
```

- [ ] **Step 3: Add schema, types, and repository methods**

Use these exact public states:

```go
type TaskCompletionPhase string
const (
	CompletionAgentRunning TaskCompletionPhase = "agent_running"
	CompletionPlaintextReady TaskCompletionPhase = "plaintext_ready"
	CompletionRegistering TaskCompletionPhase = "registering"
	CompletionRegistered TaskCompletionPhase = "registered"
)
type RegistrationState string
const (
	RegistrationQueued RegistrationState = "queued"
	RegistrationRunning RegistrationState = "running"
	RegistrationSucceeded RegistrationState = "succeeded"
	RegistrationFailed RegistrationState = "failed"
	RegistrationInterrupted RegistrationState = "interrupted"
)
type RegistrationAttempt struct {
	ID, TaskID, ManifestPath, WorkspacePath string
	State RegistrationState
	Attempt int
	RegisteredPath, ReceiptPath, ErrorCode, ErrorMessage *string
	StartedAt time.Time
	FinishedAt *time.Time
}
```

Add `completion_phase TEXT NOT NULL DEFAULT 'agent_running'` to `codex_tasks` if Plan 1 has not already added it. Add `montage_registration_attempts` with unique `(task_id, attempt)`, an index on `(state, started_at)`, and foreign-key cascade from tasks.

`Begin` stores the engineering artifacts and changes the task to `status=running, completion_phase=plaintext_ready`, then inserts a queued attempt. `MarkRunning` changes the phase to `registering`. `Succeed` uses one immediate transaction to insert the registered directory as the ready formal `mix_draft`, mark the attempt succeeded, mark the task completed with `completion_phase=registered`, and recompute the project stage. `Fail` records `registration_failed`, preserves the workspace artifacts, and never creates a formal asset.

- [ ] **Step 4: Verify and commit**

```powershell
go test ./internal/store ./internal/domain -count=1
git add internal/domain/montage.go internal/domain/models.go internal/store/montage.go internal/store/montage_test.go internal/store/migrations.go internal/store/migrations_test.go internal/store/tasks.go internal/store/tasks_test.go
git diff --cached --name-only
git commit -m "feat: persist montage registration attempts"
```

### Task 3: Invoke and verify the Skill registrar from the trusted host

**Files:**
- Create: `internal/montage/registrar.go`
- Create: `internal/montage/registrar_test.go`
- Create: `internal/montage/validator.go`
- Create: `internal/montage/validator_test.go`
- Modify: `C:/Users/prepare/.codex/skills/jianying-montage-draft/scripts/run_montage_job.py`
- Modify: `C:/Users/prepare/.codex/skills/jianying-montage-draft/tests/test_run_montage_job.py`

- [ ] **Step 1: Write host-boundary tests**

Use a fake command runner and temporary manifest, profile, workspace, Jianying root, and root index. Cover: valid success, non-zero exit with structured failure JSON, missing `registered_path`, target outside `jianying_root`, absent draft files, mismatched content hashes, mismatched draft ID, and an index without the registered entry.

```go
func TestRegistrarRejectsWorkspaceAsRegisteredPath(t *testing.T) {
	runner := &fakeCommandRunner{stdout: registrationJSON(workspace, sourceSHA, sourceSHA)}
	_, err := NewRegistrar(runner).Register(ctx, validRequest(t))
	if err == nil || !errors.Is(err, ErrInvalidRegistration) { t.Fatalf("error=%v", err) }
}
```

Assert the generated command is exactly Python plus the snapshotted Skill script and `register --manifest ... --draft ...`; it must not contain `codex`, sandbox flags, Jianying launch commands, or browser/UI automation.

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/montage -run 'Registrar|RegisteredDraft' -count=1
```

- [ ] **Step 3: Implement bounded invocation and independent validation**

Use these interfaces:

```go
type CommandSpec struct { Program string; Args []string; Dir string }
type CommandResult struct { Stdout, Stderr []byte; ExitCode int }
type CommandRunner interface { Run(context.Context, CommandSpec, int64) (CommandResult, error) }
type RegisterRequest struct {
	TaskID, ManifestPath, WorkspacePath, SkillRoot, ScriptSHA256, PythonBinary string
}
type RegisterResult struct {
	RegisteredPath, ReceiptPath, DraftID, SourceContentSHA256, RegisteredContentSHA256, DirectorySHA256 string
	DurationUS int64
}
```

Resolve `scripts/run_montage_job.py` below the stored Skill snapshot root and verify its SHA-256 against the snapshot file list before execution. Capture at most 1 MiB each of stdout and stderr, always parse stdout even when the exit code is non-zero, and redact stderr before persistence.

The validator must re-read the authoritative manifest and machine profile, require `task_id == job_id`, require `workspace == output_dir/workspace/task_id`, and require `registered_path == canonical(jianying_root/task_id)`. Verify `draft_content.json`, `draft_meta_info.json`, `registration/registration-result.json`, the matching `root_meta_info.json` entry, source/registered content SHA-256 equality, and matching `draft_id`. Hash the verified registered directory for the formal asset record.

Extend the Python registration receipt with:

```python
"source_content_sha256": sha256_file(content_source),
"registered_content_sha256": sha256_file(target / "draft_content.json"),
"draft_id": draft_id,
```

The host rejects a completed envelope unless both hashes are equal and the receipt file reports the same values.

- [ ] **Step 4: Verify and commit repository-owned files**

```powershell
go test ./internal/montage -count=1
python -m unittest C:/Users/prepare/.codex/skills/jianying-montage-draft/tests/test_run_montage_job.py
git add internal/montage/registrar.go internal/montage/registrar_test.go internal/montage/validator.go internal/montage/validator_test.go
git diff --cached --name-only
git commit -m "feat: register Jianying drafts from the console host"
```

### Task 4: Gate task completion through a single registration coordinator

**Files:**
- Create: `internal/taskcompletion/gate.go`
- Create: `internal/montage/coordinator.go`
- Create: `internal/montage/coordinator_test.go`
- Modify: `internal/codex/runner.go`
- Modify: `internal/codex/runner_test.go`
- Modify: `internal/conversation/broker.go`
- Modify: `internal/conversation/broker_test.go`
- Modify: `internal/app/app.go`
- Modify: `internal/app/app_test.go`
- Modify: `cmd/console/main.go`
- Modify: `cmd/console/main_test.go`

- [ ] **Step 1: Write completion-gate and serial-queue tests**

Test both legacy Runner and App Server task-result paths. A successful plaintext result must call the coordinator, leave the task non-terminal, and avoid `CompleteWithResult`. Two montage registrations may build concurrently but their registrar calls may never overlap.

```go
func TestCoordinatorSerializesRegistration(t *testing.T) {
	registrar := newBlockingRegistrar()
	c := NewCoordinator(repo, registrar, skillRepo)
	c.Enqueue(ctx, requestA)
	c.Enqueue(ctx, requestB)
	registrar.WaitForFirst(t)
	if registrar.Concurrent() != 1 { t.Fatalf("concurrent=%d", registrar.Concurrent()) }
	registrar.ReleaseFirst()
	registrar.WaitForSecond(t)
}
```

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/montage ./internal/codex ./internal/conversation ./internal/app ./cmd/console -run 'Coordinator|MontageCompletionGate' -count=1
```

- [ ] **Step 3: Implement the shared gate**

Expose one transport-neutral interface from `internal/taskcompletion` so neither `internal/codex` nor `internal/conversation` imports `internal/montage`:

```go
type CompletionGate interface {
	HandleCompleted(context.Context, CompletedInput) (handled bool, err error)
}
type CompletedInput struct {
	Task domain.CodexTask
	ManifestPath string
	Action domain.TaskAction
	Summary, RawJSON string
	Artifacts []store.TaskArtifact
}
```

For every action except `montage.execute`, return `handled=false`. For montage, find exactly one `plaintext_workspace` artifact, persist it with `Begin`, return `handled=true`, and enqueue the attempt. The single coordinator worker marks the attempt running, invokes Registrar, then calls `Succeed` or `Fail`.

Legacy Runner and the App Server task result handler must call the same gate before their normal completed-result transaction. `Runner.Run` may finish after the gate accepts the job, but the database task remains running/registering until the host worker finishes. Coordinator shutdown waits only for its owned registration worker; it never enumerates or terminates unrelated Codex, Node, proxy, desktop, Jianying, or Tray processes.

- [ ] **Step 4: Verify and commit**

```powershell
go test ./internal/montage ./internal/codex ./internal/conversation ./internal/app ./cmd/console -count=1
git add internal/taskcompletion/gate.go internal/montage/coordinator.go internal/montage/coordinator_test.go internal/codex/runner.go internal/codex/runner_test.go internal/conversation/broker.go internal/conversation/broker_test.go internal/app/app.go internal/app/app_test.go cmd/console/main.go cmd/console/main_test.go
git diff --cached --name-only
git commit -m "feat: gate montage completion on host registration"
```

### Task 5: Recover interrupted attempts, audit false completions, and retry registration only

**Files:**
- Create: `internal/montage/audit.go`
- Create: `internal/montage/audit_test.go`
- Create: `internal/httpapi/montage.go`
- Create: `internal/httpapi/montage_test.go`
- Modify: `internal/store/montage.go`
- Modify: `internal/store/montage_test.go`
- Modify: `internal/app/app.go`
- Modify: `internal/app/app_test.go`

- [ ] **Step 1: Write recovery, audit, and API tests**

Cover these exact cases:

- an interrupted attempt with a valid receipt and target is reconciled to success;
- an interrupted attempt without a valid receipt becomes failed and remains retryable;
- a historical completed montage with `registered_path` absent, missing, outside `jianying_root`, or missing receipt is marked failed/stale;
- source files, engineering artifacts, and existing Jianying directories are never deleted or overwritten;
- retry enqueues only registration and does not invoke Codex, rebuild the plan, or rewrite the plaintext workspace;
- a second retry while registration is active returns HTTP 409.

```go
request := httptest.NewRequest(http.MethodPost, "/api/tasks/"+taskID+"/retry-registration", nil)
response := httptest.NewRecorder()
handler.ServeHTTP(response, request)
if response.Code != http.StatusAccepted { t.Fatalf("status=%d body=%s", response.Code, response.Body.String()) }
if codexCalls != 0 { t.Fatalf("retry invoked Codex %d times", codexCalls) }
```

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/montage ./internal/httpapi ./internal/store -run 'Recovery|IntegrityAudit|RetryRegistration' -count=1
```

- [ ] **Step 3: Implement idempotent recovery and audit**

On startup, inspect attempts left in queued/running state. If the receipt and registered target fully validate, finalize them; otherwise mark them `interrupted` and the task failed with `registration_interrupted`. Never automatically rerun an uncertain shared mutation.

Audit completed historical `montage.execute` tasks read-only first. For invalid records, one transaction must:

```text
mark current mix_draft version stale
set stale_reason = historical montage completion missing verified registration
set task status = failed
set error_code = registration_integrity_failed
set completion_phase = plaintext_ready when a workspace artifact exists
recompute the project stage
insert an audit registration-attempt record
```

Mount `POST /api/tasks/{id}/retry-registration`. Resolve manifest and workspace only from stored attempt/artifact records, revalidate their trusted roots, create the next attempt number transactionally, and enqueue the host coordinator. Return the new attempt summary; never accept paths from the browser.

- [ ] **Step 4: Verify and commit**

```powershell
go test ./internal/montage ./internal/httpapi ./internal/store ./internal/app -count=1
git add internal/montage/audit.go internal/montage/audit_test.go internal/httpapi/montage.go internal/httpapi/montage_test.go internal/store/montage.go internal/store/montage_test.go internal/app/app.go internal/app/app_test.go
git diff --cached --name-only
git commit -m "fix: audit and recover montage registration"
```

### Task 6: Show truthful montage results and safe registered-directory actions

**Files:**
- Modify: `internal/assets/service.go`
- Modify: `internal/assets/service_test.go`
- Modify: `internal/httpapi/assets.go`
- Modify: `internal/httpapi/assets_test.go`
- Modify: `internal/httpapi/task_results.go`
- Modify: `internal/httpapi/task_results_test.go`
- Create: `web/src/features/tasks/MontageResultCard.tsx`
- Create: `web/src/features/tasks/MontageResultCard.test.tsx`
- Modify: `web/src/pages/TaskPage.tsx`
- Modify: `web/src/types/api.ts`
- Modify: `web/src/styles/shell.css`
- Modify: `web/src/styles/mobile.css`

- [ ] **Step 1: Write API and UI tests**

Test four visible registration states: not started, registering, succeeded, and failed with retained plaintext. Test that the result view includes production plan, selected-media summary, QC, registration attempts, registered asset, and actionable error without raw JSON.

For directory access, test that `mix_draft` is allowed only below the configured canonical `jianying_root`; other external directories, symlinks/reparse points, and browser-supplied paths are rejected.

```tsx
test("failed registration keeps the draft and offers registration-only retry", async () => {
  render(<MontageResultCard result={failedRegistrationResult} />);
  expect(screen.getByText("草稿已生成，注册剪映失败")).toBeTruthy();
  expect(screen.getByText("本地明文草稿已保留")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "只重试注册" }));
  expect(retryRegistration).toHaveBeenCalledTimes(1);
});
```

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/assets ./internal/httpapi -run 'MontageResult|RegisteredDirectory' -count=1
Set-Location web
npx vitest run src/features/tasks/MontageResultCard.test.tsx
Set-Location ..
```

- [ ] **Step 3: Implement the result card and root policy**

Extend the task result response with:

```go
type MontageResultView struct {
	Phase domain.TaskCompletionPhase `json:"phase"`
	Workspace *ArtifactView `json:"workspace,omitempty"`
	Plan, MediaSummary, QC *ArtifactView
	RegistrationAttempts []RegistrationAttemptView `json:"registration_attempts"`
	RegisteredAsset *AssetView `json:"registered_asset,omitempty"`
	CanRetryRegistration bool `json:"can_retry_registration"`
}
```

The directory-manifest root policy must allow a registered `mix_draft` only beneath `jianying_root`; it must not require that formal drafts live under `data_root`. Add `POST /api/assets/{id}/open-directory`, which resolves the path from the asset ID and uses an injectable desktop opener. Hide this action on non-local/mobile clients; mobile shows the canonical path and safe relative manifest instead. The browser never submits a filesystem path.

Render a dedicated montage card with plan preview, used-media count, duration/shot/audio/transition QC, registration state, retained-workspace notice, retry button, registered path, and directory manifest. Technical stdout/stderr remain in the folded diagnostics panel.

- [ ] **Step 4: Verify and commit**

```powershell
go test ./internal/assets ./internal/httpapi -count=1
Set-Location web
npx vitest run src/features/tasks/MontageResultCard.test.tsx src/features/tasks/TaskPage.test.tsx
npm run build
Set-Location ..
git add internal/assets/service.go internal/assets/service_test.go internal/httpapi/assets.go internal/httpapi/assets_test.go internal/httpapi/task_results.go internal/httpapi/task_results_test.go web/src/features/tasks/MontageResultCard.tsx web/src/features/tasks/MontageResultCard.test.tsx web/src/pages/TaskPage.tsx web/src/types/api.ts web/src/styles/shell.css web/src/styles/mobile.css internal/webui/dist
git diff --cached --name-only
git commit -m "feat: show verified montage registration results"
```

### Task 7: Document operations and run the non-invasive release gates

**Files:**
- Create: `docs/operations/montage-registration.md`
- Modify: `README.md`

- [ ] **Step 1: Document support actions**

Document the exact phases, where the plaintext workspace and registration receipt live, how `只重试注册` behaves, how to interpret `registration_failed` and `registration_integrity_failed`, and that the automated suite never launches Jianying or WeChat Channels.

- [ ] **Step 2: Run all repository and Skill tests**

```powershell
go test ./...
go vet ./...
Set-Location web
npx vitest run
npm run build
npm run lint
Set-Location ..
python -m unittest discover -s C:/Users/prepare/.codex/skills/jianying-montage-draft/tests -p "test_*.py"
```

Expected: all commands pass using temporary fake Jianying roots and injected process stoppers. No test starts Jianying, JianyingProTray, WeChat Channels, a browser automation session, or a real Codex task.

- [ ] **Step 3: Confirm the truthful completion gate**

Run the focused tests once more:

```powershell
go test ./internal/montage ./internal/store ./internal/codex ./internal/httpapi -run 'Montage|Registration|Integrity' -count=1
git diff --check
```

Expected invariants:

```text
plaintext only -> task non-terminal, no ready mix_draft
registration failure -> task failed, workspace retained, retry enabled
verified registration -> task completed, ready mix_draft under jianying_root
historical false completion -> stale/failed, files retained
```

- [ ] **Step 4: Commit documentation only**

```powershell
git add docs/operations/montage-registration.md README.md
git diff --cached --name-only
git commit -m "docs: explain montage registration recovery"
```

Do not run a real Jianying registration as part of this plan's automated verification. Real registration occurs only when the user starts a montage task from the authenticated console.
