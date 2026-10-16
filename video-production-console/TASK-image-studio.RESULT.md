# TASK-image-studio 执行结果

日期：2026-10-16；分支：`codex/single-image-backend`

已完成任务单要求的后端实现、类型契约、MCP 工具及测试。接口路径、snake_case 字段和状态枚举按任务单实现；与契约的偏差为零。本次仅在当前分支提交，不推送、不部署。

## 已完成内容

- 生图默认模型改为 `grok-imagine-image-2.0`，新增默认开启的 `image_use_text_key`。运行时统一选择生效 Key，文本 Key 为空时回退独立图片 Key；图片 URL 为空时回退图文文本 URL。图片和图文文本的 URL、模型、Key 及共用开关热更新，更新后的 Key 从已落库密文重新解密，独立图片 Key 保留，切换开关不会污染。
- 图片客户端增加 `GenerateMany`，支持最多 4 张、合法空结果和少张结果、b64/URL/SSE 返回。原 `Generate` 保持单张行为和签名。后续图片下载或解码失败时保留此前成功的结果，统一中文错误并隐藏上游正文及密钥。
- 新增出图记录及输出存储，创建时预建 pending 输出并返回 queued。后台最多运行 3 个 generation；先请求一批，再用单张并发补齐，复用可取消的重试退避。逐图保存状态，完成后区分 completed、partial、failed。
- 实现 6 分钟生成预算、90 秒扩写预算、运行中删除取消、服务关闭取消并等待、启动中断恢复，以及仅重试 failed 输出。重试保留原最终提示词、图片模型、比例和已经 ready 的文件；读取记录与输出使用同一数据库快照。
- 单张出图未选风格时原样使用用户提示词；选风格时仅追加短风格描述。扩写沿用图文 planner 的 base/key/model 解析，支持 `text_model` 覆盖；传入 `final_prompt` 时不再次扩写或追加风格。
- 模型列表使用生效 Key 查询网关，5 秒超时、10 分钟缓存，设置变化后失效；支持图片模型过滤、排除视频模型、内置说明、当前模型补入及中文失败回退。
- 实现全部 10 条 HTTP 路由：模型列表、扩写、创建、列表、详情、收藏、删除、重试、单图读取/下载、ZIP 下载。图片以 `os.Root` 限制路径，文件用 UUID 命名和 0600 创建；下载支持中文 RFC 5987 文件名及约定的缓存头。
- 现有图文项目出图失败使用相同的中文映射。MCP 目录登记全部新路由及付费/删除标记，新增 `generate_image` 和 `list_image_generations`；创建工具包含必填 prompt、ratio、count 和全部可选请求字段。
- 创建四个前端类型并登记 Go/TS 契约。唯一前端改动为 `web/src/image-mode/imageStudioTypes.ts`。

## 迁移

- **47**：仅更新 settings 中旧默认模型/空模型，并在开关不存在时写入 `image_use_text_key=true`；保留其他自定义模型、已有 false 开关及无关设置。
- **48**：按任务单追加 `image_generations`、`image_generation_outputs` 及索引、约束、级联删除。
- 旧迁移未修改。迁移文件差异为追加 36 行、删除 0 行；迁移及恢复测试仅使用临时数据库。
- 原监控迁移测试将“最新版本必须为 46”改为检查当前迁移总数，同时仍明确验证 45、46 均存在；新增测试明确验证 47、48 均存在。

## 测试结果

冻结代码后的最终验收：三个命令均退出 0；55 个含测试的包通过，11 个包无测试文件，无失败。

```text
$ go build ./...
exit_code=0

$ go vet ./...
exit_code=0

$ go test ./...
ok  	video-production-console/internal/httpapi	109.853s
ok  	video-production-console/internal/imageproject	0.869s
ok  	video-production-console/internal/imagestudio	4.922s
ok  	video-production-console/internal/mcpserver	(cached)
ok  	video-production-console/internal/settings	(cached)
ok  	video-production-console/internal/store	(cached)
ok  	video-production-console/schemas	(cached)
exit_code=0
```

首次全量运行仅因旧测试硬编码最新迁移版本 46 而失败，已修正并保留原监控迁移记录/表的断言。失败原文：

```text
--- FAIL: TestMonitorMigrationsCreateVersionsAndTables (1.06s)
    monitors_test.go:41: latest migration=48, want 46
```

修正后的迁移定向回归原文：

```text
ok  	video-production-console/internal/store	10.755s
exit_code=0
```

新增场景覆盖：共用开关、Key/URL 回退与热更新、密文重新解密、迁移保留自定义设置；多图/少图/零图、b64/URL/SSE、错误分类和脱敏；单批成功、单批失败补齐、并发补图、401/403/400 不重试、部分成功及仅重试失败项；原始提示词和风格边界；假 chat 扩写、最终提示词覆盖、取消；三任务并发上限、重启恢复、运行中删除、关闭等待；收藏/搜索/分页、图片和 ZIP、中文文件名、实际存在的越界文件拒绝；每个新 HTTP 路由均有成功与失败用例，类型契约和 MCP 路由检查通过。

完整日志在 `artifacts/image-studio-validation-20261016/`；保留首次失败、修正后的第二轮全量结果、迁移回归及冻结代码后的最终构建/静态检查/全量测试结果。本轮没有出现需单独重跑的会话超时。

## 文件清单

```text
TASK-image-studio.md
TASK-image-studio.RESULT.md
internal/app/app.go
internal/domain/imagestudio.go
internal/domain/settings.go
internal/httpapi/contract_types_test.go
internal/httpapi/imageprojects.go
internal/httpapi/imagestudio.go
internal/httpapi/imagestudio_test.go
internal/imageproject/chat.go
internal/imageproject/chat_test.go
internal/imageproject/client.go
internal/imageproject/client_test.go
internal/imageproject/service.go
internal/imagestudio/models.go
internal/imagestudio/models_test.go
internal/imagestudio/service.go
internal/imagestudio/service_test.go
internal/mcpserver/api_catalog.go
internal/mcpserver/catalog.go
internal/mcpserver/image_studio_catalog_test.go
internal/settings/activation.go
internal/settings/activation_test.go
internal/settings/active_snapshot_test.go
internal/settings/image_key_test.go
internal/settings/overlay.go
internal/settings/service.go
internal/settings/service_test.go
internal/store/imagestudio.go
internal/store/migrations.go
internal/store/migrations_test.go
internal/store/monitors_test.go
internal/store/settings.go
schemas/settings.schema.json
schemas/settings_schema_test.go
web/src/image-mode/imageStudioTypes.ts
artifacts/image-studio-validation-20261016/go-build.log
artifacts/image-studio-validation-20261016/go-vet.log
artifacts/image-studio-validation-20261016/go-test.log
artifacts/image-studio-validation-20261016/go-test-initial.log
artifacts/image-studio-validation-20261016/go-test-second.log
artifacts/image-studio-validation-20261016/go-test-migrations.log
```

## 验证边界与禁令

按任务禁令，未访问真实网关或模型，未验证真实服务的当前可用性、生成画质和实际耗时；相关网络测试均使用本地假网关/假 chat，读取故障使用本地假传输注入。未启动或重启 2030，未修改 `video-console-data/`，未对生产库应用迁移，未修改其他前端文件、删除现有功能或操作其他分支。

当前 `CGO_ENABLED=0`，未运行 Go race 检测；并发上限、补图、取消、关闭及重试边界已通过实际异步测试。没有尚未通过的任务验收项，也没有需要用户补做的后端步骤。

**与任务契约的偏差：零。**
