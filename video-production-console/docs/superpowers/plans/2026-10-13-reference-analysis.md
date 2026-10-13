# 对标视频拆解分析实施计划

> 按 `TASK.md` 已授权范围逐项实施；使用测试驱动开发并在提交前验证。核心流程由当前 Agent 执行。

**目标：** 为已缓存的对标视频提供异步多模态拆解、结构化报告和关键帧 API。

**架构：** `store` 持久化分析状态，`refanalysis` 负责镜头、抽帧、请求及任务生命周期，`httpapi` 提供访问入口。每次启动读取 settings Runtime，地址与密钥始终成组选择。帧元数据保存在分析目录的 `frames.json`，数据库保持任务指定的字段。

**技术：** Go 1.25、modernc SQLite、net/http、ffmpeg/ffprobe、OpenAI 兼容 HTTP。

**规格：** 仓库根目录 `TASK.md`。

## 约束

- 不修改前端、`reference_posts` 表、迁移 42 或 `video-console-data/`。
- 迁移 43 使用明确切片下标，空缺迁移不执行、不登记；历史验证允许为并行任务预留的 42，其他缺号仍报错。
- 异步任务独立 context，15 分钟超时；模型请求最多 6 分钟。
- 同一 reference 的 queued/running 任务去重；删除先取消并等待本服务的任务退出，再清理目录和记录。
- 帧路径只由受校验的 ID 和整数序号生成，校验实际解析后的路径，拒绝目录穿越及逃逸的符号链接。
- 不设置 `max_tokens`；仅发送关键帧和参考内容；错误不回显 URL、密钥或上游原始响应。
- 全部验证后用指定消息提交；随后生成不提交的 `CODEX_RESULT.md`。

## 1. 设置与持久化

- [x] 编写设置热更新、密钥掩码与非法 URL 用例；运行观察新增行为未实现。
- [x] 在 domain/store 白名单/settings 映射、校验、overlay 标签及热更新中加入 analysis 设置。
- [x] 编写迁移版本、重开、CRUD、排序上限、活动任务去重与级联删除测试。
- [x] 添加迁移 43、`ReferenceAnalysisRepository`；保留迁移 42 的执行机会。
- [x] settings/store 定向测试通过，全包检查由最终 `go test ./internal/...` 覆盖。

## 2. 镜头、关键帧和模型请求

- [x] 编写固定 showinfo、相邻切点、最短相邻镜头合并、均匀回退和采样测试。
- [x] 实现 `Shots(ctx, ffmpeg, ffprobe, video) (VideoInfo, []Shot, error)` 与 `Keyframes(ctx, ffmpeg, video, shots, directory) ([]Frame, error)`。
- [x] 编写配置顺序、请求顺序/时间/无转写/focus、JSON 兜底与字段缺失测试。
- [x] 实现 `ResolveConfig`、中文 prompt、请求构建、报告类型和可注入 HTTP 客户端。
- [x] 使用 `exec.LookPath` 查找 ffmpeg/ffprobe 的集成测试生成三段 testsrc 视频，验证两个切点及 JPEG 尺寸。
- [x] `internal/refanalysis` 定向测试与最终全包测试通过。

## 3. 服务、路由及应用挂载

- [x] 用 httptest 和假帧提取测试 queued/running/completed、失败、重复启动、请求取消不影响任务及 Resume。
- [x] 实现默认 `MediaLocator`、任务启动/查询/恢复/删除、帧清单落盘与安全读取。
- [x] 编写路由状态码、报告与帧响应、穿越与符号链接拒绝、删除清理及运行中删除测试。
- [x] 挂载 `/api/reference-analyses`，应用启动调用 Resume，应用关闭取消后台分析。
- [x] refanalysis/httpapi/app 定向测试与最终全包测试通过。

## 4. 验证与交付

- [x] 审查差异与 TASK 所有验收项，确认没有修改禁止路径。
- [x] 运行 `go build ./...`、`go vet ./...`、`go test ./internal/...`，最终全部通过。
- [x] 尝试 race 检查；环境 `CGO_ENABLED=0` 且 PATH 无 gcc，无法运行，交付说明中保留此限制。

交付步骤：`git add -A` 后检查暂存内容，用指定消息提交；提交后写根目录 `CODEX_RESULT.md` 并确认其不在提交中。最终提交号与交付结果记录在该说明中。

## 验证记录（2026-10-13）

- 构建、vet、最终全量 internal 测试均退出 0。首轮全量测试中，既有 `mediacatalog.TestExecRunnerKillsProcessOnContextTimeout` 耗时 26.75 秒，超过 15 秒阈值；单独复跑 5.50 秒通过，再跑全量通过。该既有模块未改动。
- 真实 ffmpeg/ffprobe 集成测试执行通过；目录符号链接逃逸测试在本机执行通过，未跳过。
- 迁移回归覆盖 43 先执行、42 空槽不登记、重开、合并后补执行及补执行前备份。测试只以 `SELECT 1` 模拟非空迁移槽，不实现真实迁移 42。
- 仓储并发去重、请求取消后任务继续、运行中删除等待最后写入、重启中断复位、密钥轮换热更新和错误脱敏均有实际通过的测试。
- 模型调用由 httptest 和注入客户端验证；未调用真实收费模型。race 工具以“requires cgo”退出，不能据此声称 race 通过。
