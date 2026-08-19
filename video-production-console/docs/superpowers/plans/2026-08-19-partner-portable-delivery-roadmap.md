# Partner Portable Delivery Roadmap Index

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver editable Jianying drafts to approved Windows partners through four gated milestones, beginning with a real cross-computer proof and ending with a production Cloudflare deployment.

**Architecture:** The work is split into four independently testable plans because the design spans package format/import, package delivery, partner workflow/UI, and production operations. Each milestone produces a usable artifact and blocks the next milestone until its exit evidence is accepted; the existing `local_jianying` path remains deployable after every merge.

**Tech Stack:** Go, SQLite, React/TypeScript, Jianying plaintext drafts, FFmpeg on the server only, Ed25519, Windows PowerShell, Cloudflare Tunnel/Access.

---

> This file is a non-executable index and gate checklist. The four linked milestone documents are the implementation plans; only their numbered tasks are dispatched to workers.

## Plans and dependency order

```text
M1 cross-computer prototype
  └─ gate: “天中观局” opens and rollback is proven
      └─ M2 formal signed package and download lifecycle
          └─ gate: signing, clipping, Range, expiry, importer regression
              └─ M3 partner owner scope, workflow, 2032 site
                  └─ gate: source-to-ready, IDOR, restart, local regression
                      └─ M4 Cloudflare, Windows deployment, operations
                          └─ gate: production release and rollback drill
```

| Milestone | Detailed plan | Tasks | Primary deliverable | Hard gate |
|---|---|---:|---|---|
| M1 | [M1 importer prototype](2026-08-19-partner-portable-delivery-m1-importer-prototype.md) | 8 | Unsigned hash-verifiable `.vpcdraft` and standalone importer | Real “天中观局” import on second Windows PC |
| M2 | [M2 package delivery](2026-08-19-partner-portable-delivery-m2-package-delivery.md) | 10 | Signed clipped package, lifecycle store, Range download | Tamper/expiry/rotation/import/local regression all pass |
| M3 | [M3 partner portal](2026-08-19-partner-portable-delivery-m3-partner-portal.md) | 9 | Owner-scoped 2032 site and durable nine-stage workflow | Source-to-ready, restart retry and IDOR suite pass |
| M4 | [M4 deployment and operations](2026-08-19-partner-portable-delivery-m4-deployment-operations.md) | 10 | Cloudflare Access/Tunnel, Windows service, operations release | Backup/rollback/drill/release checklist passes |

Total: 37 implementation tasks. Every task in the four detailed plans follows failing test → observed failure → minimal implementation → passing test → isolated commit.

## Phase 0: Protect the current system before feature work

- [ ] Record `git status --short`, current commit, Go version, Node version, and current release version in the M1 evidence template.
- [ ] Run `go test ./... -count=1`; store the baseline result. Failures that predate this project are documented before any implementation commit.
- [ ] Run `cd web; npm test; npm run typecheck; npm run build:verify`; store the baseline result.
- [ ] Export a recoverable copy of the current Jianying `root_meta_info.json` and identify the exact “天中观局” draft directory without modifying either.
- [ ] Confirm a second Windows computer has Jianying installed and does not receive the sender's complete material library.

## M1 execution gate

- [ ] Execute all eight tasks in the M1 plan in order; review each commit before starting the next task.
- [ ] Use only disposable Jianying fixtures for failure injection; the real Jianying index is read-only until registration tests pass.
- [ ] Package “天中观局” on computer A; transfer only package/importer/README/checksums.
- [ ] Import and open the editable timeline on computer B; record hash, dry-run, receipt, screenshot, idempotent rerun, and rollback evidence.
- [ ] Stop and repair M1 if any referenced image/video/audio/BGM/SFX is offline, any sender path appears, or rollback does not restore the index.
- [ ] Approve the M1 gate before checking out the first M2 task.

## M2 execution gate

- [ ] Execute the ten M2 tasks against the exact M1 package names; reject any new parallel importer/package namespace.
- [ ] Verify video bytes include the union of actual source uses plus up to three seconds on each side and draft offsets point at the used interval.
- [ ] Verify canonical payload, Ed25519 signature, final byte hashes, key rotation, public key release, and private-key exclusion.
- [ ] Verify owner-scoped package metadata, seven-day expiry, renewal, mark-before-delete, audit, 206 resume, and 416 behavior.
- [ ] Repeat the M1 cross-computer test with the formal importer and run a complete `local_jianying` regression.
- [ ] Approve the M2 gate before creating partner users or enabling the partner workflow.

## M3 execution gate

- [ ] Execute the nine M3 tasks with fixture verifier injection only in tests; production must reject a missing verifier.
- [ ] Verify every project/task/asset/package/progress/download query begins from authenticated owner scope and cross-owner access is indistinguishable from absence.
- [ ] Drive `queued → rewriting → narrating → montaging → validating → clipping_media → packaging → signing → ready` through a restart and a transient packaging failure.
- [ ] Confirm portable montage stops at plaintext validation and never invokes the host Jianying registrar; confirm an existing local project still invokes it.
- [ ] Verify the four partner pages, friendly status vocabulary, CSRF, body/rate bounds, and 2030/2032 route isolation.
- [ ] Approve the M3 gate before configuring public Tunnel/Access state.

## M4 execution gate

- [ ] Execute the ten M4 tasks; Cloudflare Access remains the only OTP provider and the application sends no OTP.
- [ ] Confirm Tunnel ingress contains only the partner hostname to `127.0.0.1:2032` plus the 404 fallback; 2030 has no ingress.
- [ ] Verify Access JWT signature/JWKS, issuer, audience, email allowlist, subject, expiry/not-before, JTI replay, and session-version revocation.
- [ ] Verify Windows service identity, DPAPI/ACL-protected signing keys, old-key verification, importer compatibility matrix, and loopback listeners.
- [ ] Run database integrity backup, migration, rollback, worker-reclaim, disk-low, Tunnel-down, expired-package, and redaction drills.
- [ ] Build the final release and sign the manual Access checklist only after automated M1–M4 suites pass.

## Required evidence matrix

| Evidence | M1 | M2 | M3 | M4 |
|---|:---:|:---:|:---:|:---:|
| Go/frontend test logs | ✓ | ✓ | ✓ | ✓ |
| Package SHA-256 | ✓ | ✓ | ✓ | ✓ |
| Signature/key ID verification | — | ✓ | ✓ | ✓ |
| Import receipt and Jianying screenshot | ✓ | ✓ | ✓ | ✓ |
| Failure/rollback proof | ✓ | ✓ | ✓ | ✓ |
| Owner-scope/IDOR result | — | transport scope | ✓ | ✓ |
| Range/expiry/renew/purge audit | — | ✓ | ✓ | ✓ |
| 2030/2032 isolation | — | — | ✓ | ✓ |
| Access/Tunnel manual checklist | — | — | — | ✓ |
| Database backup/integrity hash | — | — | migration fixture | ✓ |

Evidence rules:

- Store only hashes, IDs, stable error codes, redacted log summaries, receipts, and screenshots needed for acceptance.
- Do not store source absolute paths, raw emails, Access tokens, session tokens, private signing keys, API keys, or full partner media.
- A manually accepted screenshot never substitutes for a failing automated hash/signature/index test.

## Merge and rollback policy

- [ ] Keep each detailed-plan task as its own commit with the exact file list shown in that task.
- [ ] Run focused tests before commit and the milestone-wide suite at the exit gate.
- [ ] Merge M1, M2, M3, and M4 sequentially; do not squash away gate evidence references.
- [ ] Roll back the current milestone by reverting only its commits; do not reset, clean, or discard unrelated worktree changes.
- [ ] Database changes are forward-compatible during a milestone; release rollback uses verified backup/restore once M4 introduces operational migrations.
- [ ] Keep partner listener disabled until M4 injects a real Cloudflare verifier and the Access checklist is signed.

## Completion definition

- [ ] A partner submits source copy through the website without administrator approval.
- [ ] The local host automatically creates a signed `.vpcdraft` containing only referenced, distributable media.
- [ ] The partner downloads with resume support and imports on Windows without Python, FFmpeg, Codex, or the full library.
- [ ] Jianying opens an editable project with correct media; duplicate import and failure rollback are safe.
- [ ] Owner isolation, expiry, audit, monitoring, backup, key rotation, Access, Tunnel, and local-project regression all pass.

## Self-review

- [ ] Confirm the four plans contain all 37 tasks and each task has its own commit checkpoint.
- [ ] Confirm the dependency graph prevents website/Tunnel work before the real cross-computer proof.
- [ ] Confirm fixed interfaces remain `internal/portablepackage`, `internal/draftimport`, `BuildService`, `NewPartnerApp`, and loopback ports 2030/2032.
- [ ] Confirm all design requirements map to at least one exit-gate check and evidence item.
