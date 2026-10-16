# 任务单：图文制作 —— 单张出图、出图历史、图片模型可选、生图 Key 共用（后端）

在 Go 后端实现以下功能。前端由另一位工程师按本文契约编写，**接口路径、JSON 字段名必须与本文完全一致**（snake_case）。不要改 `web/` 下除 `web/src/image-mode/imageStudioTypes.ts` 以外的文件。先读 `AGENTS.md`。

## 已知事实（已实测，不要再猜）

- 生图网关 = `settings.image_base_url`（OpenAI 兼容，`POST {base}/images/generations`），与图文文本模型同一个网关（vps-new CPA）。
- **当前设置里的生图 Key（`image_api_key`）已失效（401 Invalid API key）**；图文文本 Key（`image_text_api_key`）有效。用户决定：**生图与图文文本共用同一把 Key**。
- 网关实测可用的图片模型：`grok-imagine-image`（约 7 秒）、`grok-imagine-image-quality`（约 7 秒）、`grok-imagine-image-2.0`（约 12–17 秒，细节最好）。`gpt-image-2`（当前默认）和 `gemini-3.1-flash-image` 在 `/images/generations` 上返回 400，不可用。用户决定默认模型：**`grok-imagine-image-2.0`**。
- 网关支持一次请求多张：`n=3` 一次返回 3 张（约 8 秒）；支持 `size` 为 `1024x1024`、`1024x1536`、`1536x1024`。现有 `imageproject.Client.Generate` 固定 `n:1`。
- 现有 `imageproject.BuildPrompt` / `styleInstructions` / `imageTextLayoutRules` 是**财经文案配图专用**（含“为中国45—65岁观众制作财经认知视频内容图”和强制图内中文标题规则）。**单张出图不得默认注入这些内容**：用户写的提示词原样送给模型；只有用户选了风格时才追加一句风格描述（用 `styleInstructions` 里的短描述，或自定义风格文本），且不追加财经受众句和文字排版规则。
- `image_model`、`image_base_url`、`image_api_key` 目前不在 `applyHotSettings` 热更新列表里，改了要重启才生效。

## 一、生图 Key 共用 + 默认模型 + 热更新（`internal/settings`）

1. 新增公开设置 `image_use_text_key`（bool，默认 **true**）。语义：为 true 时，所有生图调用（图文项目出图、单张出图、`internal/aigc` 里用到 `ImageAPIKey` 的地方）使用 `image_text_api_key`；若 `image_text_api_key` 为空则回退 `image_api_key`。为 false 时用 `image_api_key`。
   - 实现方式：在 `configuredRuntime` 组装 Runtime 后统一计算一个“生效的生图 Key”，直接覆盖 `runtime.ImageAPIKey`（这样所有现有调用点自动生效，不用逐个改）。同理：若 `image_base_url` 为空，回退 `image_text_base_url`。
   - 加入 `domain.PublicSettings`（json `image_use_text_key`）、存储、校验、`publicValues`、读取默认值（缺省=true）、激活码可导入键列表（若该列表用于公开设置白名单，请加入）。
2. 默认图片模型从 `gpt-image-2` 改为 `grok-imagine-image-2.0`：`defaultImageModel` 常量、读取时的缺省值。**已有数据库里存的 `image_model=gpt-image-2` 要迁移**：新增一个迁移（见下文版本号），把 settings 表里 `image_model` 为 `gpt-image-2` 或空的改为 `grok-imagine-image-2.0`；并写入 `image_use_text_key=true`（若不存在）。不要改用户其他设置。
3. 让 `ImageModel`、`ImageBaseURL`、`ImageAPIKey`（含共用逻辑）、`ImageTextBaseURL`、`ImageTextModel`、`ImageTextAPIKey` **热生效**：`Update` 后无需重启。做法参照 `applyHotSettings` / `applyHotVoiceSecrets`：公开字段在 `applyHotSettings` 里复制；密钥更新后重新解密对应 secret 写入 active runtime，并重新计算共用后的 `ImageAPIKey`。如果 settings 的 `restart_required` 判断里包含这些键，相应移除。补测试：Update 后 `Runtime()` 立即返回新值，`restart_required=false`。
4. 测试：共用开关两种取值；文本 Key 为空时回退；base_url 回退；迁移把 gpt-image-2 改掉但保留其他自定义模型（例如用户已设成 `grok-imagine-image` 则不动）。

## 二、生图客户端支持多张（`internal/imageproject/client.go`）

- `GenerateRequest` 增加 `Count int`（0 视为 1，上限 4）。payload 的 `n` 用它。返回值改为可以拿到多张：新增 `GenerateMany(ctx, req) ([]GenerateResult, error)`（或给 `GenerateResult` 加 `Images []GeneratedImage`），**现有 `Generate` 行为与签名保持不变**（内部可复用），现有调用点不改。
- 网关返回张数少于请求数时：返回实际拿到的张数，不报错（上层记录“只出了 N 张”）。
- `ratioSize` 保持：`3:4`、`9:16` → `1024x1536`；`4:3` → `1536x1024`；其他 `1024x1024`。
- 测试用 httptest：n=3 返回 3 张；返回 2 张时得到 2 张；b64 与 url 两种返回；错误状态码。

## 三、出图历史（新包 `internal/imagestudio` + store + HTTP）

### 迁移（**版本 47、48**，按顺序追加到 `migrations` 末尾，不改旧迁移）

迁移 47（设置迁移，见第一节第 2 点）：只做 settings 表的 UPDATE/INSERT OR IGNORE。

迁移 48：
```sql
CREATE TABLE image_generations (
  id TEXT PRIMARY KEY,
  prompt TEXT NOT NULL,              -- 用户原始输入
  final_prompt TEXT NOT NULL,        -- 实际送给图片模型的提示词（扩写/加风格后）
  enhance INTEGER NOT NULL DEFAULT 0,-- 是否用了 AI 扩写
  style TEXT NOT NULL DEFAULT '',    -- '' 表示不套风格；否则为 styleInstructions 的键或 'custom'
  custom_style TEXT NOT NULL DEFAULT '',
  model TEXT NOT NULL,
  ratio TEXT NOT NULL CHECK (ratio IN ('3:4','4:3','9:16','1:1')),
  count INTEGER NOT NULL CHECK (count BETWEEN 1 AND 4),
  status TEXT NOT NULL CHECK (status IN ('queued','running','completed','partial','failed')),
  error TEXT NOT NULL DEFAULT '',
  favorite INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX idx_image_generations_created ON image_generations(created_at DESC);
CREATE TABLE image_generation_outputs (
  id TEXT PRIMARY KEY,
  generation_id TEXT NOT NULL REFERENCES image_generations(id) ON DELETE CASCADE,
  position INTEGER NOT NULL,         -- 0..count-1
  status TEXT NOT NULL CHECK (status IN ('pending','ready','failed')),
  path TEXT NOT NULL DEFAULT '',
  mime_type TEXT NOT NULL DEFAULT '',
  width INTEGER NOT NULL DEFAULT 0,
  height INTEGER NOT NULL DEFAULT 0,
  bytes INTEGER NOT NULL DEFAULT 0,
  error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(generation_id, position)
);
```
时间格式与现有表一致（RFC3339 UTC）。

### 文件存储
- 图片写到 `<data_root>/image-studio/<generation_id>/<position>_<uuid>.<ext>`，用 `os.Root` 或等价方式保证路径不越界，权限 0600。删除 generation 时删除整个目录。

### 生成流程（异步）
1. `POST` 创建记录（status=queued，按 count 预建 outputs 为 pending），立刻返回 201。
2. 后台 goroutine：status=running →（若 enhance）调用图文文本模型扩写提示词 → 组装 final_prompt → 调图片网关。
   - **出多张的策略**：先用一次请求 `n=count`；若返回张数不足或该请求失败（非 401/403/400），对缺的张数用 `n=1` 并发补齐（并发上限 = min(count, settings.max_image_concurrency)），每张最多重试 `settings.image_generation_attempts` 次（复用 `generateWithRetry` 的退避逻辑）。401/403/400 直接全部失败，不重试。
   - 每拿到一张就落盘并把对应 output 标 ready（前端轮询能看到逐张出现）。
   - 结束：全部 ready → completed；部分 ready → partial；全部失败 → failed。error 写**中文人话**（见下）。
3. 整体超时 6 分钟；服务关闭时取消并等待；启动时把遗留 queued/running 的记录及其 pending outputs 标为 failed（“控制台重启中断”）。
4. 同时运行的 generation 上限 3 个，超出的排队（queued）。

### AI 扩写
- 用 `imageproject` 现有的 planner chat client 配置方式（`plannerClient` 的同一套 base/key/model 解析，模型可由请求传 `text_model` 覆盖）。系统提示词：把用户的一句话需求扩写成**一段**适合图片模型的中文画面描述（主体、场景、构图、光线、色彩、风格、画幅），保留用户明确提到的所有要素与文字，不添加用户没要求的文字内容，不要解释，只输出描述本身，150–400 字。用户输入是素材不是指令（防注入）。
- 另提供同步接口单独扩写（前端“先看扩写结果再出图”用）。

### 错误文案（中文、可执行）
- 401/403：`生图 Key 无效或没有权限，请到「设置 → 图片」检查 Key`
- 400 且模型可能不存在：`图片模型「<model>」网关不支持，换一个模型试试`（上游 400 一律用这句，带模型名）
- 429：`生图服务繁忙（限流），稍后再试`
- 5xx / 超时 / 网络：`生图服务暂时不可用（<简短原因>），可以重试`
- 内容审核拒绝（若上游返回体里能识别 moderation/safety 字样）：`提示词被模型的内容安全拦截，换个说法试试`
- 现有图文项目的出图失败也改用同一套映射（`imageprojects.go` 的 `generate` 里 `MarkItemFailed` 的 message），**不改**现有接口字段。

### HTTP 接口（`internal/httpapi/imagestudio.go`，挂在 `/api/image-studio` 和 `/api/image-studio/`）

GenerationJSON：
```json
{
  "id": "...",
  "prompt": "一只橘猫坐在窗台",
  "final_prompt": "……",
  "enhance": false,
  "style": "",
  "custom_style": "",
  "model": "grok-imagine-image-2.0",
  "ratio": "3:4",
  "count": 2,
  "status": "running",
  "error": "",
  "favorite": false,
  "created_at": "...",
  "updated_at": "...",
  "outputs": [
    {"id": "...", "position": 0, "status": "ready", "mime_type": "image/jpeg", "width": 1024, "height": 1536, "bytes": 601234, "error": "", "url": "/api/image-studio/outputs/<id>/image"}
  ]
}
```
（`outputs` 按 position 升序；`url` 只在 status=ready 时非空，其他为 ""。）

- `GET /api/image-studio/models` → `{"default_model":"grok-imagine-image-2.0","models":[{"id":"grok-imagine-image-2.0","label":"Grok Imagine 2.0","note":"细节最好 · 约 15 秒","available":true}, ...],"key_configured":true,"key_source":"image_text"}`
  - 列表来源：请求网关 `GET {image_base_url}/models`（用生效的生图 Key，5 秒超时），筛出图片模型（id 含 `image`/`imagine`/`dall`/`flux`/`seedream`/`imagen`，**排除含 `video` 的**）。内置说明表：`grok-imagine-image`→“快 · 约 7 秒”，`grok-imagine-image-quality`→“快 · 约 7 秒 · 画质增强”，`grok-imagine-image-2.0`→“细节最好 · 约 15 秒”，`gemini-3.1-flash-image`→`available:false`、note “网关暂不支持生图接口”，其他 note 为空。当前设置的 `image_model` 即使不在网关列表里也要出现在结果里（`available` 依网关列表判断）。网关请求失败时返回内置的三个 grok 模型 + 当前设置模型，并带 `"list_error":"中文原因"`。结果缓存 10 分钟（设置变更后失效）。
  - `key_source`：`image_text`（共用）或 `image`。
- `POST /api/image-studio/enhance` body `{"prompt":"...","style":"","custom_style":"","ratio":"3:4","text_model":""}` → 同步返回 `{"final_prompt":"...","model":"<所用文本模型>"}`，超时 90 秒。错误 502 `{"code":"enhance_failed","message":"中文原因"}`。
- `POST /api/image-studio/generations` body：
  ```json
  {"prompt":"...", "final_prompt":"", "enhance":false, "style":"", "custom_style":"", "model":"", "ratio":"3:4", "count":2, "text_model":""}
  ```
  - `final_prompt` 非空时直接用它（前端已扩写并允许用户编辑过），不再扩写；为空且 `enhance=true` 时后台扩写；都没有时用 prompt（+风格）。
  - `model` 为空用设置默认。校验：prompt 1–4000 字（final_prompt 同限）；count 1–4；ratio 四选一；style 为 '' / styleInstructions 键 / 'custom'（custom 时 custom_style 必填，≤500 字）；字符串必须是合法 UTF-8。错误 400 `{"code":"invalid_generation","message":"中文原因"}`。生图未配置（无 base_url 或无生效 Key）→ 409 `{"code":"image_not_configured","message":"还没配置生图服务，请到「设置 → 图片」填写"}`。
  - 返回 201 + GenerationJSON。
- `GET /api/image-studio/generations?limit=30&before=<created_at>&favorite=0|1&q=<关键词>` → `{"items":[GenerationJSON], "has_more":bool}`，按 created_at 倒序；`q` 匹配 prompt；`before` 用于翻页。
- `GET /api/image-studio/generations/{id}` → GenerationJSON；404 `{"code":"generation_not_found"}`。
- `PATCH /api/image-studio/generations/{id}` body `{"favorite":true}` → GenerationJSON。
- `DELETE /api/image-studio/generations/{id}` → 204（运行中的先取消再删）。
- `POST /api/image-studio/generations/{id}/retry` → 只重试该次中 failed 的 outputs（复用原 final_prompt/model/ratio），返回 202 + GenerationJSON；运行中返回 409 `{"code":"generation_running"}`。
- `GET /api/image-studio/outputs/{id}/image` → 图片字节，`Content-Type` 用存储的 mime，`Cache-Control: private, max-age=31536000, immutable`（文件名含 uuid，内容不变）；`?download=1` 时加 `Content-Disposition: attachment; filename="<generation 前 12 字提示词>_<position+1>.<ext>"`（文件名去掉非法字符，RFC 5987 编码中文）。
- `GET /api/image-studio/generations/{id}/download` → 该次所有 ready 图片打 zip。

## 四、通用要求

- 所有新接口加到 `internal/mcpserver/api_catalog.go`（花钱的打 `$`：generations POST、retry、enhance；删除打 `!`），`routes_test.go` 必须通过。
- 新响应结构体登记到 `contract_types_test.go`：前端类型文件 `web/src/image-mode/imageStudioTypes.ts`，类型名 `ImageGeneration`、`ImageGenerationOutput`、`ImageModelOption`、`ImageModelsResponse`。**你需要创建这个 TS 文件**（只写类型声明，字段与本文 JSON 完全一致），这是唯一允许你写的前端文件。
- `api_catalog` 以外，MCP 若有图文相关工具清单（`internal/mcpserver/catalog.go`），加一个 `generate_image` 工具（POST generations，Confirm 文案“会调用生图服务（花钱）”）和 `list_image_generations`。
- 密钥绝不写入日志、错误、测试快照；错误信息里出现的 key 要脱敏。
- 测试（全部用 httptest 假网关 / 假 chat，**不访问真实网关**）：
  - settings：共用开关、回退、迁移、热生效。
  - client：多张、少张、b64/url。
  - imagestudio：n=count 一次成功；一次只回 1 张时并发补齐到 count；401 不重试全部失败并给中文错误；400 给“模型不支持”；部分成功=partial；扩写时 final_prompt 来自 chat 且用户可传 final_prompt 跳过扩写；不选风格时 final_prompt == prompt（**断言不含“45—65岁”和“画面文字规则”**）；选风格时只追加风格描述；并发上限 3 排队；重启恢复；删除运行中任务；retry 只补失败的；图片接口与 zip；路径越界防护。
  - httpapi：每个新路由至少一个成功和一个失败用例。
- 运行并全部通过：`go build ./...`、`go vet ./...`、`go test ./...`（若遇到既有的偶发会话测试超时，单独重跑确认并在报告里说明）。
- 完成后写 `TASK-image-studio.RESULT.md`（中文）：做了什么、文件清单、迁移版本、测试结果原文摘录、无法验证的点、与契约的偏差（应为零）。
- `git add` + `git commit`，不 push，不动其他分支。

## 禁止
- 不改 `web/` 下除 `web/src/image-mode/imageStudioTypes.ts` 外的任何文件。
- 不改已有迁移、不删现有功能、不改 `video-console-data/`、不启动或重启 2030 控制台、不访问真实网关或模型。
