# Video Console Workflow V2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a secure local video-production console that supports pre-project topic conversations, versioned project assets, inspectable multi-turn Codex CLI tasks, configurable local dependencies, and deterministic handoff to the three installed finance production Skills.

**Architecture:** Keep the existing Go + SQLite + embedded React application as one modular monolith. Add explicit repositories and services for authentication, settings, ideas, assets, task manifests, Skill fingerprints, and Codex events; keep files in a managed data root and connect modules through typed result envelopes rather than prompt conventions.

**Tech Stack:** Go 1.25, `net/http`, SQLite via `modernc.org/sqlite`, React 19, TypeScript 6, TanStack Query, Vitest, Testing Library, Python 3 `unittest`, Codex CLI JSONL, Windows DPAPI, local Obsidian and baokuan MCP.

---

## Working rules

- Worktree root: `C:\Users\prepare\.codex\worktrees\视频号混剪\video-production-console`.
- Go module root: `C:\Users\prepare\.codex\worktrees\视频号混剪\video-production-console\video-production-console`.
- Approved specification: `video-production-console/docs/superpowers/specs/2026-08-03-video-console-workflow-v2-design.md`.
- Preserve all existing untracked logs, `dist/`, `.superpowers/`, and `video-console-data/`; do not stage them.
- Use `apply_patch` for file edits.
- Use test-first changes. A task is complete only after its focused tests and the listed regression command pass.
- Do not launch WeChat Channels. Do not launch Jianying or perform Jianying UI automation.
- Before Tasks 13–15, read `superpowers:writing-skills`; before every implementation task, follow `superpowers:test-driven-development`.
- Commit only the files named by the current task. Installed Skill directories are outside this Git repository; their contract tests are the completion record for those edits.

## Planned file structure

### Repository root and shared schemas

- Create `../.gitignore`: ignore generated console data, temporary builds, logs, Visual Companion files, frontend dependencies, and review transcripts.
- Create `schemas/task-manifest.schema.json`: authoritative task input contract.
- Replace `schemas/codex-result.schema.json`: versioned result envelope with questions, engineering artifacts, and formal asset outputs.
- Create `schemas/topic-candidates.schema.json`: 3–5 candidate topic result contract.
- Create `schemas/settings.schema.json`: public setting keys, secret markers, and validation limits.

### Go domain and persistence

- Modify `internal/domain/models.go`: add V2 task fields and normalized asset names; keep account and project core fields.
- Modify `internal/domain/stages.go`: add publication-status rules while retaining the legacy stage gate until project APIs migrate in Task 13.
- Create `internal/domain/assets.go`: logical assets, versions, dependency states, storage kinds.
- Create `internal/domain/requirements.go`: action-specific readiness evaluator.
- Create `internal/domain/ideas.go`: idea sessions, messages, and candidates.
- Create `internal/domain/settings.go`: typed public settings, secret status, Skill snapshot types.
- Modify `internal/store/migrations.go`: transactional V2 schema and legacy data conversion.
- Create `internal/store/backup.go`: pre-migration SQLite snapshot and data-root manifest.
- Create `internal/store/assets_v2.go`: logical assets, versions, current pointers, dependencies, stale propagation.
- Create `internal/store/auth.go`: administrator and session persistence.
- Create `internal/store/settings.go`: public settings, encrypted secrets, configuration versions.
- Create `internal/store/ideas.go`: pre-project conversation persistence.
- Create `internal/store/skills.go`: Skill fingerprints and task snapshots.
- Expand `internal/store/tasks.go`: task snapshots, messages, artifacts, relations, replay cursors, interrupted recovery.
- Modify `internal/store/projects.go`: V2 asset queries, project-derived account ownership, topic-card import.

### Go services and HTTP

- Create `internal/security/password.go`, `internal/security/session.go`, `internal/security/redact.go`.
- Create `internal/security/secrets_windows.go` and `internal/security/secrets_other.go`.
- Create `internal/auth/service.go` and `internal/auth/middleware.go`.
- Create `internal/settings/service.go`.
- Create `internal/skillregistry/service.go`.
- Create `internal/requirements/service.go`.
- Create `internal/ideas/service.go`.
- Create `internal/codex/manifest.go`, `internal/codex/result.go`, `internal/codex/result_validator.go`.
- Modify `internal/codex/prompt.go`, `command.go`, `events.go`, `runner.go`, and `scheduler.go`.
- Replace WebSocket delivery in `internal/realtime/hub.go` with authenticated SSE and cursor replay.
- Create `internal/httpapi/auth.go`, `settings.go`, `skills.go`, `ideas.go`, `events.go`.
- Modify `internal/httpapi/projects.go`, `assets.go`, `tasks.go`, `accounts.go`, and `dependencies.go`.
- Refactor `internal/app/app.go` into protected API route groups.
- Modify `cmd/console/main.go` to load database settings, construct command factories, and run recovery.

### React application

- Create `web/src/types.ts`, `web/src/api/client.ts`, `web/src/api/queries.ts`.
- Create `web/src/app/AuthProvider.tsx`, `web/src/app/AppShell.tsx`, `web/src/app/routes.tsx`.
- Create pages: `LoginPage.tsx`, `HomePage.tsx`, `IdeasPage.tsx`, `ProjectsPage.tsx`, `ProjectWorkspacePage.tsx`, `TaskDetailPage.tsx`, `SettingsPage.tsx`.
- Create components: `ProjectBoard.tsx`, `AssetTree.tsx`, `AssetPreview.tsx`, `TaskConversation.tsx`, `TaskTechnicalLog.tsx`, `RequirementBanner.tsx`, `HealthBadge.tsx`.
- Replace `web/src/App.tsx` with the application providers/router.
- Split `web/src/App.css` into `web/src/styles/tokens.css`, `layout.css`, `components.css`, and `responsive.css`.
- Add `web/src/test/setup.ts` and focused `*.test.tsx` files beside components/pages.
- Modify `web/package.json`, `web/vite.config.ts`, and `web/src/main.tsx`.

### Installed Skill contracts

- Modify `C:\Users\prepare\.codex\skills\finance-topic-selector\SKILL.md`, its Obsidian reference, validator, and new contract tests.
- Modify `C:\Users\prepare\.codex\skills\finance-viral-remix\SKILL.md`, output/external/spoken/topic-card references, and new contract tests.
- Modify `C:\Users\prepare\.codex\skills\jianying-montage-draft\SKILL.md`, plan/default/concurrency references, templates, and contract tests.
- Create `C:\Users\prepare\.codex\skills\jianying-montage-draft\scripts\run_montage_job.py` and `tests\test_run_montage_job.py`.

## Phase A — persistence, security, and runtime contracts

### Task 1: Establish a clean, reproducible baseline

**Files:**
- Create: `../.gitignore`
- Modify: `README.md`
- Test: existing Go, frontend, and Python test suites

- [ ] **Step 1: Record the current baseline without changing files**

Run:

```powershell
go test ./...
npm --prefix web run lint
npm --prefix web run build
python -m unittest discover 'C:\Users\prepare\.codex\skills\jianying-montage-draft\tests' -v
```

Expected: all commands exit `0`. Save any pre-existing failure in the task commentary and do not hide it with unrelated edits.

- [ ] **Step 2: Add exact ignore rules**

Create `../.gitignore` with:

```gitignore
/task*-last.txt
/video-production-console/.superpowers/
/video-production-console/dist/
/video-production-console/video-console-data/
/video-production-console/*.log
/video-production-console/web/node_modules/
/video-production-console/web/coverage/
**/__pycache__/
*.pyc
```

- [ ] **Step 3: Document local prerequisites**

Add a `Workflow V2 prerequisites` section to `README.md` containing these verified values and setup checks:

```markdown
## Workflow V2 prerequisites

- Codex CLI must resolve from `codex --version`.
- Installed Skills live under `C:\Users\prepare\.codex\skills`.
- `baokuan` MCP must appear enabled in `codex mcp list` and its HTTP service must answer at `http://127.0.0.1:2022`.
- The Obsidian vault and topic-card directory are configured in the console Settings page.
- The console listens on `0.0.0.0:2030` only with administrator authentication enabled.
- Automated verification does not open WeChat Channels or Jianying.
```

- [ ] **Step 4: Verify only intentional files are visible**

Run:

```powershell
git status --short
```

Expected: `../.gitignore` and `README.md` are visible; runtime data, logs, `.superpowers`, `dist`, Python caches, and review transcripts are absent.

- [ ] **Step 5: Commit**

```powershell
git add ../.gitignore README.md
git commit -m "chore: define console v2 workspace hygiene"
```

### Task 2: Define V2 domain types and action readiness

**Files:**
- Modify: `internal/domain/models.go:18-115`
- Modify: `internal/domain/stages.go`
- Create: `internal/domain/assets.go`
- Create: `internal/domain/requirements.go`
- Create: `internal/domain/ideas.go`
- Create: `internal/domain/settings.go`
- Test: `internal/domain/requirements_test.go`
- Test: `internal/domain/assets_test.go`

- [ ] **Step 1: Write failing requirements tests**

Create `internal/domain/requirements_test.go` with table cases that assert these exact outcomes:

```go
func TestEvaluateActionRequirements(t *testing.T) {
	ready := func(types ...AssetType) map[AssetType]AssetState {
		out := map[AssetType]AssetState{}
		for _, typ := range types { out[typ] = AssetReady }
		return out
	}
	tests := []struct {
		name string
		action TaskAction
		assets map[AssetType]AssetState
		deps DependencyHealth
		wantReady bool
		wantMissing []string
	}{
		{"empty remix", ActionRemixStandard, ready(), DependencyHealth{}, false, []string{"source_script"}},
		{"direct remix", ActionRemixStandard, ready(AssetSourceScript), DependencyHealth{}, true, nil},
		{"spoken", ActionSpokenFormat, ready(AssetContinuousScript), DependencyHealth{}, true, nil},
		{"montage missing", ActionMontagePlan, ready(AssetSpokenScript), DependencyHealth{Montage: true}, false, []string{"narration", "subtitle_srt", "account_background"}},
		{"montage ready", ActionMontagePlan, ready(AssetSpokenScript, AssetNarration, AssetSubtitleSRT, AssetAccountBackground), DependencyHealth{Montage: true}, true, nil},
		{"topic deps", ActionTopicBrainstorm, ready(), DependencyHealth{Baokuan: false, ObsidianRead: true}, false, []string{"baokuan_mcp"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EvaluateAction(tt.action, tt.assets, tt.deps)
			if got.Ready != tt.wantReady || !slices.Equal(got.MissingInputs, tt.wantMissing) {
				t.Fatalf("got %#v", got)
			}
		})
	}
}
```

Create `internal/domain/assets_test.go` asserting the invalidation rules from the specification, including `continuous_script → spoken_script,narration,subtitle_srt,mix_draft,final_video`.

- [ ] **Step 2: Run the focused tests and confirm they fail**

Run:

```powershell
go test ./internal/domain -run 'TestEvaluateActionRequirements|TestInvalidatedAssetTypes' -v
```

Expected: compile failure because `TaskAction`, `AssetState`, and `EvaluateAction` do not exist.

- [ ] **Step 3: Add exact normalized domain constants**

Add these definitions in the new files:

```go
type AssetType string
const (
	AssetSourceScript AssetType = "source_script"
	AssetTopicCard AssetType = "topic_card"
	AssetContinuousScript AssetType = "continuous_script"
	AssetSpokenScript AssetType = "spoken_script"
	AssetNarration AssetType = "narration"
	AssetSubtitleSRT AssetType = "subtitle_srt"
	AssetAccountBackground AssetType = "account_background"
	AssetMixDraft AssetType = "mix_draft"
	AssetFinalVideo AssetType = "final_video"
)

type AssetState string
const (
	AssetMissing AssetState = "missing"
	AssetReady AssetState = "ready"
	AssetStale AssetState = "stale"
	AssetGenerating AssetState = "generating"
	AssetFailed AssetState = "failed"
)

type ProjectStatus string
const (
	ProjectDraft ProjectStatus = "draft"
	ProjectProducing ProjectStatus = "producing"
	ProjectReadyToPublish ProjectStatus = "ready_to_publish"
	ProjectPublished ProjectStatus = "published"
	ProjectArchived ProjectStatus = "archived"
)

type TaskAction string
const (
	ActionTopicBrainstorm TaskAction = "topic.brainstorm"
	ActionTopicCommit TaskAction = "topic.commit"
	ActionTopicDeepen TaskAction = "topic.deepen"
	ActionRemixStandard TaskAction = "remix.standard"
	ActionRemixEnhanced TaskAction = "remix.enhanced"
	ActionRemixFromTopic TaskAction = "remix.from_topic_card"
	ActionSpokenFormat TaskAction = "remix.spoken_format"
	ActionRemixReview TaskAction = "remix.review"
	ActionMontagePlan TaskAction = "montage.plan"
	ActionMontageExecute TaskAction = "montage.execute"
)

type TaskStatus string
const (
	TaskQueued TaskStatus = "queued"
	TaskRunning TaskStatus = "running"
	TaskAwaitingInput TaskStatus = "awaiting_input"
	TaskResuming TaskStatus = "resuming"
	TaskCompleted TaskStatus = "completed"
	TaskFailed TaskStatus = "failed"
	TaskCanceled TaskStatus = "canceled"
	TaskInterrupted TaskStatus = "interrupted"
)
```

Retain deprecated `TaskWaitingInput="waiting_input"`, `TaskCancelled="cancelled"`, `AssetAudio="audio"`, and `AssetSubtitle="subtitle"` only so Tasks 2–12 remain buildable while repositories migrate; Task 13 removes their production use and Task 23 verifies no API emits them. Introduce `Project.Status` beside legacy `Project.Stage`; Task 13 removes `Stage` after all project handlers/repositories switch. Derive the current production step from assets/tasks rather than persisting it.

- [ ] **Step 4: Implement the readiness table and invalidation graph**

Create immutable maps in `requirements.go` and `assets.go`:

```go
var actionAssets = map[TaskAction][]AssetType{
	ActionRemixStandard: {AssetSourceScript},
	ActionRemixEnhanced: {AssetSourceScript},
	ActionRemixFromTopic: {AssetTopicCard},
	ActionSpokenFormat: {AssetContinuousScript},
	ActionRemixReview: {AssetContinuousScript},
	ActionMontagePlan: {AssetSpokenScript, AssetNarration, AssetSubtitleSRT, AssetAccountBackground},
	ActionMontageExecute: {AssetSpokenScript, AssetNarration, AssetSubtitleSRT, AssetAccountBackground},
}

var invalidates = map[AssetType][]AssetType{
	AssetSourceScript: {AssetContinuousScript, AssetSpokenScript, AssetNarration, AssetSubtitleSRT, AssetMixDraft, AssetFinalVideo},
	AssetTopicCard: {AssetContinuousScript, AssetSpokenScript, AssetNarration, AssetSubtitleSRT, AssetMixDraft, AssetFinalVideo},
	AssetContinuousScript: {AssetSpokenScript, AssetNarration, AssetSubtitleSRT, AssetMixDraft, AssetFinalVideo},
	AssetSpokenScript: {AssetNarration, AssetSubtitleSRT, AssetMixDraft, AssetFinalVideo},
	AssetNarration: {AssetSubtitleSRT, AssetMixDraft, AssetFinalVideo},
	AssetSubtitleSRT: {AssetMixDraft, AssetFinalVideo},
	AssetAccountBackground: {AssetMixDraft, AssetFinalVideo},
	AssetMixDraft: {AssetFinalVideo},
}
```

`EvaluateAction` must sort `MissingInputs` in the required order, treat `stale`, `generating`, `failed`, and absent assets as unavailable, and append dependency names for topic and montage actions.

- [ ] **Step 5: Add publication movement without breaking the legacy handler**

Add `CanMovePublicationStatus` for `draft → producing → ready_to_publish → published` plus archive rules. Keep legacy `CanMove` unchanged until Task 13 moves the HTTP handler to action-specific requirements; this keeps every intermediate commit green.

- [ ] **Step 6: Run tests**

```powershell
go test ./internal/domain -v
```

Expected: PASS, including the old stage transition tests after updating them to assert publication movement only.

- [ ] **Step 7: Commit**

```powershell
git add internal/domain
git commit -m "feat: define console v2 domain contracts"
```

### Task 3: Add migration backup and the V2 SQLite schema

**Files:**
- Modify: `internal/store/db.go:13-46`
- Modify: `internal/store/migrations.go:9-144`
- Create: `internal/store/backup.go`
- Test: `internal/store/backup_test.go`
- Modify: `internal/store/migrations_test.go`

- [ ] **Step 1: Write failing backup and migration tests**

Add tests that create a V2-predecessor database, insert one account, project, asset, waiting task, event, and message, then call `OpenWithOptions`. The physical `asset_items` table implements the design's logical asset entity while the old `assets` table remains a read-only migration source. Assert:

```go
if got := tableCount(t, db, "asset_versions"); got != 1 { t.Fatalf("asset_versions=%d", got) }
if got := scalar(t, db, `SELECT status FROM codex_tasks LIMIT 1`); got != "awaiting_input" { t.Fatalf("status=%s", got) }
if got := scalar(t, db, `SELECT type FROM asset_items LIMIT 1`); got != "narration" { t.Fatalf("type=%s", got) }
if len(mustGlob(t, filepath.Join(backupDir, "console-*.db"))) != 1 { t.Fatal("missing sqlite backup") }
if len(mustGlob(t, filepath.Join(backupDir, "data-manifest-*.json"))) != 1 { t.Fatal("missing data manifest") }
```

- [ ] **Step 2: Run the focused tests and confirm failure**

```powershell
go test ./internal/store -run 'TestOpenBacksUpBeforeV2Migration|TestV2MigrationBackfillsAssetsAndTasks' -v
```

Expected: FAIL because `OpenWithOptions`, `asset_versions`, and V2 task statuses do not exist.

- [ ] **Step 3: Implement consistent pre-migration backup**

Create:

```go
type OpenOptions struct {
	Path string
	DataRoot string
	BackupDir string
	Now func() time.Time
}

func Open(path string) (*sql.DB, error) {
	return OpenWithOptions(OpenOptions{Path: path, DataRoot: filepath.Dir(path), BackupDir: filepath.Join(filepath.Dir(path), "backups"), Now: time.Now})
}
```

`OpenWithOptions` must open SQLite, inspect the highest schema version, and call `backupDatabase` only when an existing database requires a newer migration. `backupDatabase` must run `VACUUM INTO` to a timestamped file and write a JSON array of `{path,size,mod_time}` for files under `DataRoot`, excluding `backups` itself.

- [ ] **Step 4: Add one transactional V2 migration**

Append a migration that performs these exact data transformations:

```text
assets(old rows) → remain unchanged as the legacy migration source
new asset_items → one logical row per project/account/type
new asset_versions → one row per legacy assets row
audio → narration
subtitle → subtitle_srt
active → ready
codex_tasks waiting_input → awaiting_input; cancelled → canceled
task_messages role check expands to user/assistant/system
```

The migration must create `admins`, `auth_sessions`, `encrypted_secrets`, `skill_snapshots`, `idea_sessions`, `idea_messages`, `idea_candidates`, `asset_items`, `asset_versions`, `asset_dependencies`, expanded `codex_tasks`, `task_messages`, `task_events`, `task_artifacts`, and `task_relations`. Add `projects.publication_status` and map `topic/script/assets → draft`, `mixing/review → producing`, `ready → ready_to_publish`, retaining `published/archived`. Add `accounts.background_asset_item_id` for the V2 logical background while preserving the old foreign key during migration. The rebuilt task table accepts legacy `waiting_input` for imported rows and all V2 states; repositories normalize API output to `awaiting_input`. Use this UUID SQL expression for backfilled version IDs:

```sql
lower(hex(randomblob(4))) || '-' || lower(hex(randomblob(2))) || '-4' ||
substr(lower(hex(randomblob(2))),2) || '-' ||
substr('89ab',abs(random()) % 4 + 1,1) || substr(lower(hex(randomblob(2))),2) || '-' ||
lower(hex(randomblob(6)))
```

Keep the old `assets` rows indefinitely as the migration audit source; new repositories write only `asset_items` and `asset_versions`. No production file or legacy row is deleted by SQL.

- [ ] **Step 5: Seed safe defaults**

The migration must insert:

```sql
INSERT INTO settings(key,value) VALUES
('max_codex_concurrency','2'),
('listen_addr','0.0.0.0:2030'),
('baokuan_base_url','http://127.0.0.1:2022'),
('codex_binary_path','codex')
ON CONFLICT(key) DO NOTHING;
```

Do not seed any API key. Administrator seeding belongs to Task 5 so the migration never contains a plaintext password.

- [ ] **Step 6: Run migration tests and regression tests**

```powershell
go test ./internal/store -run 'TestOpenBacksUpBeforeV2Migration|TestV2MigrationBackfillsAssetsAndTasks|TestMigrations' -v
go test ./internal/store ./internal/domain
```

Expected: PASS. A second open must create no second backup because no migration remains.

- [ ] **Step 7: Commit**

```powershell
git add internal/store/db.go internal/store/migrations.go internal/store/migrations_test.go internal/store/backup.go internal/store/backup_test.go
git commit -m "feat: migrate console data to v2 schema"
```

### Task 4: Implement logical assets, versions, and stale propagation

**Files:**
- Create: `internal/store/assets_v2.go`
- Create: `internal/store/assets_v2_test.go`
- Modify: `internal/assets/service.go:34-128,441-778`
- Modify: `internal/store/accounts.go:25-186`
- Modify: `internal/store/accounts_test.go`
- Modify: `internal/store/projects.go:117-195`

- [ ] **Step 1: Write failing repository tests**

Cover new logical asset creation, a second version, current pointer movement, dependency creation, stale propagation, and account background lookup:

```go
first, err := repo.AddVersion(ctx, AddAssetVersion{ProjectID: projectID, AccountID: accountID, Type: domain.AssetContinuousScript, Path: one, MIMEType: "text/plain", SHA256: "a"})
if err != nil || first.Version != 1 || first.State != domain.AssetReady { t.Fatalf("first=%#v err=%v", first, err) }
second, err := repo.AddVersion(ctx, AddAssetVersion{LogicalAssetID: first.AssetID, ProjectID: projectID, AccountID: accountID, Type: domain.AssetContinuousScript, Path: two, MIMEType: "text/plain", SHA256: "b", ParentVersionID: &first.ID})
if err != nil || second.Version != 2 { t.Fatalf("second=%#v err=%v", second, err) }
assertState(t, repo, downstreamVersionID, domain.AssetStale)
```

- [ ] **Step 2: Run the tests and confirm failure**

```powershell
go test ./internal/store -run 'TestAssetVersionLifecycle|TestAssetInvalidation|TestCurrentAccountBackground' -v
```

Expected: compile failure because `AssetRepository` and `AddAssetVersion` do not exist.

- [ ] **Step 3: Implement transactional version creation**

Add:

```go
type AddAssetVersion struct {
	LogicalAssetID string
	ProjectID *string
	AccountID string
	Type domain.AssetType
	StorageKind domain.StorageKind
	Path, Filename, MIMEType, SHA256 string
	Size int64
	ParentVersionID, SourceTaskID *string
	Dependencies []string
}

func (r *AssetRepository) AddVersion(ctx context.Context, in AddAssetVersion) (domain.AssetVersion, error)
```

The method must use `BEGIN IMMEDIATE`, derive account ownership from the project when `ProjectID` is set, allocate `MAX(version)+1`, insert dependencies, move `current_version_id`, and mark current downstream versions stale according to `domain.InvalidatedAssetTypes(in.Type)`. Validate all IDs with `uuid.Parse` before SQL.

- [ ] **Step 4: Add managed text and external-copy helpers**

Extend the asset service with:

```go
func (s *Service) SaveTextVersion(projectID string, typ domain.AssetType, filename, text string) (SavedAsset, error)
func (s *Service) ImportFile(projectID string, typ domain.AssetType, sourcePath string) (SavedAsset, error)
func (s *Service) HashDirectory(path string) (sha256 string, size int64, err error)
```

Use a temporary file in the destination directory, `Sync`, and `os.Rename`. `HashDirectory` must sort relative paths and hash each relative path plus file hash so identical directory content produces a stable digest.

- [ ] **Step 5: Replace project repository asset methods**

Remove direct inserts into the legacy `assets` layout. `ProjectRepository` must depend on `AssetRepository` for `CurrentByProject`, `History`, and `CurrentBackgroundForProject`. Update account creation/background replacement to create one account-level `asset_items` row and append versions through the same transaction, updating `background_asset_item_id`. Replacing a background marks only draft/final versions connected to the previous background version through `asset_dependencies` as stale. Update both reconciliation functions to read/write V2 rows. Do not expose storage paths in default project JSON.

- [ ] **Step 6: Run focused and regression tests**

```powershell
go test ./internal/store ./internal/assets ./internal/domain
```

Expected: PASS.

- [ ] **Step 7: Commit**

```powershell
git add internal/store/assets_v2.go internal/store/assets_v2_test.go internal/store/accounts.go internal/store/accounts_test.go internal/store/projects.go internal/assets/service.go internal/assets/service_test.go
git commit -m "feat: add versioned project asset lifecycle"
```

### Task 5: Add administrator authentication and session security

**Files:**
- Modify: `go.mod`, `go.sum`
- Create: `internal/security/password.go`
- Create: `internal/security/session.go`
- Create: `internal/security/redact.go`
- Create: `internal/auth/service.go`
- Create: `internal/auth/middleware.go`
- Create: `internal/store/auth.go`
- Create: `internal/httpapi/auth.go`
- Test: `internal/auth/service_test.go`
- Test: `internal/auth/middleware_test.go`
- Test: `internal/httpapi/auth_test.go`

- [ ] **Step 1: Add failing authentication tests**

Test bootstrap, login, bad-password throttling, CSRF rejection, cookie flags, password change, and revoke-all:

```go
func TestBootstrapAndLogin(t *testing.T) {
	service := newAuthTestService(t)
	if err := service.Bootstrap(context.Background(), "123321"); err != nil { t.Fatal(err) }
	session, err := service.Login(context.Background(), "123321", "127.0.0.1")
	if err != nil || session.Token == "" || session.CSRFToken == "" { t.Fatalf("session=%#v err=%v", session, err) }
	if _, err := service.Login(context.Background(), "wrong", "127.0.0.1"); !errors.Is(err, auth.ErrInvalidCredentials) { t.Fatalf("err=%v", err) }
}
```

- [ ] **Step 2: Run tests and confirm failure**

```powershell
go test ./internal/auth ./internal/httpapi -run 'TestBootstrapAndLogin|TestRequireSessionAndCSRF|TestLoginCookie' -v
```

Expected: compile failure because the auth packages do not exist.

- [ ] **Step 3: Implement password and token primitives**

Add `golang.org/x/crypto/bcrypt` and implement:

```go
func HashPassword(password string) (string, error) {
	if len(password) < 6 || len(password) > 128 { return "", ErrPasswordLength }
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(hash), err
}

func NewToken() (raw, hash string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil { return "", "", err }
	raw = base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(raw))
	return raw, hex.EncodeToString(sum[:]), nil
}
```

`Redactor` must replace every registered secret with `***` and mask values following `GROK_SEARCH_API_KEY=`, `PEXELS_API_KEY=`, `Authorization:`, and JSON keys ending in `_api_key`.

- [ ] **Step 4: Implement auth service and persistence**

`Bootstrap(ctx,"123321")` inserts one admin only when none exists. `Login` stores only token and CSRF hashes with a seven-day expiry. Five failures from one IP in ten minutes create a fifteen-minute lock. `ChangePassword` revokes all sessions in the same transaction.

- [ ] **Step 5: Implement middleware and HTTP endpoints**

Routes:

```text
POST /api/auth/login
POST /api/auth/logout
GET  /api/auth/me
PUT  /api/auth/password
POST /api/auth/revoke-all
```

Set cookie `video_console_session` with `Path=/`, `HttpOnly`, `SameSite=Strict`, seven-day `MaxAge`, and `Secure` only when `r.TLS != nil`. Unsafe authenticated requests must carry `X-CSRF-Token` matching the session.

- [ ] **Step 6: Run focused tests**

```powershell
go test ./internal/security ./internal/auth ./internal/httpapi -run 'Auth|Login|Session|CSRF|Redact' -v
```

Expected: PASS.

- [ ] **Step 7: Commit**

```powershell
git add go.mod go.sum internal/security internal/auth internal/store/auth.go internal/httpapi/auth.go internal/httpapi/auth_test.go
git commit -m "feat: protect console with administrator login"
```

### Task 6: Add encrypted settings and Skill fingerprints

**Files:**
- Create: `internal/security/secrets_windows.go`
- Create: `internal/security/secrets_other.go`
- Create: `internal/store/settings.go`
- Create: `internal/store/skills.go`
- Create: `internal/settings/service.go`
- Create: `internal/skillregistry/service.go`
- Create: `internal/httpapi/settings.go`
- Create: `internal/httpapi/skills.go`
- Create: `schemas/settings.schema.json`
- Test: `internal/settings/service_test.go`
- Test: `internal/skillregistry/service_test.go`
- Test: `internal/httpapi/settings_test.go`

- [ ] **Step 1: Write failing settings and fingerprint tests**

Assert masked secret reads and aggregate fingerprint changes:

```go
if err := service.PutSecret(ctx, "grok_api_key", "secret-value"); err != nil { t.Fatal(err) }
view, err := service.Get(ctx)
if err != nil { t.Fatal(err) }
if !view.Secrets["grok_api_key"].Configured || strings.Contains(mustJSON(view), "secret-value") { t.Fatalf("view=%s", mustJSON(view)) }

first, _ := registry.Scan("finance-viral-remix", skillDir)
writeFile(t, filepath.Join(skillDir, "references", "output-contract.md"), "changed")
second, _ := registry.Scan("finance-viral-remix", skillDir)
if first.SHA256 == second.SHA256 { t.Fatal("fingerprint did not change") }
```

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/settings ./internal/skillregistry ./internal/httpapi -run 'Settings|Secret|SkillFingerprint' -v
```

Expected: compile failure because the packages do not exist.

- [ ] **Step 3: Implement DPAPI secret protection**

On Windows, wrap `windows.CryptProtectData` and `windows.CryptUnprotectData` with `CRYPTPROTECT_UI_FORBIDDEN`; copy the returned `DataBlob` before `windows.LocalFree`. On non-Windows, return `ErrSecretStoreUnsupported` so tests can inject a fake protector. Store base64 ciphertext only.

- [ ] **Step 4: Implement typed settings**

The settings service must validate:

```go
type PublicSettings struct {
	ListenAddr string `json:"listen_addr"`
	DataRoot string `json:"data_root"`
	MaxCodexConcurrency int `json:"max_codex_concurrency"`
	BaokuanBaseURL string `json:"baokuan_base_url"`
	BaokuanMCPExecutable string `json:"baokuan_mcp_executable"`
	ObsidianVault string `json:"obsidian_vault"`
	TopicCardsDir string `json:"topic_cards_dir"`
	GrokBaseURL string `json:"grok_base_url"`
	GrokModel string `json:"grok_model"`
	CodexBinaryPath string `json:"codex_binary_path"`
	MediaIndexPath string `json:"media_index_path"`
	MediaRoot string `json:"media_root"`
	JianyingRoot string `json:"jianying_root"`
}
```

Reject concurrency outside 1—4, non-loopback baokuan URLs, missing absolute local paths for path checks, and secret values over 16 KiB. Return secret fields only as `{configured,masked}`.

- [ ] **Step 5: Implement Skill bundle fingerprints**

Hash sorted relative paths and file contents under each Skill root, excluding `__pycache__`, `*.pyc`, `.git`, and temporary files. Persist the aggregate SHA-256 and per-file list. Register exactly these default roots:

```text
C:\Users\prepare\.codex\skills\finance-topic-selector
C:\Users\prepare\.codex\skills\finance-viral-remix
C:\Users\prepare\.codex\skills\jianying-montage-draft
```

- [ ] **Step 6: Expose authenticated settings and Skill APIs**

Routes:

```text
GET  /api/settings
PUT  /api/settings
POST /api/settings/test/{dependency}
POST /api/settings/repair/baokuan-mcp
GET  /api/skills
POST /api/skills/scan
GET  /api/skills/{name}
```

Connection tests must return `ok`, `not_configured`, or `offline` plus a short message; never echo command environments or secrets. The baokuan test checks both HTTP health and `codex mcp list`. The repair endpoint runs `exec.Command(runtime.CodexBinaryPath, "mcp", "add", "baokuan", "--", runtime.BaokuanMCPExecutable, "mcp", "--base", runtime.BaokuanBaseURL)`, then rechecks registration; it never starts/stops baokuan, changes the system proxy, or kills another process.

- [ ] **Step 7: Run tests**

```powershell
go test ./internal/security ./internal/settings ./internal/skillregistry ./internal/httpapi -run 'Settings|Secret|Skill' -v
```

Expected: PASS.

- [ ] **Step 8: Commit**

```powershell
git add internal/security/secrets_* internal/store/settings.go internal/store/skills.go internal/settings internal/skillregistry internal/httpapi/settings.go internal/httpapi/settings_test.go internal/httpapi/skills.go schemas/settings.schema.json
git commit -m "feat: add encrypted settings and skill registry"
```

### Task 7: Wire authentication, settings, and runtime configuration into the app

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/app/app.go:22-146`
- Modify: `cmd/console/main.go:24-83`
- Modify: `cmd/console/main_test.go`
- Modify: `internal/app/app_test.go`

- [ ] **Step 1: Write failing route-protection and startup-limit tests**

Add tests asserting `/api/projects` returns `401` without a session, `/api/auth/login` remains public, static `/` remains reachable, and a stored concurrency value of `4` reaches `NewScheduler`:

```go
response := perform(t, app.Handler(), http.MethodGet, "/api/projects", nil, nil)
if response.Code != http.StatusUnauthorized { t.Fatalf("status=%d", response.Code) }
login := perform(t, app.Handler(), http.MethodPost, "/api/auth/login", strings.NewReader(`{"password":"123321"}`), nil)
if login.Code != http.StatusOK { t.Fatalf("login=%d", login.Code) }
```

- [ ] **Step 2: Run tests and confirm failure**

```powershell
go test ./internal/app ./cmd/console -run 'TestProtectedRoutes|TestStoredConcurrencyLimit' -v
```

Expected: FAIL because current routes are unprotected and scheduler startup is fixed at `2`.

- [ ] **Step 3: Refactor configuration defaults**

Keep only boot-critical fallback values in `config.Config`: database path, data root, bootstrap listen address, and schema directory. Runtime values must be loaded from `settings.Service` after the database opens. `Config.Default()` must not contain Obsidian, Grok, Pexels, or other secrets.

- [ ] **Step 4: Build public and protected route groups**

Refactor `app.New` into:

```go
root := http.NewServeMux()
root.Handle("POST /api/auth/login", authHandler)
root.Handle("GET /api/health", publicHealthHandler)
root.Handle("/api/", authMiddleware.RequireSession(protectedAPI))
root.Handle("/", webui.Handler())
```

The protected API mux must mount accounts, projects, assets, tasks, ideas, settings, skills, dependency health, Obsidian health, and event streams. Apply CSRF validation inside `RequireSession` for `POST`, `PUT`, `PATCH`, and `DELETE`.

- [ ] **Step 5: Load runtime values and bootstrap the administrator**

In `main`:

```go
settingsService := settings.NewService(settingsRepo, secretProtector)
if err := authService.Bootstrap(context.Background(), "123321"); err != nil { log.Fatal(err) }
runtime, err := settingsService.Runtime(context.Background())
if err != nil { log.Fatal(err) }
scheduler, err := codex.NewScheduler(taskRepo, runtime.MaxCodexConcurrency, makeCommand, makeResume, nil)
```

Construct the HTTP server only after auth, settings, reconciliation, Skill scanning, and interrupted-task recovery succeed. Log the listen address, never the initial password or configured secrets.

- [ ] **Step 6: Run tests**

```powershell
go test ./internal/app ./cmd/console ./internal/httpapi
```

Expected: PASS.

- [ ] **Step 7: Commit**

```powershell
git add internal/config internal/app cmd/console
git commit -m "feat: wire secure runtime configuration"
```

## Phase B — task manifest, Codex execution, and realtime conversation

### Task 8: Create task manifests and versioned result schemas

**Files:**
- Create: `internal/codex/manifest.go`
- Create: `internal/codex/manifest_test.go`
- Create: `internal/codex/result.go`
- Create: `internal/codex/result_validator.go`
- Create: `internal/codex/result_validator_test.go`
- Create: `schemas/task-manifest.schema.json`
- Replace: `schemas/codex-result.schema.json`
- Create: `schemas/topic-candidates.schema.json`
- Modify: `internal/codex/prompt.go:9-79`
- Modify: `internal/codex/prompt_test.go`

- [ ] **Step 1: Write failing manifest and result tests**

Test that a montage manifest includes the server-derived account background, all locked versions, task ID as job ID, and no secret fields:

```go
manifest, err := BuildManifest(BuildManifestInput{Task: task, Project: project, Inputs: []domain.AssetVersion{spoken, narration, srt, background}, Action: domain.ActionMontagePlan, OutputDir: outputDir, SkillSnapshot: snapshot})
if err != nil { t.Fatal(err) }
if manifest.JobID != task.ID { t.Fatalf("job=%s", manifest.JobID) }
if !hasRole(manifest.Inputs, "account_background") { t.Fatal("background missing") }
if strings.Contains(string(mustMarshal(manifest)), "API_KEY") { t.Fatal("secret key leaked") }
```

Test validator rejection for an asset output outside `output_dir`, an unknown asset type, missing `schema_version`, and an `awaiting_input` result without a question.

- [ ] **Step 2: Run focused tests and confirm failure**

```powershell
go test ./internal/codex -run 'TestBuildManifest|TestValidateResultEnvelope|TestPromptUsesManifest' -v
```

Expected: compile failure because the manifest and validator types do not exist.

- [ ] **Step 3: Define exact manifest structs**

Create:

```go
type TaskManifest struct {
	SchemaVersion string `json:"schema_version"`
	TaskID string `json:"task_id"`
	JobID string `json:"job_id"`
	Skill string `json:"skill"`
	Action domain.TaskAction `json:"action"`
	Project *ManifestProject `json:"project,omitempty"`
	Inputs []ManifestInput `json:"inputs"`
	OutputDir string `json:"output_dir"`
	ExpectedOutputs []ExpectedOutput `json:"expected_outputs"`
	ApprovalMode string `json:"approval_mode"`
	SkillSnapshotID string `json:"skill_snapshot_id"`
	NonSecretSettings map[string]any `json:"non_secret_settings"`
}
```

`WriteManifest` must validate UUIDs and canonicalize every path. Project asset inputs must be under the project root; `account_background` must be under the managed account-assets root; source topic cards may be under the configured Obsidian root; every other root is rejected. Create `filepath.Join(projectRoot, "tasks", manifest.TaskID)` and atomically write `task_manifest.json`.

- [ ] **Step 4: Define the exact result envelope**

Use:

```go
type ResultEnvelope struct {
	SchemaVersion string `json:"schema_version"`
	TaskID string `json:"task_id"`
	Action domain.TaskAction `json:"action"`
	Status string `json:"status"`
	Summary string `json:"summary"`
	Questions []Question `json:"questions"`
	Artifacts []ArtifactOutput `json:"artifacts"`
	AssetOutputs []AssetOutput `json:"asset_outputs"`
	Warnings []string `json:"warnings"`
}
```

Allowed statuses are `completed`, `awaiting_input`, and `failed`. Parse legacy `needs_input` as `awaiting_input` only during the transition. `AssetOutput.Type` must be one of the formal asset constants; engineering artifacts never become assets.

`topic-candidates.schema.json` must require `schema_version`, `task_id`, `session_id`, and `candidates`; candidates contain `id`, `topic`, `mother_theme`, `family_conflict`, `anomaly_framing`, `narrative_entry`, `score` (`audience`, `evidence`, `freshness`, `distance`, `course_fit`, `total`), `source_refs`, and `fragment_refs`. Set `minItems: 3`, `maxItems: 5`, and `additionalProperties: false` at every object level.

- [ ] **Step 5: Replace prompt construction**

Map actions exactly:

```go
var skillByAction = map[domain.TaskAction]string{
	domain.ActionTopicBrainstorm: "finance-topic-selector",
	domain.ActionTopicCommit: "finance-topic-selector",
	domain.ActionTopicDeepen: "finance-topic-selector",
	domain.ActionRemixStandard: "finance-viral-remix",
	domain.ActionRemixEnhanced: "finance-viral-remix",
	domain.ActionRemixFromTopic: "finance-viral-remix",
	domain.ActionSpokenFormat: "finance-viral-remix",
	domain.ActionRemixReview: "finance-viral-remix",
	domain.ActionMontagePlan: "jianying-montage-draft",
	domain.ActionMontageExecute: "jianying-montage-draft",
}
```

Build the bounded Prompt from the manifest path rather than embedding asset paths individually:

```go
prompt := fmt.Sprintf(`Use the $%s skill.
Execute action=%s using the task manifest at %s.
Treat manifest inputs as authoritative and do not ask for paths already present.
Write declared artifacts only under output_dir.
Return exactly one JSON object matching the configured result schema.
Do not open WeChat Channels. Do not launch Jianying unless the manifest explicitly authorizes UI execution.`,
	manifest.Skill, manifest.Action, manifestPath)
```

- [ ] **Step 6: Run schema and Go tests**

```powershell
go test ./internal/codex -run 'TestBuildManifest|TestValidateResultEnvelope|TestPrompt' -v
```

Expected: PASS.

- [ ] **Step 7: Commit**

```powershell
git add internal/codex/manifest* internal/codex/result* internal/codex/prompt.go internal/codex/prompt_test.go schemas
git commit -m "feat: define codex task manifest protocol"
```

### Task 9: Build exact Codex exec and resume commands

**Files:**
- Modify: `internal/codex/command.go:12-68`
- Modify: `internal/codex/command_test.go`
- Modify: `cmd/console/main.go:42-68`

- [ ] **Step 1: Write failing command tests**

Assert both commands include JSONL, schema, last-message output, correct session ordering, working directory, stdin Prompt, and only explicitly allowed secret environment variables:

```go
want := []string{"codex", "exec", "--json", "--skip-git-repo-check", "--output-schema", schema, "--output-last-message", last, "-C", workdir, "-"}
if diff := cmp.Diff(want, cmd.Args); diff != "" { t.Fatal(diff) }

resumeWant := []string{"codex", "exec", "resume", "--json", "--output-schema", schema, "--output-last-message", last, sessionID, "-"}
if diff := cmp.Diff(resumeWant, resume.Args); diff != "" { t.Fatal(diff) }
```

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/codex -run 'TestBuildExecCommandV2|TestBuildResumeCommandV2|TestSafeEnvironment' -v
```

Expected: FAIL because `--output-last-message` and secret injection are absent and resume ordering differs.

- [ ] **Step 3: Extend command configuration**

Use:

```go
type Config struct {
	CodexBinaryPath string
	ResultSchema string
	OutputLastMessage string
	WorkingDirectory string
	SecretEnvironment map[string]string
	Redactor *security.Redactor
}
```

Only allow `GROK_SEARCH_BASE_URL`, `GROK_SEARCH_MODEL`, `GROK_SEARCH_API_KEY`, and `PEXELS_API_KEY` from `SecretEnvironment`. Do not inherit arbitrary parent variables. Set `cmd.Dir` to the managed project/task root for both new and resumed commands.

- [ ] **Step 4: Implement exact argument ordering**

Use the arrays from the failing tests. Pass Prompt/answer through stdin. Store a separately redacted `CommandSnapshot` containing binary, args, working directory, and environment key names only.

- [ ] **Step 5: Remove duplicate ad-hoc command creation from main**

`cmd/console/main.go` must call `codex.BuildExecCommand` and `codex.BuildResumeCommand`; delete its hand-built `exec.Command` arrays and string formatting.

- [ ] **Step 6: Run tests**

```powershell
go test ./internal/codex ./cmd/console -run 'Command|Main' -v
```

Expected: PASS.

- [ ] **Step 7: Commit**

```powershell
git add internal/codex/command.go internal/codex/command_test.go cmd/console/main.go cmd/console/main_test.go
git commit -m "fix: run codex with inspectable output files"
```

### Task 10: Parse agent messages, persist questions, and validate final results

**Files:**
- Modify: `internal/codex/events.go:9-67`
- Modify: `internal/codex/events_test.go`
- Modify: `internal/codex/runner.go:18-260`
- Modify: `internal/codex/runner_test.go`
- Modify: `tests/fakes/fake-codex.ps1`
- Expand: `internal/store/tasks.go`

- [ ] **Step 1: Write failing parser and runner tests**

Add JSONL fixtures for:

```json
{"type":"thread.started","thread_id":"11111111-1111-1111-1111-111111111111"}
{"type":"item.completed","item":{"type":"agent_message","text":"{\"schema_version\":\"2.0\",\"task_id\":\"...\",\"action\":\"remix.standard\",\"status\":\"completed\",\"summary\":\"done\",\"questions\":[],\"artifacts\":[],\"asset_outputs\":[],\"warnings\":[]}"}}
{"type":"turn.completed"}
```

Assert the result is taken from `item.completed.item.text`, the last-message file is used when the agent event is absent, invalid schema produces `output_invalid`, and an awaiting result inserts one assistant message with `question_schema`.

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/codex -run 'TestParseAgentMessageResult|TestRunnerUsesLastMessageFallback|TestRunnerPersistsQuestion|TestRunnerRejectsInvalidResult' -v
```

Expected: FAIL because the runner only assigns `FinalResult` from `turn.completed.result`.

- [ ] **Step 3: Separate display events from candidate final text**

Extend `Event` with `AgentMessageText string`. `ParseLine` must preserve every raw line and set:

```go
case "item.completed":
	if envelope.Item.Type == "agent_message" {
		e.Kind = "agent_message"
		e.DisplayText = envelope.Item.Text
		e.AgentMessageText = envelope.Item.Text
	}
```

Do not parse arbitrary intermediate agent messages as final until the process exits; keep the latest candidate.

- [ ] **Step 4: Implement final result resolution**

After `cmd.Wait`, resolve in this order:

```go
func resolveFinalResult(agentText, lastMessagePath string, validator *ResultValidator) (ResultEnvelope, error) {
	if strings.TrimSpace(agentText) != "" {
		if result, err := validator.Parse([]byte(agentText)); err == nil { return result, nil }
	}
	b, err := os.ReadFile(lastMessagePath)
	if err != nil { return ResultEnvelope{}, fmt.Errorf("output_last_message_missing: %w", err) }
	return validator.Parse(b)
}
```

If validation fails, store the raw last message as a task artifact, set `output_invalid`, and register no formal assets.

- [ ] **Step 5: Persist questions, artifacts, and asset outputs transactionally**

Add repository methods:

```go
func (r *TaskRepository) CompleteWithResult(ctx context.Context, taskID string, result codex.ResultEnvelope, artifacts []domain.TaskArtifact, assets []store.AddAssetVersion) error
func (r *TaskRepository) AwaitInput(ctx context.Context, taskID string, result codex.ResultEnvelope) error
```

`AwaitInput` inserts an assistant message with the JSON question/options and changes status to `awaiting_input` in one transaction. `CompleteWithResult` registers engineering artifacts separately and delegates formal asset registration to `AssetRepository`.

- [ ] **Step 6: Expand the fake CLI**

Make `fake-codex.ps1` parse `--output-last-message`, write the same result JSON to that file, emit an agent-message event, and support modes `completed`, `awaiting_input`, `invalid_schema`, `agent_only`, `last_message_only`, `failed`, and `large`.

- [ ] **Step 7: Run tests**

```powershell
go test ./internal/codex ./internal/store -run 'Runner|Event|TaskResult|Question' -v
```

Expected: PASS.

- [ ] **Step 8: Commit**

```powershell
git add internal/codex/events.go internal/codex/events_test.go internal/codex/runner.go internal/codex/runner_test.go internal/store/tasks.go internal/store/tasks_test.go tests/fakes/fake-codex.ps1
git commit -m "fix: persist codex conversations and final results"
```

### Task 11: Make scheduling resumable, recoverable, and dynamically limited

**Files:**
- Modify: `internal/codex/scheduler.go:15-267`
- Modify: `internal/codex/scheduler_test.go`
- Expand: `internal/store/tasks.go`
- Test: `internal/store/tasks_test.go`

- [ ] **Step 1: Write failing scheduler tests**

Cover all of these cases:

```text
limit loads as 4 and can shrink to 1 without killing running tasks
different project write tasks run in parallel
same project write tasks serialize
project-less idea tasks do not all share an empty project lock
answer changes awaiting_input → resuming → running
Skill fingerprint mismatch rejects silent resume and returns replacement_required
startup changes running/resuming → interrupted and leaves queued unchanged
closing the console terminates only child Codex process trees recorded in `running`; it never stops baokuan, a network proxy, or any process discovered by name
```

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/codex ./internal/store -run 'Scheduler|RecoverInterrupted|SkillFingerprint' -v
```

Expected: FAIL on idea locking, resume state, fingerprint comparison, and recovery.

- [ ] **Step 3: Add explicit queue metadata**

Extend task persistence with `action`, `write_scope`, `skill_snapshot_id`, `config_snapshot_json`, `manifest_path`, `prompt_path`, `output_schema_path`, `output_last_message_path`, and `queue_reason`. Use `fmt.Sprintf("project:%s", project.ID)` for asset-writing tasks, an empty scope for read-only brainstorm, and `fmt.Sprintf("obsidian-card:%s", card.ID)` for topic-card writes.

- [ ] **Step 4: Implement state-correct resume**

`Resume` must:

```go
if task.Status != domain.TaskAwaitingInput { return ErrTaskNotAwaitingInput }
if task.SkillSnapshotID != currentSnapshot.ID { return ErrSkillChanged }
AddMessage(role="user", content=answer)
UpdateStatus(domain.TaskResuming)
signal()
```

Dispatch selects resume when status is `resuming` and session ID exists. A replacement endpoint will clone the manifest with current Skill/settings snapshots while preserving `task_relations(kind='replacement_for')`.

- [ ] **Step 5: Implement crash recovery and dynamic limits**

At startup, `RecoverInterrupted` changes only `running` and `resuming` to `interrupted`, appends a system event, and returns their IDs. Do not auto-resume them. `SetLimit` accepts 1—4, never cancels running work, and applies the new limit to subsequent dispatch. Scheduler shutdown may terminate only `exec.Cmd` process trees it created and stored in `running`; it must contain no process-name search, proxy command, baokuan stop/restart, or machine-wide network action.

- [ ] **Step 6: Run tests**

```powershell
go test ./internal/codex ./internal/store -run 'Scheduler|Recover|Resume|Replacement|Limit' -v
```

Expected: PASS.

- [ ] **Step 7: Commit**

```powershell
git add internal/codex/scheduler.go internal/codex/scheduler_test.go internal/store/tasks.go internal/store/tasks_test.go
git commit -m "feat: make codex scheduling resumable and recoverable"
```

### Task 12: Replace task WebSockets with authenticated SSE and full task APIs

**Files:**
- Replace: `internal/realtime/hub.go`
- Replace: `internal/realtime/hub_test.go`
- Modify: `internal/httpapi/tasks.go:17-157`
- Create: `internal/httpapi/tasks_test.go`
- Create: `internal/httpapi/events.go`
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Write failing HTTP and SSE tests**

Assert:

```text
GET /api/tasks/{id} returns prompt/manifest/command snapshot/messages/events/artifacts/result
POST /api/tasks/{id}/reply accepts one non-empty answer and returns resuming
POST /api/tasks/{id}/replacement creates a linked queued task after Skill change
GET /api/tasks/{id}/events uses text/event-stream
Last-Event-ID: 7 replays only events with sequence > 7
unauthenticated SSE returns 401
```

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/httpapi ./internal/realtime -run 'TaskDetail|TaskReply|TaskReplacement|SSE' -v
```

Expected: FAIL because the API uses `/answer`, omits detail fields, and uses WebSocket.

- [ ] **Step 3: Implement SSE delivery**

Use `http.Flusher` and this frame format:

```go
fmt.Fprintf(w, "id: %d\nevent: task_event\ndata: %s\n\n", event.Sequence, mustJSON(event))
flusher.Flush()
```

Set `Content-Type: text/event-stream`, `Cache-Control: no-cache`, and `X-Accel-Buffering: no`. Send a comment heartbeat every 20 seconds. Replay persisted events before subscribing. Remove `github.com/coder/websocket` after `go mod tidy` confirms no remaining imports.

- [ ] **Step 4: Implement task detail and commands**

Routes:

```text
GET  /api/tasks
GET  /api/tasks/{id}
POST /api/projects/{id}/tasks
POST /api/tasks/{id}/reply
POST /api/tasks/{id}/cancel
POST /api/tasks/{id}/retry
POST /api/tasks/{id}/replacement
GET  /api/tasks/{id}/events
```

Task creation accepts only `{action,user_instruction,approval_mode}`. Derive project/account/Skill/requirements server-side. Return `409` with `missing_inputs` when blocked. Never accept `account_id` or arbitrary Skill names from the browser.

- [ ] **Step 5: Add cursor and ownership validation**

Parse `Last-Event-ID` as a non-negative integer. Verify the task exists before opening the stream. Authentication middleware protects the route; no local-origin bypass is a substitute for login.

- [ ] **Step 6: Run tests and tidy modules**

```powershell
go test ./internal/httpapi ./internal/realtime ./internal/codex
go mod tidy
go test ./...
```

Expected: PASS; `github.com/coder/websocket` is absent from direct and indirect requirements when unused.

- [ ] **Step 7: Commit**

```powershell
git add internal/realtime internal/httpapi/tasks.go internal/httpapi/tasks_test.go internal/httpapi/events.go go.mod go.sum
git commit -m "feat: expose live codex task conversations"
```

## Phase C — project assets and pre-project ideas

### Task 13: Expose versioned assets, previews, and action readiness

**Files:**
- Modify: `internal/httpapi/projects.go:22-366`
- Modify: `internal/httpapi/projects_test.go`
- Modify: `internal/httpapi/assets.go:18-67`
- Modify: `internal/httpapi/assets_test.go`
- Modify: `internal/httpapi/accounts.go:38-287`
- Modify: `internal/httpapi/accounts_test.go`
- Modify: `internal/domain/models.go`
- Modify: `internal/domain/stages.go`
- Create: `internal/requirements/service.go`
- Create: `internal/requirements/service_test.go`

- [ ] **Step 1: Write failing API tests**

Add exact assertions for an empty project:

```go
detail := decodeProjectDetail(t, get(t, handler, "/api/projects/"+projectID))
montage := detail.Requirements["montage.plan"]
if montage.Ready { t.Fatal("empty project must not be montage-ready") }
if diff := cmp.Diff([]string{"spoken_script","narration","subtitle_srt","account_background"}, montage.MissingInputs); diff != "" { t.Fatal(diff) }
```

Also test text `GET/PUT`, a new version invalidating downstream, audio/video Range requests, SRT MIME, directory assets returning metadata rather than raw file serving, and all JSON responses omitting storage paths.

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/httpapi ./internal/requirements -run 'EmptyProject|AssetTextVersion|AssetRange|AssetPathHidden|Requirements' -v
```

Expected: FAIL because the current `missingForStage` returns an empty slice for topic projects and paths are exposed.

- [ ] **Step 3: Add readiness service**

`requirements.Service.ProjectActions(ctx, projectID)` must load current asset states, add the account background state, merge dependency health, and call `domain.EvaluateAction` for every project action. Return a map keyed by the exact `TaskAction` string. At this point switch project handlers/repositories to `Project.Status` and `CanMovePublicationStatus`, remove legacy asset gating from `CanMove`, and stop serializing old stage/task/asset aliases.

- [ ] **Step 4: Add asset APIs**

Routes:

```text
GET  /api/projects/{id}/assets
POST /api/projects/{id}/assets/{type}
POST /api/projects/from-source
GET  /api/assets/{id}
GET  /api/assets/{id}/versions
GET  /api/assets/{id}/content
GET  /api/assets/{id}/text
PUT  /api/assets/{id}/text
POST /api/assets/{id}/restore/{version_id}
```

`PUT /text` accepts `{content,expected_version_id}` with a 2 MiB limit, only for text asset types, and returns `409` on concurrent modification. `content` uses the stored MIME and `http.ServeContent`; directory assets return `409 directory_asset_not_streamable`. `/api/projects/from-source` accepts multipart `account_id`, `title`, optional text field or uploaded source file, and `mode=enhanced|standard`; it creates the project and `source_script` v1 as one compensated operation, removing the copied file if the database transaction does not commit.

- [ ] **Step 5: Update project detail and upload types**

Support uploads for `source_script`, `continuous_script`, `spoken_script`, `narration`, `subtitle_srt`, `mix_draft`, and `final_video`. Return logical asset summaries, current versions, history links, account background reference, and the full requirements map. Account responses expose `background_asset_id` and a content URL, not `background_path`. Never serialize a storage path from project/account asset endpoints.

- [ ] **Step 6: Run tests**

```powershell
go test ./internal/httpapi ./internal/requirements ./internal/assets ./internal/store
```

Expected: PASS.

- [ ] **Step 7: Commit**

```powershell
git add internal/httpapi/projects.go internal/httpapi/projects_test.go internal/httpapi/assets.go internal/httpapi/assets_test.go internal/httpapi/accounts.go internal/httpapi/accounts_test.go internal/domain/models.go internal/domain/stages.go internal/requirements
git commit -m "feat: expose versioned project asset workspace"
```

### Task 14: Add pre-project idea sessions and confirmed project creation

**Files:**
- Create: `internal/store/ideas.go`
- Create: `internal/store/ideas_test.go`
- Create: `internal/ideas/service.go`
- Create: `internal/ideas/service_test.go`
- Create: `internal/httpapi/ideas.go`
- Create: `internal/httpapi/ideas_test.go`
- Modify: `internal/app/app.go`

- [ ] **Step 1: Write failing idea workflow tests**

Exercise the complete state machine:

```go
session := createIdeaSession(t, api)
brainstorm := postIdeaAction(t, api, session.ID, "brainstorm", nil)
if brainstorm.Task.ProjectID != nil { t.Fatal("brainstorm must be project-less") }
saveCandidates(t, brainstorm.Task.ID, threeCandidates())
card := postIdeaAction(t, api, session.ID, "commit", map[string]string{"candidate_id":"cand-2"})
deepen := postIdeaAction(t, api, session.ID, "deepen", map[string]string{"topic_card_path":card.Path})
markCardReady(t, deepen.Task.ID)
project := confirmIdeaProject(t, api, session.ID, accountID, "存款异动")
if project.TopicCardAssetID == "" { t.Fatal("topic card asset not imported") }
```

Also assert project creation is rejected before `可写稿`, when the account is inactive, or when the card path escapes the configured Vault.

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/store ./internal/ideas ./internal/httpapi -run 'Idea|Candidate|ConfirmedTopicProject' -v
```

Expected: compile failure because idea persistence and APIs do not exist.

- [ ] **Step 3: Implement idea persistence**

Repositories must expose:

```go
CreateSession(ctx, domain.IdeaSession) error
AddMessage(ctx, domain.IdeaMessage) error
ReplaceCandidates(ctx, sessionID, taskID string, candidates []domain.IdeaCandidate) error
SelectCandidate(ctx, sessionID, candidateID string) error
SetTopicCard(ctx, sessionID, path, status, sha256 string) error
GetSession(ctx, id string) (domain.IdeaSessionDetail, error)
ListSessions(ctx, status string) ([]domain.IdeaSession, error)
```

Candidate replacement is transactional and preserves the selected candidate after commit.

- [ ] **Step 4: Implement idea service actions**

`Brainstorm` creates a project-less task with action `topic.brainstorm`. `Commit` requires a candidate belonging to the session and creates `topic.commit`. `Deepen` requires the session card path and creates `topic.deepen`. Task results update session candidates/card through a typed result-consumer hook.

- [ ] **Step 5: Implement confirmed project creation**

`ConfirmProject` must validate card status `可写稿`, re-read the file under the configured Vault, compare its SHA-256 with the idea record, create the project, copy the Markdown into the managed project directory as `topic_card` v1, and link the idea session to the project in one service operation. A file/database failure must leave no half-created project or unmanaged copy.

- [ ] **Step 6: Add APIs**

```text
POST /api/ideas
GET  /api/ideas
GET  /api/ideas/{id}
POST /api/ideas/{id}/messages
POST /api/ideas/{id}/brainstorm
POST /api/ideas/{id}/commit
POST /api/ideas/{id}/deepen
POST /api/ideas/{id}/create-project
```

- [ ] **Step 7: Run tests**

```powershell
go test ./internal/store ./internal/ideas ./internal/httpapi -run 'Idea|TopicProject' -v
```

Expected: PASS.

- [ ] **Step 8: Commit**

```powershell
git add internal/store/ideas* internal/ideas internal/httpapi/ideas* internal/app/app.go
git commit -m "feat: add pre-project topic workspace"
```

## Phase D — installed Skill contracts

### Task 15: Adapt `finance-topic-selector` to brainstorm, commit, and deepen actions

**Required sub-skill:** `superpowers:writing-skills`

**Files:**
- Modify: `C:\Users\prepare\.codex\skills\finance-topic-selector\SKILL.md`
- Modify: `C:\Users\prepare\.codex\skills\finance-topic-selector\references\obsidian-card-operations.md`
- Modify: `C:\Users\prepare\.codex\skills\finance-topic-selector\scripts\validate_topic_card.py`
- Create: `C:\Users\prepare\.codex\skills\finance-topic-selector\references\console-contract.md`
- Create: `C:\Users\prepare\.codex\skills\finance-topic-selector\scripts\topic_card_store.py`
- Create: `C:\Users\prepare\.codex\skills\finance-topic-selector\tests\test_console_contract.py`
- Create: `C:\Users\prepare\.codex\skills\finance-topic-selector\tests\test_topic_card_store.py`

- [ ] **Step 1: Read the Skill-writing instructions and current Skill completely**

Read `superpowers:writing-skills`, then re-read the selected Skill, both current references, the validator, and the shared topic-card contract. Record the console action names in the task commentary before editing.

- [ ] **Step 2: Write failing contract tests**

Create tests asserting:

```python
def test_console_actions_are_explicit(self):
    text = (SKILL / "SKILL.md").read_text(encoding="utf-8-sig")
    for action in ("brainstorm", "commit_topic", "deepen"):
        self.assertIn(action, text)
    self.assertNotIn(r"C:\Users\prepare\Documents\爆款库文案\选题卡", text)

def test_brainstorm_does_not_write_card(self):
    contract = (SKILL / "references" / "console-contract.md").read_text(encoding="utf-8-sig")
    self.assertIn("brainstorm 不写入 Obsidian", contract)
    self.assertIn("3—5", contract)
```

`test_topic_card_store.py` must create two concurrent candidate cards in a temporary Vault, assert unique filenames, valid frontmatter, no truncated writes, and unchanged unrelated cards.

- [ ] **Step 3: Run and confirm failure**

```powershell
python -m unittest discover 'C:\Users\prepare\.codex\skills\finance-topic-selector\tests' -v
```

Expected: FAIL because console actions, portable paths, and atomic store do not exist.

- [ ] **Step 4: Write the console action contract**

Define exact input/output behavior:

```markdown
## brainstorm
Read `task_manifest.json`; return 3—5 candidates in `topic_candidates.json`. Do not write Obsidian.

## commit_topic
Require `candidate_id`; create one `候选` card atomically under `non_secret_settings.topic_cards_dir`; return its absolute path and SHA-256.

## deepen
Require exactly one card path from the manifest; preserve prior sources, add 3—8 complete sources or approved snippets, validate, and return `可写稿` or `证据薄弱`.
```

Every result uses the shared envelope and includes no project creation action.

- [ ] **Step 5: Implement portable, atomic card storage**

`topic_card_store.py` must accept the following PowerShell invocation shape:

```powershell
python scripts/topic_card_store.py create --vault $vaultPath --cards-dir $cardsDir --input $candidateJSON
python scripts/topic_card_store.py update --vault $vaultPath --card $cardPath --input $deepenJSON
```

Validate both paths remain inside the Vault. Use a machine-wide lock directory under `%LOCALAPPDATA%\Codex\finance-topic-selector\locks`, write a UTF-8 temporary file beside the destination, flush, `os.fsync`, validate the temporary card, then `os.replace`. The script prints one JSON object with `path`, `relative_path`, `sha256`, and `status`.

- [ ] **Step 6: Update the Skill workflow and validator**

Replace hard-coded Vault/card paths with manifest keys. Keep baokuan tools read-only. `validate_topic_card.py` must accept `--expected-status`, reject duplicate source/fragment refs, and continue enforcing a 150–300 Chinese-character handoff for `可写稿`.

- [ ] **Step 7: Run all topic Skill checks**

```powershell
python -m unittest discover 'C:\Users\prepare\.codex\skills\finance-topic-selector\tests' -v
python 'C:\Users\prepare\.codex\skills\finance-topic-selector\scripts\verify_baokuan_mcp.py'
```

Expected: all unit tests PASS; MCP verification reports the registered read-only tools without mutating the library.

- [ ] **Step 8: Re-scan the installed Skill in the console**

Run the Skill registry test or authenticated `/api/skills/scan`; assert the aggregate hash changes and all required files appear in the file list. Do not resume any task created with the previous fingerprint.

### Task 16: Adapt `finance-viral-remix` to structured console outputs

**Required sub-skill:** `superpowers:writing-skills`

**Files:**
- Modify: `C:\Users\prepare\.codex\skills\finance-viral-remix\SKILL.md`
- Modify: `C:\Users\prepare\.codex\skills\finance-viral-remix\references\output-contract.md`
- Modify: `C:\Users\prepare\.codex\skills\finance-viral-remix\references\external-research.md`
- Modify: `C:\Users\prepare\.codex\skills\finance-viral-remix\references\spoken-line-breaks.md`
- Modify: `C:\Users\prepare\.codex\skills\finance-viral-remix\references\topic-card-contract.md`
- Create: `C:\Users\prepare\.codex\skills\finance-viral-remix\references\console-contract.md`
- Create: `C:\Users\prepare\.codex\skills\finance-viral-remix\scripts\validate_spoken_script.py`
- Create: `C:\Users\prepare\.codex\skills\finance-viral-remix\tests\test_console_contract.py`
- Create: `C:\Users\prepare\.codex\skills\finance-viral-remix\tests\test_spoken_script.py`
- Create: `C:\Users\prepare\.codex\skills\finance-viral-remix\tests\fixtures\continuous.txt`
- Create: `C:\Users\prepare\.codex\skills\finance-viral-remix\tests\fixtures\spoken.txt`

- [ ] **Step 1: Re-read the Skill and every reference touched by console mode**

Read the complete Skill plus account profile, remix framework, finance bottom line, output contract, external research, spoken-line rules, and topic-card contract. Preserve the fixed audience, course name, abnormal-event opening, 3+ function moves, 4+ reconstruction dimensions, and default publishing package.

- [ ] **Step 2: Write failing action/output tests**

Assert the Skill contains actions `standard`, `enhanced`, `from_topic_card`, `spoken_format`, and `review`; contains declared filenames; contains no API key-shaped string; and states that `source_script` remains the primary source in enhanced mode:

```python
for name in ("viral_analysis.json", "continuous_script.txt", "spoken_script.txt", "publishing_package.json", "self_check.json"):
    self.assertIn(name, contract)
self.assertNotRegex(combined, r"sk-[A-Za-z0-9_-]{16,}")
self.assertIn("用户提供的完整爆款原文始终是主来源", combined)
```

Write spoken-script fixtures for valid semantic lines, missing text, changed punctuation, a split course name, a split number/unit, a single-character line, and a line beginning with a forbidden function word.

- [ ] **Step 3: Run and confirm failure**

```powershell
python -m unittest discover 'C:\Users\prepare\.codex\skills\finance-viral-remix\tests' -v
```

Expected: FAIL because console artifact names and the validator do not exist.

- [ ] **Step 4: Define console actions and outputs**

The console contract must require these files under `manifest.output_dir`:

```text
viral_analysis.json
structure_design.json
publishing_package.json
continuous_script.txt
spoken_script.txt       when requested by expected_outputs
self_check.json
result.json
```

`publishing_package.json` contains exactly `titles` (3), `description` (1), `topics` (3–5), and `cta`. `asset_outputs` registers only continuous and spoken scripts; analysis and package files are engineering artifacts.

- [ ] **Step 5: Implement deterministic spoken-script validation**

`validate_spoken_script.py` must accept `--continuous` and `--spoken`, compare text after removing `\r` and `\n`, and emit JSON findings. Enforce 2–9 Chinese-character default semantic lines, no single-character lines, forbidden leading words, intact numbers/units, intact `《财富觉醒方法论》`, and no fabricated timestamps. The exact-text comparison is the non-negotiable pass condition.

- [ ] **Step 6: Update enhanced research rules**

Read only configuration keys from the manifest/environment. Search baokuan complete videos and `approved` snippets first. Use Grok only for necessary public facts, then system search fallback. Never send the full source script, internal notes, library text, or credentials to external search. Failed search must degrade to source + local materials and record a warning, not invent a result.

- [ ] **Step 7: Run all remix Skill tests**

```powershell
python -m unittest discover 'C:\Users\prepare\.codex\skills\finance-viral-remix\tests' -v
python 'C:\Users\prepare\.codex\skills\finance-viral-remix\scripts\validate_spoken_script.py' --continuous 'C:\Users\prepare\.codex\skills\finance-viral-remix\tests\fixtures\continuous.txt' --spoken 'C:\Users\prepare\.codex\skills\finance-viral-remix\tests\fixtures\spoken.txt'
```

Expected: all unit tests PASS; the explicit fixture command prints `{"valid":true,"findings":[]}`.

- [ ] **Step 8: Re-scan the installed Skill**

Assert the registry hash changes. Run a fake-manifest contract invocation with Codex CLI only after the console backend tests can capture all sent Prompt and output paths; do not use a real unpublished source in this verification.

### Task 17: Add a portable machine profile and deterministic Jianying builder

**Required sub-skill:** `superpowers:writing-skills`

**Files:**
- Modify: `C:\Users\prepare\.codex\skills\jianying-montage-draft\SKILL.md`
- Modify: `C:\Users\prepare\.codex\skills\jianying-montage-draft\references\planning-execution-contract.md`
- Modify: `C:\Users\prepare\.codex\skills\jianying-montage-draft\references\defaults.md`
- Modify: `C:\Users\prepare\.codex\skills\jianying-montage-draft\references\concurrency-contract.md`
- Modify: `C:\Users\prepare\.codex\skills\jianying-montage-draft\references\reusable-draft-case.md`
- Modify: `C:\Users\prepare\.codex\skills\jianying-montage-draft\assets\user-verified-audio-transition-case.json`
- Create: `C:\Users\prepare\.codex\skills\jianying-montage-draft\assets\machine_profile.template.json`
- Create: `C:\Users\prepare\.codex\skills\jianying-montage-draft\references\console-contract.md`
- Create: `C:\Users\prepare\.codex\skills\jianying-montage-draft\scripts\run_montage_job.py`
- Create: `C:\Users\prepare\.codex\skills\jianying-montage-draft\tests\test_run_montage_job.py`
- Modify: `C:\Users\prepare\.codex\skills\jianying-montage-draft\tests\test_skill_contract.py`

- [ ] **Step 1: Re-read the complete montage Skill and all required references**

Read the Skill, all ten required references, both verified JSON assets, validators, downloader, lock implementation, and current contract tests. Preserve all verified layout, audio, transition, title, source-reuse, PLAN, plaintext validation, and registration rules.

- [ ] **Step 2: Write failing portable-profile and builder tests**

Add tests asserting:

```python
def test_manifest_job_id_is_authoritative(self):
    context = load_context(self.manifest_path)
    self.assertEqual(context.job_id, self.task_id)

def test_all_machine_paths_come_from_profile(self):
    serialized = json.dumps(json.loads((SKILL / "assets" / "user-verified-audio-transition-case.json").read_text(encoding="utf-8-sig")))
    self.assertNotIn("C:/Users", serialized)
    self.assertNotIn("E:/", serialized)

def test_dry_run_writes_plan_validation_and_result(self):
    result = run_job(self.manifest_path, mode="dry-run", backend=FakeDraftBackend())
    self.assertEqual(result["status"], "completed")
    self.assertTrue((self.output_dir / "production_plan.validation.json").is_file())
    self.assertTrue((self.output_dir / "result.json").is_file())
```

Also assert missing narration, SRT, background, media index, BGM cache, SFX cache, or transition mapping returns `awaiting_input`/failed validation before draft mutation.

- [ ] **Step 3: Run and confirm failure**

```powershell
python -m unittest discover 'C:\Users\prepare\.codex\skills\jianying-montage-draft\tests' -v
```

Expected: FAIL because paths still live in the reusable case and `run_montage_job.py` does not exist.

- [ ] **Step 4: Split logical policy from machine paths**

Keep IDs, names, roles, native durations, levels, and cache keys in the reusable case. Move absolute cache paths, media index/root, Jianying root, and executable locations to a local machine profile matching:

```json
{
  "schema_version": "1.0",
  "python_binary": "python",
  "media_index_path": "",
  "media_root": "",
  "jianying_root": "",
  "cache_paths": {
    "bgm_extasy_remake": "",
    "sfx_opening_hit": "",
    "sfx_water_drop": "",
    "sfx_whoosh": "",
    "sfx_conclusion_hit": ""
  }
}
```

The console writes the actual profile under `video-console-data/machine-profiles/jianying.json` from encrypted/public settings and passes its path in the task manifest. The shared template remains empty.

- [ ] **Step 5: Implement manifest intake and deterministic phases**

`run_montage_job.py` supports these exact command forms:

```powershell
python scripts/run_montage_job.py validate-inputs --manifest $manifestPath
python scripts/run_montage_job.py validate-plan --manifest $manifestPath --plan $planPath
python scripts/run_montage_job.py execute --manifest $manifestPath --plan $planPath
python scripts/run_montage_job.py register --manifest $manifestPath --draft $draftPath
```

Use frozen dataclasses `JobContext`, `MachineProfile`, and `BuildResult`. Reject a manifest job ID different from task ID. Create only `Path(context.output_dir) / "workspace" / context.job_id`. Read TXT/SRT only as context. Never generate narration, text copy, captions, ASR, descriptions, or topics.

- [ ] **Step 6: Implement the draft backend**

Use `pyJianYingDraft.DraftFolder.create_draft(name,1080,1920,30,allow_replace=False)`. For each approved timeline entry, add a muted `VideoSegment` with independent `speed` and `ClipSettings(scale_x,scale_y,transform_x,transform_y,alpha)`. Add the transparent frame, two 1080×6 red image/video line materials, full-duration title/subtitle, narration at `1.7783`, looped BGM at `0.1593`, sparse SFX at `0.3981`, and verified overlapping transition metadata for every cut. Generate fresh UUIDs for every record.

After `ScriptFile.save()`, patch only the verified BGM/SFX/transition fields that `pyJianYingDraft` cannot express, using the sanitized policy + machine profile. Run `validate_montage_draft.py` against the project profile; any finding aborts registration.

- [ ] **Step 7: Keep registration inside the existing managed lock**

Generate one job-specific PowerShell registration script and invoke:

```powershell
python scripts/jianying_concurrency_lock.py run jianying-registration --job-id $taskID --lease-seconds 1800 --wait-seconds 30 -- powershell -NoProfile -File $registrationScript
```

The script rechecks the target, stops Jianying/Tray, creates a job-qualified backup of `root_meta_info.json`, registers exactly one new draft, verifies directory/index results, and exits. It never relaunches Jianying.

- [ ] **Step 8: Emit the structured result**

`result.json` must list production plan, validation, selected-media summary, plaintext workspace draft, optional registered path, actual narration/BGM/SFX/transitions, unresolved inputs, and one `mix_draft` asset output. Use `awaiting_input` for approval/missing inputs and `completed` only after the requested phase succeeds.

- [ ] **Step 9: Run all montage tests**

```powershell
python -m unittest discover 'C:\Users\prepare\.codex\skills\jianying-montage-draft\tests' -v
```

Expected: all tests PASS. Do not open Jianying. The builder integration test uses `FakeDraftBackend` and temporary directories; plaintext validator tests continue to inspect JSON only.

- [ ] **Step 10: Re-scan the installed Skill**

Assert the registry hash changes and the Skill health result confirms Python, `pyJianYingDraft`, media index, background profile fields, and verified cache keys without returning absolute cache paths in the API response.

## Phase E — React console

### Task 18: Build the authenticated frontend shell and API client

**Files:**
- Modify: `web/package.json`, `web/package-lock.json`
- Modify: `web/vite.config.ts`
- Modify: `web/src/main.tsx`
- Replace: `web/src/App.tsx`
- Create: `web/src/types.ts`
- Create: `web/src/api/client.ts`
- Create: `web/src/api/queries.ts`
- Create: `web/src/app/AuthProvider.tsx`
- Create: `web/src/app/AppShell.tsx`
- Create: `web/src/app/routes.tsx`
- Create: `web/src/pages/LoginPage.tsx`
- Create: `web/src/test/setup.ts`
- Create: `web/src/pages/LoginPage.test.tsx`
- Create: `web/src/styles/tokens.css`
- Create: `web/src/styles/layout.css`
- Create: `web/src/styles/components.css`
- Create: `web/src/styles/responsive.css`

- [ ] **Step 1: Add failing frontend authentication tests**

Configure Vitest + jsdom and write:

```tsx
it('shows login until the administrator session is valid', async () => {
  server.use(http.get('/api/auth/me', () => HttpResponse.json({code:'unauthorized'}, {status:401})))
  render(<App />)
  expect(await screen.findByRole('heading', {name:'登录视频生产控制台'})).toBeInTheDocument()
})

it('sends csrf on unsafe requests after login', async () => {
  let csrf = ''
  server.use(http.put('/api/settings', ({request}) => { csrf = request.headers.get('X-CSRF-Token') || ''; return HttpResponse.json({}) }))
  await api.put('/api/settings', {max_codex_concurrency:3})
  expect(csrf).toBe('csrf-test-token')
})
```

Use MSW for deterministic API handlers; add it as a dev dependency.

- [ ] **Step 2: Run and confirm failure**

```powershell
npm --prefix web test -- --run
```

Expected: the `test` script is absent or tests fail because App immediately loads projects.

- [ ] **Step 3: Install router/test dependencies and scripts**

Run:

```powershell
npm --prefix web install react-router-dom
npm --prefix web install --save-dev msw
```

Add scripts:

```json
"test": "vitest",
"test:run": "vitest run",
"test:e2e": "playwright test"
```

Configure `vite.config.ts` with `test: {environment:'jsdom', setupFiles:['./src/test/setup.ts'], css:true}`.

- [ ] **Step 4: Implement the authenticated API client**

`api/client.ts` must expose `get`, `post`, `put`, `upload`, and `logout`. It stores CSRF in memory from `/api/auth/me` or login response, adds it only to unsafe requests, parses structured API errors, and dispatches `auth-expired` on `401`. Never persist the password or secret setting values in localStorage.

- [ ] **Step 5: Implement auth provider and hash routes**

Use `HashRouter` so embedded Go static serving requires no route fallback. Routes:

```text
/#/                    HomePage
/#/ideas               IdeasPage
/#/projects            ProjectsPage
/#/projects/:id        ProjectWorkspacePage
/#/tasks/:id           TaskDetailPage
/#/settings            SettingsPage
```

`AuthProvider` performs `/api/auth/me`, renders `LoginPage` on `401`, and exposes `login`, `logout`, `csrfToken`, and `initialPasswordWarning`.

- [ ] **Step 6: Implement the app shell and styles**

Create a fixed top header, collapsible navigation, account filter region, content outlet, waiting-task badge, dependency health badge, and logout button. Use the confirmed neutral finance palette, minimum 44 px controls, visible focus states, and responsive single-column layout below 760 px.

- [ ] **Step 7: Run frontend tests and build**

```powershell
npm --prefix web run test:run
npm --prefix web run lint
npm --prefix web run build
```

Expected: PASS; Vite writes embedded files under `internal/webui/dist`.

- [ ] **Step 8: Commit**

```powershell
git add web/package.json web/package-lock.json web/vite.config.ts web/src internal/webui/dist
git commit -m "feat: add authenticated console application shell"
```

### Task 19: Build the dual-entry home and conversational topic workspace

**Files:**
- Create: `web/src/pages/HomePage.tsx`
- Create: `web/src/pages/HomePage.test.tsx`
- Create: `web/src/pages/IdeasPage.tsx`
- Create: `web/src/pages/IdeasPage.test.tsx`
- Create: `web/src/components/HealthBadge.tsx`
- Modify: `web/src/api/queries.ts`
- Modify: `web/src/styles/components.css`
- Modify: `web/src/styles/responsive.css`

- [ ] **Step 1: Write failing dual-entry and topic-flow tests**

Cover:

```tsx
expect(screen.getByRole('button', {name:'开始选题'})).toBeInTheDocument()
expect(screen.getByRole('button', {name:'已有爆款原文'})).toBeInTheDocument()
```

Then mock an idea session and assert the page renders 3–5 candidate cards with score breakdowns, disables “创建项目” before a ready card, displays the full Markdown card after deepen, and enables account/project confirmation only after `status === '可写稿'`.

- [ ] **Step 2: Run and confirm failure**

```powershell
npm --prefix web run test:run -- HomePage IdeasPage
```

Expected: FAIL because the pages do not exist.

- [ ] **Step 3: Implement the home page**

The two primary cards route to `/ideas` and open a direct-source form. The direct-source form contains account, project title, source text/file, and an `素材增强` switch defaulting to enabled. Submit multipart data to `/api/projects/from-source`; after success route to `/projects/{id}`.

Below the cards, render recent projects, tasks in `awaiting_input`, and dependency failures. Do not require a project before entering `/ideas`.

- [ ] **Step 4: Implement conversational topic sessions**

The left rail lists recent sessions; the center shows user/Codex messages and candidate cards; the right panel shows selected candidate, source references, topic-card Markdown, and create-project confirmation. Buttons invoke `brainstorm`, `commit`, and `deepen`; stream the current task over authenticated SSE and refetch on terminal events.

- [ ] **Step 5: Make pending states explicit**

Display `正在从爆款库和近30天选题卡中检索`, `等待你选择候选`, `正在写入 Obsidian`, `证据薄弱`, and `可写稿`. Never show a fabricated score or source when the API omits it.

- [ ] **Step 6: Run tests and build**

```powershell
npm --prefix web run test:run -- HomePage IdeasPage
npm --prefix web run lint
npm --prefix web run build
```

Expected: PASS.

- [ ] **Step 7: Commit**

```powershell
git add web/src/pages/HomePage* web/src/pages/IdeasPage* web/src/components/HealthBadge.tsx web/src/api/queries.ts web/src/styles internal/webui/dist
git commit -m "feat: add topic-first production entry"
```

### Task 20: Build the unified project board and three-column asset workspace

**Files:**
- Create: `web/src/pages/ProjectsPage.tsx`
- Create: `web/src/pages/ProjectsPage.test.tsx`
- Create: `web/src/pages/ProjectWorkspacePage.tsx`
- Create: `web/src/pages/ProjectWorkspacePage.test.tsx`
- Create: `web/src/components/ProjectBoard.tsx`
- Create: `web/src/components/AssetTree.tsx`
- Create: `web/src/components/AssetPreview.tsx`
- Create: `web/src/components/RequirementBanner.tsx`
- Modify: `web/src/types.ts`
- Modify: `web/src/api/queries.ts`
- Modify: `web/src/styles/layout.css`
- Modify: `web/src/styles/components.css`
- Modify: `web/src/styles/responsive.css`

- [ ] **Step 1: Write failing board and empty-project tests**

Assert exactly four board columns (`待制作`, `制作中`, `待发布`, `已发布`), account filtering, and the corrected empty state:

```tsx
render(<ProjectWorkspacePage />, {wrapper: projectWithNoAssets()})
expect(await screen.findByText('缺少口播稿、配音、SRT、账号背景图')).toBeInTheDocument()
expect(screen.queryByText('当前阶段所需素材齐全')).not.toBeInTheDocument()
expect(screen.getByRole('button', {name:'生成混剪'})).toBeDisabled()
```

Add preview tests for source text, continuous text, spoken text, audio, SRT, video, directory draft, version history, and stale badges.

- [ ] **Step 2: Run and confirm failure**

```powershell
npm --prefix web run test:run -- ProjectsPage ProjectWorkspacePage
```

Expected: FAIL because current UI has seven stage columns and a drawer.

- [ ] **Step 3: Implement the four-column board**

Map backend publication states to four columns without inferring readiness. Each card shows account, current production step, missing/stale count, active task status, and last update. Account filter works independently from route and can include disabled accounts only when explicitly selected.

- [ ] **Step 4: Implement the confirmed three-column workspace**

Left: grouped asset tree (`原始输入`, `文案产物`, `制作素材`, `视频输出`) with state/version. Middle: selected preview and editing/version actions. Right: compact task conversation and action launcher. Header: production-step rail and account identity. Both side columns collapse; below 900 px switch to tabs.

- [ ] **Step 5: Implement preview/edit behavior**

`AssetPreview` behavior:

```text
text/plain, text/markdown → fetched text viewer/editor with expected_version_id
application/x-subrip → SRT editor plus validation findings
audio/* → <audio controls preload="metadata">
video/* → <video controls preload="metadata">
storage_kind=directory → file-count/hash/validation/registered-path metadata
unknown binary → metadata and download button only
```

On a new text version, refetch project requirements and show downstream stale assets. Do not silently start regeneration.

- [ ] **Step 6: Implement action launchers**

Use backend requirements to enable actions. Send only `action`, optional instruction, and approval mode. Display the returned `missing_inputs` on a `409`. Never send account ID, local paths, or Skill names from the UI.

- [ ] **Step 7: Run tests and build**

```powershell
npm --prefix web run test:run -- ProjectsPage ProjectWorkspacePage AssetPreview
npm --prefix web run lint
npm --prefix web run build
```

Expected: PASS.

- [ ] **Step 8: Commit**

```powershell
git add web/src/pages/ProjectsPage* web/src/pages/ProjectWorkspacePage* web/src/components/ProjectBoard.tsx web/src/components/AssetTree.tsx web/src/components/AssetPreview.tsx web/src/components/RequirementBanner.tsx web/src/types.ts web/src/api/queries.ts web/src/styles internal/webui/dist
git commit -m "feat: add three-column project asset workspace"
```

### Task 21: Build full task inspection, replies, settings, and Skill health

**Files:**
- Create: `web/src/pages/TaskDetailPage.tsx`
- Create: `web/src/pages/TaskDetailPage.test.tsx`
- Create: `web/src/pages/SettingsPage.tsx`
- Create: `web/src/pages/SettingsPage.test.tsx`
- Create: `web/src/components/TaskConversation.tsx`
- Create: `web/src/components/TaskTechnicalLog.tsx`
- Create: `web/src/components/HealthBadge.test.tsx`
- Modify: `web/src/api/client.ts`
- Modify: `web/src/api/queries.ts`
- Modify: `web/src/styles/components.css`
- Modify: `web/src/styles/responsive.css`

- [ ] **Step 1: Write failing task-detail tests**

Assert the four sections and reply flow:

```tsx
for (const name of ['对话','发送内容','技术日志','产物']) {
  expect(await screen.findByRole('tab', {name})).toBeInTheDocument()
}
await user.click(screen.getByRole('tab', {name:'发送内容'}))
expect(screen.getByText('task_manifest.json')).toBeInTheDocument()
expect(screen.getByText('$finance-viral-remix')).toBeInTheDocument()
expect(screen.queryByText('secret-value')).not.toBeInTheDocument()
```

Reply test selects a structured option, adds optional text, sends `/reply`, and displays `继续执行中`.

- [ ] **Step 2: Write failing settings tests**

Assert concurrency options 1–4, masked secret fields, dependency test buttons, Skill path/hash/mtime, and the update warning. The DOM must never contain secret response fixture values.

- [ ] **Step 3: Run and confirm failure**

```powershell
npm --prefix web run test:run -- TaskDetailPage SettingsPage HealthBadge
```

Expected: FAIL because the pages and components do not exist.

- [ ] **Step 4: Implement task detail**

Conversation displays persisted user/assistant/system messages and structured question options. Sent content displays redacted Prompt, manifest JSON, input roles/versions/sizes/hashes, Skill snapshot, and configuration snapshot. Technical log displays redacted argv, session ID, JSONL events, stderr, exit code, queue reason, and timestamps. Artifacts displays engineering files and registered asset versions with preview links.

Use one `EventSource` per open task and reconnect with the last seen event ID through the URL query fallback supported by the backend. Close the stream on unmount.

- [ ] **Step 5: Implement settings sections**

Sections: System, baokuan, Obsidian, Search, Codex CLI, Montage, Skill Registry, Authentication. Save public fields and changed secret values only; an empty secret input means “leave unchanged”. Each dependency has an explicit Test button and `ok/not_configured/offline` result. Skill rows show fingerprint and a “重新扫描” action.

- [ ] **Step 6: Implement Skill-change behavior**

If a task in `awaiting_input` reports `skill_snapshot_outdated`, disable ordinary reply, explain that the old session may not reload updated rules, and offer “创建替代任务”. Link to both old and new task details after creation.

- [ ] **Step 7: Run frontend tests and build**

```powershell
npm --prefix web run test:run
npm --prefix web run lint
npm --prefix web run build
```

Expected: PASS.

- [ ] **Step 8: Commit**

```powershell
git add web/src/pages/TaskDetailPage* web/src/pages/SettingsPage* web/src/components/TaskConversation.tsx web/src/components/TaskTechnicalLog.tsx web/src/components/HealthBadge* web/src/api web/src/styles internal/webui/dist
git commit -m "feat: expose codex task and system diagnostics"
```

## Phase F — migration rehearsal and acceptance

### Task 22: Complete legacy import, integration tests, and operator documentation

**Files:**
- Create: `internal/store/legacy_import.go`
- Create: `internal/store/legacy_import_test.go`
- Create: `internal/integration/workflow_v2_test.go`
- Create: `web/playwright.config.ts`
- Create: `web/e2e/workflow-v2.spec.ts`
- Create: `web/e2e/fixtures/account-background.png`
- Modify: `README.md`
- Modify: `cmd/console/main.go`

- [ ] **Step 1: Write failing legacy import tests**

Build a temporary legacy data root containing known project text/audio/SRT/draft/video plus manifest, plan, QC, and an unknown file. Assert:

```text
known formal files become versioned assets
manifest/plan/QC/log become task_artifacts
unknown file remains in place and creates one migration warning
no source file is deleted or overwritten
second reconciliation is idempotent
```

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/store -run 'TestLegacyImportClassifiesWithoutDeleting|TestLegacyImportIsIdempotent' -v
```

Expected: compile failure because `LegacyImporter` does not exist.

- [ ] **Step 3: Implement conservative legacy import**

Classify by both existing database type and validated MIME/content. Copy recognized files into managed V2 paths before registering. Store original path and import hash in migration metadata. Engineering filenames match exact allowlists (`task_manifest.json`, `production_plan.json`, `production_plan.md`, `*qc*.json`, `events.jsonl`, `stderr.log`). Everything else remains untouched and is reported.

- [ ] **Step 4: Add Go end-to-end integration tests with fake Codex**

The integration test must:

```text
bootstrap/login
create account with fixed image
create project from source_script
verify remix ready and montage blocked
enqueue fake remix
receive SSE agent events
persist an awaiting question
reply and resume the same session
complete with continuous_script + spoken_script
edit continuous_script and observe downstream stale
upload narration + SRT
verify montage manifest includes the account background
```

Run the fake PowerShell CLI only; do not call real Codex, baokuan, Obsidian, WeChat Channels, or Jianying.

- [ ] **Step 5: Add Playwright acceptance test**

Start the Go server against a temporary database/data root and fake CLI. The browser test logs in with `123321`, changes no secret, creates an account/project from source, verifies missing-asset text, opens task details, inspects Prompt/manifest/log tabs, sends a reply, previews text/audio/SRT, and changes concurrency from 2 to 4.

- [ ] **Step 6: Document operation and recovery**

README must include:

```text
first login and password change
LAN URL and firewall scope
where project files and backups live
how desktop and CLI Skill synchronization works
why waiting tasks may require replacement after Skill changes
how files reach Codex through task_manifest.json
what every formal asset means and whether it is uploaded/generated/editable
how to configure/test baokuan, Obsidian, Grok, media library, and Jianying
how to recover interrupted tasks
explicit statement that verification does not open WeChat Channels or Jianying
```

- [ ] **Step 7: Run the full verification suite**

```powershell
go test ./...
npm --prefix web run test:run
npm --prefix web run lint
npm --prefix web run build
npm --prefix web run test:e2e
python -m unittest discover 'C:\Users\prepare\.codex\skills\finance-topic-selector\tests' -v
python -m unittest discover 'C:\Users\prepare\.codex\skills\finance-viral-remix\tests' -v
python -m unittest discover 'C:\Users\prepare\.codex\skills\jianying-montage-draft\tests' -v
git diff --check
```

Expected: every command exits `0`; no browser test starts WeChat Channels or Jianying; `git status --short` contains only intended tracked changes.

- [ ] **Step 8: Perform a temporary migration rehearsal**

Copy the current database and project directory into a new temporary directory, start the new binary against the copy, inspect migration report counts and all project readiness states, then stop it. Do not point this rehearsal at the live `video-console-data` directory.

- [ ] **Step 9: Commit**

```powershell
git add internal/store/legacy_import* internal/integration web/playwright.config.ts web/e2e README.md cmd/console/main.go internal/webui/dist
git commit -m "test: verify complete console v2 workflow"
```

### Task 23: Final specification audit and release handoff

**Files:**
- Modify only files required by verified audit findings
- Reference: `docs/superpowers/specs/2026-08-03-video-console-workflow-v2-design.md`
- Reference: this implementation plan

- [ ] **Step 1: Map every specification section to evidence**

Create a task-local checklist with one row for each specification section 4–16 and columns `implementation`, `test`, and `result`. Each row must name an exact package/test. Do not add a repository document unless the user requests one.

- [ ] **Step 2: Run security-focused checks**

```powershell
rg -n "sk-[A-Za-z0-9_-]{16,}|GROK_SEARCH_API_KEY\s*[:=]\s*[^<{]" . 'C:\Users\prepare\.codex\skills\finance-topic-selector' 'C:\Users\prepare\.codex\skills\finance-viral-remix' 'C:\Users\prepare\.codex\skills\jianying-montage-draft'
go test ./... -run 'Auth|CSRF|Secret|Redact|Path|Traversal|Range|Manifest|Fingerprint' -v
```

Expected: secret scan returns no real credential; security tests PASS.

- [ ] **Step 3: Run concurrency-focused checks**

```powershell
go test ./internal/codex ./internal/store -run 'Limit|Concurrent|ProjectLock|Recover|Resume|Replacement' -count=10
python -m unittest 'C:\Users\prepare\.codex\skills\jianying-montage-draft\tests\test_concurrency_lock.py' -v
```

Expected: PASS across all repetitions with no leaked process or lock directory.

- [ ] **Step 4: Verify clean repository state and commit audit fixes**

```powershell
git diff --check
git status --short
```

If the audit finds a defect, return to the owning numbered task, add a failing regression test there, apply that task's exact file/commit procedure, and rerun Steps 1–3 of this audit. If no audit fix is needed, do not create an empty commit.

- [ ] **Step 5: Prepare the release handoff**

Report the final branch, commits, full test results, temporary migration rehearsal result, installed Skill fingerprints, configured-but-masked dependency status, LAN address, remaining user-only action of changing the initial password, and the explicit fact that WeChat Channels/Jianying UI were not opened.

## Plan completion criteria

- The empty-project readiness bug is covered by domain, API, frontend, and end-to-end tests.
- Every Codex task is clickable and exposes conversation, sent content, technical logs, and artifacts.
- Same-session reply/resume is persistent and Skill changes create explicit replacement tasks.
- Text, audio, SRT, video, and directory draft assets have correct preview behavior and version history.
- Global settings are editable, secrets are encrypted/masked, dependencies are testable, and Skill fingerprints are visible.
- Topic conversations work before project creation; project creation requires user confirmation of a `可写稿` card.
- Direct source-script remix works without a topic card and defaults to visible enhanced mode.
- All three installed Skills implement the explicit console contracts while remaining directly usable from desktop/CLI.
- Montage receives narration, spoken text, SRT, and account background through the manifest and uses the deterministic builder without UI tests.
- Migration is backed up, idempotent, non-destructive, and rehearsed on a copy.
- All Go, React, Python, integration, Playwright, security, and concurrency checks pass.

## Specification coverage map

| Approved design area | Implementation tasks | Primary verification |
|---|---:|---|
| Module boundaries and protected runtime | 5–7 | auth/app/settings Go tests |
| Dual-entry home and pre-project topics | 14, 15, 19 | idea API tests + IdeasPage tests |
| Four-column board and three-column workspace | 13, 20 | project API + workspace component tests |
| Formal asset meanings, previews, versions, stale graph | 2, 4, 13, 20 | domain/store/API/frontend tests |
| Task manifest and file delivery to CLI | 8, 9 | manifest/command tests |
| Clickable task details, live events, questions, same-session replies | 10–12, 21 | runner/SSE/task-detail tests |
| Settings, DPAPI secrets, health checks | 5–7, 21 | security/settings/frontend tests |
| Desktop/CLI Skill fingerprint synchronization | 6, 11, 21 | registry/replacement tests |
| Topic-selector console contract | 15 | topic Skill Python tests |
| Viral-remix console contract and spoken equality | 16 | remix Skill Python tests |
| Montage manifest, portable profile, deterministic builder, locks | 17 | montage Python tests |
| Backup, legacy import, interrupted recovery | 3, 11, 22 | migration/import/recovery tests |
| No WeChat/Jianying UI testing | 17, 22, 23 | command assertions and handoff audit |
