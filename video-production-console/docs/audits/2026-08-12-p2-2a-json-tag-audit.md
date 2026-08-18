# P2-2a 审计：HTTP 响应中缺 `json:` tag 的 struct 字段

> 时点快照，结论仍有效。但文中 `web/src/App.tsx:NNNN` 的行号取自 `App.tsx` 拆分前（当时约 2900 行，现为 1086 行），相关调用点已迁到 `web/src/{tasks,assets,projects,chat}/` 等目录——按符号名查找，不要按行号。

审计日期：2026-08-12。范围：`internal/domain`、`internal/httpapi` 全部 `writeJSON` 调用点，以及其它被 HTTP 响应直接序列化的包（`internal/history`、`internal/settings`、`internal/conversation`）。

审计方法：枚举 `internal/httpapi` 下所有 `writeJSON(` 调用点的载荷类型，逐个回溯类型定义，判定字段是否带 `json:` tag；再确认每个类型是否被 `json.Unmarshal` 用于读取库内 JSON 列或磁盘 manifest（决定能否直接改 tag）。

## A 组：必须改（任务 timing / phase，前端已在消费）

`domain.TaskPhaseRun`（`internal/domain/timings.go:24-39`）——出网路径：`internal/httpapi/tasks.go:103`（`GET /api/tasks/{id}` 的 `timing_runs`）、`tasks.go:320`（`GET /api/tasks/{id}/timing/runs`）、以及作为 `TaskTimingSummary.Phases` 的元素。

| file:line | 字段 | 当前键名 | 目标键名 |
| --- | --- | --- | --- |
| timings.go:25 | `ID` | `ID` | `id` |
| timings.go:26 | `TaskID` | `TaskID` | `task_id` |
| timings.go:27 | `PhaseKey` | `PhaseKey` | `phase_key` |
| timings.go:28 | `DisplayName` | `DisplayName` | `display_name` |
| timings.go:29 | `ExternalID` | `ExternalID` | `external_id` |
| timings.go:30 | `DetailJSON` | `DetailJSON` | `detail_json` |
| timings.go:31 | `Attempt` | `Attempt` | `attempt` |
| timings.go:32 | `Source` | `Source` | `source` |
| timings.go:33 | `State` | `State` | `state` |
| timings.go:34 | `StartedAt` | `StartedAt` | `started_at` |
| timings.go:35 | `RunningAt` | `RunningAt` | `running_at` |
| timings.go:36 | `FinishedAt` | `FinishedAt` | `finished_at` |
| timings.go:37 | `DurationMS` | `DurationMS` | `duration_ms` |
| timings.go:38 | `CreatedAt` | `CreatedAt` | `created_at` |

`domain.TaskTimingSummary`（`internal/domain/timings.go:55-66`）——出网路径：`internal/httpapi/tasks.go:102`（`GET /api/tasks/{id}` 的 `timing_summary`）、`tasks.go:301`（`GET /api/tasks/{id}/timing/summary`）。

| file:line | 字段 | 当前键名 | 目标键名 |
| --- | --- | --- | --- |
| timings.go:56 | `TaskID` | `TaskID` | `task_id` |
| timings.go:57 | `TotalMS` | `TotalMS` | `total_ms` |
| timings.go:58 | `PreparationMS` | `PreparationMS` | `preparation_ms` |
| timings.go:59 | `QueueMS` | `QueueMS` | `queue_ms` |
| timings.go:60 | `ExecutionMS` | `ExecutionMS` | `execution_ms` |
| timings.go:61 | `QueueEstimated` | `QueueEstimated` | `queue_estimated` |
| timings.go:62 | `SlowestPhase` | `SlowestPhase` | `slowest_phase` |
| timings.go:63 | `SlowestPhasePercent` | `SlowestPhasePercent` | `slowest_phase_percent` |
| timings.go:64 | `Phases` | `Phases` | `phases` |
| timings.go:65 | `LegacyWithoutPhases` | `LegacyWithoutPhases` | `legacy_without_phases` |

**持久化风险判定：安全。** 两个类型只经 `database/sql` 逐列 `Scan` 读写（`internal/store/task_timings.go:377-395` 的 `scanPhase`、`SummaryForTask` 在内存中聚合），列名由 SQL 语句决定，与 `json:` tag 无关。全仓没有把这两个类型作为 `json.Unmarshal` 目标的调用点：技能侧耗时文件走独立的 artifact 类型 `timing.timingArtifact` / `timing.timingPhase`（`internal/timing/importer.go:35-53`，本身已是 snake_case tag），解析后再手工映射为 `domain.SkillTimingRun`。`DetailJSON` 是已序列化的字符串列，按字符串原样透传，不参与结构体级反序列化。

## B 组：也在出网、但前端未消费（本工单一并统一）

`domain.RegistrationAttempt`（`internal/domain/montage.go:24-31`）——出网路径：`internal/httpapi/montage.go:49`（`POST /api/tasks/{id}/retry-registration` 的 202 响应体）。

| file:line | 字段 | 当前键名 | 目标键名 |
| --- | --- | --- | --- |
| montage.go:25 | `ID` | `ID` | `id` |
| montage.go:25 | `TaskID` | `TaskID` | `task_id` |
| montage.go:25 | `ManifestPath` | `ManifestPath` | `manifest_path` |
| montage.go:25 | `WorkspacePath` | `WorkspacePath` | `workspace_path` |
| montage.go:26 | `State` | `State` | `state` |
| montage.go:27 | `Attempt` | `Attempt` | `attempt` |
| montage.go:28 | `RegisteredPath` | `RegisteredPath` | `registered_path` |
| montage.go:28 | `ReceiptPath` | `ReceiptPath` | `receipt_path` |
| montage.go:28 | `ErrorCode` | `ErrorCode` | `error_code` |
| montage.go:28 | `ErrorMessage` | `ErrorMessage` | `error_message` |
| montage.go:29 | `StartedAt` | `StartedAt` | `started_at` |
| montage.go:30 | `FinishedAt` | `FinishedAt` | `finished_at` |

`domain.ChatMessage`（`internal/domain/conversations.go:48-60`）——出网路径：`internal/httpapi/history.go:68`（`GET /api/codex/history/{id}` 的 `messages`）。同一类型在 `internal/httpapi/conversations.go:117` 走的是 `chatMessageView` DTO（`conversations.go:218-226`，已是 snake_case），history 这条是唯一的裸序列化出口。

| file:line | 字段 | 当前键名 | 目标键名 |
| --- | --- | --- | --- |
| conversations.go:49 | `ID` | `ID` | `id` |
| conversations.go:50 | `SessionID` | `SessionID` | `session_id` |
| conversations.go:51 | `Role` | `Role` | `role` |
| conversations.go:52 | `Kind` | `Kind` | `kind` |
| conversations.go:53 | `Content` | `Content` | `content` |
| conversations.go:54 | `DeliveryStatus` | `DeliveryStatus` | `delivery_status` |
| conversations.go:55 | `ClientKey` | `ClientKey` | `client_key` |
| conversations.go:56 | `CodexItemID` | `CodexItemID` | `codex_item_id` |
| conversations.go:57 | `TurnID` | `TurnID` | `turn_id` |
| conversations.go:58 | `Sequence` | `Sequence` | `sequence` |
| conversations.go:59 | `CreatedAt` | `CreatedAt` | `created_at` |

**持久化风险判定：安全。** 两者同样只经 `database/sql` 列级扫描（`internal/store/montage.go`、`internal/store/conversations.go:274-311`），无 `json.Marshal` / `json.Unmarshal` 参与；`internal/history/app_server_source.go:55` 是在内存里构造 `domain.ChatMessage`，不做 JSON 往返。

**前端消费判定：未消费。** `web/src/App.tsx:1596` 只读 `retry-registration` 的 HTTP 状态码，不读响应体；`GET /api/codex/history/{id}` 全仓前端无调用点（`App.tsx:2014/2024` 是列表，`App.tsx:2019` 是 `resume`/`fork`）。

## C 组：报告但不改

- `conversation.SendReceipt`（`internal/conversation/broker.go:53-59`）在 `internal/httpapi/conversations.go:187` 裸序列化，输出 `MessageID`/`Method`/`ThreadID`/`TurnID`/`State`。`internal/conversation/` 有并行任务占用，本工单不改；建议后续在 `httpapi/conversations.go` 增加响应 DTO（不动 domain 层）。前端未读该响应体的字段。
- `domain.SkillTimingRun`（`timings.go:41-53`）、`domain.TaskTimingAggregate`（`timings.go:68-76`）、`domain.TaskPhaseTimingAggregate`（`timings.go:78-84`）缺 tag，但没有任何 HTTP 出口（只在 `internal/store`、`internal/codex`、`internal/taskcompletion` 与测试内使用），不属于本工单范围。
- 其它缺 tag 的 domain 类型（`models.go`、`assets.go`、`workflows.go`、`conversations.go` 的会话/出入站队列类型等）全部经 httpapi 的 view/DTO 或 `map[string]any` 转换后出网，键名已是 snake_case，无需改动。
- `schemas/` 下无任何契约引用 timing/phase 响应键（grep `PhaseKey|phase_key|DurationMS|duration_ms|TimingSummary|timing_summary` 无命中），无需同步。

## 结论

需要补 tag 的字段共 47 个，分布在 4 个类型、3 个文件：`internal/domain/timings.go`（A 组 24 个）、`internal/domain/montage.go`（B 组 12 个）、`internal/domain/conversations.go`（B 组 11 个）。全部判定为可直接改 tag，无需额外 DTO 或旧键 alias。
