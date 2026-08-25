# Skill Inventory Selector Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a standalone local HTML that inventories every installed Skill instance and plugin package, explains its purpose and Codex CLI/Desktop scope, safely selects recommended disable candidates, and exports a reviewable JSON handoff without changing the machine.

**Architecture:** `skill-cleanup-selector.html` is the only production artifact. It embeds inventory JSON, pure selection/filter/export functions in a separately identifiable script block, and a DOM layer that renders, persists, filters, and exports the selection. A dependency-free Node test reads the HTML, evaluates the pure core with `node:vm`, and verifies the safety contract before browser-level inspection.

**Tech Stack:** HTML5, CSS, vanilla JavaScript, `localStorage`, Blob download, Clipboard API, Node.js built-in test runner, Playwright/browser inspection for final UI verification.

---

### Task 1: Lock the inventory and safety contract

**Files:**
- Create: `tests/skill-cleanup-selector.test.mjs`
- Create: `skill-cleanup-selector.html`

- [ ] **Step 1: Write the failing inventory test**

Create a Node test that reads the future HTML, extracts `<script id="inventory-data" type="application/json">`, and asserts the required inventory contract:

```js
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

const root = path.resolve(import.meta.dirname, "..");
const htmlPath = path.join(root, "skill-cleanup-selector.html");

function loadHtml() {
  return fs.readFileSync(htmlPath, "utf8");
}

function extractScript(html, id) {
  const pattern = new RegExp(`<script[^>]+id=["']${id}["'][^>]*>([\\s\\S]*?)<\\/script>`);
  const match = html.match(pattern);
  assert.ok(match, `missing script #${id}`);
  return match[1].trim();
}

test("inventory contains every Skill instance and plugin package", () => {
  const inventory = JSON.parse(extractScript(loadHtml(), "inventory-data"));
  assert.equal(inventory.summary.nonPluginSkillFiles, 159);
  assert.equal(inventory.summary.uniqueSkillNames, 144);
  assert.equal(inventory.summary.duplicateNameGroups, 15);
  assert.equal(inventory.summary.pluginPackages, 18);
  assert.equal(inventory.summary.pluginSkillFiles, 54);
  assert.equal(inventory.items.filter((item) => item.kind === "skill").length, 159);
  assert.equal(inventory.items.filter((item) => item.kind === "plugin").length, 18);
});

test("the three console Skills are locked core dependencies", () => {
  const inventory = JSON.parse(extractScript(loadHtml(), "inventory-data"));
  const core = new Set([
    "finance-topic-selector",
    "finance-viral-remix",
    "jianying-montage-draft",
  ]);
  const rows = inventory.items.filter((item) => core.has(item.name));
  assert.equal(rows.length, 3);
  for (const item of rows) {
    assert.equal(item.recommendation, "required");
    assert.equal(item.selectable, false);
    assert.equal(item.consoleDependency, true);
  }
});
```

- [ ] **Step 2: Run the test and verify RED**

Run:

```powershell
node --test tests/skill-cleanup-selector.test.mjs
```

Expected: FAIL with `ENOENT` for `skill-cleanup-selector.html`.

- [ ] **Step 3: Add the minimal HTML and complete inventory JSON**

Create `skill-cleanup-selector.html` with the final document shell and an embedded inventory object using this schema for all 177 top-level installed instances:

```json
{
  "schemaVersion": "1.0",
  "generatedAt": "2026-08-04T00:00:00+08:00",
  "summary": {
    "nonPluginSkillFiles": 159,
    "uniqueSkillNames": 144,
    "duplicateNameGroups": 15,
    "pluginPackages": 18,
    "pluginSkillFiles": 54
  },
  "items": [
    {
      "id": "skill:codex:finance-topic-selector",
      "kind": "skill",
      "name": "finance-topic-selector",
      "description": "从爆款库和 Obsidian 生成、确认并深化财经选题卡。",
      "path": "C:\\Users\\prepare\\.codex\\skills\\finance-topic-selector\\SKILL.md",
      "source": "codex-user",
      "category": "核心生产链",
      "recommendation": "required",
      "reason": "视频生产控制台硬依赖，删除会中断选题任务。",
      "selectable": false,
      "desktopVisible": true,
      "cliOfficialRoot": false,
      "consoleDependency": true,
      "duplicateState": "none"
    }
  ]
}
```

Every Skill file receives its exact `SKILL.md` path. Every plugin receives its package source, package ID, version, cache path, internal Skill count, and `kind: "plugin"`. Missing descriptions use `未提供说明`.

- [ ] **Step 4: Run the inventory tests and verify GREEN**

Run:

```powershell
node --test tests/skill-cleanup-selector.test.mjs
```

Expected: 2 tests pass, 0 fail.

- [ ] **Step 5: Commit the inventory contract**

```powershell
git add -- tests/skill-cleanup-selector.test.mjs skill-cleanup-selector.html
git commit -m "feat: add skill inventory audit contract"
```

### Task 2: Implement safe batch selection with TDD

**Files:**
- Modify: `tests/skill-cleanup-selector.test.mjs`
- Modify: `skill-cleanup-selector.html`

- [ ] **Step 1: Write failing tests for the pure core**

Add a helper that evaluates `<script id="core-script">` in a clean VM context and tests the exact selection rules:

```js
import vm from "node:vm";

function loadCore() {
  const source = extractScript(loadHtml(), "core-script");
  const context = { globalThis: {} };
  vm.createContext(context);
  vm.runInContext(source, context);
  return context.globalThis.SkillSelectorCore;
}

test("recommended batch selection excludes protected and ambiguous items", () => {
  const core = loadCore();
  const items = [
    { id: "safe", recommendation: "disable", selectable: true, duplicateState: "none" },
    { id: "core", recommendation: "required", selectable: false, duplicateState: "none" },
    { id: "system", recommendation: "system", selectable: false, duplicateState: "none" },
    { id: "ambiguous", recommendation: "compare", selectable: true, duplicateState: "different" },
  ];
  assert.deepEqual([...core.recommendedIds(items)], ["safe"]);
});

test("filters combine search, recommendation, environment, kind and selected state", () => {
  const core = loadCore();
  const item = {
    id: "a",
    name: "ra-video-download",
    description: "下载视频",
    path: "C:\\Users\\prepare\\.agents\\skills\\ra-video-download\\SKILL.md",
    recommendation: "optional",
    source: "agents-user",
    kind: "skill",
    desktopVisible: true,
    cliOfficialRoot: true,
    consoleDependency: false,
  };
  assert.equal(core.matchesFilters(item, {
    search: "download",
    recommendations: new Set(["optional"]),
    environments: new Set(["cli"]),
    kinds: new Set(["skill"]),
    selectedOnly: false,
    selectedIds: new Set(),
  }), true);
});
```

- [ ] **Step 2: Run the tests and verify RED**

Run:

```powershell
node --test tests/skill-cleanup-selector.test.mjs
```

Expected: FAIL because `#core-script` or `SkillSelectorCore` is missing.

- [ ] **Step 3: Implement the pure selection, filter and export core**

Add a DOM-free script block that assigns this API to `globalThis.SkillSelectorCore`:

```js
(() => {
  const normalize = (value) => String(value ?? "").toLocaleLowerCase("zh-CN");
  const recommendedIds = (items) => new Set(items
    .filter((item) => item.recommendation === "disable"
      && item.selectable === true
      && item.duplicateState !== "different")
    .map((item) => item.id));
  const matchesFilters = (item, filters) => {
    const haystack = normalize([item.name, item.description, item.path, item.category].join(" "));
    if (filters.search && !haystack.includes(normalize(filters.search))) return false;
    if (filters.recommendations.size && !filters.recommendations.has(item.recommendation)) return false;
    if (filters.kinds.size && !filters.kinds.has(item.kind)) return false;
    if (filters.environments.has("cli") && !item.cliOfficialRoot) return false;
    if (filters.environments.has("desktop") && !item.desktopVisible) return false;
    if (filters.environments.has("console") && !item.consoleDependency) return false;
    if (filters.selectedOnly && !filters.selectedIds.has(item.id)) return false;
    return true;
  };
  const buildExport = (inventory, selectedIds) => ({
    schemaVersion: "1.0",
    auditGeneratedAt: inventory.generatedAt,
    exportedAt: new Date().toISOString(),
    selected: inventory.items.filter((item) => selectedIds.has(item.id)),
  });
  globalThis.SkillSelectorCore = { recommendedIds, matchesFilters, buildExport };
})();
```

- [ ] **Step 4: Run all Node tests and verify GREEN**

Run:

```powershell
node --test tests/skill-cleanup-selector.test.mjs
```

Expected: 4 tests pass, 0 fail.

- [ ] **Step 5: Commit the safety core**

```powershell
git add -- tests/skill-cleanup-selector.test.mjs skill-cleanup-selector.html
git commit -m "feat: add safe skill selection rules"
```

### Task 3: Build the interactive audit interface

**Files:**
- Modify: `tests/skill-cleanup-selector.test.mjs`
- Modify: `skill-cleanup-selector.html`

- [ ] **Step 1: Write failing markup and export-contract tests**

Add tests that require the interface controls and exact export payload behavior:

```js
test("page exposes the complete audit workflow controls", () => {
  const html = loadHtml();
  for (const id of [
    "search-input",
    "select-recommended",
    "select-identical-duplicates",
    "clear-selection",
    "selected-only",
    "export-json",
    "copy-selection",
    "inventory-list",
  ]) assert.match(html, new RegExp(`id=["']${id}["']`));
});

test("export contains only selected exact instances", () => {
  const inventory = JSON.parse(extractScript(loadHtml(), "inventory-data"));
  const core = loadCore();
  const chosen = new Set([inventory.items[0].id, inventory.items[1].id]);
  const payload = core.buildExport(inventory, chosen);
  assert.equal(payload.selected.length, 2);
  assert.ok(payload.selected.every((item) => item.path || item.packageId));
});
```

- [ ] **Step 2: Run the tests and verify RED**

Run:

```powershell
node --test tests/skill-cleanup-selector.test.mjs
```

Expected: FAIL because the workflow controls are absent.

- [ ] **Step 3: Implement markup, styling and DOM behavior**

Implement the approved production-audit visual system and wire these behaviors:

- Render all items with an accessible checkbox, purpose, exact source, recommendation, and environment badges.
- Disable checkboxes when `selectable` is false.
- Use `recommendedIds` for the batch action.
- Select only byte-identical duplicates for the duplicate batch action.
- Persist selected IDs and filter state under `skill-cleanup-selector:v1`.
- Download `skill-removal-selection.json` with a Blob and object URL.
- Copy a plain-text review list with Clipboard API, with a textarea fallback.
- Announce selection and export results through an `aria-live` status element.
- Preserve keyboard focus, mobile single-column layout, and reduced-motion behavior.

The DOM layer must call the pure core rather than duplicating selection logic.

- [ ] **Step 4: Run all Node tests and verify GREEN**

Run:

```powershell
node --test tests/skill-cleanup-selector.test.mjs
```

Expected: 6 tests pass, 0 fail.

- [ ] **Step 5: Commit the interface**

```powershell
git add -- tests/skill-cleanup-selector.test.mjs skill-cleanup-selector.html
git commit -m "feat: build interactive skill cleanup selector"
```

### Task 4: Browser verification and handoff

**Files:**
- Modify if required: `skill-cleanup-selector.html`
- Modify if required: `tests/skill-cleanup-selector.test.mjs`

- [ ] **Step 1: Run the complete automated suite**

```powershell
node --test tests/skill-cleanup-selector.test.mjs
```

Expected: all tests pass with 0 failures and no warnings.

- [ ] **Step 2: Open the local HTML in a browser and exercise the workflow**

Verify:

1. Initial selected count is zero.
2. “一键勾选推荐停用” changes the count and never selects the three core Skill rows.
3. Search for `finance` narrows the list to finance-related rows.
4. CLI filter shows `.agents\skills` items and preserves selected state.
5. “仅看已选” displays exactly the selected set.
6. Reload restores state from `localStorage`.
7. JSON export contains the selected exact paths and plugin package IDs.
8. Mobile width and keyboard focus remain usable.

- [ ] **Step 3: Capture and inspect a screenshot**

Confirm that recommendation states are distinguishable without relying on color alone, long Windows paths wrap without horizontal page overflow, and the fixed audit bar does not cover list content.

- [ ] **Step 4: Re-run tests after visual corrections**

```powershell
node --test tests/skill-cleanup-selector.test.mjs
```

Expected: all tests pass, 0 fail.

- [ ] **Step 5: Commit verified corrections**

```powershell
git add -- tests/skill-cleanup-selector.test.mjs skill-cleanup-selector.html
git commit -m "test: verify skill cleanup selector workflow"
```

- [ ] **Step 6: Deliver without changing installed Skills**

Report the HTML path, test evidence, selection handoff instructions, and the CLI/Desktop path distinction. Do not edit `config.toml`, move Skill directories, delete files, or uninstall plugins.
