# Codex Task Model Selection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add safe console defaults and per-task overrides for the Codex CLI model and reasoning effort, persist the resolved values, reuse them on resume, and show them in the web UI.

**Architecture:** The dependency-neutral `internal/taskmodel` package owns the validated model-selection value object, while `internal/codex` owns CLI argv generation. `internal/settings` owns stored defaults and resolves optional request overrides. Every queued `domain.CodexTask` stores the resolved model fields, so the scheduler and resume path never depend on later settings changes. HTTP handlers accept optional overrides and the React UI exposes a shared task configuration control.

**Tech Stack:** Go 1.25, `net/http`, SQLite via `modernc.org/sqlite`, React 19, TypeScript 6, Vitest, Testing Library, Vite.

---

## File map

- Create `internal/taskmodel/model_selection.go`: constants, value type, validation, and override resolution without importing settings, store, HTTP, or CLI packages.
- Create `internal/taskmodel/model_selection_test.go`: model name and reasoning effort table tests.
- Modify `internal/domain/settings.go`: add public default fields.
- Modify `internal/settings/service.go`: store, load, default, validate, and resolve task model settings.
- Modify `internal/settings/service_test.go`: defaulting, persistence, partial-update, and invalid-value tests.
- Modify `internal/httpapi/settings_test.go`: HTTP JSON round-trip coverage.
- Modify `schemas/settings.schema.json`: public settings contract.
- Modify `internal/domain/models.go`: persist resolved fields on `CodexTask`.
- Modify `internal/store/migrations.go`: additive migration for historical and fresh databases.
- Modify `internal/store/migrations_test.go`: migration backfill coverage.
- Modify `internal/store/tasks.go`: insert, get, and list model fields.
- Create `internal/store/tasks_model_test.go`: repository round-trip coverage.
- Modify `internal/codex/command.go`: add model fields to `Config` and explicit safe argv.
- Modify `internal/codex/command_test.go`: exact exec and resume command tests.
- Modify `cmd/console/main.go`: copy persisted task values into both command factories.
- Modify `cmd/console/main_test.go`: factory/resume regression tests.
- Create `internal/httpapi/task_models.go`: narrow settings resolver interface and request helper.
- Modify `internal/httpapi/tasks.go`: accept overrides, resolve before enqueue, expose actual values.
- Create `internal/httpapi/tasks_model_test.go`: defaults, partial/full override, 400, and view tests.
- Modify `internal/httpapi/task_manifest_test.go`: pass the explicit resolver to the changed task-handler constructor.
- Modify `internal/httpapi/ideas.go`: use the same request and resolver contract.
- Modify `internal/httpapi/ideas_test.go`: selected model persistence tests.
- Modify `internal/app/app.go`: inject the settings service into both handlers.
- Modify `web/src/App.tsx`: types, settings controls, shared task config, API payloads, and task metadata.
- Modify `web/src/App.test.tsx`: settings/default/override/resume display interaction tests.
- Modify `web/src/App.css` and `web/src/idea.css`: compact configuration control styling.
- Rebuild `internal/webui/dist/*`: embed the verified production frontend.

## Task 1: Validated model selection and settings defaults

**Files:**
- Create: `internal/taskmodel/model_selection.go`
- Create: `internal/taskmodel/model_selection_test.go`
- Modify: `internal/domain/settings.go`
- Modify: `internal/settings/service.go`
- Modify: `internal/settings/service_test.go`
- Modify: `internal/httpapi/settings_test.go`
- Modify: `schemas/settings.schema.json`

- [ ] **Step 1: Write failing validation and defaulting tests**

Add table tests that require exact normalization and rejection behavior:

```go
func TestNormalizeSelection(t *testing.T) {
	got, err := Normalize(Selection{Model: "  gpt-5.6-sol  ", ReasoningEffort: " HIGH "})
	if err != nil {
		t.Fatal(err)
	}
	if got != (Selection{Model: "gpt-5.6-sol", ReasoningEffort: "high"}) {
		t.Fatalf("selection=%+v", got)
	}
}

func TestNormalizeSelectionRejectsUnsafeValues(t *testing.T) {
	tests := []Selection{
		{Model: "", ReasoningEffort: "medium"},
		{Model: "-danger", ReasoningEffort: "medium"},
		{Model: "gpt model", ReasoningEffort: "medium"},
		{Model: "gpt;whoami", ReasoningEffort: "medium"},
		{Model: strings.Repeat("a", 129), ReasoningEffort: "medium"},
		{Model: "gpt-5.6-sol", ReasoningEffort: "extreme"},
	}
	for _, input := range tests {
		if _, err := Normalize(input); err == nil {
			t.Fatalf("accepted %+v", input)
		}
	}
}
```

Extend `newSettingsTestService` and `newSettingsHTTPTest` fixtures with:

```go
CodexDefaultModel: "gpt-5.6-sol",
CodexDefaultReasoningEffort: "medium",
```

Add a service test that seeds a repository without the new keys and asserts `Get()` returns those defaults, then saves `gpt-5.6-terra + high` and asserts it round-trips.

- [ ] **Step 2: Run focused tests and verify failure**

Run:

```powershell
go test ./internal/taskmodel ./internal/settings ./internal/httpapi -run 'Selection|CodexDefault|SettingsPublic' -count=1
```

Expected: FAIL because `taskmodel.Selection`, the new settings fields, and defaulting logic do not exist.

- [ ] **Step 3: Implement the model value object**

Create `internal/taskmodel/model_selection.go` with this public contract:

```go
package taskmodel

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	DefaultModel = "gpt-5.6-sol"
	DefaultReasoningEffort = "medium"
)

var validModelName = regexp.MustCompile(`^[A-Za-z0-9_./:][A-Za-z0-9_.:/-]{0,127}$`)

var validReasoningEfforts = map[string]struct{}{
	"low": {}, "medium": {}, "high": {}, "xhigh": {}, "max": {}, "ultra": {},
}

type Selection struct {
	Model string `json:"model"`
	ReasoningEffort string `json:"reasoning_effort"`
}

func Normalize(value Selection) (Selection, error) {
	value.Model = strings.TrimSpace(value.Model)
	value.ReasoningEffort = strings.ToLower(strings.TrimSpace(value.ReasoningEffort))
	if !validModelName.MatchString(value.Model) {
		return Selection{}, fmt.Errorf("invalid Codex model")
	}
	if _, ok := validReasoningEfforts[value.ReasoningEffort]; !ok {
		return Selection{}, fmt.Errorf("invalid Codex reasoning effort")
	}
	return value, nil
}

func Resolve(defaults, override Selection) (Selection, error) {
	if strings.TrimSpace(override.Model) != "" {
		defaults.Model = override.Model
	}
	if strings.TrimSpace(override.ReasoningEffort) != "" {
		defaults.ReasoningEffort = override.ReasoningEffort
	}
	return Normalize(defaults)
}
```

- [ ] **Step 4: Add settings fields and service behavior**

Add to `domain.PublicSettings`:

```go
CodexDefaultModel           string `json:"codex_default_model"`
CodexDefaultReasoningEffort string `json:"codex_default_reasoning_effort"`
```

Update `publicValues` and `publicFromValues`. In `publicFromValues`, replace missing values before constructing the result:

```go
model := values["codex_default_model"]
if strings.TrimSpace(model) == "" {
	model = taskmodel.DefaultModel
}
effort := values["codex_default_reasoning_effort"]
if strings.TrimSpace(effort) == "" {
	effort = taskmodel.DefaultReasoningEffort
}
```

Call `taskmodel.Normalize` from `validatePublic`. Add this method for HTTP handlers:

```go
func (s *Service) ResolveTaskModel(ctx context.Context, override taskmodel.Selection) (taskmodel.Selection, error) {
	view, err := s.Get(ctx)
	if err != nil {
		return taskmodel.Selection{}, err
	}
	return taskmodel.Resolve(taskmodel.Selection{
		Model: view.Public.CodexDefaultModel,
		ReasoningEffort: view.Public.CodexDefaultReasoningEffort,
	}, override)
}
```

Update the JSON schema required fields and properties:

```json
"codex_default_model": {
  "type": "string",
  "minLength": 1,
  "maxLength": 128,
  "pattern": "^[A-Za-z0-9_./:][A-Za-z0-9_.:/-]{0,127}$"
},
"codex_default_reasoning_effort": {
  "enum": ["low", "medium", "high", "xhigh", "max", "ultra"]
}
```

- [ ] **Step 5: Run focused tests and schema checks**

Run:

```powershell
go test ./internal/taskmodel ./internal/settings ./internal/httpapi -run 'Selection|CodexDefault|SettingsPublic' -count=1
go test ./internal/settings ./internal/httpapi -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit the settings contract**

```powershell
git add internal/taskmodel/model_selection.go internal/taskmodel/model_selection_test.go internal/domain/settings.go internal/settings/service.go internal/settings/service_test.go internal/httpapi/settings_test.go schemas/settings.schema.json
git commit -m "feat: add Codex task model defaults"
```

## Task 2: Persist resolved model values on every task

**Files:**
- Modify: `internal/domain/models.go`
- Modify: `internal/store/migrations.go`
- Modify: `internal/store/migrations_test.go`
- Modify: `internal/store/tasks.go`
- Create: `internal/store/tasks_model_test.go`

- [ ] **Step 1: Write failing migration and repository tests**

Append a migration test that opens a database at the predecessor migration count, inserts an old task, reopens it, and asserts:

```go
var model, effort string
err := db.QueryRow(`SELECT model_name, reasoning_effort FROM codex_tasks WHERE id=?`, taskID).Scan(&model, &effort)
if err != nil {
	t.Fatal(err)
}
if model != taskmodel.DefaultModel || effort != taskmodel.DefaultReasoningEffort {
	t.Fatalf("model=%q effort=%q", model, effort)
}
```

Create a repository round-trip test with:

```go
task := domain.CodexTask{
	ID: uuid.NewString(), AccountID: accountID, Type: "remix",
	SkillName: "finance-viral-remix", Action: domain.ActionRemixStandard,
	Status: domain.TaskQueued, PromptSnapshot: "test", CreatedAt: time.Now().UTC(),
	ModelName: "gpt-5.6-terra", ReasoningEffort: "high",
}
if err := repo.CreateV2(t.Context(), task); err != nil { t.Fatal(err) }
got, err := repo.Get(t.Context(), task.ID)
if err != nil { t.Fatal(err) }
if got.ModelName != task.ModelName || got.ReasoningEffort != task.ReasoningEffort {
	t.Fatalf("got=%+v", got)
}
```

- [ ] **Step 2: Run focused tests and verify failure**

Run:

```powershell
go test ./internal/store -run 'TaskModel|ModelSelectionMigration' -count=1
```

Expected: FAIL because the columns and domain fields do not exist.

- [ ] **Step 3: Add domain fields and additive migration**

Add to `domain.CodexTask`:

```go
ModelName       string
ReasoningEffort string
```

Append one migration string:

```sql
ALTER TABLE codex_tasks ADD COLUMN model_name TEXT NOT NULL DEFAULT 'gpt-5.6-sol';
ALTER TABLE codex_tasks ADD COLUMN reasoning_effort TEXT NOT NULL DEFAULT 'medium';
UPDATE codex_tasks SET model_name='gpt-5.6-sol' WHERE trim(model_name)='';
UPDATE codex_tasks SET reasoning_effort='medium' WHERE trim(reasoning_effort)='';
```

Do not rewrite migration 3 or any published migration; the additive migration must run for both fresh databases and upgraded databases.

- [ ] **Step 4: Update all task SQL projections**

Update both `Create` and `CreateV2` to write `model_name,reasoning_effort`. Update `Get` and `List` SELECT/Scan lists in exactly the same order. Introduce one shared projection constant to prevent future drift:

```go
const taskColumns = `id,project_id,account_id,type,skill_name,action,status,codex_session_id,prompt_snapshot,result_summary,error_code,error_message,model_name,reasoning_effort,created_at,started_at,finished_at`
```

For legacy callers that construct an empty model selection, use `taskmodel.Resolve(taskmodel.Selection{Model: taskmodel.DefaultModel, ReasoningEffort: taskmodel.DefaultReasoningEffort}, taskmodel.Selection{Model: task.ModelName, ReasoningEffort: task.ReasoningEffort})` inside `Create` and `CreateV2` before INSERT; do not allow empty persisted values. The independent package prevents the existing `codex -> store` dependency from becoming a cycle.

- [ ] **Step 5: Run repository and migration tests**

Run:

```powershell
go test ./internal/store -run 'TaskModel|ModelSelectionMigration|Migration' -count=1
go test ./internal/store -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit task persistence**

```powershell
git add internal/domain/models.go internal/store/migrations.go internal/store/migrations_test.go internal/store/tasks.go internal/store/tasks_model_test.go
git commit -m "feat: persist Codex task model selection"
```

## Task 3: Pass persisted values to Codex exec and resume

**Files:**
- Modify: `internal/codex/command.go`
- Modify: `internal/codex/command_test.go`
- Modify: `cmd/console/main.go`
- Modify: `cmd/console/main_test.go`

- [ ] **Step 1: Change exact-command tests first**

Update `commandFixture` so `Config` contains:

```go
ModelName: "gpt-5.6-sol",
ReasoningEffort: "medium",
```

Require both exact argv lists to include independent arguments:

```go
"-m", "gpt-5.6-sol", "-c", `model_reasoning_effort="medium"`,
```

Add a factory test that passes a task with `gpt-5.6-terra + high`, changes no global configuration, and asserts both `makeCommand(task)` and `makeResume(task, "answer")` contain the task values.

- [ ] **Step 2: Run focused tests and verify failure**

Run:

```powershell
go test ./internal/codex ./cmd/console -run 'BuildExecCommandV2|BuildResumeCommandV2|CommandFactor.*Model' -count=1
```

Expected: FAIL because command config has no model fields and argv omits them.

- [ ] **Step 3: Add model fields to command config and validate them**

Extend `codex.Config`:

```go
ModelName       string
ReasoningEffort string
```

At the beginning of `Config.normalized`, call:

```go
selection, err := taskmodel.Normalize(taskmodel.Selection{
	Model: c.ModelName,
	ReasoningEffort: c.ReasoningEffort,
})
if err != nil { return Config{}, err }
c.ModelName = selection.Model
c.ReasoningEffort = selection.ReasoningEffort
```

- [ ] **Step 4: Add explicit argv without shell interpolation**

For first execution, extend the existing argument slice immediately after `exec`:

```go
args := []string{
	"--ask-for-approval", "never", "exec",
	"-m", cfg.ModelName,
	"-c", fmt.Sprintf(`model_reasoning_effort="%s"`, cfg.ReasoningEffort),
	"--json", "--skip-git-repo-check",
}
```

For resume, add the same pair after `resume`. Continue using `exec.Command(resolved, args...)`; never construct a shell command string.

- [ ] **Step 5: Wire task values into both factories**

Change the helper signature and assign the persisted values:

```go
func taskCommandConfig(base codex.Config, projectRoot, taskRoot string, task domain.CodexTask) codex.Config {
	base.WorkingDirectory = projectRoot
	base.OutputLastMessage = filepath.Join(taskRoot, "output-last-message.json")
	base.ModelName = task.ModelName
	base.ReasoningEffort = task.ReasoningEffort
	return base
}
```

Update both call sites to pass `task`:

```go
cfg := taskCommandConfig(base, root, taskRoot, task)
```

```go
cmd, err := codex.BuildResumeCommand(taskCommandConfig(base, root, taskRoot, task), *task.CodexSessionID, answer)
```

This keeps initial and resume factories on the same persisted values, so changing console defaults later cannot affect an existing task.

- [ ] **Step 6: Run command and factory tests**

Run:

```powershell
go test ./internal/codex ./cmd/console -count=1
```

Expected: PASS, including exact argv and resume tests.

- [ ] **Step 7: Commit CLI execution support**

```powershell
git add internal/codex/command.go internal/codex/command_test.go cmd/console/main.go cmd/console/main_test.go
git commit -m "feat: select model for Codex task commands"
```

## Task 4: Resolve project-task overrides in the HTTP API

**Files:**
- Create: `internal/httpapi/task_models.go`
- Modify: `internal/httpapi/tasks.go`
- Create: `internal/httpapi/tasks_model_test.go`
- Modify: `internal/app/app.go`

- [ ] **Step 1: Write failing handler tests**

Create a fake resolver:

```go
type fixedTaskModelResolver struct {
	defaults taskmodel.Selection
}

func (r fixedTaskModelResolver) ResolveTaskModel(_ context.Context, override taskmodel.Selection) (taskmodel.Selection, error) {
	return taskmodel.Resolve(r.defaults, override)
}
```

Use a synchronous persisted scheduler and test these POST bodies:

```json
{"account_id":"<uuid>","type":"remix","prompt":"test"}
{"account_id":"<uuid>","type":"remix","prompt":"test","model":"gpt-5.6-terra"}
{"account_id":"<uuid>","type":"remix","prompt":"test","reasoning_effort":"high"}
{"account_id":"<uuid>","type":"remix","prompt":"test","model":"gpt-5.6-terra","reasoning_effort":"high"}
```

Assert the persisted task has the resolved pair. Send `"model":"-danger"` and assert HTTP 400 and zero new rows.

- [ ] **Step 2: Run tests and verify failure**

Run:

```powershell
go test ./internal/httpapi -run 'ProjectTask.*Model' -count=1
```

Expected: FAIL because the handler ignores overrides and does not expose model metadata.

- [ ] **Step 3: Add a narrow resolver contract**

Create `internal/httpapi/task_models.go`:

```go
package httpapi

import (
	"context"
	"video-production-console/internal/taskmodel"
)

type TaskModelResolver interface {
	ResolveTaskModel(context.Context, taskmodel.Selection) (taskmodel.Selection, error)
}

type taskModelRequest struct {
	Model string `json:"model"`
	ReasoningEffort string `json:"reasoning_effort"`
}

func (r taskModelRequest) selection() taskmodel.Selection {
	return taskmodel.Selection{Model: r.Model, ReasoningEffort: r.ReasoningEffort}
}

type defaultTaskModelResolver struct{}

func (defaultTaskModelResolver) ResolveTaskModel(_ context.Context, override taskmodel.Selection) (taskmodel.Selection, error) {
	return taskmodel.Resolve(taskmodel.Selection{
		Model: taskmodel.DefaultModel,
		ReasoningEffort: taskmodel.DefaultReasoningEffort,
	}, override)
}

func taskModelResolverOrDefault(resolver TaskModelResolver) TaskModelResolver {
	if resolver == nil {
		return defaultTaskModelResolver{}
	}
	return resolver
}
```

- [ ] **Step 4: Resolve before enqueue and expose the result**

Give `taskAPI` a required `models TaskModelResolver` field and change the constructor to:

```go
func NewTasksHandler(db *sql.DB, s codex.Scheduler, preparer TaskManifestPreparer, models TaskModelResolver) http.Handler
```

Assign `models: taskModelResolverOrDefault(models)`. Update `internal/httpapi/task_manifest_test.go` to pass its existing `preparer` followed by `nil`; update any two-argument task-handler construction to pass `nil, nil`. Embed `taskModelRequest` in the create request, resolve it before manifest preparation, then set:

```go
ModelName: selection.Model,
ReasoningEffort: selection.ReasoningEffort,
```

Add to `taskView`:

```go
Model string `json:"model"`
ReasoningEffort string `json:"reasoning_effort"`
```

Convert `viewTask` from positional to named fields so adding metadata cannot silently reorder JSON fields.

- [ ] **Step 5: Inject the real settings service**

In `internal/app/app.go`, avoid converting a nil `*settings.Service` into a non-nil interface:

```go
var taskModels httpapi.TaskModelResolver
if options.Settings != nil {
	taskModels = options.Settings
}
tasksHandler := httpapi.NewTasksHandler(options.DB, options.Scheduler, options.TaskPreparer, taskModels)
```

`NewTasksHandler` normalizes a nil resolver to a private resolver backed by `taskmodel.DefaultModel` and `taskmodel.DefaultReasoningEffort`; production construction uses the real settings service.

- [ ] **Step 6: Run API tests**

Run:

```powershell
go test ./internal/httpapi ./internal/app -run 'ProjectTask.*Model|TaskView|App' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit the project task API**

```powershell
git add internal/httpapi/task_models.go internal/httpapi/tasks.go internal/httpapi/tasks_model_test.go internal/app/app.go
git commit -m "feat: accept Codex model overrides for project tasks"
```

## Task 5: Apply the same contract to selection conversations

**Files:**
- Modify: `internal/httpapi/ideas.go`
- Modify: `internal/httpapi/ideas_test.go`
- Modify: `internal/app/app.go`

- [ ] **Step 1: Add failing selection-message tests**

Extend `TestIdeaMessagePersistsTaskBeforeForeignKeyReference` with a resolver default of `gpt-5.6-sol + medium`, then add a second test posting:

```json
{"content":"测试选题","model":"gpt-5.6-terra","reasoning_effort":"high"}
```

Read the referenced task and assert the exact override. Add an invalid reasoning effort request and assert HTTP 400 without a new message or task.

- [ ] **Step 2: Run focused tests and verify failure**

Run:

```powershell
go test ./internal/httpapi -run 'IdeaMessage.*Model|IdeaMessagePersists' -count=1
```

Expected: FAIL because the idea message request does not parse or resolve model fields.

- [ ] **Step 3: Reuse the task model request and resolver**

Give `ideasHandler` a `models TaskModelResolver` field and change the constructor to:

```go
func NewIdeasHandler(db *sql.DB, scheduler codex.Scheduler, preparer TaskManifestPreparer, models TaskModelResolver) http.Handler
```

Assign `models: taskModelResolverOrDefault(models)`. Update the existing idea tests to call `NewIdeasHandler(db, scheduler, nil, resolver)`; the delete-only test uses `nil, nil, nil`. Embed `taskModelRequest` beside `Content` and `AccountID`. Immediately before constructing `domain.CodexTask`, resolve and assign:

```go
selection, err := h.models.ResolveTaskModel(r.Context(), in.taskModelRequest.selection())
if err != nil {
	writeError(w, http.StatusBadRequest, "invalid_task_model", err.Error())
	return
}
```

Only call the resolver on the path that actually enqueues Codex. The scheduler-nil conversation persistence behavior remains unchanged.

- [ ] **Step 4: Inject settings from the app**

Update the `NewIdeasHandler` call in `internal/app/app.go` to pass the same resolver used by project tasks.

- [ ] **Step 5: Run all idea and task API tests**

Run:

```powershell
go test ./internal/httpapi -run 'Idea|ProjectTask|TaskView' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit selection conversation support**

```powershell
git add internal/httpapi/ideas.go internal/httpapi/ideas_test.go internal/app/app.go
git commit -m "feat: select Codex model for topic conversations"
```

## Task 6: Add settings, per-task controls, and actual-model display to React

**Files:**
- Modify: `web/src/App.tsx`
- Modify: `web/src/App.test.tsx`
- Modify: `web/src/App.css`
- Modify: `web/src/idea.css`

- [ ] **Step 1: Write failing UI tests**

Mock `/api/settings` with:

```ts
public: {
  listen_addr: "127.0.0.1:2030",
  data_root: "C:\\console-data",
  max_codex_concurrency: 2,
  baokuan_base_url: "http://127.0.0.1:2022",
  baokuan_mcp_executable: "C:\\tools\\baokuan.exe",
  obsidian_vault: "C:\\vault",
  topic_cards_dir: "C:\\vault\\topic-cards",
  grok_base_url: "http://127.0.0.1:3030",
  grok_model: "grok-4.5",
  codex_binary_path: "C:\\tools\\codex.exe",
  media_index_path: "C:\\media\\index.json",
  media_root: "C:\\media",
  jianying_root: "C:\\jianying",
  codex_default_model: "gpt-5.6-sol",
  codex_default_reasoning_effort: "medium",
}
```

Add tests that:

1. Open settings and find “默认模型” and “默认推理强度”.
2. Open the task configuration, choose `high`, start a remix task, and inspect the POST body for `reasoning_effort: "high"` with no `model` when model inheritance remains selected.
3. Send an idea message with model override and inspect the idea-message POST body.
4. Render a task with `model: "gpt-5.6-terra"` and `reasoning_effort: "high"`, then find `gpt-5.6-terra · high`.
5. Render an awaiting-input task and verify the reply path displays “继续使用” but no editable model control.

- [ ] **Step 2: Run UI tests and verify failure**

Run:

```powershell
Set-Location web
npx vitest run src/App.test.tsx
```

Expected: FAIL because the types and controls do not exist.

- [ ] **Step 3: Extend frontend types and reusable state**

Add fields to `Task` and `PublicSettings`:

```ts
model?: string;
reasoning_effort?: ReasoningEffort;
```

```ts
codex_default_model: string;
codex_default_reasoning_effort: ReasoningEffort;
```

Define:

```ts
type ReasoningEffort = "low" | "medium" | "high" | "xhigh" | "max" | "ultra";
type TaskModelOverride = { model: string; reasoning_effort: "" | ReasoningEffort };
const inheritedTaskModel: TaskModelOverride = { model: "", reasoning_effort: "" };
```

Keep separate override state for the project workflow launcher and idea composer so opening one surface cannot leak temporary values into the other.

- [ ] **Step 4: Add the settings controls**

Render a normal text input for `codex_default_model` and a select for `codex_default_reasoning_effort`. Include this exact user guidance:

```text
默认值只影响之后新建的任务，不会修改运行中任务，也不会改写本机 Codex 全局配置。
```

After a successful PUT, use the response body to replace both `settings` and `settingsDraft`; do not keep a stale local draft.

- [ ] **Step 5: Add the compact task configuration control**

Implement one local `TaskModelFields` component in `App.tsx` with props for defaults, value, and `onChange`. It renders:

- A collapsed summary by default.
- Model input whose empty value means inheritance.
- Reasoning select whose empty value means inheritance.
- `实际将使用：<resolved model> · <resolved effort>`.

Do not create a global provider or new state library for two fields.

- [ ] **Step 6: Send only explicit overrides and reset them**

In both `startTask` and `sendIdeaMessage`, build:

```ts
const taskModelPayload = {
  ...(override.model.trim() ? { model: override.model.trim() } : {}),
  ...(override.reasoning_effort ? { reasoning_effort: override.reasoning_effort } : {}),
};
```

Merge it into the JSON body. Reset the corresponding override to `inheritedTaskModel` only after a successful response; preserve it when sending fails so the user can retry.

- [ ] **Step 7: Display actual values and freeze resume UI**

In each task row render:

```tsx
{task.model && task.reasoning_effort && (
  <small className="task-model">{task.model} · {task.reasoning_effort}</small>
)}
```

For awaiting-input tasks show `继续使用：...` beside the reply action. Do not render editable model fields in `answerTask`; its request stays `{ answer }`.

- [ ] **Step 8: Style and run frontend checks**

Add compact `.task-model-config`, `.task-model-grid`, and `.task-model` styles using existing colors, border radius, and responsive breakpoints. Then run:

```powershell
Set-Location web
npx vitest run src/App.test.tsx
npm run lint
npm run build
```

Expected: all tests PASS, lint exits 0, and Vite production build succeeds.

- [ ] **Step 9: Commit the web UI**

```powershell
git add web/src/App.tsx web/src/App.test.tsx web/src/App.css web/src/idea.css
git commit -m "feat: choose Codex model from the console"
```

## Task 7: Full regression, embedded build, and runtime smoke test

**Files:**
- Modify: `internal/webui/dist/index.html`
- Replace generated hashed files under: `internal/webui/dist/assets/`

- [ ] **Step 1: Run formatting and the full Go suite**

Run:

```powershell
gofmt -w internal/taskmodel/model_selection.go internal/taskmodel/model_selection_test.go internal/domain/settings.go internal/settings/service.go internal/settings/service_test.go internal/httpapi/settings_test.go internal/domain/models.go internal/store/migrations.go internal/store/migrations_test.go internal/store/tasks.go internal/store/tasks_model_test.go internal/codex/command.go internal/codex/command_test.go cmd/console/main.go cmd/console/main_test.go internal/httpapi/task_models.go internal/httpapi/tasks.go internal/httpapi/tasks_model_test.go internal/httpapi/task_manifest_test.go internal/httpapi/ideas.go internal/httpapi/ideas_test.go internal/app/app.go
go test ./... -count=1
```

Expected: formatting produces no semantic changes and every Go package passes.

- [ ] **Step 2: Run full frontend verification**

Run:

```powershell
Set-Location web
npx vitest run
npm run lint
npm run build
Set-Location ..
```

Expected: all Vitest tests pass, lint exits 0, and the build succeeds.

- [ ] **Step 3: Copy the verified frontend build**

`web/vite.config.ts` already sets `build.outDir` to `../internal/webui/dist` with `emptyOutDir: true`, so `npm run build` in Step 2 has already replaced the embedded build. Verify it directly:

```powershell
$entry = Get-Content -LiteralPath 'internal\webui\dist\index.html' -Encoding UTF8 -Raw
if ($entry -notmatch '/assets/index-[^"'']+\.js') { throw 'embedded JavaScript bundle reference missing' }
if ($entry -notmatch '/assets/index-[^"'']+\.css') { throw 'embedded CSS bundle reference missing' }
Get-ChildItem -LiteralPath 'internal\webui\dist\assets' | Select-Object Name,Length
```

Expected: `internal/webui/dist/index.html` references the newly generated hashed JS and CSS files, and no stale referenced bundle is missing.

- [ ] **Step 4: Build the console binary**

Run:

```powershell
go build ./cmd/console
```

Expected: exit code 0.

- [ ] **Step 5: Perform a non-destructive runtime smoke test**

Restart only the current console process using the project's existing safe launcher. Log in at `http://127.0.0.1:2030`, then verify through HTTP/UI:

1. Settings shows `gpt-5.6-sol + medium`.
2. Creating a task with no override stores those values.
3. Creating a task with a harmless fake model name reaches CLI and fails visibly rather than silently falling back.
4. Task details show the selected values.
5. No WeChat Channels window or Jianying application is opened.

Do not terminate the network proxy or unrelated Node/Codex processes.

- [ ] **Step 6: Inspect the final diff and commit generated assets**

Run:

```powershell
git diff --check
git status --short
```

Confirm only intended source, tests, schema, migration, and generated frontend assets changed. Then:

```powershell
git add internal/webui/dist
git commit -m "build: embed Codex model selection UI"
```

- [ ] **Step 7: Record verification evidence**

In the implementation handoff, report the exact commands run, pass counts, console address, and any pre-existing dirty files intentionally left untouched. Do not claim completion if any focused or full-suite test is failing.
