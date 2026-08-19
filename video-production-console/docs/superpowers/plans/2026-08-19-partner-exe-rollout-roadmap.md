# Partner EXE Rollout Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver one self-extracting Windows partner EXE plus a separately distributed scenery library, backed by a device-bound VPS gateway for four partners.

**Architecture:** Implement four independently testable milestones: the VPS gateway, the partner client/edition, the portable runtime/first-run setup, and deployment plus clean-Windows acceptance. Each milestone has its own detailed plan and commit boundary; later milestones consume stable interfaces from earlier ones.

**Tech Stack:** Go 1.25, React 19/TypeScript 6/Vite 8, modernc SQLite, Windows DPAPI, Docker Compose, private-CA TLS, PowerShell release tooling, Python/pyJianYingDraft, FFmpeg/FFprobe.

---

## Approved design

Implementation must conform to:

- `docs/superpowers/specs/2026-08-19-partner-self-extracting-exe-gateway-design.md`

This roadmap supersedes the earlier website, Cloudflare, download-center and `.vpcdraft` plans for the current objective. Do not execute those older plans unless the user explicitly reactivates them.

## Milestone plans

1. `docs/superpowers/plans/2026-08-19-partner-exe-m1-gateway.md`
   - Builds the independent gateway package, SQLite schema, activation/session protocol, model proxy, admin commands and mock-upstream tests.
   - Produces a locally runnable `partner-gateway.exe`/Linux binary without touching the existing console behavior.
2. `docs/superpowers/plans/2026-08-19-partner-exe-m2-client.md`
   - Adds the partner edition, pinned-TLS client, DPAPI device credentials, startup gate, settings/API sanitization, model routing, Aura provisioning and partner UI.
   - Produces a directory-build partner console that can activate against the M1 gateway.
3. `docs/superpowers/plans/2026-08-19-partner-exe-m3-portable.md`
   - Adds the overlay self-extractor, safe version switching, bundled runtime discovery, first-run path wizard, machine profile generation and local media re-indexing.
   - Produces the single EXE and its SHA-256 without requiring local Codex CLI/Python/FFmpeg on the target PC.
4. `docs/superpowers/plans/2026-08-19-partner-exe-m4-deploy-acceptance.md`
   - Creates offline private-CA assets, deploys the gateway to `cpa`, creates the four partner identities, runs the 天中观局 pilot, and records rollback/acceptance evidence before the remaining rollout.

## Locked file ownership

During parallel execution, assign one worker to one milestone and do not allow overlapping edits. M2 and M3 both touch `cmd/console/main.go`, `internal/app/app.go`, frontend `App.tsx`, and release configuration, so they must execute serially in that order. M1 can execute in parallel with early M2 work because it creates new `internal/partnergateway`, `cmd/partner-gateway`, and `deploy/partner-gateway` paths. M4 starts only after M1-M3 are merged and all local tests pass.

## Design coverage matrix

| Approved design area | Implementation owner |
|---|---|
| Standalone gateway, partner DB, authentication, sessions, policy, proxy | M1 Tasks 1-6 |
| Partner edition, pinned client, DPAPI, startup gate, sanitized settings | M2 Tasks 1-6 |
| Capability-limited frontend and model/Aura runtime routing | M2 Tasks 5-8 |
| Self-extracting EXE, safe versions, offline CA, bundled dependencies | M3 Tasks 1-5 |
| Jianying discovery, machine profile, local index, first-run setup | M3 Tasks 6-8 |
| Docker deployment, isolated upstream key, four partner keys | M4 Tasks 1-4 |
| Real models, 天中观局 pilot, update/rollback, remaining rollout | M4 Tasks 5-7 |
| Explicit non-goals: website, auto-update, catalog migration, movie, image-to-video, task approval | Enforced by roadmap scope and M2/M3 endpoint/UI tests |

## Required execution order

- [ ] **Step 1: Establish the baseline before any milestone**

Run:

```powershell
git status --short
go test ./...
npm --prefix web test
npm --prefix web run typecheck
```

Expected: preserve the full `git status` as evidence. If the existing unrelated baseline test failure remains, record its exact test name and continue only with milestone-scoped tests until its owner resolves it; do not reset, restore, stash, clean, or absorb unrelated working-tree changes.

- [ ] **Step 2: Execute M1 and require its gateway contract to pass**

Run:

```powershell
go test ./internal/partnergateway ./cmd/partner-gateway
go test -race ./internal/partnergateway
```

Expected: PASS. No external API is called; all proxy tests use `httptest.Server`.

- [ ] **Step 3: Execute M2 against the frozen M1 protocol**

Run:

```powershell
go test ./internal/partnerclient ./internal/partneredition ./internal/httpapi ./internal/settings ./internal/app ./cmd/console
npm --prefix web test
npm --prefix web run typecheck
```

Expected: PASS. Partner-mode tests prove that Base URLs and keys are absent from responses and rejected on write.

- [ ] **Step 4: Execute M3 and build a self-extracting artifact**

Run with explicit dependency directories outside the repository:

```powershell
pwsh -File scripts/build-partner.ps1 `
  -Version 0.1.0 `
  -PythonRuntimeDir C:\PartnerBuildDeps\python `
  -FFmpegDir C:\PartnerBuildDeps\ffmpeg `
  -MediaResourcesDir C:\PartnerBuildDeps\montage-resources `
  -PinnedCAFile C:\PartnerBuildDeps\tls\partner-ca.crt
```

Expected: `release/partner/0.1.0/video-production-console-partner-0.1.0.exe` and the adjacent `.sha256` file exist; the validation script reports no API key, development-machine absolute path, or unexpected executable.

- [ ] **Step 5: Run the pre-deployment regression gate**

Run:

```powershell
go test ./...
npm --prefix web test
npm --prefix web run typecheck
npm --prefix web run build:verify
pwsh -File scripts/test-partner-package.ps1 -Version 0.1.0
```

Expected: all new and existing non-baseline tests PASS. Any known pre-existing failure must match the Step 1 evidence exactly; a changed failure blocks deployment.

- [ ] **Step 6: Execute M4 on `cpa` without changing the current port 2001 service**

Run the exact deployment and acceptance commands from the M4 plan. The new service must listen on `2443`; existing `cli-proxy-api` on `2001` remains available throughout rollout.

- [ ] **Step 7: Release in the approved account order**

Use these exact display names:

```text
1. 天中观局
2. 居中观
3. 认知漫步
4. 观局思考
```

Expected: 天中观局 completes the full clean-Windows acceptance first. Only then create or deliver usable activation material to the remaining three partners.

- [ ] **Step 8: Final completion commit**

After M4 evidence is committed and secrets are excluded:

```powershell
git status --short
git log --oneline -12
```

Expected: only known user-owned unrelated changes remain. Do not include VPS secrets, partner activation keys, Aura credentials, private-CA key, device secrets, sessions, generated EXE payloads, local databases, or acceptance media in Git.

## Completion definition

The roadmap is complete only when:

- the gateway is independently testable and deployed on `https://23.138.12.112:2443` with pinned private-CA TLS;
- each of the four partner records exists, has its own revocable key, and accepts only one device;
- the single EXE starts without admin rights on a Windows account lacking Codex CLI/Python/FFmpeg;
- partner settings and browser responses expose model names but no upstream URL or secret;
- `gpt-5.6-sol`, `grok-4.6`, all allowed reasoning efforts and `gpt-image-2` pass the real gateway smoke test;
- Aura creates audio, SRT and word timing while subtitles remain disabled in the draft;
- a scenery montage and an image-text project are generated locally and the draft opens in Jianying;
- upgrade and rollback preserve activation data, paths and project history;
- the 天中观局 pilot evidence is approved before the other three deliveries.
