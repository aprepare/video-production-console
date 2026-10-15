# 修复单：监控后端审查问题（在当前分支 codex/monitor-backend 上修）

先读 `TASK-monitor-backend.md`（原契约，不变）。以下问题来自代码审查，逐条修复并补测试。接口路径和 JSON 字段不许变。不要改 `web/` 任何文件。

## 必须修

1. **评论洞察引用校验过严**（`internal/commentinsight/report.go`）。现在一条引用不是原评论的精确子串、或某个结论 quotes 为空、或情绪比例之和≠100，整份报告失败。改为：
   - 比对前两边都做：NFKC 归一化 → 去掉全部空白、标点（含全角）、emoji/符号 → 小写。quote 含 `…` 或 `...` 时按片段切开，每个片段（归一化后长度≥2）都要在**同一条**评论里按顺序出现。quote 去掉开头的 `[赞N]` 前缀再比。
   - 不合格的 quote **丢弃**而不是失败；pain_points/questions/needs 中 quotes 全被丢掉的结论也丢弃；**opportunities 和 content_ideas 允许没有 quotes**（它们是综合推断）。只有当 pain_points、questions、needs、opportunities 全部为空时才判失败。
   - sentiment：接受整数或小数；若三项之和在 0.99~1.01 之间视为比例，×100；之后按比例归一到合计 100（取整，余数补给最大项）；全为 0 时保留 0。
   - 仍然禁止“编造引用”：归一化后找不到出处的就丢弃，不能保留。
   - 测试：省略号截断、全半角标点互换、去 emoji、带 `[赞12]` 前缀都能保留；跨两条评论拼接的被丢弃；一条坏引用不影响其余结论；opportunities 无引用仍保留；60/25/16 归一为合计 100；0.6/0.25/0.15 变成 60/25/15。

2. **打分漏条目**（`internal/monitor/service.go` 约 466 行、`scoring.go`）。去掉“只给本轮新条目打分”的条件，改为每轮从该任务里挑 `score_status IN ('none','pending')` 的条目（优先本轮出现的，再按 first_seen_at 升序），最多 30 条。启动恢复时把遗留 `pending` 视为可重新打分。测试：第一轮 45 条新条目 → 第一轮打 30 条，第二轮补打剩下 15 条；进程中断留下的 pending 下一轮会被打分。

## 应该修

3. **飙升误报**（`surge.go`、`store/monitors.go`）：
   - 平台某次返回的点赞/评论为缺失或 null 时，不要用 0 覆盖旧计数：platformapi 层要能区分“0”和“缺失”（可以在 Post 里加内部字段，如 `CountsKnown bool`，**不要**改对外 JSON）；缺失时保留旧值、本条本轮不做飙升判定、快照也不写。
   - 小红书计数是近似值（≥10000 时以“万”为单位，精度 1000）：当 platform=xhs 且 prev.likes ≥ 10000 时，绝对增量规则只有在 delta ≥ max(surge_like_delta, 2000) 时才命中；相对增幅规则照常。
   - 相对增幅分支额外要求 delta > 0（min_base=0 时 0≥0 误判）。
   - 两次快照间隔超过 `2 × interval_minutes` 时（视频掉出搜索后又回来），绝对增量规则不判，只看相对增幅且要求 `like_rate_per_hour` 也达到 `surge_like_delta / (interval_minutes/60)`。
   - 测试覆盖以上每一条。

4. **打分超时**（`scoring.go`）：封面下载+转码改为并发（最多 4 个），总预算 30 秒，各自超时 10 秒，失败的只是不附图；模型调用用独立的 90 秒 ctx；纯文字重试也用新的 ctx。HTTP 429 和 5xx **不要**触发纯文字重试（只有 400/413/415/422 这类“不支持图片/请求太大”才退回纯文字）。

5. **JSON 解析**（`monitorai/client.go` 及调用处）：请求加 `"response_format":{"type":"json_object"}` 和 `"temperature":0.2`；若上游对 response_format 返回 400，自动去掉 response_format 重试一次。解析时：先试整体解析，失败则取第一个 `{` 到最后一个 `}` 之间的子串再解析（兼容前言+```json 围栏）。测试：带前言和围栏的输出能解析。

6. **PATCH/POST 接受完整对象**（`httpapi/monitors.go`）：前端可能回传完整 TaskJSON。对 monitors 的 POST/PATCH 不要用 `DisallowUnknownFields`，或显式忽略只读字段（id、running、item_count、unread_count、surge_count、last_*、next_run_at、created_at、updated_at）。测试：把 GET 得到的完整 JSON 改一个字段后 PATCH 回去，成功且只读字段不被修改。

## 低优先（顺手修）

7. 第 2 页及以后失败时，先把已拿到页的结果按正常流程入库（飙升/打分照常），run 标为 failed 并在 error 里写“第 N 页失败：原因（已保存前 N-1 页）”。
8. history 查询改为 `ORDER BY at DESC` 再反转，不依赖 rowid。

## 完成标准

- `go build ./...`、`go vet ./...`、`go test ./...` 全部通过（全量测试若遇到既有的偶发会话测试超时，单独重跑确认）。
- 在 `TASK-monitor-backend.RESULT.md` 末尾追加“审查修复”一节：每条怎么修的、新增测试名、测试结果摘录。
- `git add` + `git commit`，不 push。
- 禁令同原任务单：不访问真实网关/模型，不碰 `video-console-data/`，不启动 2030 控制台。
