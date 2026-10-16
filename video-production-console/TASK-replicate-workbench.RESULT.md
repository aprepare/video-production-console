# AI 复刻工作台后端交付结果

本次在 `codex/replicate-qc-backend` 实现拆解建项 → 分镜编辑 → 首帧生成/上传/引用 → 多版本并行生成 → 选版 → 无声拼接 → 视频质检。接口路径和 snake_case JSON 字段按任务单交付；只新增指定的 `web/src/replicate/replicateTypes.ts`，没有实现或改动前端界面。

## 实现内容

- `internal/runninghub` 原生实现上传、运行 H3 应用、查询和下载，内嵌完整 22 节点模板。API 请求禁止重定向；结果下载允许公网 HTTP/HTTPS 重定向，解析和实际拨号均校验公网地址，禁用代理，并限制响应与文件大小。上游错误归一化，密钥不进入错误、日志或 take 数据。
- 多 Key 池轮询分配，单 Key 并发默认 1、范围 1—5；占满时排队。鉴权或余额错误停用该 Key 30 分钟，提交阶段换 Key 重试一次；已有 task ID 后不会重复提交。热更新排队中的 Key 和并发限制；同进程移除再添加 Key 保留占用量与停用时间。
- 设置新增 `runninghub_api_keys`、`runninghub_per_key_concurrency`、`replicate_default_provider`、`replicate_default_variants`；包含持久化白名单、DPAPI 密钥存储、运行时热更新、激活码登记和密钥数量元信息。
- 复刻项目支持从完成的拆解报告建项、空白建项、字段编辑、增删镜头和完整顺序校验。旧拆解没有提示词时返回约定的 `analysis_has_no_prompts`。
- 首帧复用现有 `GenerateMany` 客户端；并发随 `max_image_concurrency`，上限 6。支持 PNG/JPEG/WebP 上传、20 MiB 文件上限、内容与扩展名核验，以及引用拆解关键帧。提示词包含风格、比例、无文字水印要求和商品外观约束。
- 一次 Generate 在启动工作协程前持久化所有 take；RunningHub 由 Key 池控制，Grok 全局最多 4 个并行任务。支持每镜头 1—4 个版本、单镜头重生成、provider 覆盖、缺首帧跳过、自动首次选中、手动选中、取消和删除。动作提示词完整保留，附加风格超限时截断并将警告保存到 take.error。
- 下载完成后 FFprobe 读取时长和分辨率。每个 take 从创建起计入 15 分钟总预算；关闭服务取消本地等待并保留已取得的上游 task ID。重启恢复查询已有 task ID；没有 ID 的中断任务明确失败，不重新提交。上传/提交成功后保存 ID 使用独立短时上下文，避免正常关闭丢失已经付费的任务。
- 拼接用 FFmpeg 将选中版本统一为项目比例对应的分辨率、30 fps、H.264/yuv420p、无音轨，再使用 concat demuxer 合成。保存按实际转码时长计算的镜头区间清单。缺版本时返回 `stitch_missing_takes`，列出镜头位置。
- 视频质检新增 `replicate` 和 `replicate_take` 来源。待审视频复制到独立审核目录，防止后续重拼或删除改变正在审核的文件。仅整片来源由服务端填写 `shot_position`，模型自己填写的镜头位置不被信任；项目记录 `qc_id`。
- 22 条 HTTP 路由、4 个 MCP 工具、付费/删除标记、Range 下载、中文成片下载名和 6 组 Go/TS 响应契约均已登记。
- 原 AIGC 图生视频已改走 RunningHub 原生 Go 客户端及新的 RunningHub Key，不再错误使用生图 Key，也不再运行 hypit Python 脚本。保留既有素材导入和来源记录行为。

## 迁移与范围

只追加迁移 **50**。新增三张复刻表及索引，按“新建表 → 复制数据 → 删除旧表 → 改名”重建 `video_qc_checks`，扩展来源约束。验证包含新库、49→50 升级、旧 QC 数据保留和重复迁移；逐条比较确认原 49 条迁移正文完全不变。

文件保存在 `<data_root>/replicate/<project_id>/`，文件读写与删除由 `os.Root` 约束。迁移和测试只使用临时数据库及临时目录，未操作 `video-console-data/`，未启动或重启 2030。

## 测试与审查

最终命令原文与退出码见 `artifacts/replicate-workbench-validation-20261016/`，汇总见 `results.json`。全仓 58 个含测试包通过，11 个包无测试文件，无失败。最终实测摘录如下：

```text
$ go build ./...
exit_code=0
elapsed_s=26.75

$ go vet ./...
exit_code=0
elapsed_s=26.922

$ go test ./... -count=1
ok  video-production-console/internal/aigc       50.470s
ok  video-production-console/internal/app        15.179s
ok  video-production-console/internal/httpapi   162.500s
ok  video-production-console/internal/mcpserver   3.492s
ok  video-production-console/internal/replicate  16.456s
ok  video-production-console/internal/runninghub  5.758s
ok  video-production-console/internal/settings   55.678s
ok  video-production-console/internal/store     238.281s
ok  video-production-console/internal/videoqc     5.350s
exit_code=0
elapsed_s=336.203

$ go test ./internal/replicate -run TestStitchRealMedia -count=1 -v
=== RUN   TestStitchRealMedia
--- PASS: TestStitchRealMedia (6.01s)
PASS
exit_code=0
```

唯一新增 TypeScript 文件使用本机已有编译器执行 `--noEmit --strict --target ES2022 --skipLibCheck`，退出码 0；未安装或修改前端依赖。`git diff --check` 无错误。范围验证结果为 `old_migrations=49`、`new_migrations=50`、`migrations_1_to_49_unchanged=true`、`forbidden_changed_files=[]`。

重点验证覆盖 RunningHub 协议、节点默认值、HTTP 200 业务错误和纯数字鉴权/余额码、密钥脱敏、响应上限、下载重定向与公网约束；Key 池轮询/并发/停用/热更；多镜头多版本同时运行、重生成、跳过、选中、取消、删除、总超时和重启不重复提交；不同分辨率真实 testsrc 视频转码拼接；质检来源、独立视频快照及镜头映射；所有新增 HTTP 路由成功与失败、真实 Service+SQLite 的 HTTP 主链路；MCP 与 TypeScript 契约。

审查中先复现再修复了仓储零时间戳、排队任务无法即时采用新并发限制、删除一个 take 时遗漏其他 take 的失败状态，以及 QC 前置条件错误被统一当作 500 等问题。

## 验证边界与接口偏差

- **接口路径、JSON 字段及指定前端修改范围的偏差为零。** 新增后端字段 `shot_position` 不改动被禁止修改的既有前端 QC 文件；前端对接时可按本任务契约使用该字段。
- 严格遵守禁令，没有访问真实 RunningHub、Grok 或模型网关。真实模型画面质量、真实额度扣费、线上排队情况和生产部署尚未验证；已验证的网络行为来自本地假服务。
- H3 比例枚举不是猜测：本机指定模板的 `115/aspect_ratio.fieldData` 明确包含 `9:16 (Portrait Widescreen)`、`1:1 (Square)`、`3:4 (Portrait Standard)`、`4:3 (Standard)` 和 `16:9 (Widescreen)`。完整 22 节点与源模板结构化比较一致；这些枚举的线上实际接受情况仍需真实调用验证。
- 模板 `186/value` 只写有整数默认值，没有上下界。按照任务允许的回退方案使用 **1—10 秒**，默认 6 秒；线上有效范围尚未实测。
- 本地提示词采用 4000 字符预算（既有 Grok 实现的上限），只截断动作以外的附加部分；动作本身超过预算时要求先缩短。H3 模板没有提供字符串最大长度，其线上上限尚未验证。
- task 只持久化 `key_slot`，不持久化 Key。恢复中的 RunningHub 任务依赖原槽位仍对应原 Key；重启前后重排或删除 Key 会影响这些任务的恢复。30 分钟停用仅在当前进程有效，符合任务要求。
- 当前本机 `CGO_ENABLED=0`，额外的 `go test -race` 不可执行；任务要求的 build/vet/test 和针对并发行为的阻塞假服务测试另有实测证据。

## 文件清单

- 原生上游：`internal/runninghub/{client.go,h3.go,pool.go,client_test.go,h3_test.go,pool_test.go}`。
- 核心服务：`internal/replicate/{types.go,service.go,files.go,generate.go,lifecycle.go,media.go,qc.go}`；测试：`{service_test.go,analysis_test.go,lifecycle_test.go,recovery_test.go,concurrency_test.go,media_test.go,qc_test.go}`。
- 存储：`internal/store/{migrations.go,replicate.go,replicate_test.go,replicate_migration_test.go,settings.go}`。
- 设置：`internal/domain/settings.go`、`internal/settings/{service.go,activation.go,overlay.go,service_test.go,runninghub_test.go}`。
- 旧 AIGC 修复：`internal/aigc/{video.go,boundaries_test.go,service_test.go}`。
- HTTP：`internal/httpapi/{replicate.go,replicate_test.go,replicate_integration_test.go,contract_types_test.go}`。
- 应用挂载：`internal/app/{app.go,replicate_test.go}`。
- 质检：`internal/videoqc/{service.go,report.go,replicate.go,replicate_test.go}`。
- MCP：`internal/mcpserver/{api_catalog.go,catalog.go,replicate_catalog_test.go}`。
- 唯一前端文件：`web/src/replicate/replicateTypes.ts`。
- 任务及验收：`TASK-replicate-workbench.md`、本报告、`artifacts/replicate-workbench-validation-20261016/` 的可复现验证脚本、日志和结果 JSON。

按用户要求提交当前分支，不推送。保留工作区原有的未跟踪 `.codex-last.txt`，不纳入本次提交。
