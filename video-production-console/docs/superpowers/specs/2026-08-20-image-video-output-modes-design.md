 # 图文制作输出模式设计规格
 
 ## 1. 基线与目标
 
 当前 `/image-projects` 仅提供分段、生图、预览和 ZIP 导出，不具备配音或剪映草稿能力。本设计在该入口新增专用图文视频作业器，并复用 `internal/narration` 与可信登记的底层能力，新增两种持久化、可恢复、可审计的输出模式：`image_slideshow`（图片视频，默认）和 `image_to_video`（图生视频）。模式在作业开始后锁定。
 
 用户输入是完整文案，不是“主题”。系统基于完整文案生成语义分段，再为每段生成图片、配音和视频媒体。
 
 ## 2. 范围
 
 共享分段、生图、配音和剪映草稿登记链路。图片视频使用图片加轻推拉、平移、淡入淡出；图生视频以分段图片为输入生成短视频。不做 Vox、字幕恢复或可编辑标题。图片内中文标题属于生图资产，视频处理不得拉伸、遮挡或裁掉标题。不改造 `/projects?mode=image-video`，两条产品线保持隔离。
 
 ## 3. 用户流程
 
 1. 用户在 `/image-projects` 创建项目并提交完整文案，生成语义分段和图片。
 2. 用户选择输出模式，并选择发布账号与模板 profile。历史纯 ZIP 项目可以继续预览和导出 ZIP。
 3. 用户确认开始；服务端在同一事务中校验并锁定 `output_mode`、`account_id` 和 `template_profile_id`，创建专用作业及幂等记录。
 4. 作业器复用 `internal/narration` 生成配音和对齐后的分段时长，按模式生成媒体，逐镜头记录每次尝试。
 5. 所有镜头成功后才生成剪映草稿并可信登记；任何镜头最终失败，作业明确失败，不生成部分失败草稿。
 6. 刷新页面从服务端恢复状态。用户可只重试最终失败镜头；重试成功后再汇总。
 
 ## 4. 领域模型与迁移
 
 `image_projects` 增加：
 
 ```
 output_mode varchar not null default 'image_slideshow'
 output_mode_locked_at timestamptz null
 account_id uuid null
 template_profile_id uuid null
 ```
 
 历史记录迁移为 `image_slideshow`，`account_id` 允许为空以保持纯 ZIP 项目可用。视频作业创建请求必须提供有效账号和模板 profile；服务端在开始时锁定二者，锁定后拒绝变更。
 
 新增 `image_video_jobs`：
 
 ```
 id, project_id, output_mode, account_id, template_profile_id, status,
 model, resolution, concurrency_limit, retry_round, error_code, error_message,
 lease_owner, lease_expires_at, version, created_at, started_at, finished_at, updated_at
 ```
 
 `status` 为 `pending|running|succeeded|failed|canceled`；`output_mode`、账号和模板在创建后不可变。租约字段和 `version` 用于崩溃恢复与并发控制。
 
 新增 `image_video_job_items`：
 
 ```
 id, job_id, segment_id, ordinal, input_image_asset_id, output_video_asset_id,
 requested_duration_seconds, actual_duration_seconds, status, attempt, max_attempts,
 provider_request_id, error_code, error_message, started_at, finished_at, updated_at
 ```
 
 `requested_duration_seconds` 仅允许 6、10、15；`actual_duration_seconds` 是下载后探针确认并标准化后的真实时长。新增 `image_video_job_attempts` 保存每次尝试的历史：
 
 ```
 id, job_item_id, retry_round, attempt, idempotency_key, provider_request_id,
 request_fingerprint, status, error_code, error_message, output_video_asset_id,
 started_at, finished_at, created_at
 ```
 
 自动一轮固定为首次加 2 次重试；手动“只重试失败镜头”必须开启新的 `retry_round`，该轮最多 3 次，并保留此前全部历史，绝不降级到另一输出模式。错误文本脱敏。
 
 迁移可重复执行，外键只关联现有项目和分段，删除不得误删共享图片、配音资产；旧通用运行记录继续可读，新链路只读专用作业表。
 
 ## 5. 时长与语义分段
 
 配音生成后，以最终配音对齐结果确定每个语义分段的目标时长，并选择能够覆盖该时长的最小档位（6、10、15 秒）。超过 15 秒必须在语义边界拆成多个子镜头；不得截断口播。图片视频的前 30 秒单图目标为 4.3–4.6 秒、后续目标至少 6 秒，但这些约束也必须通过语义分段和配音对齐实现，不能截断或硬切配音。剪映时间线须校验总时长、连续性和音画对齐。
 
 ## 6. 模式行为
 
 ### 6.1 图片视频 `image_slideshow`
 
 默认模式。每个语义子镜头使用对应图片，应用轻推拉、水平/垂直平移和淡入淡出；禁止夸张变形、闪烁、拉伸或覆盖图片内标题。
 
 ### 6.2 图生视频 `image_to_video`
 
 固定模型 `grok-imagine-video-1.5`，输出目标 `480x848`、24fps，最大并发 6，档位为 6、10、15 秒。请求提示词要求保持图片内中文标题稳定、清晰、位置和字形不变。前端仅显示模型、分辨率、帧率、时长和并发说明，不显示接口地址、令牌或密钥。
 
 供应商响应元数据不可信：下载完成后必须真实探针并完整解码，确认视频轨道、帧率、尺寸和可播放性。服务可能返回 `400x736`；服务端必须等比例补边或裁切到 `480x848`、24fps，禁止非等比拉伸，裁切不得裁掉图片内标题。探针或完整解码失败即该镜头失败，写入 attempt 历史并进入重试。
 
 ## 7. 失败、重试与恢复
 
 每镜头自动一轮为首次加 2 次重试，每次持久化 request fingerprint、请求标识、错误码、脱敏错误和输出资产。三次失败后 item/job 为 `failed`，无静默降级、无部分失败草稿。手动重试只选最终失败镜头，开启新 `retry_round`，最多 3 次；成功镜头不可覆盖或重复生成。
 
 作业领取使用租约和 version 条件。进程启动扫描租约过期的 `running` 作业，安全重新排队未完成项；已有成功输出资产的 item 不重复覆盖。幂等键至少为 `job_id + segment_id + retry_round + attempt`，供应商重放可识别。
 
 ## 8. API
 
 - `POST /api/image-projects`：提交完整文案；可选 `output_mode`，缺省 `image_slideshow`；纯 ZIP 项目可不提供账号和模板。
 - `PATCH /api/image-projects/:id/output-mode`：仅未开始且未锁定时修改。
 - `POST /api/image-projects/:id/image-video-jobs`：携带模式、`account_id`、`template_profile_id`、幂等键和图生视频参数；服务端校验并原子锁定。
 - `GET /api/image-video-jobs/:id`：返回锁定模式、账号/模板公开标识、状态、进度、尝试历史摘要和可重试镜头。
 - `POST /api/image-video-jobs/:id/retry-failed`：开启新 retry round，仅处理最终失败镜头。
 - `POST /api/image-video-jobs/:id/cancel`：取消未完成作业，资产保留但不可发布。
 
 响应只含稳定 `code`、用户可读 `message`、`job_id` 和 `item_ids`，不返回凭据、供应商私有 URL 或完整上游响应。
 
 ## 9. UI
 
 开始前显示完整文案、模式、账号、模板 profile 和确认按钮；纯 ZIP 历史项目显示原有预览/ZIP 操作。视频开始后显示模式、账号和模板锁定徽章，选择器禁用。运行中展示每个镜头状态、`requested_duration_seconds`、`actual_duration_seconds`、当前 retry round 和尝试次数。失败面板提供“只重试失败镜头”；成功后展示剪映草稿和可信登记状态。刷新优先读取服务端，不得解除锁定或创建重复作业。
 
 ## 10. 幂等边界与安全
 
 账号和 template profile 以稳定 ID、版本/快照指纹绑定到视频作业；账号校验、模板校验和锁定使用同一开始事务，重复幂等请求返回同一 job。配音资产按 `project_id + narration_input_fingerprint + voice_profile` 幂等，生成成功后复用，不因镜头重试重复生成。媒体作业按上述 attempt 幂等键；剪映草稿登记以 `job_id + template_profile_id + ordered_asset_fingerprint` 幂等，草稿生成和可信登记分开记录，可从成功媒体作业重试登记。
 
 同一项目同时只能有一个活动视频作业；数据库唯一约束、租约和幂等键共同防止双击、重复消费和超限并发。凭据仅由服务端运行时注入，日志、追踪和 API 统一脱敏，输入图片、完整文案、提示词和回调按项目权限隔离。
 
 ## 11. 验收标准
 
 - 迁移后旧项目为 `image_slideshow`，纯 ZIP 仍可用；视频作业强制账号和模板，开始后模式、账号、模板、作业均锁定。
 - 完整文案可生成语义分段；最终配音对齐后，档位仅为 6/10/15，超过 15 秒按语义边界拆分，任何模式均不截断口播；图片视频验证 4.3–4.6 秒及后续至少 6 秒约束。
 - 图生视频验证 `grok-imagine-video-1.5`、`480x848`、24fps、并发 6；对 `400x736` 等返回执行等比补边/裁切；探针、完整解码失败进入重试，标题不被拉伸或裁掉。
 - 自动首次加 2 次重试，每次 attempt 有独立历史；手动失败镜头重试开启新 retry round、最多 3 次，成功镜头不重跑，不降级，不生成部分失败草稿。
 - 模拟超时、5xx、无效媒体、崩溃、租约过期、重复请求和并发领取，验证恢复、version 条件、幂等和资产不覆盖。
 - 仅全量成功才生成剪映草稿并可信登记；配音资产、账号/template 绑定、草稿登记的幂等边界和日志脱敏通过测试。
 - 搜索规格文件确认不存在 `TBD`、`TODO`、占位符、错误的“现有已具备配音草稿”表述或不一致的模式名。
 
 ## 12. 非目标
 
 不实现 Vox；不恢复字幕；不增加可编辑标题；不改造 `/projects?mode=image-video`；不把图生视频失败自动转成图片视频；不向前端暴露供应商接口或密钥；不允许作业开始后切换模式、账号或模板。
 
