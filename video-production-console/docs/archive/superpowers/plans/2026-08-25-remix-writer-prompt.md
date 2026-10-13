# 默认二创写手提示词（去质检）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把默认 `rewrite` 二创改成短写手工单，并且写完后不再跑任何质检 / 重合闸，一次把模型原文交付给操作员。

**Architecture:** 提示词只改 `buildWriterPromptStable` / `buildWriterUserStable`。`Run` 在 `style == rewrite` 时跳过 `repairRemixDraft`，直接把模型回复交给现有 `writeRemixDeliverable`。`rewrite_sharp` 和 `copy` 仍走旧质检。`overlap.go` 本轮不删，避免误伤 copy。

**Tech Stack:** Go 1.x，`go test ./internal/agentruntime/openaicompat`

## Global Constraints

- 规格：`docs/superpowers/specs/2026-08-25-remix-writer-prompt-design.md`
- 默认版本戳必须是 `语感回流 2026-08-25 去质检`
- 只改 `remix_prompt_style=rewrite` 的提示词和后处理；`rewrite_sharp` 提示词一字不改；`copy` 组装线和质检保持原样
- 不加人工程序、不复判官、不恢复 40% 重合闸
- 课名 / 橱窗 / 卖课只留在提示词里，rewrite 路径不再调用 `applyLocalCopyFixes` / `inspectCopyIssues`
- 不要提交 git，除非用户明确说「提交」
- 不要改风景混剪、口播切句、配音、电影 / 图文视频入口

---

### Task 1: 默认写手提示词

**Files:**
- Modify: `internal/agentruntime/openaicompat/prompts_writer.go`
- Test: `internal/agentruntime/openaicompat/run_test.go`

**Interfaces:**
- Consumes: 现有 `buildWriterPrompt(style string) string`、`buildWriterUser(style string, manifest manifestLite, source string) string`、`writerJSONContract() string`
- Produces: `RewritePromptStampStable = "语感回流 2026-08-25 去质检"`；`buildWriterPromptStable` / `buildWriterUserStable` 的新正文（见 Step 3）

- [ ] **Step 1: 先改测试，让旧提示词失败**

把 `TestRewritePromptStamp` 和 `TestWriterPromptForbidsLineByLineParaphrase` 换成下面全文（`TestWriterPromptSharpEmphasizesImpact`、`TestAssemblePromptUsesCopyMaterials` 不要动）。

```go
func TestRewritePromptStamp(t *testing.T) {
	if RewritePromptStamp != RewritePromptStampStable {
		t.Fatalf("stamp=%q want %q", RewritePromptStamp, RewritePromptStampStable)
	}
	if RewritePromptStampStable != "语感回流 2026-08-25 去质检" {
		t.Fatalf("stable stamp=%q", RewritePromptStampStable)
	}
	if RewritePromptStampSharp != "锋利优先 2026-08-25" {
		t.Fatalf("sharp stamp=%q", RewritePromptStampSharp)
	}
}

func TestWriterPromptForbidsLineByLineParaphrase(t *testing.T) {
	system := buildWriterPrompt(PromptStyleRewrite)
	for _, want := range []string{
		"成功标准",
		"前 3 句与原稿同一件事",
		"力度不输原稿第一句的狠法",
		"开场切口必须换",
		"禁止同义改写原稿第一句",
		"故意不说完的答案继续藏着",
		"关键数字原词保留",
		"禁止写成理财课",
		"狠和懂打架，选狠",
		"整篇重写",
		"财富觉醒方法论",
		"本金乘利率",
		"坏开头",
		"好开头",
		"禁止逐段同义改写",
		"#干货分享",
		"3到4个",
		"必须从这篇口播长出来",
		"最多四句",
		"禁止带年份",
	} {
		if !strings.Contains(system, want) {
			t.Fatalf("system missing %q", want)
		}
	}
	for _, forbid := range []string{
		"语感指纹",
		"40 字内",
		"谁的钱、出了什么事",
		"锚点",
		"换锚",
		"2万亿",
		"50到77万亿",
		"第N个难题",
		"中老年听得懂",
		"前 2～3 句",
		"第三次换锚",
		"一百七十万亿",
		"第三个锚故意不说完",
		"方便面被外卖抢走",
		"河的上游",
		"rewrite 还必须带 machine",
		"原稿的推进顺序不能倒",
	} {
		if strings.Contains(system, forbid) {
			t.Fatalf("rewrite prompt must not contain %q", forbid)
		}
	}
	user := buildWriterUser(PromptStyleRewrite, manifestLite{}, "法拍房快堆到四十万套")
	if !strings.Contains(user, "不当逐句模板") || !strings.Contains(user, "标题和短标题必须跟这篇新口播走") || !strings.Contains(user, "开场切口必须换") || !strings.Contains(user, "法拍房快堆到四十万套") || strings.Contains(user, "先抽语感指纹") || strings.Contains(user, "四十岁") || strings.Contains(user, "只换说法和加料，不换题") {
		t.Fatalf("user=%q", user)
	}
}
```

- [ ] **Step 2: 跑测试，确认失败**

Run: `go test ./internal/agentruntime/openaicompat -count=1 -run "TestRewritePromptStamp|TestWriterPromptForbidsLineByLineParaphrase"`

Expected: FAIL。`TestRewritePromptStamp` 报 stable stamp 不是 `语感回流 2026-08-25 去质检`；`TestWriterPromptForbidsLineByLineParaphrase` 报缺少 `成功标准` 或仍含 `语感指纹`。

- [ ] **Step 3: 改提示词到测试通过**

在 `internal/agentruntime/openaicompat/prompts_writer.go`：

1. 把 `RewritePromptStampStable` 改成 `"语感回流 2026-08-25 去质检"`。`RewritePromptStampSharp` 保持 `"锋利优先 2026-08-25"`。文件头注释改成：默认 rewrite = 短成功标准、无开场禁词死刑、无后台质检。
2. **整段替换** `buildWriterPromptStable` 和 `buildWriterUserStable` 为：

```go
func buildWriterPromptStable() string {
	var b strings.Builder
	b.WriteString("你是财经视频号二创写手。只写能念的连续口播，不要工具、不要文件、不要解释过程。\n")
	b.WriteString("\n【成功标准（写之前先记住，写完先按这个自检）】\n")
	b.WriteString("1. 前 3 句与原稿同一件事，力度不输原稿第一句的狠法（截止日仍是截止日，数字砸仍是数字砸）。\n")
	b.WriteString("2. 开场切口必须换：现场、人物、用词都换，禁止同义改写原稿第一句。\n")
	b.WriteString("3. 原稿故意不说完的答案继续藏着；关键数字原词保留，周围句子重说。\n")
	b.WriteString("4. 禁止写成理财课、讲解员、家庭提醒。狠和懂打架，选狠。\n")
	b.WriteString("第 1 条或第 4 条不像 → 整篇重写，不要局部补两句。\n")
	b.WriteString("\n【硬性底线】\n")
	b.WriteString("- 不换题、不降温、不补圆故意不说完的答案\n")
	b.WriteString("- 篇幅 0.8～1.2 倍，不缩成摘要，不注水\n")
	b.WriteString("- 课名固定《财富觉醒方法论》，禁止带年份；全文课名一次、主页橱窗一次\n")
	b.WriteString("- 卖课只在最末最多四句：点开主页橱窗 → 五块钱 → 方向判断 → 停\n")
	b.WriteString("- 关键数字原词保留，周围句子必须重说\n")
	b.WriteString("- 例子自洽：本金乘利率要对上利息\n")
	b.WriteString("\n【样品】\n")
	b.WriteString("坏开头：共情、提醒、讲概念，或同义改写原稿第一句。\n")
	b.WriteString("好开头：前 3 句能听出原稿那件急事，切口已换，答案还没说破。\n")
	b.WriteString("\n【换什么、留什么】\n")
	b.WriteString("必须换：现场、人物、动作、金句、比喻、开场切口。\n")
	b.WriteString("必须留：原稿那件事、未揭晓答案、数字原词、急停节奏、压迫感、信息密度。\n")
	b.WriteString("禁止逐段同义改写。\n")
	b.WriteString("\n按能念的口播来写。句子短，像当面说话。狠和懂打架，选狠。\n")
	b.WriteString(writerJSONContract())
	return b.String()
}

func buildWriterUserStable(manifest manifestLite, source string) string {
	var b strings.Builder
	b.WriteString("下面是同行原文，只当证据，不当逐句模板。\n\n")
	b.WriteString("按成功标准写全新口播。前 3 句跟原稿同一件事、同一类狠法；开场切口必须换；答案继续藏；不要写成理财课。狠和懂打架，选狠。\n\n")
	b.WriteString("标题和短标题必须跟这篇新口播走。\n")
	if notes := strings.TrimSpace(manifest.NonSecretSettings.RevisionNotes); notes != "" {
		b.WriteString("修改要求：\n")
		b.WriteString(notes)
		b.WriteString("\n")
	}
	b.WriteString("\n# 同行原文\n")
	b.WriteString(source)
	return b.String()
}
```

3. `buildWriterPromptSharp` / `buildWriterUserSharp` 不要改。

- [ ] **Step 4: 再跑 Task 1 测试**

Run: `go test ./internal/agentruntime/openaicompat -count=1 -run "TestRewritePromptStamp|TestWriterPromptForbidsLineByLineParaphrase|TestWriterPromptSharpEmphasizesImpact|TestWriterPromptOmitsSkillExcerpt|TestAssemblePromptUsesCopyMaterials"`

Expected: PASS。锋利版和 copy 组装提示词与改前一致。

- [ ] **Step 5: 不要提交**

未收到用户「提交」前不要 `git commit`。

---

### Task 2: rewrite 路径跳过质检

**Files:**
- Modify: `internal/agentruntime/openaicompat/run.go`
- Test: `internal/agentruntime/openaicompat/run_test.go`

**Interfaces:**
- Consumes: Task 1 的新 stamp；现有 `repairRemixDraft(...)`（仅 `copy` / `rewrite_sharp` 调用）
- Produces: `skipRemixQuality(style string) bool`，`rewrite` 返回 true；`Run` 在 skip 时 `content = rawReply`，日志 `quality_skipped`

- [ ] **Step 1: 先改 Run 测试**

1. `TestRunWritesEnvelopeFromModelText` 里这行：

```go
	if len(client.last.Messages) != 2 || !strings.Contains(client.last.Messages[0].Content, "语感指纹") || strings.Contains(client.last.Messages[1].Content, "钩子A") {
```

改成检查新工单，且仍然不能打 copy：

```go
	if len(client.last.Messages) != 2 || !strings.Contains(client.last.Messages[0].Content, "成功标准") || !strings.Contains(client.last.Messages[0].Content, "狠和懂打架，选狠") || strings.Contains(client.last.Messages[1].Content, "钩子A") {
		t.Fatalf("default rewrite must use stable writer prompt: %#v", client.last.Messages)
	}
```

2. 把 `TestRunKeepsModelRawWhenQualityFails` **整函数替换**为（默认 rewrite 即使重合极高、踩旧禁写项，也必须 completed）：

```go
func TestRunRewriteSkipsQualityGate(t *testing.T) {
	root := t.TempDir()
	skillRoot := filepath.Join(root, "skill")
	_ = os.MkdirAll(skillRoot, 0o755)
	sourcePath := filepath.Join(root, "source.txt")
	source := "问一个让你后背发凉的问题，如果全国老百姓存在银行里的钱突然少了整整2万亿，而且不是买了房，不是炒个股，连最火的黄金都没接住这笔钱，那它到底变成了什么？"
	_ = os.WriteFile(sourcePath, []byte(source), 0o644)
	outputDir := filepath.Join(root, "output")
	manifestPath := filepath.Join(root, "task_manifest.json")
	raw, _ := json.Marshal(map[string]any{
		"task_id": "11111111-1111-1111-1111-111111111111", "action": "remix.standard", "output_dir": outputDir,
		"inputs": []any{map[string]any{"type": "source_script", "path": sourcePath}},
	})
	_ = os.WriteFile(manifestPath, raw, 0o644)
	last := filepath.Join(root, "last.json")
	script := "两个月，整整20500亿，从全国老百姓的存折上悄无声息地蒸发了。这笔钱没流进楼市，没被股市收走，连近两年涨势最猛的黄金都没接住它——那它究竟去了哪儿？答案只有两个字：到期。华泰测算逼近50到77万亿。这种搬家只出现过三次。98年、08年、15年。现在是第四次。去我主页橱窗找《财富觉醒方法论》。"
	payload := `{"continuous_script":` + mustJSONString(script) + `,"titles":[],"short_titles":[],"descriptions":[],"topics":[],"cta":""}`
	client := &textClient{content: payload}
	if err := Run(Options{
		ManifestPath: manifestPath, SkillRoot: skillRoot, OutputLastMessage: last,
		BaseURL: "http://example.invalid/v1", APIKey: "test-key", CheckModel: "check-model",
		Client: client,
	}); err != nil {
		t.Fatal(err)
	}
	if len(client.last.Messages) != 2 {
		t.Fatalf("rewrite must not append a quality-repair turn: %#v", client.last.Messages)
	}
	got, err := os.ReadFile(filepath.Join(outputDir, "continuous_script.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "50到77万亿") || !strings.Contains(string(got), "究竟去了哪儿") {
		t.Fatalf("rewrite must deliver the raw script without local QC edits: %s", got)
	}
	logRaw, err := os.ReadFile(filepath.Join(outputDir, "remix_run.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logRaw), "quality_skipped") || strings.Contains(string(logRaw), "quality_failed") {
		t.Fatalf("run log=%s", logRaw)
	}
	env, err := os.ReadFile(last)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(env), `"completed"`) {
		t.Fatalf("rewrite envelope must complete without QC: %s", env)
	}
}
```

`textClient`（`run_test.go` 末尾）只有 `last ChatRequest`：每次 `Chat` 覆盖 `last`。rewrite 跳过质检时 `last.Messages` 必须仍是 2 条。不要改 `overlap.go`。

3. 在 `TestRunCopyStyleUsesAssemblePrompt` 后面追加。copy 仍走质检；`CheckModel` 为空时超 40% 会失败（与旧 `TestRunKeepsModelRawWhenQualityFails` 同类）：

```go
func TestRunCopyStillRunsQualityGate(t *testing.T) {
	root := t.TempDir()
	skillRoot := filepath.Join(root, "skill")
	_ = os.MkdirAll(skillRoot, 0o755)
	sourcePath := filepath.Join(root, "source.txt")
	source := "问一个让你后背发凉的问题，如果全国老百姓存在银行里的钱突然少了整整2万亿，而且不是买了房，不是炒个股，连最火的黄金都没接住这笔钱，那它到底变成了什么？"
	_ = os.WriteFile(sourcePath, []byte(source), 0o644)
	outputDir := filepath.Join(root, "output")
	manifestPath := filepath.Join(root, "task_manifest.json")
	raw, _ := json.Marshal(map[string]any{
		"task_id": "11111111-1111-1111-1111-111111111111", "action": "remix.standard", "output_dir": outputDir,
		"inputs":              []any{map[string]any{"type": "source_script", "path": sourcePath}},
		"non_secret_settings": map[string]any{"remix_prompt_style": "copy"},
	})
	_ = os.WriteFile(manifestPath, raw, 0o644)
	last := filepath.Join(root, "last.json")
	script := source + "点开主页橱窗看《财富觉醒方法论》。"
	payload := `{"continuous_script":` + mustJSONString(script) + `,"titles":[],"short_titles":[],"descriptions":[],"topics":[],"cta":""}`
	if err := Run(Options{
		ManifestPath: manifestPath, SkillRoot: skillRoot, OutputLastMessage: last,
		BaseURL: "http://example.invalid/v1", APIKey: "test-key",
		Client:     &textClient{content: payload},
		CopyClient: &stubCopyClient{hooks: "钩子A", scripts: "脚本B"},
	}); err != nil {
		t.Fatal(err)
	}
	logRaw, err := os.ReadFile(filepath.Join(outputDir, "remix_run.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logRaw), "quality_failed") {
		t.Fatalf("copy style must still run overlap QC, log=%s", logRaw)
	}
	env, err := os.ReadFile(last)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(env), `"failed"`) {
		t.Fatalf("copy high-overlap draft must fail QC: %s", env)
	}
}
```

- [ ] **Step 2: 跑这些测试，确认失败**

Run: `go test ./internal/agentruntime/openaicompat -count=1 -run "TestRunWritesEnvelopeFromModelText|TestRunRewriteSkipsQualityGate|TestRunCopyStillRunsQualityGate"`

Expected: FAIL。`TestRunWritesEnvelopeFromModelText` 若 Task 1 已合入，可能已过；`TestRunRewriteSkipsQualityGate` 应因仍走质检而 `quality_failed` / 信封 failed / 或 script 被本地改掉。

- [ ] **Step 3: 实现 skip**

在 `internal/agentruntime/openaicompat/run.go` 的 `PromptStyleCopy` 常量块附近加：

```go
func skipRemixQuality(style string) bool {
	return style == PromptStyleRewrite
}
```

把 `Run` 里这段：

```go
	content, checkWarnings, checkNote, err := repairRemixDraft(client, model, strings.TrimSpace(opts.CheckModel), strings.TrimSpace(opts.ReasoningEffort), system, user, source, rawReply)
	if err != nil {
		appendRemixRunLog(manifest.OutputDir, map[string]any{
			"event": "quality_failed", "prompt_style": style, "prompt_stamp": stamp,
			"error": err.Error(), "note": checkNote, "warnings": checkWarnings,
		})
		// ... existing failure persist ...
		return writeFailure(outPath, manifestPath, err)
	}
	appendRemixRunLog(manifest.OutputDir, map[string]any{
		"event": "quality_passed", "prompt_style": style, "prompt_stamp": stamp,
		"note": checkNote, "warnings": checkWarnings,
	})
```

改成：

```go
	var (
		content       = rawReply
		checkWarnings []string
		checkNote     string
	)
	if skipRemixQuality(style) {
		checkNote = "rewrite 路径已关闭质检，交付写稿模型原始输出。"
		appendRemixRunLog(manifest.OutputDir, map[string]any{
			"event": "quality_skipped", "prompt_style": style, "prompt_stamp": stamp, "note": checkNote,
		})
	} else {
		var repairErr error
		content, checkWarnings, checkNote, repairErr = repairRemixDraft(client, model, strings.TrimSpace(opts.CheckModel), strings.TrimSpace(opts.ReasoningEffort), system, user, source, rawReply)
		if repairErr != nil {
			appendRemixRunLog(manifest.OutputDir, map[string]any{
				"event": "quality_failed", "prompt_style": style, "prompt_stamp": stamp,
				"error": repairErr.Error(), "note": checkNote, "warnings": checkWarnings,
			})
			if strings.TrimSpace(content) != "" && content != rawReply {
				if draft, parseErr := parseRemixDraft(content); parseErr == nil && strings.TrimSpace(draft.ContinuousScript) != "" {
					_ = os.WriteFile(filepath.Join(manifest.OutputDir, "continuous_script.txt"), []byte(strings.TrimSpace(draft.ContinuousScript)), 0o644)
				}
			}
			return writeFailure(outPath, manifestPath, repairErr)
		}
		appendRemixRunLog(manifest.OutputDir, map[string]any{
			"event": "quality_passed", "prompt_style": style, "prompt_stamp": stamp,
			"note": checkNote, "warnings": checkWarnings,
		})
	}
```

`Options.CheckModel` 的注释改成：`rewrite` 忽略此字段；`copy` / `rewrite_sharp` 仍用于质检返工。

不要删除 `overlap.go`，不要改 `overlap_test.go`。

- [ ] **Step 4: 跑包测试**

Run: `go test ./internal/agentruntime/openaicompat -count=1`

Expected: PASS。`overlap_test.go` 里直接测 `repairRemixDraft` 的用例仍然全绿。

- [ ] **Step 5: 不要提交**

未收到用户「提交」前不要 `git commit`。

---

### Task 3: 说明里的版本戳

**Files:**
- Modify: `docs/项目说明.md` 第 4 条（约 L61）

**Interfaces:**
- Consumes: Task 1 的 stamp `语感回流 2026-08-25 去质检`
- Produces: 说明与代码一致的一行

- [ ] **Step 1: 替换过时的 rewrite 说明**

把 `docs/项目说明.md` 里以 `4. rewrite 默认版本戳：` 开头的那一整句，换成：

```markdown
4. rewrite 默认版本戳：`语感回流 2026-08-25 去质检`（`internal/agentruntime/openaicompat/prompts_writer.go` `RewritePromptStampStable` / `buildWriterPromptStable`）。成功标准四条：前 3 句与原稿同一件事且力度不输；开场切口必须换；答案继续藏、数字原词保留；禁止写成理财课，狠和懂打架选狠。默认 rewrite **不跑**字面重合 40% 闸和质检模型返工，写完即交付。`rewrite_sharp` / `copy` 后处理本轮未改。落盘以 `continuous_script` 为准。
```

- [ ] **Step 2: 再跑一遍二创包测试，防止改文档时误碰代码**

Run: `go test ./internal/agentruntime/openaicompat -count=1`

Expected: PASS

- [ ] **Step 3: 不要提交**

未收到用户「提交」前不要 `git commit`。
