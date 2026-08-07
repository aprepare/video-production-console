# Jianying Readable Draft Names Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show every new and recoverable Jianying draft as `账号名_第一个短标题_UUID后6位` while preserving the task UUID directory, draft ID, asset ID, and registration integrity contract.

**Architecture:** Resolve and freeze a sanitized display name while preparing the montage manifest, then let the deterministic Jianying script write that name into draft metadata and `root_meta_info.json` while continuing to register under the immutable UUID directory. Extend the trusted host validator and receipt so a display-name mismatch is detectable, and provide a lock-protected metadata-only reconciliation path for already registered valid drafts.

**Tech Stack:** Go, Python 3, SQLite, Jianying plaintext JSON, existing `jianying-registration` lease.

**Execution order:** Tasks 1–5 may begin after the workflow plan's task-contract changes are stable. Task 6 requires the project-workbench files created by that plan and must be integrated after them.

---

## File map

- Create `internal/montage/display_name.go`, `internal/montage/display_name_test.go`: resolve the account/project/title inputs and sanitize the display name.
- Modify `internal/httpapi/task_manifest.go`, `internal/httpapi/task_manifest_test.go`: freeze `draft_display_name` in montage manifests.
- Create `internal/publishing/package.go`, `internal/publishing/package_test.go`: share the bounded, SHA-verified publishing-package reader between manifest preparation and HTTP views.
- Modify `internal/codex/manifest.go`, `internal/codex/manifest_test.go`, `schemas/task-manifest.schema.json`: carry the non-secret display field.
- Modify `C:/Users/prepare/.codex/skills/jianying-montage-draft/scripts/run_montage_job.py`: use display name in workspace metadata, registration index, and receipt.
- Modify `C:/Users/prepare/.codex/skills/jianying-montage-draft/references/console-contract.md`: document that display name is presentation metadata only.
- Modify `internal/montage/registrar.go`, `internal/montage/validator.go`, `internal/montage/validator_test.go`: validate the expected display name.
- Modify `internal/montage/coordinator.go`, `internal/montage/coordinator_test.go`, `internal/store/montage.go`, `internal/store/montage_test.go`: reconcile older registered drafts without rebuilding them.
- Modify `internal/httpapi/task_results.go`, `web/src/project-workbench/ProjectAssets.tsx`: expose the readable name in the console.

### Task 1: Resolve a deterministic, Windows-safe display name

**Files:**
- Create: `internal/montage/display_name.go`
- Create: `internal/montage/display_name_test.go`

- [ ] **Step 1: Write failing table-driven tests**

Cover a normal title, missing short titles, reserved Windows characters, control characters, trailing dots/spaces, repeated separators, long account/project names, and Unicode Chinese. Always preserve the final underscore plus six lowercase UUID characters.

```go
func TestDraftDisplayNameUsesFirstShortTitle(t *testing.T) {
	got := BuildDraftDisplayName(
		"财富觉醒02",
		"项目兜底",
		"存款大搬家",
		"984c42ec-67b8-4d3f-99e3-d3d7a4b66205",
	)
	if got != "财富觉醒02_存款大搬家_b66205" {
		t.Fatalf("display name=%q", got)
	}
}
```

The implementation must derive the exact six-character suffix through a helper such as `taskSuffix(taskID)`; for this fixture the suffix is `b66205`.

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/montage -run DraftDisplayName -count=1
```

- [ ] **Step 3: Implement the pure helper**

```go
func BuildDraftDisplayName(account, project, shortTitle, taskID string) string {
	label := strings.TrimSpace(shortTitle)
	if label == "" { label = strings.TrimSpace(project) }
	account = sanitizeDraftLabel(account, 24)
	label = sanitizeDraftLabel(label, 36)
	suffix := strings.ToLower(strings.ReplaceAll(taskID, "-", ""))
	if len(suffix) > 6 { suffix = suffix[len(suffix)-6:] }
	return strings.Join([]string{fallback(account, "未命名账号"), fallback(label, "未命名项目"), suffix}, "_")
}
```

`sanitizeDraftLabel` removes `< > : " / \\ | ? *`, C0 control characters, trims leading/trailing whitespace and dots, collapses repeated underscores, counts Unicode runes rather than bytes, and never changes the UUID suffix.

- [ ] **Step 4: Verify and commit**

```powershell
go test ./internal/montage -run DraftDisplayName -count=1
git add internal/montage/display_name.go internal/montage/display_name_test.go
git commit -m "feat: build readable Jianying draft names"
```

### Task 2: Freeze the display name in the montage task manifest

**Files:**
- Modify: `internal/codex/manifest.go`
- Modify: `internal/codex/manifest_test.go`
- Modify: `schemas/task-manifest.schema.json`
- Modify: `internal/httpapi/task_manifest.go`
- Modify: `internal/httpapi/task_manifest_test.go`
- Modify: `internal/httpapi/task_results.go`
- Create: `internal/publishing/package.go`
- Create: `internal/publishing/package_test.go`

- [ ] **Step 1: Write failing manifest tests**

Create a project with an account and a completed remix task whose validated `publishing_package` contains `short_titles`. Assert the next `montage.execute` manifest contains the first short title-derived `non_secret_settings.draft_display_name`. Add a second test without a publishing package and assert it falls back to the project title.

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/codex ./internal/httpapi ./internal/publishing -run 'DraftDisplayName|MontageManifest|PublishingPackage' -count=1
```

- [ ] **Step 3: Add the manifest field and trusted resolver**

Extend `ManifestSettings` and the JSON schema:

```go
DraftDisplayName string `json:"draft_display_name,omitempty"`
```

Move the existing bounded file, regular-file, SHA-256, and JSON checks from `internal/httpapi/task_results.go` into `internal/publishing.Reader`, keeping the HTTP response shape unchanged. In `TaskManifestPreparer.Prepare`, query the project/account names and locate the newest completed remix task in the same project. Read only its persisted, SHA-verified `publishing_package` artifact through that reader; use `short_titles[0]` when valid, otherwise the project title. Freeze the final value before Codex starts so a later title edit cannot silently rename an in-flight job.

- [ ] **Step 4: Return the readable name in task result views**

Add `display_name` to `registered_asset` and the montage result by reading the manifest value. Continue returning the actual UUID directory basename separately as `storage_name`; do not replace `registered_path` or the task ID.

- [ ] **Step 5: Verify and commit**

```powershell
go test ./internal/codex ./internal/httpapi ./internal/publishing -count=1
git add internal/codex/manifest.go internal/codex/manifest_test.go schemas/task-manifest.schema.json internal/httpapi/task_manifest.go internal/httpapi/task_manifest_test.go internal/httpapi/task_results.go internal/publishing/package.go internal/publishing/package_test.go
git commit -m "feat: freeze Jianying display names in task manifests"
```

### Task 3: Write readable metadata without changing the UUID workspace or target directory

**Files:**
- Modify: `C:/Users/prepare/.codex/skills/jianying-montage-draft/scripts/run_montage_job.py`
- Create: `C:/Users/prepare/.codex/skills/jianying-montage-draft/scripts/test_run_montage_job_display_name.py`
- Modify: `C:/Users/prepare/.codex/skills/jianying-montage-draft/references/console-contract.md`

- [ ] **Step 1: Write failing Python tests**

Use temporary manifests and Jianying roots. Assert `load_context` rejects an empty, overlong, or unsafe `draft_display_name`; the workspace remains `output_dir/workspace/<task_uuid>`; the registered target remains `jianying_root/<task_uuid>`; `draft_meta_info.json` and the matching `root_meta_info.json` entry use the readable name; and the receipt reports both immutable identity and display name.

```python
assert context.workspace.name == context.task_id
assert context.draft_display_name == "财富觉醒02_存款大搬家_b66205"
assert registration["registered_path"].endswith(context.task_id)
assert registration["draft_display_name"] == context.draft_display_name
```

- [ ] **Step 2: Run and confirm failure**

```powershell
python -m unittest C:\Users\prepare\.codex\skills\jianying-montage-draft\scripts\test_run_montage_job_display_name.py -v
```

- [ ] **Step 3: Extend `JobContext` and draft creation**

Parse `non_secret_settings.draft_display_name` into `JobContext`. Use it when constructing workspace `draft_meta_info.json`; keep `job_id` for workspace paths, lock ownership, task identity, target folder, backup filenames, and generated draft IDs.

- [ ] **Step 4: Extend the registration transaction**

Inside the existing same-process `jianying-registration` lease, set the copied draft metadata and index entry `draft_name` to `context.draft_display_name`. Keep `draft_fold_path=target`, where `target.name == context.task_id`. Add `draft_display_name`, `task_id`, and `registered_path` to `registration-result.json`. Verify the just-written index entry matches all three values before releasing the lock.

- [ ] **Step 5: Update the Skill contract and verify**

Document this invariant in `console-contract.md`:

```text
draft_display_name is presentation metadata only. task_id/job_id remains the workspace name,
registered directory name, lock owner, task identity, and recovery key.
```

Run:

```powershell
python -m unittest C:\Users\prepare\.codex\skills\jianying-montage-draft\scripts\test_run_montage_job_display_name.py -v
python C:\Users\prepare\.codex\skills\jianying-montage-draft\scripts\run_montage_job.py --help
```

Do not invoke the `register` command against the real Jianying root during this test.

### Task 4: Validate display metadata in the trusted Go host

**Files:**
- Modify: `internal/montage/registrar.go`
- Modify: `internal/montage/validator.go`
- Modify: `internal/montage/validator_test.go`
- Modify: `internal/montage/coordinator.go`
- Modify: `internal/montage/coordinator_test.go`

- [ ] **Step 1: Write failing validator tests**

Add fixtures where the directory and content hashes are valid but the receipt, draft metadata, or root index has the wrong display name. All must fail with `ErrInvalidRegistration`. A correct readable name with a UUID directory must pass.

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/montage -run 'DisplayName|ReadableName' -count=1
```

- [ ] **Step 3: Extend trusted request/result types**

```go
type ValidationRequest struct {
	TaskID, DisplayName, WorkspacePath, ReceiptPath, JianyingRoot string
}

type RegisterResult struct {
	RegisteredPath, ReceiptPath, DraftID, DisplayName string
	SourceContentSHA256, RegisteredContentSHA256, DirectorySHA256 string
	DurationUS int64
}
```

`Coordinator.resolveRuntime` reads `draft_display_name` from the task-bound manifest and passes it to the registrar. `ValidateRegisteredDraft` verifies the receipt name, registered `draft_meta_info.json` name, and index `draft_name` while retaining the existing path, draft-ID, content-hash, directory-hash, and index checks. On successful registration, pass `result.DisplayName` as `RegistrationSuccess.Filename` so the ready `mix_draft` asset also has the readable name; keep `RegisteredPath` on the immutable UUID directory.

- [ ] **Step 4: Verify and commit**

```powershell
go test ./internal/montage -count=1
git add internal/montage/registrar.go internal/montage/validator.go internal/montage/validator_test.go internal/montage/coordinator.go internal/montage/coordinator_test.go
git commit -m "feat: validate Jianying display metadata"
```

### Task 5: Reconcile already registered valid drafts under the registration lock

**Files:**
- Modify: `C:/Users/prepare/.codex/skills/jianying-montage-draft/scripts/run_montage_job.py`
- Modify: `C:/Users/prepare/.codex/skills/jianying-montage-draft/scripts/test_run_montage_job_display_name.py`
- Modify: `internal/domain/montage.go`
- Modify: `internal/store/montage.go`
- Modify: `internal/store/montage_test.go`
- Modify: `internal/montage/coordinator.go`
- Modify: `internal/montage/coordinator_test.go`
- Modify: `cmd/console/main.go`

- [ ] **Step 1: Write failing idempotent reconciliation tests**

Prove that a valid registered UUID target with an old UUID display name is metadata-reconciled exactly once; `draft_content.json` bytes, folder path, draft ID, asset ID, and task ID remain unchanged; only display metadata and the corresponding directory integrity hash change; an active/invalid/unknown draft is skipped; and lock contention reports queued/busy rather than bypassing the lease.

- [ ] **Step 2: Run and confirm failure**

```powershell
go test ./internal/store ./internal/montage -run 'ReconcileDraftDisplay|BackfillDisplay' -count=1
python -m unittest C:\Users\prepare\.codex\skills\jianying-montage-draft\scripts\test_run_montage_job_display_name.py -v
```

- [ ] **Step 3: Add a metadata-only script phase**

Add `reconcile-name --manifest <path> --draft <registered-uuid-directory>` to the deterministic script. It must acquire the existing `jianying-registration` lease, revalidate the authoritative manifest, target directory, draft ID, content fingerprint, and index entry, atomically update only `draft_name` fields plus a reconciliation receipt, verify them, and release in `finally`. It must not stop or launch Jianying unless the existing registration contract already requires the process to be stopped for index mutation; it must never rebuild or recopy draft content.

- [ ] **Step 4: Add host discovery and startup scheduling**

`MontageRepository.DraftsNeedingDisplayName` returns only ready `mix_draft` assets whose completed montage task has a valid manifest and registered UUID directory. `Coordinator.ReconcileDisplayNames` queues the metadata-only jobs on the existing serialized host queue after active registration recovery. After the script receipt and trusted validation succeed, update that asset version's `filename` and recomputed directory `sha256` in one reconciliation transaction; never change its ID, path, state, source task, or `draft_content.json`. Failures append warnings and remain retryable; they do not stale a valid mix draft solely because its display name is old.

- [ ] **Step 5: Verify and commit**

```powershell
go test ./internal/store ./internal/montage ./cmd/console -count=1
python -m unittest C:\Users\prepare\.codex\skills\jianying-montage-draft\scripts\test_run_montage_job_display_name.py -v
git add internal/domain/montage.go internal/store/montage.go internal/store/montage_test.go internal/montage/coordinator.go internal/montage/coordinator_test.go cmd/console/main.go
git commit -m "feat: reconcile readable Jianying draft names"
```

Do not run this reconciliation against the real Jianying root as part of automated verification.

### Task 6: Display the readable draft identity in the project workbench

**Files:**
- Modify: `web/src/project-workbench/types.ts`
- Modify: `web/src/project-workbench/ProjectAssets.tsx`
- Modify: `web/src/project-workbench/ProjectWorkbench.test.tsx`

- [ ] **Step 1: Write a failing UI test**

Render a registered montage result and assert the primary label is the readable display name, the technical disclosure shows the UUID storage name, and copying/opening the asset still targets the actual registered asset ID/path through existing controlled APIs.

- [ ] **Step 2: Run and confirm failure**

```powershell
cd web
npx vitest run src/project-workbench/ProjectWorkbench.test.tsx
```

- [ ] **Step 3: Implement the display**

Show:

```text
剪映草稿
财富觉醒02_存款大搬家_b66205
已登记 · 可继续编辑
```

Place `storage_name`, draft ID, and full task UUID only inside the collapsed technical section. Do not render the UUID as the main asset title.

- [ ] **Step 4: Verify and commit**

```powershell
npx vitest run src/project-workbench/ProjectWorkbench.test.tsx
npm run build
git add src/project-workbench/types.ts src/project-workbench/ProjectAssets.tsx src/project-workbench/ProjectWorkbench.test.tsx
git commit -m "feat: show readable Jianying draft names"
```

## Acceptance checklist

- New montage manifests freeze `账号名_第一条短标题_UUID后6位`; missing short titles fall back to the project title.
- Unsafe Windows filename characters and excessive lengths are sanitized without touching the six-character UUID suffix.
- Workspace and registered directory names remain the full task UUID.
- Draft ID, task ID, asset ID, lock owner, and recovery key remain unchanged.
- The trusted receipt, registered metadata, and Jianying root index all agree on the readable display name.
- Existing valid drafts can receive a metadata-only name reconciliation under the existing registration lease; content is not rebuilt or moved.
- Automated checks use temporary roots only and do not launch Jianying or WeChat Video Channels.
