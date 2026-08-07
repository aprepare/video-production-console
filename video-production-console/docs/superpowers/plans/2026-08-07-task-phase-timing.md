# Task Phase Timing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Record and display real preparation, queue, execution, validation, asset-commit, research, draft-build, and Jianying-registration durations for every console task without inventing timing data.

**Architecture:** Persist host-clock phase attempts in a new `task_phase_runs` table and add `queued_at` to tasks. Instrument deterministic boundaries in the HTTP preparer, scheduler, App Server adapter, result validator, and montage coordinator; classify only known App Server/CLI tool events, import script timings only from a validated timing artifact, and aggregate the resulting records for task and project views.

**Tech Stack:** Go, SQLite, Python 3, existing App Server notifications, existing task WebSocket, React, TypeScript, Vitest.

**Execution order:** Implement the timing backend after the workflow task contract is stable. Task 9 requires the project-workbench components created by the workflow plan; do not create competing placeholder components.

---

## File map

- Create `internal/domain/timings.go`: timing keys, states, sources, task summaries.
- Modify `internal/domain/models.go`: add `QueuedAt` to `CodexTask`.
- Modify `internal/store/migrations.go`, `internal/store/migrations_test.go`: add `queued_at` and `task_phase_runs`.
- Create `internal/store/task_timings.go`, `internal/store/task_timings_test.go`: atomic phase lifecycle and aggregates.
- Modify `internal/store/tasks.go`, `internal/store/tasks_test.go`: persist `queued_at` and correct start/terminal boundaries.
- Create `internal/timing/classifier.go`, `internal/timing/classifier_test.go`: deterministic App Server/legacy event classification.
- Modify `internal/codex/scheduler.go`, `internal/codex/runner.go`, `internal/conversation/task_adapter.go`, `internal/conversation/broker.go`: instrument host and Codex boundaries.
- Modify `C:/Users/prepare/.codex/skills/jianying-montage-draft/scripts/run_montage_job.py`: emit validated deterministic execution timings.
- Modify `schemas/codex-result.schema.json`, `internal/codex/result_validator.go`: admit and verify a montage timing artifact.
- Modify `internal/montage/coordinator.go`, `internal/store/montage.go`: record registration attempts and asset commit.
- Create `internal/httpapi/task_timings.go`, `internal/httpapi/task_timings_test.go`: task and project timing APIs.
- Create `web/src/project-workbench/TaskTimingPanel.tsx`, `web/src/project-workbench/timing.ts`, and tests.

### Task 1: Add the timing data model and migration

**Files:**
- Create: `internal/domain/timings.go`
- Modify: `internal/domain/models.go`
- Modify: `internal/store/migrations.go`
- Modify: `internal/store/migrations_test.go`

- [ ] **Step 1: Write failing migration and type tests**

Assert a current database has `codex_tasks.queued_at`, `task_phase_runs`, the running-phase partial unique index, the task/attempt index, and a restart-safe state vocabulary. Upgrade a predecessor fixture and prove existing tasks remain present with `queued_at IS NULL`.

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/store -run 'TimingMigration|QueuedAtMigration' -count=1
```

- [ ] **Step 3: Define the domain types**

```go
type TaskPhaseSource string
const (
	PhaseSourceHost TaskPhaseSource = "host"
	PhaseSourceAppServer TaskPhaseSource = "app_server"
	PhaseSourceSkill TaskPhaseSource = "skill"
)

type TaskPhaseState string
const (
	PhaseQueued TaskPhaseState = "queued"
	PhaseRunning TaskPhaseState = "running"
	PhaseCompleted TaskPhaseState = "completed"
	PhaseFailed TaskPhaseState = "failed"
	PhaseCanceled TaskPhaseState = "canceled"
	PhaseInterrupted TaskPhaseState = "interrupted"
)

type TaskPhaseRun struct {
	ID, TaskID, PhaseKey, DisplayName, ExternalID, DetailJSON string
	Attempt int
	Source TaskPhaseSource
	State TaskPhaseState
	StartedAt time.Time
	RunningAt *time.Time
	FinishedAt *time.Time
	DurationMS *int64
	CreatedAt time.Time
}
```

Add `QueuedAt *time.Time` to `CodexTask`.

- [ ] **Step 4: Append the SQLite migration**

```sql
ALTER TABLE codex_tasks ADD COLUMN queued_at DATETIME;

CREATE TABLE task_phase_runs (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES codex_tasks(id) ON DELETE CASCADE,
    attempt INTEGER NOT NULL CHECK (attempt > 0),
    phase_key TEXT NOT NULL,
    display_name TEXT NOT NULL,
    source TEXT NOT NULL CHECK (source IN ('host','app_server','skill')),
    state TEXT NOT NULL CHECK (state IN ('queued','running','completed','failed','canceled','interrupted')),
    started_at DATETIME NOT NULL,
    running_at DATETIME,
    finished_at DATETIME,
    duration_ms INTEGER CHECK (duration_ms IS NULL OR duration_ms >= 0),
    external_id TEXT,
    detail_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(detail_json)),
    created_at DATETIME NOT NULL,
    CHECK (running_at IS NULL OR running_at >= started_at),
    CHECK (finished_at IS NULL OR finished_at >= started_at),
    CHECK (finished_at IS NULL OR running_at IS NULL OR finished_at >= running_at)
);
CREATE UNIQUE INDEX task_phase_one_running_uq
ON task_phase_runs(task_id,attempt,phase_key) WHERE state='running';
CREATE UNIQUE INDEX task_phase_external_event_uq
ON task_phase_runs(task_id,attempt,phase_key,source,external_id)
WHERE external_id IS NOT NULL;
CREATE INDEX task_phase_task_attempt_idx
ON task_phase_runs(task_id,attempt,started_at,id);
```

Do not backfill synthetic phases. Historical task `queued_at` stays null so the API can label its queue time as estimated from `created_at`.

- [ ] **Step 5: Verify and commit**

```powershell
go test ./internal/store -run 'TimingMigration|QueuedAtMigration' -count=1
git add internal/domain/timings.go internal/domain/models.go internal/store/migrations.go internal/store/migrations_test.go
git commit -m "feat: add task phase timing schema"
```

### Task 2: Implement atomic phase recording and summary calculations

**Files:**
- Create: `internal/store/task_timings.go`
- Create: `internal/store/task_timings_test.go`
- Modify: `internal/store/tasks.go`
- Modify: `internal/store/tasks_test.go`

- [ ] **Step 1: Write failing repository tests**

Test `StartPhase`, idempotent duplicate starts through `external_id`, queued-to-running transitions, `FinishPhase`, failure/cancel/interruption, retry attempt separation, restart interruption, negative-duration rejection, and summaries for running and terminal tasks. Use an injected clock so assertions are exact.

```go
started, err := timings.StartPhase(ctx, StartPhase{TaskID: taskID, Attempt: 1, Key: "result_validation", DisplayName: "结果校验", Source: domain.PhaseSourceHost, At: t0})
if err != nil { t.Fatal(err) }
finished, err := timings.FinishPhase(ctx, FinishPhase{ID: started.ID, State: domain.PhaseCompleted, At: t0.Add(1500*time.Millisecond)})
if err != nil || *finished.DurationMS != 1500 { t.Fatal(finished, err) }
```

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/store -run 'TaskPhase|TaskTimingSummary|QueuedAt' -count=1
```

- [ ] **Step 3: Implement repository methods and aggregates**

Provide:

```go
StartPhase(context.Context, StartPhase) (domain.TaskPhaseRun, error)
MarkPhaseRunning(context.Context, string, time.Time) (domain.TaskPhaseRun, error)
FinishPhase(context.Context, FinishPhase) (domain.TaskPhaseRun, error)
InterruptRunning(context.Context, time.Time) (int, error)
ForTask(context.Context, string) ([]domain.TaskPhaseRun, error)
SummaryForTask(context.Context, string, time.Time) (domain.TaskTimingSummary, error)
ProjectSummary(context.Context, string, int) ([]domain.TaskTimingAggregate, error)
```

The summary calculates:

```text
total       = terminal-or-now - created_at
prepare     = queued_at - created_at
queue       = first started_at - queued_at
execution   = terminal-or-now - first started_at
```

`StartPhase` accepts an optional initial state of `queued` or `running` and defaults to `running`; running phases set `running_at=started_at`. `MarkPhaseRunning` preserves the original accepted/queue timestamp and sets `running_at` to the actual active boundary. `duration_ms` measures the whole phase from accepted start to finish; the API may separately show `running_at-started_at` as that phase's queue portion. When task `queued_at` is null, use `created_at` only for the task queue boundary and set `QueueEstimated=true`. Pick the slowest completed execution phase by `duration_ms`; calculate its percentage against actual execution with a zero guard.

- [ ] **Step 4: Extend task persistence**

Add `queued_at` to `taskColumns`, all scan targets, `Create`, `CreateV2`, and list/get queries. Add `MarkQueued(ctx,id,at)` and make `Start`/the first transition to running set `started_at` exactly once. Terminal status methods retain the first start and set `finished_at` once.

- [ ] **Step 5: Verify and commit**

```powershell
go test ./internal/store -count=1
git add internal/store/task_timings.go internal/store/task_timings_test.go internal/store/tasks.go internal/store/tasks_test.go
git commit -m "feat: persist task timing phases"
```

### Task 3: Record preparation, queue, and actual scheduler start boundaries

**Files:**
- Modify: `internal/httpapi/tasks.go`
- Modify: `internal/httpapi/topic_selection.go`
- Create: `internal/httpapi/tasks_test.go`
- Modify: `internal/codex/scheduler.go`
- Modify: `internal/codex/scheduler_test.go`
- Modify: `internal/conversation/task_adapter.go`
- Modify: `internal/conversation/task_adapter_retry_test.go`

- [ ] **Step 1: Write failing boundary tests**

Prove `task_prepare` records the captured request-entry time through successful manifest preparation and finishes before `queued_at`; a preparation failure is persisted as a failed task with a failed preparation phase but is never schedulable; `queue_wait` starts at scheduler acceptance and ends at the first real execution boundary; legacy tasks set `started_at` only when a worker slot and project lock are acquired; App Server formal tasks queued behind an active turn remain `queued` with no `started_at`; and `TaskTurnStarted` atomically changes them to running.

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/httpapi ./internal/codex ./internal/conversation -run 'TaskPrepareTiming|QueueBoundary|TurnStartedTiming' -count=1
```

- [ ] **Step 3: Instrument HTTP preparation and enqueue**

Capture `prepareStartedAt` at request entry. `TaskManifestPreparer.Prepare` continues to validate and persist only a complete task/manifest, so no incomplete queued row can be raced by another scheduler signal. After success, insert `task_prepare` with the captured start and current finish time. On preparation failure, persist the task directly as terminal `failed`, record the failed preparation phase from the captured start, and never call the scheduler. Extract this as one shared helper used by ordinary project tasks and `enqueueTopicCommit` so chained workflows have identical boundaries.

- [ ] **Step 4: Correct legacy and App Server start semantics**

Each scheduler implementation calls `MarkQueued` exactly once when it accepts the already prepared task, starts the host `queue_wait` phase at that same timestamp, then signals a legacy worker or sends to the App Server. Legacy scheduler finishes `queue_wait` and starts `codex_execution` immediately after acquiring its concurrency slot/project lock and before launching the process. `TaskAdapter.Enqueue` no longer calls `UpdateStatus(...running...)` before `Broker.SendTask`; it preserves queued state when the broker returns `State="queued"`. `TaskTurnStarted` atomically finishes `queue_wait`, performs the single durable running transition, and starts `codex_execution`. Cancellation or terminal delivery failure before execution closes `queue_wait` with the matching terminal state and never creates a fake execution phase.

- [ ] **Step 5: Verify and commit**

```powershell
go test ./internal/httpapi ./internal/codex ./internal/conversation -count=1
git add internal/httpapi/tasks.go internal/httpapi/topic_selection.go internal/httpapi/tasks_test.go internal/codex/scheduler.go internal/codex/scheduler_test.go internal/conversation/task_adapter.go internal/conversation/task_adapter_retry_test.go
git commit -m "feat: record task preparation and queue timing"
```

### Task 4: Classify only observable Codex and research events

**Files:**
- Create: `internal/timing/classifier.go`
- Create: `internal/timing/classifier_test.go`
- Modify: `internal/progress/projector.go`
- Modify: `internal/progress/projector_test.go`
- Modify: `internal/conversation/broker.go`
- Modify: `internal/conversation/broker_test.go`
- Modify: `internal/codex/runner.go`
- Modify: `internal/codex/runner_test.go`

- [ ] **Step 1: Write failing classifier tests**

Map only recognized evidence:

```text
baokuan MCP call                    -> baokuan_search
grok_search.py command/tool call    -> web_research
Obsidian recent-card read           -> obsidian_dedup
download_pexels_media.py/index read -> media_search
validate_production_plan.py         -> production_plan
validate_montage_draft.py           -> plaintext_validation
turn start/completion               -> codex_execution
```

Unknown commands return no phase. The outer `run_montage_job.py` command is deliberately not classified as `draft_build`, because its validated timing artifact supplies the internal montage phases and counting both would duplicate/overlap time. Persist only the phase key, item ID, and safe classification; never store command arguments, full prompts, API keys, source text, or raw tool payload in `detail_json`.

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/timing ./internal/progress ./internal/conversation ./internal/codex -run 'Classify|TimingProjection' -count=1
```

- [ ] **Step 3: Implement idempotent event projection**

`Classify(Notification)` returns `{PhaseKey,DisplayName,Boundary,ExternalItemID}`. On item start, start the phase; on matching item completion/failure, finish that exact run. Store the safe item identity in `external_id` and use the dedicated unique index to merge replayed notifications. Consecutive occurrences of the same tool phase have different external IDs, remain separate runs, and task summaries sum them by key.

- [ ] **Step 4: Instrument App Server and legacy streams**

Broker resolves task identity from the persisted turn before recording item phases. Legacy Runner sends parsed JSONL events through the same classifier. `turn/completed`, terminal turn failure, cancellation, and transport interruption finish `codex_execution` with the matching terminal state.

- [ ] **Step 5: Verify and commit**

```powershell
go test ./internal/timing ./internal/progress ./internal/conversation ./internal/codex -count=1
git add internal/timing/classifier.go internal/timing/classifier_test.go internal/progress/projector.go internal/progress/projector_test.go internal/conversation/broker.go internal/conversation/broker_test.go internal/codex/runner.go internal/codex/runner_test.go
git commit -m "feat: derive timings from observable Codex events"
```

### Task 5: Record result validation and formal asset commit

**Files:**
- Modify: `internal/codex/runner.go`
- Modify: `internal/codex/runner_test.go`
- Modify: `internal/store/tasks.go`
- Modify: `internal/store/tasks_test.go`

- [ ] **Step 1: Write failing completion-phase tests**

Assert `result_validation` spans envelope parsing, path/hash/MIME validation, and publishing-package validation; `asset_commit` spans the single durable task/artifact/asset transaction; validation failure records failed validation and never starts asset commit; database failure records failed asset commit without claiming success.

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/codex ./internal/store -run 'ResultValidationTiming|AssetCommitTiming' -count=1
```

- [ ] **Step 3: Instrument exact boundaries**

Start `result_validation` at the entry to `CompleteAgentResult`/final-result resolution and finish only after all artifacts and formal outputs are verified in memory. Start `asset_commit` immediately before `CompleteWithResult`, `AwaitInput`, or the montage gate transaction and finish after commit/reconciliation returns. Do not include browser refresh or post-commit UI work.

- [ ] **Step 4: Verify and commit**

```powershell
go test ./internal/codex ./internal/store -count=1
git add internal/codex/runner.go internal/codex/runner_test.go internal/store/tasks.go internal/store/tasks_test.go
git commit -m "feat: time task result validation and asset commit"
```

### Task 6: Emit and import deterministic montage execution timings

**Files:**
- Modify: `C:/Users/prepare/.codex/skills/jianying-montage-draft/scripts/run_montage_job.py`
- Create: `C:/Users/prepare/.codex/skills/jianying-montage-draft/scripts/test_execution_timings.py`
- Modify: `C:/Users/prepare/.codex/skills/jianying-montage-draft/references/console-contract.md`
- Modify: `schemas/codex-result.schema.json`
- Modify: `internal/codex/result_validator.go`
- Modify: `internal/codex/result_validator_test.go`
- Create: `internal/timing/importer.go`
- Create: `internal/timing/importer_test.go`

- [ ] **Step 1: Write failing Python and Go tests**

Use a fake monotonic clock to prove the script writes non-negative phase records for deterministic phases and atomically publishes `execution-timings.json`. Go tests reject an unknown key, negative/overflow duration, wall-clock timestamps outside task bounds, duplicate external IDs, a path outside `output_dir`, and a changed SHA-256.

- [ ] **Step 2: Run and confirm failure**

```powershell
python -m unittest C:\Users\prepare\.codex\skills\jianying-montage-draft\scripts\test_execution_timings.py -v
go test ./internal/timing ./internal/codex -run 'SkillTimingImport|ExecutionTimings' -count=1
```

- [ ] **Step 3: Add the deterministic timing artifact**

The script writes UTF-8 JSON with this closed shape:

```json
{
  "schema_version": "1.0",
  "task_id": "uuid",
  "phases": [
    {"phase_key":"input_validation","display_name":"输入校验","started_at":"RFC3339Nano","finished_at":"RFC3339Nano","duration_ms":123,"external_id":"validate-inputs-1"}
  ]
}
```

Allowed keys are `input_validation`, `media_search`, `production_plan`, `draft_build`, and `plaintext_validation`; the script writes only phases it directly executed. Console result envelopes register the file as an `execution_timings` engineering artifact. No timing is produced for reasoning that has no tool boundary.

- [ ] **Step 4: Import after artifact verification**

After `Runner` verifies artifact containment and SHA-256, `timing.Importer` inserts skill-source phase runs idempotently. The server recomputes `duration_ms` from timestamps and rejects a discrepancy greater than 1000ms. Imported phases never overwrite host or App Server phases.

- [ ] **Step 5: Verify and commit**

```powershell
python -m unittest C:\Users\prepare\.codex\skills\jianying-montage-draft\scripts\test_execution_timings.py -v
go test ./internal/timing ./internal/codex -count=1
git add schemas/codex-result.schema.json internal/codex/result_validator.go internal/codex/result_validator_test.go internal/timing/importer.go internal/timing/importer_test.go
git commit -m "feat: import deterministic montage timings"
```

### Task 7: Time Jianying registration attempts and recovery

**Files:**
- Modify: `internal/montage/coordinator.go`
- Modify: `internal/montage/coordinator_test.go`
- Modify: `internal/store/montage.go`
- Modify: `internal/store/montage_test.go`

- [ ] **Step 1: Write failing registration timing tests**

Assert each registration attempt creates its own `jianying_registration` phase with the same attempt number; lock queue and active registration are retained in safe detail; retry does not overwrite attempt one; `repo.Succeed` records a distinct `asset_commit`; recovery marks an uncertain running phase interrupted before requeuing.

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/montage ./internal/store -run 'RegistrationTiming|RegistrationRetryTiming' -count=1
```

- [ ] **Step 3: Instrument coordinator boundaries**

Start registration timing when the host job is accepted into the registration queue, record its queued/running transition when `MarkRunning` succeeds, and finish it after the trusted registrar result is verified. Start the montage `asset_commit` phase immediately before `repo.Succeed` and finish after the ready `mix_draft` transaction is durable. Preserve all earlier attempts.

- [ ] **Step 4: Verify and commit**

```powershell
go test ./internal/montage ./internal/store -count=1
git add internal/montage/coordinator.go internal/montage/coordinator_test.go internal/store/montage.go internal/store/montage_test.go
git commit -m "feat: time Jianying registration attempts"
```

### Task 8: Expose timing APIs and replayable phase events

**Files:**
- Create: `internal/httpapi/task_timings.go`
- Create: `internal/httpapi/task_timings_test.go`
- Modify: `internal/app/app.go`
- Modify: `internal/app/app_test.go`
- Modify: `internal/realtime/hub.go`
- Modify: `internal/realtime/hub_test.go`

- [ ] **Step 1: Write failing API and event tests**

Cover:

```text
GET /api/tasks/{id}/timings
GET /api/projects/{id}/timing-summary?limit=50
```

The task response includes total, preparation, queue, actual execution, estimate flag, slowest phase, phase attempts, and `legacy_without_phases`. The project summary groups recent tasks by action and returns median/max phase durations. Phase WebSocket events contain sequence, `phase_started|phase_completed|phase_failed`, safe Chinese display name, state, timestamps, and duration; they contain no raw JSON or command text.

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/httpapi ./internal/realtime ./internal/app -run 'TaskTimings|TimingSummary|PhaseEvent' -count=1
```

- [ ] **Step 3: Implement authenticated read-only views**

Return durations as integer milliseconds plus server timestamps. Reject unknown task/project IDs; cap recent-task limit at 200. Publish phase events only after timing rows commit. Reconnect uses the existing task-event sequence/cursor path and must not mutate timing rows from browser time.

- [ ] **Step 4: Verify and commit**

```powershell
go test ./internal/httpapi ./internal/realtime ./internal/app -count=1
git add internal/httpapi/task_timings.go internal/httpapi/task_timings_test.go internal/app/app.go internal/app/app_test.go internal/realtime/hub.go internal/realtime/hub_test.go
git commit -m "feat: expose task timing APIs"
```

### Task 9: Render timing rails and recent bottleneck statistics

**Files:**
- Create: `web/src/project-workbench/timing.ts`
- Create: `web/src/project-workbench/timing.test.ts`
- Create: `web/src/project-workbench/TaskTimingPanel.tsx`
- Create: `web/src/project-workbench/TaskTimingPanel.test.tsx`
- Modify: `web/src/project-workbench/ProjectWorkbench.tsx`
- Modify: `web/src/project-workbench/project-workbench.css`
- Modify: `web/src/App.tsx`

- [ ] **Step 1: Write failing formatter and component tests**

Assert `127000` renders `2分07秒`; a running phase updates from its server `started_at`; the overview shows total, queue, execution, slowest phase and percentage; mobile defaults to the three slowest phases; retries remain separate rows; and historical tasks display “旧任务无步骤统计” without fabricated segments.

- [ ] **Step 2: Run and confirm failure**

```powershell
cd web
npx vitest run src/project-workbench/timing.test.ts src/project-workbench/TaskTimingPanel.test.tsx
```

- [ ] **Step 3: Build the proportional timing rail**

Use the same structural visual language as `ProductionRail`: static completed segments, one restrained active cursor, tooltips/expanded rows with start, finish, duration, attempt, and failure explanation. Use server duration for terminal phases and `Date.now() - started_at` only for the display clock of a running phase; never POST browser timing.

- [ ] **Step 4: Add recent timing insights**

Add a collapsed “耗时统计” view that calls the project summary endpoint and lists median/max by task action and phase. Do not add cost prediction, cross-machine comparisons, or model-generated recommendations.

- [ ] **Step 5: Verify without real external applications and commit**

```powershell
npx vitest run
npm run lint
npm run build
cd ..
go test ./internal/domain ./internal/store ./internal/timing ./internal/httpapi ./internal/realtime ./internal/codex ./internal/conversation ./internal/montage ./internal/app ./cmd/console -count=1
git add web/src/project-workbench/timing.ts web/src/project-workbench/timing.test.ts web/src/project-workbench/TaskTimingPanel.tsx web/src/project-workbench/TaskTimingPanel.test.tsx web/src/project-workbench/ProjectWorkbench.tsx web/src/project-workbench/project-workbench.css web/src/App.tsx internal/webui/dist
git commit -m "feat: display task timing bottlenecks"
```

Do not launch Jianying, do not open WeChat Video Channels, and do not invoke real Grok, Pexels, Baokuan MCP, or Obsidian writes in automated tests.

## Acceptance checklist

- Every new task stores `queued_at`; historical tasks retain a visible estimated-queue marker.
- Total, preparation, queue, and actual execution boundaries are non-negative and derived from host timestamps.
- App Server tasks queued behind an active turn do not appear running before their turn starts.
- Unknown tool events do not generate guessed phases.
- Selection/remix phases may show Obsidian de-duplication, Baokuan search, web research, and generation only when observable.
- Montage shows input validation, media search, production plan, draft build, plaintext validation, Jianying registration, and asset commit only when backed by host/tool/script events.
- Registration retries preserve all attempts and restart marks uncertain running phases interrupted.
- Task UI shows total, queue, execution, slowest phase and proportion; old tasks say they lack step statistics.
- No browser clock is persisted, no secrets/raw commands enter timing details, and no real external application is launched by tests.
