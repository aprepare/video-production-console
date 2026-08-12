# 项目全景说明（Architecture）

> 读这一份就能建立全局视角：项目在做什么、用什么技术、一条内容怎么从原文走到剪映草稿、每个代码目录是什么、关键机制怎么实现。
>
> 与其他文档的分工：本文是**现状描述**（是什么、怎么实现）；[AI 接手说明](AI-HANDOFF.md) 是**接手备忘**（近期改了什么、下一步做什么、工作区红线）；[使用说明](USER-GUIDE.md) 面向使用者；[优化工单](OPTIMIZATION-BACKLOG.md) 是待办改进。`docs/` 下不再保留历史设计稿与交付快照——那些内容已实现且与现状不符，需要考古请查 git 历史。
>
> 结论都带 `file:line`。行号会随改动漂移，符号名比行号可靠；两者不一致时以代码为准。

---

## 1. 这是什么

一个**本地单机**的视频生产控制台。它把「同行爆款原文 → 二创文案 → 混剪草稿 → 剪映可继续编辑的正式资产」这条流水线固定下来，由 Go 服务托管状态与校验，由 Codex CLI（或本地确定性脚本）承担生成，由浏览器页面做操作界面。

它管什么：

- **鉴权与单管理员会话**：本机口令登录，7 天会话，CSRF 双提交。
- **账号 / 项目 / 资产**：项目按发布账号隔离；资产不可变版本化，上游换版本会把下游标记为 `stale`。
- **任务调度**：把一次生成封装成「任务清单（manifest）→ 排队 → 认领 → 子进程执行 → 结果严格校验 → 资产入库」，全程有阶段计时和事件流。
- **对话工作台**：基于 Codex App Server 的长会话，与正式任务分开。
- **混剪**：本机确定性算法生成 `production_plan.json`，Python skill 造出明文草稿，可信主机把它登记成剪映真实草稿目录，才算 `mix_draft` 资产就绪。

它**不**管什么（这些是设计决定，不是缺口）：

- 不打开微信视频号、不托管成片上传；成片导出在剪映侧完成（`docs/AI-HANDOFF.md:195`）。
- 工作台不出现 `final_video`，进「审核」阶段的条件是剪映草稿 ready。
- AgentRuntime 只能用环境变量切换，设置页**不做** runtime 选择。
- 选片是**确定性稳定打散**，不是语义级镜头理解。计划 JSON 里的「前30秒语义匹配」是历史文案标签（`internal/agentruntime/montageplan/plan.go:173`、`:502-511`），不要误读为已实现语义选片。

---

## 2. 技术栈与运行形态

| 层 | 技术 | 要点 |
|---|---|---|
| 服务 | Go，标准库 `net/http` + Go 1.22 方法路由 | 单进程；根 mux 注册 25 个前缀（`internal/app/app.go:62-160`），各域处理器内部再挂自己的子 mux |
| 存储 | SQLite（`modernc` 驱动路径见 `internal/store/db.go`） | **`SetMaxOpenConns(1)`**（`internal/store/db.go:112`）；写事务统一 `BEGIN IMMEDIATE`；18 个版本化迁移 |
| 前端 | React + TypeScript + Vite | 无路由库，手写 History API；react-query 管服务端数据；`oxlint` + `vitest` + Playwright |
| 交付 | 前端 `dist` 用 `go:embed` 嵌进 exe | `internal/webui/embed.go:10`；未命中静态文件的 GET 回落 `index.html`，SPA 刷新才不 404 |
| 实时 | WebSocket（`github.com/coder/websocket`） | 仅任务事件走 WS；语义事件是轮询 REST |
| 日志 | `log/slog`，JSON handler → stderr | `internal/logging/logging.go:27-29`；级别只由 `VIDEO_CONSOLE_LOG_LEVEL` 控制 |
| 生成侧 | Codex CLI 子进程；或控制台**自调用**子命令 | `montage-script-run` / `openai-compat-run` / `pi-run`，见 §5.2 |

默认监听 `127.0.0.1:2030`（`internal/config/config.go:16`），运行时数据根 `./video-console-data`，权威库 `video-console-data/console.db`。根目录遗留的 `video-console.db` 不是权威库。

关键运行形态含义：**开发页与生产页不是同一条路径**。`npm run dev` 是 Vite HMR；exe 里跑的是嵌入的 `internal/webui/dist`。改了前端但没 `build:embed` 并重建 exe，生产页面不会变。

---

## 3. 一次完整生产流程

### 3.1 业务阶段

看板用 **6 段**：`topic → script → assets → mixing → review → published`（`web/src/projects/stages.ts:5-12`，中文标签 `:18-23`）。
项目详情页的「生产轨」只画后 **5 段**：`script → assets → mixing → review → published`（`web/src/project-workbench/workflow.ts:3-9`）。
文档里出现的「五阶段工作台」指的是后者；看板多一个 `topic` 前置段。阶段流转规则在 `internal/domain/stages.go:36`（`CanMove`）。工作台文案阶段把同行原文收纳为紧凑按钮，点击后才弹出原文与模型强度输入；根容器允许纵向滚动，避免制作状态和项目资产被固定视口裁切。

### 3.2 端到端走一遍

1. **选题（可选）**：`topic.brainstorm` 产出 `topic_candidates` 工件 → 落成 idea session 与 3–5 个候选（`internal/httpapi/ideas.go` 的 `message`）。调用方若已明确选中爆款库正式作品，可在消息请求中传 `source_feed_ids`；控制台会在入队前通过爆款库 `/materials/bundle` 读取完整档案与转写，快照为任务 `engineering_inputs` 中的 `baokuan_source_bundle`。它是可验证的正式来源，任务不再依赖 Codex 运行环境里的 MCP 重取同一作品。`topic.commit` 在 Obsidian Vault 写下选题卡，控制台再把这份**已验证的工件**提升为项目的 `topic_card` 资产（`internal/codex/runner.go:338-355`）。
2. **文案**：上传同行原文成 `source_script` 资产，或用 `topic_card`。当已登记选题卡需要从“候选”升级为“可写稿”时，使用 `POST /api/projects/{id}/topic-card/versions` 上传 Markdown 新版本；接口验证状态前进与交接简报，创建同一逻辑资产的新版本、更新项目卡片路径，并由资产依赖图把旧卡下游标记为 stale。随后发起 `remix.standard` 或 `remix.from_topic_card` → 产出 `continuous_script`（连续文案）资产。
   - 幂等：同项目已有在跑的 remix 时，请求不带 `source_version_id` 或带的是同一个版本 → 返回既有任务 `200`；带的是**不同**版本 → `409 active_remix_conflict`（`internal/httpapi/tasks.go:164-177`）。
   - 改稿有两条路：弹窗内直接改存（版本 +1，下游转 stale）；或 `remix.review` 带 `revision_notes` 让模型重写（`internal/httpapi/tasks.go:191-205`）。
3. **素材**：`media_root` + `media_index_path` 指向本机素材库与索引（不是项目资产表）。发起 `montage.execute` 前会做**入队前严格预检**，素材缺失就根本不入队（§5.4）。
4. **混剪**：`montage.execute` 默认走本机 script runtime：Go 算出 `production_plan.json` → Python skill `validate-plan` → `execute` 造出明文草稿工作区。此时**还没有** `mix_draft` 资产——校验器对两个 montage 动作的 `asset_outputs` 白名单是空集（`internal/codex/result_validator.go:618`），skill 无权自己铸造资产。
5. **登记**：任务完成时走完成门 `Coordinator.HandleCompleted`（`internal/montage/coordinator.go:223`）：校验保留路径 → 入队 → 校验工作区摘要 → `python run_montage_job.py register` 把草稿搬进剪映根 → 逐项验证（回执、`draft_content.json` 三方哈希一致、`root_meta_info.json` 里有匹配条目、目录级哈希且拒绝符号链接，`internal/montage/validator.go:41-133`）→ 同一事务里插入 `mix_draft` 资产版本并把项目推进到 `review`（`internal/store/montage.go:495-559`）。
6. **审核 / 发布**：有 `publishing_package` 时展示「视频描述」「短标题」供复制到视频号发布页。成片导出与上传在剪映和视频号侧，控制台不接。

失败路径都是可恢复的：登记失败/中断可 `POST /api/tasks/{id}/retry-registration` 重试（`internal/httpapi/montage.go:22`），且只从**最新一次** attempt 派生、路径全部取库中留存值（`internal/store/montage.go:179-222`）。

---

## 4. 目录地图

### 4.1 Go 侧

`cmd/`

| 目录 | 职责 |
|---|---|
| `cmd/console` | 服务主程序；同时承载三个自调用子命令（`montage-script-run`/`openai-compat-run`/`pi-run`）。`main()` 在 `cmd/console/main.go:54` |
| `cmd/maintenance` | 独立小工具：SQLite `backup` / `check` / `restore`（`cmd/maintenance/main.go:21`） |

`internal/`

| 目录 | 职责 | 主要符号 |
|---|---|---|
| `app` | HTTP 组装根：一个根 mux + 中间件链；依赖缺失就不注册对应路由 | `New` `app/app.go:60`、`taskRouteHandler` `:183` |
| `config` | 23 行的默认值结构，无 env 读取 | `Default` `config/config.go:14` |
| `httpapi` | 全部 REST 处理器（一域一文件）+ manifest 准备 + 工作流启动 | `NewTaskManifestPreparer` `httpapi/task_manifest.go:65` |
| `domain` | 纯数据模型与状态规则，无 I/O | `CodexTask` `domain/models.go:113`、`CanMove` `domain/stages.go:36`、`EvaluateAction` `domain/requirements.go:49` |
| `store` | SQLite：迁移、备份/恢复、每个聚合一个 Repository | `Open` `store/db.go:35`、`migrate` `store/migrations.go:813`、`runImmediate` `store/tx.go:26` |
| `auth` | 单管理员会话、CSRF、改密、登录限流 | `Service` `auth/service.go:47`、`Middleware.Protect` `auth/middleware.go:34` |
| `security` | 口令哈希、会话密钥、日志脱敏、OS 级密钥保护 | `HashPassword` `security/password.go:12`、`Redactor` `security/redact.go:18` |
| `settings` | 设置的启动初始化、public/secret 分离、runtime 快照、依赖健康、路径策略 | `Service` `settings/service.go:70`、`Runtime` `:334`、`validatePublic` `:453` |
| `codex` | Codex CLI 集成：命令构造、沙箱守卫、调度、事件解析、manifest 与结果信封校验 | `NewScheduler` `codex/scheduler.go:53`、`BuildManifest` `codex/manifest.go:128`、`ValidateResultEnvelopeJSONWithRoots` `codex/result_validator.go:34` |
| `codexapp` | 监管长驻的 Codex App Server 子进程及其 JSON-RPC | `Manager` `codexapp/manager.go:79` |
| `conversation` | App Server 之上的对话：broker/outbox 投递、会话路由、任务↔对话适配 | `Broker` `conversation/broker.go:79`、`TaskAdapter` `conversation/task_adapter.go:22` |
| `agentruntime` | 任务后端抽象与按 env 选路 | `Select` `agentruntime/router.go:46` |
| `agentruntime/montageplan` | 确定性混剪计划：素材扫描/校验、取样、类别打散、时间线、资源配置 | `Build` `plan.go:67`、`ValidateMediaLibrary` `plan.go:422`、`interleaveByCategory` `diversify.go:11`、`scanMediaIndex` `mediascan.go:56` |
| `agentruntime/montagescript` | `montage-script-run` 子命令的编排（validate-inputs → 生成计划 → validate-plan → execute） | `Run` `montagescript/run.go:27` |
| `agentruntime/openaicompat`、`agentruntime/piruntime` | 两个 opt-in 的替代生成后端 | `Run` `openaicompat/run.go:34`、`piruntime/run.go:33` |
| `montage` | 剪映登记：可信运行时解析、登记子进程、验证、恢复与审计 | `Coordinator` `montage/coordinator.go:49`、`ValidateRegisteredDraft` `montage/validator.go:41` |
| `assets` | 资产落盘、体积/类型策略、目录清单、库↔盘对账、在资源管理器打开 | `Service` `assets/service.go:634`、`MaxSizeForType` `:36` |
| `realtime` | 任务事件的 WebSocket 扇出与断线重放 | `Hub` `realtime/hub.go:22`、`Handler` `:141` |
| `progress` | 把原始通知投影成 UI 语义事件（永不回传原始载荷） | `Project` `progress/projector.go:42`、`RedactedRaw` `:74` |
| `timing` | 把通知分类成阶段边界并记录；导入 skill 上报的计时 | `Classify` `timing/classifier.go:48`、`LoadSkillTimings` `timing/importer.go:55` |
| `workflow` | 二创多步工作流编排与任务启动 | `RemixCoordinator` `workflow/remix.go:35` |
| `taskcompletion` | 只有接口、没有实现的契约壳（完成门/观察者） | `Gate` `taskcompletion/gate.go:23` |
| `taskmodel` | 模型名与推理强度的归一化与解析 | `Resolve` `taskmodel/model_selection.go:40` |
| `skillregistry` | 扫描 skill 目录成带哈希的快照并提供「最新一份」 | `ScanAll` `skillregistry/service.go:222`、`Latest` `:241` |
| `history` | App Server 线程历史的读模型 | `Service` `history/service.go:32` |
| `publishing` | 读取并按哈希校验发布包工件 | `Reader.Read` `publishing/package.go:34` |
| `baokuan` | 外部爆款库服务的 HTTP 客户端 | `Client` `baokuan/client.go:38` |
| `obsidian` | 47 行的 Vault 路径校验与健康探测 | `ValidateCardPath` `obsidian/service.go:14` |
| `logging` | slog 初始化 + 请求/任务关联 ID + `RequestID` 中间件 | `Init` `logging/logging.go:20`、`RequestID` `logging/context.go:92` |
| `webui` | 嵌入构建好的 SPA，带 history 回退 | `Handler` `webui/embed.go:13` |
| `buildinfo` | 13 行，承载 ldflags 注入的版本信息 | `String` `buildinfo/buildinfo.go:11` |

### 4.2 前端（`web/src/`）

`App.tsx` 现在 **1086 行**（重构前约 2900），只保留**跨弹窗的全局关注点**：认证与 csrf、主题、当前选中项目、URL 与 History 同步、WebSocket 订阅、模态焦点陷阱与 Escape 分层、任务水合与轮询节奏。其余按目录切开：

| 目录 | 职责 |
|---|---|
| `api` | 唯一 HTTP 出口 `apiRequest`：注入 `X-CSRF-Token`、`credentials: same-origin`、401 回调（`web/src/api/client.ts:6`） |
| `query` | react-query 客户端默认值（`query/client.ts:3`）与集中式 query key 工厂（`query/keys.ts:3`） |
| `auth` | 登录页 |
| `accounts` | 账号切换与新建账号表单 |
| `console` | `useConsoleData`：账号 + 项目两个列表查询，对外仍暴露命令式 `setAccounts`/`setProjects`（写 react-query 缓存） |
| `shell` | `ConsoleHome`：未选中项目时的首页（顶栏 + 账号区 + 按阶段分组的项目看板） |
| `projects` | `useProjectActions`（项目所有写操作，按项目做「单飞」互斥）、`stages.ts`（看板 6 阶段常量与折叠阈值）、新建表单 |
| `project-workbench` | 项目详情页整体：生产轨、素材面板、会话卡、阶段推导 `workflow.ts`、URL 解析 `routes.ts`、自有类型 |
| `tasks` | 任务详情弹窗、任务展示派生逻辑 `task-view.ts`、事件类型 |
| `assets` | 素材预览弹窗、改稿弹窗、素材类型中文标签 |
| `idea` | 选题规划弹窗 + `useIdeaPlanner` |
| `chat` | 对话工作台弹窗 + `useChatWorkbench` |
| `settings` | 设置弹窗 + `useSettingsDialog` |
| `runtime` | `useRuntimeQuery`：7 秒轮询并发额度 |

顶层散文件：`main.tsx`（入口与首屏防闪主题）、`types.ts`（跨模块 API 类型）、`taskModel.ts`、`TaskModelFields.tsx`、`messageTone.ts`、样式。

自定义 hook 一共 6 个：`useConsoleData`、`useIdeaPlanner`、`useChatWorkbench`、`useSettingsDialog`、`useProjectActions`、`useRuntimeQuery`。

### 4.3 其他

`schemas/` 契约文件（注意：**运行时都不校验**，见 §6）、`scripts/` 三个 PowerShell 脚本（§8）、`docs/operations/` 运维与验收清单、`internal/webui/dist/` 嵌入式发布输入（不要删）。

---

## 5. 关键机制

### 5.1 任务生命周期

**任务清单（manifest）是控制台与 skill 之间唯一的输入契约。** `TaskManifest`（`internal/codex/manifest.go:45-59`）`schema_version` 冻结在 `"2.0"`，`job_id` 必须等于 `task_id`。提示词里**不含任何路径**（`internal/codex/prompt.go:104`），路径只在 manifest 里。

盘上布局（根为 `DataRoot`）：

```
<DataRoot>/projects/<projectID>/tasks/<taskID>/task_manifest.json
<DataRoot>/projects/<projectID>/tasks/<taskID>/output/           # output_dir
<DataRoot>/projects/<projectID>/tasks/<taskID>/output/output-last-message.json
<DataRoot>/projects/<projectID>/tasks/<taskID>/output/workspace/<taskID>/   # 混剪明文草稿
<DataRoot>/projects/<projectID>/<asset_type>/                    # 上传的项目资产
```

`output_dir` 的形状被结构性强制为 `projectRoot/tasks/<taskID>/output`（`internal/codex/manifest.go:440-468`）。manifest 写入是原子且**写一次**的：先写 `0o600` 临时文件再 `os.Link`，内容相同视为幂等成功，不同则报错（`internal/codex/manifest.go:391-436`）。

`non_secret_settings` 是一份**扁平白名单**，类型注释明确说明凭据没有可表示的字段（`internal/codex/manifest.go:91-92`）。密钥只走进程环境变量并注册进脱敏器（`internal/codex/runner.go:1170-1184`）；混剪动作还会额外剥掉所有 `GROK_*`（`cmd/console/main.go:801-804`）。浏览器能提交的 manifest 字段只有 `TaskManifestRequest` 那几项（`internal/httpapi/task_manifest.go:28-38`）。

**准备失败必须保持任务未入队**：`prepareAndPublishTask`（`internal/httpapi/task_preparation.go:21`）先准备再发布，准备失败会写一条 `failed` 记录 + 一个 `failed` 的 `task_prepare` 阶段，然后返回 `409 task_manifest_not_ready`。

**队列与认领**。`TaskScheduler`（`internal/codex/scheduler.go:32`）并发被硬夹在 1–4（`:57-59`），**按项目互斥**（无项目的规划任务按任务 ID 加锁，`:103-117`），事件驱动、无轮询定时器。认领是 `ClaimLegacyStart`（`internal/store/tasks.go:1027`）：`BEGIN IMMEDIATE` 内要求 `status=queued AND transport='legacy_exec'`，关掉 `queue_wait` 阶段、开 `codex_execution` 阶段、`UPDATE ... WHERE id=? AND status=?` 并断言影响 1 行，输的一方拿到 `task changed before start`。

三件容易误判的事实：

- **任务认领没有 lease/TTL**。唯一的 TTL 租约是对话 broker 用的 Codex **线程**租约 30s（`internal/conversation/broker.go:25`）。
- **调度器没有重试与退避**。命令构造失败即 `command_build_failed`、启动失败即 `task_start_failed`，都不重排（`internal/codex/scheduler.go:134-156`）。
- **崩溃中断的任务永不自动重排**。启动时 `InterruptInFlight`（`internal/store/tasks.go:329`）把 `running`/`resuming` 置 `interrupted`，注释写明这是为了让人先检查、避免误跑过期工作（`:326-328`）。只有两处例外，是为了不抢走别处拥有的持久重放（inbox/outbox 在途）。

**结果校验**。控制台侧的权威校验是**手写**的 `ValidateResultEnvelopeJSONWithRoots`（`internal/codex/result_validator.go:34`）：拒重复 JSON 键 → 顶层字段集必须精确匹配 → 嵌套类型归一 → `DisallowUnknownFields` 严格解码 → 只能有一个 JSON 对象 → 语义校验。语义层管住了状态机（`awaiting_input` 必须带结构化问题、`asset_outputs` 只在 `completed` 合法）和**路径包含**（工件与资产必须是绝对、规范、位于 `output_dir` 之内；工件不得是符号链接；目录资产含符号链接直接拒）。文件资产的 size/sha256/MIME 都对着字节重新验一遍。

校验不通过走 `persistOutputInvalid`（`internal/codex/runner.go:1048`）：状态 `failed`、`error_code=output_invalid`、**保留原始输出**为 `raw_output_last_message` 工件、不登记任何正式资产。之后可以 `POST /api/tasks/{id}/retry-completion` 只重跑校验、**不重跑模型**（`internal/store/tasks.go:616` 的资格谓词写得很死）。

`input_superseded`：只在 `completed` 分支、在资产入库**之前**检查每个带 `version_id` 的输入是否仍是当前版本（`internal/codex/runner.go:391`、调用点 `:307-309`），所以被顶掉的任务不会写出资产。

**资产入库**在一个事务里完成：`CompleteWithResult`（`internal/store/tasks.go:1368`）把任务状态、工件、资产版本一起提交，任一资产被拒则整体回滚。随后 `SyncStageFromAssets` 重算项目阶段。

**阶段计时**。`task_phase_runs` 表（`internal/store/migrations.go:762`）用 CHECK 约束住 `source`（`host|app_server|skill`）与 `state`，并用索引保证「每 (task, attempt, phase_key) 最多一个 running」。主机侧五个生命周期阶段：`task_prepare` → `queue_wait` → `codex_execution` → `result_validation` → `asset_commit`。前端读 `timing_summary` / `timing_runs`（字段见 `internal/domain/timings.go:24-66`）。

### 5.2 AgentRuntime 路由

选路函数 `Select`（`internal/agentruntime/router.go:46`），两个 opt-in 环境变量：

| 任务 | 实际默认 | Opt-in | 失败行为 |
|---|---|---|---|
| `montage.execute` | `script`（本机确定性计划 + Python skill） | `VIDEO_CONSOLE_MONTAGE_RUNTIME=codex` | 构建失败 → 警告并回落 Codex |
| `remix.*` / `topic.*` | `codex` | `VIDEO_CONSOLE_LLM_RUNTIME=openai_compat` 或 `pi` | 同上 |
| `montage.plan` | **`codex`** | — | — |
| 其他 | `codex` | — | — |

`montage.plan` 那一行值得单独记住：分发处的 `if` 只特判 `ActionMontageExecute`（`cmd/console/main.go:595`），`else` 分支的 switch 又只有 `RuntimeOpenAI`/`RuntimePi` 两个 case，所以**不论怎么设环境变量，`montage.plan` 都会构造 Codex 命令**，尽管 `Select` 对它会返回 `script`。

替代 runtime 都是**控制台自己的子命令**（`os.Executable()` 再入），统一收 `--manifest`/`--skill-root`/`--output-last-message` 并在子进程环境里带 `VIDEO_CONSOLE_TASK_MANIFEST`，因此遵守同一套 manifest/结果契约。失败一律**警告 + 回落 Codex**，不会让任务失败（`cmd/console/main.go:603`、`:612`、`:618`）。

另外 `agentruntime.Runtime` / `LaunchRequest` / `RunHandle` 这组接口是**声明但无实现**的前瞻代码（`internal/agentruntime/runtime.go:48-79`），真正的选路发生在 `codex.CommandFactory` 里，别被接口误导。

### 5.3 实时事件

任务事件走 WebSocket：`GET /api/tasks/{id}/events`（`internal/realtime/hub.go:141`）。Hub 只负责投递与重放，事件的持久化由 runner 完成（`:20-21`）。

- 订阅按任务 ID 分桶（`byTask`，`:22-27`），一任务多客户端。
- 每客户端有单调游标 `lastSequence`，`Sequence <= lastSequence` 的事件直接丢弃（`:119-121`），成功写出后才推进。
- 断线重放靠 `?after=` 查询参数种下游标（`:154`、`:164`），重放是**按游标过滤的全量历史读**（`:175`），没有环形缓冲、没有丢包计数、没有每客户端发送队列。
- 注册时**持有自己的写锁**，让并发 `Publish` 阻塞到重放快照发完为止，从而消掉「注册后持久化但已在快照里」的重复（`:165-170`）。
- 非本机 `Origin` 直接 403（`:150-153`）。

前端只把 WS 消息当「有变化」信号：不解析载荷，300ms 防抖后重取详情（`web/src/App.tsx:416`、`:356-366`），并保留兜底轮询（弹窗打开 5s / 有活跃任务 8s，`web/src/App.tsx:270-275`）。**语义事件不走 WS**，是轮询 `GET /api/tasks/{id}/semantic-events`。

语义事件的投影器 `progress.Project`（`internal/progress/projector.go:42`）只匹配稳定协议词并输出固定中文话术；`RedactedRaw` 无条件返回空串（`:74-83`），所以 `semantic_events.raw_json` 列虽然存在但实际恒为空。runner 里写语义事件的错误是**故意吞掉**的——展示层事件不该打断已持久化权威事件的生产任务（`internal/codex/runner.go:607-609`）。

### 5.4 混剪：取样与登记

**素材来源**：manifest 的 `media_root` + `media_index_path`，缺失时回落 machine profile 的同名字段（`internal/agentruntime/montageplan/plan.go:121-138`）。索引是**顶层 JSON 数组**，流式解码，容忍 BOM，拒绝非数组 / 未闭合 / 尾部脏数据（`mediascan.go:82-127`）。不是项目资产表，也不做全库枚举扫盘。

**入队前严格预检**：`montage.execute` 准备 manifest 时调 `ValidateMediaLibrary`（`internal/httpapi/task_manifest.go:159`），内部 `sampleMedia(..., strict=true)`。合格条目要求 `id`、`relative_path` 非空且 `duration_seconds >= 10`（`mediascan.go:100`；**阈值是字面量 10，没有命名常量**，注释解释是「8s 时间线 @1.1x 需要 8.8s 源」）。合格条目文件缺失或非普通文件，strict 立即报错，错误前缀 `montage media preflight:`，**任务不入队**。

**取样与排序**（`plan.go:372-414`）：

1. 优先池 `isScenic`：category 含 `nature`/`landscape`/`scenery`/`architecture`/`building`（`plan.go:448-453`，刻意排除 `City_Traffic`）。优先池为空才整体启用 fallback 池；**两池不混合**。
2. 稳定排序：按 `sha256(seed + "\x00" + id + "\x00" + Clean(absPath))` 升序（`plan.go:416-419`），seed 为 `task_id`。所以同任务重试顺序稳定、不同任务开头不同。
3. **类别相邻打散** `interleaveByCategory`（`diversify.go:11`）：排序之后、截断之前执行，保证相邻优先不同 category，每轮抽剩余最多的组、平局取首次出现更早者，完全确定性。只有一个 category 时原样返回；尾部只剩单一 category 时整段追加，此时不再保证相邻不同。
4. 截断到 `MediaLimit`，默认 **48**（`plan.go:75-78`）。生产链路没有配置该字段的入口，实际恒为 48。

**扫描缓存**（`mediascan.go:56`）：进程内全局 map + 互斥锁，键是 `(Clean(indexPath), Clean(mediaRoot))`，用索引文件的 `modTime`+`size` 做失效判断。两条正确性保护：strict 提前退出的 partial 扫描**永不发布**；读完后再 `Stat` 一次，size+mtime 与开始时一致才发布。所以缓存条目一定描述整份索引。无 TTL、无容量上限。

**时间线**（`plan.go:455-533`）：镜头默认 8.0s，`cursor < 30` 秒时 7.0s（唯一的 30s 边界，同时决定 `selection_reason` 文案）。素材不足就循环复用。尾段剩余 <1.5s 时不新建镜头，改为延长上一镜并把 `source_out_s` 夹到真实素材长度、重算速度。`fitShotToClip`（`plan.go:536`）保证 `source_in/out`/`speed` 永不超出素材真实时长。

**BGM/SFX**：BGM 单条、无时长规则。SFX（`plan.go:240-313`）在 `duration < 240s` 时只有 1 条（起点 0s）；`>= 240s` 时数量收敛到 **3–5** 条、最小间隔 12s（`sfxMinGapSeconds`）。这两个常量在 `plan.go:17-20`。

**剪映资源 ID 已外置为配置**（`resources.go`）：`montageResources` 结构 + 指针 overlay 合并，默认值仍是原来的硬编码值（转场「叠化」等，`resources.go:69-88`），逐字段覆盖、空值保留默认。设置位置只有 **machine profile JSON**：内联 `montage_resources` 对象，或 `montage_resources_path` 指向独立文件（相对路径相对 profile 目录解析）。**没有对应的设置页字段**。

**登记**的可信性由三层保证：路径绑定校验（manifest 必须等于库里记录的 `manifest_path`、canonical 且不跟随符号链接、`task_id == job_id == taskID`，`coordinator.go:357-396`）；运行时指纹（skill 快照路径、`run_montage_job.py` 指纹、machine profile 的 SHA-256 pin，`coordinator.go:657-726`）；结果验证（`validator.go:41-133`，见 §3.2 第 5 步）。登记子进程的回执路径由 Go 侧**预先推导**，不信任子进程返回的路径（`registrar.go:114-119`）。

### 5.5 资产版本与 stale 传播

两张表：`asset_items`（逻辑资产，持有 `current_version_id`）与 `asset_versions`（不可变版本）。逻辑身份是 `(project_id, account_id, type)`；版本号是该逻辑资产内 `MAX(version)+1`；新版本插入即 `ready` 并立刻成为 current（`internal/store/assets_v2.go:141-208`）。

失效图在 `internal/domain/assets.go:81-89`（例：`source_script` → `continuous_script`/`narration`/`subtitle_srt`/`mix_draft`/`final_video`）。**机制**是：新版本顶替旧 current 时，一条递归 CTE 沿 `asset_dependencies` 传递地把受影响且仍是 current 的行置 `stale`，理由写成 `upstream version replaced: <oldVersionID>`（`internal/store/assets_v2.go:211-219`）。而那些依赖边是任务完成时由 `manifestDependencies`（`internal/codex/runner.go:415`）依据 manifest 输入算出来的。

第二条 stale 路径专属混剪：启动审计发现已登记的剪映草稿不再校验通过时，把 `mix_draft` 版本置 stale（`internal/store/montage.go:640`）。

消费侧闭环：manifest 的每个输入都必须是 `ready`，所以 stale 资产会直接挡住新任务的创建（`internal/httpapi/task_manifest.go:413`）。

### 5.6 鉴权、CSRF、限流、回环

- **会话**：单管理员，登录后 7 天有效（`internal/auth/service.go:27`），库里只存 token/CSRF 的 SHA-256（`internal/security/session.go:12-24`）。会话有效性还要求 `created_at >= password_changed_at`，所以改密自动作废旧会话。口令是 SHA-256 预摘要 + bcrypt。
- **中间件顺序**（外 → 内，`internal/app/app.go:157-170`）：`logging.RequestID` 包住**整个** mux（含静态资源）→ 路径白名单（`/api/health`、`/api/auth/*`、非 `/api/` 路径直通）→ `Protect`（会话认证，**CSRF 检查在其内部**，`internal/auth/middleware.go:50-53`）。
- **CSRF**：双提交 cookie + `X-CSRF-Token` 头，两者都要与库里的 hash 常量时间相等；保护除 GET/HEAD/OPTIONS 外的所有方法。
- **限流只有登录一处**：进程内内存表，键是 remote host（**不认 `X-Forwarded-For`**），10 分钟滑窗、5 次失败即锁 15 分钟，响应 `429` + `Retry-After: 900`（`internal/auth/service.go:237-320`、`internal/httpapi/auth.go:57-60`）。重启即清零，条目不回收。
- **回环限定只有一个端点**：`POST /api/assets/{id}/open-directory`（在资源管理器打开剪映草稿目录）。判定要求服务端监听地址、客户端地址、`Host` 三者都是回环，且 `Origin` 非空、`Sec-Fetch-Site: same-origin`、`Origin` 的 scheme 与 TLS 状态一致等一整组条件（`internal/httpapi/assets.go:216-238`）。目标目录还必须是由**成功 attempt** 登记过的、位于可信剪映根内的草稿。

### 5.7 设置与 machine profile

设置是 `settings` 表里的 key/value，写入走 **20 个键的白名单**（`internal/store/settings.go:24-32`）；密钥单独存在 `encrypted_secrets`，用 OS 保护（Windows 上 DPAPI）后 base64。一次更新里 public 与 secrets 在同一个 `BEGIN IMMEDIATE` 提交（`internal/store/settings.go:184-255`）。

校验全部是手写 Go（`internal/settings/service.go:453-517`），包含几条有安全含义的约束：并发 1–4；路径必须是规范绝对路径且不经符号链接别名；`topic_cards_dir ⊂ obsidian_vault`、`media_index_path ⊂ media_root`；`baokuan_base_url` 只允许回环主机。

**密钥永不回传**：GET 只给 `{configured, masked}`，`masked` 是常量 `********`；解密值只存在于 `settings.Runtime`，其密钥字段带 `json:"-"`（`internal/settings/service.go:104-111`）。PUT 时空字符串表示「不改」。

**`restart_required`** 的原理是「配置态 vs 生效态」对比：服务缓存首次读到的 `Runtime` 快照，`Get` 时把两个可热更字段（`max_codex_concurrency`、`codex_history_limit`）清零后 `reflect.DeepEqual`，再比对密钥版本号（`internal/settings/service.go:402-410`）。热更字段直接就地生效并立刻推给调度器（`internal/app/app.go:215-224`）。

**machine profile** 是一个设置键 `machine_profile_path` 指向的本机 JSON，声明 `python_binary` 与 `jianying_root`（也可承载混剪资源覆盖，见 §5.4）。启动时它为空会**整体禁用混剪登记**（`cmd/console/main.go:188`、`:216`）；否则解析成 `TrustedRuntime` 并**固定其 SHA-256**，之后每个任务在登记前都要比对路径与哈希（`internal/montage/coordinator.go:75-120`、`:492-503`）。

### 5.8 存储与迁移

- `store.Open` → 单连接、`foreign_keys=ON`、`busy_timeout=5000`，先校验迁移历史（版本必须是连续的 `1..N`，否则拒绝启动），对已存在的库在升级前自动备份，然后 `migrate`（`internal/store/db.go:96-161`）。
- 迁移是**版本化**的（`schema_migrations` 表），整轮在一个 `BEGIN IMMEDIATE` 内、期间关闭外键并在 `defer` 里恢复，提交前跑 `PRAGMA foreign_key_check`（`internal/store/migrations.go:837-911`）。当前 18 个迁移。
- 写事务统一走 `runImmediate`（`internal/store/tx.go:26`）：独占连接 + `BEGIN IMMEDIATE`；`fn` 出错则 `ROLLBACK`；**COMMIT 失败不再尝试 ROLLBACK**（可能已提交），返回 `CommitUnknown` 并把事务状态不明的连接用 `driver.ErrBadConn` 踢出连接池。
- 主要表：`accounts`/`projects`/`codex_tasks`/`task_events`/`task_messages`/`asset_items`/`asset_versions`/`asset_dependencies`/`task_artifacts`/`task_phase_runs`/`semantic_events`/`chat_*`/`idea_*`/`montage_registration_attempts`/`project_workflow_runs`/`project_step_notes`/`settings`/`encrypted_secrets`/`admins`/`auth_sessions`/`skill_snapshots`/`thread_leases`。`assets`（v1）在 v2 之后只当只读审计表。

### 5.9 结构化日志

`logging.Init` 是 `main` 的第一句（`cmd/console/main.go:55`），JSON handler 写 stderr，`slog.SetDefault` 同时把标准库默认 logger 也接进同一条流（`internal/logging/logging.go:16-29`）。级别只认 `VIDEO_CONSOLE_LOG_LEVEL`（`debug|warn|error`，其他一律 info），**没有设置项或命令行开关**。

- `request_id`：`logging.RequestID` 中间件（`internal/logging/context.go:92`）复用入站 `X-Request-Id`（经严格净化：≤128 字节、全部可打印 ASCII，挡住头注入），否则生成 UUID；**并回写同名响应头**；同时把带 `request_id` 的 logger 放进 context，处理器用 `logging.LoggerFrom(r.Context())` 取。
- `task_id`：后台路径用 `logging.TaskLogger(taskID)`（`internal/montage/coordinator.go:524`），其余按调用点显式加 `"task_id"` 属性。注意 `logging.WithTaskID`/`TaskIDFrom` 虽然导出，但生产代码没有调用者。
- 常用属性键：`error`、`task_id`、`request_id`、`phase`、`action`、`attempt_id`、`code`、`account_id`、`project_id`、`listen_addr`。消息用小写短语，失败记录一律以 `"error", err` 收尾。
- 标准库 `log` 只剩两处，都是**有意的桥**：`logging.StdLogger` 为仍需 `*log.Logger` 的依赖提供适配（输出仍进 JSON 流）；`internal/assets/service.go` 的两个对账函数收 `*log.Logger` 参数（`main` 传的是 warn 级 `StdLogger`）——这几行是非结构化的 `Printf`，没有 `project_id` 之类属性，属于已知的粗糙点。启动期致命错误用本地 `fatal` 助手（`slog.Error` + `os.Exit(1)`，`cmd/console/main.go:272`）。

---

## 6. 契约现状（重要且反直觉）

`schemas/` 下有四个文件，**没有任何一个在运行时被 JSON Schema 引擎求值**——`go.mod` 里根本没有 schema 校验库。它们是文档级契约，由手写 Go 校验器镜像实现：

| 文件 | 谁在用 | 运行时是否生效 |
|---|---|---|
| `task-manifest.schema.json` | 只被 `internal/codex/manifest_test.go:433` 等测试读取 | 否；运行时是 `internal/codex/manifest.go` 的手写校验 |
| `codex-result.schema.json` | 只被测试读取 | 否，**而且没有下发给 Codex**：`Config.ResultSchema` 控制 `--output-schema` 参数，但生产配置从不设置它（`cmd/console/main.go:142`）。注释解释原因：本地代理拒绝 Codex 的高级 JSON Schema 方言，改由控制台自己严格校验（`cmd/console/main.go:140-141`） |
| `topic-candidates.schema.json` | 只被 `internal/codex/result_validator_test.go:511` 读取 | 否；运行时是 `internal/codex/topic_candidates.go:41-60` 的严格解码 |
| `settings.schema.json` | 只被 `schemas/settings_schema_test.go:10` 读取 | 否；运行时是 `internal/settings/service.go:453` 的手写校验 |

`resolveResultSchemaPath`（`cmd/console/main.go:403`）与 `scripts/release.ps1:25` 打包 `schemas/` 的动作都还在，但既然没人在生产路径调用，**打进发布包的 schema 目前是惰性载荷**。

**Go 响应结构 ↔ 前端 TS 类型：两边都是手写，没有代码生成**，耦合仅靠 json tag 与字面量键名对齐。现在有一道守卫：`internal/httpapi/contract_types_test.go` 把 6 组 Go↔TS 类型配对，断言前端声明的每个字段都能在 Go json tag 里找到，并拒绝任何 PascalCase 键（未打 tag 的导出字段会以 Go 字段名进入键集，因此裸 struct 当响应发也会被抓住）。加类型时**请把配对补进那张表**。风险与选型方案记录在 [P2-2a 审计](audits/2026-08-12-p2-2a-json-tag-audit.md) 和 [优化工单 P2-5](OPTIMIZATION-BACKLOG.md)。已知两处真实漂移：

- Go `PublicSettings` 有 20 个字段含 `codex_task_project_root`（`internal/domain/settings.go:24`），TS 侧 19 个、**缺这一项**（`web/src/types.ts:65-85`）。因为设置 PUT 提交的是整个 `public` 对象，类型系统抓不到。
- `remix.review`：Go 校验器允许 `continuous_script` 作为资产输出（`internal/codex/result_validator.go:617`），而 schema 把 `asset_outputs` 钉成 `maxItems: 0`（`schemas/codex-result.schema.json:85`）。

漂移能长期活着的原因值得记一笔：`POST /api/ideas/{id}/select` 曾把裸 `domain.Project`（无 json tag，键名 PascalCase）当响应发出，而前端按 `project.id` / `project.account_id` 读，全是 `undefined`；已修为 `toProjectView`（`internal/httpapi/ideas.go:254`）。**用反序列化写的契约测试抓不到这类回归**——`encoding/json` 匹配对象键时大小写不敏感，`"Stage"` 一样能填进带 `json:"stage"` 的字段。断言响应形状时要断言原始键名（`internal/httpapi/ideas_test.go:315-334` 是范例）。

---

## 7. 配置与环境变量

`internal/config/config.go` 只有 6 个编译期默认值，**不读任何环境变量**；它们启动时被写进 `settings` 表，之后一律以库里的值为准。

| 值 | 默认 | 覆盖方式 |
|---|---|---|
| `ListenAddr` | `127.0.0.1:2030` | 设置键 `listen_addr`（改后需重启） |
| `DataRoot` | `./video-console-data` | 启动时先解析成安装目录级绝对路径，再由设置键 `data_root` 覆盖 |
| `DatabasePath` | `<DataRoot>/console.db` | **不是设置键**，恒等于 `DataRoot` 拼出来的路径 |
| `BaokuanBaseURL` | `http://127.0.0.1:2022` | 设置键 `baokuan_base_url`（只允许回环） |
| `CodexBinaryPath` | `codex`（裸名） | 启动 `LookPath` + `Abs` 解析，再由设置键覆盖；失效路径会自修复 |
| `ObsidianVault` | 空 | 设置键 `obsidian_vault` |
| `max_codex_concurrency` | `2` | 设置键，1–4，**可热更** |
| `codex_history_limit` | `10` | 设置键，5–50，**可热更** |
| `codex_default_model` / `..._reasoning_effort` | `gpt-5.6-sol` / `medium` | 设置键，可热更 |

环境变量（进程启动时生效）：

| 变量 | 默认 | 说明 |
|---|---|---|
| `VIDEO_CONSOLE_INITIAL_PASSWORD` | — | **仅首次安装**（尚无管理员）时必须提供，否则启动失败 |
| `VIDEO_CONSOLE_LOG_LEVEL` | `info` | `debug`\|`warn`\|`error`，其他值回落 info |
| `VIDEO_CONSOLE_MONTAGE_RUNTIME` | `script` | `script`\|`codex` |
| `VIDEO_CONSOLE_LLM_RUNTIME` | `codex` | `codex`\|`openai_compat`\|`pi` |
| `VIDEO_CONSOLE_OPENAI_BASE_URL` / `_API_KEY` | 空 | `openai_compat` 必填，缺任一即回落 Codex |
| `CODEX_DESKTOP_CWD` | `~/Documents/视频号混剪`（存在时） | 桌面对话的工作目录 |

转发给 Codex 子进程的密钥：`GROK_SEARCH_BASE_URL`、`GROK_SEARCH_MODEL`、`GROK_SEARCH_API_KEY`、`PEXELS_API_KEY`（`cmd/console/main.go:44-49`），库里存的密钥优先于进程环境。

不可配置的硬编码值（需要时改代码）：HTTP 超时（读头 10s、读写 2m、空闲 1m）、优雅关闭 20s、会话 7 天、依赖探测 5s、登记队列深度 32 / 入队等待 30s / 改名重试 3 次。

---

## 8. 构建、验证、发布

PowerShell 环境，不要用 bash 的 `&&`。

```powershell
codex --version
go run .\cmd\console
```

前端开发：`Set-Location web; npm install; npm run dev`（仅 HMR，生产走嵌入 dist）。

**日常验证**（不覆盖嵌入资源）：

```powershell
.\scripts\verify-baseline.ps1
```

它依次跑 `go test ./...` → `go vet ./...` → 前端 lint / 单测 / Playwright e2e → 一次性 `build:verify`（输出到 `.tmp/web-dist`）→ 嵌入产物检查。`-StrictEmbeddedDist` 会让未跟踪的嵌入资源变成致命错误。

前端脚本与产物目录（`web/package.json:6-16`）：

| 脚本 | 作用 | 输出 |
|---|---|---|
| `typecheck` | `tsc -b` | — |
| `lint` | `oxlint` | — |
| `test` | `vitest run`（排除 `e2e/**`） | — |
| `test:e2e` | Playwright，自动起 dev server 到 `127.0.0.1:4173` | — |
| `build:verify` | typecheck + 构建 | `../.tmp/web-dist` |
| `build:embed` | typecheck + 构建 | **`../internal/webui/dist`**（`emptyOutDir: true`） |

**只在明确要同步生产页面时**才 `build:embed`，随后 `.\scripts\check-embedded-dist.ps1 -Strict`（它解析 `index.html` 里的每个根相对引用，断言文件存在且被 git 跟踪——未跟踪的资源在打包版里会变成 404）。

正式发布：`.\scripts\release.ps1 -Version x.y.z`。它要求 semver、拒绝覆盖已有目录/压缩包，用 `-ldflags` 打进版本/commit/构建时间并 `-trimpath` 构建两个 exe，然后跑 `--version` 自校验、写 `SHA256SUMS.txt`、打包并生成 `.sha256` 侧车文件。

人工验收：[acceptance-checklist.md](operations/acceptance-checklist.md)、[montage-registration.md](operations/montage-registration.md)。自动测试用 fake Codex / httptest，**不等于外部桌面软件已验收**。

---

## 9. 排障入口

| 现象 | 先看哪里 |
|---|---|
| 登录失败 | 库路径、CSRF 头/cookie 是否都在；连续失败 5 次会锁 15 分钟 |
| 点混剪立即失败 | 响应或日志里的 `montage media preflight`（素材缺失/索引截断/时长 <10s） |
| 任务入队后失败 | 任务目录的 `output-last-message.json`、script stderr；`error_code=output_invalid` 说明结果信封没过校验，原始输出保留在 `raw_output_last_message` 工件里 |
| 草稿生成但资产未 ready | `draft.validation.json`，然后用 `POST /api/tasks/{id}/retry-registration` 重试登记（**不要先改 runtime**） |
| 任务卡住 | 事件流 / 并发额度 / 当前 runtime；注意崩溃重启后的任务是 `interrupted`，设计上**不会**自动重排 |
| 前端空白 | 区分 Vite 开发页与嵌入 dist |
| 项目页刷新 404 | SPA 回退是否已构建进当前二进制 |
| 改了前端但页面没变 | 是否只跑了 `dev`／忘了 `build:embed` + 重建 exe |

---

## 10. 工作区红线

- 禁止擅自 `git reset/checkout/restore/stash/clean`，禁止删除未跟踪的数据库与嵌入 dist。
- 清理运行垃圾时保留：`video-console-data/`、`internal/webui/dist/`、当前正在监听的服务进程所用 exe。
- `.gitignore` 已忽略 `.tmp/`、`dist/`、`video-console-data/`、日志、exe、Playwright 缓存等。
