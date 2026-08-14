# Image Project Quick Generation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a refresh-safe one-click image-project workflow with configurable per-image total attempts, partial-success delivery, a dedicated advanced manual route, and a polished publishing-copy editor.

**Architecture:** Keep the existing SQLite repository and HTTP service. Add persisted quick-run checkpoints plus a small in-process job guard; `POST /api/image-projects/quick-generate` creates the project before starting detached work and `POST /api/image-projects/{id}/resume` continues from the last safe phase. The React entry defaults to a focused quick form, while the current segment-confirmation UI remains available at `/image-projects/advanced`.

**Tech Stack:** Go 1.24, `net/http`, SQLite, React 19, TypeScript, Vitest, Testing Library, Playwright, Vite, embedded frontend assets.

---

## File structure and responsibilities

### New files

- `internal/httpapi/imageproject_jobs.go`: per-project in-process job guard; prevents duplicate quick runs.
- `internal/httpapi/imageproject_quick.go`: quick request validation, 202 creation, phase orchestration, resume endpoint, and deterministic fallback title.
- `internal/httpapi/imageproject_quick_test.go`: asynchronous endpoint, checkpoint, resume, and partial-success HTTP tests.
- `web/src/image-mode/QuickGenerateForm.tsx`: focused one-click form and request submission.
- `web/src/image-mode/QuickGenerateForm.test.tsx`: form defaults, payload, validation, and 202 navigation tests.
- `web/src/image-mode/PublishingDialog.tsx`: wide two-column publishing candidate editor.
- `web/src/image-mode/PublishingDialog.test.tsx`: candidate switching, length limits, copy, save, and empty-state tests.

### Existing files to modify

- `internal/domain/settings.go`: expose the global image-attempt default.
- `internal/store/settings.go`: allow the new public setting key.
- `internal/settings/service.go`: default, validate, serialize, and deserialize image attempts.
- `schemas/settings.schema.json`, `schemas/settings_schema_test.go`: publish the 1–4/default-2 contract.
- `web/src/settings/SettingsPanel.tsx`, `web/src/settings/SettingsPanel.test.tsx`: editable global default.
- `internal/domain/imageprojects.go`: quick-run phase, progress, model selection, and item attempt fields.
- `internal/store/migrations.go`: append backward-compatible quick-run columns.
- `internal/store/imageprojects.go`, `internal/store/imageprojects_test.go`: persist checkpoints, plans, prompts, counters, and interrupted runs.
- `internal/imageproject/client.go`, `internal/imageproject/client_test.go`: parameterize total attempts and report actual attempts.
- `internal/imageproject/planner.go`, `internal/imageproject/planner_test.go`: one-call quick plan with AI title and non-blocking publishing validation.
- `internal/httpapi/imageprojects.go`, `internal/httpapi/imageprojects_test.go`, `internal/httpapi/imageprojects_lifecycle_test.go`: mount quick routes and reuse configurable generation.
- `internal/app/app.go`: initialize the handler so stale running jobs are marked interrupted on startup.
- `web/src/types.ts`: settings, quick request/response, run phase, run status, progress, and item attempt types.
- `web/src/project-workbench/routes.ts`, `web/src/project-workbench/routes.test.ts`: add `/image-projects/advanced`.
- `web/src/App.tsx`, `web/src/App.test.tsx`: route quick/manual mode and pass image defaults.
- `web/src/image-mode/ImageModeWorkbench.tsx`, `web/src/image-mode/ImageModeWorkbench.test.tsx`: eliminate duplicate GETs, poll active jobs, render progress, and integrate extracted components.
- `web/src/image-mode/image-mode.css`: focus-workbench layout, stage rail, quick detail states, and 960px publishing editor.
- `web/e2e/image-mode.spec.ts`: cover quick and advanced flows, refresh recovery, and partial success.
- `docs/ARCHITECTURE.md`, `docs/USER-GUIDE.md`, `docs/AI-HANDOFF.md`: document routes, settings semantics, job recovery, and operator-visible errors.
- `internal/webui/dist/**`: regenerate embedded frontend assets after verification.

## Task 1: Add the global image-attempt default

**Files:**
- Modify: `internal/domain/settings.go:5-46`
- Modify: `internal/store/settings.go:24-40`
- Modify: `internal/settings/service.go:30-54,307-319,438-441,489-505,756-804,845-910`
- Modify: `internal/settings/service_test.go:767-875`
- Modify: `internal/settings/active_snapshot_test.go`
- Modify: `schemas/settings.schema.json:79-108,190-207`
- Modify: `schemas/settings_schema_test.go:49-109`
- Modify: `web/src/types.ts:74-114`
- Modify: `web/src/settings/SettingsPanel.tsx:223-267`
- Modify: `web/src/settings/SettingsPanel.test.tsx:144-171`

- [ ] **Step 1: Write failing backend and schema tests**

Add assertions that legacy settings resolve to 2, values 1 and 4 round-trip, and 0 or 5 are rejected. Assert in `active_snapshot_test.go` that changing the value is hot-applied and does not set `restart_required`. Extend the schema image key list and assert the exact limits:

```go
func TestSettingsImageGenerationAttemptsDefaultRoundTripAndValidation(t *testing.T) {
	service, _, _, public := newSettingsTestService(t, Options{})
	public.ImageGenerationAttempts = 4
	view, err := service.Update(t.Context(), public, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if view.Public.ImageGenerationAttempts != 4 {
		t.Fatalf("attempts=%d", view.Public.ImageGenerationAttempts)
	}
	for _, invalidValue := range []int{0, 5} {
		candidate := public
		candidate.ImageGenerationAttempts = invalidValue
		if _, err := service.PutPublic(t.Context(), candidate); !errors.Is(err, ErrInvalidSettings) {
			t.Fatalf("attempts=%d error=%v", invalidValue, err)
		}
	}
}
```

```go
attempts := properties["image_generation_attempts"].(map[string]any)
if attempts["minimum"] != float64(1) || attempts["maximum"] != float64(4) || attempts["default"] != float64(2) {
	t.Fatalf("image_generation_attempts schema=%v", attempts)
}
```

- [ ] **Step 2: Run the focused tests and verify failure**

Run:

```powershell
go test ./internal/settings ./schemas
Set-Location web
npm test -- --run src/settings/SettingsPanel.test.tsx
```

Expected: Go tests fail because `ImageGenerationAttempts` and the schema key do not exist; the frontend test fails because the field is not rendered.

- [ ] **Step 3: Implement the settings contract**

Add the public field and constants:

```go
ImageGenerationAttempts int `json:"image_generation_attempts"`
```

```go
defaultImageGenerationAttempts = 2
maxImageGenerationAttempts     = 4
```

Add `image_generation_attempts` to `publicSettingKeys`, `publicValues`, `publicFromValues`, `withImageDefaults`, and `validatePublic`. Missing or out-of-range persisted legacy values must resolve to 2; explicit update values outside 1–4 must return `ErrInvalidSettings`. Copy the value in `applyHotSettings`, and zero it alongside concurrency fields in `restartSensitiveChanged` so saving it does not require a restart.

Add this property to `publicSettings` and require it only in `publicSettingsView`, preserving legacy PUT compatibility:

```json
"image_generation_attempts": {
  "type": "integer",
  "minimum": 1,
  "maximum": 4,
  "default": 2
}
```

Add the TypeScript field and a settings select:

```ts
image_generation_attempts: number;
```

```tsx
<label className="settings-field">
  每张图片最多请求次数
  <select
    value={draft.image_generation_attempts || 2}
    onChange={(event) => onDraftChange({
      ...draft,
      image_generation_attempts: Number(event.target.value),
    })}
  >
    {[1, 2, 3, 4].map((value) => <option key={value} value={value}>{value}</option>)}
  </select>
  <small>包含首次请求；设置为 2 表示失败后最多再请求一次。</small>
</label>
```

- [ ] **Step 4: Run backend and frontend settings tests**

Run:

```powershell
go test ./internal/settings ./schemas
Set-Location web
npm test -- --run src/settings/SettingsPanel.test.tsx
```

Expected: all focused tests pass.

- [ ] **Step 5: Commit the settings slice**

```powershell
git add internal/domain/settings.go internal/store/settings.go internal/settings/service.go internal/settings/service_test.go internal/settings/active_snapshot_test.go schemas/settings.schema.json schemas/settings_schema_test.go web/src/types.ts web/src/settings/SettingsPanel.tsx web/src/settings/SettingsPanel.test.tsx
git commit -m "feat: configure image generation attempts"
```

## Task 2: Persist quick-run checkpoints and per-item attempts

**Files:**
- Modify: `internal/domain/imageprojects.go:5-53`
- Modify: `internal/store/migrations.go:882-892`
- Modify: `internal/store/imageprojects.go:24-130,178-230,269-287`
- Modify: `internal/store/imageprojects_test.go:13-85`

- [ ] **Step 1: Write failing repository tests**

Add `TestImageProjectRepositoryPersistsQuickRunStateAndAttempts` and `TestImageProjectRepositoryMarksRunningQuickProjectsInterrupted`. The first creates a quick project, saves a two-item plan, saves prompts, records item attempts, and reloads every field. The second creates one running quick project and one manual project, calls the interruption method, and asserts only the quick run changes.

Use this expected project contract:

```go
project := domain.ImageProject{
	ID: uuid.NewString(), Title: "fallback", Script: "第一句。第二句。",
	ImageCount: 1, Ratio: "3:4", Style: "finance_documentary", Concurrency: 2,
	Status: "draft", RunMode: "quick", RunPhase: "planning", RunStatus: "running",
	ImageAttempts: 2, TextModel: "gpt-5.6-sol", ReasoningEffort: "medium",
	ImageModel: "gpt-image-2", CreatedAt: now, UpdatedAt: now,
}
```

Expected reload assertions:

```go
if got.RunMode != "quick" || got.RunPhase != "prompting" || got.RunStatus != "running" {
	t.Fatalf("run state=%+v", got)
}
if got.ImageAttempts != 2 || got.TextModel != "gpt-5.6-sol" || got.ImageModel != "gpt-image-2" {
	t.Fatalf("run config=%+v", got)
}
if len(gotItems) != 2 || gotItems[0].AttemptCount != 2 {
	t.Fatalf("items=%+v", gotItems)
}
```

- [ ] **Step 2: Run the store test and verify failure**

Run:

```powershell
go test ./internal/store -run 'TestImageProjectRepository(PersistsQuickRunStateAndAttempts|MarksRunningQuickProjectsInterrupted)' -count=1
```

Expected: compilation fails because the domain fields and repository methods do not exist.

- [ ] **Step 3: Add domain fields and an append-only migration**

Extend `domain.ImageProject`:

```go
RunMode          string `json:"run_mode"`
RunPhase         string `json:"run_phase"`
RunStatus        string `json:"run_status"`
PhaseError       string `json:"phase_error,omitempty"`
PublishingError  string `json:"publishing_error,omitempty"`
SuccessCount     int    `json:"success_count"`
FailureCount     int    `json:"failure_count"`
ImageAttempts    int    `json:"image_attempts"`
TextModel        string `json:"text_model,omitempty"`
ReasoningEffort  string `json:"reasoning_effort,omitempty"`
ImageModel       string `json:"image_model,omitempty"`
```

Extend `domain.ImageProjectItem`:

```go
AttemptCount int `json:"attempt_count"`
```

Append one migration after the publishing migration; do not edit historical migrations:

```sql
ALTER TABLE image_projects ADD COLUMN run_mode TEXT NOT NULL DEFAULT 'manual' CHECK(run_mode IN ('manual','quick'));
ALTER TABLE image_projects ADD COLUMN run_phase TEXT NOT NULL DEFAULT 'idle' CHECK(run_phase IN ('idle','planning','prompting','imaging','completed'));
ALTER TABLE image_projects ADD COLUMN run_status TEXT NOT NULL DEFAULT 'idle' CHECK(run_status IN ('idle','running','failed','completed','interrupted'));
ALTER TABLE image_projects ADD COLUMN phase_error TEXT NOT NULL DEFAULT '';
ALTER TABLE image_projects ADD COLUMN publishing_error TEXT NOT NULL DEFAULT '';
ALTER TABLE image_projects ADD COLUMN success_count INTEGER NOT NULL DEFAULT 0 CHECK(success_count >= 0);
ALTER TABLE image_projects ADD COLUMN failure_count INTEGER NOT NULL DEFAULT 0 CHECK(failure_count >= 0);
ALTER TABLE image_projects ADD COLUMN image_attempts INTEGER NOT NULL DEFAULT 2 CHECK(image_attempts BETWEEN 1 AND 4);
ALTER TABLE image_projects ADD COLUMN text_model TEXT NOT NULL DEFAULT '';
ALTER TABLE image_projects ADD COLUMN reasoning_effort TEXT NOT NULL DEFAULT '';
ALTER TABLE image_projects ADD COLUMN image_model TEXT NOT NULL DEFAULT '';
ALTER TABLE image_project_items ADD COLUMN attempt_count INTEGER NOT NULL DEFAULT 0 CHECK(attempt_count >= 0);
```

Keep the existing readiness `Status` field separate from `RunStatus`. Normalize zero-value legacy structs inside `Create`: mode=`manual`, phase=`idle`, run status=`idle`, image attempts=`2`.

- [ ] **Step 4: Implement repository checkpoint methods**

Update every INSERT, SELECT, Scan, and item query to include the new fields. Add these exact repository boundaries:

```go
func (r *ImageProjectRepository) SetRunState(ctx context.Context, id, phase, status, phaseError string, successCount, failureCount int, at time.Time) error
func (r *ImageProjectRepository) SaveQuickPlan(ctx context.Context, id, title string, items []domain.ImageProjectItem, candidates []domain.PublishingCandidate, publishingError string, at time.Time) error
func (r *ImageProjectRepository) SavePrompts(ctx context.Context, projectID string, prompts []string, at time.Time) error
func (r *ImageProjectRepository) MarkItemAttempts(ctx context.Context, itemID string, attempts int, at time.Time) error
func (r *ImageProjectRepository) MarkRunningQuickProjectsInterrupted(ctx context.Context, at time.Time) error
func (r *ImageProjectRepository) UpdateProjectTitle(ctx context.Context, projectID, title string, at time.Time) error
```

`SaveQuickPlan` must use one transaction to replace items and publishing candidates, set `title`, set the real `image_count`, select candidate 1 only when five valid candidates exist, set phase=`prompting`, and preserve the original script. `SavePrompts` must require exactly one prompt per ordered item and set phase=`imaging` in the same transaction. `MarkRunningQuickProjectsInterrupted` must update only `run_mode='quick' AND run_status='running'`. `UpdateProjectTitle` trims the title, rejects blank or more than 120 runes, and updates `updated_at` without changing run state.

- [ ] **Step 5: Run repository and migration tests**

Run:

```powershell
go test ./internal/store -count=1
```

Expected: all store tests pass, including legacy create/read/delete tests.

- [ ] **Step 6: Commit persistence**

```powershell
git add internal/domain/imageprojects.go internal/store/migrations.go internal/store/imageprojects.go internal/store/imageprojects_test.go
git commit -m "feat: persist image project run checkpoints"
```

## Task 3: Parameterize image attempts and record actual calls

**Files:**
- Modify: `internal/imageproject/client.go:41-51,366-430`
- Modify: `internal/imageproject/client_test.go:190-219,220-247,351-399`
- Modify: `internal/httpapi/imageprojects.go:447-517`
- Modify: `internal/httpapi/imageprojects_test.go:64-87,439-470`
- Modify: `internal/httpapi/imageprojects_lifecycle_test.go`

- [ ] **Step 1: Replace fixed-retry tests with total-attempt tests**

Add table-driven assertions for total attempts 1, 2, and 4; retain permanent-error and cancellation cases. Add a URL response test with two data entries and assert the downloaded bytes come from the first URL only.

```go
func TestGenerateBatchUsesConfiguredTotalAttemptsPerItem(t *testing.T) {
	for _, totalAttempts := range []int{1, 2, 4} {
		t.Run(strconv.Itoa(totalAttempts), func(t *testing.T) {
			generator := &countingGenerator{failuresBeforeSuccess: 10}
			results := GenerateBatch(t.Context(), generator, []GenerateRequest{{Prompt: "p"}}, 1, totalAttempts)
			if generator.Calls() != totalAttempts || results[0].Attempts != totalAttempts {
				t.Fatalf("calls=%d result=%+v", generator.Calls(), results[0])
			}
		})
	}
}
```

- [ ] **Step 2: Run the image client tests and verify failure**

Run:

```powershell
go test ./internal/imageproject -run 'TestGenerateBatch|TestClientGenerateAcceptsFirstPublicURL' -count=1
```

Expected: compilation fails because `GenerateBatch` does not accept total attempts and `GenerateResult` has no `Attempts` field.

- [ ] **Step 3: Implement total-attempt semantics**

Extend the result:

```go
type GenerateResult struct {
	Bytes     []byte
	MIMEType  string
	Width     int
	Height    int
	Attempts  int
	Error     error
}
```

Change the public batch signature and retry helper:

```go
func GenerateBatch(ctx context.Context, generator Generator, requests []GenerateRequest, concurrency, totalAttempts int) []GenerateResult
func generateWithRetry(ctx context.Context, generator Generator, request GenerateRequest, totalAttempts int) (GenerateResult, error)
```

Clamp `totalAttempts` to 1–4. Increment `result.Attempts` after every actual `generator.Generate` call. Treat 429/502/503/504, `io.EOF`, `io.ErrUnexpectedEOF`, timeout errors, `syscall.ECONNRESET`, and `net.ErrClosed` as transient when the caller context remains active. If `ctx.Err()` is non-nil, stop immediately; otherwise a wrapped `context.DeadlineExceeded` from the per-request HTTP client is a retryable network timeout. Keep cancellation, 400/401/403, and all other HTTP statuses permanent.

The URL decoder must continue to choose `result.Data[0]`; do not iterate or download later entries.

- [ ] **Step 4: Wire attempts into project generation**

In `imageProjectsHandler.generate`, choose the configured values as follows:

```go
attempts := project.ImageAttempts
if attempts < 1 || attempts > 4 {
	attempts = runtime.ImageGenerationAttempts
}
model := strings.TrimSpace(project.ImageModel)
if model == "" {
	model = runtime.ImageModel
}
```

Add an `ImageAttempts int` field with JSON name `image_attempts` to `imageProjectDraft`. During manual creation, use the explicit 1–4 value when present and otherwise copy `runtime.ImageGenerationAttempts`; persist the result in `domain.ImageProject.ImageAttempts`. Pass `attempts` to `GenerateBatch`, use `model` in every request, and call `MarkItemAttempts` before `MarkItemReady`, `MarkItemFailed`, or `MarkItemRegenerationFailed`. Preserve old image files when a regeneration fails.

- [ ] **Step 5: Run focused lifecycle tests**

Run:

```powershell
go test ./internal/imageproject ./internal/httpapi -run 'TestGenerateBatch|TestClientGenerateAcceptsFirstPublicURL|TestImageProjectsHTTP' -count=1
```

Expected: tests pass; a partial batch does not return a handler-level error merely because one item exhausted its attempts.

- [ ] **Step 6: Commit generation retry behavior**

```powershell
git add internal/imageproject/client.go internal/imageproject/client_test.go internal/httpapi/imageprojects.go internal/httpapi/imageprojects_test.go internal/httpapi/imageprojects_lifecycle_test.go
git commit -m "feat: honor per-project image attempts"
```

## Task 4: Add a quick-plan model response with AI title and validated topics

**Files:**
- Modify: `internal/imageproject/planner.go:20-31,96-126,217-260`
- Modify: `internal/imageproject/planner_test.go:20-147`
- Modify: `internal/imageproject/montageprompts.go`

- [ ] **Step 1: Write failing quick-plan tests**

Add tests proving that one model call returns a title, exact-covering segments, and five publishing candidates whose descriptions end with 3–5 hashtags. Add a second test where segments are valid but candidates are invalid; it must return the segments plus a non-empty publishing error instead of failing the whole plan.

```go
func TestSuggestQuickPlanKeepsValidSegmentsWhenPublishingIsInvalid(t *testing.T) {
	raw := `{"project_title":"存款流向","segments":[{"sequence":1,"role":"cover","title":"钱去哪了","source_text":"钱去哪了。"}],"publishing_candidates":[]}`
	chat := &scriptedChat{replies: []string{raw}}
	plan, err := SuggestQuickPlan(t.Context(), chat, "model", "钱去哪了。", 1, "medium")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Segments) != 1 || plan.PublishingError == "" {
		t.Fatalf("plan=%+v", plan)
	}
}
```

- [ ] **Step 2: Run planner tests and verify failure**

Run:

```powershell
go test ./internal/imageproject -run 'TestSuggestQuickPlan|TestQuickPlan' -count=1
```

Expected: compilation fails because `QuickPlan` and `SuggestQuickPlan` do not exist.

- [ ] **Step 3: Implement the quick-plan parser and schema**

Add:

```go
type QuickPlan struct {
	ProjectTitle    string
	Segments        []Segment
	Publishing      []PublishingCandidate
	PublishingError string
}

func SuggestQuickPlan(ctx context.Context, client ChatClient, model, script string, preferredCount int, reasoningEffort string) (QuickPlan, error)
func ParseQuickPlan(script, raw string) (QuickPlan, error)
```

The fatal contract is only: valid JSON, non-empty project title after fallback, and segments that exactly cover the original script. Publishing validation runs separately and returns its error in `PublishingError`.

Count topics with the pattern `#[^#\s]+` so both `#思维 #干货分享` and the user's compact `#思维#干货分享` form are accepted. Require 3–5 tags at the end of the description, require each tag to contain at least one rune after `#`, and reject replacement characters. Titles remain at most 22 runes and descriptions at most 1000 runes.

Add `project_title` to the strict quick response schema and prompt. Apply the same publishing topic instruction and validation to both quick planning and the existing manual `SuggestSegmentsAndPublishing` path: append 3–5 content-specific hashtags at the end of every description, separated by spaces; do not return generic tags only.

- [ ] **Step 4: Preserve existing planner guarantees**

Run:

```powershell
go test ./internal/imageproject -count=1
```

Expected: quick-plan tests and existing exact-source, strict-schema, prompt-text, and manual publishing tests all pass.

- [ ] **Step 5: Commit planner changes**

```powershell
git add internal/imageproject/planner.go internal/imageproject/planner_test.go internal/imageproject/montageprompts.go
git commit -m "feat: generate quick image project plans"
```

## Task 5: Implement asynchronous quick generation and resume

**Files:**
- Create: `internal/httpapi/imageproject_jobs.go`
- Create: `internal/httpapi/imageproject_quick.go`
- Create: `internal/httpapi/imageproject_quick_test.go`
- Modify: `internal/httpapi/imageprojects.go:28-62,248-338,447-530`
- Modify: `internal/app/app.go:108-113`

- [ ] **Step 1: Write failing HTTP tests**

Cover these scenarios with `httptest`, a blocking fake planner, and a deterministic fake generator:

1. `POST /api/image-projects/quick-generate` returns 202 and a persisted `project_id` before the blocking planner is released.
2. Releasing the planner advances planning → prompting → imaging → completed and persists success/failure counts.
3. Invalid publishing candidates do not stop prompt or image generation.
4. Two simultaneous resume requests start at most one worker; the other returns 409.
5. A failed prompting phase resumes from prompting without calling planning again.
6. Constructor initialization marks stale running quick jobs interrupted.
7. `PATCH /api/image-projects/{id}` trims and saves a valid user-edited project title and rejects blank or over-120-rune titles.

Use the request contract:

```json
{
  "script": "完整口播文案",
  "image_count": 0,
  "ratio": "3:4",
  "style": "finance_documentary",
  "custom_style": "",
  "concurrency": 3,
  "text_model": "gpt-5.6-sol",
  "reasoning_effort": "medium",
  "image_model": "gpt-image-2",
  "image_attempts": 2
}
```

- [ ] **Step 2: Run the quick handler tests and verify failure**

Run:

```powershell
go test ./internal/httpapi -run 'TestImageProjectsQuick|TestImageProjectsResume' -count=1
```

Expected: 404 or compilation failure because the routes and job guard do not exist.

- [ ] **Step 3: Add the per-project job guard**

Implement this complete concurrency boundary in `imageproject_jobs.go`:

```go
type imageProjectJobs struct {
	mu     sync.Mutex
	active map[string]struct{}
}

func newImageProjectJobs() *imageProjectJobs {
	return &imageProjectJobs{active: make(map[string]struct{})}
}

func (j *imageProjectJobs) Start(projectID string, run func()) bool {
	j.mu.Lock()
	if _, exists := j.active[projectID]; exists {
		j.mu.Unlock()
		return false
	}
	j.active[projectID] = struct{}{}
	j.mu.Unlock()
	go func() {
		defer func() {
			j.mu.Lock()
			delete(j.active, projectID)
			j.mu.Unlock()
		}()
		run()
	}()
	return true
}
```

Add `jobs *imageProjectJobs` to `imageProjectsHandler`. Initialize it once per handler. Call `MarkRunningQuickProjectsInterrupted` during construction before serving requests.

- [ ] **Step 4: Implement quick creation and phase orchestration**

Register:

```go
mux.HandleFunc("POST /api/image-projects/quick-generate", h.quickGenerate)
mux.HandleFunc("POST /api/image-projects/{id}/resume", h.resumeQuickGenerate)
mux.HandleFunc("PATCH /api/image-projects/{id}", h.updateProjectTitle)
```

`quickGenerate` must validate the same script/ratio/style/concurrency/reasoning rules as manual creation plus `image_attempts` 1–4. It creates a project with a deterministic first-sentence title, provisional `ImageCount: 1`, `Status: "draft"`, quick run state, and persisted model selections. Only after `repo.Create` succeeds may it call `jobs.Start`. Respond with:

```go
writeJSON(w, http.StatusAccepted, map[string]string{
	"project_id": project.ID,
	"run_status": "running",
})
```

Implement `runQuickProject` as a checkpoint switch:

```go
switch project.RunPhase {
case "planning":
	err = h.runQuickPlanning(ctx, project)
case "prompting":
	err = h.runQuickPrompting(ctx, project, items)
case "imaging":
	err = h.runQuickImaging(ctx, project, items)
case "completed":
	return
default:
	err = errors.New("image project phase is invalid")
}
```

After each successful phase, reload the project before continuing. Planning calls `SuggestQuickPlan` once and `SaveQuickPlan`; prompting reconstructs ordered `imageproject.Segment` values from saved items, calls `SuggestPrompts`, and `SavePrompts`; imaging calls the shared `generate` method only for non-ready items, aggregates counts, and sets phase/status to completed even when some items failed.

On a fatal phase error, persist the current phase, `run_status="failed"`, and the exact safe error text. Never put API keys or full HTTP authorization values into errors.

`resumeQuickGenerate` permits only quick projects with `run_status` failed or interrupted. It clears `phase_error`, marks running, and starts the job guard. Running projects return 409; completed projects return 200 without new work.

`updateProjectTitle` accepts `{"title":"用户修改后的名称"}`, validates the same 120-rune project-title limit as manual creation, calls `UpdateProjectTitle`, and returns the refreshed project detail.

- [ ] **Step 5: Run backend orchestration tests**

Run:

```powershell
go test ./internal/httpapi ./internal/store ./internal/imageproject -count=1
```

Expected: all packages pass; asynchronous tests poll persisted state with bounded deadlines and never sleep for an unbounded duration.

- [ ] **Step 6: Commit backend orchestration**

```powershell
git add internal/httpapi/imageproject_jobs.go internal/httpapi/imageproject_quick.go internal/httpapi/imageproject_quick_test.go internal/httpapi/imageprojects.go internal/app/app.go
git commit -m "feat: orchestrate quick image projects"
```

## Task 6: Add advanced routing, typed run state, polling, and the 404 fix

**Files:**
- Modify: `web/src/types.ts:74-156`
- Modify: `web/src/project-workbench/routes.ts:1-25`
- Modify: `web/src/project-workbench/routes.test.ts:6-29`
- Modify: `web/src/App.tsx:91-99,950-960`
- Modify: `web/src/App.test.tsx`
- Modify: `web/src/image-mode/ImageModeWorkbench.tsx:8-20,151-209,431-498`
- Modify: `web/src/image-mode/ImageModeWorkbench.test.tsx:32-37`

- [ ] **Step 1: Write failing route, loading, and polling tests**

Extend route tests:

```ts
expect(parseLocation("/image-projects/advanced")).toEqual({ view: "image-projects-advanced" });
```

Add workbench tests that:

- Click a project once, update the parent `initialProjectID`, and assert exactly one GET for that ID.
- Start with a successful detail, return a transient 404 from a stale duplicate, and assert the successful detail remains visible.
- Return `run_status: "running"`, advance the fake timers, and assert a quiet refresh updates `success_count` without replacing the page with a loading screen.

- [ ] **Step 2: Run focused frontend tests and verify failure**

Run:

```powershell
Set-Location web
npm test -- --run src/project-workbench/routes.test.ts src/image-mode/ImageModeWorkbench.test.tsx src/App.test.tsx
```

Expected: advanced route is not recognized and the duplicate-load/polling assertions fail.

- [ ] **Step 3: Add TypeScript contracts and route mode**

Add:

```ts
export type ImageRunPhase = "idle" | "planning" | "prompting" | "imaging" | "completed";
export type ImageRunStatus = "idle" | "running" | "failed" | "completed" | "interrupted";

export type QuickImageProjectRequest = {
  script: string;
  image_count: number;
  ratio: ImageProject["ratio"];
  style: string;
  custom_style: string;
  concurrency: number;
  text_model: string;
  reasoning_effort: ReasoningEffort | "";
  image_model: string;
  image_attempts: number;
};

export type QuickImageProjectResponse = {
  project_id: string;
  run_status: ImageRunStatus;
};
```

Extend `ImageProject` with the persisted run fields and `ImageProjectItem` with `attempt_count`. Add the `image-projects-advanced` route before the UUID matcher. In `App`, include it in `imageRoute` and pass `mode="advanced"`; all other image list/detail routes use `mode="quick"`.

- [ ] **Step 4: Make the URL the single project-loading source**

Change list clicks so navigation happens before fetching:

```ts
const openProject = (project: ImageProject) => {
  if (onProjectOpen) {
    onProjectOpen(project.id);
    return;
  }
  void loadProject(project.id);
};
```

Do not call `loadProject(id, true)`. Track the latest loaded detail in a ref. A 404 may set `error404` only when no valid detail for that same ID is held. Add a quiet `refreshProject(id)` that updates detail without clearing it or setting route loading.

Poll every 1500ms only while `detail.project.run_status === "running"`; clear the interval on ID/status change and component unmount. A completed, failed, or interrupted response stops polling.

- [ ] **Step 5: Run route and workbench tests**

Run:

```powershell
Set-Location web
npm test -- --run src/project-workbench/routes.test.ts src/image-mode/ImageModeWorkbench.test.tsx src/App.test.tsx
```

Expected: all focused tests pass with one detail GET per navigation and stable UI during polling.

- [ ] **Step 6: Commit route and recovery behavior**

```powershell
git add web/src/types.ts web/src/project-workbench/routes.ts web/src/project-workbench/routes.test.ts web/src/App.tsx web/src/App.test.tsx web/src/image-mode/ImageModeWorkbench.tsx web/src/image-mode/ImageModeWorkbench.test.tsx
git commit -m "fix: restore image projects from their route"
```

## Task 7: Build the default focus workbench and progress experience

**Files:**
- Create: `web/src/image-mode/QuickGenerateForm.tsx`
- Create: `web/src/image-mode/QuickGenerateForm.test.tsx`
- Modify: `web/src/image-mode/ImageModeWorkbench.tsx:73-125,431-553`
- Modify: `web/src/image-mode/ImageModeWorkbench.test.tsx`
- Modify: `web/src/App.tsx:950-960`
- Modify: `web/src/image-mode/image-mode.css:1-111,180-230,300-332`

- [ ] **Step 1: Write failing quick-form tests**

Test that the form renders no project-name field, uses all supplied defaults, exposes image attempts under an expandable “高级参数”, submits the exact quick payload, disables submission for blank script or blank custom style, and calls `onCreated(project_id)` for a 202 response.

```ts
expect(JSON.parse(String(request?.body))).toEqual({
  script: "完整文案",
  image_count: 0,
  ratio: "3:4",
  style: "finance_documentary",
  custom_style: "",
  concurrency: 3,
  text_model: "gpt-5.6-sol",
  reasoning_effort: "medium",
  image_model: "gpt-image-2",
  image_attempts: 2,
});
```

- [ ] **Step 2: Run the new component tests and verify failure**

Run:

```powershell
Set-Location web
npm test -- --run src/image-mode/QuickGenerateForm.test.tsx
```

Expected: the module does not exist.

- [ ] **Step 3: Implement `QuickGenerateForm`**

Use this prop boundary:

```ts
type QuickGenerateFormProps = {
  api: API;
  defaultRatio: ImageProject["ratio"];
  defaultStyle: string;
  defaultConcurrency: number;
  defaultTextModel: string;
  defaultReasoningEffort: ReasoningEffort | "";
  defaultImageModel: string;
  defaultImageAttempts: number;
  onCreated: (projectID: string) => void;
  onAdvancedMode: () => void;
};
```

The layout contains one large script textarea, the compact right-side parameter rail, one primary “开始生成图片” button, and a text-link button to advanced manual mode. Keep model inputs editable. Clamp concurrency to 1–18 and attempts to 1–4 before submission. Parse API error `{code,message}` and show the server message with the stage-independent request failure label.

- [ ] **Step 4: Integrate quick/manual mode and project progress**

Add `mode: "quick" | "advanced"`, `defaultImageModel`, and `defaultImageAttempts` to `ImageModeWorkbench` props. Render `QuickGenerateForm` when no detail is open and mode is quick; render the existing segment-confirmation form when mode is advanced. Keep the existing project list available in both modes. Initialize the manual draft from `defaultImageAttempts`, include `image_attempts` in both segment-preview/create payload helpers, and expose the same 1–4 selector in the manual parameter grid so every newly created project persists its chosen total-attempt value.

For quick projects, render a four-stage rail derived from `run_phase`, plus `生成图片 {success_count + failure_count}/{image_count}` during imaging. Show a “继续生成” button for failed/interrupted runs and POST `/resume`. Allow the AI-generated project title to be edited in place; saving sends `PATCH /api/image-projects/{id}` and updates the loaded detail. On completed quick projects, hide source text and prompt editors by default behind a single “显示高级编辑” toggle; always keep image title, error text, attempts, and single-image regeneration visible.

- [ ] **Step 5: Apply the confirmed visual layout**

Implement a two-column desktop grid with the script surface wider than the parameter rail, cold-gray canvas, white content surfaces, teal primary action, minimum 44px controls, visible keyboard focus, and a single-column breakpoint without horizontal overflow. Do not add gradients, emoji, or nested decorative cards.

- [ ] **Step 6: Run quick-form and workbench tests**

Run:

```powershell
Set-Location web
npm test -- --run src/image-mode/QuickGenerateForm.test.tsx src/image-mode/ImageModeWorkbench.test.tsx src/App.test.tsx
npm run typecheck
```

Expected: tests and typecheck pass.

- [ ] **Step 7: Commit the quick workbench**

```powershell
git add web/src/image-mode/QuickGenerateForm.tsx web/src/image-mode/QuickGenerateForm.test.tsx web/src/image-mode/ImageModeWorkbench.tsx web/src/image-mode/ImageModeWorkbench.test.tsx web/src/App.tsx web/src/image-mode/image-mode.css
git commit -m "feat: add one-click image workbench"
```

## Task 8: Replace the publishing dialog with the confirmed two-column editor

**Files:**
- Create: `web/src/image-mode/PublishingDialog.tsx`
- Create: `web/src/image-mode/PublishingDialog.test.tsx`
- Modify: `web/src/image-mode/ImageModeWorkbench.tsx:63-70,438-497`
- Modify: `web/src/image-mode/ImageModeWorkbench.test.tsx:44-63`
- Modify: `web/src/image-mode/image-mode.css:236-261`

- [ ] **Step 1: Write failing dialog tests**

Test five candidate rows, selected-candidate editing, Unicode title length 22/23, description length 1000/1001, separate copy-title/copy-description/copy-all actions, PATCH then select POST, escape-to-close, focus behavior, and the empty-state generate action. Use descriptions that end with 3–5 hashtags so the UI preserves them verbatim.

- [ ] **Step 2: Run dialog tests and verify failure**

Run:

```powershell
Set-Location web
npm test -- --run src/image-mode/PublishingDialog.test.tsx
```

Expected: the extracted component does not exist.

- [ ] **Step 3: Implement and integrate the dialog**

Use this component boundary:

```ts
type PublishingDialogProps = {
  api: API;
  projectID: string;
  candidates: PublishingCandidate[];
  selected: number;
  publishingError?: string;
  onClose: () => void;
  onSaved: (candidates: PublishingCandidate[], selected: number) => void;
};
```

The left column is a five-row candidate list showing position, title, and description excerpt. The right column contains full title/description editors and live counts. Footer actions are “复制标题”, “复制描述”, “复制全部”, and teal “保存当前候选”. Preserve the actual selected position in `onSaved`; do not reset to candidate 1 after saving.

When candidates are absent, show `publishingError` and a “重新生成发布文案” action that calls the existing generate endpoint. The backend remains the source of topic validation; the textarea edits and saves the complete description including hashtags.

- [ ] **Step 4: Apply responsive and accessible styles**

Set `.publishing-dialog` to `width:min(960px,calc(100vw - 32px))`. Use a 280px/flexible two-column body, sticky footer actions where needed, minimum 44px controls, `aria-modal`, labelled dialog title, visible focus, and a single-column mobile layout. Use Lucide `Copy`, `Save`, and `X` icons with text labels; do not use emoji.

- [ ] **Step 5: Run publishing and workbench tests**

Run:

```powershell
Set-Location web
npm test -- --run src/image-mode/PublishingDialog.test.tsx src/image-mode/ImageModeWorkbench.test.tsx
npm run typecheck
```

Expected: all tests and typecheck pass.

- [ ] **Step 6: Commit publishing UI**

```powershell
git add web/src/image-mode/PublishingDialog.tsx web/src/image-mode/PublishingDialog.test.tsx web/src/image-mode/ImageModeWorkbench.tsx web/src/image-mode/ImageModeWorkbench.test.tsx web/src/image-mode/image-mode.css
git commit -m "feat: redesign image publishing copy editor"
```

## Task 9: Verify full flows, rebuild embedded assets, and update documentation

**Files:**
- Modify: `web/e2e/image-mode.spec.ts:106-170`
- Modify: `docs/ARCHITECTURE.md`
- Modify: `docs/USER-GUIDE.md`
- Modify: `docs/AI-HANDOFF.md`
- Modify: `internal/webui/dist/index.html`
- Modify/Create: `internal/webui/dist/assets/*`

- [ ] **Step 1: Add end-to-end coverage before rebuilding**

Add a quick-flow test that stubs 202 creation followed by planning, imaging, and partial completion detail responses; refresh `/image-projects/{id}` and assert the same project remains visible. Assert the publishing editor opens before the download button and includes hashtags. Add an advanced-route test that verifies the manual segment-preview/create flow remains available.

The partial completion fixture must contain one ready item and one failed item:

```ts
project: {
  status: "partial",
  run_mode: "quick",
  run_phase: "completed",
  run_status: "completed",
  image_count: 2,
  success_count: 1,
  failure_count: 1,
  image_attempts: 2,
}
```

- [ ] **Step 2: Run all backend tests**

Run:

```powershell
go test ./...
```

Expected: every Go package passes.

- [ ] **Step 3: Run all frontend checks**

Run:

```powershell
Set-Location web
npm run lint
npm test
npm run typecheck
npm run build:verify
npm run test:e2e -- image-mode.spec.ts
```

Expected: lint, Vitest, typecheck, temporary production build, and image-mode Playwright tests pass.

- [ ] **Step 4: Update operator and user documentation**

Document:

- `/image-projects` quick mode, `/image-projects/advanced` manual mode, and `/image-projects/{id}` refresh behavior.
- `image_generation_attempts` means total attempts including the first request, default 2, range 1–4.
- Quick phases, partial-success semantics, first-URL selection, and stage-specific retry actions.
- In-process jobs survive browser disconnects; a service restart marks active jobs interrupted and requires “继续生成”.
- Publishing descriptions contain 3–5 AI-generated content-related hashtags.

- [ ] **Step 5: Rebuild the embedded frontend and verify the embed**

Run:

```powershell
Set-Location web
npm run build:embed
Set-Location ..
go test ./internal/webui ./internal/app
```

Expected: Vite writes the current hashed assets into `internal/webui/dist`; embed and app tests pass.

- [ ] **Step 6: Inspect the final diff for unrelated changes**

Run:

```powershell
git status --short
git diff --check
git diff --stat
```

Expected: no whitespace errors. Preserve all pre-existing user changes and do not stage unrelated files.

- [ ] **Step 7: Commit integration, docs, and generated assets**

```powershell
git add web/e2e/image-mode.spec.ts docs/ARCHITECTURE.md docs/USER-GUIDE.md docs/AI-HANDOFF.md internal/webui/dist
git commit -m "docs: document quick image generation workflow"
```

## Final acceptance checklist

- [ ] A user can paste only a script and start generation using defaults.
- [ ] The AI supplies the project name; the user is not forced to type one.
- [ ] The generated project name can be edited and saved after creation.
- [ ] Planning, prompting, and imaging continue after browser refresh or disconnect.
- [ ] Refreshing `/image-projects/{id}` never returns to the home page.
- [ ] Opening a project makes one authoritative detail request and does not show a false 404.
- [ ] Setting attempts to 2 produces at most two calls per image item.
- [ ] Transient failures retry; permanent failures and cancellation do not.
- [ ] Multiple returned URLs always select the first result.
- [ ] A partially successful project remains usable and exposes failed-item regeneration.
- [ ] Quick mode hides segment/prompt editing by default; advanced mode retains the original workflow.
- [ ] The publishing editor is a wide two-column layout with five candidates.
- [ ] Every AI publishing description ends with 3–5 relevant hashtags.
- [ ] Go tests, frontend tests, typecheck, lint, production build, embed tests, and image-mode E2E tests pass.
