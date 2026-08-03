# 视频生产控制台 Workflow V2 设计

日期：2026-08-03
状态：已逐段确认，待最终文档复核
目标系统：`video-production-console`
目标用户：本机单管理员，通过局域网浏览器管理多个视频号账号与视频项目

## 1. 背景与目标

当前控制台已经具备账号、项目、资产上传、Codex CLI 任务、实时事件和局域网访问的基础能力，但仍存在以下系统性问题：

1. 空项目会错误显示“当前阶段所需素材齐全”。
2. Codex 任务不可点击，用户看不到实际 Prompt、CLI 参数、任务事件、提问、回复和最终结果。
3. 素材列表缺少文本、音频、SRT、视频和工程目录的统一预览与版本管理。
4. 爆款库、Obsidian、搜索服务、Codex CLI、媒体库和剪映等依赖没有统一设置与健康检查。
5. 连续文案、口播稿、配音、SRT、草稿、成片的来源、用途和可操作能力不明确。
6. 用户必须先创建项目，无法先以聊天方式完成选题探索与候选确认。
7. 桌面端修改 Skill 后，控制台无法判断新任务、运行中任务和等待回复任务分别使用了哪个版本。
8. 当前 CLI 结果解析、任务输入、账号归属、资产登记和调度配置仍有多处不可靠实现。

V2 的目标是建立“统一视频项目看板 + 账号筛选 + 每个项目独立 Codex 会话”，同时新增无项目选题工作台。系统继续使用一个本地 Go 程序、一个 SQLite 数据库、一个 React 前端和本机 Codex CLI，不拆成多个部署服务。

## 2. 已确认的产品决策

- 首页采用双入口：“开始选题”和“已有爆款原文”。
- 选题先返回 3—5 个候选，用户选择、深化并确认完整选题卡后，才创建正式项目。
- 项目内部采用三栏工作台：左侧资产树，中间预览与编辑，右侧 Codex 任务与续聊。
- Codex 提问时当前轮结束并进入 `awaiting_input`；网页回答后使用同一 session ID 继续执行。
- 文本和 SRT 编辑采用版本化保存；上游变化只将下游标记为过期，不删除旧文件。
- 设置采用“全局配置 + 任务快照”；已启动任务不受后续设置静默影响。
- Skill 正文仍由桌面端或文件编辑器维护，网页只显示路径、版本、指纹、依赖和健康状态。
- 控制台使用单管理员账号，初始口令为 `123321`；局域网设备共用该登录口令。
- 上传文件复制进托管项目目录；CLI 通过 `task_manifest.json` 读取文件，不依赖浏览器原始路径。
- 密钥加密保存并通过进程环境变量注入，禁止进入 Prompt、manifest、日志和网页明文响应。
- 全局 Codex 并发范围为 1—4，可在设置页动态调整。
- 测试不打开微信视频号；默认不启动剪映，也不进行剪映 UI 自动化。

## 3. 方案选择

### 3.1 未采用：继续补丁式扩展

直接在当前 `web/src/App.tsx`、现有任务结构和资产布尔判断上继续增加功能，短期修改较快，但会继续放大界面、状态、任务协议和文件处理之间的耦合。

### 3.2 采用：模块化单体

保留 Go、SQLite、React 和单一部署程序，后端拆分为认证、设置、选题、项目、资产、Codex 网关、调度器和 Skill 注册表模块；前端拆成独立页面、功能组件和 API 层。模块之间通过数据库记录、服务接口、事件和 manifest 交互。

### 3.3 未采用：多个独立服务

将任务、素材、Codex 网关拆成不同进程虽然扩展能力更强，但会增加本地安装、端口、认证、故障排查和数据一致性复杂度，不符合当前四任务并发与单机使用规模。

## 4. 总体架构

```text
浏览器
  登录 / 首页 / 选题工作台 / 项目看板 / 三栏项目工作台 / 任务中心 / 设置
        │ HTTP API + SSE + Session
        ▼
Go 模块化单体
  Auth | Settings | Ideas | Projects | Assets | Codex Gateway | Scheduler | Skill Registry
        │
        ├── SQLite：业务记录、版本索引、消息、事件、设置元数据
        ├── 托管数据目录：上传副本、manifest、日志、文本、音频、SRT、草稿、成片
        └── 外部依赖：Codex CLI、Skill、爆款库 MCP、Obsidian、Grok、媒体库、剪映
```

统一执行链路为：

```text
网页选择动作
→ 服务端解析项目、账号、资产版本和设置
→ 生成 task_manifest、Prompt、output schema 和任务快照
→ codex exec --json --output-schema --output-last-message
→ JSONL 事件解析并持久化
→ SSE 实时推送
→ 结构化结果校验
→ 登记资产版本或工程产物
```

## 5. 页面与用户流程

### 5.1 登录与首页

未登录用户只能访问登录页。登录后首页包含：

- `开始选题`：进入无项目选题会话。
- `已有爆款原文`：粘贴或上传 `source_script`，选择账号后创建项目。
- 最近项目。
- 等待用户回复的 Codex 任务。
- 爆款库、Obsidian、Codex、搜索服务和混剪环境的异常提醒。

### 5.2 选题工作台

选题工作台不依赖项目，使用 `idea_sessions` 保存聊天上下文：

1. 用户开始一次选题会话。
2. 控制台调用 `finance-topic-selector` 的 `brainstorm` 动作。
3. Skill 读取近 30 天 Obsidian 选题卡，从正式爆款文案和已审核片段中返回 3—5 个候选。
4. 用户选择一个候选，调用 `commit_topic` 原子创建候选卡。
5. 用户说“深化一下”或点击深化，调用 `deepen` 补充 3—8 条来源并生成二创交接简报。
6. 页面预览完整选题卡。只有状态为 `可写稿` 且用户确认后，才允许创建项目。
7. 创建项目时选择视频号账号，并把选题卡登记为项目 `topic_card` 资产。

### 5.3 直接爆款原文流程

用户可直接粘贴或上传完整同行文案，不需要先创建选题卡，也不强制提供爆款片段。创建项目时保存：

- 项目名称。
- 视频号账号。
- 完整 `source_script` 资产。
- 二创模式，控制台默认勾选 `enhanced`，用户可改为 `standard`。

### 5.4 统一项目看板

看板列为：`待制作`、`制作中`、`待发布`、`已发布`。看板状态只表达发布流程，不直接代表资产齐全。用户可按账号筛选。新增账号只需名称和一张固定背景图；文案、配音、SRT、草稿和成片属于具体视频项目。

### 5.5 三栏项目工作台

- 左栏：按分组显示项目资产、版本和 `missing / ready / stale / generating / failed` 状态。
- 中栏：文本预览和编辑、历史版本比较、音频播放、SRT 查看与编辑、视频播放、草稿工程清单。
- 右栏：项目内 Codex 任务、对话、问题、回复框和继续执行按钮。
- 顶部：显示选题、二创、配音、SRT、混剪、成片的阶段进度与下一动作缺失项。

左右栏可折叠；窄屏退化为 Tab 页面。

## 6. 正式资产、工程产物与依赖关系

### 6.1 正式资产定义

| 分组 | 类型 | 来源 | 用途 | 网页能力 |
|---|---|---|---|---|
| 原始输入 | `source_script` | 用户粘贴或上传 | 直接二创主来源 | 查看、编辑、替换、版本比较、导出 |
| 原始输入 | `topic_card` | 选题 Skill + Obsidian | 选题型写稿简报与来源引用 | 查看、编辑、同步状态、打开路径 |
| 文案产物 | `continuous_script` | 二创 Skill | 最终连续版文案 | 查看、编辑、版本比较、导出 |
| 文案产物 | `spoken_script` | 连续版只做语义断句 | 配音与字幕制作文本 | 查看、编辑、逐字一致校验、导出 |
| 制作素材 | `narration` | 用户上传或后续配音流程 | 混剪唯一旁白音轨 | 播放、替换、下载 |
| 制作素材 | `subtitle_srt` | 用户上传或后续字幕流程 | 混剪语义选镜上下文和项目交付 | 查看、校验、编辑、替换、下载 |
| 制作素材 | `account_background` | 账号固定背景图 | 财经版式背景板输入 | 预览、替换、版本化 |
| 视频输出 | `mix_draft` | 混剪 Skill | 可编辑剪映明文工作区与注册结果 | 查看清单、打开目录、重新生成 |
| 视频输出 | `final_video` | 剪映导出后登记或用户上传 | 待发布与已发布成片 | 播放、替换、下载 |

`mix_draft` 支持目录型资产版本；托管明文工作区是主资产，剪映注册路径作为版本元数据保存。

### 6.2 工程产物

以下内容属于 `task_artifacts`，不得混入正式资产：

- `task_manifest.json`
- Prompt 文件和 output schema
- `production_plan.json` / `production_plan.md`
- 结构化最终结果与 `output-last-message`
- QC、校验和素材使用报告
- CLI stdout JSONL、stderr 和诊断日志

### 6.3 版本字段

资产采用逻辑资产与版本分离模型。每个版本保存：

- 标准 UUID。
- 递增版本号。
- 文件或目录存储类型。
- 托管路径。
- 真实 MIME、大小和 SHA-256。
- 创建来源、父版本和生成任务。
- 当前状态、过期原因和创建时间。

### 6.4 依赖失效

产生新版本后只传播 `stale`，不删除旧版本：

- `source_script` 或 `topic_card` 变化：连续文案及全部下游过期。
- `continuous_script` 变化：口播稿、配音、SRT、草稿、成片过期。
- `spoken_script` 变化：配音、SRT、草稿、成片过期。
- `narration` 变化：SRT、草稿、成片过期。
- `subtitle_srt` 或账号背景图变化：草稿、成片过期。
- `mix_draft` 变化：旧成片过期。

过期传播按实际版本依赖记录执行，页面允许查看旧链路和恢复旧版本。

### 6.5 动作就绪度

`Requirements Evaluator` 针对动作实时计算 `ready` 或 `blocked`，并返回明确 `missing_inputs`：

| 动作 | 必需输入 |
|---|---|
| 选题 brainstorm | 爆款库 MCP 健康、Obsidian 可读、选题配置有效 |
| 选题 commit | 有效候选、Obsidian 可写 |
| 选题 deepen | 唯一候选卡、卡片可写、爆款库 MCP 健康 |
| 直接二创 | `source_script` 为 ready |
| 选题卡写稿 | `topic_card` 状态为可写稿且引用材料可读 |
| 口播格式化 | `continuous_script` 为 ready |
| 混剪 | `spoken_script`、`narration`、`subtitle_srt`、`account_background` 为 ready，混剪环境健康 |
| 成片登记 | `mix_draft` 为 ready，用户提供导出视频 |

空项目没有上述必需资产，因此不得显示“素材齐全”。

## 7. Codex 任务协议

### 7.1 创建任务

任务创建只接受 `project_id`、Skill action 和用户可选参数。服务端必须从项目读取 `account_id`，不得信任前端提交的账号归属。创建时：

1. 校验动作就绪度。
2. 锁定输入资产的具体版本。
3. 创建标准 task UUID；混剪任务同时把该 ID 作为 `job_id`。
4. 保存 Skill、配置和依赖健康快照。
5. 生成 manifest、Prompt、output schema 和输出目录。
6. 进入全局调度队列。

### 7.2 Task Manifest

manifest 至少包含：

```json
{
  "schema_version": "2.0",
  "task_id": "uuid",
  "job_id": "uuid",
  "skill": "finance-viral-remix",
  "action": "enhanced",
  "project": {"id": "uuid", "account_id": "uuid"},
  "inputs": [
    {
      "asset_id": "uuid",
      "version_id": "uuid",
      "type": "source_script",
      "role": "primary_source",
      "path": "absolute-managed-path",
      "mime": "text/plain",
      "sha256": "hex",
      "required": true
    }
  ],
  "output_dir": "absolute-task-output-path",
  "expected_outputs": [],
  "approval_mode": "plan_then_wait",
  "skill_snapshot_id": "uuid",
  "non_secret_settings": {}
}
```

账号背景图必须作为混剪输入写入 manifest。密钥和可直接还原密钥的值不得写入。

### 7.3 CLI 命令与 Prompt

新任务使用：

```text
codex exec --json --output-schema <schema> --output-last-message <last-message> -C <workspace> <prompt>
```

Prompt 使用稳定模板，例如：

```text
Use the $finance-viral-remix skill.
Execute action=enhanced using the task manifest at <manifest-path>.
Treat manifest inputs as authoritative. Do not ask for paths already present.
Write every declared artifact under output_dir and return only the required result schema.
```

混剪映射必须从当前错误的 `chatcut-finance-video` 改为 `$jianying-montage-draft`。

### 7.4 实时事件与最终结果

Runner 逐行解析 stdout JSONL，同时原样追加到任务事件文件并写入 `task_events`。浏览器通过 SSE 接收持久化事件，断线后使用 `Last-Event-ID` 补发。

最终结果按以下顺序获取：

1. 解析 `item.completed` 中 `agent_message.text`。
2. 读取 `--output-last-message` 指定文件。
3. 按任务 output schema 校验。

当前只读取 `turn.completed.result` 的逻辑必须删除。没有有效最终结果时，任务不得标记完成或登记资产。

### 7.5 任务详情

任务详情包含：

- 对话：用户消息、Codex 回复、结构化问题和选项、回复与继续。
- 发送内容：实际 Prompt、manifest、输入文件角色、版本、大小、hash、Skill 与配置快照。
- 技术日志：脱敏 CLI argv、session ID、JSONL、stderr、退出码和耗时。
- 产物：结果信封、工程产物、登记资产和校验错误。

### 7.6 提问与续聊

CLI 提问必须持久化到 `task_messages`，包括问题文本、选项和创建时间。任务转为 `awaiting_input`。用户回复后运行：

```text
codex exec resume --json --output-schema <schema> --output-last-message <last-message> <session-id> <reply>
```

回复、恢复事件和新的最终结果继续写入同一逻辑任务会话。若 Skill 指纹已变化，默认不静默 resume，而是提示创建关联的替代任务。

### 7.7 状态机与并发

任务状态为：

```text
queued → running → awaiting_input → resuming → completed
                    ↘ failed / canceled
```

- 调度器启动时读取数据库中的全局并发设置，范围 1—4，设置变化后安全调整新任务槽位。
- 同一项目只允许一个会写资产的任务运行。
- 不同项目允许并行。
- 继续使用 `media-index-write` 和 `jianying-registration` 共享锁。
- 锁忙、排队原因和当前持有任务必须在网页显示。

## 8. 认证与设置

### 8.1 单管理员认证

- 首次创建数据库且无管理员记录时，用初始口令 `123321` 生成强哈希；不保存明文。
- 登录成功后使用随机高熵 Session token，数据库只保存 token hash。
- Cookie 使用 `HttpOnly`、`SameSite=Strict`，局域网 HTTP 环境不错误声明不可用的 `Secure`。
- 修改类请求使用 CSRF token。
- 登录失败限速并实施短期锁定。
- 设置页允许修改口令、查看活跃会话和注销全部会话。
- 初始口令未修改时显示持续安全提醒，但不阻塞已确认的局域网使用流程。
- `/api`、SSE、预览、下载和静态应用入口中的敏感页面均受认证保护。

### 8.2 密钥保护

- Windows 上使用 DPAPI 加密敏感设置后落盘。
- API 只返回 `configured` 和 masked 提示，不返回密文或明文。
- CLI/MCP 子进程通过受控环境变量获得密钥。
- 日志、错误、argv 展示、诊断导出和任务快照执行统一脱敏。
- 现有 Skill 中不保存真实密钥；分享 Skill 时只保留环境变量名和配置说明。

### 8.3 设置分组

| 分组 | 配置 |
|---|---|
| 系统 | 监听地址、数据根目录、Codex 并发 1—4、日志保留 |
| 爆款库 | MCP 启动命令、Base URL、连接测试、完整文案和片段接口能力 |
| Obsidian | Vault 路径、选题卡目录、近 30 天去重、读写测试 |
| 搜索服务 | Grok Base URL、模型、密钥、系统默认搜索回退、连接测试 |
| Codex CLI | 可执行文件、版本、MCP 列表、运行测试 |
| 混剪环境 | 剪映路径、媒体索引、媒体根、Pexels 密钥、BGM/SFX/叠化缓存、锁状态 |
| Skill 注册表 | 安装路径、mtime、SHA-256、依赖、重新扫描 |
| 认证 | 修改口令、会话、注销全部设备 |

设置 API 保存全局值；任务创建时保存脱敏的非敏感快照和密钥版本标识，运行中的任务不读取变化后的全局值。

## 9. Skill 注册、同步与契约

### 9.1 同步规则

桌面端和 CLI 当前共用 `C:\Users\prepare\.codex\skills`。新 `codex exec` 会读取最新 Skill，运行中或等待恢复的旧 session 不保证重新加载。

Skill Registry 对每个目标 Skill 目录建立聚合指纹，记录 Skill 正文、直接引用文件、脚本和必要资产的相对路径、mtime 和 SHA-256。任务保存 `skill_snapshot_id`。规则为：

- 未启动任务：启动前重新扫描并使用最新版。
- 正在运行：保持启动时上下文和快照。
- 等待回复：指纹未变时可 resume；指纹变化时默认创建替代任务并关联旧任务。
- 网页提供查看版本、路径、更新时间、依赖检查和重新扫描，不在线编辑 Skill 正文。

### 9.2 `finance-topic-selector`

新增显式动作：

- `brainstorm`：读取最近卡片和爆款材料，返回 3—5 个候选及评分，不写 Obsidian。
- `commit_topic`：根据用户选中的 `candidate_id` 原子创建 `候选` 卡，使用选题卡写锁，返回路径、hash 和引用。
- `deepen`：只深化指定卡片，补充 3—8 条来源，校验后返回 `可写稿` 或 `证据薄弱`。

改造要求：

- Vault、选题卡目录、MCP 地址由 manifest 和设置提供，不再硬编码个人路径。
- Obsidian 写入使用临时文件、校验和原子替换；同一张卡使用跨任务写锁。
- 输出同时写 JSON 结果和 Markdown 卡片。
- Skill 不创建视频项目；控制台负责把卡片登记为资产并在用户确认后创建项目。
- 片段只能提供钩子、结构、情绪、场景和解释方式，不能拼接成稿。

### 9.3 `finance-viral-remix`

新增显式动作：

- `standard`：只以完整 `source_script` 为主来源。
- `enhanced`：主原文不变，辅助使用正式爆款文案、审核通过片段和必要公开资料。
- `from_topic_card`：只接受状态为 `可写稿` 的卡片及其整理文字。
- `spoken_format`：只对已确认连续文案换行，去除换行后必须逐字一致。
- `review`：对现有文案执行爆点、结构距离、AI 味和输出契约检查。

控制台完整交付的结构化产物为：爆点分析、新结构设计、3 个标题、视频简介、3—5 个话题、连续文案、口播稿、CTA 和简短自检。连续文案与口播稿保存为不同正式资产；其余内容保存为任务结果和可预览的发布包工程产物。

增强模式规则：

- 用户原文始终是主来源。
- 本地先使用 `baokuan_search_materials` 和 `baokuan_get_material_bundle`。
- 需要公开事实补强时 Grok 优先，失败后才回退系统默认搜索。
- 不向外部搜索发送完整原文、内部库内容或密钥。
- 不拼接其他完整文案或片段原句。

### 9.4 `jianying-montage-draft`

控制台模式输入：

- `task_id` 同时作为不可变 `job_id`。
- 用户配音，必需且是唯一旁白音轨。
- 口播稿和 SRT，只作语义选镜上下文，不创建字幕轨。
- 账号固定背景图。
- machine profile 中的媒体索引、媒体根、剪映路径、缓存和注册配置。

改造要求：

- 如果 manifest 已提供 job ID，Skill 必须复用；独立 CLI 使用时才自行生成。
- 新增统一确定性 builder 入口，消费 manifest 和批准后的 `production_plan.json`，不依赖模型临时拼装命令。
- 保留 PLAN、批准、EXECUTE、明文校验和注册门。
- `approval_mode` 支持 `plan_then_wait`、`plan_only` 和已明确授权的 `auto_after_valid_plan`。
- 可分享 Skill 只保存逻辑资产 ID 与去路径化案例；本机绝对缓存路径迁入 machine profile。
- 保留独立工作区、`media-index-write` 和 `jianying-registration` 现有并发规则。
- 默认不启动剪映、不做 UI 冒烟测试。

输出包括 production plan、校验报告、素材使用摘要、明文草稿工作区、注册路径和结构化 result manifest，不输出新文案、合成配音、标题简介或话题。

### 9.5 统一结果信封

三个 Skill 返回同一顶层结构：

```json
{
  "schema_version": "2.0",
  "task_id": "uuid",
  "action": "string",
  "status": "completed|awaiting_input|failed",
  "summary": "string",
  "questions": [],
  "artifacts": [],
  "asset_outputs": [],
  "warnings": []
}
```

控制台只根据通过 Schema 校验的 `asset_outputs` 登记正式资产；路径必须位于任务输出目录或被允许的注册目录。

## 10. 数据模型

### 10.1 新增或扩展实体

- `admins`：管理员 ID、口令哈希、口令更新时间。
- `auth_sessions`：Session token hash、CSRF hash、过期和最近访问。
- `settings`：非敏感设置及版本。
- `encrypted_secrets`：配置键、DPAPI 密文、版本和更新时间。
- `skill_snapshots`：Skill 名称、路径、聚合指纹和文件清单。
- `idea_sessions`：无项目选题会话及状态。
- `idea_messages`：选题会话消息。
- `idea_candidates`：候选主题、顺序、评分、来源和选择状态。
- `assets`：项目内逻辑资产、类型和当前版本。
- `asset_versions`：路径、MIME、大小、hash、父版本、状态和生成任务。
- `asset_dependencies`：上游版本与下游版本关系。
- `codex_tasks`：项目、Skill、action、CLI session、manifest、Prompt、结果、快照和状态。
- `task_messages`：任务会话中的用户、Codex 和系统消息。
- `task_events`：排序后的 JSONL 与领域事件。
- `task_artifacts`：工程文件、类型、路径、MIME、大小和 hash。
- `task_relations`：重试、替代、续跑和来源任务关系。

### 10.2 状态分离

- 项目发布状态：`draft / producing / ready_to_publish / published`。
- 动作就绪度：实时计算 `ready / blocked`，不存为全局布尔值。
- 资产状态：`missing / ready / stale / generating / failed`。
- 任务状态：`queued / running / awaiting_input / resuming / completed / failed / canceled / interrupted`。

### 10.3 文件布局

```text
video-console-data/
  console.db
  projects/<project-id>/
    assets/<asset-type>/<asset-id>/v<version>/...
    tasks/<task-id>/
      task_manifest.json
      prompt.txt
      output.schema.json
      output-last-message.json
      events.jsonl
      stderr.log
      artifacts/...
  ideas/<idea-session-id>/...
  backups/...
```

所有写入先写临时文件并原子替换。上传文件必须计算 hash 后再登记数据库。目录型资产生成清单 hash。

## 11. API 边界

主要 API 组：

- `/api/auth`：登录、登出、当前用户、修改口令、注销会话。
- `/api/settings`：脱敏读取、分组更新、连接测试。
- `/api/skills`：扫描、查看指纹、依赖和健康状态。
- `/api/ideas`：会话、消息、brainstorm、commit、deepen、创建项目。
- `/api/accounts`：账号、固定背景图和启用状态。
- `/api/projects`：看板、筛选、项目详情、阶段和 requirements。
- `/api/assets`：上传、文本内容、预览、版本、更新、替换和下载。
- `/api/tasks`：创建、详情、回复、取消、重试、替代任务。
- `/api/events`：全局与任务 SSE。
- `/api/health`：Codex、MCP、Obsidian、搜索、媒体库和剪映环境。

文件读取只接受 `asset_id` 或 `task_artifact_id`。后端解析托管路径、验证归属和允许根目录，拒绝任意绝对路径与路径穿越。音视频预览支持 HTTP Range 和正确 MIME。

## 12. 迁移与故障恢复

### 12.1 增量迁移

1. 检查 schema version。
2. 创建带时间戳的 SQLite 备份和数据目录清单。
3. 在单事务内执行新增表、字段和索引，失败自动回滚。
4. 扫描旧项目目录：可识别的文件登记为正式资产版本。
5. manifest、计划、QC 和日志迁入 `task_artifacts`。
6. 无法可靠分类的文件保持原位并标记待确认，不删除。
7. 旧任务缺少 manifest 时保持只读历史状态；新任务强制使用 V2 协议。
8. 重新计算每个项目的动作就绪度与资产过期状态。

### 12.2 服务重启

- `queued` 任务重新排队。
- `running / resuming` 任务标记 `interrupted`。
- 只读任务可恢复原 session。
- 会写资产或草稿的任务默认创建关联安全重试。
- 剪映注册关键区绝不自动重放；先检查锁、目标目录和注册结果。

### 12.3 错误类型

- `missing_input`：返回确切缺失资产。
- `dependency_unhealthy`：指出 MCP、路径、密钥或服务异常。
- `awaiting_input`：保存并显示 Codex 原问题和选项。
- `output_invalid`：保存原结果但不登记资产。
- `lock_busy`：显示锁名、持有任务和排队状态。
- `process_failed`：保存退出码、stderr 和可安全重试建议。

## 13. 测试与验收

### 13.1 后端单元测试

- Requirements Evaluator 对空项目、缺失、ready、stale 和 failed 的判断。
- 依赖图的过期传播与旧版本保留。
- JSONL 解析，特别是 `item.completed / agent_message / text`。
- 结果 Schema、路径归属、UUID、版本、MIME 和 hash。
- 登录限速、Session、CSRF、DPAPI 包装和日志脱敏。
- Skill 指纹与旧 session 更新检测。

### 13.2 集成测试

- 使用伪 Codex CLI 输出 JSONL、问题、错误和最终 agent message。
- 新任务、等待回复、同 session resume、替代任务。
- `--output-schema` 与 `--output-last-message` 缺失/有效/无效结果。
- SSE 断线和 `Last-Event-ID` 补发。
- 上传、文本版本、音视频 Range、路径穿越拒绝。
- SQLite 增量迁移、失败回滚和旧数据保留。
- 1—4 并发、同项目单写、跨项目并行和共享锁排队。

### 13.3 前端测试

- 新建空项目显示明确缺失，不显示素材齐全。
- 首页双入口、选题聊天、候选选择、卡片确认后创建项目。
- 资产树、文本编辑、版本比较、音频/SRT/视频预览。
- 任务行可点击，四个详情区可访问，等待回复可发送并续跑。
- 设置脱敏、健康测试和 Skill 更新提示。
- 窄屏 Tab 退化。

### 13.4 Skill 契约测试

- 选题候选、原子建卡、深化、`可写稿` 校验和并发写锁。
- 二创完整发布包与结构化结果。
- 口播稿去除换行后逐字还原连续文案，不漏段、不改词。
- 增强模式只使用完整爆款文案、approved 片段和可追溯公开资料。
- 混剪 manifest、PLAN、批准门、确定性 builder、明文 QC 和结果清单。
- 默认不启动剪映、不做 UI 冒烟测试。

### 13.5 验收场景

1. 空项目只上传爆款原文，系统只允许二创，不允许混剪。
2. 无项目状态完成候选讨论、深化和选题卡确认，再创建项目。
3. 二创任务实时显示 Prompt、事件和结果，并登记连续文案和口播稿。
4. 修改连续文案后，下游被标记过期但旧文件仍可查看。
5. 上传配音与 SRT 后，混剪要求同时解析账号背景图并生成 manifest。
6. Codex 提问后网页回复，同一 session 继续并产生最终结果。
7. Skill 文件更新后，新任务使用新指纹，等待中的旧任务提示替代。
8. 四个不同项目可并行，剪映注册仍严格串行。
9. 重启控制台后，任务、消息、事件和资产版本仍可追溯。

## 14. 推荐实施顺序

1. 认证、设置、密钥保护和数据库迁移基础。
2. Task manifest、Prompt、output schema、Runner JSONL 解析和实时续聊。
3. 资产版本、预览、依赖失效和 Requirements Evaluator。
4. 无项目选题工作台与 Obsidian 卡片生命周期。
5. 三个 Skill 的显式动作、结果信封和契约测试。
6. 前端路由、首页、看板、三栏项目工作台、任务详情和设置页。
7. 混剪 machine profile 与确定性 builder。
8. 旧数据迁移演练、并发测试、安全测试、文档和最终验收。

## 15. 明确不在本次范围

- 微信视频号自动发布与 UI 测试。
- 默认启动剪映或剪映 UI 自动化测试。
- 多管理员、角色和权限系统。
- 在网页中直接编辑 `SKILL.md` 正文。
- 自动生成配音或自动生成 SRT 的新 Skill；本次只保留上传与未来扩展接口。
- 将本地单体拆成云端多服务。
- 删除旧项目、旧资产、旧任务或覆盖现有剪映草稿。

## 16. 已知现状与必须修复的代码点

- `internal/codex/prompt.go` 当前把混剪映射到 `chatcut-finance-video`，必须改为 `jianying-montage-draft`。
- `cmd/console/main.go` 的 CLI 命令必须加入 `--output-schema` 和 `--output-last-message`。
- `internal/codex/runner.go` 必须解析 `item.completed` 的 `agent_message.text`，不能只依赖 `turn.completed.result`。
- `topic_card_path` 必须从结构化输出登记为项目资产。
- Codex 问题与 options 必须持久化到消息表并进入 `awaiting_input`。
- 资产 ID 改为标准 UUID，版本递增，MIME 真实识别，工程文件不再混入正式资产。
- 创建任务时从项目读取账号，拒绝前端篡改账号归属。
- Scheduler 必须从数据库读取并动态应用并发设置，而不是启动固定为 2。
- 控制台保持 `0.0.0.0` 局域网访问前，必须先上线全站登录保护。

本设计没有未决的功能选择。后续实施计划必须以本文件为唯一产品与系统边界，不得在未获得新确认时扩展范围。
