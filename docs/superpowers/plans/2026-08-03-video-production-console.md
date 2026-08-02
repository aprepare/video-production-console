# Local Video Production Console Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 构建一个仅在本机运行的统一视频生产看板，用账号固定背景图和项目资产隔离四个及后续新增账号，并通过 Codex CLI 实时执行选题、二创和混剪 Skill。

**Architecture:** 使用独立 Go 服务管理 SQLite、受控文件目录、Codex 子进程、爆款库 HTTP/SSE 和 WebSocket 事件；使用 React/TypeScript 实现统一看板、资产上传、任务对话和设置页。Codex CLI 使用专用配置加载现有 Skill 与爆款库 MCP，网页不控制桌面版会话，也不直接读写爆款库数据库。

**Tech Stack:** Go 1.24、`modernc.org/sqlite`、`github.com/go-chi/chi/v5`、`github.com/coder/websocket`、React 19、TypeScript、Vite、TanStack Query、dnd-kit、Vitest、Playwright。

---

## 文件结构

在独立 worktree 根目录创建：

```text
video-production-console/
├─ cmd/console/main.go
├─ internal/
│  ├─ app/app.go
│  ├─ config/config.go
│  ├─ domain/models.go
│  ├─ store/db.go
│  ├─ store/migrations.go
│  ├─ store/accounts.go
│  ├─ store/projects.go
│  ├─ store/tasks.go
│  ├─ assets/service.go
│  ├─ codex/command.go
│  ├─ codex/events.go
│  ├─ codex/prompt.go
│  ├─ codex/runner.go
│  ├─ codex/scheduler.go
│  ├─ baokuan/client.go
│  ├─ obsidian/service.go
│  ├─ realtime/hub.go
│  └─ httpapi/
│     ├─ router.go
│     ├─ accounts.go
│     ├─ projects.go
│     ├─ tasks.go
│     ├─ settings.go
│     └─ dependencies.go
├─ schemas/codex-result.schema.json
├─ web/
│  ├─ src/api/client.ts
│  ├─ src/api/types.ts
│  ├─ src/app/App.tsx
│  ├─ src/components/AccountSidebar.tsx
│  ├─ src/components/ProjectBoard.tsx
│  ├─ src/components/ProjectDrawer.tsx
│  ├─ src/components/TaskConversation.tsx
│  ├─ src/components/AssetPanel.tsx
│  ├─ src/components/SettingsDialog.tsx
│  └─ src/test/
├─ tests/fakes/fake-codex.ps1
├─ tests/e2e/console.spec.ts
├─ go.mod
├─ Makefile
└─ README.md
```

除 Task 1 创建 worktree 的命令外，后续所有 Go、npm 和 git 命令默认都在 `C:\Users\prepare\Documents\video-production-console-worktree\video-production-console` 执行。

## Task 1: 创建独立 worktree 和可启动骨架

**Files:**
- Create: `video-production-console/go.mod`
- Create: `video-production-console/cmd/console/main.go`
- Create: `video-production-console/internal/config/config.go`
- Create: `video-production-console/internal/app/app.go`
- Create: `video-production-console/internal/app/app_test.go`
- Create: `video-production-console/web/*`

- [ ] **Step 1: 创建功能分支 worktree**

Run:

```powershell
git worktree add 'C:\Users\prepare\Documents\video-production-console-worktree' -b codex/video-production-console
New-Item -ItemType Directory -Path 'C:\Users\prepare\Documents\video-production-console-worktree\video-production-console'
```

Expected: 新 worktree 位于指定目录，当前分支为 `codex/video-production-console`。

- [ ] **Step 2: 初始化 Go 与 React 工程**

Run:

```powershell
Set-Location 'C:\Users\prepare\Documents\video-production-console-worktree\video-production-console'
go mod init video-production-console
npm create vite@latest web -- --template react-ts
Set-Location web
npm install
npm install @tanstack/react-query @dnd-kit/core @dnd-kit/sortable
npm install -D vitest @testing-library/react @testing-library/jest-dom playwright
```

Expected: `go.mod` 与 `web/package.json` 存在，`npm run build` 成功。

- [ ] **Step 3: 先写健康检查失败测试**

Create `internal/app/app_test.go`:

```go
package app

import (
    "net/http"
    "net/http/httptest"
    "testing"
)

func TestHealth(t *testing.T) {
    srv := New(Options{})
    req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
    rec := httptest.NewRecorder()
    srv.Handler().ServeHTTP(rec, req)
    if rec.Code != http.StatusOK || rec.Body.String() != `{"status":"ok"}`+"\n" {
        t.Fatalf("status=%d body=%q", rec.Code, rec.Body.String())
    }
}
```

- [ ] **Step 4: 运行测试确认失败**

Run: `go test ./internal/app -run TestHealth -v`

Expected: FAIL，提示 `New` 或 `Options` 未定义。

- [ ] **Step 5: 实现最小服务和只绑定本机的配置**

Create `internal/config/config.go`:

```go
package config

type Config struct {
    ListenAddr      string
    DataRoot        string
    DatabasePath    string
    BaokuanBaseURL  string
    ObsidianVault   string
    CodexBinaryPath string
}

func Default() Config {
    return Config{
        ListenAddr: "127.0.0.1:2030",
        DataRoot: "./video-console-data",
        DatabasePath: "./video-console-data/console.db",
        BaokuanBaseURL: "http://127.0.0.1:2022",
        CodexBinaryPath: "codex",
    }
}
```

Create `internal/app/app.go` with `New(Options)`, `Handler()` and a `/api/health` JSON handler. Create `cmd/console/main.go` to load `config.Default()` and call `http.ListenAndServe(cfg.ListenAddr, app.New(...).Handler())`.

- [ ] **Step 6: 验证并提交**

Run:

```powershell
go test ./internal/app -v
Set-Location web
npm run build
```

Expected: Go 测试与 Vite 构建均 PASS。

Commit:

```powershell
git add video-production-console
git commit -m 'feat: scaffold local video production console'
```

## Task 2: 建立领域类型和 SQLite 迁移

**Files:**
- Create: `video-production-console/internal/domain/models.go`
- Create: `video-production-console/internal/store/db.go`
- Create: `video-production-console/internal/store/migrations.go`
- Create: `video-production-console/internal/store/migrations_test.go`

- [ ] **Step 1: 写迁移测试**

Create `internal/store/migrations_test.go`，使用 `t.TempDir()` 创建数据库，调用 `Open()` 后查询 `sqlite_master`，断言以下表全部存在：

```go
want := []string{"settings", "accounts", "projects", "assets", "codex_tasks", "task_events", "task_messages"}
```

同时断言初始设置 `max_codex_concurrency` 的值为 `2`。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/store -run TestMigrations -v`

Expected: FAIL，提示 `Open` 未定义。

- [ ] **Step 3: 定义稳定领域枚举**

Create `internal/domain/models.go`:

```go
package domain

type ProjectStage string
const (
    StageTopic ProjectStage = "topic"
    StageScript ProjectStage = "script"
    StageAssets ProjectStage = "assets"
    StageMixing ProjectStage = "mixing"
    StageReview ProjectStage = "review"
    StageReady ProjectStage = "ready"
    StagePublished ProjectStage = "published"
    StageArchived ProjectStage = "archived"
)

type AssetType string
const (
    AssetContinuousScript AssetType = "continuous_script"
    AssetSpokenScript AssetType = "spoken_script"
    AssetAudio AssetType = "audio"
    AssetSubtitle AssetType = "subtitle"
    AssetAccountBackground AssetType = "account_background"
    AssetMixDraft AssetType = "mix_draft"
    AssetFinalVideo AssetType = "final_video"
)

type TaskStatus string
const (
    TaskQueued TaskStatus = "queued"
    TaskRunning TaskStatus = "running"
    TaskWaitingInput TaskStatus = "waiting_input"
    TaskCompleted TaskStatus = "completed"
    TaskFailed TaskStatus = "failed"
    TaskCancelled TaskStatus = "cancelled"
)
```

在同一文件定义 `Account`、`Project`、`Asset`、`CodexTask`、`TaskEvent`、`TaskMessage`，字段名与设计文档完全一致。

- [ ] **Step 4: 实现数据库连接和迁移**

`store.Open(path)` 必须：创建父目录、用 `modernc.org/sqlite` 打开连接、设置 `PRAGMA foreign_keys=ON`、`PRAGMA busy_timeout=5000`、执行版本化迁移。

首个迁移必须创建七张表、外键和以下索引：

```sql
CREATE UNIQUE INDEX accounts_name_uq ON accounts(name) WHERE status='active';
CREATE INDEX projects_account_stage_idx ON projects(account_id, stage);
CREATE INDEX assets_project_type_idx ON assets(project_id, type, version DESC);
CREATE INDEX tasks_status_created_idx ON codex_tasks(status, created_at);
CREATE UNIQUE INDEX task_events_seq_uq ON task_events(task_id, sequence);
```

插入设置：

```sql
INSERT OR IGNORE INTO settings(key, value) VALUES ('max_codex_concurrency', '2');
```

- [ ] **Step 5: 验证并提交**

Run: `go test ./internal/store -v`

Expected: PASS，且测试数据库包含全部表和默认并发值。

Commit:

```powershell
git add video-production-console/internal/domain video-production-console/internal/store video-production-console/go.mod video-production-console/go.sum
git commit -m 'feat: add console database schema'
```

## Task 3: 实现账号和固定背景图

**Files:**
- Create: `video-production-console/internal/store/accounts.go`
- Create: `video-production-console/internal/assets/service.go`
- Create: `video-production-console/internal/assets/service_test.go`
- Create: `video-production-console/internal/httpapi/accounts.go`
- Create: `video-production-console/internal/httpapi/accounts_test.go`

- [ ] **Step 1: 写账号创建接口测试**

构造 multipart 请求：`name=账号A`、`background=@background.png`，发送到 `POST /api/accounts`。断言：

```go
if got.Name != "账号A" { t.Fatalf("name=%q", got.Name) }
if got.BackgroundAssetID == "" { t.Fatal("missing background asset") }
if !strings.HasPrefix(got.BackgroundPath, dataRoot) { t.Fatal("asset escaped data root") }
```

再测试缺少名称、缺少图片、重复名称、伪装成 PNG 的文本文件均返回 `400` 或 `409`。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/assets ./internal/httpapi -run Account -v`

Expected: FAIL，账号处理器尚不存在。

- [ ] **Step 3: 实现安全上传服务**

`assets.Service.SaveAccountBackground(accountID, filename, io.Reader)` 必须：

- 只允许 PNG、JPEG、WebP。
- 读取文件头做 MIME 探测，不相信扩展名。
- 限制 20 MiB。
- 计算 SHA-256。
- 写入 `<dataRoot>/accounts/<account-id>/background/<uuid>.<ext>`。
- 使用临时文件加 `os.Rename` 原子落盘。
- 返回路径、MIME、大小和校验值。

- [ ] **Step 4: 实现账号事务**

`POST /api/accounts` 顺序：校验名称、生成 UUID、保存图片、在一个数据库事务内写 `accounts` 和 `assets`、更新 `background_asset_id`。数据库失败时删除本次新文件；不得删除任何旧背景图。

提供：

```text
GET    /api/accounts
POST   /api/accounts
PATCH  /api/accounts/{id}
POST   /api/accounts/{id}/background
DELETE /api/accounts/{id}  -> 只改为 inactive
```

- [ ] **Step 5: 验证并提交**

Run: `go test ./internal/assets ./internal/httpapi -run Account -v`

Expected: 所有账号、图片类型和路径逃逸测试 PASS。

Commit:

```powershell
git add video-production-console/internal/assets video-production-console/internal/store/accounts.go video-production-console/internal/httpapi/accounts*
git commit -m 'feat: manage accounts with fixed backgrounds'
```

## Task 4: 实现项目、资产清单和阶段门槛

**Files:**
- Create: `video-production-console/internal/store/projects.go`
- Create: `video-production-console/internal/domain/stages.go`
- Create: `video-production-console/internal/domain/stages_test.go`
- Create: `video-production-console/internal/httpapi/projects.go`
- Create: `video-production-console/internal/httpapi/projects_test.go`

- [ ] **Step 1: 写阶段状态机测试**

覆盖以下明确规则：

```go
tests := []struct{
    from, to domain.ProjectStage
    assets []domain.AssetType
    allowed bool
}{
    {domain.StageTopic, domain.StageScript, nil, true},
    {domain.StageAssets, domain.StageMixing, []domain.AssetType{
        domain.AssetContinuousScript, domain.AssetAudio, domain.AssetSubtitle,
        domain.AssetAccountBackground,
    }, true},
    {domain.StageAssets, domain.StageMixing, []domain.AssetType{domain.AssetAudio}, false},
    {domain.StageReview, domain.StageReady, []domain.AssetType{domain.AssetFinalVideo}, true},
    {domain.StageReady, domain.StagePublished, []domain.AssetType{domain.AssetFinalVideo}, true},
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/domain -run Stage -v`

Expected: FAIL，`CanMove` 未定义。

- [ ] **Step 3: 实现状态机和项目仓库**

实现：

```go
func CanMove(from, to ProjectStage, available map[AssetType]bool) error
```

错误值必须包含缺失资产类型，API 将其转换为 `409 Conflict`。项目仓库提供 `CreateProject`、`ListProjects(accountID, stage, query)`、`GetProject`、`MoveProject`、`SetTopicCardPath`。

- [ ] **Step 4: 实现项目资产上传**

提供：

```text
POST /api/projects
GET  /api/projects?account_id=&stage=&q=
GET  /api/projects/{id}
POST /api/projects/{id}/assets/{type}
POST /api/projects/{id}/move
```

文案允许 UTF-8 `.txt`、`.md`；配音允许 MP3、WAV、M4A；字幕只允许 UTF-8 `.srt`；成片允许 MP4。每种类型保留版本记录，项目详情只返回当前版本并可展开历史版本。

- [ ] **Step 5: 验证并提交**

Run: `go test ./internal/domain ./internal/httpapi -run 'Project|Stage|Asset' -v`

Expected: 创建、筛选、上传和阶段阻止行为全部 PASS。

Commit:

```powershell
git add video-production-console/internal/domain video-production-console/internal/store/projects.go video-production-console/internal/httpapi/projects*
git commit -m 'feat: add project assets and stage gates'
```

## Task 5: 实现 Codex 任务协议和 PromptBuilder

**Files:**
- Create: `video-production-console/schemas/codex-result.schema.json`
- Create: `video-production-console/internal/codex/prompt.go`
- Create: `video-production-console/internal/codex/prompt_test.go`
- Create: `video-production-console/internal/codex/command.go`
- Create: `video-production-console/internal/codex/command_test.go`

- [ ] **Step 1: 写任务映射和隔离测试**

断言任务类型只能映射到以下 Skill：

```go
var skills = map[string]string{
    "topic_select": "finance-topic-selector",
    "topic_deepen": "finance-topic-selector",
    "remix": "finance-viral-remix",
    "spoken_format": "finance-viral-remix",
    "montage": "chatcut-finance-video",
}
```

测试 prompt 包含当前 `project_id`、账号名称、当前项目资产路径，并且不包含另一个项目路径和任何环境变量值。

- [ ] **Step 2: 创建最终响应 Schema**

`schemas/codex-result.schema.json` 必须要求：

```json
{
  "type": "object",
  "required": ["status", "summary", "artifacts"],
  "properties": {
    "status": {"enum": ["completed", "needs_input", "failed"]},
    "summary": {"type": "string"},
    "question": {
      "type": ["object", "null"],
      "properties": {
        "text": {"type": "string"},
        "options": {"type": "array", "items": {"type": "string"}}
      },
      "required": ["text", "options"]
    },
    "artifacts": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["type", "path", "description"],
        "properties": {
          "type": {"type": "string"},
          "path": {"type": "string"},
          "description": {"type": "string"}
        }
      }
    },
    "topic_card_path": {"type": ["string", "null"]},
    "next_recommended_action": {"type": ["string", "null"]}
  },
  "additionalProperties": false
}
```

- [ ] **Step 3: 实现 PromptBuilder**

`BuildPrompt(TaskContext)` 明确要求 Codex：使用指定 `$skill`；不得访问其他项目；需要人工决定时返回 `needs_input` 并结束本轮；所有产物必须写入允许目录；不得启动微信视频号测试。

- [ ] **Step 4: 实现无 Shell 拼接的命令构造器**

```go
func BuildExecCommand(cfg Config, ctx TaskContext) *exec.Cmd {
    args := []string{
        "exec", "--json", "--skip-git-repo-check",
        "--output-schema", cfg.ResultSchema,
        "-C", ctx.ProjectDir, "-",
    }
    cmd := exec.Command(cfg.CodexBinaryPath, args...)
    cmd.Stdin = strings.NewReader(BuildPrompt(ctx))
    cmd.Env = cfg.SafeEnvironment()
    return cmd
}
```

恢复命令使用参数数组：`exec resume <session-id> --json --output-schema <schema> -`。

- [ ] **Step 5: 验证并提交**

Run: `go test ./internal/codex -run 'Prompt|Command' -v`

Expected: Skill 映射、路径隔离、无 Shell 插值和恢复参数全部 PASS。

Commit:

```powershell
git add video-production-console/schemas video-production-console/internal/codex
git commit -m 'feat: define codex task protocol'
```

## Task 6: 解析 Codex JSONL 并持久化实时事件

**Files:**
- Create: `video-production-console/internal/codex/events.go`
- Create: `video-production-console/internal/codex/events_test.go`
- Create: `video-production-console/internal/store/tasks.go`
- Create: `video-production-console/internal/codex/runner.go`
- Create: `video-production-console/tests/fakes/fake-codex.ps1`

- [ ] **Step 1: 准备假 CLI 事件流**

`tests/fakes/fake-codex.ps1` 根据首个输入参数输出：

```jsonl
{"type":"thread.started","thread_id":"11111111-1111-1111-1111-111111111111"}
{"type":"item.completed","item":{"type":"agent_message","text":"正在搜索爆款库"}}
{"type":"turn.completed","result":{"status":"needs_input","summary":"需要选择方向","question":{"text":"选择哪个方向？","options":["养老金","存款"]},"artifacts":[]}}
```

脚本必须逐行 flush，并能用参数切换 `completed`、`failed` 和延迟输出。

- [ ] **Step 2: 写事件解析测试**

断言：`thread.started` 保存 session ID；agent message 转为可见事件；未知事件保存 `raw_json`；错误 JSON 产生 `parse_warning` 而不是终止整项任务。

- [ ] **Step 3: 实现稳定内部事件**

```go
type Event struct {
    Kind string
    Level string
    DisplayText string
    SessionID string
    RawJSON json.RawMessage
    FinalResult *Result
}
```

`ParseLine([]byte)` 对已知类型做映射，对未知类型返回 `Kind="raw_event"`。

- [ ] **Step 4: 实现 Runner**

Runner 必须分别读取 stdout 和 stderr，stdout 每行写 `task_events` 后再广播；stderr 写 `technical_log`；进程退出时依据最终结果把任务更新为 `waiting_input`、`completed` 或 `failed`。先登记产物再完成任务，所有产物路径必须通过资产根目录校验。

- [ ] **Step 5: 验证并提交**

Run: `go test ./internal/codex ./internal/store -run 'Event|Runner|Task' -v`

Expected: 假 CLI 的三种结局和未知事件均 PASS。

Commit:

```powershell
git add video-production-console/internal/codex video-production-console/internal/store/tasks.go video-production-console/tests/fakes
git commit -m 'feat: stream and persist codex task events'
```

## Task 7: 实现可调 1—4 并发调度、取消与恢复

**Files:**
- Create: `video-production-console/internal/codex/scheduler.go`
- Create: `video-production-console/internal/codex/scheduler_test.go`
- Create: `video-production-console/internal/httpapi/tasks.go`
- Create: `video-production-console/internal/httpapi/tasks_test.go`

- [ ] **Step 1: 写并发测试**

测试设置为 1 时第二项保持 `queued`；设置改成 4 后最多四项变为 `running`；`waiting_input` 不计入运行数；同项目第二个写任务必须保持排队。

- [ ] **Step 2: 实现调度器接口**

```go
type Scheduler interface {
    Enqueue(context.Context, domain.CodexTask) error
    Resume(context.Context, taskID string, answer string) error
    Cancel(context.Context, taskID string) error
    SetLimit(limit int) error
    Snapshot() Snapshot
}
```

`SetLimit` 只接受 1—4；降低限制不结束运行中的任务。

- [ ] **Step 3: 实现项目锁和温和取消**

运行任务前获取 `project_id` 锁；结束、失败或取消时释放。取消先调用 `Process.Kill` 前的 Windows 温和终止策略；5 秒未退出再结束进程树，保存 `cancel_requested` 与 `cancelled` 事件。

- [ ] **Step 4: 实现任务 API**

```text
POST /api/projects/{id}/tasks
GET  /api/tasks?project_id=&status=
GET  /api/tasks/{id}
POST /api/tasks/{id}/answer
POST /api/tasks/{id}/cancel
```

回答接口只接受 `waiting_input`，并校验 session ID 已存在。回答内容作为 stdin 传给 resume 命令，不拼入 Shell。

- [ ] **Step 5: 验证并提交**

Run: `go test ./internal/codex ./internal/httpapi -run 'Scheduler|Task|Resume|Cancel' -v`

Expected: 并发、项目锁、等待不占槽位、回答恢复和取消全部 PASS。

Commit:

```powershell
git add video-production-console/internal/codex/scheduler* video-production-console/internal/httpapi/tasks*
git commit -m 'feat: schedule resumable codex tasks'
```

## Task 8: 实现 WebSocket 事件和断线重放

**Files:**
- Create: `video-production-console/internal/realtime/hub.go`
- Create: `video-production-console/internal/realtime/hub_test.go`
- Modify: `video-production-console/internal/httpapi/router.go`
- Create: `video-production-console/internal/httpapi/events_test.go`

- [ ] **Step 1: 写重放测试**

先写入 sequence 1—3，WebSocket 连接使用 `?task_id=<id>&after=1`，断言只收到 2 和 3；再广播 4，断言同一连接实时收到 4。

- [ ] **Step 2: 实现 Hub**

Hub 以 `task_id` 订阅，不允许客户端订阅全局任意目录。连接建立时先从数据库读取 `sequence > after`，随后注册实时订阅。每个客户端使用有界缓冲 128；慢客户端断开并依靠重放恢复，不能阻塞 CLI Runner。

- [ ] **Step 3: 增加本机 Origin 校验**

仅允许 `http://127.0.0.1:<configured-port>` 和 `http://localhost:<configured-port>`。没有 Origin 的本机测试客户端可在测试配置中启用，生产默认拒绝未知 Origin。

- [ ] **Step 4: 验证并提交**

Run: `go test ./internal/realtime ./internal/httpapi -run 'Hub|Event|Origin' -v`

Expected: 重放、实时事件、慢客户端和 Origin 测试 PASS。

Commit:

```powershell
git add video-production-console/internal/realtime video-production-console/internal/httpapi
git commit -m 'feat: stream task events to the browser'
```

## Task 9: 接入爆款库 HTTP/SSE 并验证 CLI MCP

**Files:**
- Create: `video-production-console/internal/baokuan/client.go`
- Create: `video-production-console/internal/baokuan/client_test.go`
- Create: `video-production-console/internal/httpapi/dependencies.go`
- Create: `video-production-console/internal/httpapi/dependencies_test.go`
- Create: `video-production-console/internal/codex/mcpcheck.go`
- Create: `video-production-console/internal/codex/mcpcheck_test.go`
- Create: `video-production-console/internal/codex/mcpconfigure.go`
- Create: `video-production-console/internal/codex/mcpconfigure_test.go`

- [ ] **Step 1: 写假爆款库测试**

使用 `httptest.Server` 覆盖：`/materials/search` 正常代理、`/materials/bundle` 最大 20 条、`/events` SSE 重连、服务离线返回依赖状态而不退出控制台。

- [ ] **Step 2: 实现只读客户端**

客户端只允许以下操作：

```go
SearchMaterials(ctx context.Context, query url.Values) ([]Material, error)
GetMaterialBundle(ctx context.Context, ids BundleRequest) (Bundle, error)
Subscribe(ctx context.Context, after string) (<-chan LibraryEvent, error)
Health(ctx context.Context) DependencyStatus
```

不得实现采集、审核写入、代理控制和程序重启。

- [ ] **Step 3: 实现 MCP 健康检查和显式配置**

执行 `codex mcp list`，解析输出并确认存在启用状态的 `baokuan`。再请求 `GET http://127.0.0.1:2022/api/channels/library/materials/search?limit=1`。两个条件分别显示“CLI MCP 未注册”和“爆款库主程序离线”。检查不得自动关闭或重启主程序。

提供显式方法：

```go
func ConfigureBaokuanMCP(ctx context.Context, codexBinary, mcpExecutable, baseURL string) error {
    cmd := exec.CommandContext(ctx, codexBinary,
        "mcp", "add", "baokuan", "--",
        mcpExecutable, "mcp", "--base", baseURL,
    )
    output, err := cmd.CombinedOutput()
    if err != nil {
        return fmt.Errorf("configure baokuan MCP: %w: %s", err, strings.TrimSpace(string(output)))
    }
    return nil
}
```

配置前先解析 `codex mcp list`：不存在时才允许添加；已经存在且命令一致时直接成功；已经存在但命令不同则返回 `mcp_config_conflict`，不得自动删除或覆盖。网页只有在用户点击“配置爆款库 MCP”后才调用该方法。

- [ ] **Step 4: 暴露网页只读接口**

```text
GET  /api/dependencies
POST /api/dependencies/baokuan-mcp/configure
GET  /api/library/materials/search
POST /api/library/materials/bundle
```

后端过滤允许的查询参数，限制 bundle 为 1—20 项。

- [ ] **Step 5: 验证并提交**

Run: `go test ./internal/baokuan ./internal/codex ./internal/httpapi -run 'Baokuan|MCP|Dependency' -v`

Expected: HTTP、SSE、离线和 MCP 缺失状态全部 PASS。

Commit:

```powershell
git add video-production-console/internal/baokuan video-production-console/internal/codex/mcpcheck* video-production-console/internal/httpapi/dependencies*
git commit -m 'feat: connect the shared viral library'
```

## Task 10: 接入 Obsidian 和三个 Skill 工作流

**Files:**
- Create: `video-production-console/internal/obsidian/service.go`
- Create: `video-production-console/internal/obsidian/service_test.go`
- Modify: `video-production-console/internal/codex/prompt.go`
- Create: `video-production-console/internal/codex/workflows_test.go`

- [ ] **Step 1: 写 Obsidian 路径测试**

验证卡片路径必须位于配置的 vault 内；不存在的 vault 阻止 `topic_select` 和 `topic_deepen`，但不阻止已有素材的 `montage`；删除项目只解除数据库关联，不删除 Markdown。

- [ ] **Step 2: 实现服务**

```go
func (s Service) ValidateCardPath(path string) (string, error)
func (s Service) RelativeCardPath(path string) (string, error)
func (s Service) Health() DependencyStatus
```

使用 `filepath.EvalSymlinks` 和 `filepath.Rel` 防止软链接逃出 vault。

- [ ] **Step 3: 固定工作流 Prompt**

- `topic_select`：`Use $finance-topic-selector`，创建候选卡并返回路径。
- `topic_deepen`：恢复或新建任务，要求深化指定卡片。
- `remix`：`Use $finance-viral-remix`，读取状态为可写稿的卡片；用户直接上传原文时可跳过卡片。
- `spoken_format`：只对已确认连续版逐字断句。
- `montage`：使用混剪 Skill，输入当前文案、配音、SRT 和账号背景图路径。

每种任务都返回统一结果 Schema，且明确禁止打开微信视频号测试。

- [ ] **Step 4: 验证并提交**

Run: `go test ./internal/obsidian ./internal/codex -run 'Obsidian|Workflow' -v`

Expected: 五种任务 prompt、vault 边界和跳过选题卡路径全部 PASS。

Commit:

```powershell
git add video-production-console/internal/obsidian video-production-console/internal/codex
git commit -m 'feat: wire topic remix and montage workflows'
```

## Task 11: 实现 React 账号侧栏和统一看板

**Files:**
- Create: `video-production-console/web/src/api/types.ts`
- Create: `video-production-console/web/src/api/client.ts`
- Create: `video-production-console/web/src/app/App.tsx`
- Create: `video-production-console/web/src/components/AccountSidebar.tsx`
- Create: `video-production-console/web/src/components/ProjectBoard.tsx`
- Create: `video-production-console/web/src/components/CreateAccountDialog.tsx`
- Create: `video-production-console/web/src/components/CreateProjectDialog.tsx`
- Create: `video-production-console/web/src/test/ProjectBoard.test.tsx`

- [ ] **Step 1: 写看板测试**

使用 Mock Service Worker 或测试 fetch stub 返回两个账号、三个项目。断言“全部账号”显示三张卡，选择账号 A 后只显示其项目；新增账号表单只有名称和背景图；新增项目支持“从选题开始”和“已有素材开始”。

- [ ] **Step 2: 实现 API 类型**

前端 `Account`、`Project`、`Asset`、`CodexTask` 枚举必须与 Go JSON 字段一一对应。`api/client.ts` 统一处理非 2xx 响应为：

```ts
export type APIError = { code: string; message: string; details?: unknown };
```

- [ ] **Step 3: 实现响应式三栏界面**

左栏账号筛选，中部按阶段分列，右侧项目抽屉。项目卡必须显示账号色、缺失资产、活动任务和更新时间。拖动卡片调用 `/move`，409 时恢复原位置并显示缺失资产。

- [ ] **Step 4: 验证并提交**

Run:

```powershell
Set-Location video-production-console/web
npm run test -- --run
npm run build
```

Expected: 组件测试 PASS，TypeScript 和 Vite 构建成功。

Commit:

```powershell
git add video-production-console/web
git commit -m 'feat: add account-filtered production board'
```

## Task 12: 实现项目详情、资产预览和 Codex 对话

**Files:**
- Create: `video-production-console/web/src/components/ProjectDrawer.tsx`
- Create: `video-production-console/web/src/components/AssetPanel.tsx`
- Create: `video-production-console/web/src/components/TaskConversation.tsx`
- Create: `video-production-console/web/src/hooks/useTaskEvents.ts`
- Create: `video-production-console/web/src/test/TaskConversation.test.tsx`
- Modify: `video-production-console/internal/httpapi/projects.go`

- [ ] **Step 1: 写实时对话测试**

模拟历史事件、WebSocket 新事件和断线重连，断言技术日志默认折叠；`needs_input` 显示问题、选项和输入框；提交后按钮禁用直到任务重新进入 `running`。

- [ ] **Step 2: 实现事件 Hook**

`useTaskEvents(taskID)` 保存最后 `sequence`，连接 `/api/events?task_id=...&after=...`；断线按 1、2、5、10 秒退避重连；组件卸载时关闭连接。

- [ ] **Step 3: 实现资产面板**

- 文案和 SRT 使用文本预览。
- MP3/WAV/M4A 使用 `<audio controls>`。
- 背景图使用 `<img>`。
- MP4 使用 `<video controls preload="metadata">`。
- 缺失文件显示 `missing`，不返回本机任意路径内容。

后端增加受控 `GET /api/assets/{id}/content`，只通过资产 ID 读取数据库登记路径，支持 Range 请求播放音视频。

- [ ] **Step 4: 验证并提交**

Run:

```powershell
go test ./internal/httpapi -run AssetContent -v
Set-Location video-production-console/web
npm run test -- --run
```

Expected: 资产边界、Range、实时问题和重连测试 PASS。

Commit:

```powershell
git add video-production-console/internal/httpapi video-production-console/web/src
git commit -m 'feat: add project assets and codex conversations'
```

## Task 13: 实现设置、服务状态和发布看板

**Files:**
- Create: `video-production-console/internal/httpapi/settings.go`
- Create: `video-production-console/internal/httpapi/settings_test.go`
- Create: `video-production-console/web/src/components/SettingsDialog.tsx`
- Create: `video-production-console/web/src/components/DependencyStatus.tsx`
- Create: `video-production-console/web/src/test/SettingsDialog.test.tsx`

- [ ] **Step 1: 写设置测试**

断言并发值 1 和 4 成功，0 和 5 返回 400；降低并发不取消运行任务；Codex、MCP、爆款库、Obsidian 分别显示独立状态。

- [ ] **Step 2: 实现设置 API**

```text
GET /api/settings
PUT /api/settings/max-codex-concurrency
GET /api/dependencies
```

写入并发值后调用 `Scheduler.SetLimit`。路径设置必须规范化并验证存在，不允许通过网页设置 API 密钥。

- [ ] **Step 3: 实现设置页面和发布筛选**

设置页用步进器显示 1—4；展示 Codex 版本、认证可用性、爆款库 HTTP、爆款库 MCP 和 Obsidian。主看板提供“待发布”和“已发布”快捷筛选，发布操作记录 `published_at`。

- [ ] **Step 4: 验证并提交**

Run:

```powershell
go test ./internal/httpapi -run 'Setting|Dependency|Published' -v
Set-Location video-production-console/web
npm run test -- --run
```

Expected: 设置边界、服务状态和发布筛选全部 PASS。

Commit:

```powershell
git add video-production-console/internal/httpapi video-production-console/web/src
git commit -m 'feat: add console settings and publishing views'
```

## Task 14: 端到端测试、单文件构建和运行手册

**Files:**
- Create: `video-production-console/tests/e2e/console.spec.ts`
- Create: `video-production-console/Makefile`
- Create: `video-production-console/README.md`
- Modify: `video-production-console/internal/app/app.go`
- Modify: `video-production-console/cmd/console/main.go`

- [ ] **Step 1: 写端到端场景**

Playwright 覆盖：添加账号和背景图；创建空项目并收到 CLI 问题；网页回答后恢复；上传文案、配音和 SRT；调整并发为 4；按账号筛选；移动到待发布和已发布；刷新后恢复事件。

测试使用假 Codex 和假爆款库，禁止打开微信视频号。

- [ ] **Step 2: 嵌入前端并支持优雅退出**

使用 `//go:embed web/dist/*` 提供静态资源；主进程监听 `os.Interrupt`，停止接收新任务、等待 HTTP 连接、把仍运行任务标记 `interrupted`，但不关闭爆款库主程序。

- [ ] **Step 3: 编写 Makefile 目标**

```make
test:
	go test ./...
	cd web && npm run test -- --run

build-web:
	cd web && npm ci && npm run build

build: build-web
	go build -o dist/video-production-console.exe ./cmd/console

e2e:
	cd web && npx playwright test ../tests/e2e
```

- [ ] **Step 4: 编写运行手册**

README 必须写明：本机端口、数据目录、如何配置 Codex CLI 专用 profile、如何注册 `baokuan` MCP、如何选择 Obsidian vault、如何检查依赖、如何备份 `console.db` 和资产目录、为何不能关闭爆款库主程序。

- [ ] **Step 5: 运行完整验证**

Run:

```powershell
go test ./...
Set-Location video-production-console/web
npm run test -- --run
npm run build
npx playwright test ../tests/e2e
Set-Location ..
go build -o dist/video-production-console.exe ./cmd/console
```

Expected: 所有 Go、Vitest、Playwright 测试 PASS，并生成 `dist/video-production-console.exe`。整个验证过程不启动微信视频号，不执行真实发布。

- [ ] **Step 6: 最终提交**

```powershell
git add video-production-console
git commit -m 'feat: complete local video production console'
git status --short
```

Expected: commit 成功，`git status --short` 无计划内未提交文件。

## 完成后验收

Run:

```powershell
Start-Process -FilePath '.\video-production-console\dist\video-production-console.exe' -WindowStyle Hidden
Invoke-RestMethod 'http://127.0.0.1:2030/api/health'
codex mcp list
```

Expected:

- 健康接口返回 `status=ok`。
- 设置页允许并发值 1—4。
- `codex mcp list` 显示启用的 `baokuan`。
- 爆款库主程序仍监听 `127.0.0.1:2022`，系统代理未被修改。
- 可添加第五个及更多账号，无需修改代码。
- 任一项目的资产和 Codex session 不会出现在另一项目中。
