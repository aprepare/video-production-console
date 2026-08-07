# Project Workbench And Workflow Simplification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the project drawer with a durable one-screen production workbench and reduce every new video project to the real flow “二创文案 → 配音与字幕 → 生成混剪 → 成片审核 → 已发布”.

**Architecture:** Preserve historical project and spoken-script rows for audit, but remove them from every new-task contract and UI decision. Add a durable server-side remix workflow run so one click can commit a missing topic card and automatically enqueue the remix task; expose publication as an explicit local state change, then render one project at `/projects/{id}` from focused React components instead of the current drawer in `App.tsx`.

**Tech Stack:** Go, SQLite, React 19, TypeScript, Vite, Vitest, Testing Library, Lucide React.

**Execution order:** Implement this plan first. The other three plans may build on its `project-workbench` components and Lucide dependency; backend-only tasks from the readable-name plan may run in parallel when they do not touch the same files.

---

## File map

- Modify `internal/domain/models.go`, `internal/domain/stages.go`, `internal/domain/assets.go`, `internal/domain/requirements.go`: define the five-stage new-project workflow while retaining read-only legacy constants.
- Modify `internal/store/migrations.go`, `internal/store/migrations_test.go`: migrate legacy `topic`/`ready` projects and add durable `project_workflow_runs`.
- Modify `internal/store/projects.go`, `internal/store/projects_test.go`: publish projects and calculate stages from current formal assets.
- Create `internal/workflow/remix.go`, `internal/workflow/remix_test.go`: orchestrate topic-card commit followed by remix.
- Create `internal/httpapi/workflow_launcher.go`, `internal/httpapi/workflow_launcher_test.go`: adapt existing topic-selection and manifest preparation rules to the workflow's narrow launcher interface without an import cycle.
- Modify `internal/taskcompletion/gate.go`, `internal/codex/runner.go`, `internal/conversation/task_adapter.go`: notify the workflow only after a task result is durably committed.
- Modify `internal/httpapi/projects.go`, `internal/httpapi/projects_test.go`, `internal/httpapi/tasks.go`, `internal/app/app.go`: expose remix workflow and publish endpoints.
- Modify `internal/codex/manifest.go`, `internal/codex/result_validator.go`, `schemas/codex-result.schema.json`, `schemas/task-manifest.schema.json`: remove spoken-script outputs from new console contracts.
- Modify `C:/Users/prepare/.codex/skills/finance-viral-remix/references/console-contract.md`: make `continuous_script` the only formal copy asset in console mode.
- Create `web/src/project-workbench/types.ts`, `web/src/project-workbench/workflow.ts`, `web/src/project-workbench/ProjectWorkbench.tsx`, `web/src/project-workbench/ProductionRail.tsx`, `web/src/project-workbench/ProjectAssets.tsx`, `web/src/project-workbench/ProjectConversation.tsx`, `web/src/project-workbench/project-workbench.css`.
- Modify `web/src/App.tsx`, `web/src/App.css`, `web/src/App.test.tsx`, `web/package.json`, `web/package-lock.json`.

### Task 1: Migrate the project lifecycle and keep legacy spoken scripts read-only

**Files:**
- Modify: `internal/domain/models.go`
- Modify: `internal/domain/stages.go`
- Modify: `internal/domain/assets.go`
- Modify: `internal/domain/requirements.go`
- Modify: `internal/domain/stages_test.go`
- Modify: `internal/domain/assets_test.go`
- Modify: `internal/domain/requirements_test.go`
- Modify: `internal/store/migrations.go`
- Modify: `internal/store/migrations_test.go`
- Modify: `internal/store/projects.go`
- Modify: `internal/store/projects_test.go`

- [ ] **Step 1: Write failing lifecycle and migration tests**

Add tests proving new projects are created at `script` and move only through `script`, `assets`, `mixing`, `review`, `published`; `review → published` requires a ready `final_video`; `montage.execute` does not require `spoken_script`; changing a continuous script no longer treats a historical spoken script as a required downstream product; and migration converts existing `stage='topic'` rows to `stage='script'` plus `stage='ready'` rows to `stage='review'` without deleting assets or tasks.

```go
func TestReviewPublishesWithoutReadyStage(t *testing.T) {
	available := map[AssetType]bool{AssetFinalVideo: true}
	if err := CanMove(StageReview, StagePublished, available); err != nil {
		t.Fatal(err)
	}
	if err := CanMove(StageReview, StageReady, available); err == nil {
		t.Fatal("new workflow still accepted the removed ready stage")
	}
}
```

- [ ] **Step 2: Run the focused tests and confirm they fail**

```powershell
go test ./internal/domain ./internal/store -run 'ReviewPublishes|SpokenScript|LegacyStageMigration|NewProjectStartsAtScript' -count=1
```

Expected: failures show legacy `StageTopic`/`StageReady`, `AssetSpokenScript`, and the old invalidation edges are still active.

- [ ] **Step 3: Implement the domain compatibility boundary**

Keep `StageTopic`, `StageReady`, `AssetSpokenScript`, and `ActionSpokenFormat` declared with `Deprecated:` comments so historical structs and audit views can still decode them. Remove them from `CanMove`, `validStage`, new action requirements, invalidation targets, upload/result allowlists, and new-project creation. Use this new order:

```go
var productionStageOrder = map[ProjectStage]int{
	StageScript: 0,
	StageAssets: 1,
	StageMixing: 2,
	StageReview: 3,
	StagePublished: 4,
}
```

Append a migration that rebuilds the `projects` table constraint with only `script`, `assets`, `mixing`, `review`, `published`, `archived`, updates `topic` to `script` and `ready` to `review`, and preserves every existing column, index, foreign key, timestamp, and publication field. Change `ProjectRepository.CreateProject` and `SyncStageFromAssets` so a new/empty project remains at `script`, a ready continuous script advances to `assets`, narration plus SRT advances to `mixing`, and a mix draft or final video leaves the project at `review` until explicit publication. Do not delete `spoken_script` enum values from historical asset tables.

- [ ] **Step 4: Verify and commit**

```powershell
go test ./internal/domain ./internal/store -count=1
git add internal/domain/models.go internal/domain/stages.go internal/domain/assets.go internal/domain/requirements.go internal/domain/stages_test.go internal/domain/assets_test.go internal/domain/requirements_test.go internal/store/migrations.go internal/store/migrations_test.go internal/store/projects.go internal/store/projects_test.go
git commit -m "refactor: simplify project production lifecycle"
```

### Task 2: Persist remix workflow runs and publication state

**Files:**
- Modify: `internal/store/migrations.go`
- Modify: `internal/store/migrations_test.go`
- Create: `internal/domain/workflows.go`
- Create: `internal/store/workflows.go`
- Create: `internal/store/workflows_test.go`
- Modify: `internal/store/projects.go`
- Modify: `internal/store/projects_test.go`

- [ ] **Step 1: Write failing repository tests**

Cover one active remix workflow per project, idempotent retrieval after a repeated click, transition `topic_card → remix → completed`, durable failure details, and publishing only from `review` with a ready `final_video`.

```go
run, err := workflows.BeginRemix(ctx, domain.ProjectWorkflowRun{
	ID: runID, ProjectID: projectID, AccountID: accountID,
	Kind: domain.WorkflowRemix, State: domain.WorkflowRunning,
	CurrentStep: domain.WorkflowStepTopicCard,
})
if err != nil || run.CurrentStep != domain.WorkflowStepTopicCard { t.Fatal(run, err) }
```

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/store -run 'Workflow|PublishProject' -count=1
```

- [ ] **Step 3: Add the durable model and transaction methods**

Append this table and its partial uniqueness guard in the migration:

```sql
CREATE TABLE project_workflow_runs (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('remix')),
    state TEXT NOT NULL CHECK (state IN ('running','completed','failed','canceled')),
    current_step TEXT NOT NULL CHECK (current_step IN ('topic_card','remix','completed')),
    topic_task_id TEXT REFERENCES codex_tasks(id) ON DELETE SET NULL,
    remix_task_id TEXT REFERENCES codex_tasks(id) ON DELETE SET NULL,
    model_name TEXT NOT NULL,
    reasoning_effort TEXT NOT NULL,
    error_code TEXT,
    error_message TEXT,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    finished_at DATETIME
);
CREATE UNIQUE INDEX project_workflow_active_uq
ON project_workflow_runs(project_id,kind) WHERE state='running';
```

Implement `BeginRemix`, `BindTopicTask`, `AdvanceToRemix`, `Complete`, `Fail`, `ActiveForProject`, and `ByTask`. Add `ProjectRepository.PublishProject(ctx,id,now)` as one immediate transaction that verifies the current ready final-video version, updates `stage='published'`, `publication_status='published'`, and `published_at`, and never calls an external publisher.

- [ ] **Step 4: Verify and commit**

```powershell
go test ./internal/store -count=1
git add internal/domain/workflows.go internal/store/migrations.go internal/store/migrations_test.go internal/store/workflows.go internal/store/workflows_test.go internal/store/projects.go internal/store/projects_test.go
git commit -m "feat: persist project remix workflows"
```

### Task 3: Orchestrate topic-card commit and remix after durable completion

**Files:**
- Modify: `internal/taskcompletion/gate.go`
- Create: `internal/workflow/remix.go`
- Create: `internal/workflow/remix_test.go`
- Modify: `internal/codex/runner.go`
- Modify: `internal/codex/runner_test.go`
- Modify: `internal/conversation/task_adapter.go`
- Modify: `internal/conversation/task_adapter_retry_test.go`
- Create: `internal/httpapi/workflow_launcher.go`
- Create: `internal/httpapi/workflow_launcher_test.go`
- Modify: `cmd/console/main.go`
- Modify: `cmd/console/main_test.go`

- [ ] **Step 1: Write failing orchestration tests**

Use a fake workflow task launcher and scheduler. Prove that a project with a ready topic card immediately enqueues `remix.from_topic_card`; a project without one enqueues `topic.commit`; a successful topic task starts exactly one remix task; a failed/canceled topic task preserves its card artifacts and stops the workflow without creating a remix task; and repeated completion notifications do not create duplicates.

```go
func TestRemixWorkflowContinuesAfterTopicCommit(t *testing.T) {
	run, err := coordinator.Start(ctx, StartRemix{ProjectID: projectID, AccountID: accountID})
	if err != nil || run.TopicTaskID == nil { t.Fatal(run, err) }
	if err := coordinator.AfterTerminalTask(ctx, completedTopicTask); err != nil { t.Fatal(err) }
	if scheduler.Count(domain.ActionRemixFromTopic) != 1 { t.Fatal("remix was not enqueued once") }
}
```

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/workflow ./internal/httpapi ./internal/codex ./internal/conversation -run 'RemixWorkflow|WorkflowTaskLauncher|CompletionObserver' -count=1
```

- [ ] **Step 3: Add a post-commit observer and coordinator**

Extend the completion contract without changing the montage gate:

```go
type Observer interface {
	AfterTerminal(context.Context, domain.CodexTask) error
}
```

`Runner` calls the observer only after `CompleteWithResult`, `AwaitInput`, or terminal failure has committed. Observer failure appends a workflow warning event and updates `project_workflow_runs`; it must not roll back or relabel the already valid task result.

Keep `internal/workflow` independent from the HTTP package by defining a narrow `TaskLauncher` interface with `LaunchTopicCommit` and `LaunchRemixFromTopicCard`. Implement it in `internal/httpapi/workflow_launcher.go`, where it can reuse `findProjectTopicSelection`, the existing `enqueueTopicCommit` rules, `TaskManifestPreparer`, and model resolution without creating an import cycle. `workflow.RemixCoordinator` records the chosen model/reasoning once and passes the same values through both launcher calls.

- [ ] **Step 4: Wire both legacy CLI and App Server paths**

Create one coordinator in `cmd/console/main.go`, pass it to the legacy `Runner` factory and `conversation.TaskCompletionConfig`, and ensure both paths invoke the same post-commit observer. Keep `montage.Coordinator` as the pre-commit registration gate for `montage.execute`.

- [ ] **Step 5: Verify and commit**

```powershell
go test ./internal/workflow ./internal/httpapi ./internal/codex ./internal/conversation ./cmd/console -count=1
git add internal/taskcompletion/gate.go internal/workflow/remix.go internal/workflow/remix_test.go internal/httpapi/workflow_launcher.go internal/httpapi/workflow_launcher_test.go internal/codex/runner.go internal/codex/runner_test.go internal/conversation/task_adapter.go internal/conversation/task_adapter_retry_test.go cmd/console/main.go cmd/console/main_test.go
git commit -m "feat: chain topic card and remix tasks"
```

### Task 4: Expose one remix action and an explicit published action

**Files:**
- Modify: `internal/httpapi/projects.go`
- Modify: `internal/httpapi/projects_test.go`
- Modify: `internal/httpapi/tasks.go`
- Modify: `internal/httpapi/task_manifest.go`
- Modify: `internal/httpapi/ideas.go`
- Modify: `internal/app/app.go`
- Modify: `internal/app/app_test.go`

- [ ] **Step 1: Write failing HTTP tests**

Test these endpoints with authenticated requests and fake services:

```text
POST /api/projects/{id}/remix
POST /api/projects/{id}/publish
```

The remix body accepts only `model` and `reasoning_effort`; project/account identity comes from the database. The response returns the workflow run and current task. Publish returns `409 final_video_missing` before a final video, `409 project_not_in_review` from other stages, and `200` with `stage=published` when valid.

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/httpapi ./internal/app -run 'ProjectRemix|ProjectPublish' -count=1
```

- [ ] **Step 3: Implement narrow handlers and project detail state**

Add the remix coordinator dependency to `app.Options` and `NewProjectsHandler`. Include `active_workflow` in `GET /api/projects/{id}`. Ensure both direct project creation and “确认并建项目” create the project at `script`. Remove the browser-facing `POST /topic-card` action from the project workbench path but retain its handler for compatibility with old clients until one release after the new workbench ships.

- [ ] **Step 4: Verify and commit**

```powershell
go test ./internal/httpapi ./internal/app -count=1
git add internal/httpapi/projects.go internal/httpapi/projects_test.go internal/httpapi/tasks.go internal/httpapi/task_manifest.go internal/httpapi/ideas.go internal/app/app.go internal/app/app_test.go
git commit -m "feat: expose project remix and publish actions"
```

### Task 5: Remove spoken-script generation from new task contracts

**Files:**
- Modify: `internal/codex/manifest.go`
- Modify: `internal/codex/manifest_test.go`
- Modify: `internal/codex/result_validator.go`
- Modify: `internal/codex/result_validator_test.go`
- Modify: `schemas/codex-result.schema.json`
- Modify: `schemas/task-manifest.schema.json`
- Modify: `C:/Users/prepare/.codex/skills/finance-viral-remix/references/console-contract.md`
- Modify: `C:/Users/prepare/.codex/skills/finance-viral-remix/SKILL.md`

- [ ] **Step 1: Write failing protocol tests**

Assert that new remix manifests declare only `continuous_script`, `remix.spoken_format` is rejected for a new console task, and a result envelope containing `asset_outputs[].type="spoken_script"` fails validation while historical database reads remain valid.

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/codex ./internal/httpapi -run 'Spoken|RemixOutputs' -count=1
```

- [ ] **Step 3: Update code, schemas, and Skill console documentation together**

Set the new matrix to:

```text
remix.standard        -> continuous_script
remix.enhanced        -> continuous_script
remix.from_topic_card -> continuous_script
remix.review          -> no formal asset
```

Remove `remix.spoken_format` from the console-mode accepted action list and remove `spoken_script.txt` from the console artifact matrix. Keep ordinary non-console Skill behavior unchanged for a user who explicitly asks for a line-broken oral script outside this production console.

- [ ] **Step 4: Run deterministic Skill checks and commit**

```powershell
go test ./internal/codex ./internal/httpapi -count=1
rg -n "spoken_script|spoken_format" schemas internal/codex internal/httpapi C:\Users\prepare\.codex\skills\finance-viral-remix\references\console-contract.md
git add internal/codex/manifest.go internal/codex/manifest_test.go internal/codex/result_validator.go internal/codex/result_validator_test.go schemas/codex-result.schema.json schemas/task-manifest.schema.json
git commit -m "refactor: remove spoken scripts from console production"
```

The `rg` result may contain only deprecated historical decode constants and explicitly documented non-console compatibility.

### Task 6: Add a persistent project route and focused workbench model

**Files:**
- Modify: `web/package.json`
- Modify: `web/package-lock.json`
- Create: `web/src/project-workbench/types.ts`
- Create: `web/src/project-workbench/workflow.ts`
- Create: `web/src/project-workbench/workflow.test.ts`
- Modify: `web/src/App.tsx`
- Modify: `web/src/App.test.tsx`

- [ ] **Step 1: Install Lucide and write failing route/workflow tests**

```powershell
cd web
npm install lucide-react
```

Add tests that clicking a project pushes `/projects/project-1`, reload at that path restores the same project, browser Back returns to the board, and workflow derivation depends on ready formal assets rather than click count.

```ts
expect(deriveProductionStage({ continuous_script: ready })).toBe("assets");
expect(deriveProductionStage({ continuous_script: ready, narration: ready, subtitle_srt: ready })).toBe("mixing");
```

- [ ] **Step 2: Run and confirm failure**

```powershell
npx vitest run src/project-workbench/workflow.test.ts src/App.test.tsx
```

- [ ] **Step 3: Implement the route parser and typed view model**

Create `parseLocation(pathname)` returning `{view:'projects'}` or `{view:'project',projectID}`. Listen to `popstate`, use `history.pushState` on project open, and keep query-string task selection compatible. `workflow.ts` exports the five Chinese stage labels, current stage, missing inputs, and next primary action from `ProjectDetail`; it never maps `spoken_script` or `ready`.

- [ ] **Step 4: Verify and commit**

```powershell
npx vitest run src/project-workbench/workflow.test.ts src/App.test.tsx
git add package.json package-lock.json src/project-workbench/types.ts src/project-workbench/workflow.ts src/project-workbench/workflow.test.ts src/App.tsx src/App.test.tsx
git commit -m "feat: add persistent project workbench route"
```

### Task 7: Build the desktop one-screen production workbench

**Files:**
- Create: `web/src/project-workbench/ProjectWorkbench.tsx`
- Create: `web/src/project-workbench/ProductionRail.tsx`
- Create: `web/src/project-workbench/ProjectAssets.tsx`
- Create: `web/src/project-workbench/ProjectConversation.tsx`
- Create: `web/src/project-workbench/ProjectWorkbench.test.tsx`
- Create: `web/src/project-workbench/project-workbench.css`
- Modify: `web/src/App.tsx`
- Modify: `web/src/App.css`

- [ ] **Step 1: Write failing behavior and accessibility tests**

Render a 1366×768 workbench and assert the project title, five production stages, next action, asset summary, Codex conversation entry, delete action, and task summary are all in the initial document. Assert there is no `role="dialog"` project drawer, no “给我选题”, no “深化一下”, no “口播稿”, and no “待发布”. Assert every icon button has an accessible name.

- [ ] **Step 2: Run and confirm failure**

```powershell
npx vitest run src/project-workbench/ProjectWorkbench.test.tsx
```

- [ ] **Step 3: Implement the authored workbench composition**

Use a cool-gray editing surface, deep-ink text, green completion, amber waiting, and one warm active cursor. The signature element is a single horizontal production rail shared visually with the task timing rail; the rest of the page stays quiet. The desktop grid is:

```text
project masthead
production rail
primary action + active task | current project assets | Codex conversation
collapsed technical history
```

Use only Lucide icons such as `ArrowLeft`, `Trash2`, `FileText`, `AudioLines`, `Captions`, `Clapperboard`, `CircleCheck`, `MessageSquare`, `Upload`, and `ChevronDown`. Replace the current text glyph close button and pulse dot; do not add emoji or hand-written SVG.

- [ ] **Step 4: Wire actions to real APIs**

“二创文案” calls `POST /remix`; narration and SRT have separate visible upload controls; “生成混剪” calls the existing `montage.execute` task endpoint; final video upload leads to review; “已发布” calls `POST /publish`. Active workflows display “正在生成选题卡” or “正在二创文案” from server state and do not require a second click.

- [ ] **Step 5: Verify and commit**

```powershell
npx vitest run src/project-workbench/ProjectWorkbench.test.tsx src/App.test.tsx
git add src/project-workbench/ProjectWorkbench.tsx src/project-workbench/ProductionRail.tsx src/project-workbench/ProjectAssets.tsx src/project-workbench/ProjectConversation.tsx src/project-workbench/ProjectWorkbench.test.tsx src/project-workbench/project-workbench.css src/App.tsx src/App.css
git commit -m "feat: build one-screen project production workbench"
```

### Task 8: Finish mobile behavior, Chinese copy, and regression boundaries

**Files:**
- Modify: `web/src/project-workbench/project-workbench.css`
- Modify: `web/src/project-workbench/ProjectWorkbench.tsx`
- Modify: `web/src/project-workbench/ProjectWorkbench.test.tsx`
- Modify: `web/src/App.tsx`
- Modify: `web/src/App.css`
- Modify: `web/src/App.test.tsx`

- [ ] **Step 1: Add failing mobile and copy tests**

Assert the workbench becomes one column below 760px, the production rail is vertical, the primary action is in a safe-area-aware sticky bottom bar, touch targets are at least 44px, assets/conversation are page accordions, and touched user-facing strings contain neither replacement characters nor known mojibake sequences.

- [ ] **Step 2: Run and confirm failure**

```powershell
npx vitest run src/project-workbench/ProjectWorkbench.test.tsx src/App.test.tsx
```

- [ ] **Step 3: Implement responsive and reduced-motion rules**

Use `padding-bottom: calc(5.5rem + env(safe-area-inset-bottom))`, a 44px minimum interactive height, and `@media (prefers-reduced-motion: reduce)` to disable the active cursor animation. Keep technical JSON, paths, and raw diagnostics collapsed. Replace mojibake in every project/task string touched by this work with plain Chinese that states the failure step, preserved result, and next action.

- [ ] **Step 4: Run the package verification without launching Jianying or Video Channels**

```powershell
go test ./internal/domain ./internal/store ./internal/workflow ./internal/httpapi ./internal/codex ./internal/conversation ./internal/app ./cmd/console -count=1
cd web
npx vitest run
npm run lint
npm run build
```

Expected: all tests pass, lint exits 0, and Vite produces the web bundle. Do not start Jianying, do not open WeChat Video Channels, and do not publish anything.

- [ ] **Step 5: Commit the responsive finish**

```powershell
git add web/src/project-workbench/project-workbench.css web/src/project-workbench/ProjectWorkbench.tsx web/src/project-workbench/ProjectWorkbench.test.tsx web/src/App.tsx web/src/App.css web/src/App.test.tsx internal/webui/dist
git commit -m "feat: finish responsive project workbench"
```

## Acceptance checklist

- `/projects/{id}` survives reload, Back, and direct navigation.
- A 1366×768 first screen shows project identity, five-stage rail, next action, asset summary, and Codex conversation entry.
- Mobile is one column with a safe-area sticky primary action and 44px touch targets.
- The project page contains no spoken-script controls, project topic button, deepen button, ready stage, drawer, or emoji.
- One remix click durably runs topic-card commit when needed and then remix; a failure stops at the failed step and preserves prior outputs.
- Publishing changes only local project state and never invokes an external platform.
- Historical spoken-script and task rows remain readable for audit but cannot enter new task manifests or outputs.
- All icons are Lucide, user-facing touched strings are valid Chinese, and reduced motion is respected.
