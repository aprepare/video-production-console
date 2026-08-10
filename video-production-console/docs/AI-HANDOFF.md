# AI 接手说明

> 面向接手本仓库的 AI。本文记录当前可见实现与操作边界；先保护工作区，再开始任何修改。
>
> 本轮增量与收口状态见 [交付总结](HANDOFF-DELIVERY-SUMMARY.md)。

## 1. 项目定位与当前状态

**一句话定位：**这是一个本地视频生产控制台：用 Go 提供鉴权、项目/资产、Codex 任务调度、对话与实时事件服务，用 React/Vite 提供项目看板和五阶段工作台，并把混剪草稿登记为剪映可继续编辑的正式资产。

**当前状态：**主流程已具备登录、项目隔离、资产管理、Codex/桌面对话、任务进度与恢复、爆款库代理、Obsidian/Skills 状态、混剪明文草稿与剪映登记；前端近期加入了五阶段项目工作台、主题切换和项目列折叠。同行原文 `source_script` → `remix.standard` → `continuous_script` 闭环已在源码收口，包含上传、版本绑定、活动任务幂等、血缘、前端入口与测试；发布到运行服务仍须显式执行 `build:embed` 并重启验证。当前工作区不是干净基线，存在大量已修改文件、未跟踪测试/产物/数据库和嵌入式前端 dist 新旧文件混杂，见“工作区注意事项”。

## 2. 目录结构与职责

- `cmd/console/main.go`：启动链；加载默认/持久化设置，解析 Codex，打开 SQLite，扫描 Skills，恢复任务，组装服务、路由和实时 Hub，启动 HTTP 服务；`--version` 可无副作用输出注入的版本信息。
- `cmd/maintenance/main.go`：离线/运维命令；提供 SQLite 一致性备份、完整性检查和恢复到新路径。
- `internal/app/`：应用装配与总 Handler 组合。
- `internal/config/`：进程默认配置；`config.Default()` 新安装默认监听 `127.0.0.1:2030`、数据目录 `./video-console-data`、爆款库 `http://127.0.0.1:2022`、Codex 可执行文件 `codex`。
- `internal/httpapi/`：HTTP API 按领域拆分，包括 auth、accounts、projects、tasks、task results、conversations、history、settings、skills、dependencies、assets、montage 等。
- `internal/store/`：SQLite 访问、迁移和 Repository；覆盖 accounts、projects、assets、tasks、events、conversations、workflows、settings、skills、montage 等。
- `internal/domain/`：领域模型与设置/任务等共享类型。
- `internal/codex/`：Codex CLI 命令、Runner、Dispatcher、Scheduler、事件、结果校验和任务 manifest。
- `internal/codexapp/`、`internal/conversation/`：Codex App Server 进程/RPC、控制台对话、历史恢复和任务适配。
- `internal/realtime/`：任务事件 Hub；任务事件持久化后通过 WebSocket 推送，客户端可按序号断线重放。
- `internal/workflow/`：混剪等工作流协调；`RemixCoordinator` 监听任务完成并推进终态工作流。
- `internal/montage/`：剪映机器配置解析、可信运行时、登记、验证、恢复和重试。
- `internal/assets/`、`internal/obsidian/`、`internal/skillregistry/`、`internal/baokuan/`：资产文件、Obsidian、Skills 扫描和爆款库客户端。
- `internal/webui/embed.go`：通过 `//go:embed dist/* dist/assets/*` 嵌入 `internal/webui/dist`；生产二进制直接从嵌入资源提供前端。
- `web/src/App.tsx`：React 主页面、路由状态、账号/项目/任务/设置/对话交互、主题和项目列折叠。
- `web/src/project-workbench/`：项目工作台；`ProjectWorkbench.tsx`、`ProductionRail.tsx`、`ProjectAssets.tsx`、`ProjectConversation.tsx`、`workflow.ts` 负责阶段、主动作、资产和对话。
- `web/package.json`、`web/vite.config.ts`：React/Vite 开发、构建、Lint、Vitest 配置。
- `schemas/`：Codex 结果、任务 manifest、设置和选题候选 JSON Schema。
- `docs/operations/acceptance-checklist.md`：真实人工验收路径；`docs/operations/montage-registration.md`：混剪登记与恢复说明。
- `scripts/release.ps1`：显式正式发布入口；同步嵌入前端、注入版本/提交/构建时间、构建控制台与维护工具、复制 Schema、生成 SHA-256 和 ZIP。

SQLite 默认运行时数据库是数据根目录下的 `console.db`；仓库根目录当前还存在未跟踪的 `video-console.db`，不要把它误认为唯一权威数据库。

## 3. 启动链、开发与验证命令

以下均为 PowerShell 语法；Windows 命令不要使用 bash 的 `&&`，需要连续执行时逐条运行或使用 PowerShell 的 `;`/条件判断。

### 前置检查

```powershell
codex --version
codex mcp list
Get-Command go
Get-Command node
Get-Command npm
Test-NetConnection 127.0.0.1 -Port 2022
```

### 前端开发

```powershell
Set-Location .\web
npm install
npm run dev
```

Vite 开发服务器只用于前端开发/HMR；生产启动使用 Go 嵌入的 `internal/webui/dist`。

### 后端直接运行（使用现有/刚构建的嵌入 dist）

```powershell
Set-Location ..
go run .\cmd\console
```

也可构建并运行：

```powershell
go build -o .\dist\video-production-console.exe .\cmd\console
.\dist\video-production-console.exe
```

启动后访问 `http://127.0.0.1:2030`。新数据根默认监听 `127.0.0.1:2030`；已有数据根使用持久化的 `listen_addr`。需要局域网访问时，须显式配置具体局域网地址或 `0.0.0.0:2030`，并在 API 返回 `restart_required` 后重启服务使配置生效。

### 日常验证（`build:verify`）

```powershell
.\scripts\verify-baseline.ps1
```

该脚本依次运行 `go test ./...`、`go vet ./...`、前端 lint/Vitest/Playwright，并通过 `npm run build:verify` 构建到 `.tmp/web-dist`，不会覆盖生产嵌入资源。若需要只验证前端：`Set-Location web; npm run typecheck; npm run lint; npm run test; npm run test:e2e; npm run build:verify`。

### 发布嵌入构建（`build:embed`）

只有明确需要同步生产页面时才运行 `npm run build:embed`；该命令会清空并重建 `internal/webui/dist`。`npm run build` 是兼容别名，不应用于日常验证。正式发布统一运行 `.\scripts\release.ps1 -Version 1.0.0`，脚本会拒绝覆盖同版本目录/ZIP，并生成 `release/<version>/SHA256SUMS.txt`。构建后可用 `video-production-console.exe --version` 核对版本。若只查看当前修改：`git status --short; git diff --stat; git diff -- .`。

### 数据库备份与恢复

发布包内的 `console-maintenance.exe` 提供：

```powershell
.\console-maintenance.exe backup -data-root C:\path\to\video-console-data -output D:\video-console-backups
.\console-maintenance.exe check -database D:\video-console-backups\console-<timestamp>.db
.\console-maintenance.exe restore -backup D:\video-console-backups\console-<timestamp>.db -destination D:\restored-video-console-data\console.db
```

备份输出目录必须在数据根外。备份使用 SQLite `VACUUM INTO`，同时生成同时间戳 `data-manifest-*.json`。恢复只写入不存在的新数据库路径并在复制前后执行 `PRAGMA integrity_check`，不会覆盖当前数据库；资产/媒体文件需按 manifest 从文件级备份恢复，切换数据根前必须停止控制台并保留原目录。

## 4. 运行前置条件与默认值

- **Codex CLI**：`cmd/console/main.go` 通过 `exec.LookPath("codex")` 解析，需先确认 `codex --version` 可执行。
- **baokuan 2022**：默认只读代理地址为 `http://127.0.0.1:2022`；`codex mcp list` 应看到启用的 `baokuan` MCP，HTTP 服务也应可连接。
- **Obsidian**：在“设置”中配置 Vault 与选题卡目录；相关路径由 `internal/obsidian/service.go` 使用。
- **Skills**：默认扫描 `C:\Users\prepare\.codex\skills`；启动时由 `internal/skillregistry` 扫描，manifest-backed 任务依赖可用 Skill。
- **剪映 machine profile**：在设置中配置 `machine_profile_path`、`jianying_root`、媒体索引/媒体根等；有有效机器配置时才启用 `internal/montage.Coordinator` 和登记恢复。
- **默认监听**：新数据根默认 `127.0.0.1:2030`，仅本机可访问；已有数据根使用持久化的 `listen_addr`。需要手机或局域网访问时，须在设置中显式配置具体局域网地址或 `0.0.0.0:2030`，并在 `restart_required` 后重启控制台；不得暴露公网或公共 Wi-Fi。
- **初始管理员口令**：数据库尚无管理员时，启动前必须设置 `VIDEO_CONSOLE_INITIAL_PASSWORD`；已有管理员时不会读取该变量。密码只用于 bcrypt 初始化，不会回显或写入设置；首次登录后仍应立即修改。
- **配置生效**：并发限制、历史数量等部分设置可热更新；路径、App Server、模型/密钥等启动配置可能返回 `restart_required=true`，保存后需重启控制台。

## 5. 前端主要页面与近期 UI

- 首页/项目看板：账号、项目列表、任务状态、项目资产和选题入口。
- 选题工作台：选题会话、候选、来源和深化结果；正式选题卡写入配置的 Obsidian 选题卡目录。
- 项目工作台：`web/src/project-workbench/ProjectWorkbench.tsx` 依据 `deriveProductionStage()` 和 `nextPrimaryAction()` 推导当前阶段和下一主动作。
- Codex 对话/任务详情：查看用户消息、助手消息、中文语义进度、原始任务结果、产物、诊断和时序。
- 历史与设置：区分桌面版/CLI/控制台历史，管理 Codex、爆款库、Obsidian、媒体、剪映和 App Server 配置。
- 生产阶段：`script` 文案生成 → `assets` 制作素材 → `mixing` 智能混剪 → `review` 审核 → `published` 发布完成；`mix_draft` 或可选的 `final_video` 都可进入审核，发布不强制要求 `final_video`（以 `internal/domain/stages.go` 为准）。工作台还会根据缺失的连续文案、配音、SRT、账号背景图、混剪草稿或可选成片给出下一步。
- 主题：`App.tsx` 使用 `THEME_STORAGE_KEY = "video-production-console-theme"`，支持 light/dark 并持久化到浏览器 `localStorage`。
- 项目列折叠：`App.tsx` 使用 `PROJECT_COLLAPSE_LIMIT = 4`，项目较多时按列折叠，保留展开/收起交互；改动时优先保留项目标题、当前项目和移动端可用性。
- 混剪 UI：任务阶段区分“生成明文草稿”“等待/正在登记剪映”“登记失败/中断”“已登记为正式资产”，失败时支持只重试登记。

## 6. 关键后端数据流

1. **登录**：`internal/auth` + `internal/security` + `internal/store/auth.go`；登录创建会话/CSRF 信息，API 受鉴权和限速保护，密码修改后旧会话可按安全策略撤销。
2. **项目与资产**：`internal/httpapi/projects.go` 接收项目创建、读取、删除、上传资产、移动阶段、启动二创/发布；`internal/store/projects.go`、`assets_v2.go` 持久化项目隔离资产，文件由 `internal/assets` 管理。
3. **任务调度**：任务从项目接口进入 `internal/httpapi/tasks.go` 和 `task_manifest.go`，经 preparer 生成可校验 manifest，交给 `internal/codex.Scheduler`/`CompositeScheduler`；队列、并发、取消、恢复和完成观察器在 scheduler/adapter 中处理。
4. **事件/WebSocket**：Codex/任务运行过程写入 task events 和 semantic events；`internal/realtime.Hub` 按 task ID 发布，`/api/tasks/{id}/events` 支持 WebSocket 与 `after` 序号重放，前端将其转成阶段、耗时和中文进度。
5. **工作流**：`internal/workflow/remix.go` 监听任务完成，推进二创/混剪相关工作流；会话层 `internal/conversation` 负责 Codex App Server 对话与任务适配。
6. **混剪登记**：`internal/workflow`/`internal/montage` 先生成受控工作区明文草稿，再登记到剪映目录，随后做本机路径/回执/哈希等验证；只有成功后才把项目资产标为 ready。失败/中断保留明文产物，`retry-registration` 只重跑登记与验证，不重新运行 Codex。
7. **外部依赖**：`internal/baokuan` 代理爆款库，`internal/obsidian` 写入/读取 Vault，`internal/skillregistry` 扫描 Skills；启动时还会恢复未完成任务并对项目/账户资产做 reconcile。

## 7. 关键 API 分类

不要把 API 清单当作稳定公共 SDK；按领域理解即可：

- **鉴权与会话**：`/api/auth/*`，登录、当前用户、登出、密码、撤销会话。
- **账号与项目**：`/api/accounts/*`、`/api/projects/*`，账号背景、项目 CRUD、上传/替换资产、阶段移动、二创和发布。
- **任务与结果**：`/api/projects/{id}/tasks`、`/api/tasks/{id}/*`，创建/读取/回答/取消、结果、产物、诊断、语义事件、时序、完成重试。
- **实时与对话**：任务事件 WebSocket；`/api/chat/sessions/*` 管理控制台会话、消息、事件和 fork。
- **Codex 历史**：`/api/codex/history/*`，列表、读取、继续原会话、复制新会话。
- **资产与混剪**：`/api/assets/{id}/*`、`/api/tasks/{id}/retry-registration`，内容、目录 manifest、本机打开目录和登记重试。
- **依赖/设置/Skills**：`/api/dependencies`、`/api/settings/*`、`/api/skills/*`，依赖状态、配置测试/修复、热更新/重启提示、Skills 扫描与读取。
- **爆款库代理**：`/api/library/materials/*`，查询和打包材料；控制台不启动、停止或修改爆款库主程序。

## 8. 测试覆盖与验证标准

- Go 测试遍布 `internal/*`，覆盖 store 迁移/Repository、鉴权、密码/会话、Codex command/runner/scheduler/结果校验、对话恢复、HTTP API、实时 Hub、资产、混剪登记、设置和时序。
- 前端测试位于 `web/src/App.test.tsx`、`web/src/project-workbench/*.test.tsx`，使用 Vitest、Testing Library 和 jsdom；日常脚本为 `npm run typecheck`、`npm run lint`、`npm run test`、`npm run build:verify`。
- 基线验证运行 `.\scripts\verify-baseline.ps1`。其中前端构建输出到 `.tmp/web-dist`，不会修改正式嵌入资源。仅在明确同步生产页面时运行 `npm run build:embed`，随后确认 `internal/webui/dist/index.html` 引用资源存在并通过 `.\scripts\check-embedded-dist.ps1 -Strict`；当前工作区已有 dist 混杂，先看 diff，不要自动覆盖。
- 人工验收按 `docs/operations/acceptance-checklist.md`：登录/改密、设置、选题、项目/二创、对话/历史、混剪登记、手机访问和重启恢复。
- 混剪验收按 `docs/operations/montage-registration.md`：明确验证明文保留、登记失败/中断恢复、只重试登记、本机目录动作限制和项目隔离。
- 自动验证使用 fake Codex 与 httptest，不会自动打开微信视频号或剪映；“测试通过”不等于外部桌面软件已完成最终确认。

## 9. 接手 AI 推荐顺序与排查清单

### 推荐顺序

1. 先运行 `git status --short`，记录已有修改/未跟踪文件；读取本说明、根 README 和 `docs/operations/*.md`。
2. 先确认依赖：`codex --version`、`codex mcp list`、baokuan `:2022`、Obsidian Vault、Skills 根目录、剪映 machine profile。
3. 先跑只读/低副作用检查：`go test ./...`、`go vet ./...`、前端 lint/test；不要先构建覆盖 dist。
4. 按真实调用链阅读 `cmd/console/main.go` → `internal/app` → `internal/httpapi` → `internal/store`/服务 → `web/src/App.tsx` 和工作台组件。
5. 修改前建立最小复现和回归测试；改动后只运行相关测试，再运行完整验证。
6. 需要生产 UI 时，先确认嵌入 dist 的来源、生成时间和当前 diff，再决定是否允许重建。

### 排查清单

- 登录失败：确认数据库路径、Bootstrap 状态、密码是否已改、Cookie/CSRF 和监听地址。
- 项目数据不见：确认实际 `console.db` 路径、项目 ID、资产是否属于当前项目，避免误读根目录未跟踪数据库。
- 任务卡住：看任务状态、事件序号/断线重放、scheduler 并发、Codex 路径和 App Server 状态；重启恢复会将未完成任务标记为 interrupted。
- 无法生成选题/二创：看 baokuan、Obsidian、Skills 扫描、manifest/schema 校验和外部 API 配置。
- 混剪失败：先看明文工作区、登记目录写权限、machine profile、jianying_root、媒体索引/缓存和验证回执；若明文已存在，优先只重试登记。
- 前端空白/旧界面：确认 Vite 开发服务与 Go 嵌入 dist 是否混用，检查 `internal/webui/dist/index.html` 引用的 asset 文件与 git diff。
- 配置改了未生效：查看 API 返回的 `public`、`configured_public`、`active_public` 和 `restart_required`，必要时重启。
- 手机能看但不能打开目录：这是设计限制；`open-directory` 仅允许控制台所在电脑通过 localhost/回环地址触发。

## 10. 当前工作区注意事项

当前已有大量未提交修改，以及未跟踪的测试/源码、数据库、可执行文件、日志、`node_modules`、`.playwright-cli` 和前端构建输出；`internal/webui/dist` 同时出现旧资源删除、现有文件修改和新 hash 资源。它们都可能属于当前工作过程，不能凭文件名自行清理。

- 禁止未经确认执行 `git reset`、`git checkout`、`git restore`、`git stash`、`git clean`，或删除/覆盖已有修改、数据库、构建产物和未跟踪文件。
- 不要把当前未跟踪的 `video-console.db`、`console.exe`、`publish*.exe`、`web/*-output*.txt`、`node_modules` 或 `.playwright-cli` 自动纳入提交。
- 不要把前端构建结果直接覆盖嵌入式 dist；先确认用户希望同步源码与 `internal/webui/dist`，再构建。
- 任何修复都应只改任务范围内文件；提交/推送必须等待明确指令。

## 11. 已知限制与风险

- 控制台不会自动打开微信视频号，也不会自动启动或操作剪映；用户需要在本机手动完成最终检查/打开草稿。
- baokuan、Codex CLI/App Server、Obsidian、Skills、外部搜索/媒体服务和剪映本机环境都属于外部依赖，离线或路径变化会影响功能。
- 新安装默认仅监听 `127.0.0.1:2030`；局域网监听必须显式配置，仍只应在受信任网络使用。
- 数据库无管理员时必须通过 `VIDEO_CONSOLE_INITIAL_PASSWORD` 显式提供初始口令；已有管理员时不会读取该变量。首次登录后必须修改。
- 配置的路径、App Server、模型/密钥等部分需要重启才能让当前进程采用新值；设置页通过 `restart_required` 暴露该状态。
- `internal/webui/dist` 是嵌入式发布输入，源码与 dist 可能暂时不一致；开发时 Vite 页面和生产二进制页面不是同一服务路径。
- 自动化测试主要验证服务、文件和协议，不替代真实 Codex、Obsidian、爆款库、剪映或微信视频号的人工/外部验证。