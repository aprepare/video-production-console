# 二创进化台

日期：2026-08-25  
状态：已审阅  
范围：控制台独立页 `/remix-lab` + 后台实验库 + 写手禁空收尾。不改日产风景混剪主路径。

## 1. 要解决什么

日产二创一次只走设置里的一个地址和密钥，同一模型连跑还会被任务去重合成一条，也没有按篇批注。进化提示词需要：同一篇对标、多个模型、每个模型跑几次、每篇单独写想法；对话里说「看进化台」时能直接读库，不用再贴文案。

已定决策：

- 批注只存。不按批注自动改提示词。提示词仍在对话里改，改完新开实验重跑。
- 一次对标 = 一次实验，历史全留。
- 做进现有控制台独立页，不复用项目二创对比，不做本地文件夹探针。
- 某篇可「采用到已有风景混剪项目」，只写该项目的连续文案，不新建项目，不覆盖项目原文。
- 写手只多禁「就这些」这类空收尾，不在本轮大改正文。

## 2. 页面

路由：`/remix-lab`、`/remix-lab/{experiment_id}`。  
`web/src/project-workbench/routes.ts` 的 `AppLocation` 增加 `remix-lab`。`App.tsx` 分支渲染，不挂 `ModeHome` / `ImageVideoStudio` / 选题。

入口：风景混剪看板顶栏，在「设置」旁加「进化台」。图文制作页不强制加。

布局：

- 左：实验历史（时间倒序）。每项显示短标题、时间、状态（跑着 / 完成 / 部分失败）。
- 右：当前实验。
  1. 原文大框。
  2. 模型槽 1～4 个。每槽：地址、模型名、密钥、思考强度、次数（1～3）。
  3. 「开始二创」。
  4. 结果卡：按槽分组，槽内按第 1/2/3 稿。每卡：开头三句、全文、标题候选、失败原因、批注框、「采用到项目」。

默认槽：带出设置里的 `remix_base_url` / `remix_model` / `remix_reasoning_effort`；密钥只显示「已配置」，不回显明文。空地址或空密钥的槽，跑的时候用控制台 `Runtime()` 的二创地址和 `remix_api_key`。

上次用过的额外槽（含加密密钥）记住，新实验自动带上，不用每次重贴 DeepSeek。密钥 GET 永远掩码；空提交表示保持原密钥。

批注：每稿一个文本框，失焦或短防抖即 `PATCH` 保存。没有「按批注改提示词」按钮。

采用：弹出已有风景混剪项目列表（只用现有 `GET /api/projects`）。确认后把该稿 `continuous_script` 写成目标项目当前连续文案版本，提示「已采用，可去项目生成口播」。提供跳到 `/projects/{id}`。不写 `source_script`，不导入发布标题（与现有 `adoptContinuousScript` 一致，只落连续文案）。

## 3. 后台

不创建 `remix.standard` 任务，不进项目，不走 Codex 调度去重。每稿单独临时目录，直接调 `openaicompat.Run`：

- `remix_prompt_style` 固定 `rewrite`
- `BaseURL` / `APIKey` / `Model` / `ReasoningEffort` 来自该槽（空则回落 `Runtime()`）
- `CheckModel` 不传（rewrite 本就跳过质检）
- 每稿独立 `output_dir` 与 `OutputLastMessage`，同一模型两稿不得互相覆盖

进化台 LLM 并发单独限制为 **2**，不占用、不修改 `max_codex_concurrency`。多出来的稿排队。单稿超时沿用现有 HTTP 客户端 10 分钟。一篇失败其他继续；实验状态：全成 = `completed`，有成有败 = `partial`，全败 = `failed`，尚有排队/在跑 = `running`。

产物目录（gitignored 数据根下）：`video-console-data/remix-lab/{experiment_id}/{run_id}/`，保留 `continuous_script.txt`、`publishing_package.json`、`prompt_system.txt`、`prompt_user.txt`、`remix_run.json`。库里再存一份正文和标题，方便列表和我读。

### 3.1 表

新 migration，三张表。

`remix_lab_experiments`

| 列 | 含义 |
|---|---|
| id | uuid |
| title | 原文前 24 字，空则「实验」+ 时间 |
| source_text | 对标原文 |
| prompt_stamp | 开跑时的 `RewritePromptStampStable` |
| status | `running` / `completed` / `partial` / `failed` |
| created_at / updated_at | |

`remix_lab_slots`

| 列 | 含义 |
|---|---|
| id | uuid |
| experiment_id | FK |
| sort_index | 0..3 |
| label | 可选，默认用模型名 |
| base_url | 可空 = 用 Runtime |
| model | 必填 |
| reasoning_effort | 可空 |
| run_count | 1..3 |
| api_key_ciphertext | 可空 = 用 Runtime `remix_api_key`；DPAPI 加密 |

上次槽位只存在 settings 键 `remix_lab_slot_presets`（JSON，其中每条密钥单独 DPAPI，不得明文）。`GET /api/remix-lab/defaults` 返回这些槽的掩码预填。`POST` 某槽 `api_key` 为空时：若带了 `preset_index` 且该预置有密钥，用预置密钥；否则用 `Runtime().RemixAPIKey`。每次实验成功开跑后，用本次槽位覆盖预置。

`remix_lab_runs`

| 列 | 含义 |
|---|---|
| id | uuid |
| experiment_id / slot_id | FK |
| run_index | 该槽第几次，从 1 |
| status | `queued` / `running` / `completed` / `failed` |
| continuous_script | 成功才有 |
| titles_json | 成功才有，来自 publishing_package |
| error_message | 失败才有 |
| comment | 操作员批注，可空，原地更新 |
| output_dir | 相对数据根的路径 |
| adopted_project_id | 最近一次采用到的项目，可空 |
| started_at / finished_at | |

不设批注历史表。一稿一条批注。

### 3.2 HTTP

前缀 `/api/remix-lab/`，走现有登录和 CSRF。`internal/app/app.go` 注册。密钥不出现在任何 GET JSON 里，只回 `api_key_configured: true|false`。

| 方法 | 路径 | 作用 |
|---|---|---|
| GET | `/api/remix-lab/defaults` | 设置回落值 + 上次槽位（密钥掩码） |
| GET | `/api/remix-lab/experiments` | 历史列表，不含全文 |
| POST | `/api/remix-lab/experiments` | 建实验并开跑。body：`source`、`slots[{base_url,model,api_key,reasoning_effort,run_count}]` |
| GET | `/api/remix-lab/experiments/{id}` | 原文、槽、各稿全文、批注、状态 |
| PATCH | `/api/remix-lab/runs/{id}` | `{comment}` |
| POST | `/api/remix-lab/runs/{id}/adopt` | `{project_id}` → 调现有连续文案落盘（与 `POST /api/projects/{id}/assets/continuous_script` 同一套 `assets.Service`） |

列表可用短轮询 GET 实验详情刷状态，不接任务 WebSocket。间隔 2 秒，实验终态后停。

限制：原文 1～20000 字；槽 1～4；每槽次数 1～3；批注最长 2000 字。超限 400。采用目标必须是已有风景混剪项目（现有 `projects` 表里的项目）；图文项目 400。

采用成功不改实验状态，只更新该稿 `adopted_project_id`，卡片显示已采用到哪。不另建采用历史表。

## 4. 写手提示词（本轮仅此）

`buildWriterPromptStable` 硬性底线增加：卖课四句停在「方向判断」，禁止「就这些」「就这样」「好了」「就说到这儿」这类空收尾。

版本戳改为 `语感回流 2026-08-25 禁空收尾`。  
`rewrite_sharp` / `copy` 不改。  
`TestRewritePromptStamp`、`TestWriterPromptForbidsLineByLineParaphrase`、`docs/项目说明.md` 第 4 条同步戳和禁空收尾。不修已有的 `TestWriterPromptSharpEmphasizesImpact`（缺「第一目标不是「合规」」是旧账）。

## 5. 我怎么读

对话里「看进化台」时读：

1. `console.db` 的 `remix_lab_experiments` / `remix_lab_runs`（原文、各稿、批注、戳）
2. 需要对照提示词时再读该 run 的 `prompt_system.txt`

不要让用户再贴文案。密钥列不读、不打印。

## 6. 明确不做

- 按批注自动改提示词或一键重写提示词
- 采用时新建项目、回写对标原文、导入 titles/descriptions
- 改设置页日产默认二创模型/地址/密钥（进化台槽是独立的）
- 进化台出入口播、配音、混剪
- 进化台切换 `rewrite_sharp` / `copy`
- 挂回电影混剪、图文视频、选题

## 7. 验收

- 同一模型次数=2：两条 run，目录和正文独立，HTTP 去重不会把它们合成一条。
- 默认槽 + 另一个地址的槽能同时排队，实际 LLM 同时最多 2 个。
- 一篇故意失败（坏地址或坏密钥），其他稿仍能完成，实验 `partial`。
- 批注 PATCH 后刷新实验详情仍在。
- 采用到已有项目后，该项目当前 `continuous_script` 就是这篇；项目原 `source_script` 不变。
- 默认 rewrite 提示词含空收尾禁令；新戳写入实验的 `prompt_stamp`。
- `/remix-lab` 能打开；未知路径仍 404；`App.tsx` 仍不包含 `ModeHome` / `ImageVideoStudio`。
- 单元测试覆盖：defaults 掩码、次数/槽数校验、同模型两稿不互相覆盖、采用走现有落盘。不在 CI 打真实 DeepSeek。

## 8. 风险

- 进化台在进程内调 `Run`，控制台重启会丢掉未完成的 in-flight 调用；重启后把仍是 `running`/`queued` 的 run 标 `failed`（错误：控制台已重启），实验改为 `partial` 或 `failed`。
- 额外密钥只存在本机 DPAPI。换机器或换 Windows 用户会变成未配置，需要重填。
- 10 分钟 × 排队可能让一次实验很久；页面必须显示每稿 queued/running，不能假装已经在写。
