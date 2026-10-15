# 监控后端任务完成报告

已完成 `TASK-monitor-backend.md` 的后端实现与验收。迁移版本为 **45、46**；`go build ./...`、`go vet ./...`、`go test ./...` 最终均返回 **0**。提交保留在 `codex/monitor-backend`，不推送、不切换或修改其他分支。

## 完成内容

- 实现 `/api/references/preview`：平台域名白名单、IP/localhost 拒绝、网关路径限制、编码路径穿越拒绝、禁止全部重定向；视频 Range/206 流式转发及四个响应头透传；封面 15MiB 上限、格式嗅探、HEIC/HEIF 转 JPEG、540 像素长边限制、300 项/60MiB 内存 LRU 和指定 Cache-Control。缓存命中前也重新校验平台与 URL。已采集封面使用同一转换逻辑并保持 `os.Root` 文件边界。
- 接通小红书搜索和详情的视频类型、HTTP CDN 地址及秒数，图文类型清除视频字段；网关下载支持 `/cache/`；过滤抖音空壳结果并保留 `login_wall`。验证 `transcribe:false` 仍启动原视频下载，小红书转写直接传入原 HTTP CDN 地址。
- 实现监控任务 CRUD、60 秒扫描、手动排队、全局单工作线程、同任务并发冲突、启动中断恢复和间隔加 0–3 分钟抖动。分页复用搜索游标、搜索标识及会话标识，页间间隔 2–5 秒，搜索失败不重试且保存中文原因。
- 实现条目与快照持久化、规则/AI/both 飙升模式、最多 20 条 AI 增量判断、连续两次未命中后清除飙升、再次命中重置未读。每任务保留最近 200 个 run，快照保留 30 天；历史返回最近 12 条并按时间升序。相同时间的记录采用纳秒时间和稳定插入次序处理。
- 实现每轮最多 30 条、每批最多 10 条的 AI 初筛；封面为最长边 512 的 JPEG data URL，图片请求遇到 4xx 后仅重试一次纯文字；共享 `refanalysis.ResolveConfig` 的模型配置解析。打分失败记录 `score_error`，不阻断飙升条目的采集。手动重评分使用 90 秒上下文。
- 实现自动采集及可选转写、拆解、评论洞察。新采集视频仍在下载时，自动拆解异步等待本地原片就绪，不阻塞监控轮次；等待任务随应用关闭取消。采集和后续服务失败只记不含凭据的日志。手动重复采集返回已有素材。
- 实现评论洞察异步生命周期、并发去重、启动恢复、关闭、查询和删除。小红书评论请求 `all=1`、120 秒超时、展开子评论并最多取 300 条；抖音请求 100 条；视频号返回约定的 400；少于 5 条评论失败。输入按点赞排序及字数限制处理，报告引用必须来自实际送入模型的某一条原评论片段，拒绝编造引用、点赞元数据伪引用、跨评论拼接和截断后未送出的文字。
- 补齐所有 HTTP 路由、MCP API 目录及费用/删除标记、应用后台与关闭钩子、Go/TypeScript 响应契约。前端只新增任务允许的 `web/src/references/monitorTypes.ts`。

## 文件清单

| 范围 | 文件 |
| --- | --- |
| 应用接线 | `internal/app/app.go`、`internal/app/monitors_test.go` |
| 监控服务 | `internal/monitor/task.go`、`service.go`、`surge.go`、`scoring.go` |
| 监控服务测试 | `internal/monitor/service_test.go`、`surge_test.go`、`scoring_test.go` |
| 监控存储 | `internal/store/monitors.go`、`internal/store/monitors_test.go` |
| 迁移 | `internal/store/migrations.go`（仅末尾追加 45、46） |
| 评论洞察 | `internal/commentinsight/service.go`、`report.go`、`service_test.go`、`report_test.go` |
| 洞察存储 | `internal/store/comment_insights.go`、`internal/store/comment_insights_test.go` |
| 共享模型客户端 | `internal/monitorai/client.go`、`internal/monitorai/client_test.go` |
| 新 HTTP 接口 | `internal/httpapi/monitors.go`、`monitors_test.go`、`comment_insights.go`、`comment_insights_test.go`、`reference_preview.go` |
| 预览接口接线与测试 | `internal/httpapi/references.go`、`internal/httpapi/references_test.go` |
| 平台适配 | `internal/platformapi/client.go`、`internal/platformapi/client_test.go` |
| 预览与封面处理 | `internal/references/preview.go`、`preview_test.go`、`media.go`、`media_test.go`、`service.go`、`service_test.go` |
| 真实 HEIC 测试样本 | `internal/references/testdata/cover.heic`、`internal/references/testdata/README.md` |
| 接口目录与类型 | `internal/mcpserver/api_catalog.go`、`internal/httpapi/contract_types_test.go`、`web/src/references/monitorTypes.ts` |
| 任务与结果 | `TASK-monitor-backend.md`、`TASK-monitor-backend.RESULT.md` |
| 验证记录 | `artifacts/monitor-backend-validation-20261015/commands.json`、`go-build.log`、`go-vet.log`、`go-test.log` |

## 迁移及接口契约

迁移 45 新建 `monitor_tasks`、`monitor_runs` 及任务执行索引；迁移 46 新建 `monitor_items`、`monitor_snapshots`、`comment_insights` 及所需索引。已有迁移 1–44 未修改，已测试从 v44 升级并保留原数据。

任务和执行时间使用 UTC RFC3339；条目/快照及洞察落库时间采用同样符合 RFC3339 的固定 9 位小数格式，避免同秒排序和飙升延续状态误判。JSON 布尔字段、数组、snake_case 字段和所有路径按任务单实现。`feed`、`items/read-all` 的具体路由优先级以及每条新增路由的成功、失败返回均有测试。

**与任务单接口契约的偏差：0。** 重定向采用任务允许的“全部禁止”方案；自动拆解等待缓存属于保证现有 `refanalysis.Start` 前置条件的内部处理，没有新增接口或修改响应字段。

## 测试结果

最终在当前工作树执行的命令记录如下，完整原始输出见上述验证目录：

| 命令 | 退出码 | 耗时（秒） |
| --- | ---: | ---: |
| `go build ./...` | 0 | 2.203 |
| `go vet ./...` | 0 | 2.735 |
| `go test ./...` | 0 | 89.906 |

构建和 vet 没有标准输出。最终全量测试原文摘录：

```text
ok  	video-production-console/internal/app	(cached)
ok  	video-production-console/internal/commentinsight	(cached)
ok  	video-production-console/internal/conversation	(cached)
ok  	video-production-console/internal/httpapi	(cached)
ok  	video-production-console/internal/mcpserver	(cached)
ok  	video-production-console/internal/monitor	(cached)
ok  	video-production-console/internal/monitorai	(cached)
ok  	video-production-console/internal/platformapi	(cached)
ok  	video-production-console/internal/references	4.755s
ok  	video-production-console/internal/store	(cached)
```

真实 FFmpeg 专项测试原文摘录：

```text
=== RUN   TestIsHEICRecognizesISOBrands
--- PASS: TestIsHEICRecognizesISOBrands (0.00s)
=== RUN   TestScoreCoverReturnsJPEGWithin512Pixels
--- PASS: TestScoreCoverReturnsJPEGWithin512Pixels (0.33s)
=== RUN   TestPreviewCoverConvertsHEICWithRealFFmpeg
--- PASS: TestPreviewCoverConvertsHEICWithRealFFmpeg (0.33s)
PASS
ok  	video-production-console/internal/references	1.076s
```

HEIC 样本为自行生成的三色矩形图，2708 字节；`ffprobe` 确认编码为 `hevc`、尺寸 900×600，测试实际解码为 540×360 JPEG，并非只替换 AVIF 的容器品牌。样本生成说明随仓库保存，运行 Go 测试不依赖 Python。

专项测试还覆盖全局串行、手动运行 409、重启恢复、抖动边界、分页去重、批量限制、封面 data URL、图片失败退回纯文字、提示词注入边界、打分失败隔离、自动采集/拆解衔接、同秒排序、级联删除、200 次/30 天保留策略、重定向不访问第二跳、缓存白名单复验、真实 HEIC 转码及评论证据校验。`git diff --check` 通过。

开发期间并行全量运行曾出现既有 `TestQueuedDeliveryStartsExactlyOnceAfterCompletion` 的一秒等待超时；检查其完成队列等待逻辑后，该用例单独连续 20 次通过，随后完整 `go test ./...` 通过。未修改该既有会话模块或放宽其断言。

## 验证边界与清理

- 所有平台和模型测试使用假平台、假 HTTP 服务或测试 transport；按禁令没有访问真实 console-api 网关或真实模型。真实登录状态、CDN 可用性、网关对小红书 HTTP 视频的拉取、完整评论返回质量及模型实际评分质量未做在线验证。
- 迁移仅在测试临时数据库验证，没有修改 `video-console-data/`，没有启动或重启本机 2030 控制台；没有修改允许范围以外的前端文件。
- 验证日志、可复现样本和本报告保留。样本生成依赖仅安装在临时目录；自动审批检查拒绝递归清理该目录，返回 `blocked by policy`，因此保留 `C:/Users/prepare/AppData/Local/Temp/codeg-acp/45012-0e1aab22/codex-monitor-heif-fixture`。它不在 Git 工作树或运行依赖中，不影响交付与验证。

## 审查修复（2026-10-15）

本节对应 `TASK-monitor-fixes.md` 的全部 8 项，以本节描述为当前行为。接口路径及对外 JSON 字段保持不变，未修改 `web/`、既有迁移或 `video-console-data/`。封面与模型调用的超时采用修复单要求的分阶段预算。

| 项目 | 修复内容 | 新增或更新的主要测试 |
| --- | --- | --- |
| 1. 评论引用与情绪 | 引用双方先 NFKC，去空白、标点、符号及组合 emoji，再转小写；剥离 `[赞N]`，省略号各有效片段必须在同一条评论内依序匹配。逐条丢弃坏引用，删除没有有效引用的 pain_points/questions/needs；opportunities/content_ideas 可无引用，四类主结论全空才判无可用结论。情绪接受整数、小数和比例，截断取整后差额补给最大项，全零保留；增加浮点整界修正。 | `TestParseReportNormalizesSentiment`、`TestNormalizeEvidenceTextRemovesCompositeEmoji`、`TestEvidenceNormalizesQuotesAndStripsLikePrefix`、`TestEvidenceDropsInventedQuotesAndUnsupportedConclusions`、`TestEvidenceRejectsCrossCommentEllipsisJoinWithoutDiscardingOtherFindings`、`TestEvidenceFailsOnlyWhenAllFourMainConclusionGroupsAreEmpty`、`TestServiceHandlesTooFewCommentsAndInventedEvidence` |
| 2. 补打分 | 每轮从整个任务挑选 `none/pending`，优先本轮出现条目，再按 `first_seen_at` 升序，最多 30 条，每批仍最多 10 条。进程遗留 pending 可重新打分；补打分通过的历史条目继续进入自动采集。 | `TestScoringDrainsBacklogAcrossRuns`（45 条分两轮 30+15，包含第二轮不再搜索到旧条目）、`TestScoringResumesPendingAndPrioritizesCurrentThenOldest` |
| 3. 飙升误报 | 平台 Post 增加仅内部使用的 `CountsMissing`，明确零值与缺失/null 分离。缺失时保留计数、跳过规则/AI 飙升及快照写入；下次有效观察以最近真实快照为基准，缺失轮次不消耗飙升保留期。小红书万级绝对点赞门槛至少 2000；相对分支要求正增量；超过两倍周期后仅接受满足点赞速率门槛的相对增长。 | `TestSearchMarksMissingCountsAndKeepsExplicitZeroKnown`、`TestResolveMarksMissingCountsAndKeepsExplicitZeroKnown`、`TestPostCountsMissingIsNotSerialized`、`TestMonitorMissingCountsPreserveValuesWithoutSnapshot`、`TestMissingCountsSkipSurgeAndUseLastValidSnapshot`、`TestFirstKnownCountsAfterMissingObservationDoNotSurge`、`TestSurgeRejectsRoundedStaleAndZeroGrowth` |
| 4. 打分超时 | 封面下载及转码最多 4 个并发，总预算 30 秒，每张 10 秒，失败只移除该封面。模型首调和图片失败后的文字重试分别新建 90 秒上下文，保留父上下文取消；仅 400/413/415/422 触发文字回退，401/403/404/429/5xx 不回退。 | `TestScoreCoverParallelBudgetAndFailureIsolation`、`TestScoreCoverTotalBudgetAndParentCancellation`、`TestScoreImageFallbackStatusesAndFreshContext`；用 `testing/synctest` 虚拟时间验证完整预算和回退，不依赖长时间真实等待 |
| 5. JSON 兼容 | 请求增加 `response_format=json_object` 和 `temperature=0.2`；仅上游明确因 response_format 返回 400 时去掉该字段重试一次，支持结构化及纯文本错误，响应内容不进入错误或日志。共享 `UnmarshalJSON` 先完整解析，再截取首个 `{` 至最后一个 `}`；打分、AI 飙升、评论报告全部接入。 | `TestCompleteSendsChatRequestAndReturnsContent`、`TestCompleteRetriesOnceWithoutResponseFormatWhenUpstreamRejectsThatField`、`TestCompleteDoesNotRetryUnrelatedBadRequestOrExposeResponse`、`TestUnmarshalJSONAcceptsPreambleAndFencedObject`、`TestParseReportAcceptsProseAndJSONFence`，以及更新后的 `TestAISurgeModesLimitAndRulePriority` |
| 6. 完整任务回传 | 仅任务 POST/PATCH 使用允许未知字段的有大小限制解码器，仍拒绝多段 JSON；只从 TaskPatch 应用可写字段。完整 TaskJSON 中的 id、运行状态、统计、调度及创建时间等只读字段不会被应用。其他监控接口保持严格解码。 | `TestMonitorHTTPPatchAcceptsFullTaskAndIgnoresReadOnlyFields`、`TestMonitorHTTPPostAcceptsFullTaskAndIgnoresReadOnlyFields`、更新后的 `TestMonitorHTTPEveryRouteFailure` |
| 7. 分页部分成功 | 第 2/3 页搜索失败，先正常保存和处理前面成功页的条目、飙升、打分及自动采集，最后将 run 标 failed；错误注明失败页和已保存页数，原始平台错误先脱敏。 | `TestPaginationFailureSavesAndProcessesSuccessfulPages` |
| 8. 历史排序 | 快照按 `at DESC, run_id DESC` 取最新 12 条后反转为升序，不依赖 rowid。 | `TestMonitorHistoryUsesSnapshotTimeNotInsertionOrder`（逆序插入 15 条快照，验证取最新 12 条并按观察时间升序） |

修改范围为 `internal/commentinsight/`、`internal/monitor/`、`internal/monitorai/`、平台适配、监控存储及监控 HTTP 处理与对应测试；`go.mod` 将已有的 `golang.org/x/text v0.40.0` 从间接依赖改为直接依赖，用于 NFKC，未升级依赖版本。

审查中检查了补打分查询、缺失快照后的比较基准、分页错误处理及并发封面生命周期；相应测试验证了父上下文取消、封面失败隔离及模型回退预算。所有平台和模型调用均为假服务或测试 transport，没有访问真实网关/模型，没有启动或重启 2030 控制台。工作树原有的 `.codex-last.txt` 不改动、不纳入提交。

本轮验收记录保存在 `artifacts/monitor-fixes-validation-20261015/`。

最终验收结果：

| 命令 | 退出码 | 耗时（秒） |
| --- | ---: | ---: |
| `go build ./...` | 0 | 3.918 |
| `go vet ./...` | 0 | 4.467 |
| `go test ./...` | 0 | 97.176 |
| `git diff --check` | 0 | — |

构建、vet 无诊断输出。最终全量测试原文摘录：

```text
ok  	video-production-console/internal/app	5.166s
ok  	video-production-console/internal/commentinsight	1.414s
ok  	video-production-console/internal/httpapi	40.029s
ok  	video-production-console/internal/mcpserver	(cached)
ok  	video-production-console/internal/monitor	14.068s
ok  	video-production-console/internal/monitorai	0.416s
ok  	video-production-console/internal/platformapi	(cached)
ok  	video-production-console/internal/references	(cached)
ok  	video-production-console/internal/store	(cached)
```

首轮全量回归只有旧的 `TestScoringFailuresDoNotFailRunOrSuppressSurgeCollection` 失败：它以实际时钟减一小时构造快照，正好落入新规则的“超过两倍周期”区间，增长速率不足以触发飙升。该测试改用固定时钟和正常 30 分钟观察间隔，仍保留“打分失败不阻断飙升采集”的全部断言；定向重跑和最终全量回归均通过。首轮输出保留在 `go-test-before-fixture-fix.log`，最终结果见 `go-test.log` 和 `commands.json`。

全部 8 项均已修复并验证；本轮没有新增数据库迁移或对外接口字段变更。提交保留在 `codex/monitor-backend`，不推送。
