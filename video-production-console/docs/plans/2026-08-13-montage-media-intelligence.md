# 素材智能混剪 v2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把当前“固定风景轮询 + 全片固定文字”的混剪，升级为能混用本地电影镜头、普通 B-roll、图片/图表并按文案做大体匹配的可编辑剪映草稿，同时只显示少量重点字幕并增加克制、可验证的动效与音效。

**Architecture:** 新增独立的素材智能层：本地建立 typed media catalog，电影只在本机用 FFmpeg 切镜并抽取低清关键帧，云端视觉模型只看关键帧并生成标签/摘要/向量；AI 负责召回候选，确定性规划器负责配额、去重、节奏、降级和最终时间线。控制台输出 `production_plan` v2，`jianying-montage-draft` 先完成 v1/v2 双读和草稿 QC 后，控制台才切换到 v2，避免兼容性中断。

**Tech Stack:** Go 1.25、SQLite (`modernc.org/sqlite`)、FFmpeg/ffprobe、OpenAI-compatible multimodal/embedding API、React 19、TypeScript 6、Vitest 4、Playwright、Python、pyJianYingDraft。

---

## 0. 已确认的产品决策

- 字幕采用“重点字幕”：只覆盖约 15%–25% 的旁白时长，选择重点句、数字、反转、逻辑转折和结论；不得把现有 SRT 整轨导入，也不得创建自动字幕/文稿匹配轨。
- 片头标题只显示 3–5 秒；后续只在章节切换时显示短章节标签，不再用标题和副标题贯穿全片。
- 目标时长配额是 B-roll 35%–45%、电影片段 25%–35%、图片/图表 20%–30%。配额按时间线时长计算，不按素材条数计算。
- 电影由用户放进本地素材库；系统负责切镜、抽帧、分析、索引和选段。只上传每个镜头 2–3 张低清关键帧，不上传完整电影或原音轨。
- 匹配降级阶梯固定为：`direct` 直接主题 → `metaphor` 主题隐喻 → `emotion` 情绪对应 → `neutral` 中性过渡。
- 风格为克制的财经叙事型：每 20–30 秒最多一个明显效果，不能为“热闹”堆转场、贴纸或音效。
- 用户自行管理电影文件的使用权。本项目不实现盗版下载、DRM 绕过、付费流媒体抓取或平台限制规避。

## 1. Evidence → Finding → Path

| Evidence | Finding | Implementation path |
|---|---|---|
| `internal/agentruntime/montageplan/plan.go:372-413` 在存在风景池时把其他类别全部丢弃 | “一直是风景”是确定性选择器造成的，不是模型能力不足 | Task 1–2：typed catalog + 三类时长配额选择器 |
| `internal/agentruntime/montageplan/plan.go:501-505` 的语义理由是硬编码字符串 | 当前没有真实的文案—镜头语义匹配 | Task 8–9：关键帧分析、文案意图、四级召回与确定性重排 |
| `internal/agentruntime/montageplan/plan.go:507-528` 给所有镜头固定 `scale=1.4`、`opacity=0.5`、同一叠化 | 即便素材丰富，表现手法也会单调 | Task 3–5：v2 shot motion/effect policy + 真机白名单 |
| `internal/agentruntime/montageplan/plan.go:189-203` 创建全片固定标题/副标题且 `caption_tracks="forbidden"` | 用户看到的“像自动字幕”实际是贯穿全片的装饰文字；重点字幕尚不存在 | Task 3–4：片头标题/章节标签/重点字幕分轨，并明确禁止全量字幕 |
| `jianying-montage-draft/scripts/run_montage_job.py:862-877` 只建基础文字；`:968-984` 已注入 verified 叠化 | skill 已能生成文字和真实叠化，但没有封装重点字幕、关键帧、滤镜等 | Task 4–5：先扩 skill 并做 QC，再让控制台发 v2 |
| pyJianYingDraft 的 `TextSegment.add_animation`、`VisualSegment.add_keyframe`、`VideoSegment.add_filter/add_effect/add_mask` 可用 | 底层库能力足够；风险在剪映版本/资源 ID，而非 API 是否存在 | Task 5：通过真机草稿形成小而稳定的 verified allowlist |
| `internal/imageproject/service.go:28-151` 与 `client.go:43-72` 已有文案切分、提示词构建、Generator 接口 | 图文模式可为混剪生成财经配图，但其项目表/ZIP API 不是 montage 资产契约 | Task 11：只复用服务/客户端，通过适配器导入统一素材目录 |

## 2. 目标边界

### 2.1 本期必须交付

1. 本地电影/B-roll/图片三类素材能建库、查询、查看状态并被混剪使用。
2. 一个电影文件被稳定切成 shot，保存源文件哈希、in/out、关键帧、标签、摘要和权利元数据。
3. 文案被拆成带时间的叙事意图段，镜头选择能解释命中层级和分数；无强匹配时安全降级。
4. 最终计划满足三类素材时长配额、单源复用和相邻去重约束。
5. 图片具有 Ken Burns 关键帧；电影/B-roll 不再统一 1.4 倍缩放和 0.5 透明度。
6. 草稿只有少量重点字幕；标题只在片头 3–5 秒；所有文字、关键帧、滤镜、转场、BGM、SFX 都可在剪映里继续编辑。
7. 每个生产计划与草稿均有机器可读 QC：素材配额、字幕覆盖率、明显效果密度、来源追溯、转场/音频资源存在性。

### 2.2 明确不做

- 不让大模型直接输出最终完整时间线；它只产出结构化标签、摘要、意图和候选分数。
- 不上传完整电影、电影音轨或高分辨率连续帧。
- 不做实时 NLE 预览器；网页只展示建库进度、素材摘要和计划/QC 摘要。
- 不承诺任意电影片段的公开发布权，也不提供侵权规避建议。
- 不在 P0/P1 接入所有图库；先交付本地库与稳定的图片生成适配器。

## 3. 总体数据流

```mermaid
flowchart LR
    A["本地电影 / B-roll / 图片"] --> B["FFmpeg 探测与电影切镜"]
    B --> C["2–3 张低清关键帧"]
    C --> D["视觉标签 / 摘要 / embedding"]
    D --> E["独立 catalog.db"]
    F["文案 + SRT 时间"] --> G["叙事意图与重点句"]
    G --> H["AI 候选召回"]
    E --> H
    H --> I["确定性配额 / 去重 / 节奏规划"]
    I --> J["production_plan v2"]
    J --> K["jianying-montage-draft v1/v2 双读"]
    K --> L["可编辑剪映草稿 + QC"]
```

权威边界：

- `media_root/catalog.db` 是素材智能元数据权威库。
- 原始/衍生媒体文件均在 `media_root` 内；数据库只存相对路径，运行时 canonicalize 后校验不得逃逸根目录。
- `media_index.json` 在迁移期仍可供 v1 读取；它是从 catalog 导出的兼容视图，不再是 v2 权威库。
- 每个任务的 `production_plan.json` 是该次选择结果的冻结快照；后续 catalog 更新不能改变已批准计划。

## 4. 素材目录、catalog 与权利元数据

### 4.1 建议目录

```text
media_root/
├── catalog.db
├── originals/
│   ├── movies/
│   ├── broll/
│   └── images/
├── derived/
│   ├── shots/<source_sha256>/<shot_id>.mp4
│   ├── keyframes/<source_sha256>/<shot_id>/{01,02,03}.jpg
│   └── generated/<provider>/<asset_id>.<ext>
└── exports/
    └── media_index.json
```

第一版不要求把电影 shot 物理切成独立 MP4；只要 `source_relative_path + source_in_ms + source_out_ms` 可直接被剪映引用即可。`derived/shots` 只用于将来代理文件/转码，不是 P1 硬依赖。

### 4.2 `catalog.db` 最小表

| Table | Required columns |
|---|---|
| `media_sources` | `id`, `kind(movie/broll/image/generated_image)`, `relative_path`, `sha256`, `size_bytes`, `mime_type`, `width`, `height`, `duration_ms`, `fps`, `status`, `error_code`, `created_at`, `updated_at` |
| `media_shots` | `id`, `source_id`, `ordinal`, `source_in_ms`, `source_out_ms`, `duration_ms`, `analysis_status`, `summary`, `mood`, `setting`, `people_count`, `motion_level`, `has_text`, `embedding_model`, `embedding_blob`, `analysis_version` |
| `media_keyframes` | `id`, `shot_id`, `ordinal`, `relative_path`, `at_ms`, `width`, `height`, `sha256` |
| `media_tags` | `shot_id`, `namespace`, `value`, `confidence`；唯一键 `(shot_id, namespace, value)` |
| `media_rights` | `source_id`, `source_url`, `creator`, `license_code`, `license_url`, `attribution`, `retrieved_at`, `rights_notes` |
| `media_jobs` | `id`, `source_id`, `phase`, `status`, `completed_units`, `total_units`, `error_code`, `error_message`, `started_at`, `finished_at` |

数据库约束：`media_sources.sha256` 唯一；shot 时间不重叠且 `0 <= in < out <= source.duration`；所有向量绑定模型名和分析版本；重跑同版本必须幂等。

### 4.3 推荐素材渠道

| Priority | Channel | Best use | Automation and rights rule |
|---|---|---|---|
| A | 用户本地电影/纪录片/已购授权素材 | 人物、办公室、冲突、时代感、情绪隐喻 | 系统只做本地分析；用户填写 `rights_notes` |
| A | Pexels Videos / Photos | 城市、家庭、职场、消费、抽象 B-roll | 有 API；保留素材 URL/作者/检索时间，不把“可下载”当成任意用途许可 |
| A | Pixabay | 视频、图片、轻量插画 | 有 API；逐条保留来源与许可快照 |
| B | NASA Image and Video Library、美国政府明确公有领域站点 | 航天、科技、宏观叙事 | 逐条核对机构、商标、人物与第三方内容说明 |
| B | Unsplash | 高质量静态图 | 遵守 API hotlink/下载触发等规则；不把图片当电影替代品连续铺满 |
| C | Wikimedia Commons / Openverse | 历史人物、机构、档案图片 | 聚合结果必须逐条检查 CC BY/SA/公版条件并自动生成署名 |
| C | Internet Archive / Prelinger Archives | 历史影像、老工业/消费场景 | 权利状态差异大，只导入明确公版或明确许可条目 |

所有外部导入至少保存：`source_url`、`creator`、`license_code`、`license_url`、`attribution`、`retrieved_at`、`rights_notes`。CC BY/SA 素材进入计划时，QC 报告必须列出署名文本；许可未知的素材默认只允许本地草稿，不标为“可发布”。

## 5. 匹配、评分与确定性规划

### 5.1 文案意图结构

每个时间段输出 `NarrativeIntent`：

```json
{
  "segment_id": "seg-004",
  "start_ms": 31500,
  "end_ms": 38200,
  "text": "真正危险的不是价格涨了一次，而是家庭现金流越来越薄。",
  "entities": ["家庭", "现金流"],
  "topics": ["家庭财务", "风险"],
  "mood": "warning",
  "visual_concepts": ["账本", "空钱包", "深夜家庭"],
  "metaphors": ["水位下降", "绳索绷紧"],
  "importance": 0.91,
  "caption_kind": "turning_point"
}
```

结构化输出须用严格 JSON 校验；模型失败、超时或格式错误时，使用本地规则抽取实体、数字、转折词和情绪，不阻塞整个任务。

### 5.2 四级召回

1. `direct`：实体/主题/地点/动作直接重合，例如“银行”→银行柜台或取款。
2. `metaphor`：抽象财经概念的可读隐喻，例如“流动性收紧”→水位下降、门逐渐关闭。
3. `emotion`：语义不直接相同但情绪一致，例如“焦虑”→深夜独坐、空办公室。
4. `neutral`：城市航拍、交通、自然细节等中性过渡，只用于补足节奏和配额。

### 5.3 候选评分

所有分数归一到 `[0,1]`：

```text
base = 0.40 * semantic_similarity
     + 0.18 * topic_overlap
     + 0.12 * entity_overlap
     + 0.10 * mood_match
     + 0.08 * motion_fit
     + 0.07 * composition_quality
     + 0.05 * recency_or_underuse

penalty = 0.35 * same_source_nearby
        + 0.25 * repeated_shot
        + 0.20 * visible_text_risk
        + 0.20 * quota_pressure

score = clamp(base - penalty, 0, 1)
```

建议阈值：`direct >= 0.72`，否则尝试 `metaphor >= 0.62`，再尝试 `emotion >= 0.54`，最后从 `neutral` 中按未使用、构图和配额选取。模型分数只做候选召回，最终选择必须由同输入同结果的 deterministic planner 完成。

### 5.4 时间线硬约束

- B-roll 35%–45%，movie 25%–35%，image/chart 20%–30%；允许素材不足时按 `movie → broll → image` 的可配置替代矩阵降级，但 QC 必须报告偏差和原因。
- 同一个 `source_id` 最多使用 2 次，不得相邻；两次之间至少隔 5 个视觉片段。
- 同一个 `shot_id` 不得重复。
- 前 30 秒片段时长 4–7 秒；后续电影 3–6 秒、B-roll 5–9 秒、图片 4–7 秒。最后一个片段可在不越源时长的前提下吸收尾差。
- 相邻片段优先不同 `media_kind`、不同 setting 和不同 motion_level。
- 明显效果至少间隔 20 秒，任意 30 秒窗口最多 1 个；叠化属于基础剪辑，不计入“明显效果”。

## 6. Production plan v2 契约

v2 保留 v1 的 `concurrency`、`inputs`、`audio` 和可追溯路径字段，新增明确的媒体类型、匹配证据、运动、字幕与效果策略：

```json
{
  "plan_version": "2.0",
  "media_mix_policy": {
    "basis": "timeline_duration",
    "targets": {
      "broll": {"min": 0.35, "max": 0.45},
      "movie": {"min": 0.25, "max": 0.35},
      "image": {"min": 0.20, "max": 0.30}
    },
    "max_source_uses": 2,
    "min_segments_between_reuse": 5
  },
  "timeline": [{
    "shot_no": 1,
    "start_s": 0,
    "end_s": 4.2,
    "media_kind": "movie",
    "source_id": "src-...",
    "shot_id": "shot-...",
    "source_path": "C:/.../movie.mp4",
    "source_in_s": 91.2,
    "source_out_s": 95.4,
    "match": {"level": "metaphor", "score": 0.76, "intent_id": "seg-001", "reason": "关门隐喻机会收紧"},
    "motion": {"preset": "steady", "scale_from": 1.02, "scale_to": 1.04, "x_from": 0, "x_to": 0, "y_from": 0, "y_to": 0},
    "opacity": 1.0,
    "source_audio_muted": true,
    "look": "finance_neutral",
    "transition": {"preset": "cross_dissolve", "duration_s": 0.466666},
    "emphasis_effect": null
  }],
  "graphics": {
    "opening_title": {"text": "家庭现金流", "start_s": 0, "end_s": 4.0, "style": "opening_title_v1"},
    "chapter_labels": [{"text": "风险开始", "start_s": 42.0, "end_s": 44.5, "style": "chapter_v1"}],
    "captions": {
      "mode": "highlights_only",
      "target_coverage_min": 0.15,
      "target_coverage_max": 0.25,
      "items": [{"text": "现金流越来越薄", "start_s": 31.5, "end_s": 34.8, "kind": "turning_point", "style": "highlight_v1", "intro": "fade_up"}]
    }
  },
  "qc_expectations": {"max_obvious_effects_per_30s": 1, "full_caption_track_forbidden": true}
}
```

兼容规则：

- skill 先实现 `plan_version in {"1.0","2.0"}`。v1 的校验与构建行为保持原样，现有 fixture 必须继续通过。
- v2 只接受 typed schema；版本未知直接失败，不静默当 v1。
- 控制台在检测到当前 skill snapshot 明确声明 v2 capability 前仍输出 v1。不能只凭文件存在或版本字符串猜测支持。
- 已经生成的 v1 任务继续用冻结的 skill snapshot 登记；不能因当前 skill 升级而改变历史任务内容。

## 7. 草稿表现白名单

### 7.1 文字

- `opening_title_v1`：0–4 秒，大字、轻微阴影/描边，0.3–0.5 秒淡入或向上淡入；不与重点字幕重叠。
- `chapter_v1`：2–3 秒，小标签，可带半透明底；每章最多一次。
- `highlight_v1`：单条 2–4 秒、最多两行；数字/结论可用强调色。只从重点句列表生成，绝不调用全量 SRT 导入。
- 字幕覆盖率按所有重点字幕时间区间的并集 / narration duration 计算，要求 15%–25%；同一时间最多一条重点字幕。

### 7.2 视觉运动与调色

- 图片必须使用 seek-safe Ken Burns：`scale 1.00→1.06` 或 `1.06→1.00`，平移幅度不超过画面宽/高的 3%；首尾关键帧都落在片段内部。
- 电影默认 `scale 1.00–1.05`、`opacity=1.0`；只有叠加画面才允许降低透明度。
- B-roll 可用轻推/轻拉/稳定三种预设；同一预设不得连续超过 2 次。
- `finance_neutral` 调色只允许轻度统一：饱和、对比、亮度调整不超过真机验证值；禁止所有素材统一强滤镜。

### 7.3 转场、明显效果、BGM 与 SFX

- 基础转场保留已验证叠化；后续可以加入硬切和一个经真机验证的轻闪白，但不得直接使用未验证 effect/resource ID。
- 明显效果候选仅限：数字强调、章节轻扫光、单次风险震动。每 20–30 秒最多一次，并由 `emphasis_effect` 明确声明。
- BGM 保留 verified EXTA$Y+ 作为 v1 fallback；v2 policy 允许未来多首白名单，但每首都要记录 cache key、ID、音量、loop 和真机结果。
- SFX 仅在片头、数字、重大转折、结论使用；五分钟视频建议 3–5 次、间隔至少 12 秒，仍使用 verified cache mapping，缺失时返回 PLAN 而不是静默省略。

## 8. 分阶段交付与停止线

| Phase | Deliverable | Stop line |
|---|---|---|
| P0：基线与可见改进 | 完成图文模式；修复只选风景；typed index 能同时读 video/image/movie；计划仍可输出 v1 | 未升级 skill 时绝不输出 v2 |
| P1：丰富草稿 | skill v1/v2 双读、重点字幕、片头标题、图片关键帧、verified 效果白名单；控制台可产 v2 | 无真机 allowlist 的效果不进生产 |
| P2：电影建库 | 独立 catalog、FFmpeg 切镜/抽帧、视觉分析、embedding、四级匹配 | 不上传完整电影/音轨 |
| P3：产品化 | 建库 API/UI、图片生成/外部素材适配、完整 QC 与文档 | 权利元数据不完整时明确提示，不宣称可发布 |

## 9. 文件责任图

### 9.1 控制台仓库

| File | Responsibility |
|---|---|
| `internal/agentruntime/montageplan/media.go` | v2 typed media item、kind、shot 范围与索引解码 |
| `internal/agentruntime/montageplan/quota.go` | 三类时长配额、复用/相邻去重与 deterministic selection |
| `internal/agentruntime/montageplan/intent.go` | SRT/文案时间段、重点句规则 fallback、`NarrativeIntent` |
| `internal/agentruntime/montageplan/match.go` | 四级候选召回、评分、解释和稳定排序 |
| `internal/agentruntime/montageplan/plan_v2.go` | typed `ProductionPlanV2`、timeline/graphics/QC 构建与 JSON 输出 |
| `internal/mediacatalog/models.go` | catalog domain types 与校验 |
| `internal/mediacatalog/repository.go` | 独立 `catalog.db` schema、transaction、query/upsert |
| `internal/mediacatalog/indexer.go` | 发现源文件、哈希、幂等建库状态机 |
| `internal/mediacatalog/ffmpeg.go` | 受控执行 ffmpeg/ffprobe、切镜解析、关键帧抽取 |
| `internal/mediacatalog/analyzer.go` | multimodal JSON 分析、embedding client 接口及限流 |
| `internal/mediacatalog/importer.go` | 本地/生成图片/外部下载统一导入与权利元数据 |
| `internal/httpapi/media_catalog.go` | 建库、状态、列表、重试 API |
| `internal/app/app.go` | 挂载 `/api/media-catalog` |
| `internal/domain/settings.go`、`internal/settings/service.go` | catalog/FFmpeg/视觉与 embedding 的公开设置和加密密钥 |
| `internal/codex/manifest.go`、`schemas/task-manifest.schema.json` | 冻结 `catalog_path` 与 `montage_plan_version`，不传密钥 |
| `internal/httpapi/task_manifest.go` | 只在 skill capability 已确认时冻结 v2，并运行对应 preflight |
| `internal/agentruntime/montagescript/run.go` | 向 planner 注入 catalog/analyzer 依赖；保持 v1 orchestration |
| `web/src/media-library/MediaLibraryPanel.tsx` | 素材目录、建库进度、错误和权利提示 |
| `web/src/project-workbench/ProjectWorkbench.tsx` | 混剪任务前的素材库摘要与 plan QC 摘要 |
| `web/src/project-workbench/types.ts` | 前端 media catalog / montage QC DTO |

### 9.2 `jianying-montage-draft` skill

| File | Responsibility |
|---|---|
| `assets/production_plan.v2.template.json` | v2 示例契约 |
| `assets/montage-style-policy.v2.json` | 只存已真机验证的逻辑 style/effect/cache mapping |
| `scripts/production_plan_v2.py` | v2 typed parse/validate；v1 不经过这里 |
| `scripts/run_montage_job.py` | 版本路由、v2 build、图片/视频关键帧、重点字幕和 report |
| `scripts/validate_montage_draft.py` | 草稿轨道、字幕覆盖、关键帧、效果密度与 audio/transition QC |
| `tests/test_production_plan_v2.py` | v2 schema/约束单测 |
| `tests/test_run_montage_job.py` | v1 回归 + v2 build/registration orchestration |
| `tests/test_validate_montage_draft.py` | 重点字幕/关键帧/效果 QC |
| `references/console-contract.md`、`SKILL.md` | 双版本契约、caption guardrail 和 capability 声明 |

## 10. 实施任务

### Task 0：冻结图文模式并建立可信基线

**Files:**

- Verify only: `docs/plans/2026-08-13-image-mode.md`
- Verify only: `internal/imageproject/`, `internal/httpapi/imageprojects*.go`, `internal/store/imageprojects*.go`
- Verify only: `web/src/image-mode/`, `web/e2e/image-mode.spec.ts`
- Verify only: `scripts/verify-baseline.ps1`, `.github/workflows/baseline.yml`

- [ ] **Step 1: 保护当前工作树**

运行 `git status --short` 并把结果保存到实现任务记录。不得执行 `reset`、`restore`、`stash` 或删除现有图文模式文件。先把图文模式按其自己的计划完成、测试并提交；本计划从该提交创建独立 worktree/分支。

- [ ] **Step 2: 安装与 CI 相同的前端依赖**

Run: `npm ci --prefix web`

Expected: exit 0；Node 22，lockfile 未被修改。

- [ ] **Step 3: 验证图文模式基线**

Run:

```powershell
go test ./internal/imageproject ./internal/store ./internal/httpapi ./internal/app
npm --prefix web run typecheck
npm --prefix web run lint
npm --prefix web run test
npx --prefix web playwright install chromium
npm --prefix web run test:e2e
npm --prefix web run build:verify
```

Expected: 全部 exit 0。若现有失败，先在图文模式分支修复或记录明确的失败测试名；不能把失败带入本计划后再归因给 montage v2。

- [ ] **Step 4: 验证系统依赖并形成可操作错误**

Run: `ffmpeg -version` and `ffprobe -version`

Expected: 两者 exit 0。当前开发环境实测两者均不在 `PATH`；实现前必须安装 FFmpeg，或在 Task 6 增加受校验的绝对路径设置。缺失时 UI/API 返回 `ffmpeg_not_configured`，不能等到电影任务中途才失败。

- [ ] **Step 5: 建立分支基线**

```powershell
git add <仅图文模式计划内的文件>
git commit -m "feat: complete image generation mode"
git worktree add ..\video-production-console-montage-v2 -b codex/montage-media-intelligence
```

Expected: 新 worktree 干净；原工作树的用户改动被保留。本计划后续只在新 worktree 实施。

### Task 1：把旧 JSON 索引升级为 typed media index

**Files:**

- Create: `internal/agentruntime/montageplan/media.go`
- Modify: `internal/agentruntime/montageplan/mediascan.go`
- Modify: `internal/agentruntime/montageplan/plan.go`
- Test: `internal/agentruntime/montageplan/mediascan_test.go`
- Test: `internal/agentruntime/montageplan/plan_test.go`

- [ ] **Step 1: 写兼容解码失败测试**

增加 `TestReadMediaScanDecodesLegacyAndTypedRows`，fixture 同时包含：

```json
[
  {"id":"legacy","category":"Nature_Landscape","relative_path":"broll/a.mp4","duration_seconds":20},
  {"id":"movie-1","kind":"movie","category":"office","relative_path":"movies/m.mp4","duration_seconds":300,"source_in_seconds":40,"source_out_seconds":46,"shot_id":"shot-1"},
  {"id":"image-1","kind":"image","category":"ledger","relative_path":"images/ledger.png","duration_seconds":0}
]
```

断言 legacy 映射为 `kind=broll`；movie 保留 shot in/out；image 不因 `<10s` 被过滤；未知 kind、绝对 `relative_path`、`..` 逃逸和非普通文件被拒绝。

- [ ] **Step 2: 运行定向测试确认失败**

Run: `go test ./internal/agentruntime/montageplan -run 'TestReadMediaScanDecodesLegacyAndTypedRows|TestMediaScan' -count=1`

Expected: 新测试 FAIL，指出 `kind`/image/shot fields 尚不存在；既有 cache tests 仍可编译。

- [ ] **Step 3: 实现最小 typed model**

在 `media.go` 定义：

```go
type mediaKind string

const (
    mediaKindBroll mediaKind = "broll"
    mediaKindMovie mediaKind = "movie"
    mediaKindImage mediaKind = "image"
)

type mediaItem struct {
    ID              string    `json:"id"`
    Kind            mediaKind `json:"kind,omitempty"`
    Category        string    `json:"category"`
    RelativePath    string    `json:"relative_path"`
    DurationSeconds float64   `json:"duration_seconds"`
    SourceInSeconds float64   `json:"source_in_seconds,omitempty"`
    SourceOutSeconds float64  `json:"source_out_seconds,omitempty"`
    ShotID          string    `json:"shot_id,omitempty"`
    Tags            []string  `json:"tags,omitempty"`
    Summary         string    `json:"summary,omitempty"`
    Mood            string    `json:"mood,omitempty"`
    AbsPath         string    `json:"-"`
}
```

把 `plan.go` 中旧 `mediaItem` 移除。解码后默认空 kind 为 broll；image 只要求图片文件存在；video/movie 要求可用 shot/source 时长。用 `filepath.Rel` + canonical root 校验阻止路径逃逸。

- [ ] **Step 4: 运行格式化与测试**

Run: `gofmt -w internal/agentruntime/montageplan/media.go internal/agentruntime/montageplan/mediascan.go internal/agentruntime/montageplan/plan.go internal/agentruntime/montageplan/mediascan_test.go internal/agentruntime/montageplan/plan_test.go`

Run: `go test ./internal/agentruntime/montageplan -count=1`

Expected: PASS。

- [ ] **Step 5: Commit**

```powershell
git add internal/agentruntime/montageplan
git commit -m "feat: support typed montage media index"
```

### Task 2：实现三类时长配额与去重选择器

**Files:**

- Create: `internal/agentruntime/montageplan/quota.go`
- Create: `internal/agentruntime/montageplan/quota_test.go`
- Modify: `internal/agentruntime/montageplan/diversify.go`
- Modify: `internal/agentruntime/montageplan/plan.go`
- Test: `internal/agentruntime/montageplan/plan_test.go`

- [ ] **Step 1: 写硬约束测试**

新增以下测试：

- `TestSelectTimelineMeetsDurationQuotas`：300 秒任务结果 B-roll 35%–45%、movie 25%–35%、image 20%–30%。
- `TestSelectTimelineNeverRepeatsShotAndSpacesSourceReuse`：shot 不重复，同 source 最多 2 次且间隔至少 5 段。
- `TestSelectTimelineIsStableForSameSeed`：同 seed 字节级相同，不同 seed 允许顺序变化但约束不变。
- `TestSelectTimelineReportsQuotaFallback`：电影不足时返回 typed warning，仍覆盖完整 narration。

- [ ] **Step 2: 运行测试确认当前 scenic-only 逻辑失败**

Run: `go test ./internal/agentruntime/montageplan -run 'TestSelectTimeline|TestSampleMedia' -count=1`

Expected: FAIL；当前 `sampleMedia` 在 scenic pool 非空时丢弃 movie/image。

- [ ] **Step 3: 实现 deterministic quota planner**

定义并实现：

```go
type quotaRange struct{ Min, Max float64 }
type mixPolicy struct {
    Targets map[mediaKind]quotaRange
    MaxSourceUses int
    MinSegmentsBetweenReuse int
}
type quotaWarning struct{ Kind, Code string; Wanted, Actual float64 }

func selectTimeline(candidates []rankedCandidate, duration float64, seed string, policy mixPolicy) ([]plannedMedia, []quotaWarning, error)
```

算法顺序固定：先生成目标片段槽位 → 按最大缺口选择 kind → 在该 kind 内按分数/稳定 hash 选择 → 应用 shot/source/相邻约束 → 使用替代矩阵降级 → 最后一个片段吸收尾差。不要使用 map iteration 决定顺序。

- [ ] **Step 4: 替换 scenic-only 池**

删除 `sampleMedia` 中“preferred 非空就只用 preferred”的分支。P0 在还没有 AI 分数时使用 category/underuse/stable hash 形成 `rankedCandidate`，使三类素材立即能混用。

- [ ] **Step 5: 运行测试和 race detector**

Run:

```powershell
gofmt -w internal/agentruntime/montageplan
go test ./internal/agentruntime/montageplan -count=1
go test -race ./internal/agentruntime/montageplan -count=1
```

Expected: PASS；Windows 若 race toolchain 不可用，记录工具链错误并让 CI 的普通测试继续作为门禁。

- [ ] **Step 6: Commit**

```powershell
git add internal/agentruntime/montageplan
git commit -m "feat: plan montage media by duration quotas"
```

