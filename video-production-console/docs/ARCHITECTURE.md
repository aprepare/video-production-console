# 项目全景说明（Architecture）

> 读这一份就能建立全局视角：项目在做什么、用什么技术、一条内容怎么从原文走到剪映草稿、每个代码目录是什么、关键机制怎么实现。可交互架构图：[runtime.architecture.html](archify/runtime.architecture.html)（源 JSON 同目录）。
>
> 与其他文档的分工：本文是**现状描述**（是什么、怎么实现）；[AI 接手说明](AI-HANDOFF.md) 是**接手备忘**（2026-08-15 进度、下一步、红线）；[使用说明](USER-GUIDE.md) 面向使用者；[优化工单](OPTIMIZATION-BACKLOG.md) 是待办改进。`docs/superpowers/` 历史设计稿已删除——内容已实现或会误导，考古走 git 历史。
>
> 结论都带 `file:line`。行号会随改动漂移，符号名比行号可靠；两者不一致时以代码为准。

---

## 1. 这是什么

一个**本地单机**的视频生产控制台，包含两条互相隔离的生产线：

- **混剪模式**：把「同行爆款原文 → 二创文案 → 混剪草稿 → 剪映可继续编辑的正式资产」固定为项目流水线；
- **图文模式**：直接接收已经定稿的完整文案，不进行二创。默认入口 `/image-projects` 是一键生成：粘贴文案后由文本模型生成项目名、分段、提示词和 5 条发布候选，再按项目并发生图。`/image-projects/advanced` 保留原「分段建议 → 确认 → 提示词」手动流程。详情地址 `/image-projects/{id}` 刷新只恢复该项目，不回首页。

Go 服务统一托管鉴权、设置和 SQLite 状态；两条线使用独立的领域表、接口和页面状态，图文卡片不会进入混剪项目、素材资产或剪映草稿状态机。

它管什么：

- **鉴权与单管理员会话**：本机口令登录，7 天会话，CSRF 双提交。
- **账号 / 项目 / 资产**：项目按发布账号隔离；资产不可变版本化，上游换版本会把下游标记为 `stale`。
- **任务调度**：把一次生成封装成「任务清单（manifest）→ 排队 → 认领 → 子进程执行 → 结果严格校验 → 资产入库」，全程有阶段计时和事件流。
- **对话工作台**：基于 Codex App Server 的长会话，与正式任务分开。
- **混剪**：本机确定性算法生成 `production_plan.json`，Python skill 造出明文草稿，可信主机把它登记成剪映真实草稿目录，才算 `mix_draft` 资产就绪。
- **图文生图**：`image_projects` / `image_project_items` 保存原文、顺序、封面/内容角色、提示词、运行检查点、每项尝试次数与输出位置；`internal/imageproject` 负责一键规划、校验分段覆盖原文、生成提示词和 OpenAI 兼容图片请求（总尝试次数 1–4，多 URL 只取第一张）；`internal/httpapi/imageprojects.go` 与 `imageproject_quick.go` 负责一键 202 编排、resume、分段预览、确认创建、生成、预览、重生成、删除和 ZIP 清单。进程内 job guard 保证同一项目不同时跑两份编排；服务重启把遗留 `running` 一键任务标为 `interrupted`，需用户点「继续生成」。
- **素材库与自动检索**：`media_root/catalog.db` 是镜头元数据权威库。控制台「开始建库」与 `catalog-builder` 都调用 `mediacatalog.RunHostedBuild`（扫描 → 切镜抽帧 → 打标向量）。混剪任务在 `montage-script-run` 里注入 Embedder/IntentAnalyzer，`BuildV2` 从 catalog 四级召回后再由确定性 planner 拍板。风景和电影都按口播选片。没有 `ready_shots` 时风景任务回退 `media_index.json` 风景打散，电影任务直接失败。

它**不**管什么（这些是设计决定，不是缺口）：

- 不打开微信视频号、不托管成片上传；成片导出在剪映侧完成。
- 工作台不出现 `final_video`，进「审核」阶段的条件是剪映草稿 ready。
- 设置页**不做** runtime 下拉；二创三字段已经决定 remix 走 OpenAI 兼容接口。
- v1 计划 JSON 里的「前30秒语义匹配」是历史文案标签，不是真语义选片。v2 才按 catalog 标签/向量召回，最终选择仍是确定性算法。

---

## 2. 技术栈与运行形态

| 层 | 技术 | 要点 |
|---|---|---|
| 服务 | Go，标准库 `net/http` + Go 1.22 方法路由 | 单进程；根 mux 注册 25 个前缀（`internal/app/app.go:62-160`），各域处理器内部再挂自己的子 mux |
| 存储 | SQLite（`modernc` 驱动路径见 `internal/store/db.go`） | **`SetMaxOpenConns(1)`**（`internal/store/db.go:112`）；写事务统一 `BEGIN IMMEDIATE`；19 个版本化迁移 |
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

看板与工作台都是 **5 段**：`script → assets → mixing → review → published`（`web/src/projects/stages.ts:5-11`，`web/src/project-workbench/workflow.ts:3-9`）。后端若仍返回遗留 `topic`，看板把它映射成 `script`（`boardStage`）。阶段流转规则在 `internal/domain/stages.go`（`CanMove`）。工作台文案阶段把同行原文收纳为紧凑按钮；根容器允许纵向滚动。首页 `/` 是制作方式选择（`web/src/production-modes/`），不再是直接看板。

### 3.2 端到端走一遍（日产主路径）

日常主路径**不经过选题**：

1. **文案**：工作台粘贴同行原文 → 保存为 `source_script` → `remix.standard` 走 `openai-compat-run`（`internal/agentruntime/openaicompat`）→ 产出 `continuous_script` + `publishing_package.json`。
   - 幂等：同项目已有在跑的 remix 时，请求不带 `source_version_id` 或带的是同一个版本 → 返回既有任务 `200`；带的是**不同**版本 → `409 active_remix_conflict`（`internal/httpapi/tasks.go`）。
   - 改稿：弹窗内直接改存（版本 +1，下游转 stale）；或 `remix.review` 带 `revision_notes` 让模型重写。
2. **配音字幕**：`POST /api/projects/{id}/narration` 调火山 TTS，成对登记 `narration` + 词级 `subtitle_srt`。手动上传走 `POST /api/projects/{id}/assets/narration`，两条路由不能抢占（§5.6）。
3. **素材**：本机 `media_root` + `catalog.db`（权威检索库）以及可选的 `media_index.json`（风景线降级）。发起 `montage.execute` 前做入队预检（§5.4）。建库后混剪**自动从 catalog 检索**，不必手工选片。
4. **混剪**：`montage.execute` 默认走本机 script runtime：Go 写出 `production_plan.json` → Python skill `validate-plan` → `execute` 造明文草稿工作区。此时**还没有** `mix_draft` 资产——校验器对 montage 动作的 `asset_outputs` 白名单是空集，skill 无权自己铸造资产。
5. **登记**：任务完成时走完成门 `Coordinator.HandleCompleted`（`internal/montage/coordinator.go`）：校验保留路径 → 入队 → 校验工作区摘要 → `python run_montage_job.py register` 把草稿搬进剪映根 → 逐项验证（回执、`draft_content.json` 三方哈希一致、`root_meta_info.json` 有匹配条目、目录级哈希且拒绝符号链接）→ 同一事务插入 `mix_draft` 并把项目推进到 `review`。
6. **审核 / 发布**：有 `publishing_package` 时展示「视频描述」「短标题」供复制。成片导出与上传在剪映和视频号侧，控制台不接。可「重做混剪」（再发一条 `montage.execute`，不删旧草稿）。

失败路径可恢复：登记失败/中断可 `POST /api/tasks/{id}/retry-registration`，只从**最新一次** attempt 派生。

**遗留选题路径**（界面已卸，`web/src/idea/` 未挂载；后端 `/api/ideas` 仍在）：`topic.brainstorm` → idea session → `topic.commit` 写 Obsidian 选题卡 → `topic_card` 资产。openai_compat 二创只认 `source_script`，不要把选题当主路径接回。

---

## 4. 目录地图

### 4.1 Go 侧

`cmd/`

| 目录 | 职责 |
|---|---|
| `cmd/console` | 服务主程序；同时承载三个自调用子命令（`montage-script-run`/`openai-compat-run`/`pi-run`）。`main()` 在 `cmd/console/main.go` |
| `cmd/maintenance` | 独立小工具：SQLite `backup` / `check` / `restore` |
| `cmd/catalog-builder` | 云机/本机建库小站：完整三步建库、下载结果包、按 SHA-256 合并（默认 `127.0.0.1:2031`）。不读 `console.db` |

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
| `agentruntime/montageplan` | 确定性混剪计划：v1 打散取样与 v2 配额/召回时间线 | `Build`、`BuildV2`、`ValidateMediaLibrary` |
| `agentruntime/montagescript` | `montage-script-run`：按冻结的 `montage_plan_version` 选 `Build`/`BuildV2`，并注入 catalog 客户端 | `Run`、`attachCatalogClients` |
| `mediacatalog` | 独立 `catalog.db`：扫描、切镜抽帧、打标向量、召回、导入、权利元数据 | `RunHostedBuild`、`CompatEndpoint`、`RecallByTags`、`Importer` |
| `catalogbuilder` | 独立建库小站：包装 `RunHostedBuild`、结果包、按哈希合并 | `Build`、`ExportPack`、`MergePack`、`NewServer` |
| `agentruntime/openaicompat` | 二创主路径：OpenAI 兼容流式写稿 | `Run`、`buildWriterPrompt` |
| `agentruntime/piruntime` | 可选 Pi 后端 | `Run` |
| `imageproject` | 图文分段、提示词、生图客户端 | `SuggestSegments`、`SuggestPrompts`、`Generator` |
| `narration` | 火山 TTS、词级 SRT、音色 | `produce.go`、`volcengine.go`、`srt.go` |
| `montage` | 剪映登记：可信运行时解析、登记子进程、验证、恢复与审计 | `Coordinator` `montage/coordinator.go:49`、`ValidateRegisteredDraft` `montage/validator.go:41` |
| `assets` | 资产落盘、体积/类型策略、目录清单、库↔盘对账、在资源管理器打开 | `Service` `assets/service.go:634`、`MaxSizeForType` `:36` |
| `realtime` | 任务事件的 WebSocket 扇出与断线重放 | `Hub` `realtime/hub.go:22`、`Handler` `:141` |
| `progress` | 把原始通知投影成 UI 语义事件（永不回传原始载荷） | `Project` `progress/projector.go:42`、`RedactedRaw` `:74` |
| `timing` | 把通知分类成阶段边界并记录；导入 skill 上报的计时 | `Classify` `timing/classifier.go:48`、`LoadSkillTimings` `timing/importer.go:55` |
| `workflow` | 二创多步工作流编排与任务启动 | `RemixCoordinator` `workflow/remix.go:35` |
| `taskcompletion` | 只有接口、没有实现的契约壳（完成门/观察者） | `Gate` `taskcompletion/gate.go:23` |
| `taskmodel` | 模型名与推理强度的归一化与解析 | `Resolve` `taskmodel/model_selection.go:40` |
| `skillregistry` | 扫描 skill 目录成带哈希的快照；按冻结 `assets/capabilities.json` 决定计划版本 | `ScanAll`、`DecideMontagePlanVersion` |
| `history` | App Server 线程历史的读模型 | `Service` `history/service.go:32` |
| `publishing` | 读取并按哈希校验发布包工件 | `Reader.Read` `publishing/package.go:34` |
| `baokuan` | 外部爆款库服务的 HTTP 客户端 | `Client` `baokuan/client.go:38` |
| `obsidian` | 47 行的 Vault 路径校验与健康探测 | `ValidateCardPath` `obsidian/service.go:14` |
| `logging` | slog 初始化 + 请求/任务关联 ID + `RequestID` 中间件 | `Init` `logging/logging.go:20`、`RequestID` `logging/context.go:92` |
| `webui` | 嵌入构建好的 SPA，带 history 回退 | `Handler` `webui/embed.go:13` |
| `buildinfo` | 13 行，承载 ldflags 注入的版本信息 | `String` `buildinfo/buildinfo.go:11` |

### 4.2 前端（`web/src/`）

`App.tsx` 只保留**跨弹窗的全局关注点**：认证与 csrf、主题、当前选中项目、URL 与 History 同步、WebSocket 订阅、模态焦点陷阱与 Escape 分层、任务水合与轮询节奏。其余按目录切开：

| 目录 | 职责 |
|---|---|
| `api` | 唯一 HTTP 出口 `apiRequest`：注入 `X-CSRF-Token`、`credentials: same-origin`、401 回调 |
| `query` | react-query 客户端与 query key 工厂 |
| `auth` | 登录页 |
| `accounts` | 账号切换与新建账号表单 |
| `console` | `useConsoleData`：账号 + 项目列表 |
| `production-modes` | `/` 制作方式首页：混剪 / 电影混剪 / 图文（后两个混剪卡片目前同进 `/projects`） |
| `shell` | `ConsoleHome`：`/projects` 看板（顶栏 + 账号 + 五阶段列） |
| `projects` | `useProjectActions`、五阶段常量、新建表单 |
| `project-workbench` | 混剪项目详情：生产轨、资产、`workflow.ts`、`routes.ts` |
| `image-mode` | 图文一键表单、详情、发布编辑台 |
| `media-library` | 素材库建库 / 外部图库导入面板（无独立 URL） |
| `tasks` | 任务详情弹窗、`task-view.ts` |
| `assets` | 素材预览、改稿弹窗 |
| `idea` | **未挂载**。文件还在，`App.tsx` 不引用 |
| `settings` | 设置弹窗（二创 / Codex / 图文 / 素材库 / 密钥） |
| `runtime` | `useRuntimeQuery`：并发额度轮询 |

顶层散文件：`main.tsx`、`types.ts`、`taskModel.ts`、`TaskModelFields.tsx`、`RemixPromptStyleFields.tsx`、`messageTone.ts`、样式。

自定义 hook：`useConsoleData`、`useSettingsDialog`、`useProjectActions`、`useRuntimeQuery`。`useIdeaPlanner` 存在但未接线。

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
| `remix.*` / `topic.*` | **硬切 `openai_compat`**（设置了二创/Grok 地址+密钥时） | `VIDEO_CONSOLE_LLM_RUNTIME=pi` 可改 Pi；未配地址则任务直接失败，**不回退 Codex** | 见 `cmd/console/main.go` `makeCommand` |
| `montage.plan` | **`codex`**（与 `Select` 声明不一致） | — | `CommandFactory` 只特判 `montage.execute` |
| 其他 | `codex` | — | — |

`montage.plan`：`Select` 对它返回 `script`，但 `CommandFactory` 的 `if` 只特判 `ActionMontageExecute`，所以它仍构造 Codex 命令。

`remix.*` 不再是「默认 Codex、env opt-in」。`makeCommand` 对 LLM action 直接进 `openai-compat-run`；`LLMRuntimePreferred` 只用来在 openai 与 pi 之间选。设置页「二创服务地址 / 二创模型 / 二创 API 密钥」写入 `REMIX_*`。

替代 runtime 都是控制台自调用子命令，遵守同一套 manifest/结果契约。`montage.execute` 的 script 构建失败仍会警告并回落 Codex；remix 构建失败则返回错误。

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

**素材来源**：v1 仍读 manifest 的 `media_root` + `media_index_path`。v2 使用 `media_catalog_path` 指向的 `catalog.db`：`montagescript.attachCatalogClients` 从 manifest + `VIDEO_CONSOLE_*` 环境变量构造 Embedder / 本地 IntentAnalyzer / 可选 ShotSelector，`BuildV2` 打开库做标签四级召回并叠加全库向量近邻，再由确定性 planner 拍板。catalog 只存相对路径；电影本体和音轨不上云。风景任务和电影任务都按口播召回 catalog；风景任务仅在 catalog 没有可用镜头时回退 `media_index.json` 风景打散；电影任务在无可用镜头时报错。选片契约见 [混剪自动选片](operations/montage-catalog-matching.md)。

**本机建库**：控制台「开始建库」与 `catalog-builder.Build` 都调用 `mediacatalog.RunHostedBuild`（`Indexer` → FFmpeg `Pipeline` → `AnalysisRunner`）。缺 FFmpeg 时 ingest 仍保留、错误码 `ffmpeg_not_configured`；缺视觉/向量时错误码 `vision_not_configured`；识别全失败返回 `ErrAnalysisAllFailed`，不得标 ready。OpenAI 兼容地址若只填主机名，`CompatEndpoint` 会补 `/v1`。云机结果包合并只在 `catalog-builder` 小站（`MergePack`），控制台无合并 API。

**v2 自动检索（建库之后）**：

1. `taskManifestPreparer` 把 `media_catalog_path`、视觉/向量地址写入 `non_secret_settings`（`internal/httpapi/task_manifest.go`）。
2. `cmd/console` 的 `appendMontageCatalogEnv` 把 `VIDEO_CONSOLE_VISION_API_KEY` / `VIDEO_CONSOLE_EMBEDDING_API_KEY` / `VIDEO_CONSOLE_EMBEDDING_BASE_URL` / `VIDEO_CONSOLE_EMBEDDING_MODEL` / `VIDEO_CONSOLE_INTENT_*` 注入 `montage-script-run` 子进程（不进 Codex allowlist）。embedding 三项改完须重启，否则 Runtime 仍是旧快照。
3. `attachCatalogClients` 读 manifest：风景和电影都 `RestrictToLandscape=false`。意图分析默认 `LocalIntentAnalyzer`（中英词表桥）；对话客户端只挂成 `ShotSelector`。再构造 `HTTPEmbedder`（manifest 字段空则回落环境变量；URL/模型仍空则不挂 embedder）。
4. `BuildV2` 打开 `catalog.db` → `IntentAnalyzer.Analyze` → `rankLibrary`：
   - 四级召回（`recallForIntent`）：实体/话题标签 → 隐喻/视觉概念标签 → 情绪 → 任意就绪镜头。每级最多 50 条，去重后截断 80。
   - 有 embedder 时 `RecallReadyShots(2000)` 扫全库，按查询向量并入每段 top 32 邻居。
   - 打分：标签命中 + 向量余弦（`weightSemantic=0.40`）；缺 embedding 不失败，notes 写 `embedding_disabled`。
   - 可选 `ShotSelector` 只在每段 top 8 里点 1 个；失败写 `llm_shot_select_fallback`，保留排序结果。
5. **风景任务**用 catalog 召回结果，不再滤成风景/景观。catalog 有就绪镜头就只从库里选；catalog 为空或不存在才回退 `media_index.json` 风景打散。
6. **电影任务**（`SelectModeMovieCatalog` / `movie_catalog` 预设）不过滤风景；无可用镜头直接报错，不回退旧索引。配额用 `movieCatalogPolicy`（电影 70%–100%，同源可复用）。
7. 口播字幕默认 `captions.mode=off`（`SpokenCaptionsEnabled=false`）：不建「字幕」轨。板上标题/副标题仍在。大模型分段/关键词和 `highlights_only` 都不要打开。缩放约 1.20–1.26，B-roll/电影播放 **1.5×**，不写窗内白色片头。
8. 最终时间线仍是确定性 `selectTimelineV2`。对话模型不能绕过短名单去扫全库。

**v1 取样**（skill snapshot 未声明 `2.0` 时）仍按下面的 `media_index.json` 打散规则，没有 catalog 召回。

**计划版本门控**：`taskManifestPreparer` 读取**当前任务将冻结的** skill snapshot，用 `skillregistry.DecideMontagePlanVersion` 解析 `assets/capabilities.json`（必须出现在 `SkillSnapshot.Files` 且 SHA-256 与磁盘一致，`contract_version` 必须是已知的 `1.0`，且 `production_plan_versions` 明确含 `2.0`）。只有这时才把 `non_secret_settings.montage_plan_version` 写成 `2.0`。畸形/未知/缺文件/哈希不一致都写 `1.0` 并打 warning，任务仍可生成。已经 `EnsurePreparedTask` 的任务再次 Prepare 直接返回，不因 Latest snapshot 升级而改写 manifest。`montagescript.Run` 只看这份冻结字段：`2.0` 调 `BuildV2`，其余调 `Build`。

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

**配音路由必须精确分流**（`internal/app/app.go` 的 `projectRouteHandler`）：`POST /api/projects/{id}/narration` 进入 `narrationHandler`，负责调用语音供应商并成对登记配音与 SRT；`POST /api/projects/{id}/assets/narration` 必须留在 `projectsHandler` 的通用资产上传链路，只保存用户提供的音频。不能只用 `HasSuffix(path, "/narration")` 判断自动配音路由，否则手动上传路径也会被误分流到自动配音 mux，最终返回裸 `404 page not found`，浏览器会把连接/刷新阶段的异常泛化为网络失败。`internal/app/app_test.go` 同时覆盖两条路由，后续修改必须保证两者互不抢占。

- **会话**：单管理员，登录后 7 天有效（`internal/auth/service.go:27`），库里只存 token/CSRF 的 SHA-256（`internal/security/session.go:12-24`）。会话有效性还要求 `created_at >= password_changed_at`，所以改密自动作废旧会话。口令是 SHA-256 预摘要 + bcrypt。
- **中间件顺序**（外 → 内，`internal/app/app.go:157-170`）：`logging.RequestID` 包住**整个** mux（含静态资源）→ 路径白名单（`/api/health`、`/api/auth/*`、非 `/api/` 路径直通）→ `Protect`（会话认证，**CSRF 检查在其内部**，`internal/auth/middleware.go:50-53`）。
- **CSRF**：双提交 cookie + `X-CSRF-Token` 头，两者都要与库里的 hash 常量时间相等；保护除 GET/HEAD/OPTIONS 外的所有方法。
- **限流只有登录一处**：进程内内存表，键是 remote host（**不认 `X-Forwarded-For`**），10 分钟滑窗、5 次失败即锁 15 分钟，响应 `429` + `Retry-After: 900`（`internal/auth/service.go:237-320`、`internal/httpapi/auth.go:57-60`）。重启即清零，条目不回收。
- **回环限定只有一个端点**：`POST /api/assets/{id}/open-directory`（在资源管理器打开剪映草稿目录）。判定要求服务端监听地址、客户端地址、`Host` 三者都是回环，且 `Origin` 非空、`Sec-Fetch-Site: same-origin`、`Origin` 的 scheme 与 TLS 状态一致等一整组条件（`internal/httpapi/assets.go:216-238`）。目标目录还必须是由**成功 attempt** 登记过的、位于可信剪映根内的草稿。

### 5.7 设置与 machine profile

设置是 `settings` 表里的 key/value，写入走显式键白名单（`internal/store/settings.go`）；图文生图新增 `image_base_url`、`image_model`、`max_image_concurrency`、`image_generation_attempts`、`default_image_ratio`、`default_image_style`。`image_generation_attempts` 表示每张图含首次请求在内的总次数，默认 2、范围 1–4，与并发数字段独立，保存后热更新、不要求重启。密钥单独存在 `encrypted_secrets`，用 OS 保护（Windows 上 DPAPI）后 base64。一次更新里 public 与 secrets 在同一个 `BEGIN IMMEDIATE` 提交。

校验全部是手写 Go（`internal/settings/service.go:453-517`），包含几条有安全含义的约束：并发 1–4；路径必须是规范绝对路径且不经符号链接别名；`topic_cards_dir ⊂ obsidian_vault`、`media_index_path ⊂ media_root`；`baokuan_base_url` 只允许回环主机。

**密钥永不回传**：包括 `image_api_key` 在内，GET 只给 `{configured, masked}`，`masked` 是常量 `********`；解密值只存在于 `settings.Runtime`，密钥字段带 `json:"-"`。PUT 时空字符串表示「不改」。生图 Base URL 允许 HTTP 是为了兼容本机代理，但公网 HTTP 会明文暴露 Authorization/API Key，请求公网供应商必须使用 HTTPS；日志不得记录密钥或完整鉴权头。

**`restart_required`** 的原理是「配置态 vs 生效态」对比：服务缓存首次读到的 `Runtime` 快照，`Get` 时把可热更字段 `max_codex_concurrency` 清零后 `reflect.DeepEqual`，再比对密钥版本号（`internal/settings/service.go`）。热更字段直接就地生效并立刻推给调度器（`internal/app/app.go`）。

**machine profile** 是一个设置键 `machine_profile_path` 指向的本机 JSON，声明 `python_binary` 与 `jianying_root`（也可承载混剪资源覆盖，见 §5.4）。启动时它为空会**整体禁用混剪登记**（`cmd/console/main.go:188`、`:216`）；否则解析成 `TrustedRuntime` 并**固定其 SHA-256**，之后每个任务在登记前都要比对路径与哈希（`internal/montage/coordinator.go:75-120`、`:492-503`）。

`python_binary` 同时约束混剪的**生成阶段和登记阶段**：控制台构造 `montage-script-run` 子进程时会读取 machine profile，把解析后的 Python 绝对路径通过 `--python-binary` 传入自调用子命令；子命令再将它交给 `montagescript.Run`。不能只在登记协调器中读取该字段，否则生成阶段会回退到进程 `PATH` 里的 `python`，可能误用 Hermes 虚拟环境并报 `No module named 'pyJianYingDraft'`，即使 machine profile 指向的 Python 已正确安装模块。登记/重命名子进程还会显式移除父进程继承的 `PYTHONPATH`/`PYTHONHOME`：Hermes 启动环境可能把自身 venv 注入 `PYTHONPATH`，若原样传给 Anaconda Python，会优先加载 Hermes venv 中不匹配的 Pillow，出现 `cannot import name '_imaging' from 'PIL'`。

### 5.8 存储与迁移

- `store.Open` → 单连接、`foreign_keys=ON`、`busy_timeout=5000`，先校验迁移历史（版本必须是连续的 `1..N`，否则拒绝启动），对已存在的库在升级前自动备份，然后 `migrate`（`internal/store/db.go:96-161`）。
- 迁移是**版本化**的（`schema_migrations` 表），整轮在一个 `BEGIN IMMEDIATE` 内、期间关闭外键并在 `defer` 里恢复，提交前跑 `PRAGMA foreign_key_check`（`internal/store/migrations.go:837-911`）。当前 19 个迁移；第 19 个迁移新增图文域的 `image_projects`、`image_project_items` 两张表及其索引。
- 写事务统一走 `runImmediate`（`internal/store/tx.go:26`）：独占连接 + `BEGIN IMMEDIATE`；`fn` 出错则 `ROLLBACK`；**COMMIT 失败不再尝试 ROLLBACK**（可能已提交），返回 `CommitUnknown` 并把事务状态不明的连接用 `driver.ErrBadConn` 踢出连接池。
- 主要表：`accounts`/`projects`/`codex_tasks`/`task_events`/`task_messages`/`asset_items`/`asset_versions`/`asset_dependencies`/`task_artifacts`/`task_phase_runs`/`semantic_events`/`chat_*`/`idea_*`/`montage_registration_attempts`/`project_workflow_runs`/`project_step_notes`/`image_projects`/`image_project_items`/`settings`/`encrypted_secrets`/`admins`/`auth_sessions`/`skill_snapshots`/`thread_leases`。`assets`（v1）在 v2 之后只当只读审计表。

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
| `codex_default_model` / `..._reasoning_effort` | `gpt-5.6-sol` / `medium` | 设置键，可热更 |
| `image_model` / `max_image_concurrency` / `image_generation_attempts` | `gpt-image-2` / `3` / `2` | 设置键；并发 1–18、尝试次数 1–4（含首次），二者独立且可热更；服务地址/模型/密钥变更需重启 |
| `image_text_base_url` / `image_text_model` / `image_text_reasoning_effort` | 空 / 空 / 空 | 图文分段与提示词使用的 OpenAI 兼容文本模型；思考强度可空（请求不带该字段）；未单独配置时回落 Grok 配置 |
| `default_image_ratio` / `default_image_style` | `3:4` / `finance_documentary` | 设置键；用于新建图文项目默认值 |

环境变量（进程启动时生效）：

| 变量 | 默认 | 说明 |
|---|---|---|
| `VIDEO_CONSOLE_INITIAL_PASSWORD` | — | **仅首次安装**（尚无管理员）时必须提供，否则启动失败 |
| `VIDEO_CONSOLE_LOG_LEVEL` | `info` | `debug`\|`warn`\|`error`，其他值回落 info |
| `VIDEO_CONSOLE_MONTAGE_RUNTIME` | `script` | `script`\|`codex`；script 构建失败才回落 Codex |
| `VIDEO_CONSOLE_LLM_RUNTIME` | 空（remix **硬切** openai_compat） | 设为 `pi` 才改走 Pi；remix **不会**因缺配置回落 Codex，而是任务失败 |
| `VIDEO_CONSOLE_OPENAI_BASE_URL` / `_API_KEY` | 空 | 可被设置页 `REMIX_*` / `GROK_*` 覆盖 |
| `VIDEO_CONSOLE_VISION_API_KEY` / `_EMBEDDING_API_KEY` / `_INTENT_*` | 空 | 只注入 `montage-script-run`，用于建库检索与意图分析 |
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

---

## 11. 模块与代码对照（给接手 AI）

读代码时按模块进，不要按过期计划勾选框施工。符号名比行号可靠。

### 11.1 进程与路由

| 用户动作 | 进程 | 关键代码 |
|---|---|---|
| 打开控制台 | `cmd/console` `:2030` | `main()` → `app.New` 注册 mux |
| 制作方式首页 `/` | 嵌入 SPA | `web/src/production-modes/ModeHome.tsx` + `catalog.ts`；路由 `project-workbench/routes.ts` `parseLocation` |
| 混剪看板 `/projects` | 同上 | `web/src/shell/ConsoleHome.tsx` |
| 项目工作台 `/projects/{id}` | 同上 | `web/src/project-workbench/ProjectWorkbench.tsx` |
| 图文 `/image-projects` | 同上 | `web/src/image-mode/QuickGenerateForm.tsx` / `ImageModeWorkbench.tsx` |
| 素材库面板 | 无独立 URL，弹层 | `web/src/media-library/MediaLibraryPanel.tsx` → `/api/media-catalog/*` |
| 云机建库 | `cmd/catalog-builder` `:2031` | `internal/catalogbuilder/server.go` `web.go` |

HTTP 前缀在 `internal/app/app.go`：`/api/auth` `/api/accounts` `/api/projects` `/api/tasks` `/api/assets` `/api/settings` `/api/image-projects` `/api/media-catalog` `/api/skills` `/api/ideas` `/api/runtime` `/api/dependencies` `/api/library` `/api/obsidian` `/api/health`。未命中的 GET 回落 `index.html`。

`POST /api/projects/{id}/narration` 与 `POST /api/projects/{id}/assets/narration` 必须由 `projectRouteHandler` 精确分流。

### 11.2 二创（remix）

| 步骤 | 代码 |
|---|---|
| 工作台保存原文并启动 | `web/src/projects/useProjectActions.ts` → `POST /api/projects/{id}/tasks` |
| 任务创建 / 冲突 | `internal/httpapi/tasks.go` `remix.standard` / `remix.review` |
| 工作流编排 | `internal/workflow/remix.go` |
| 选路 | `cmd/console/main.go` `makeCommand`：LLM action **硬切** `openai-compat-run`，不回退 Codex |
| 写稿 | `internal/agentruntime/openaicompat/run.go` `buildWriterPrompt`（财经爆款机器；禁止抄原稿金句） |
| 落盘 | `openaicompat/deliver.go`：`continuous_script` + `publishing_package.json` |
| 改稿弹窗 | `web/src/assets/ReviseDialog.tsx` |
| 模型名 | 原样发送，不剥 `cursor-`；思考强度写在模型名里。`internal/taskmodel` 仍服务 Codex/图文文本 |

设置页「二创服务地址 / 二创模型 / 二创 API 密钥」→ `REMIX_*`（可回落 `GROK_*`）。`finance-viral-remix/SKILL.md` 只作 ≤1800 字补充，不是主提示。

### 11.3 配音与字幕

| 步骤 | 代码 |
|---|---|
| 自动配音 | `internal/httpapi/narration.go` → `internal/narration/produce.go` `volcengine.go` |
| 词级 SRT | `internal/narration/srt.go` `captions.go` |
| 手动上传 | `internal/httpapi/projects.go` 资产上传，类型 `narration` |
| 工作台按钮 | `web/src/project-workbench/ProjectWorkbench.tsx` / `ProjectAssets.tsx` |

混剪草稿默认不画口播字幕轨（`SpokenCaptionsEnabled=false`，`captions.mode=off`）。排版过关前不要打开。SRT 仍是资产，供配音对齐和选片语义。`highlights_only` 不要打开。

### 11.4 素材库建库

| 步骤 | 代码 |
|---|---|
| 设置路径与模型 | `internal/settings/service.go`；前端 `web/src/settings/SettingsPanel.tsx` |
| 开始建库 API | `internal/httpapi/media_catalog.go` → `internal/app/media_catalog_service.go` `runIndex` |
| 三步流水线 | `internal/mediacatalog/build.go` `RunHostedBuild`：`Indexer` → `Pipeline`/`ffmpeg.go` → `AnalysisRunner`/`analyzer.go` |
| URL 补 `/v1` | `internal/mediacatalog/compaturl.go` `CompatEndpoint` |
| 库表 | `internal/mediacatalog` 的 `catalog.db`（与 `console.db` 分离） |
| 状态/重试/导入 | `media_catalog.go`：status、sources、retry、Pexels/Pixabay search、local import |
| 云机打包合并 | `internal/catalogbuilder/build.go` `pack.go` `merge.go` |
| 前端文案 | `MediaLibraryPanel.tsx`：建好后混剪自动检索；`analysis_all_failed` 有独立提示 |

错误码：`ffmpeg_not_configured` / `vision_not_configured` / `analysis_all_failed` / `index_failed`。看 `ready_shots`，不要只看 `state=ready`。

### 11.5 混剪计划与自动检索

| 步骤 | 代码 |
|---|---|
| 入队预检 | `internal/httpapi/task_manifest.go` `ValidateMediaLibrary` |
| 计划版本门控 | `internal/skillregistry` `DecideMontagePlanVersion`（skill `assets/capabilities.json` 含 `2.0` 才冻结 v2） |
| 电影 vs 风景 skill | `cmd/console` `skillFromTaskManifest`；电影用 `jianying-movie-montage` |
| 执行入口 | `internal/agentruntime/montagescript/run.go` `Run` |
| 注入检索客户端 | `montagescript/catalog_clients.go` `attachCatalogClients` |
| v1 计划 | `montageplan/plan.go` `Build`：`media_index.json` 风景/建筑打散，缩放 1.4 |
| v2 计划 | `montageplan/plan_v2.go` `BuildV2` |
| 意图 | `montageplan/intent_analyze.go`：风景和电影都跟口播实体/话题走 |
| 四级召回 + 全库向量 | `montageplan/match.go` `recallForIntent` + `rankLibrary` + `appendSemanticNeighbors`；底层 `mediacatalog/recall.go` `RecallReadyShots` |
| 对话点选 | `montageplan/shot_select.go`：只重排每段 top 8 |
| 风景回退 | catalog 为空时 `media.go` `isLandscapeItem` 打散旧索引 |
| 配额 | `montageplan/quota.go`：`movieMixPolicy` / `movieCatalogPolicy` / `imageVideoPolicy`（后者无用户入口） |
| Python 造草稿 | skill `jianying-montage-draft` 或 `jianying-movie-montage` 的 `scripts/run_montage_job.py` |
| 登记 | `internal/montage/coordinator.go` `validator.go` `registrar.go` |
| 重试登记 | `internal/httpapi/montage.go` |
| 工作台按钮 | `ProjectWorkbench.tsx`：「开始风景混剪」「开始电影混剪」「重做混剪」 |

产品锁（风景/电影日产）：缩放约 1.20、B-roll 1.5×、口播字幕轨关闭、无窗内白字标题。不要按旧 v2 计划把重点字幕或口播字幕加回来。选片说明见 [混剪自动选片](operations/montage-catalog-matching.md)。

### 11.6 图文

| 步骤 | 代码 |
|---|---|
| 一键 202 | `internal/httpapi/imageproject_quick.go` + `imageproject_jobs.go` |
| 手动分段 | `internal/httpapi/imageprojects.go` |
| 规划/生图 | `internal/imageproject/planner.go` `client.go` `chat.go` `prompt_text_rules.go` |
| 表 | `internal/store/imageprojects.go`；领域 `internal/domain/imageprojects.go` |
| 前端 | `QuickGenerateForm.tsx`、`ImageModeWorkbench.tsx`、`PublishingDialog.tsx` |
| 与混剪隔离 | 不用 `codex_tasks`；卡片不进混剪看板 |

### 11.7 前端其余模块

| 目录 | 要点 |
|---|---|
| `web/src/App.tsx` | 认证、主题、History、WS、弹窗分层、任务水合 |
| `web/src/api/client.ts` | 唯一 HTTP 出口 |
| `web/src/api/mediaCatalog.ts` | 素材库 API 封装 |
| `web/src/query/` | react-query keys |
| `web/src/auth/` `accounts/` | 登录与账号 |
| `web/src/projects/stages.ts` | 五阶段常量；遗留 `topic` → `script` |
| `web/src/project-workbench/workflow.ts` | 由资产推导阶段 |
| `web/src/tasks/` | 任务详情 |
| `web/src/settings/` | 二创 / Codex / 图文 / 素材库 / 火山 |
| `web/src/idea/` | **未挂载**。`App.test.tsx` 禁止接回 |
| `web/src/RemixPromptStyleFields.tsx` | 二创风格字段 |

改前端后若要更新 exe 内页面：`npm --prefix web run build:embed`，再重建 `cmd/console`。

### 11.8 明确不要当现状的东西

| 误读 | 实际 |
|---|---|
| remix 默认 Codex，`VIDEO_CONSOLE_LLM_RUNTIME=openai_compat` 才切换 | 已硬切 openai_compat |
| 控制台建库只扫描、catalog-builder 才打标 | 两边都走 `RunHostedBuild` |
| 建库只是台账，混剪不会搜库 | 有 `ready_shots` 后 `BuildV2` 自动召回 |
| 方式四「代码已落地待验收」 | 仅 `image_video` 预设，无用户入口，**不要做** |
| 选题工作台 / 爆款库主路径 | UI 已卸 |
| v2 计划里的 15%–25% 重点字幕、3–5 秒白字片头 | 产品已锁关闭 |
| `docs/superpowers/` 勾选框 | 已删除；考古走 git |
| `montage.plan` 走 script | `Select` 如此声明，`CommandFactory` 仍走 Codex |
