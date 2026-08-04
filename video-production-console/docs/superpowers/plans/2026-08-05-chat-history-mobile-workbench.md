# Chat History and Mobile Workbench Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the recent local Codex history with dual entry, a general-purpose chat page, persistent URL navigation, and a mobile-first workbench shell.

**Architecture:** Discover history through the App Server thread API and expose only sanitized metadata through the console. Split the monolithic React file into an application shell, pages, API client, and conversation feature; use the browser History API so task/chat pages survive refresh without adding a router dependency.

**Tech Stack:** Go HTTP, App Server thread APIs, React 19, TypeScript, TanStack Query, Vitest, CSS custom properties and responsive media queries.

---

## File map

- Create `internal/history/service.go`: filtering, source mapping, deduplication, and active-owner rules.
- Create `internal/httpapi/history.go`: list, resume, and fork endpoints.
- Create `web/src/lib/api.ts`, `web/src/lib/route.ts`, `web/src/types/api.ts`: shared client and URL state.
- Create `web/src/app/AppShell.tsx`, `web/src/app/AuthGate.tsx`: persistent shell and authentication.
- Create `web/src/pages/HomePage.tsx`, `ProjectsPage.tsx`, `ConversationsPage.tsx`, `SettingsPage.tsx`.
- Create `web/src/features/conversations/*`: list, transcript, composer, and history cards.
- Create `web/src/styles/tokens.css`, `shell.css`, `mobile.css`: visual system and responsive layout.
- Reduce `web/src/App.tsx` to top-level composition.

### Task 1: Add the recent-history setting and adapter

**Files:**
- Create: `internal/history/service.go`
- Create: `internal/history/service_test.go`
- Modify: `internal/domain/settings.go`
- Modify: `internal/settings/service.go`
- Modify: `internal/settings/service_test.go`
- Modify: `internal/store/settings.go`
- Modify: `schemas/settings.schema.json`
- Modify: `schemas/settings_schema_test.go`

- [ ] **Step 1: Write filtering and setting tests**

```go
func TestRecentHistoryFiltersAndDeduplicates(t *testing.T) {
	threads := []ThreadSummary{
		{ID:"desktop-1", Source:"vscode", Title:"桌面会话", Recency: time.Unix(30,0)},
		{ID:"sub-1", Source:"subagent", Title:"子代理", Recency: time.Unix(40,0)},
		{ID:"archived-1", Source:"cli", Title:"归档", Archived:true, Recency: time.Unix(50,0)},
		{ID:"owned-1", Source:"exec", Title:"控制台任务", Recency: time.Unix(60,0)},
	}
	got := FilterRecent(threads, map[string]bool{"owned-1":true}, 10)
	if len(got) != 1 || got[0].ID != "desktop-1" { t.Fatalf("got=%+v", got) }
}
```

Test `codex_history_limit` defaults to 10, accepts 5 and 50, and rejects 4 or 51 without clearing other settings.

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/history ./internal/settings ./schemas -run 'History|Recent' -count=1
```

- [ ] **Step 3: Implement the adapter**

```go
type ThreadSource interface {
	List(context.Context, int) ([]ThreadSummary, error)
	Read(context.Context, string) (ThreadDetail, error)
	Resume(context.Context, string) error
	Fork(context.Context, string) (string, error)
}
type ThreadSummary struct {
	ID, Title, Preview, Source, Model, ReasoningEffort string
	Archived, Active bool
	Recency time.Time
}
```

Map `vscode` to `desktop`, `cli` to `cli`, and `exec` to `task`. Remove subagents, archived threads, blank conversations, and thread IDs already owned by `chat_sessions`. Sort descending and apply the configured limit after filtering. Use App Server `thread/list` and `thread/read`; do not modify Codex SQLite or rollout files.

- [ ] **Step 4: Verify and commit**

```powershell
go test ./internal/history ./internal/settings ./schemas -count=1
git add internal/history/service.go internal/history/service_test.go internal/domain/settings.go internal/settings/service.go internal/settings/service_test.go internal/store/settings.go schemas/settings.schema.json schemas/settings_schema_test.go
git commit -m "feat: discover recent Codex history"
```

### Task 2: Add history list, resume, and fork APIs

**Files:**
- Create: `internal/httpapi/history.go`
- Create: `internal/httpapi/history_test.go`
- Modify: `internal/app/app.go`
- Modify: `internal/app/app_test.go`

- [ ] **Step 1: Write endpoint tests**

Assert list clamps to the configured limit, resume preserves the original thread ID, fork creates a different thread ID, and an active thread owned by another runtime returns HTTP 409.

```go
request := httptest.NewRequest(http.MethodPost, "/api/codex/history/desktop-1/resume", nil)
response := httptest.NewRecorder()
handler.ServeHTTP(response, request)
if response.Code != http.StatusCreated { t.Fatalf("status=%d body=%s", response.Code, response.Body.String()) }
```

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/httpapi ./internal/app -run History -count=1
```

- [ ] **Step 3: Implement the dual-entry API**

Mount:

```text
GET  /api/codex/history?limit=10&source=desktop|cli|task
GET  /api/codex/history/{thread_id}
POST /api/codex/history/{thread_id}/resume
POST /api/codex/history/{thread_id}/fork
```

`resume` creates a `chat_sessions` mapping with the same Codex thread ID. `fork` asks App Server for a new thread and creates a mapping to it. When `ThreadSummary.Active` is true and the lease owner is not this Manager, return `409 thread_active_elsewhere` with actions `wait` and `fork`; never start a second writer.

- [ ] **Step 4: Verify and commit**

```powershell
go test ./internal/httpapi ./internal/app -count=1
git add internal/httpapi/history.go internal/httpapi/history_test.go internal/app/app.go internal/app/app_test.go
git commit -m "feat: add Codex history dual entry"
```

### Task 3: Split the React application without changing behavior

**Files:**
- Create: `web/src/types/api.ts`
- Create: `web/src/lib/api.ts`
- Create: `web/src/lib/route.ts`
- Create: `web/src/app/AuthGate.tsx`
- Create: `web/src/app/AppShell.tsx`
- Create: `web/src/pages/ProjectsPage.tsx`
- Create: `web/src/pages/SettingsPage.tsx`
- Modify: `web/src/App.tsx`
- Modify: `web/src/App.test.tsx`

- [ ] **Step 1: Add characterization tests**

Keep the current login, account filter, project creation, project deletion, task model override, idea creation, and settings tests. Add a route test:

```tsx
test("projects route survives a reload", async () => {
  window.history.replaceState({}, "", "/projects");
  render(<App />);
  expect(await screen.findByRole("navigation", { name: "主导航" })).toBeTruthy();
  expect(screen.getByRole("link", { name: "视频项目" }).getAttribute("aria-current")).toBe("page");
});
```

- [ ] **Step 2: Run the characterization suite**

```powershell
Set-Location web
npx vitest run src/App.test.tsx
Set-Location ..
```

Expected before the route implementation: the new test fails while existing tests pass.

- [ ] **Step 3: Extract modules and use a minimal URL router**

`web/src/lib/route.ts` must expose:

```ts
export type Route = { page: "home" | "projects" | "conversations" | "tasks" | "settings"; id?: string };
export function parseRoute(pathname: string): Route;
export function navigate(path: string): void {
  window.history.pushState({}, "", path);
  window.dispatchEvent(new PopStateEvent("popstate"));
}
```

Move API types out of `App.tsx`, move authenticated fetch/CSRF handling into `api.ts`, move the existing project/idea behavior into `ProjectsPage`, and move settings UI into `SettingsPage`. `App.tsx` becomes:

```tsx
export default function App() {
  return <AuthGate><AppShell /></AuthGate>;
}
```

Do not redesign behavior in this task; it is a mechanical boundary change protected by existing tests.
Reserve `/tasks/{taskID}` in `parseRoute` for the semantic-results plan so refresh and back/forward navigation already understand task URLs before `TaskPage` is added.

- [ ] **Step 4: Verify and commit**

```powershell
Set-Location web
npx vitest run src/App.test.tsx
npm run build
Set-Location ..
git add web/src/types/api.ts web/src/lib/api.ts web/src/lib/route.ts web/src/app/AuthGate.tsx web/src/app/AppShell.tsx web/src/pages/ProjectsPage.tsx web/src/pages/SettingsPage.tsx web/src/App.tsx web/src/App.test.tsx
git commit -m "refactor: split the console application shell"
```

### Task 4: Build the home page and history cards

**Files:**
- Create: `web/src/pages/HomePage.tsx`
- Create: `web/src/features/conversations/HistoryList.tsx`
- Create: `web/src/features/conversations/HistoryCard.tsx`
- Create: `web/src/features/conversations/HistoryList.test.tsx`
- Modify: `web/src/app/AppShell.tsx`

- [ ] **Step 1: Write history interaction tests**

```tsx
test("history offers resume and fork", async () => {
  render(<HistoryList items={[desktopThread]} onResume={resume} onFork={fork} />);
  fireEvent.click(screen.getByRole("button", { name: "继续原会话 桌面会话" }));
  expect(resume).toHaveBeenCalledWith("desktop-1");
  fireEvent.click(screen.getByRole("button", { name: "复制为新对话 桌面会话" }));
  expect(fork).toHaveBeenCalledWith("desktop-1");
});
```

Also test source badges and the `active_elsewhere` conflict message.

- [ ] **Step 2: Run and confirm failure**

```powershell
Set-Location web
npx vitest run src/features/conversations/HistoryList.test.tsx
Set-Location ..
```

- [ ] **Step 3: Implement the action-first home page**

The first row contains `开始选题`, `已有爆款原文`, and `新建 Codex 对话`. Below it render active/waiting/failed tasks, recent projects, console conversations, and recent history. Cards load only metadata. Resume/fork success navigates to `/conversations/{sessionID}`; HTTP 409 displays “正在桌面端运行” with wait/fork actions.

- [ ] **Step 4: Verify and commit**

```powershell
Set-Location web
npx vitest run src/features/conversations/HistoryList.test.tsx src/App.test.tsx
Set-Location ..
git add web/src/pages/HomePage.tsx web/src/features/conversations/HistoryList.tsx web/src/features/conversations/HistoryCard.tsx web/src/features/conversations/HistoryList.test.tsx web/src/app/AppShell.tsx
git commit -m "feat: add Codex history to the home page"
```

### Task 5: Build the persistent conversation page and steering composer

**Files:**
- Create: `web/src/pages/ConversationsPage.tsx`
- Create: `web/src/features/conversations/ConversationList.tsx`
- Create: `web/src/features/conversations/Transcript.tsx`
- Create: `web/src/features/conversations/Composer.tsx`
- Create: `web/src/features/conversations/NewConversationDialog.tsx`
- Create: `web/src/features/conversations/ConversationPage.test.tsx`
- Modify: `web/src/app/AppShell.tsx`

- [ ] **Step 1: Write composer state tests**

```tsx
test.each([
  ["running", "发送引导", "auto"],
  ["idle", "继续对话", "auto"],
  ["awaiting_input", "回复 Codex", "auto"],
])("%s chooses the correct label", (status, label) => {
  render(<Composer status={status} onSend={send} />);
  expect(screen.getByRole("button", { name: label })).toBeTruthy();
});
```

Test that “排队到下一轮” sends `delivery:"queue"`, the composer remains visible while running, and an accepted steering message shows “已加入当前任务”.
Also test that new general chat selects only a configured workspace root, installed Skill names, a model, and reasoning effort; it must never expose a free-form Skill path or arbitrary filesystem path field.

- [ ] **Step 2: Run and confirm failure**

```powershell
Set-Location web
npx vitest run src/features/conversations/ConversationPage.test.tsx
Set-Location ..
```

- [ ] **Step 3: Implement chat behavior**

POST messages with a stable client key:

```ts
await api.post(`/api/chat/sessions/${session.id}/messages`, {
  text,
  delivery,
  client_key: crypto.randomUUID(),
});
```

Render user and assistant messages as chat bubbles. Render delivery states beside user messages. Keep the composer fixed at the bottom. New/delete conversation controls live in the list; delete requires confirmation and returns to `/conversations`. The page route, selected session ID, and scroll anchor survive refresh.

`NewConversationDialog` loads `codex_workspace_roots`, installed Skill snapshots, and model defaults from the authenticated APIs, then posts the exact `CreateSessionInput` from the App Server plan. On mobile it is a full-height sheet; on desktop it is an in-page dialog.

- [ ] **Step 4: Verify and commit**

```powershell
Set-Location web
npx vitest run src/features/conversations/ConversationPage.test.tsx src/App.test.tsx
Set-Location ..
git add web/src/pages/ConversationsPage.tsx web/src/features/conversations/ConversationList.tsx web/src/features/conversations/Transcript.tsx web/src/features/conversations/Composer.tsx web/src/features/conversations/NewConversationDialog.tsx web/src/features/conversations/ConversationPage.test.tsx web/src/app/AppShell.tsx
git commit -m "feat: add real-time Codex conversation pages"
```

### Task 6: Apply the video-workbench visual system and mobile navigation

**Files:**
- Create: `web/src/styles/tokens.css`
- Create: `web/src/styles/shell.css`
- Create: `web/src/styles/mobile.css`
- Modify: `web/src/main.tsx`
- Modify: `web/src/app/AppShell.tsx`
- Modify: `web/src/App.css`
- Create: `web/src/app/AppShell.test.tsx`

- [ ] **Step 1: Write responsive structure tests**

Assert semantic structure rather than pixels: one main navigation, mobile navigation labels, a fixed composer region, accessible focus order, and no click-away modal for task/conversation pages.

- [ ] **Step 2: Run and confirm failure**

```powershell
Set-Location web
npx vitest run src/app/AppShell.test.tsx
Set-Location ..
```

- [ ] **Step 3: Implement tokens and layouts**

Define these exact tokens:

```css
:root {
  --workbench:#f3f6f7; --ink:#17212b; --slate:#314454;
  --record:#e85d3f; --ready:#238469; --amber:#c78a2c;
  --line:#d7dfe3; --panel:#ffffff; --radius:8px;
  font-family:"HarmonyOS Sans SC","Microsoft YaHei UI",sans-serif;
}
.utility { font-family:"Cascadia Mono",Consolas,monospace; }
```

Desktop uses left navigation and the conversation three-column layout. At `max-width: 760px`, use bottom navigation, one-column pages, safe-area padding, 44px minimum controls, and context/list drawers. Only the running playhead pulses; disable it under `prefers-reduced-motion`.

- [ ] **Step 4: Verify and commit**

```powershell
Set-Location web
npx vitest run
npm run build
Set-Location ..
git add web/src/styles/tokens.css web/src/styles/shell.css web/src/styles/mobile.css web/src/main.tsx web/src/app/AppShell.tsx web/src/App.css web/src/app/AppShell.test.tsx
git commit -m "feat: add the responsive video workbench shell"
```

### Task 7: Add the history-limit setting and program regression

**Files:**
- Modify: `web/src/pages/SettingsPage.tsx`
- Modify: `web/src/types/api.ts`
- Create: `web/src/pages/SettingsPage.test.tsx`
- Modify: `README.md`

- [ ] **Step 1: Write the setting test**

Assert the history value loads as 10, accepts 5—50, and saves without clearing model, workspace roots, or integration settings. Add a workspace-root test that rejects a relative path and preserves the previously valid root list.

- [ ] **Step 2: Implement the control**

Use a number input labeled `本机历史显示条数`, `min=5`, `max=50`, and explain that subagents and archived conversations are excluded. Add `Codex 可用工作目录` as a removable list of canonical absolute directories; these values populate the new-chat selector and are validated again by the server.

- [ ] **Step 3: Run all verification**

```powershell
Set-Location web
npx vitest run
npm run build
npm run lint
Set-Location ..
go test ./...
```

Expected: all tests/build/lint pass. No browser automation is required for this plan.

- [ ] **Step 4: Commit**

```powershell
git add web/src/pages/SettingsPage.tsx web/src/pages/SettingsPage.test.tsx web/src/types/api.ts README.md internal/webui/dist
git diff --cached --name-only
git commit -m "feat: finish Codex history and mobile chat settings"
```
