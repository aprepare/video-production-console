# 任务单：AI 复刻工作台（后端）—— 拆解 → 分镜 → 首帧 → 批量生成视频 → 拼接 → 质检

在当前分支（已包含拆解报告的 `replicate.ai_prompts` / `style_lock` 和 `internal/videoqc`）上实现「AI 复刻」模块后端。前端由另一位工程师按本文契约写，**接口路径与 JSON 字段必须一致**（snake_case）。只允许写 `web/src/replicate/replicateTypes.ts` 这一个前端文件。先读 `AGENTS.md`、`TASK-replicate-qc.md`、`TASK-replicate-qc.RESULT.md`。

这是创业大赛路演要现场演示的核心流程：用户选一条对标爆款 → AI 拆解出每个镜头的复刻提示词 → 每个镜头先出首帧图 → **所有镜头并行**交给视频模型生成（默认 MiniMax H3，经 RunningHub）→ 每镜头可多版本、可单独重生成 → 选定版本拼接成片 → AI 质检。**必须真实可用**。

## 已知事实（已核实）

- **MiniMax H3 经 RunningHub AI 应用调用**（不是 MiniMax 官方 API）。协议（Python 参考实现 `C:/Users/prepare/.agents/skills/hypit/scripts/run-runninghub-h3.py`，只读参考，不要在程序里调用它）：
  1. 上传图：`POST https://www.runninghub.ai/openapi/v2/media/upload/binary`，multipart 字段 `file`，Header `Authorization: Bearer <key>` → `data.fileName`（或 `data.filename`）。
  2. 提交：`POST https://www.runninghub.ai/task/openapi/ai-app/run`，JSON `{"webappId":"2084320751339032577","apiKey":"<key>","instanceType":"default","nodeInfoList":[...]}`，Header `Authorization: Bearer <key>`, `Content-Type: application/json` → `data.taskId`。
  3. 查询：`POST https://www.runninghub.ai/openapi/v2/query`，JSON `{"taskId":"..."}`（同样带 Bearer）→ `status`（`SUCCESS`/`FAILED`/`FAILURE`/`CANCELED`/运行中其他值）、`results[]`（取 `outputType=="mp4"` 的 `url`）、`errorCode`、`usage`。轮询间隔 20–25 秒。
  - 模板 `nodeInfoList`（来自 `C:/Users/prepare/.agents/skills/hypit/assets/runninghub-h3-request.json`，把它的内容**内嵌**到 Go 代码里，不要运行时读这个文件）。需要设置的节点：`141/image`=上传得到的 fileName（参考图1，必填）；`150/value`=提示词；`186/value`=时长秒数（字符串）；`115/aspect_ratio`=比例枚举（打开模板文件查看该节点可选值的写法，如 `16:9 (Widescreen)`，竖屏 9:16、方形 1:1 的对应写法要从 RunningHub 该应用的参数定义确认；若无法确认，竖屏用与横屏同格式的字符串并在报告里注明待真机验证）；`185/value`=提示词优化开关（默认 `"true"`）。其余节点保持模板默认值（`171` 最高画质参考 false、`147` 像素 0.5、`192` 采样步数 8 等）。
  - 单条约 2.5–4 分钟、约 35 币。每个 API Key 有自己的并发上限，用多把 Key 才能真正并行。
- **Grok 视频**：`internal/grokvideo` 已有 `HTTPClient.Submit/Poll/Download`，图生视频，`grok-imagine-video-1.5`，6/10/15 秒，480p。复用，不要重写。
- **首帧图**：`internal/imagestudio` / `internal/imageproject` 的生图客户端（`GenerateMany`）已可用，默认模型 `grok-imagine-image-2.0`，生效 Key 已共用。
- **拆解**：`internal/refanalysis`，报告里 `replicate.style_lock`、`replicate.ai_prompts[]`（每镜头 `first_frame_prompt` / `motion_prompt` / `t2v_prompt` / `negative` / `keep` / `start_s` / `end_s` / `purpose`）。
- **质检**：`internal/videoqc`（上传/对标来源，抽帧 + 看图模型，verdict 服务端复核）。本任务要加一个新来源 `replicate`（见下）。
- 最新迁移版本 49。本任务新增迁移用 **50**。

## 一、RunningHub 原生客户端（新包 `internal/runninghub`）

- `Client{HTTP *http.Client, BaseURL string}`（BaseURL 默认 `https://www.runninghub.ai`，测试可替换）。方法：`Upload(ctx, key, filename, data []byte) (fileName string, error)`、`RunApp(ctx, key, webappID string, nodes []Node, instanceType string) (taskID string, error)`、`Query(ctx, key, taskID) (Status, error)`、`Download(ctx, url, w io.Writer, limit int64) error`。
- `H3Request{Image []byte; ImageName string; Prompt string; DurationS int; Ratio string; PromptOptimize bool}` → `BuildH3Nodes(...)` 生成 nodeInfoList（基于内嵌模板深拷贝）。
- 禁止重定向（下载结果 URL 除外：结果 CDN 可能 302，允许跟随但只允许 http/https 且 host 非内网 IP），响应体上限，错误信息绝不包含 Key（用现有 `security.Redactor` 或等价替换）。
- **多 Key 池**：设置新增密钥 `runninghub_api_keys`（多行，每行一把；加入 `settings` 的 secretKeys、DPAPI 加密、热生效）。`KeyPool` 按轮询分配，记录每把 Key 正在跑的任务数，单 Key 并发上限默认 1（设置项 `runninghub_per_key_concurrency`，1–5，公开设置，热生效）。全部 Key 忙时排队。某把 Key 返回余额不足/鉴权失败的错误码时，在本进程内临时停用该 Key 30 分钟并换下一把重试一次。
- 设置页需要的字段：`runninghub_api_keys`（secret，`configured` 时显示“已配置 N 把”——在 settings view 的 secrets 元信息里加一个 `count` 字段，若改动现有 secrets 结构代价大，可新增 `GET /api/replicate/providers` 返回 key 数量，见第五节）。
- 测试：httptest 模拟 RunningHub 三个接口；Key 轮询与并发上限；Key 停用切换；错误脱敏；nodeInfoList 节点值正确。

## 二、复刻项目数据（迁移 50）

```sql
CREATE TABLE replicate_projects (
  id TEXT PRIMARY KEY,
  title TEXT NOT NULL,
  reference_id TEXT NOT NULL DEFAULT '',        -- 对标素材 id（可空：允许手动建分镜）
  analysis_id TEXT NOT NULL DEFAULT '',         -- 使用的拆解 id
  product TEXT NOT NULL DEFAULT '',             -- 我的产品/场景
  style_lock TEXT NOT NULL DEFAULT '',
  ratio TEXT NOT NULL DEFAULT '9:16' CHECK (ratio IN ('9:16','16:9','1:1','3:4','4:3')),
  video_provider TEXT NOT NULL DEFAULT 'runninghub_h3' CHECK (video_provider IN ('runninghub_h3','grok')),
  image_model TEXT NOT NULL DEFAULT '',          -- 首帧图模型，空=设置默认
  variants INTEGER NOT NULL DEFAULT 1 CHECK (variants BETWEEN 1 AND 4),   -- 每个镜头默认出几版
  status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','generating','ready','stitching','stitched','failed')),
  final_path TEXT NOT NULL DEFAULT '',
  final_duration_s REAL NOT NULL DEFAULT 0,
  qc_id TEXT NOT NULL DEFAULT '',
  error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX idx_replicate_projects_created ON replicate_projects(created_at DESC);
CREATE TABLE replicate_shots (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL REFERENCES replicate_projects(id) ON DELETE CASCADE,
  position INTEGER NOT NULL,                     -- 1..N
  purpose TEXT NOT NULL DEFAULT '',
  source_start_s REAL NOT NULL DEFAULT 0,
  source_end_s REAL NOT NULL DEFAULT 0,
  duration_s INTEGER NOT NULL DEFAULT 6,         -- 生成时长：H3 允许 1–10（按模板范围校验，默认 6）；Grok 只允许 6/10/15
  first_frame_prompt TEXT NOT NULL DEFAULT '',
  motion_prompt TEXT NOT NULL DEFAULT '',
  negative TEXT NOT NULL DEFAULT '',
  keep TEXT NOT NULL DEFAULT '',
  first_frame_path TEXT NOT NULL DEFAULT '',     -- 首帧图（生成或用户上传）
  first_frame_source TEXT NOT NULL DEFAULT '' CHECK (first_frame_source IN ('','generated','uploaded','reference_frame')),
  first_frame_status TEXT NOT NULL DEFAULT 'none' CHECK (first_frame_status IN ('none','generating','ready','failed')),
  first_frame_error TEXT NOT NULL DEFAULT '',
  selected_take_id TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(project_id, position)
);
CREATE TABLE replicate_takes (
  id TEXT PRIMARY KEY,
  shot_id TEXT NOT NULL REFERENCES replicate_shots(id) ON DELETE CASCADE,
  provider TEXT NOT NULL,
  prompt TEXT NOT NULL,                          -- 实际提交的完整提示词（motion + style_lock + negative 组合后）
  status TEXT NOT NULL CHECK (status IN ('queued','submitting','running','succeeded','failed','canceled')),
  provider_task_id TEXT NOT NULL DEFAULT '',
  key_slot INTEGER NOT NULL DEFAULT -1,          -- 用了第几把 RunningHub Key（不存 Key 本身）
  path TEXT NOT NULL DEFAULT '',
  duration_s REAL NOT NULL DEFAULT 0,
  width INTEGER NOT NULL DEFAULT 0,
  height INTEGER NOT NULL DEFAULT 0,
  cost_note TEXT NOT NULL DEFAULT '',            -- 上游返回的 usage 摘要（如消耗币数），可空
  error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX idx_replicate_takes_shot ON replicate_takes(shot_id, created_at);
```
文件存 `<data_root>/replicate/<project_id>/`（`os.Root` 约束）：`shots/<shot_id>/first.<ext>`、`takes/<take_id>.mp4`、`final.mp4`。

## 三、服务（新包 `internal/replicate`）

1. **建项目**：`CreateFromAnalysis(reference_id, analysis_id, product, ratio, provider, image_model, variants)`：读取已完成的拆解报告，把 `ai_prompts` 转成 shots（position 按顺序；`duration_s` 由 `end_s-start_s` 四舍五入后夹到所选模型允许范围；Grok 取最接近的 6/10/15）。拆解没有 `ai_prompts`（旧报告）→ 400 `{"code":"analysis_has_no_prompts","message":"这份拆解没有复刻提示词，请重新拆解一次"}`。也支持 `CreateBlank(title, ratio, provider)` 手动加镜头。
2. **编辑**：项目字段（title/product/style_lock/ratio/provider/image_model/variants）、镜头字段（purpose/duration_s/first_frame_prompt/motion_prompt/negative/keep）、增删镜头、调整顺序（`reorder` 传 shot_id 数组）。
3. **首帧图**：
   - `GenerateFirstFrames(project, shot_ids[]/all)`：**并行**生成（并发 = settings.max_image_concurrency，上限 6），提示词 = `first_frame_prompt` + `style_lock` + 比例说明 + “不要出现文字、水印”（若 keep 非空追加“商品外观保持：<keep>”）。模型 = 项目 image_model 或设置默认。每个镜头只保留最新一张。
   - `UploadFirstFrame(shot, file)`：用户上传实拍图替代（png/jpg/webp，≤20MB），`first_frame_source=uploaded`。
   - `UseReferenceFrame(shot, analysis_frame_index)`：用拆解时抽的关键帧当首帧（`reference_frame`）。（注意：这是同行画面，前端会提示“仅用于测试，正式发布请换成自己的画面”。）
4. **生成视频（核心）**：
   - `Generate(project, request)`：request `{shot_ids: [] (空=全部有首帧的镜头), variants: n (空=用项目默认), provider: ""(空=项目默认)}`。为每个镜头创建 n 个 take，**全部立即入队并行执行**（不要逐条等待）。RunningHub 受 KeyPool 并发控制；Grok 并发上限 = 4。
   - 提交的完整提示词 = `motion_prompt` + “。画面风格：” + style_lock + （negative 非空时）“。避免：” + negative + （keep 非空时）“。商品外观保持：” + keep。提示词长度超过模型上限时截断 motion 以外部分并在 take.error 记录警告（不报错）。
   - 首帧不存在的镜头跳过并在返回里列出 `skipped`（原因“先出首帧图或上传图片”）。
   - 单镜头重生成 = `Generate` 传单个 shot_id；`variants` 自定义（1–4）。
   - take 状态机：queued → submitting → running → succeeded/failed；下载后用 ffprobe 读时长和宽高。成功的 take 若该镜头还没有 selected_take，自动设为 selected。
   - `CancelTake(take)`、`SelectTake(shot, take)`、`DeleteTake(take)`（删文件）。
   - 任务恢复：进程重启时，`running` 且有 `provider_task_id` 的 take 继续轮询（RunningHub 支持按 taskId 恢复，Grok 也有 request id）；`queued/submitting` 无 task id 的标 failed（“控制台重启中断，请重新生成”）——**不要盲目重复提交**以免重复扣费。
   - 每个 take 整体超时 15 分钟（RunningHub 排队可能很久）。服务关闭时取消轮询（不取消上游任务），下次启动恢复。
5. **拼接**：`Stitch(project)`：按 position 取每个镜头的 selected_take（缺失的镜头 → 400 列出哪些镜头没有可用版本）。用 ffmpeg 统一规格后拼接：先把每段转码为同一分辨率（按项目比例：9:16→720x1280，16:9→1280x720，1:1→960x960，3:4→720x960，4:3→960x720，`scale+pad` 保持比例黑边填充）、30fps、yuv420p、H.264，去掉音轨或补静音轨，然后 concat demuxer 合成 `final.mp4`。异步执行，状态 stitching → stitched。可选参数 `{"audio":"none"}`（本期只做无声成片；为以后加配音预留字段即可）。
6. **质检联动**：`videoqc` 新增来源 `replicate`：`source_id`=项目 id，审核 `final.mp4`；也支持审核单个 take：`source=replicate_take`、`source_id`=take id。QC 结果的问题帧时间戳能映射回镜头：在 QC 报告 JSON 里额外加 `shot_position`（根据每段在成片中的起止时间换算，只对 replicate 来源填写）。项目记录 `qc_id`。
   - 修改 `video_qc_checks.source` 的 CHECK 约束：SQLite 不能直接改 CHECK，迁移 50 里用“新建表-复制数据-删旧表-改名”的方式重建该表，允许 `upload/reference/image_video_job/replicate/replicate_take`。
7. **模型可选**：
   - 拆解模型：已有（`POST /api/reference-analyses` 的 `model`）。
   - 文案改写（复刻用的口播/字幕文案，若后续需要）本期不做。
   - 首帧图模型：项目 `image_model`。
   - 视频模型：项目 `video_provider`（`runninghub_h3` / `grok`），每次 Generate 可覆盖。
   - 质检模型：`POST /api/video-qc` 的 `model`（已有）。
   所有模型选项的列表：图片模型用现有 `GET /api/image-studio/models`；看图/文本模型沿用前端现有模型下拉（settings.model_options）。

## 四、HTTP（`internal/httpapi/replicate.go`，挂 `/api/replicate` 与 `/api/replicate/`）

ProjectJSON：
```json
{"id":"...","title":"...","reference_id":"...","analysis_id":"...","product":"...","style_lock":"...","ratio":"9:16",
 "video_provider":"runninghub_h3","image_model":"","variants":2,"status":"generating","final_url":"","final_duration_s":0,
 "qc_id":"","error":"","created_at":"...","updated_at":"...",
 "shots":[{"id":"...","position":1,"purpose":"钩子","source_start_s":0,"source_end_s":2.4,"duration_s":6,
   "first_frame_prompt":"...","motion_prompt":"...","negative":"...","keep":"...",
   "first_frame_url":"/api/replicate/shots/<id>/first-frame?v=<updated_at>","first_frame_source":"generated","first_frame_status":"ready","first_frame_error":"",
   "selected_take_id":"...",
   "takes":[{"id":"...","provider":"runninghub_h3","status":"running","error":"","duration_s":0,"width":0,"height":0,"url":"","cost_note":"","created_at":"..."}]}],
 "progress":{"takes_total":12,"takes_done":5,"takes_failed":1,"takes_running":6}}
```
（`final_url` 仅 stitched 时为 `/api/replicate/<id>/final?v=...`；take 的 `url` 仅 succeeded 时为 `/api/replicate/takes/<id>/video`。）

- `GET /api/replicate/providers` → `{"providers":[{"id":"runninghub_h3","label":"MiniMax H3（RunningHub）","note":"质量好 · 每条约 3 分钟 · 约 35 币","available":true,"keys":12,"durations":[1,2,3,4,5,6,7,8,9,10],"default_duration":6},{"id":"grok","label":"Grok Video","note":"快 · 便宜 · 480p","available":true,"durations":[6,10,15],"default_duration":6}],"default_provider":"runninghub_h3"}`（`available` 依据是否配置了 Key；H3 时长范围以内嵌模板的合法范围为准，查不到就用 1–10）。
- `POST /api/replicate` body `{"reference_id":"","analysis_id":"","product":"","ratio":"9:16","video_provider":"","image_model":"","variants":1}` → 201 ProjectJSON；或 `{"blank":true,"title":"..."}`。
- `GET /api/replicate?limit=30` → `[ProjectJSON 不含 shots]`；`GET /api/replicate/{id}` → 完整。
- `PATCH /api/replicate/{id}`（项目字段部分更新）；`DELETE /api/replicate/{id}`（取消轮询、删文件；上游任务不取消）。
- `POST /api/replicate/{id}/shots`（新增镜头，body 镜头字段，可带 `after_position`）；`PATCH /api/replicate/shots/{shot_id}`；`DELETE /api/replicate/shots/{shot_id}`；`POST /api/replicate/{id}/shots/reorder` body `{"shot_ids":[...]}`。
- `POST /api/replicate/{id}/first-frames` body `{"shot_ids":[]}` → 202 ProjectJSON。
- `POST /api/replicate/shots/{shot_id}/first-frame`（multipart 上传 `file`）→ ProjectJSON；`POST /api/replicate/shots/{shot_id}/first-frame/reference` body `{"frame_index":3}` → ProjectJSON。
- `GET /api/replicate/shots/{shot_id}/first-frame` → 图片。
- `POST /api/replicate/{id}/generate` body `{"shot_ids":[],"variants":0,"provider":""}` → 202 `{"project":ProjectJSON,"skipped":[{"shot_id":"...","reason":"..."}]}`。
- `POST /api/replicate/takes/{take_id}/select`、`POST /api/replicate/takes/{take_id}/cancel`、`DELETE /api/replicate/takes/{take_id}`。
- `GET /api/replicate/takes/{take_id}/video`（Range 支持）。
- `POST /api/replicate/{id}/stitch` → 202；`GET /api/replicate/{id}/final`（Range，`?download=1` 加 Content-Disposition 中文文件名）。
- `POST /api/replicate/{id}/qc` body `{"model":""}` → 202 + VideoQCCheck（来源 replicate）。
- 错误码统一 `{"code":"...","message":"中文"}`：`replicate_not_found`、`invalid_replicate`、`analysis_has_no_prompts`、`video_provider_not_configured`（“还没配置 RunningHub Key，请到设置填写”）、`stitch_missing_takes`。
- RunningHub/Grok 上游错误翻译成中文人话（余额不足、Key 无效、排队超时、内容违规、提示词过长）。

## 五、设置与通用

- 设置新增：`runninghub_api_keys`（secret）、`runninghub_per_key_concurrency`（public，默认 1）、`replicate_default_provider`（public，默认 `runninghub_h3`）、`replicate_default_variants`（public，默认 1）。都热生效。加到 settings schema、activation 白名单（公开键）等所有需要登记的地方。
- **修复现有 bug**：`internal/aigc/video.go` 里 `runVideo(..., rt.ImageAPIKey)` 把生图 Key 当 RunningHub Key 传给外部脚本。改为使用新的 RunningHub 原生客户端（推荐，把 aigc 的图生视频改走 `internal/runninghub`，去掉外部 Python 脚本依赖）；如果改动太大，至少改成从 `runninghub_api_keys` 取第一把 Key 传入，并在报告说明。
- 新接口登记 `api_catalog.go`（generate/first-frames/stitch/qc 打 `$`；删除打 `!`），`routes_test.go` 通过；MCP `catalog.go` 加 `create_replicate_project`、`generate_replicate_shots`（Confirm “会调用视频模型（花钱）”）、`stitch_replicate`、`get_replicate_project`。
- 契约：`contract_types_test.go` 登记，前端类型文件 `web/src/replicate/replicateTypes.ts`（你创建，类型名 `ReplicateProject`、`ReplicateShot`、`ReplicateTake`、`ReplicateProvider`、`ReplicateProvidersResponse`、`ReplicateGenerateResponse`）。
- 密钥绝不写入日志、错误、数据库（take 只记 key_slot）。
- 测试（全部假服务，**不访问真实 RunningHub/Grok/模型**）：RunningHub 客户端全流程；KeyPool 并发与停用；Generate 并行（N 个镜头 × M 版本同时进入 running，而不是串行——用阻塞的假上游验证最大同时在跑数）；单镜头重生成；variants 自定义；跳过无首帧镜头；重启恢复（有 task id 的继续轮询、无 task id 的标 failed 且不重复提交）；SelectTake 与自动选中；拼接（用 ffmpeg 生成 2–3 段不同分辨率 testsrc 视频，拼接后时长≈总和、分辨率正确；ffmpeg 不可用时跳过集成子测试）；缺镜头拼接报错；QC replicate 来源与 shot_position 映射；迁移 50（含 video_qc_checks 重建后旧数据保留）；每个新路由成功/失败用例。
- 运行并全部通过：`go build ./...`、`go vet ./...`、`go test ./...`。
- 写 `TASK-replicate-workbench.RESULT.md`（中文）：做了什么、文件清单、迁移、测试原文摘录、**无法验证的点（尤其是 H3 竖屏比例枚举、时长范围）**、与契约偏差（应为零）。
- `git add` + `git commit`，不 push。

## 禁止
- 不改 `web/` 下除 `web/src/replicate/replicateTypes.ts` 外的文件。
- 不改已有迁移（49 及以前）；不删现有功能；不改 `video-console-data/`；不启动或重启 2030；不访问真实 RunningHub、Grok、模型网关；不在程序里调用 `hypit/scripts` 下的 Python 脚本（只能读来参考协议）。
