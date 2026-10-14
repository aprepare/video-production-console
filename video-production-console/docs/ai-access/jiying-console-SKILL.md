---
name: jiying-console
description: 操作本机「极影智造」视频生产控制台（127.0.0.1:2030）：建账号、商户与订单记账、采集对标与转写、二创出稿、改二创提示词/风格/简报、确认生产（口播·配音·混剪出剪映草稿）、出封面、查任务。用户提到 控制台、极影、二创、对标、混剪、口播稿、配音、封面、发布包、商户订单、认知漫步/居中观/观局思考等账号，或要求“帮我在系统里做一条视频”时使用。改控制台程序代码不走这里。
---

# 极影智造控制台操作手册

控制台通过 MCP 服务 `jiying-console` 暴露全部操作（地址 `http://127.0.0.1:2030/mcp`，只能本机访问）。工具名以 `mcp__jiying-console__` 开头。

## 接入（只需做一次）

控制台「设置 → 系统 → AI 接入（MCP）」可以复制现成配置：

- Claude Code：在终端运行复制来的 `claude mcp add --transport http jiying-console http://127.0.0.1:2030/mcp --header "Authorization: Bearer <令牌>"`
- Codex：把复制来的 `[mcp_servers.jiying-console]` 段粘贴进 `~/.codex/config.toml`

令牌泄露了，就在同一处点「重置令牌」，旧令牌会立即失效。控制台没在运行时工具会连不上，先启动控制台。

## 先读这几条

1. **花钱的操作先问用户**。调用模型、配音、转写、生图、RunningHub，以及删除、覆盖设置或提示词，工具都会要求 `confirm=true`。先用一句话说明要做什么、大概花多少钱，用户同意后再调用。不要替用户默认同意。
2. **ID 只从工具里拿**：`list_accounts`、`list_merchants`、`list_remix_experiments`、`get_remix_experiment` 等。不要编造。
3. **长任务都是异步的**：二创要 1–5 分钟，生产要几分钟到十几分钟，AI 封面要 3–25 分钟，转写要 20–60 秒。提交后轮询状态，不要重复提交。
4. **专用工具没覆盖的操作**：先用 `console_api_catalog`（可带 keyword）查接口，再用 `console_api` 调用，同样遵守第 1 条。
5. 接口严格校验 JSON，不要传接口没有的字段。

## 做一条视频（主流程）

| 步骤 | 工具 | 说明 |
|---|---|---|
| 1 采集对标 | `collect_reference(link, transcribe=true)` | 链接用抖音 v.douyin.com 短链或视频号 weixin.qq.com/sph；转写要付费，先确认 |
| 2 取口播 | `get_reference(id)` → `transcript` | 转写还没好就等一会儿再查 |
| 3 二创 | `start_remix(source=口播, account_id)` | 要花钱，先确认；然后用 `get_remix_experiment` 轮询，直到 `runs[].status=completed` |
| 4 看稿 | `get_publish_package(run_id)` | 正文、短标题、简介、封面大字 |
| 5 改稿（可选） | `rework_run(run_id, annotations)` 或 `save_publish_package` | rework 会调模型，要花钱；save 不花钱，是直接改内容 |
| 6 生产 | `start_production(run_id)` | 依次出口播稿、字幕、配音、混剪和剪映草稿，要花钱，先确认 |
| 7 看进度 | `get_run_stages(run_id)` / `list_tasks(project_id)` | 某一步坏了用 `redo_production_step(step=spoken/captions/narration/montage)` |
| 8 封面 | `generate_cover(run_id, line1, line2)` | 本地出图，免费，字一定准；`generate_ai_cover` 走 RunningHub，约 30 币，出好后要检查字 |

视频号发布文案的规则：短标题是一句话，不带标点和空格，最多 16 字；简介是「短词 + 3–4 个 #话题」；封面大字两行，每行最多 12 字。

## 改二创提示词

| 想改什么 | 工具 |
|---|---|
| 某个账号写手或审稿的提示词 | 先 `get_workflow(account_id)` 看当前内容，再 `set_node_prompt(account_id, node=writer/reviewer/planner, system_prompt=…)` |
| 给账号整套换风格（例如「视频号爆款二创 v2」） | `list_styles` → `apply_style_to_account(account_id, style_id)`，会覆盖账号原有的提示词，先确认 |
| 账号的受众、卖点、人设、禁用词 | `update_account_brief(account_id, …)` |
| 全局默认写手提示词 | `get_active_prompt` / `set_active_prompt` |
| 提示词库里的条目 | `list_prompts` / `save_prompt` / `delete_prompt` |

改之前先读出当前内容，改完告诉用户改了哪里。账号的工作流如果已经单独定制过写手提示词，以账号自己的为准，全局默认对它不生效。

注意：发布包是否自动出短标题、简介，取决于账号用的风格。用户自定义的 rewrite 风格（例如 2026-10-01 那版完整提示词）只出正文。需要自动出发布包，就用 `apply_style_to_account` 切到 `finance_v2`（视频号爆款二创 v2）。

## 账号与商户业务

- 账号：`list_accounts`、`create_account(name, background_path=本机图片)`、`rename_account`、`set_account_overrides`（音色、混剪样式）、`deactivate_account`。
- 商户：`create_merchant`、`update_merchant`、`upload_merchant_material(file_path=本机文件, license_*)`。卖点和授权信息都要真实，不要编造。
- 订单：`create_order`（金额单位是分，800 元写 80000）、`assign_project_to_order`、`record_order_cost(kind=labor/api/material/revision/other)`、`mark_project_delivered`、`business_overview`。

## 系统

- `setup_status`：配置体检，看哪一项没配好。
- `get_settings` / `update_settings`：读改设置（改要先确认）。
- `test_dependency`：检测某个依赖是否连得上。
- `restart_console`：重启控制台，正在跑的任务会被打断，先确认。

## 改控制台程序本身

MCP 只负责操作数据，不负责改代码。要改控制台的功能或修 bug，在项目目录 `C:/Users/prepare/.codex/worktrees/视频号混剪/video-production-console/video-production-console` 用 Claude Code / Codex 修改代码，按项目 `AGENTS.md` 编译测试，然后编译出 `dist/video-production-console.next.exe` 并运行 `scripts/restart-compiled-console.ps1`；或者编译后用 `restart_console` 重启。
