# Codex Misc Project Routing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Route every console-owned video task thread into the Codex Desktop project rooted at `C:/Users/prepare/Documents/杂项`, give it a readable Chinese name, and safely migrate idle old console threads without interrupting active work.

**Architecture:** Codex App Server does not accept a Desktop `projectId`, so the console will classify threads through a configurable canonical project root and per-video-project cwd: `<杂项>/video-console-tasks/<video-project-id>`. Formal turns receive two writable roots—the thread cwd and the task output directory—while all inputs remain absolute/read-only; a durable migration service forks idle console-owned project threads into the new cwd, verifies and names them, atomically swaps the console binding, then archives the old thread.

**Tech Stack:** Go, SQLite, Codex App Server JSON-RPC, React, TypeScript, existing authenticated settings UI.

**Execution order:** Backend routing tasks are independent of timing work but share `conversation`, `app`, and Settings files with the workflow plan. Integrate them after those touching tasks are committed; Task 8 assumes the workflow plan has installed Lucide React.

---

## Fixed target

```text
Desktop project name: 杂项
Desktop project ID:   4d8d66b0-f80b-4f77-aef3-280a232398a6
Project root:         C:\Users\prepare\Documents\杂项
Thread cwd pattern:   C:\Users\prepare\Documents\杂项\video-console-tasks\<video-project-id>
```

The implementation never edits Codex private databases or rollout files. The Desktop project ID is informational only; cwd is the supported classification mechanism.

## File map

- Modify `internal/domain/settings.go`, `internal/settings/service.go`, tests and JSON schema: add the task project root setting.
- Modify `cmd/console/main.go`, `cmd/console/main_test.go`: seed the default from the user Documents folder and include it in allowed roots.
- Modify `internal/conversation/service.go`, `internal/conversation/service_test.go`: create project main threads under the misc root and set readable names.
- Modify `internal/conversation/broker.go`, `internal/conversation/broker_test.go`, `internal/conversation/task_adapter.go`: give formal turns both writable roots.
- Modify `internal/store/conversations.go`, `internal/store/conversations_test.go`: query eligibility and atomically switch a main-thread binding.
- Create `internal/conversation/routing.go`, `internal/conversation/routing_test.go`: safe fork/read/name/swap/archive migration workflow.
- Create `internal/httpapi/codex_routing.go`, `internal/httpapi/codex_routing_test.go`: routing settings and migration endpoints.
- Modify `web/src/App.tsx`, `web/src/App.test.tsx`: show routing health and migration status in Settings.

### Task 1: Add and validate the Codex task project root setting

**Files:**
- Modify: `internal/domain/settings.go`
- Modify: `internal/settings/service.go`
- Modify: `internal/settings/service_test.go`
- Modify: `internal/settings/windows_path_test.go`
- Modify: `internal/store/settings.go`
- Modify: `schemas/settings.schema.json`
- Modify: `cmd/console/main.go`
- Modify: `cmd/console/main_test.go`

- [ ] **Step 1: Write failing settings tests**

Assert `codex_task_project_root` round-trips through public/configured/active settings, defaults to the canonical `Documents/杂项` path when available, rejects relative paths, aliases/symlinks/reparse points, files, and non-writable directories, and marks a changed value restart-sensitive.

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/settings ./cmd/console -run 'TaskProjectRoot|CodexRoutingRoot' -count=1
```

- [ ] **Step 3: Extend settings and boot defaults**

Add:

```go
CodexTaskProjectRoot string `json:"codex_task_project_root"`
```

to `PublicSettings` and `Runtime`. Include it in `publicValues`, `publicFromValues`, cloning, restart comparison, JSON schema, and validation. `resolveDefaultTaskProjectRoot(home)` returns `filepath.Join(home,"Documents","杂项")` only when the directory exists and canonicalizes to itself. Do not silently fall back to `视频号混剪`; leave an invalid/unavailable configured root as an explicit health error.

- [ ] **Step 4: Verify and commit**

```powershell
go test ./internal/settings ./cmd/console -count=1
git add internal/domain/settings.go internal/settings/service.go internal/settings/service_test.go internal/settings/windows_path_test.go internal/store/settings.go schemas/settings.schema.json cmd/console/main.go cmd/console/main_test.go
git commit -m "feat: configure Codex task project root"
```

### Task 2: Create every new project main thread in the misc-project cwd

**Files:**
- Modify: `internal/conversation/service.go`
- Modify: `internal/conversation/service_test.go`
- Modify: `internal/conversation/broker.go`
- Modify: `internal/conversation/broker_test.go`
- Modify: `cmd/console/main.go`

- [ ] **Step 1: Write failing cwd tests**

Prove `EnsureProjectMainSession(projectID)` creates and starts a thread at `<task-root>/video-console-tasks/<projectID>`, accepts that canonical directory as an allowed root, refuses an unavailable task root, and does not fall back to `data_root/projects` or the current development repository.

```go
want := filepath.Join(miscRoot, "video-console-tasks", projectID)
if session.WorkingDirectory != want { t.Fatalf("cwd=%q want %q", session.WorkingDirectory, want) }
if rpc.lastStartParams["cwd"] != want { t.Fatalf("thread/start cwd=%v", rpc.lastStartParams["cwd"]) }
```

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/conversation ./cmd/console -run 'ProjectMain.*WorkingDirectory|TaskProjectRoot' -count=1
```

- [ ] **Step 3: Change `ServiceOptions` and directory resolution**

```go
type ServiceOptions struct {
	DataRoot, DesktopWorkingDirectory, TaskProjectRoot string
}
```

`projectWorkingDirectory` validates the configured root, creates only `video-console-tasks` and the UUID child, rejects links/reparse aliases, and verifies the relative path is exactly `video-console-tasks/<projectID>`. `cmd/console/main.go` adds the task root to `CodexWorkspaceRoots` and passes it to `conversation.NewService`.

- [ ] **Step 4: Make missing-thread replacement preserve the new cwd**

`Broker.replaceMissingThread` continues using the persisted session working directory. Tests must prove a replaced thread receives the misc-project cwd and never the process working directory.

- [ ] **Step 5: Verify and commit**

```powershell
go test ./internal/conversation ./cmd/console -count=1
git add internal/conversation/service.go internal/conversation/service_test.go internal/conversation/broker.go internal/conversation/broker_test.go cmd/console/main.go
git commit -m "feat: route project threads through misc workspace"
```

### Task 3: Give formal task turns exactly two writable roots

**Files:**
- Modify: `internal/conversation/broker.go`
- Modify: `internal/conversation/broker_test.go`
- Modify: `internal/conversation/task_adapter.go`
- Modify: `internal/conversation/task_adapter_retry_test.go`
- Modify: `internal/codex/manifest.go`
- Modify: `internal/codex/manifest_test.go`

- [ ] **Step 1: Write failing sandbox tests**

Assert `turn/start.sandboxPolicy` for a formal task contains the canonical thread cwd and manifest `output_dir`, deduplicated and in that order. A normal chat turn contains only the session cwd. Reject a missing manifest, output path outside the task-bound managed project directory, relative path, symlink/reparse path, or a third browser-supplied root.

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/conversation ./internal/codex -run 'WritableRoots|FormalTaskSandbox' -count=1
```

- [ ] **Step 3: Replace the two-string execution config with a typed result**

```go
type FormalTaskExecutionConfig struct {
	Model string
	ReasoningEffort string
	WritableRoots []string
}

type FormalTaskExecutionConfigProvider interface {
	TaskExecutionConfig(context.Context, string) (FormalTaskExecutionConfig, error)
}
```

`TaskAdapter` reads the task-bound manifest path from `codex_tasks`, validates the manifest task ID and canonical `output_dir`, and returns `[session.WorkingDirectory, outputDir]`. `managedTurnSandboxPolicy` accepts a root slice and emits:

```go
map[string]any{"type":"workspaceWrite", "writableRoots": roots}
```

Inputs, Obsidian, media index, Baokuan service, and global Skill directories remain usable through absolute read paths; they are not added as writable roots.

- [ ] **Step 4: Verify and commit**

```powershell
go test ./internal/conversation ./internal/codex -count=1
git add internal/conversation/broker.go internal/conversation/broker_test.go internal/conversation/task_adapter.go internal/conversation/task_adapter_retry_test.go internal/codex/manifest.go internal/codex/manifest_test.go
git commit -m "feat: isolate formal task writable roots"
```

### Task 4: Name new and replacement threads in readable Chinese

**Files:**
- Modify: `internal/store/conversations.go`
- Modify: `internal/store/conversations_test.go`
- Modify: `internal/conversation/service.go`
- Modify: `internal/conversation/service_test.go`
- Modify: `internal/conversation/broker.go`
- Modify: `internal/conversation/broker_test.go`

- [ ] **Step 1: Write failing name tests**

Create an account/project fixture and assert `thread/name/set` follows `thread/start` with:

```text
[视频项目] 账号名｜项目短标题
```

Test trimming, control-character removal, 80-rune cap, missing account/project fallback, and the same naming call after missing-thread replacement. A naming RPC failure should archive an unpersisted new thread; it must not leave a nameless durable main session.

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/store ./internal/conversation -run 'ThreadName|ProjectThreadLabel' -count=1
```

- [ ] **Step 3: Add the label query and naming helper**

`ConversationRepository.ProjectThreadLabel(ctx,projectID)` joins `projects` and `accounts`. `projectThreadName(account,title)` returns a sanitized readable string. After `thread/start` and before `CreateSession`, call:

```go
rpc.Call(ctx, "thread/name/set", map[string]any{"threadId": threadID, "name": name}, &struct{}{})
```

On replacement, name the replacement before updating `chat_sessions.codex_thread_id`.

- [ ] **Step 4: Verify and commit**

```powershell
go test ./internal/store ./internal/conversation -count=1
git add internal/store/conversations.go internal/store/conversations_test.go internal/conversation/service.go internal/conversation/service_test.go internal/conversation/broker.go internal/conversation/broker_test.go
git commit -m "feat: name project Codex threads"
```

### Task 5: Persist routing migration status and atomic binding swaps

**Files:**
- Modify: `internal/store/migrations.go`
- Modify: `internal/store/migrations_test.go`
- Create: `internal/domain/routing.go`
- Modify: `internal/store/conversations.go`
- Modify: `internal/store/conversations_test.go`

- [ ] **Step 1: Write failing store tests**

Cover candidate discovery, active-turn/task exclusion, exact console ownership proof, atomic main binding swap, old/new thread IDs, migration attempt history, and no change when the expected old thread or working directory has changed concurrently.

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/store -run 'RoutingMigration|ReplaceProjectMainBinding' -count=1
```

- [ ] **Step 3: Add a durable migration ledger**

```sql
CREATE TABLE codex_routing_migrations (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES chat_sessions(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    old_thread_id TEXT NOT NULL,
    new_thread_id TEXT,
    target_working_directory TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('planned','forked','verified','switched','archived','skipped_active','failed')),
    failed_step TEXT,
    error_message TEXT,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    finished_at DATETIME
);
CREATE INDEX codex_routing_migrations_project_idx
ON codex_routing_migrations(project_id,created_at DESC);
```

Implement `ListRoutingCandidates`, `SessionHasActiveWork`, `BeginRoutingMigration`, `AdvanceRoutingMigration`, and `ReplaceProjectMainBinding`. The swap transaction checks `source='console'`, `kind='project'`, expected old thread ID/cwd, and zero active task/turn counts before updating `codex_thread_id`, `working_directory`, title, and updated time.

- [ ] **Step 4: Verify and commit**

```powershell
go test ./internal/store -count=1
git add internal/store/migrations.go internal/store/migrations_test.go internal/domain/routing.go internal/store/conversations.go internal/store/conversations_test.go
git commit -m "feat: persist Codex routing migrations"
```

### Task 6: Implement safe fork, verify, switch, and archive migration

**Files:**
- Create: `internal/conversation/routing.go`
- Create: `internal/conversation/routing_test.go`
- Modify: `internal/conversation/service.go`
- Modify: `internal/conversation/service_test.go`

- [ ] **Step 1: Write failing migration-service tests**

Test the exact RPC sequence for an idle session:

```text
thread/fork(cwd=target)
thread/read(new thread)
thread/name/set(new thread)
database binding swap
thread/read(new bound thread)
thread/archive(old thread)
```

Test active, queued, awaiting-input, and running sessions are `skipped_active`; fork/read/name/swap failures keep the old binding; post-swap archive failure records cleanup without reverting the valid new binding; repeated migration is idempotent; manually created non-console threads are ignored.

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/conversation -run 'RoutingMigration' -count=1
```

- [ ] **Step 3: Implement the migration state machine**

`RoutingMigrator.MigrateIdleProjectThreads(ctx)` takes no browser-provided thread IDs or paths. It reads the configured root and candidates from the repository, computes each UUID child cwd, then performs the sequence above. Fork params must include:

```go
map[string]any{"threadId": oldThreadID, "cwd": targetWorkingDirectory}
```

Verification requires the returned thread ID to be non-empty and `thread/read` to succeed. Only then name and swap. Archive is last and never deletes the original history. The currently active development conversation is not a candidate unless it is provably a console-owned `chat_sessions` project main mapping; manually created Desktop threads are never touched.

- [ ] **Step 4: Reconcile superseded console-owned threads**

After main sessions migrate, archive only old thread IDs proven by `chat_sessions.source='console_fork'` or completed `codex_tasks.codex_thread_id` that no current session owns. Reuse `thread_cleanup_intents` for failed archives. Do not inspect or mutate Codex private databases.

- [ ] **Step 5: Verify and commit**

```powershell
go test ./internal/conversation -count=1
git add internal/conversation/routing.go internal/conversation/routing_test.go internal/conversation/service.go internal/conversation/service_test.go
git commit -m "feat: migrate idle Codex task threads"
```

### Task 7: Expose authenticated routing settings and migration controls

**Files:**
- Create: `internal/httpapi/codex_routing.go`
- Create: `internal/httpapi/codex_routing_test.go`
- Modify: `internal/app/app.go`
- Modify: `internal/app/app_test.go`

- [ ] **Step 1: Write failing HTTP tests**

Cover:

```text
GET  /api/settings/codex-routing
PUT  /api/settings/codex-routing
POST /api/codex/routing/migrate
GET  /api/codex/routing/migration-status
```

GET returns configured root, detected name `杂项`, canonical/writable/available health, and pending/active/migrated/failed counts. PUT accepts only `task_project_root` and settings version. POST accepts only `{"operation":"migrate_idle_console_threads"}`; it rejects thread IDs, cwd, project IDs, and unknown fields.

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/httpapi ./internal/app -run 'CodexRouting' -count=1
```

- [ ] **Step 3: Implement narrow handlers**

Use existing auth/CSRF middleware. Return Chinese action-focused errors: unavailable root, active threads skipped, fork failure, verification failure, binding conflict, and archive queued for retry. The status endpoint returns the durable ledger, never raw RPC payloads.

- [ ] **Step 4: Verify and commit**

```powershell
go test ./internal/httpapi ./internal/app -count=1
git add internal/httpapi/codex_routing.go internal/httpapi/codex_routing_test.go internal/app/app.go internal/app/app_test.go
git commit -m "feat: expose Codex routing controls"
```

### Task 8: Add routing health and migration status to Settings

**Files:**
- Modify: `web/src/App.tsx`
- Modify: `web/src/App.test.tsx`
- Modify: `web/src/App.css`

- [ ] **Step 1: Write failing UI tests**

Assert Settings displays the task project root, detected project name, available/unavailable status, the exact future cwd pattern, counts of idle/active/migrated/failed threads, a “迁移空闲任务对话” button, and plain Chinese per-step failures. Active sessions must be shown as skipped, not failed or interrupted.

- [ ] **Step 2: Run and confirm failure**

```powershell
cd web
npx vitest run src/App.test.tsx -t 'Codex routing'
```

- [ ] **Step 3: Implement the settings section**

Use Lucide `FolderCog`, `CircleCheck`, `TriangleAlert`, and `RefreshCw`; no emoji. The confirmation copy states that only console-owned idle project threads are forked, original histories are archived only after verification, active work is untouched, and video assets stay in the console data root.

- [ ] **Step 4: Verify and commit**

```powershell
npx vitest run src/App.test.tsx
npm run lint
npm run build
git add src/App.tsx src/App.test.tsx src/App.css
git commit -m "feat: show Codex task routing health"
```

### Task 9: Run the non-disruptive package verification

**Files:**
- Modify: `README.md`
- Create: `docs/operations/codex-task-routing.md`

- [ ] **Step 1: Document operation and rollback boundaries**

Document the configured path, restart requirement, new-thread behavior, active-thread skip rule, migration sequence, archive retry, and the fact that changing the setting back affects only newly created/replaced threads unless another explicit migration is run.

- [ ] **Step 2: Run all deterministic tests**

```powershell
go test ./internal/settings ./internal/store ./internal/conversation ./internal/codex ./internal/httpapi ./internal/app ./cmd/console -count=1
cd web
npx vitest run
npm run lint
npm run build
```

Use fake App Server RPCs and temporary directories only. Do not migrate real threads during automated tests, do not stop the currently running console task, and do not launch Jianying or WeChat Video Channels.

- [ ] **Step 3: Commit documentation and generated web bundle**

```powershell
git add README.md docs/operations/codex-task-routing.md internal/webui/dist
git commit -m "docs: explain Codex task routing"
```

## Acceptance checklist

- New console project threads use `C:/Users/prepare/Documents/杂项/video-console-tasks/<project-id>` and appear under the Desktop “杂项” project by cwd classification.
- Each video project still has its own main thread; the misc project does not mean a shared conversation.
- Formal turns can write only the thread cwd and their task `output_dir`; all source assets remain absolute read-only inputs.
- Global Skills, Baokuan MCP, Obsidian, media index, and configured secrets remain available through existing configuration.
- New/replacement/migrated threads receive `[视频项目] 账号名｜项目短标题` through `thread/name/set`.
- Only idle console-owned project threads migrate; active, queued, running, and awaiting-input work is untouched.
- Fork/read/name/swap failure keeps the old binding; archive happens only after successful verification and swap.
- The implementation never modifies Codex private databases or rollout files and never accepts arbitrary thread IDs/paths from the browser.
- Automated tests use fake RPCs and do not affect real running tasks or external applications.
