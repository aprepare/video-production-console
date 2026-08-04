# Codex Conversation Workbench Program Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver the confirmed Codex conversation workbench, local history, mobile UI, semantic task results, and truthful Jianying montage completion without interrupting legacy tasks.

**Architecture:** Execute four independently testable plans in order. The first introduces the App Server compatibility boundary; the second builds history and the responsive chat shell; the third projects raw events into task conversations and result cards; the fourth moves Jianying registration to a trusted host post-processor and audits false completions.

**Tech Stack:** Go 1.25, SQLite, `github.com/coder/websocket`, React 19, TypeScript 6, Vite 8, Vitest, Python 3, Codex App Server JSON-RPC.

---

## Execution order

1. [App Server Gateway and conversation persistence](2026-08-05-app-server-gateway.md)
2. [History, chat shell, and mobile experience](2026-08-05-chat-history-mobile-workbench.md)
3. [Semantic task progress and result presentation](2026-08-05-semantic-task-results.md)
4. [Montage registration integrity](2026-08-05-montage-registration-integrity.md)

Plan 4 may start after Plan 1's persistence and coordinator boundaries are merged. It must not ship before its strict completion gate, host registrar, historical audit, and UI state are all enabled together.

## Dirty-worktree rule

The repository already contains user-owned, uncommitted changes. Every implementation task must begin with:

```powershell
git status --short
git diff --check
```

Stage only the exact files listed in that task and verify the index before committing:

```powershell
git diff --cached --name-only
```

Never run `git reset --hard`, `git checkout --`, or clean unrelated untracked files.

## Release gates

- No existing `legacy_exec` task is canceled, restarted, or converted in place.
- App Server remains loopback/stdio-only; browsers never receive App Server credentials.
- The maximum active Codex turn count remains configurable from 1 to 4.
- Automated tests do not open WeChat Channels or Jianying.
- A montage task cannot become completed until a verified registered Jianying path exists.
- The packaged UI passes the mobile-width interaction tests and Go embeds the new Vite build.
