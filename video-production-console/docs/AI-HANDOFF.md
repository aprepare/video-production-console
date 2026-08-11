# AI 接手说明

> 面向接手本仓库的 AI。先保护工作区与运行时数据，再开始任何修改。
>
> 使用者操作见 [使用说明](USER-GUIDE.md)。2026-08-10 交付快照已归档至 [history/HANDOFF-DELIVERY-2026-08-10.md](history/HANDOFF-DELIVERY-2026-08-10.md)。

## 1. 项目定位与当前状态

**一句话定位：**本地视频生产控制台——Go 提供鉴权、项目/资产、Codex 任务调度、对话与实时事件；React/Vite 提供项目看板和五阶段工作台；混剪明文草稿可登记为剪映正式资产。

**当前状态（2026-08-11）：**主流程具备登录、项目隔离、资产管理、Codex/桌面对话、任务进度与恢复、爆款库代理、Obsidian/Skills、混剪登记、五阶段工作台、主题与项目列折叠。同行原文闭环已落地：`source_script` 上传 → `remix.standard`（`source_version_id` 绑定与活动任务幂等）→ `continuous_script`（依赖血缘）→ 解锁素材阶段。SPA 路由刷新由 `internal/webui/embed.go` 回退到 `index.html`。

### 1.1 近期进度快照（接手必读）

工作区 Git 提示（接手时先 `git status` 复核）：

- 分支常为 `codex/video-production-console`；相对 origin / `master` 可能已 ahead 若干提交。
- 混剪素材预检、稳定打散，以及二创改稿 / 打回重做 / 工作台选模型已纳入本分支（见下表）。

#### 已提交

| 主题 | 说明 | 线索 |
|---|---|---|
| 混剪 script runtime | `montage.execute` 默认走本地确定性 plan + skill Python，不再默认 Codex | `internal/agentruntime/`；`15ca7d9` |
| 素材预检 | 入队前 `ValidateMediaLibrary`；错误前缀 `montage media preflight:` | `httpapi/task_manifest.go`、`montageplan` |
| 稳定打散取样 | 完整合格池 → `SHA-256(task_id + 素材身份)` 排序 → 最多 48 条；Build 非严格 / 预检严格 | `montageplan.sampleMedia` / `mediaRank` |
| openai_compat / Pi | remix/topic **opt-in**；失败回退 Codex | `openaicompat/`、`piruntime/`；`967432e`、`4dcf06a` |
| Runtime 文档 | 矩阵与 env 见 §3 | `26f4360` |
| 片内标题 | 画面标题去账号前缀/任务后缀；草稿文件夹名仍可用 `draft_display_name` | `montageplan/plan.go`（随 openai 提交） |
| 发布文案复制 | 混剪/审核展示「视频描述」「短标题」+ 复制；不展示推荐标题/话题/成片 | `ProjectWorkbench.tsx`；`26a8a66` |
| 去掉成片资产 | 工作台不出现 `final_video`；进审核只看剪映草稿 ready | `ProjectAssets.tsx`、`workflow.ts`；`26a8a66` |
| 二创改稿/打回/选模型 | 连续文案可编辑；`project_step_notes` + `remix.review`（`revision_notes`）；工作台 `TaskModelFields`；embed 已重建 | `store/project_step_notes.go`、`ProjectWorkbench.tsx`、`tasks.go`；`afe21c5` |

默认运行时（勿擅自改默认）：

- 混剪 → `script`
- remix/topic → `codex`
- OpenAI / Pi → 仅环境变量 opt-in（见 §3）

本地服务常见地址：`http://127.0.0.1:2030`；嵌入前端变更需 `npm --prefix web run build:embed` 后重建 exe。混剪后端改动需重建/重启服务后生效。

### 1.2 下一阶段规划（建议接手顺序）

1. **混剪真机回归（高优先）**  
   重启 `:2030` 后，用真实大素材库连续跑多条 `montage.execute`：缺失素材应在入队前提示；不同 `task_id` 开头应变化；同一任务重试顺序应稳定。若仍有 `mix_draft` 失败/未登记，再查 `output-last-message.json`、`draft.validation.json`、登记重试 API；勿先改 runtime。
2. **二创改稿真机点验**  
   有连续文案后：手工改稿 → 版本 +1、下游 stale；填写修改要求 → 打回重做 `remix.review`（manifest `non_secret_settings.revision_notes`）；工作台模型为空时继承设置默认。嵌入前端：`npm --prefix web run build:embed` 后重建/重启。
3. **真机试用 openai_compat / pi（可选）**  
   设 env 重启后跑一条 `remix.standard`；确认仍走 manifest → result schema → 资产入库。设置页 UI **不做**。
4. **发布文案质量（可选）**  
   文案来自二创 `publishing_package`；若描述/短标题空，查 remix skill 产物而非前端。
5. **二期：项目 notes 提升为 skill（未实施）**  
   审阅项目级 revision notes → 写入对应 skill 的 `references/` 或约定规则文件；可选跨项目聚合同类需求。本期**不**自动改 `SKILL.md`。
6. **明确不做**  
   设置页选 runtime；App Server 接到 openai/pi；强行把 remix/topic 默认切离 Codex；把成片上传加回工作台；把素材选择做成真正的语义镜头理解（当前只是稳定打散）。

回滚基线参考：Week1 script runtime `15ca7d9`（以当时分支为准）。

## 2. 目录结构与职责

- `cmd/console/main.go`：启动链；设置、Codex、SQLite、Skills、任务恢复、路由、实时 Hub；`--version` 输出注入版本。
- `cmd/maintenance/main.go`：SQLite 备份 / 完整性检查 / 恢复到新路径。
- `internal/app/`：Handler 装配。
- `internal/config/`：默认 `127.0.0.1:2030`、`./video-console-data`、爆款库 `http://127.0.0.1:2022`、Codex 二进制 `codex`。
- `internal/httpapi/`：auth、accounts、projects、tasks、conversations、settings、skills、assets、montage 等。
- `internal/store/`：SQLite 迁移与 Repository。
- `internal/domain/`：共享领域类型。
- `internal/codex/`：Runner、Dispatcher、Scheduler、manifest、结果校验。
- `internal/agentruntime/`：可插拔任务后端（script / openai_compat / pi / codex 路由与适配）。
- `internal/codexapp/`、`internal/conversation/`：App Server 与控制台对话。
- `internal/realtime/`：任务事件 Hub 与断线重放。
- `internal/workflow/`、`internal/montage/`：二创工作流与剪映登记。
- `internal/assets/`、`internal/obsidian/`、`internal/skillregistry/`、`internal/baokuan/`：资产、Vault、Skills、爆款库。
- `internal/webui/`：嵌入 `dist`；未知 GET/HEAD 非文件路径回退 `index.html`（SPA）。
- `web/src/App.tsx` 与 `web/src/project-workbench/`：看板与五阶段工作台。
- `web/src/api|auth|console|projects|query|runtime|tasks/`：已接入前端模块（非死代码）。
- `schemas/`、`scripts/`、`docs/operations/`：契约、验证/发布脚本、运维文档。

权威数据库：`video-console-data/console.db`。根目录遗留 `video-console.db` 不是权威库。

## 3. AgentRuntime 矩阵与环境变量

控制台仍拥有资产、manifest、结果校验与剪映登记；Codex App Server / 设置页 UI **不**切换这些后端。选择仅通过环境变量（进程启动时生效），均为 **opt-in**，默认行为不变。

| 任务 | 默认 runtime | Opt-in | 回退 |
|---|---|---|---|
| `montage.execute` / `montage.plan` | `script`（本机 `montage-script-run` + skill Python） | `VIDEO_CONSOLE_MONTAGE_RUNTIME=codex` | script 构建失败 → Codex |
| `remix.*` / `topic.*` | `codex` | `VIDEO_CONSOLE_LLM_RUNTIME=openai_compat` 或 `pi` | 构建失败 → Codex |
| 其他任务 | `codex` | — | — |

环境变量：

| 变量 | 默认 | 说明 |
|---|---|---|
| `VIDEO_CONSOLE_MONTAGE_RUNTIME` | `script` | `script` \| `codex` |
| `VIDEO_CONSOLE_LLM_RUNTIME` | `codex` | `codex` \| `openai_compat` \| `pi` |
| `VIDEO_CONSOLE_OPENAI_BASE_URL` | （空） | `openai_compat` 必填 |
| `VIDEO_CONSOLE_OPENAI_API_KEY` | （空） | `openai_compat` 必填 |

子命令（由 `os.Executable()` 自调用，不改产品 HTTP）：`montage-script-run`、`openai-compat-run`、`pi-run`。`pi` 要求本机 `LookPath("pi")` 可用；不可用时日志回退 Codex。密钥只走环境变量，勿写入仓库或设置页。

## 4. 启动、开发与验证

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

## 5. 运行前置

- Codex CLI 可执行；爆款库 `:2022` 只读代理（可选，不阻塞手动粘贴原文）。
- Obsidian Vault / 选题卡目录、Skills 根、剪映 machine profile 按设置配置。
- 监听：新数据根默认 `127.0.0.1:2030`；已有安装用持久化 `listen_addr`；局域网须显式配置并在 `restart_required` 后重启。
- 无管理员时必须提供 `VIDEO_CONSOLE_INITIAL_PASSWORD`。
- 可选 AgentRuntime：见第 3 节；默认不要求 OpenAI / Pi。
- 混剪还依赖 machine profile / 设置里的 `media_root` + `media_index_path`（见 §6.1）。

## 6. 关键数据流

1. 登录会话 + CSRF；API 鉴权与限速。
2. 项目资产版本化；`source_script` 替换会使依赖下游变 stale。
3. 任务经 manifest 准备后入队；`remix.standard` 可绑定 `source_version_id`；同版本活动任务复用，不同版本 `409 active_remix_conflict`。
4. 完成时校验输入仍为 current，否则 `input_superseded`；`continuous_script` 写入 source 依赖。
5. 事件经 WebSocket 推送；混剪明文 → 登记 → 验证 → ready。

### 6.1 混剪素材选择与预检

符号与行为以本分支 `montageplan` / `task_manifest` 实现为准。

1. **素材来源：**设置或 machine profile 的 `media_root` + `media_index_path`。不是项目资产表，也不是每次扫盘枚举全部文件。
2. **入队前严格预检：**`task_manifest.go` 在 `montage.execute` 准备 manifest 时调用 `montageplan.ValidateMediaLibrary`。内部走 `sampleMedia(..., strict=true)`：索引须为完整 JSON 数组（拒绝截断/尾部脏数据）；ID/相对路径齐全且时长 ≥ 8s 的条目若文件缺失或不是普通文件 → 任务不入队，错误形如 `montage media preflight: ...`。
3. **构建 plan 时非严格取样：**`Build` → `sampleMedia(..., seed=task_id, strict=false)`。坏文件跳过；最终池为空才失败。避免单个坏条目拖垮整次混剪。
4. **池与排序：**优先 `isScenic`（category 含 nature/landscape/scenery/architecture/building）；否则用 fallback。对完整优先池做稳定排序：`SHA-256(task_id + "\0" + id + "\0" + clean(absPath))`，再截取最多 **48** 条（`MediaLimit` 默认）。同 `task_id` 重试顺序稳定；不同任务通常不同开头。
5. **timeline：**按打散顺序轮询；素材不足则循环。前 30 秒约 7 秒一镜，其后约 8 秒一镜。plan JSON 里仍可能出现「前30秒语义匹配」字样——**那是历史文案标签，并非真正的语义镜头理解**。

排障顺序：

1. 点击混剪立即失败 → 看响应/日志里的 `montage media preflight`。
2. 任务已入队后失败 → 看任务目录 `output-last-message.json`、script stderr。
3. 草稿生成但资产未 ready → 看 `draft.validation.json` 与登记重试 API。

## 7. 前端工作台要点

- 阶段：`script → assets → mixing → review → published`；**进审核条件是剪映草稿 ready**，工作台不展示/不要求成片 `final_video`。
- 发布文案：混剪及之后若有 `publishing_package`，展示「视频描述」「短标题」并提供复制；对应视频号发布页粘贴。
- 同行原文入口仅在 `script` 阶段；主动作区分选题卡 `/remix` 与正式 `remix.standard`。
- 主题键 `video-production-console-theme`；项目列折叠阈值 `4`。

## 8. 测试与验收

- Go：`go test ./...`、`go vet ./...`
- 混剪本次改动的聚焦验证（接手请复跑）：
  - `go test ./internal/agentruntime/montageplan ./internal/httpapi -count=1`
  - `go vet ./...`
- 全量 `go test ./...` 在 Windows 上可能仍有与本次混剪无关的路径基线失败（曾观察到 `internal/codex` App Server manifest 路径断言、`internal/conversation` 测试 manifest 路径不存在）。接手时先复跑确认；**勿为绿这些测试去改混剪逻辑**。
- 前端：`npm --prefix web run typecheck|lint|test|test:e2e|build:verify`
- 人工：[acceptance-checklist.md](operations/acceptance-checklist.md)、[montage-registration.md](operations/montage-registration.md)
- 自动测试使用 fake Codex / httptest，不等于外部桌面软件已验收。

## 9. 接手顺序与排障

1. `git status --short`；读本说明与 USER-GUIDE；分清已提交 / 未提交。
2. 确认依赖与数据根，勿清 `video-console-data/`。
3. 先跑低副作用测试，勿先 `build:embed`。
4. 阅读 `cmd/console/main.go` → `internal/app` → httpapi/store → `web/src/App.tsx`；任务后端见 `internal/agentruntime/`；混剪取样见 `montageplan`。
5. 修改限于任务范围；提交/推送须明确指令。

排障摘要：登录看库路径与 CSRF；任务卡住看事件/并发/Codex 或当前 LLM runtime；混剪先看是否被 `montage media preflight` 拒绝，再看任务输出，草稿已生成但未 ready 才优先重试登记；前端空白区分 Vite 与嵌入 dist；项目页刷新 404 检查 SPA 回退是否已构建进当前二进制。

## 10. 工作区保护

- 禁止擅自 `git reset/checkout/restore/stash/clean` 或删除未跟踪的数据库与嵌入 dist。
- `.gitignore` 已忽略 `.tmp/`、`dist/`、`video-console-data/`、日志、exe、Playwright 缓存等。
- 清理运行垃圾时保留：`video-console-data/`、`internal/webui/dist/`、当前正在监听的服务进程所用 exe。

## 11. 已知限制

- 不自动打开微信视频号或剪映；成片导出在剪映侧完成，控制台不托管成片上传。
- 外部依赖离线会影响功能。
- 源码与嵌入 dist 可能暂时不一致；开发页与生产二进制不是同一路径。
- AgentRuntime 切换仅环境变量；设置页 UI 不做 runtime 选择。
- 素材侧当前只是**稳定打散**，不是语义级镜头理解；plan 内「语义匹配」文案不要误读为已实现语义选片。素材池过小或同类高度相似时，成片仍可能视觉重复。
