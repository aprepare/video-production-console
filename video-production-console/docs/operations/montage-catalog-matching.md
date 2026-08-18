# 混剪自动选片（catalog + 向量）

风景/电影混剪不再手工选片。`montage.execute` 走本机 `montage-script-run`，由 `BuildV2` 从 `media_root/catalog.db` 召回镜头，再写成 `production_plan.json`。本文说明**当前真实管线**，以及为什么「库很大却对不上买房画面」。

配套入口：[使用说明 · 素材库](../USER-GUIDE.md)、[AI 接手说明 §2.2](../AI-HANDOFF.md)、[架构 §5.4 / §11.5](../ARCHITECTURE.md)。

## 1. 选片顺序

```
口播分句
  → LocalIntentAnalyzer（中英词表桥，不走对话模型分析）
  → rankLibrary
       标签四级池（每级最多 50，合计截断 80）
       + 全库向量近邻（最多扫 2000 镜，每段并入 top 32）
  → 可选 ShotSelector（对话模型只在每段 top 8 里点 1 个）
  → selectTimelineV2 配额铺轨（B-roll 1.5×）
```

| 步骤 | 代码 | 失败时 |
|---|---|---|
| 注入客户端 | `montagescript/catalog_clients.go` `attachCatalogClients` | Embedder 构造失败 → `embedding_disabled`，仍用标签 |
| 标签召回 | `montageplan/match.go` `recallForIntent` | 空池再中性补镜 |
| 全库向量 | `rankLibrary` + `mediacatalog.RecallReadyShots(2000)` | 无 embedder 则跳过 |
| 对话点选 | `montageplan/shot_select.go` | `llm_shot_select_fallback`，保留向量/标签排序 |
| 铺轨 | `quota.go` `v2PlaybackSpeed=1.5` | 配额 warning，不卡死任务 |

`planner_notes` 必须出现其中一条，才能判断向量有没有真正跑起来：

- `embedding_pool: scanned N catalog shots` — 扫了全库（财经库约 569）
- `embedding_disabled: embedder not configured` — 子进程没接到向量配置

对话点选成功时还会有 `llm_shot_select: picked N segment shots`。这只说明短名单点选成功，**不能**代替 `embedding_pool`。

## 2. 设置怎么接到混剪子进程

设置页填写：

- `embedding_base_url`（写到 `/v1`，例如硅基 `https://api.siliconflow.cn/v1`）
- `embedding_model`（须与建库时同一模型，当前验收用 `Qwen/Qwen3-Embedding-8B`）
- `embedding_api_key`（只存 `encrypted_secrets`，页面只显示是否已配置）

写入 `task_manifest.non_secret_settings` 的是 URL/模型。密钥和 URL/模型还会经 `appendMontageCatalogEnv` 注入：

- `VIDEO_CONSOLE_EMBEDDING_API_KEY`
- `VIDEO_CONSOLE_EMBEDDING_BASE_URL`
- `VIDEO_CONSOLE_EMBEDDING_MODEL`

`NewHTTPEmbedder` 在 URL 或模型为空时直接返回未配置，即使 Key 在。

**改完设置必须重启控制台。** `settings.Runtime()` 用进程内 active 快照；`restart_required=true` 时 GET 能看到新值，但正在跑的混剪子进程仍用旧快照。未重启时 manifest 里不会出现 `embedding_*`，计划里也没有 `embedding_pool`。

建库向量和混剪查询必须同一模型、同一维度。混用会让余弦无意义。不要把 Key 写进仓库或文档。

## 3. 标签为什么不够，必须扫全库

库标签几乎全是英文财经：`cityscape`、`coins`、`financial_data`、office、K 线。口播是中文「买房 / 法拍 / 月供」。

`LocalIntentAnalyzer` 用 `spokenVisualBridge` 把中文桥到这些英文标签。桥不到的镜头，标签召回永远进不了 80 条小池。

因此 `rankLibrary` 在有 embedder 时：

1. `RecallReadyShots(2000)` 拉全部就绪镜（含已落库的 embedding blob，**不再**为每条素材调 embedding API）
2. 每段口播用 `visual_query` 或「原文 + 视觉概念」向同一模型要一条查询向量
3. 余弦排序后并入 top 32 邻居，再和标签一起打分（`weightSemantic=0.40`）

向量只影响召回和分数，不会把 match level 改成 `direct`（避免测试夹具向量把所有镜头抬成直接命中）。

## 4. 对话模型扮演什么角色

默认**不用**对话模型做整段意图分析（慢，还曾带 remix `reasoning_effort`）。分析走本地词表。

对话模型只做 `ShotSelector`：每段看已经排好的最多 8 个候选，点一个 `shot_id`。提示词写明库里几乎没有真住宅/法拍，住房对城市天际线，月供对硬币/计算器/钱包。

点选失败必须回落向量/标签排序，任务继续。

## 5. 库本身的硬限制

向量只能在**已经入库的画面**里找语义最近。2026-08-18 财经 B-roll 库（约 303 源 / 569 镜）实测：

| 标签/摘要 | 数量 |
|---|---|
| `cityscape` | 17 |
| `coins` | 45 |
| `calculator` | 22 |
| `house` / `apartment` | 0 |
| 摘要含住宅/法拍/楼盘 | 0 |

所以「要不要买房」最多对到城市天际线、窗边办公、硬币、存钱罐、钱包、K 线。要真法拍/小区/售楼，必须先往 `originals/broll` 补片再「开始建库」。不要改 `catalog-builder.config.json` 的 `media_root` 去指到别的盘。

## 6. 日产画面约定

- 素材走 `originals/broll`（风景线有 catalog 时按口播用财经 B-roll，不再滤成只剩风景）
- 播放速度 **1.5×**（`v2PlaybackSpeed`）
- 口播字幕轨关闭，无窗内白字片头；板上标题/副标题仍在
- 「重做混剪」再发一条 `montage.execute`，不删旧草稿

## 7. 验收

1. 设置里 embedding 三项已配置，保存后若提示 `restart_required` 则重启。
2. 重跑混剪，打开 `output/production_plan.json`：
   - 有 `embedding_pool: scanned N`，N 接近就绪镜数
   - 没有 `embedding_disabled`
   - `embedding_query_empty` 不应成片出现（出现说明查询向量 API 失败或口播为空）
3. `task_manifest.json` 的 `non_secret_settings` 含 `embedding_base_url` / `embedding_model`（不要期望看到 Key）。
4. 开头不应被无关手部/签字笔占满；住房段应更多 cityscape；月供段 coins/calculator/wallet。
5. 镜头路径在 `originals/broll`，速度 1.5×。

```powershell
go test ./internal/agentruntime/montageplan ./internal/agentruntime/montagescript ./internal/mediacatalog -count=1
```

关键单测：`TestRankLibraryPullsEmbeddingNeighborsOutsideTagPool`（标签池只有 money 时，向量应拉到 cityscape）。
