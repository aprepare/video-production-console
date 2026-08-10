# AI 接手说明

> 面向接手本仓库的 AI。先保护工作区与运行时数据，再开始任何修改。
>
> 使用者操作见 [使用说明](USER-GUIDE.md)。2026-08-10 交付快照已归档至 [history/HANDOFF-DELIVERY-2026-08-10.md](history/HANDOFF-DELIVERY-2026-08-10.md)。

## 1. 项目定位与当前状态

**一句话定位：**本地视频生产控制台——Go 提供鉴权、项目/资产、Codex 任务调度、对话与实时事件；React/Vite 提供项目看板和五阶段工作台；混剪明文草稿可登记为剪映正式资产。

**当前状态：**主流程具备登录、项目隔离、资产管理、Codex/桌面对话、任务进度与恢复、爆款库代理、Obsidian/Skills、混剪登记、五阶段工作台、主题与项目列折叠。同行原文闭环已落地：`source_script` 上传 → `remix.standard`（`source_version_id` 绑定与活动任务幂等）→ `continuous_script`（依赖血缘）→ 解锁素材阶段。SPA 路由刷新由 `internal/webui/embed.go` 回退到 `index.html`。

## 2. 目录结构与职责

- `cmd/console/main.go`：启动链；设置、Codex、SQLite、Skills、任务恢复、路由、实时 Hub；`--version` 输出注入版本。
- `cmd/maintenance/main.go`：SQLite 备份 / 完整性检查 / 恢复到新路径。
- `internal/app/`：Handler 装配。
- `internal/config/`：默认 `127.0.0.1:2030`、`./video-console-data`、爆款库 `http://127.0.0.1:2022`、Codex 二进制 `codex`。
- `internal/httpapi/`：auth、accounts、projects、tasks、conversations、settings、skills、assets、montage 等。
- `internal/store/`：SQLite 迁移与 Repository。
- `internal/domain/`：共享领域类型。
- `internal/codex/`：Runner、Dispatcher、Scheduler、manifest、结果校验。
- `internal/codexapp/`、`internal/conversation/`：App Server 与控制台对话。
- `internal/realtime/`：任务事件 Hub 与断线重放。
- `internal/workflow/`、`internal/montage/`：二创工作流与剪映登记。
- `internal/assets/`、`internal/obsidian/`、`internal/skillregistry/`、`internal/baokuan/`：资产、Vault、Skills、爆款库。
- `internal/webui/`：嵌入 `dist`；未知 GET/HEAD 非文件路径回退 `index.html`（SPA）。
- `web/src/App.tsx` 与 `web/src/project-workbench/`：看板与五阶段工作台。
- `web/src/api|auth|console|projects|query|runtime|tasks/`：已接入前端模块（非死代码）。
- `schemas/`、`scripts/`、`docs/operations/`：契约、验证/发布脚本、运维文档。

权威数据库：`video-console-data/console.db`。根目录遗留 `video-console.db` 不是权威库。

## 3. 启动、开发与验证

PowerShell；不要用 bash 的 `&&`。

```powershell
codex --version
go run .\cmd\console
```

前端开发：`Set-Location web; npm install; npm run dev`（仅 HMR；生产走嵌入 dist）。

日常验证（不覆盖嵌入资源）：

```powershell
.\scripts\verify-baseline.ps1
```

发布同步嵌入前端时才运行 `npm --prefix web run build:embed`，随后 `.\scripts\check-embedded-dist.ps1 -Strict`。正式发布优先 `.\scripts\release.ps1 -Version x.y.z`。

## 4. 运行前置

- Codex CLI 可执行；爆款库 `:2022` 只读代理（可选，不阻塞手动粘贴原文）。
- Obsidian Vault / 选题卡目录、Skills 根、剪映 machine profile 按设置配置。
- 监听：新数据根默认 `127.0.0.1:2030`；已有安装用持久化 `listen_addr`；局域网须显式配置并在 `restart_required` 后重启。
- 无管理员时必须提供 `VIDEO_CONSOLE_INITIAL_PASSWORD`。

## 5. 关键数据流

1. 登录会话 + CSRF；API 鉴权与限速。
2. 项目资产版本化；`source_script` 替换会使依赖下游变 stale。
3. 任务经 manifest 准备后入队；`remix.standard` 可绑定 `source_version_id`；同版本活动任务复用，不同版本 `409 active_remix_conflict`。
4. 完成时校验输入仍为 current，否则 `input_superseded`；`continuous_script` 写入 source 依赖。
5. 事件经 WebSocket 推送；混剪明文 → 登记 → 验证 → ready。

## 6. 前端工作台要点

- 阶段：`script → assets → mixing → review → published`；成片 `final_video` 可选。
- 同行原文入口仅在 `script` 阶段；主动作区分选题卡 `/remix` 与正式 `remix.standard`。
- 主题键 `video-production-console-theme`；项目列折叠阈值 `4`。

## 7. 测试与验收

- Go：`go test ./...`、`go vet ./...`
- 前端：`npm --prefix web run typecheck|lint|test|test:e2e|build:verify`
- 人工：[acceptance-checklist.md](operations/acceptance-checklist.md)、[montage-registration.md](operations/montage-registration.md)
- 自动测试使用 fake Codex / httptest，不等于外部桌面软件已验收。

## 8. 接手顺序与排障

1. `git status --short`；读本说明与 USER-GUIDE。
2. 确认依赖与数据根，勿清 `video-console-data/`。
3. 先跑低副作用测试，勿先 `build:embed`。
4. 阅读 `cmd/console/main.go` → `internal/app` → httpapi/store → `web/src/App.tsx`。
5. 修改限于任务范围；提交/推送须明确指令。

排障摘要：登录看库路径与 CSRF；任务卡住看事件/并发/Codex；混剪优先重试登记；前端空白区分 Vite 与嵌入 dist；项目页刷新 404 检查 SPA 回退是否已构建进当前二进制。

## 9. 工作区保护

- 禁止擅自 `git reset/checkout/restore/stash/clean` 或删除未跟踪的数据库与嵌入 dist。
- `.gitignore` 已忽略 `.tmp/`、`dist/`、`video-console-data/`、日志、exe、Playwright 缓存等。
- 清理运行垃圾时保留：`video-console-data/`、`internal/webui/dist/`、当前正在监听的服务进程所用 exe。

## 10. 已知限制

- 不自动打开微信视频号或剪映。
- 外部依赖离线会影响功能。
- 源码与嵌入 dist 可能暂时不一致；开发页与生产二进制不是同一路径。
