# Codex 对话工作台、移动端与混剪真实交付设计

日期：2026-08-05  
状态：已完成产品确认，待用户复核书面规格  
目标系统：`video-production-console`

## 1. 设计结论

控制台继续采用本地 Go 模块化单体、SQLite、React 和一个共享数据目录，不拆分为云服务。Codex 执行层从“一次性 `codex exec` 子进程”渐进迁移到“控制台持有的长驻 Codex App Server”，以获得真正的双向对话、运行中引导、统一历史和稳定事件流。

本次改造同时解决四类问题：

1. 任务详情从原始事件列表改成类似 Codex 的聊天工作台。
2. 首页增加通用 Codex 对话，并统一展示控制台对话与本机 Codex 历史。
3. 手机可在任务运行中发送引导消息，不中断当前任务。
4. 混剪任务只有真正注册进剪映后才能显示完成；本地草稿、注册状态和产物必须可见。

本设计增量覆盖 `2026-08-03-video-console-workflow-v2-design.md` 中的任务详情、Codex 执行协议、移动端、故障恢复和相关验收条款。旧设计中认证、资产版本、选题、二创、设置和 Skill 契约仍然有效；冲突处以本文件为准。

## 2. 已确认的产品决策

- 首页同时显示“控制台对话”和“本机 Codex 历史”，但明确标记来源。
- 本机历史默认显示最近 10 条，设置中允许调整为 5—50 条。
- 过滤子代理和已归档会话，并与控制台已经接管的会话去重。
- 历史会话保留两个入口：`继续原会话` 和 `复制为新对话`。
- 控制台管理的运行中任务支持 `turn/steer`，不先中断任务。
- 空闲或已完成会话发送消息时使用 `turn/start`；需要稍后执行时可选择“排队到下一轮”。
- 每个视频项目拥有独立主 Codex 会话；通用对话、选题会话和项目会话彼此隔离。
- 现有正在运行或等待回复的 `codex exec` 任务不迁移、不取消、不重启，继续由旧适配器完成。
- 新架构不把 App Server 端口直接暴露给局域网；手机只访问带登录和 CSRF 保护的控制台。
- 混剪 Codex 只在托管工作区生成和校验明文草稿，剪映注册由控制台宿主侧受控执行。
- 不打开微信视频号测试；自动测试不启动剪映，也不进行剪映 UI 自动化。

## 3. 方案比较与最终选择

### 3.1 继续扩展 `codex exec`

优点是改动小，但 stdin 是一次性输入，运行中无法继续双向发送消息。所谓“续聊”只能等当前进程结束后执行 `codex exec resume`，无法满足手机实时引导。

不采用为新任务的长期方案，仅作为旧任务兼容层保留。

### 3.2 每条消息启动一个 CLI/TUI 进程

可以模拟交互，但进程控制、终端转义、窗口尺寸、恢复和并发都脆弱，内存占用也难控制。

不采用。

### 3.3 控制台托管 Codex App Server

采用。控制台后端通过 App Server JSON-RPC 管理 `thread/start`、`thread/resume`、`thread/fork`、`turn/start`、`turn/steer` 和 `turn/interrupt`，浏览器只接收控制台整理后的消息和阶段事件。

该方案能够保留 Codex 原生 thread/turn 语义，支持运行中引导，也能以增量方式兼容旧任务。

## 4. 总体架构

```text
电脑或手机浏览器
  首页 / 项目 / 对话 / 设置
  聊天气泡 / 中文阶段 / 产物卡 / 固定输入框
                │
       登录会话 + CSRF + WebSocket
                ▼
Go 控制台
  Conversation Service
  Thread Broker
  App Server Manager
  Semantic Event Projector
  Legacy Exec Adapter
  History Adapter
  Montage Registrar
                │
       ┌────────┴────────┐
       ▼                 ▼
Codex App Server       SQLite + 托管文件
threads / turns        消息、任务、事件、产物、资产
       │
       ├── Skills / MCP / 爆款库 / Obsidian
       └── 托管工作区

Montage Registrar（宿主权限、串行锁）
       └── 剪映草稿目录
```

App Server 默认使用本机 stdio 或仅回环地址传输。任何 App Server 凭据、访问令牌和原始 RPC 都不得下发到浏览器。

## 5. Codex 会话与运行中引导

### 5.1 会话归属

- `general`：首页通用 Codex 对话，可选择工作目录、模型和 Skill。
- `idea`：无项目选题会话。
- `project`：单个视频项目的主会话，选题深化、二创和混剪任务作为其中的明确工作回合。
- `history`：从本机 Codex 历史继续或派生的会话。

同一项目默认只保留一个主 thread，确保上下文连续。用户需要尝试另一条路线时使用“复制为新对话”，底层调用 `thread/fork`，不会污染原 thread。

### 5.2 输入框路由

统一消息接口接收：

```json
{
  "text": "修改开头，先强调存款利率跌破 1%",
  "delivery": "auto"
}
```

`delivery` 支持：

- `auto`：活动 turn 存在时执行 `turn/steer`，否则执行 `turn/start`。
- `steer`：强制加入当前 turn；没有活动 turn 时返回明确冲突，不静默丢失。
- `queue`：控制台保存消息，收到 `turn/completed` 后自动开始下一 turn。

运行中引导必须携带当前 `expectedTurnId`。App Server 接受后，界面将用户气泡标记为“已加入当前任务”。`turn/steer` 不允许更改模型、工作目录、沙箱或输出结构；需要更换这些配置时必须新建或 fork 会话。

### 5.3 竞态处理

若用户发送时当前 turn 恰好结束：

1. Thread Broker 刷新 thread 状态。
2. `auto` 消息自动转为下一 turn，并在气泡旁显示“当前步骤刚结束，已作为下一轮发送”。
3. 显式 `steer` 消息保持未发送，允许用户选择“作为下一轮发送”或撤回。

每条发送请求带控制台生成的幂等键。网页断线或重复点击不得产生重复 steering。

### 5.4 提问、审批和普通消息

- 模型在回合结束后提出普通问题：下一条用户消息使用 `turn/start`。
- App Server 发出结构化审批请求：界面显示批准或拒绝按钮，由专用审批 RPC 处理，不伪装成聊天文本。
- Skill 返回结构化 `awaiting_input`：保存问题和选项；用户回答后继续同一 thread。
- 原始审批 payload 和技术字段只在诊断面板显示。

## 6. 本机历史与双入口

### 6.1 历史发现

History Adapter 优先通过 App Server 的 thread 列表和读取能力获取会话；只在需要补充来源信息时，以只读方式访问本机 Codex 状态索引。控制台不得改写 `state_5.sqlite` 或直接修改 rollout 文件。

历史列表：

- 默认最近 10 条，可配置 5—50。
- 排除 `subagent`、已归档和无用户内容会话。
- 标记来源：桌面版、CLI、控制台任务。
- 按 thread ID 与控制台会话去重。
- 列表只加载标题、预览、来源、模型和最近时间；正文在打开时按需读取。

### 6.2 继续原会话

`继续原会话` 调用 `thread/resume`，保留原 thread ID，并为它建立控制台会话映射。之后由控制台发起的 turn 可以在电脑或手机上继续，并支持运行中 steering。

如果该 thread 此刻正由另一个 Codex 进程执行活动 turn，控制台不得创建第二个写入者。界面显示“正在桌面端运行”，提供：

- 等当前 turn 结束后接管；
- 复制为新对话。

只有控制台连接到拥有该活动 turn 的同一个 App Server 时，才允许直接 steering。该边界必须如实展示，不承诺跨两个独立 App Server 强行注入消息。

### 6.3 复制为新对话

`复制为新对话` 使用 `thread/fork` 建立新 thread，原历史只读保留。新会话可独立选择工作目录和后续任务，不影响桌面版原会话。

## 7. 页面与交互结构

### 7.1 信息架构

桌面端主导航：

```text
首页 | 视频项目 | Codex 对话 | 设置
```

移动端使用固定底部导航：

```text
首页 | 项目 | 对话 | 设置
```

账号筛选只影响视频项目和项目相关任务，不过滤通用 Codex 对话。本机历史拥有独立来源筛选。

### 7.2 首页

首页先回答“现在应该做什么”，不使用通用统计后台模板：

1. `开始选题`、`已有爆款原文`、`新建 Codex 对话`三个主动作。
2. 正在运行、等待回复和失败待处理的任务。
3. 最近视频项目。
4. 最近控制台对话。
5. 本机 Codex 历史，默认 10 条。

历史卡片显示标题、来源、更新时间和两个入口，不在首页展开长正文。

### 7.3 聊天工作台

桌面端采用三部分：

```text
┌──────────────┬────────────────────────────┬─────────────────┐
│ 会话与来源    │ 对话、阶段、产物            │ 项目上下文/素材  │
│ 新建/搜索/删  │ 固定底部输入框              │ 配置/诊断        │
└──────────────┴────────────────────────────┴─────────────────┘
```

移动端一次只显示主对话；会话列表和上下文改为独立页面或底部抽屉。输入框始终固定在安全区上方，按钮尺寸适合触控。

任务详情不再使用点击遮罩就消失的临时模态框，而是使用有 URL 的持久页面。刷新、后退或切换页面后，仍能回到同一会话和滚动位置。

### 7.4 消息类型

- 用户消息气泡。
- Codex 正文气泡。
- 中文阶段卡：正在读取任务、检查素材、搜索资料、生成方案、执行、校验、完成。
- 工具状态：只显示用户能理解的动作和结果，不展示模型内部推理。
- 问题或审批卡。
- 产物卡：名称、类型、状态、查看、打开、下载或重试。
- 错误卡：发生位置、已保留内容和下一步动作。

原始 JSONL、英文日志、完整命令和 stderr 放在“技术详情”折叠区，支持搜索、复制和下载，默认不渲染数百条事件。

### 7.5 项目归属

项目页面只显示当前视频项目的文案、配音、SRT、草稿和成片。账号固定背景图单独显示为“继承自账号”，不得与其他项目资产混在一起。

项目进度使用真实制作阶段：

```text
选题 → 文案 → 配音与字幕 → 混剪草稿 → 成片
```

每个阶段显示“缺什么、有什么、下一步做什么”，不得用阶段标签暗示资产可以跨项目共用。

## 8. 语义事件投影

原始 App Server 通知和旧 CLI JSONL 全部保留，但默认 UI 消费统一的语义事件：

```text
phase_started
phase_progress
assistant_message
user_question
approval_required
artifact_ready
warning
failure
turn_completed
```

Semantic Event Projector 根据任务 action、App Server item 类型、工具名和结果状态生成中文阶段，不用模型二次总结关键状态。相同阶段的高频 delta 合并为一张实时卡，避免页面和数据库出现数百条重复内容。

WebSocket 继续使用递增序号和 `after` 断线回放。浏览器重连后只补发缺失的语义事件；原始事件通过独立诊断接口按页读取。

## 9. 数据模型

采用增量表和可空外键，不破坏现有记录：

### 9.1 新增

- `chat_sessions`：控制台会话、类型、来源、Codex thread ID、项目/选题关联、标题、所有者和状态。
- `chat_messages`：用户、助手、系统、阶段、产物和错误消息；保存外部 item ID 与发送状态。
- `chat_turns`：turn ID、状态、模型、推理强度、起止时间和错误。
- `chat_outbox`：幂等发送与下一轮排队消息。
- `semantic_events`：面向 UI 的阶段事件和递增序号。
- `thread_leases`：防止同一 thread 被两个控制台执行器同时写入。

### 9.2 扩展

`codex_tasks` 增加：

- `chat_session_id`
- `codex_thread_id`
- `codex_turn_id`
- `transport`：`legacy_exec` 或 `app_server`
- `completion_phase`

旧 `task_messages` 和 `task_events` 保留。旧任务仍从原表读取；新任务的聊天投影写入新表，并保留任务关联。

## 10. App Server 生命周期与资源控制

- 控制台启动后按需启动一个 App Server 管理器，不为每条消息创建新 Node 或 CLI 常驻进程。
- 最多同时运行 4 个 Codex turn，继续服从设置页并发限制。
- 同一项目只允许一个写入型 turn 活动；不同项目可并行。
- 浏览器关闭不终止 turn；控制台继续接收并持久化事件。
- App Server 异常退出时，管理器记录错误并限次重连，不循环拉起大量进程。
- 服务关闭只结束自己启动且仍持有的 App Server，不批量结束桌面客户端、其他 CLI、Node 或代理进程。
- 事件 delta 合并写入，原始日志按任务分文件并设置保留上限，避免长期占用内存。

## 11. 兼容迁移

迁移按能力开关逐步执行：

1. 新增表、App Server 健康检查和只读历史列表，不改变现有任务。
2. 首页通用对话和新建历史 fork 先使用 App Server。
3. 新建选题会话切换到 App Server。
4. 新建项目任务切换到 App Server；旧任务仍由 Legacy Exec Adapter 展示和恢复。
5. 稳定后停止创建新的 legacy 任务，但保留旧记录读取。

迁移期间：

- `running / resuming / awaiting_input` 的旧任务绝不自动转换 thread。
- 已完成任务保持原状态，除非被完整性审计识别为“假完成”。
- 数据库迁移先备份、事务执行、失败回滚。
- 任何阶段都可以关闭 App Server 新任务开关并回到 legacy 创建路径，不删除新表。

## 12. 混剪真实交付修复

### 12.1 已确认的现有故障

任务 `0dfc0068-670d-4b9d-8c57-441d1c8aa9bd` 已生成 279.38 秒、17 段的本地明文草稿，配音、BGM、音效和转场校验通过，但写入 `.jianying-registration.guard` 时权限不足，剪映注册失败。最终结果仍返回 `registered_path: null` 和 `status: completed`，控制台又把本地目录登记为 ready，形成“页面完成、剪映无结果”。

### 12.2 权限边界

Codex 使用 `workspace-write`：

1. 读取 manifest 和项目输入。
2. 生成 `production_plan`。
3. 在任务 `output_dir` 内构建明文草稿。
4. 完成确定性 QC。

Codex 不直接写 AppData 锁目录或剪映全局草稿目录。控制台的 Montage Registrar 在 Codex 回合结束、结果通过校验后，以当前登录用户的宿主权限执行注册。

### 12.3 注册流程

```text
明文草稿校验通过
→ 获取 data_root/locks/jianying-registration.lock
→ 再次验证 machine profile 与目标根目录
→ 复制到剪映根目录下的临时目录
→ 校验 draft_content.json / draft_meta_info.json 与目录指纹
→ 原子改名为正式草稿目录
→ 写 registration_result.json
→ 数据库事务登记 mix_draft ready
→ 任务完成并推进项目阶段
```

注册失败时：

- 任务状态为 `failed`，错误码 `registration_failed`。
- 明文草稿作为工程产物保留，不能登记为 ready 的正式 `mix_draft`。
- 页面显示“草稿已生成，注册剪映失败”，提供“只重试注册”。
- 重试注册不重新调用 Codex，也不重新生成文案或选镜计划。

### 12.4 完成门

`montage.execute` 只有同时满足以下条件才能完成：

1. 制作方案和明文草稿 QC 通过。
2. `registered_path` 是非空绝对路径，且位于配置的 `jianying_root` 内。
3. 注册目录存在且包含必需草稿文件。
4. 注册结果指纹与明文工作区一致。
5. `registration_result.json` 明确为成功。
6. 正式资产版本和任务结果在同一数据库事务中登记成功。

结果契约对 `montage.execute + completed` 禁止 `registered_path: null`。本地工作区只能作为 `plaintext_workspace` 工程产物，不得冒充正式混剪草稿。

### 12.5 历史假完成审计

升级时只读扫描已完成混剪任务。若出现 `registered_path` 为空、路径不存在或缺少注册回执：

- 保留所有原文件和原始事件。
- 把错误登记的 `mix_draft` 版本标为 failed 或 stale。
- 重新计算项目阶段。
- 添加“历史任务完整性异常”审计记录和“只重试注册”入口。

不得直接删除旧资产或覆盖剪映现有草稿。

### 12.6 结果展示

混剪结果卡分为：

- 制作方案：可直接预览 Markdown。
- 素材使用：17 个片段及来源摘要。
- QC：时长、旁白、BGM、音效、转场和异常项。
- 剪映注册：未开始、注册中、成功或失败。
- 正式草稿：桌面端打开目录；手机端查看路径、清单和下载安全导出包。

目录型资产不再调用普通文件 `/content` 接口。后端提供受控的目录清单、打开目录和可选打包下载接口。

## 13. 视觉系统

主题是“单人视频制作台”，而不是通用企业后台。

### 13.1 设计令牌

- `Workbench` `#F3F6F7`：整体冷灰工作台背景。
- `Ink` `#17212B`：标题和主要文字。
- `Slate` `#314454`：导航、轨道和次级结构。
- `Record` `#E85D3F`：运行中 playhead 与关键动作。
- `Ready` `#238469`：成功和可交付。
- `Amber` `#C78A2C`：等待回复和需要处理。

中文正文优先使用 `HarmonyOS Sans SC`、`Microsoft YaHei UI` 系统字体；数据、路径和任务 ID 使用 `Cascadia Mono`。不依赖公网字体。

### 13.2 签名元素

每个项目顶部使用真实的“制作时间线轨道”，以 playhead 表示当前阶段。轨道节点对应选题、文案、配音字幕、混剪草稿和成片；点击节点进入该项目对应资产和会话位置。它编码真实状态，不作为装饰。

运行时只让 playhead 做一次克制的脉冲动画，其他区域保持稳定；支持 `prefers-reduced-motion`。

### 13.3 组件约束

- 减少大面积圆角卡片堆叠，使用开放分区、细分隔线和 6—10px 小圆角。
- 主要按钮使用具体动作：`发送引导`、`继续对话`、`只重试注册`，不使用“提交”。
- 空状态直接说明缺失内容和下一步。
- 错误不只显示红色提示，必须说明保留了什么、失败在哪一步以及可执行动作。
- 键盘焦点、触控尺寸、颜色对比和移动安全区必须纳入验收。

## 14. 安全与局域网

- 全站继续使用单管理员登录口令、HttpOnly Session、SameSite 和 CSRF。
- WebSocket 握手验证登录会话、Origin 和 CSRF 派生令牌。
- App Server 只监听 stdio、Unix socket 或回环地址，不监听 `0.0.0.0`。
- 手机端无法获取 App Server token、Codex 登录凭据、Grok 密钥或本机绝对任意文件。
- 附件和资产只通过数据库 ID 选择，后端解析可信路径并校验根目录。
- 技术日志在返回网页前统一脱敏。

## 15. 错误和恢复

- App Server 未启动：页面显示依赖异常，消息保留为未发送，可手动重试。
- WebSocket 断线：任务继续，重连后按序号补发。
- steering 被拒绝：保留消息，显示是 turn 已结束、ID 不匹配还是 thread 不活动。
- 控制台重启：恢复已知 thread 映射；无法确认的活动 turn 标记为“状态确认中”，不重复执行。
- Provider 余额或鉴权失败：显示具体 Provider/模型和原始错误摘要，不自动换模型。
- Skill 或 MCP 失败：阶段卡显示失败点，原始工具日志留在诊断面板。
- 注册剪映失败：保留本地草稿并允许只重试注册。

## 16. API 边界

主要新增接口：

- `GET/POST /api/chat/sessions`
- `GET/DELETE /api/chat/sessions/{id}`
- `POST /api/chat/sessions/{id}/messages`
- `POST /api/chat/sessions/{id}/fork`
- `GET /api/chat/sessions/{id}/events?after=`
- `GET /api/codex/history?limit=&source=`
- `POST /api/codex/history/{thread_id}/resume`
- `POST /api/codex/history/{thread_id}/fork`
- `GET /api/tasks/{id}/artifacts`
- `GET /api/tasks/{id}/diagnostics`
- `POST /api/tasks/{id}/retry-registration`
- `GET /api/assets/{id}/directory-manifest`

浏览器永远不提交任意 Codex thread 文件路径或 App Server RPC。服务端验证会话归属、项目归属和当前 turn 后再转发。

## 17. 测试与验收

### 17.1 App Server 协议

- fake App Server 覆盖 thread start/resume/fork、turn start/steer/completed/interrupt。
- 活动 turn steering 不产生新 turn。
- `expectedTurnId` 竞态按设计转换或保留消息。
- 排队消息只在当前 turn 完成后发送一次。
- App Server 崩溃、重连和幂等发送不重复任务。

### 17.2 历史和迁移

- 默认 10 条、5—50 设置、来源过滤、子代理/归档过滤和去重。
- 继续原会话保留 thread ID；复制使用新 thread ID。
- 另一运行时持有活动 thread 时禁止双写。
- 旧 legacy 任务不被中断，旧消息和事件仍可查看。

### 17.3 UI

- 桌面三栏和手机单栏/底部导航。
- 刷新或误点页面不会丢失任务对话框。
- 运行中输入框显示“发送引导”，空闲时显示“继续对话”。
- 中文阶段默认可读，技术日志默认折叠。
- 产物卡可预览文本、播放媒体、展示目录清单。
- 项目资产归属清晰，账号背景图单独标记继承。

### 17.4 混剪

- Codex 沙箱只写任务 output 目录。
- 注册器锁、临时目录、原子改名、路径边界和指纹校验。
- `registered_path: null` 不能完成。
- 注册失败不产生 ready 的 mix_draft，并可只重试注册。
- 历史假完成审计不删除文件、不覆盖现有剪映草稿。
- 所有自动测试使用临时假剪映目录，不启动真实剪映。

### 17.5 完成标准

1. 手机可以查看运行进度，并在控制台管理的活动 turn 中发送 steering。
2. 首页可以新建通用对话、查看最近 10 条历史并使用双入口。
3. 任务详情以聊天、阶段和产物展示，原始日志不再淹没页面。
4. 页面刷新和切换不丢失会话，消息交付状态可追踪。
5. 旧任务不中断，新任务可按开关迁移到 App Server。
6. 混剪只有注册成功才显示完成，失败时本地草稿仍可见并可重试注册。
7. 桌面端和手机端均通过局域网登录安全使用。

## 18. 实施分解

本设计是一项完整产品改造，但代码施工拆成四个可独立验收的工作包：

1. App Server Gateway、会话表、消息路由和 Legacy 兼容。
2. 历史双入口、首页通用对话和移动端会话体验。
3. 语义事件、聊天式任务详情、产物与诊断面板。
4. Montage Registrar、严格完成门、历史假完成审计和混剪结果卡。

工作包按上述顺序集成。第 4 包可在第 1 包完成后并行施工，但上线时必须与严格结果契约同时启用，不能只改页面状态。

## 19. 明确不在本次范围

- 微信视频号自动发布。
- 剪映 UI 自动化和真实剪映自动化测试。
- 多管理员和角色权限。
- 把 App Server 直接开放到公网或局域网。
- 强行 steering 由另一独立 App Server 正在执行的桌面端活动 turn。
- 在网页中直接修改 Skill 正文。
- 自动切换 Provider 或在余额不足时静默换模型。
- 删除旧任务、旧草稿或覆盖现有剪映项目。

本设计没有待定产品选择。用户复核书面规格后，下一步是按四个工作包编写可执行的代码实施计划。
