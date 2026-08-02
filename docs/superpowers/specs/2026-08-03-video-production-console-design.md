# 本地视频生产控制台设计

日期：2026-08-03

## 1. 目标

建设一个只绑定本机 `127.0.0.1` 的浏览器控制台，用统一看板管理可动态增加的视频号账号、视频项目、生产资产和 Codex CLI 任务。

控制台复用现有选题、二创和混剪 Skill，共用同一套 45—65 岁财经受众策略、同一门《财富觉醒方法论》课程、同一个爆款库和同一个 Obsidian 知识库。账号只区分名称、固定背景图、视频资产和发布归属。

## 2. 已确认范围

### 2.1 包含

- 动态新增、编辑、停用账号。
- 添加账号时只输入账号名称并上传一张固定背景图。
- 统一生产看板，通过账号筛选所有项目。
- 新建空项目后依次执行选题、深化、二创和混剪。
- 新建已有素材项目，上传文案、配音和 SRT 后直接进入混剪。
- 管理连续版文案、口播稿、配音、SRT、背景图引用、混剪草稿和成片。
- 通过 Codex CLI 执行本机 Skill，实时显示处理事件、问题、结果和产物。
- 保存 CLI 会话 ID，在网页回答问题后恢复同一会话。
- 通过既有 MCP 让选题和二创 Skill 自动读取爆款库。
- 通过既有 HTTP/SSE 接口向网页展示爆款库状态和素材来源。
- 网页设置 Codex 最大并发数，允许 1—4，默认 2。
- 统一展示待发布和已发布项目。

### 2.2 不包含

- 不自动操作 Codex 桌面版会话。
- 不把控制台合并进现有爆款库可执行程序。
- 不直接读写爆款库 SQLite 数据库。
- 不关闭、重启或替换承担系统代理的爆款库主程序。
- 第一版不自动登录、上传或发布到微信视频号。
- 第一版不建设多用户、云端部署、远程访问或权限系统。

## 3. 推荐技术方案

### 3.1 形态

使用独立 Go 后端和 React/TypeScript 前端，前端构建后嵌入 Go 可执行文件，最终以单个本地服务启动。默认监听 `127.0.0.1:2030`，端口可在配置中修改。

- 后端：Go、标准 HTTP 路由或轻量路由器、SQLite、WebSocket。
- 前端：React、TypeScript、Vite、TanStack Query、dnd-kit。
- 数据库：控制台独立 SQLite，不复用爆款库数据库。
- 文件：资产保存在本地目录，数据库只保存元数据、路径、校验值和版本。
- 打包：Go `embed` 内置前端静态资源，生成一个 Windows 可执行文件。

选择 Go 是为了稳定管理 Windows 子进程、流式读取 Codex JSONL，并与现有爆款库技术栈保持一致。选择 React 是为了实现看板、上传、实时对话和多状态界面。

## 4. 系统边界

```text
浏览器
  -> 本地控制台 HTTP/WebSocket
       -> 控制台 SQLite
       -> 本地账号与项目资产目录
       -> Codex CLI Worker
            -> ~/.codex/skills
            -> 爆款库 MCP
            -> Obsidian
       -> 爆款库 HTTP/SSE http://127.0.0.1:2022
```

爆款库有两条连接路径：

1. AI 路径：Codex CLI 加载 Skill，Skill 自动调用 `baokuan_search_materials`、`baokuan_get_material_bundle` 等 MCP 工具。
2. 网页路径：控制台后端调用现有爆款库 HTTP 接口展示素材、详情和健康状态，并订阅 SSE 更新通知。

网页不替代 Skill 做 AI 素材选择，CLI 也不承担普通列表展示。

## 5. 核心数据模型

### 5.1 Account

- `id`：不可变 UUID。
- `name`：账号显示名称，必须唯一。
- `background_asset_id`：固定背景图资产。
- `color`：系统自动分配的看板识别色。
- `status`：`active` 或 `inactive`。
- `created_at`、`updated_at`。

账号不保存课程、受众或 Skill 策略。所有账号引用同一份共享策略。

### 5.2 Project

- `id`：不可变 UUID。
- `account_id`：所属账号。
- `title`：项目标题，可在选题前使用自动编号。
- `stage`：当前生产阶段。
- `topic_card_path`：可选 Obsidian 选题卡路径。
- `created_at`、`updated_at`。
- `ready_at`、`published_at`：可选时间。
- `publish_note`：可选发布备注。

生产阶段：

- `topic`：选题中。
- `script`：文案中。
- `assets`：素材待补。
- `mixing`：混剪中。
- `review`：待审核。
- `ready`：待发布。
- `published`：已发布。
- `archived`：已归档。

阶段由任务结果自动推进，也允许用户手动移动；后端必须校验目标阶段需要的资产。

### 5.3 Asset

- `id`、`project_id`、`account_id`。
- `type`：`continuous_script`、`spoken_script`、`audio`、`subtitle`、`account_background`、`mix_draft`、`final_video`。
- `path`：受控根目录内的绝对路径。
- `filename`、`mime_type`、`size`、`sha256`。
- `version`、`status`、`created_at`。
- `source_task_id`：由 Codex 任务产生时记录来源。

项目背景图不重复复制，使用账号的 `background_asset_id` 作为只读继承引用。账号更换背景图后，新项目自动使用新版；已生成成片保留当时使用的背景图版本记录。

### 5.4 CodexTask

- `id`、`project_id`、`account_id`。
- `type`：`topic_select`、`topic_deepen`、`remix`、`spoken_format`、`montage` 或 `custom_followup`。
- `skill_name`。
- `status`：`queued`、`running`、`waiting_input`、`completed`、`failed`、`cancelled`。
- `codex_session_id`。
- `prompt_snapshot`：本次实际发送的任务说明，不保存密钥。
- `result_summary`、`error_code`、`error_message`。
- `created_at`、`started_at`、`finished_at`。

### 5.5 TaskEvent 与 TaskMessage

`TaskEvent` 保存 CLI JSONL 的标准化事件，用于实时显示和断线重放：

- `sequence`、`task_id`、`kind`、`level`、`display_text`、`raw_json`、`created_at`。

`TaskMessage` 保存用户与 Codex 的可见对话：

- `role`：`user` 或 `assistant`。
- `content`、`task_id`、`created_at`。
- 可选 `question_schema`，用于选项、文本回答或确认。

### 5.6 Setting

- `max_codex_concurrency`：整数 1—4，默认 2。
- `codex_binary_path`。
- `codex_profile_name`。
- `workspace_root`。
- `obsidian_vault_path`。
- `baokuan_base_url`，默认 `http://127.0.0.1:2022`。
- `baokuan_mcp_command` 和只读启动参数。

密钥只从进程环境或受限系统配置读取，不进入数据库、任务提示词和网页响应。

## 6. 资产目录

```text
video-console-data/
├─ console.db
├─ accounts/
│  └─ <account-id>/
│     ├─ background/
│     │  └─ <asset-version>.<ext>
│     └─ projects/
│        └─ <project-id>/
│           ├─ script/
│           ├─ audio/
│           ├─ subtitle/
│           ├─ draft/
│           └─ output/
└─ logs/
```

上传文件先写入项目内临时目录，校验类型、大小和 SHA-256 后原子移动到目标目录。后端拒绝目录穿越、任意绝对路径和工作区外写入。

## 7. 页面设计

### 7.1 主看板

- 顶栏：新建项目、添加账号、任务队列、服务状态、设置。
- 左栏：全部账号、账号筛选、账号背景图缩略图、停用账号入口。
- 中部：统一阶段看板，可按账号、状态和关键词筛选。
- 右侧抽屉：项目详情、资产清单、Codex 对话、任务历史和产物预览。

项目卡显示账号颜色、标题、阶段、缺失资产、当前任务状态和最后更新时间。

### 7.2 新建项目

入口 A：从选题开始。

1. 选择账号。
2. 创建空项目。
3. 执行 `finance-topic-selector`。
4. 选题卡写入 Obsidian，项目保存卡片路径。

入口 B：从已有素材开始。

1. 选择账号。
2. 输入项目标题。
3. 上传文案、配音和 SRT。
4. 自动继承账号背景图。
5. 资产齐全后允许启动混剪。

### 7.3 实时任务面板

- 显示排队、启动、工具调用摘要、阶段进度、问题、结果和产物。
- 默认隐藏原始 JSONL，提供“技术日志”折叠区。
- `waiting_input` 状态显示问题、可选选项和输入框。
- 用户提交回答后恢复原 `codex_session_id`。
- 浏览器断线后根据最后事件序号重放，不能丢失进度。

### 7.4 设置页

- Codex 最大并发数：下拉或步进器，范围 1—4。
- Codex CLI 路径和版本检测。
- 爆款库 HTTP 与 MCP 健康状态。
- Obsidian 路径检测。
- 工作区和资产目录。
- Skill 可发现性检查。

## 8. Codex CLI 调度

### 8.1 启动任务

后端直接启动进程，不拼接 Shell 字符串：

```text
codex exec --json --skip-git-repo-check -C <project-dir> <prompt>
```

通过参数数组传递固定选项，任务正文通过 stdin 发送。不得启用 `--dangerously-bypass-approvals-and-sandbox`。

PromptBuilder 只根据白名单任务类型组合：

- 要使用的 `$skill-name`。
- 项目、账号和资产清单。
- 本次用户指令。
- Obsidian 卡片路径。
- 允许写入的输出目录。
- 机器可读的最终状态契约。

### 8.2 机器可读结果

每类任务使用 JSON Schema 约束最终响应：

- `status`：`completed`、`needs_input` 或 `failed`。
- `summary`。
- `question`：仅 `needs_input` 使用。
- `artifacts`：类型、路径和描述。
- `topic_card_path`：选题任务可返回。
- `next_recommended_action`。

非交互 CLI 若需要用户决策，应以 `needs_input` 正常结束，不在运行中的进程内等待 stdin。控制台保存会话 ID并将任务置为 `waiting_input`。

### 8.3 恢复任务

用户回答后启动：

```text
codex exec resume <session-id> --json <answer>
```

恢复任务沿用相同项目目录、任务边界和输出 Schema。一个 `session_id` 永远只绑定一个项目。

### 8.4 事件流

CLI stdout 按行解析 JSONL；stderr 单独记录。解析器把不同 CLI 版本的事件映射为稳定内部事件。未知事件保存为原始日志但不使任务失败。

控制台使用 WebSocket 向网页推送标准化事件；REST 负责创建任务、取消任务和提交回答。所有事件先写数据库再广播，确保断线可恢复。

## 9. 调度与并发

- 设置值允许 1—4，默认 2。
- 调度器只统计 `running` 任务；`waiting_input` 不占并发名额。
- 同一项目同一时间最多一个写任务。
- 不同项目可并行执行选题和二创。
- 混剪开始后对该项目输入资产加读锁，任务完成或失败后释放。
- 修改并发值只影响后续调度，不强制中止正在运行的任务。
- 取消任务先发送温和终止信号，超时后再终止子进程树，并保存取消事件。

## 10. 爆款库与 MCP

### 10.1 CLI MCP 配置

当前 CLI 的 MCP 列表尚无爆款库服务。控制台安装检查必须验证专用 Codex 配置已注册爆款库 MCP，命令通过现有可执行文件的 `mcp --base http://127.0.0.1:2022` 模式启动。

控制台不在每个 Skill 内硬编码 MCP 命令。CLI 专用配置负责工具注册，Skill 只负责调用工具。

### 10.2 网页 HTTP/SSE

控制台后端代理白名单只读接口：

- `/api/channels/library/materials/search`
- `/api/channels/library/materials/bundle`
- 已审核片段查询接口
- 爆款库统计和健康接口
- `/api/channels/library/events`

网页不直接访问爆款库，避免跨域和接口地址散落。控制台不开放采集、自动浏览、审核写入或代理控制能力。

## 11. Obsidian 集成

- 使用现有 `finance-topic-selector` 创建和深化选题卡。
- 控制台保存返回的绝对卡片路径和可选卡片摘要。
- 网页提供“打开所在文件夹”和“复制路径”，不直接实现完整 Markdown 编辑器。
- 项目删除时不删除 Obsidian 卡片，只解除关联。

## 12. 状态推进规则

- 创建空项目：`topic`。
- 选题卡达到可写稿：允许进入 `script`。
- 连续版文案确认后：允许生成口播稿。
- 文案、配音、SRT 和账号背景图齐全：允许进入 `mixing`。
- 混剪草稿完成：进入 `review`。
- 成片确认：进入 `ready`。
- 用户手动标记已发布：进入 `published` 并记录时间。

手动拖动卡片不绕过资产校验。缺失项以明确提示阻止推进，但允许管理员将项目退回较早阶段。

## 13. 错误处理

- Codex 未安装或未登录：任务不入队，设置页显示修复提示。
- MCP 未注册：选题/素材增强任务阻止启动，普通不依赖爆款库的任务可继续。
- 爆款库离线：显示离线，不尝试关闭或重启主程序。
- Obsidian 路径失效：阻止选题卡写入，已有素材项目不受影响。
- CLI 输出非 JSON：保留原文并标记解析告警，不立即丢弃任务。
- CLI 异常退出：任务标记失败，保留 session ID、日志和已有产物。
- 文件被删除或移动：资产标记 `missing`，项目卡显示缺失提醒。
- 浏览器断线：任务后台继续，重连后从数据库恢复事件。
- 控制台重启：运行中任务标记 `interrupted` 并提供恢复或重跑，不猜测进程仍可控。

## 14. 本机安全边界

- 服务只监听 `127.0.0.1`。
- 校验 `Origin`，拒绝非本机网页调用写接口。
- 不提供任意命令输入；所有 CLI 参数来自白名单构造器。
- 项目 ID、账号 ID 和路径由后端解析，不直接信任前端路径。
- 上传文件限制扩展名、MIME、大小和目标目录。
- 不在网页、日志、SQLite 或提示词中保存 API 密钥。
- 控制台无权修改系统代理或结束爆款库主进程。

## 15. 测试策略

### 15.1 单元测试

- 账号名称唯一性和背景图版本。
- 项目阶段转换和资产门槛。
- 文件路径边界和上传校验。
- 并发设置 1—4 和运行任务计数。
- 同项目写任务互斥。
- PromptBuilder 不串项目、不暴露密钥。
- Codex JSONL 事件解析与未知事件兼容。
- `needs_input`、恢复和 session 绑定。

### 15.2 集成测试

- 使用假 Codex 进程输出固定 JSONL，测试实时事件、失败、取消和恢复。
- 使用假爆款库 HTTP/SSE 服务测试素材展示和断线重连。
- 使用临时 Obsidian 目录测试卡片路径关联。
- 使用临时资产根目录测试上传、版本和缺失检测。

### 15.3 端到端测试

- 添加账号并上传固定背景图。
- 从选题创建项目，收到问题后在网页回复并恢复会话。
- 从已有文案、配音和 SRT 创建项目并启动混剪。
- 调整并发数为 1 和 4，验证排队行为。
- 按账号筛选统一待发布/已发布看板。
- 浏览器刷新后恢复正在执行的任务状态。

测试不得打开微信视频号、启动自动浏览或执行真实发布。

## 16. 交付阶段

1. 项目骨架、数据库迁移、配置和健康检查。
2. 账号、固定背景图、项目、资产上传和统一看板。
3. Codex CLI Worker、JSONL 事件、WebSocket 和会话恢复。
4. 爆款库 MCP 配置检查、HTTP/SSE 代理和素材来源展示。
5. Obsidian 选题卡联动、选题与二创按钮。
6. 混剪任务、产物登记、预览、待发布和已发布流程。
7. 完整测试、Windows 单文件构建和本机启动说明。

每个阶段必须在假服务和本地接口测试通过后再进入下一阶段，不打开微信视频号进行测试。

## 17. 验收标准

- 可在网页添加任意数量账号，每个账号固定一张背景图。
- 所有账号项目在同一个看板中，可按账号筛选。
- 可从空项目执行选题，也可上传已有文案、配音和 SRT 直接混剪。
- Codex CLI 任务事件实时显示，刷新页面后不丢失。
- Codex 提问会使任务进入等待回复，网页回答后恢复同一项目会话。
- 并发数可在 1—4 之间即时调整。
- 选题和二创 Skill 能通过 CLI 配置的 MCP 自动读取共用爆款库。
- 控制台不关闭爆款库主程序，不影响系统代理和网络。
- 项目资产、CLI 会话和输出不会跨账号或跨项目混用。
- 待发布和已发布项目统一展示，成片可在项目详情预览。
