# Semantic Task Results Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace unreadable raw task events with Chinese stage progress, durable chat messages, visible artifacts, safe directory results, and a project-scoped production timeline.

**Architecture:** Keep raw CLI/App Server events as diagnostics, then project them deterministically into a small semantic event vocabulary stored in `semantic_events`. The default task page consumes semantic events and result APIs; raw JSONL is paginated behind a diagnostic disclosure.

**Tech Stack:** Go, SQLite, existing WebSocket hub, React, TypeScript, Vitest.

---

## File map

- Create `internal/progress/projector.go`: deterministic raw-to-semantic mapping.
- Create `internal/store/semantic_events.go`: sequence-based event persistence.
- Create `internal/realtime/conversations.go`: replayable semantic-event WebSocket.
- Create `internal/httpapi/task_results.go`: artifacts, diagnostics, and result views.
- Extend `internal/httpapi/assets.go`, `internal/assets/service.go`: directory manifests.
- Create `web/src/features/tasks/*`: task chat, phases, artifacts, errors, diagnostics.
- Create `web/src/features/projects/ProductionRail.tsx`: actual project production timeline.

### Task 1: Define and test semantic event projection

**Files:**
- Create: `internal/domain/progress.go`
- Create: `internal/progress/projector.go`
- Create: `internal/progress/projector_test.go`

- [ ] **Step 1: Write table-driven projector tests**

```go
func TestProjectorMapsKnownEvents(t *testing.T) {
	tests := []struct{ action domain.TaskAction; method, raw, wantKind, wantText string }{
		{domain.ActionTopicBrainstorm, "item/started", `{"item":{"type":"mcp_tool_call","server":"baokuan"}}`, "phase_progress", "正在检索爆款库素材"},
		{domain.ActionMontageExecute, "item/started", `{"item":{"type":"command_execution"}}`, "phase_progress", "正在生成混剪草稿"},
		{domain.ActionMontageExecute, "turn/completed", `{}`, "turn_completed", "Codex 处理已结束"},
	}
	for _, tt := range tests {
		got := Project(Input{Action:tt.action, Method:tt.method, RawJSON:tt.raw})
		if got.Kind != tt.wantKind || got.DisplayText != tt.wantText { t.Fatalf("got=%+v", got) }
	}
}
```

Add tests proving command text, secrets, full raw JSON, and model reasoning are never copied into `DisplayText`.

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/progress -count=1
```

- [ ] **Step 3: Implement the vocabulary and coalescing key**

```go
type SemanticEventKind string
const (
	SemanticPhaseStarted SemanticEventKind = "phase_started"
	SemanticPhaseProgress SemanticEventKind = "phase_progress"
	SemanticAssistantMessage SemanticEventKind = "assistant_message"
	SemanticQuestion SemanticEventKind = "user_question"
	SemanticApproval SemanticEventKind = "approval_required"
	SemanticArtifact SemanticEventKind = "artifact_ready"
	SemanticWarning SemanticEventKind = "warning"
	SemanticFailure SemanticEventKind = "failure"
	SemanticTurnCompleted SemanticEventKind = "turn_completed"
)
```

`Project` uses action, App Server method/item type, legacy event kind, known tool name, and status. Unknown events return `Visible=false` instead of inventing progress. Use `CoalesceKey=taskID+phase+kind` for high-frequency deltas.

- [ ] **Step 4: Verify and commit**

```powershell
go test ./internal/progress -count=1
git add internal/domain/progress.go internal/progress/projector.go internal/progress/projector_test.go
git commit -m "feat: project Codex events into user progress"
```

### Task 2: Persist and stream semantic events

**Files:**
- Create: `internal/store/semantic_events.go`
- Create: `internal/store/semantic_events_test.go`
- Create: `internal/realtime/conversations.go`
- Create: `internal/realtime/conversations_test.go`
- Modify: `internal/codex/runner.go`
- Modify: `internal/codex/runner_test.go`
- Modify: `internal/conversation/broker.go`
- Modify: `internal/conversation/broker_test.go`

- [ ] **Step 1: Write persistence and replay tests**

Assert monotonic per-session sequence numbers, replacement of an unfinished coalesced progress row, append of terminal events, and WebSocket replay only after the supplied cursor. Add handshake tests that reject an unauthenticated request, a foreign `Origin`, and a missing/invalid CSRF-derived WebSocket token; accept the same-origin authenticated request and redact all replayed raw fields.

```go
first, _ := repo.Append(ctx, sessionID, SemanticWrite{Kind:"phase_progress", CoalesceKey:"search", DisplayText:"正在检索"})
second, _ := repo.Append(ctx, sessionID, SemanticWrite{Kind:"phase_progress", CoalesceKey:"search", DisplayText:"已检索 20 条"})
if first.Sequence != second.Sequence { t.Fatalf("progress should coalesce") }
```

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/store ./internal/realtime -run Semantic -count=1
```

- [ ] **Step 3: Implement transaction and broadcast ordering**

Persist before broadcast. Terminal kinds clear the relevant coalesce key. App Server Broker and Legacy Runner both call the same Projector adapter after they persist raw events. Mount semantic replay at:

```text
GET /api/chat/sessions/{id}/events?after=<sequence>
```

Retain the existing task raw-event WebSocket for old clients until the new UI ships.

- [ ] **Step 4: Verify and commit**

```powershell
go test ./internal/store ./internal/realtime ./internal/codex ./internal/conversation -count=1
git add internal/store/semantic_events.go internal/store/semantic_events_test.go internal/realtime/conversations.go internal/realtime/conversations_test.go internal/codex/runner.go internal/codex/runner_test.go internal/conversation/broker.go internal/conversation/broker_test.go
git commit -m "feat: stream semantic Codex progress"
```

### Task 3: Expose task artifacts, result cards, and paginated diagnostics

**Files:**
- Create: `internal/httpapi/task_results.go`
- Create: `internal/httpapi/task_results_test.go`
- Modify: `internal/store/tasks.go`
- Modify: `internal/store/tasks_test.go`
- Modify: `internal/httpapi/tasks.go`
- Modify: `internal/app/app.go`

- [ ] **Step 1: Write result API tests**

```go
request := httptest.NewRequest(http.MethodGet, "/api/tasks/"+taskID+"/artifacts", nil)
response := httptest.NewRecorder()
handler.ServeHTTP(response, request)
if response.Code != http.StatusOK { t.Fatalf("status=%d", response.Code) }
if strings.Contains(response.Body.String(), `"raw_json"`) { t.Fatal("artifact response leaked diagnostics") }
```

Test artifact ownership, task result summary, asset outputs, diagnostics `limit` 1—200, `after` sequence, and redaction of environment values.

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/httpapi ./internal/store -run 'TaskArtifacts|TaskDiagnostics|TaskResultView' -count=1
```

- [ ] **Step 3: Implement separate result and diagnostic views**

Mount:

```text
GET /api/tasks/{id}/artifacts
GET /api/tasks/{id}/result
GET /api/tasks/{id}/diagnostics?after=0&limit=100
```

Artifact responses contain ID, kind, filename, MIME, size, created time, and a controlled content URL. Result responses contain task status, completion phase, formal assets, warnings, and error action. Diagnostics contain paginated raw events and command snapshot only after redaction.

- [ ] **Step 4: Verify and commit**

```powershell
go test ./internal/httpapi ./internal/store -count=1
git add internal/httpapi/task_results.go internal/httpapi/task_results_test.go internal/store/tasks.go internal/store/tasks_test.go internal/httpapi/tasks.go internal/app/app.go
git commit -m "feat: expose task results and diagnostics"
```

### Task 4: Add safe directory-asset manifests

**Files:**
- Modify: `internal/assets/service.go`
- Modify: `internal/assets/service_test.go`
- Modify: `internal/httpapi/assets.go`
- Modify: `internal/httpapi/assets_test.go`

- [ ] **Step 1: Write path-boundary tests**

Test a valid `mix_draft` directory, a file asset, a symlink/reparse escape, more than 5,000 entries, and a directory outside `data_root`. The response must use relative paths only.

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/assets ./internal/httpapi -run DirectoryManifest -count=1
```

- [ ] **Step 3: Implement manifest and desktop-open metadata**

```go
type DirectoryEntry struct { Path string `json:"path"`; Kind string `json:"kind"`; Size int64 `json:"size"` }
type DirectoryManifest struct { AssetID, Filename string; Entries []DirectoryEntry; Truncated bool }
```

Mount `GET /api/assets/{id}/directory-manifest`. Resolve the database asset, require `StorageDirectory`, reject symlinks/reparse escapes, sort relative entries, and cap at 5,000. Inject a `DirectoryRootResolver`: ordinary engineering directories must remain inside the managed project root, while a formal `mix_draft` may resolve inside the configured canonical `jianying_root` only after the montage registration-integrity plan records it. Do not pass directories to `http.ServeContent`, and never accept a filesystem path from the browser.

- [ ] **Step 4: Verify and commit**

```powershell
go test ./internal/assets ./internal/httpapi -count=1
git add internal/assets/service.go internal/assets/service_test.go internal/httpapi/assets.go internal/httpapi/assets_test.go
git commit -m "feat: add safe directory asset manifests"
```

### Task 5: Build the task conversation page

**Files:**
- Create: `web/src/pages/TaskPage.tsx`
- Create: `web/src/features/tasks/TaskHeader.tsx`
- Create: `web/src/features/tasks/PhaseCard.tsx`
- Create: `web/src/features/tasks/ArtifactCard.tsx`
- Create: `web/src/features/tasks/ErrorCard.tsx`
- Create: `web/src/features/tasks/DiagnosticsPanel.tsx`
- Create: `web/src/features/tasks/TaskPage.test.tsx`
- Modify: `web/src/app/AppShell.tsx`

- [ ] **Step 1: Write task-page tests**

```tsx
test("task page prioritizes semantic progress and hides raw logs", async () => {
  render(<TaskPage taskId="task-1" />);
  expect(await screen.findByText("正在检索爆款库素材")).toBeTruthy();
  expect(screen.queryByText(/item\.completed/)).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "展开技术详情" }));
  expect(await screen.findByText(/item\.completed/)).toBeTruthy();
});
```

Test artifact preview, directory manifest display, failure action, refresh persistence, and no click-away dismissal.

- [ ] **Step 2: Run and confirm failure**

```powershell
Set-Location web
npx vitest run src/features/tasks/TaskPage.test.tsx
Set-Location ..
```

- [ ] **Step 3: Implement the dedicated route**

Use `/tasks/{taskID}`. Render user/assistant messages, semantic phase cards, questions/approvals, artifact cards, result/error cards, and the shared conversation Composer. Load diagnostics only after the user expands the panel; paginate with the returned cursor. Preserve the scroll anchor in `sessionStorage` by task ID.

- [ ] **Step 4: Verify and commit**

```powershell
Set-Location web
npx vitest run src/features/tasks/TaskPage.test.tsx src/App.test.tsx
Set-Location ..
git add web/src/pages/TaskPage.tsx web/src/features/tasks/TaskHeader.tsx web/src/features/tasks/PhaseCard.tsx web/src/features/tasks/ArtifactCard.tsx web/src/features/tasks/ErrorCard.tsx web/src/features/tasks/DiagnosticsPanel.tsx web/src/features/tasks/TaskPage.test.tsx web/src/app/AppShell.tsx
git commit -m "feat: present Codex tasks as conversations"
```

### Task 6: Clarify project scope with the production rail

**Files:**
- Create: `web/src/features/projects/ProductionRail.tsx`
- Create: `web/src/features/projects/ProjectAssets.tsx`
- Create: `web/src/features/projects/ProductionRail.test.tsx`
- Modify: `web/src/pages/ProjectsPage.tsx`
- Modify: `web/src/styles/shell.css`
- Modify: `web/src/styles/mobile.css`

- [ ] **Step 1: Write project ownership tests**

Assert five real stages, project-only assets, a separately labeled inherited account background, and stage detail that states missing/current/next action.

- [ ] **Step 2: Run and confirm failure**

```powershell
Set-Location web
npx vitest run src/features/projects/ProductionRail.test.tsx
Set-Location ..
```

- [ ] **Step 3: Implement the rail and scoped assets**

Stages are exactly `选题`, `文案`, `配音与字幕`, `混剪草稿`, `成片`. Derive their state from the project requirements response, not from tab position. Clicking a stage scrolls to the matching assets and tasks. Display the account background under `继承自账号`; never mix it into project asset history.

- [ ] **Step 4: Run full verification and commit**

```powershell
Set-Location web
npx vitest run
npm run build
npm run lint
Set-Location ..
go test ./...
go vet ./...
git add web/src/features/projects/ProductionRail.tsx web/src/features/projects/ProjectAssets.tsx web/src/features/projects/ProductionRail.test.tsx web/src/pages/ProjectsPage.tsx web/src/styles/shell.css web/src/styles/mobile.css internal/webui/dist
git commit -m "feat: add project production progress and scoped assets"
```

Expected: semantic progress is the default experience; raw logs remain available but never flood the task page.
