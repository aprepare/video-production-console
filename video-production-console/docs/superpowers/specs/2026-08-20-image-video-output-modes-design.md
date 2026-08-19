# 图文制作输出模式设计规格

## 1. 背景与目标

现有 `/image-projects` 图文制作流程已经具备分段、生图、配音和剪映草稿能力。本设计在该入口增加持久化的 `output_mode`，让同一份图文内容可以明确产出图片视频或图生视频，并在作业开始后锁定模式，避免过程中切换导致资源、重试和草稿语义不一致。

本方案确认采用“专用图文视频作业器”：`image_slideshow`（图片视频，默认）与 `image_to_video`（图生视频）是两个持久化、可恢复、可审计的作业模式。当前 `/image-projects` 与 `/projects?mode=image-video` 是两条分离产品线，后者不是本设计入口。

## 2. 范围与明确不做

两种模式共享分段、生图、配音和剪映草稿链路。图片视频对每个分段使用图片并加入轻推拉、平移、淡入淡出；图生视频以分段图片为输入生成短视频。

本次不做 Vox，不恢复字幕，不新增可编辑标题。图片中的中文标题属于生图资产的一部分；图生视频必须尽力保持图片内中文标题稳定。现有 montage v1 忽略 MixPreset 的降级行为不得用于新链路；新作业器必须显式传递并验证模式所需的素材与参数。

## 3. 用户流程

1. 用户在 `/image-projects` 创建项目、输入主题并生成分段与图片。
2. 用户选择输出模式，默认 `image_slideshow`；若选择 `image_to_video`，界面展示模型信息而不展示接口地址或密钥。
3. 用户确认开始。开始请求原子地持久化 `output_mode` 并将项目/作业置为运行中；此后模式只读锁定。
4. 作业器复用分段、生图、配音阶段，按模式生成媒体，逐镜头记录状态、尝试次数和错误。
5. 全部镜头成功后生成剪映草稿并进行可信登记；任一镜头最终失败时作业明确失败，不生成或发布部分失败草稿。
6. 刷新页面或重新进入项目时从服务端恢复锁定模式、总体状态、镜头状态和重试入口。用户可只重试最终失败镜头；重试成功后再继续汇总。

## 4. 领域模型与数据库

项目新增字段：

```text
image_projects.output_mode       varchar not null default 'image_slideshow'
image_projects.output_mode_locked_at timestamptz null
```

`output_mode` 只允许 `image_slideshow`、`image_to_video`。开始作业时写入 `output_mode_locked_at`；锁定后拒绝任何不同模式更新。历史项目迁移为 `image_slideshow`，保持兼容。

建议新增专用表 `image_video_jobs`，避免滥用通用 `RunMode`：

```text
id, project_id, output_mode, status, model, resolution, duration_seconds,
concurrency_limit, attempt_count, error_code, error_message,
created_at, started_at, finished_at, updated_at
```

`status` 为 `pending|running|succeeded|failed|canceled`；`output_mode` 创建后不可变。新增 `image_video_job_items`：

```text
id, job_id, segment_id, ordinal, input_image_asset_id, output_video_asset_id,
status, attempt, max_attempts, provider_request_id, error_code, error_message,
started_at, finished_at, updated_at
```

`attempt` 从 1 开始，`max_attempts=3`。`job_id + segment_id` 唯一，`ordinal` 唯一；状态变更使用事务和版本/更新时间条件，防止重复执行。错误信息需脱敏存储。

## 5. 模式行为

### 5.1 图片视频 `image_slideshow`

这是默认模式。前 30 秒每张图片时长控制在 4.3–4.6 秒；超过 30 秒的后续镜头时长不得低于 6 秒。实际时长按分段与配音时长取整并在剪映时间线上校验。每张图仅使用轻推拉、水平/垂直平移和淡入淡出，禁止夸张变形、闪烁或覆盖图片内标题。

### 5.2 图生视频 `image_to_video`

固定使用 `grok-imagine-video-1.5`，分辨率 `480p`，最大并发数 6；允许时长为 6、10、15 秒。每个镜头以对应图片作为输入，提示词要求保持图片内中文标题稳定、清晰、位置和字形不发生不可接受变化。服务端从现有图片服务运行时凭据复用认证信息；前端只显示模型名、分辨率、可选时长和并发说明，不显示接口地址、令牌或密钥。

两种模式均复用分段、生图、配音和剪映草稿步骤，但媒体生成步骤由专用作业器按 `output_mode` 分派，禁止通过通用 `RunMode` 猜测。

## 6. 失败契约、重试与恢复

每个镜头首次执行后自动重试 2 次，总尝试次数为 3 次。每次尝试必须记录 `attempt`、请求标识（若有）、错误码和脱敏错误文本。三次均失败时镜头为 `failed`，作业为 `failed`，并向用户明确展示失败镜头和最后错误；不得静默降级为图片视频，也不允许部分失败进入剪映草稿。

用户可请求“只重试失败镜头”，服务端仅为最终 `failed` 项目建立新尝试，并保持其余成功产物不变。作业器必须支持进程崩溃、网络超时和页面刷新后的恢复：启动时扫描 `running` 且租约过期的作业，将未完成项安全地重新排队；已成功且有输出资产的项不得重复覆盖。幂等键至少包含 `job_id + segment_id + attempt`，提供商请求重放需可识别。

## 7. API 草案

- `POST /api/image-projects`：创建项目，可选 `output_mode`；缺省为 `image_slideshow`。
- `PATCH /api/image-projects/:id/output-mode`：仅在作业未开始且未锁定时修改模式。
- `POST /api/image-projects/:id/image-video-jobs`：确认并开始作业；请求携带模式、图生视频时长（6/10/15）和幂等键，服务端校验与持久化锁定。
- `GET /api/image-video-jobs/:id`：返回锁定模式、总体状态、模型公开信息、进度、镜头状态和可重试镜头。
- `POST /api/image-video-jobs/:id/retry-failed`：仅重试最终失败镜头。
- `POST /api/image-video-jobs/:id/cancel`：取消尚未完成的作业，已生成资产保留并标记不可用于发布。

错误响应使用稳定的 `code`、用户可读 `message`、`job_id` 和 `item_ids`；绝不返回凭据、供应商私有 URL 或完整上游响应。

## 8. UI 状态

开始前显示模式选择、模式说明和确认按钮；开始后显示锁定徽章，模式选择器禁用。运行中展示总体进度、每个镜头的 `pending/running/succeeded/failed`、尝试次数与可读错误。自动重试期间显示“第 n/3 次尝试”；全部失败时显示明确失败面板和“只重试失败镜头”。刷新后优先读取服务端状态，不能因本地状态丢失而解除锁定或重新创建作业。成功后展示剪映草稿与登记状态。

## 9. 剪映草稿与可信登记边界

只有 `image_video_jobs.status=succeeded` 且所有 items 成功，才允许调用现有剪映草稿生成与可信登记。图片视频将图片、时长、轻动效、配音写入草稿；图生视频将成功的视频资产、配音和分段顺序写入草稿。分段、生图、配音的已有资产可复用，但作业器必须记录输入资产版本与输出资产 ID。草稿生成和登记各自幂等，失败可从成功的媒体作业重新执行，不重新生成镜头。

## 10. 并发、幂等与安全

同一项目同时只能有一个活动图文视频作业；数据库唯一约束、租约和幂等键共同防止双击、重复消费与超限并发。图生视频全局并发上限为 6，队列应在多个项目间公平调度。凭据沿用现有图片服务运行时凭据注入方式，日志、错误、追踪和 API 响应统一脱敏；前端不持有密钥。输入图片、提示词和回调数据按项目权限隔离，供应商请求不得把内部项目标识或敏感元数据暴露给用户。

## 11. 测试与验收标准

- 数据库迁移后旧项目得到 `image_slideshow`，新项目默认该值，非法模式被拒绝。
- 开始请求成功后模式锁定；并发修改、刷新、重复开始均保持同一模式和同一作业。
- 图片视频测试验证前 30 秒镜头为 4.3–4.6 秒、后续镜头至少 6 秒，并验证轻推拉/平移/淡入淡出。
- 图生视频测试验证模型、480p、6 并发上限、6/10/15 秒校验和中文标题稳定提示词；前端快照不得含接口地址或密钥。
- 模拟超时、5xx、无效媒体和进程崩溃，验证初次加 2 次自动重试、每次 `attempt/error` 持久化、最终 `failed`、无静默降级、无部分失败草稿。
- 只重试失败镜头测试验证成功镜头不重复生成，全部成功后才可生成并登记草稿。
- 幂等、权限、日志脱敏、租约恢复和迁移回滚/前向兼容测试通过。

## 12. 迁移兼容

迁移必须可重复执行并为既有记录填充默认模式；新表外键关联现有项目和分段，删除策略不得误删共享图片、配音资产。旧通用运行记录继续可读，但新链路只读取专用作业表。已有 `/projects?mode=image-video` 数据和行为不自动迁移到本入口，两条线保持隔离。

## 13. 非目标

不实现 Vox；不恢复字幕；不增加可编辑标题；不改造 `/projects?mode=image-video`；不把图生视频失败自动转成图片视频；不复用 montage v1 忽略 MixPreset 的降级行为；不向前端暴露供应商接口或密钥；不允许用户在作业开始后切换 `output_mode`。
