# 素材智能混剪 v2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> **状态（2026-08-13）：未实施，任何 Task 均未动工。** 产品语境与交付顺序见 [制作方式路线图](2026-08-13-production-methods-roadmap.md)：P0–P1 之后先交付"图片视频"（P1.5，路线图方式四），电影建库（P2，方式三）在其后。图文模式基线已提交（`c7382e5`），Task 0 的等待前置已满足。本次修订要点：Task 11 复用目标改为 `SuggestSegments`/`SuggestPrompts`；`media_mix_policy` 支持任务级预设；§8 新增 P1.5 里程碑；Task 7 增加切镜升级路径；§4.2 增加图片 embedding 落表说明。

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
| `internal/imageproject/planner.go` 已有 AI 分段与提示词（`SuggestSegments` `:132`、`SuggestPrompts` `:144`），`client.go:43-72` 已有 Generator 接口 | 图文模式可为混剪生成财经配图，但其项目表/ZIP API 不是 montage 资产契约；`SplitScript`/`BuildPrompt` 已退役、仅测试引用 | Task 11：只复用 planner 与生图客户端，通过适配器导入统一素材目录 |

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
│   ├── shots/{source_sha256}/{shot_id}.mp4
│   ├── keyframes/{source_sha256}/{shot_id}/{01,02,03}.jpg
│   └── generated/{provider}/{asset_id}.{ext}
└── exports/
    └── media_index.json
```

第一版不要求把电影 shot 物理切成独立 MP4；只要 `source_relative_path + source_in_ms + source_out_ms` 可直接被剪映引用即可。`derived/shots` 只用于将来代理文件/转码，不是 P1 硬依赖。

### 4.2 `catalog.db` 最小表

| Table | Required columns |
|---|---|
| `media_sources` | `id`, `kind(movie/broll/image)`, `subtype(video/photo/chart/illustration/generated)`, `origin(local/generated/pexels/pixabay)`, `relative_path`, `sha256`, `size_bytes`, `mime_type`, `width`, `height`, `duration_ms`, `fps`, `status`, `error_code`, `created_at`, `updated_at` |
| `media_shots` | `id`, `source_id`, `ordinal`, `source_in_ms`, `source_out_ms`, `duration_ms`, `analysis_status`, `summary`, `mood`, `setting`, `people_count`, `motion_level`, `has_text`, `embedding_model`, `embedding_blob`, `analysis_version` |
| `media_keyframes` | `id`, `shot_id`, `ordinal`, `relative_path`, `at_ms`, `width`, `height`, `sha256` |
| `media_tags` | `shot_id`, `namespace`, `value`, `confidence`；唯一键 `(shot_id, namespace, value)` |
| `media_rights` | `source_id`, `source_url`, `creator`, `license_code`, `license_url`, `attribution`, `retrieved_at`, `rights_notes` |
| `media_jobs` | `id`, `source_id`, `phase`, `status`, `completed_units`, `total_units`, `error_code`, `error_message`, `started_at`, `finished_at` |

数据库约束：`media_sources.sha256` 唯一；shot 时间不重叠且 `0 <= in < out <= source.duration`；所有向量绑定模型名和分析版本；重跑同版本必须幂等。

图片来源没有镜头概念（上述 `in < out` 约束不适用），其分析结果与 embedding 二选一落表：存 source 级（给 `media_sources` 增加同名分析/向量字段），或为每张图片建一行"整图退化 shot"（放宽该约束）。实施 Task 6 时选定其一并用测试固定，不得让图片游离在向量检索之外——生成图片（路线图方式四）依赖该向量做"先检索后生成"的复用。

planner 对外只保留三种顶层 `mediaKind`：`movie`、`broll`、`image`。`chart` 和 `generated_image` 是 catalog 的图片子类型，通过 `media_sources.subtype`（`photo|chart|illustration|generated`）保留来源差异，但进入配额、时间线和 QC 时统一映射为 `media_kind="image"`；`origin` 继续记录 `local|generated|pexels|pixabay`。这样“图片/图表 20%–30%”只有一个可计算口径，同时不丢失可追溯性。

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

- B-roll 35%–45%，movie 25%–35%，image（含 photo/chart/generated 子类型）20%–30%；允许素材不足时按 `movie → broll → image` 的可配置替代矩阵降级，但 QC 必须报告偏差和原因。
- 配额目标来自任务级预设，预设名写入 `media_mix_policy.preset`，QC 按所选预设校验：`movie_mix`（默认，即上一条的三类区间）；`image_video`（路线图方式四"图片视频"：image 0.85–1.00、broll 0–0.15、movie 0）。未知预设名直接失败，不得静默按默认处理。
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
    "preset": "movie_mix",
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

关闭字幕使用同一契约，不是缺省猜测：

```json
{
  "graphics": {
    "captions": {
      "mode": "off",
      "target_coverage_min": 0.0,
      "target_coverage_max": 0.0,
      "items": []
    }
  }
}
```

`mode="off"` 时校验器要求 `items=[]` 且草稿中不存在 caption/ASR/document-matching 轨；`mode="highlights_only"` 时才应用 15%–25% 覆盖率。

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
| P0：基线与可见改进 | 图文模式已完成（`c7382e5`）；修复只选风景；typed index 能同时读 video/image/movie；计划仍可输出 v1 | 未升级 skill 时绝不输出 v2 |
| P1：丰富草稿 | skill v1/v2 双读、重点字幕、片头标题、图片关键帧、verified 效果白名单；控制台可产 v2 | 无真机 allowlist 的效果不进生产 |
| P1.5：图片视频（路线图方式四） | SRT 意图分段、按段生成提示词与图片（复用 `imageproject` planner/Generator，见 Task 11 生成部分）、`image_video` mix 预设，交付图片为主的可编辑草稿 | 单段生图失败回退 neutral B-roll 并写 QC warning，不阻塞草稿；不引入电影建库依赖 |
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

- Verify only: 图文模式现状见 `docs/ARCHITECTURE.md` 与 `docs/AI-HANDOFF.md` §5（其原始计划文档已按 docs 惯例删除，考古走 git 历史）
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

这是实施前置门，不由本规划擅自提交用户当前改动。先让图文模式任务的负责人按其自身验收完成并提交；仅在 `git status --short` 为空且用户确认该提交属于图文模式后，记录 `$imageModeCommit = git rev-parse HEAD`，再从这个不可变提交创建混剪 worktree。若工作树仍有任何修改，返回 `image_mode_baseline_not_committed` 并停止，绝不自动 `git add/commit/stash/reset/restore`。

2026-08-13 更新：图文模式已完成并提交（`c7382e5` 及此前一串图文提交），该前置条件在当前分支已满足；执行本任务时仍须复核 `git status --short` 为空（计划/路线图文档的改动应先随文档变更提交）。

```powershell
if (git status --porcelain) { throw 'image_mode_baseline_not_committed' }
$imageModeCommit = git rev-parse HEAD
git worktree add ..\video-production-console-montage-v2 -b codex/montage-media-intelligence $imageModeCommit
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
    Subtype         string    `json:"subtype,omitempty"`
    Origin          string    `json:"origin,omitempty"`
    AbsPath         string    `json:"-"`
}
```

把 `plan.go` 中旧 `mediaItem` 移除。解码后默认空 kind 为 broll；`chart`/`generated_image` 输入值先规范化为 `kind=image` 并写入 subtype；image 只要求图片文件存在；video/movie 要求可用 shot/source 时长。用 `filepath.Rel` + canonical root 校验阻止路径逃逸。

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

### Task 3：定义 production plan v2、重点字幕与表现策略

**Files:**

- Create: `internal/agentruntime/montageplan/plan_v2.go`
- Create: `internal/agentruntime/montageplan/intent.go`
- Create: `internal/agentruntime/montageplan/plan_v2_test.go`
- Create: `internal/agentruntime/montageplan/intent_test.go`
- Modify: `internal/agentruntime/montageplan/plan.go`
- Test: `internal/agentruntime/montageplan/plan_test.go`

- [ ] **Step 1: 写 v2 契约和字幕开关失败测试**

新增 `TestBuildV2ProducesTypedPlan`、`TestCaptionModeOffProducesNoCaptionItems`、`TestHighlightCaptionsCoverFifteenToTwentyFivePercent`、`TestHighlightsMergeOverlapsBeforeCoverage`、`TestOpeningTitleEndsWithinFiveSeconds`、`TestObviousEffectsAreAtLeastTwentySecondsApart`。测试必须断言：

```go
type CaptionMode string
const (
    CaptionOff CaptionMode = "off"
    CaptionHighlightsOnly CaptionMode = "highlights_only"
)

type CaptionPolicy struct {
    Mode CaptionMode `json:"mode"`
    TargetCoverageMin float64 `json:"target_coverage_min"`
    TargetCoverageMax float64 `json:"target_coverage_max"`
    Items []CaptionItem `json:"items"`
}
```

`off` 时 `Items` 必须为空；`highlights_only` 时字幕区间并集占旁白时长 0.15–0.25；标题结束时间位于 3–5 秒；字幕、标题和章节标签均为不同 typed 字段；不得出现全量 SRT、ASR 或 document-matching 标记。

- [ ] **Step 2: 运行定向测试并确认失败**

Run: `go test ./internal/agentruntime/montageplan -run 'TestBuildV2|TestCaption|TestHighlight|TestOpeningTitle|TestObviousEffects' -count=1`

Expected: FAIL，原因是当前只有 map 形式 v1、全片标题/副标题和 `caption_tracks="forbidden"`。

- [ ] **Step 3: 实现 typed v2 与重点句选择器**

在 `plan_v2.go` 定义 `ProductionPlanV2`、`TimelineShotV2`、`MatchEvidence`、`MotionPlan`、`GraphicsV2`、`CaptionPolicy`、`CaptionItem`、`QCExpectations`。在 `intent.go` 定义：

```go
type CaptionKind string
const (
    CaptionNumber CaptionKind = "number"
    CaptionTurningPoint CaptionKind = "turning_point"
    CaptionConclusion CaptionKind = "conclusion"
)

type TimedSentence struct { StartMS, EndMS int64; Text string }
func selectHighlightCaptions(sentences []TimedSentence, narrationMS int64, mode CaptionMode) []CaptionItem
func intervalCoverage(items []CaptionItem, narrationMS int64) float64
```

候选权重固定为结论 4、转折 3、含数字 2、普通句 0；按权重、开始时间、规范化文本稳定排序；每条 2–5 秒，重叠项只留高权重项，再按时间排序；达到 15% 后停止，加入下一条会超过 25% 时跳过。候选不足允许低于 15%，但写入 `caption_coverage_below_target` QC warning。`CaptionOff` 直接返回空数组。

- [ ] **Step 4: 用 typed builder 生成 v2，不改变 v1 路径**

保留现有 `Build` 的 v1 行为；新增 `BuildV2(opts Options) error`。v2 将 `visual_scale=1.4/opacity=0.5` 替换为按 `media_kind` 生成的 `MotionPlan`：图片 Ken Burns、电影 `scale 1.00–1.05/opacity 1.0`、B-roll 的 steady/push/pull。明显效果时间间隔至少 20 秒，任意 30 秒窗口最多一个；叠化不计入明显效果。

- [ ] **Step 5: 格式化并运行 montageplan 全包测试**

Run:

```powershell
gofmt -w internal/agentruntime/montageplan
go test ./internal/agentruntime/montageplan -count=1
```

Expected: PASS；相同输入和 job ID 生成字节级稳定的 v2 JSON。

- [ ] **Step 6: Commit**

```powershell
git add internal/agentruntime/montageplan
git commit -m "feat: define montage production plan v2"
```

### Task 4：让 jianying-montage-draft 双读 v1/v2 并构建可编辑草稿

执行本任务前从控制台 `GET /api/skills/jianying-montage-draft` 返回的最新 snapshot 取得 `path`；默认本机地址为 `http://127.0.0.1:2030`，若 `listen_addr` 已更改则替换第一行。下列 `skill-root/...` 表示相对 `$skillRoot` 的路径，不允许硬编码某个用户名目录。

```powershell
$consoleBase = 'http://127.0.0.1:2030'
$skillSnapshot = Invoke-RestMethod "$consoleBase/api/skills/jianying-montage-draft"
$skillRoot = $skillSnapshot.path
if (-not [IO.Path]::IsPathFullyQualified($skillRoot) -or -not (Test-Path -LiteralPath $skillRoot -PathType Container)) { throw 'jianying_skill_root_invalid' }
```

**Files:**

- Create: `skill-root/assets/production_plan.v2.template.json`
- Create: `skill-root/scripts/production_plan_v2.py`
- Create: `skill-root/tests/test_production_plan_v2.py`
- Modify: `skill-root/scripts/run_montage_job.py:564-729,769-1024`
- Modify: `skill-root/scripts/validate_montage_draft.py`
- Modify: `skill-root/tests/test_run_montage_job.py`
- Modify: `skill-root/tests/test_validate_montage_draft.py`
- Modify: `skill-root/references/console-contract.md`
- Modify: `skill-root/SKILL.md`

- [ ] **Step 1: 写版本路由、字幕和图片运动失败测试**

测试覆盖：v1 fixture 输出保持不变；未知 `plan_version` 明确失败；v2 缺 `media_kind/match/motion/qc_expectations` 失败；`captions.mode=off` 不创建文字轨；`highlights_only` 每项创建独立可编辑 `TextSegment`；开场标题仅 3–5 秒；图片片段具有首尾 `uniform_scale` 关键帧；电影/B-roll 不再统一 1.4 倍缩放和 0.5 透明度；字幕区间并集超过 25% 时 QC 失败。

- [ ] **Step 2: 运行测试确认 v2 尚不支持**

Run:

```powershell
Set-Location -LiteralPath $skillRoot
python -m pytest -q tests/test_production_plan_v2.py tests/test_run_montage_job.py tests/test_validate_montage_draft.py
```

Expected: 新 v2 tests FAIL；既有 v1 tests 除已知注册重试测试外仍保持原结果。当前基线实测为 28 passed、`test_registration_impl_updates_and_verifies_root_index` 1 failed；该既有失败必须单独记录，不能算 v2 回归。

- [ ] **Step 3: 实现严格的 v1/v2 路由**

`production_plan_v2.py` 使用 dataclass/显式解析函数校验第 6 节定义的结构和所有时间范围。`run_montage_job.py` 在读取 JSON 后仅按 `plan_version` 分派：`1.0` 走原校验和构建器，`2.0` 走新模块；其他版本返回 `unsupported_plan_version`，禁止静默降级。

- [ ] **Step 4: 用 pyJianYingDraft 原生 API 构建 v2**

在 `DeterministicDraftBackend.build` 的素材段落处按 kind 创建 `VideoMaterial`/`VideoSegment`。图片也由 `VideoMaterial(path)` 自动识别；用 `VisualSegment.add_keyframe(KeyframeProperty.uniform_scale, ...)` 及 position keyframe 实现 Ken Burns。文字动画由 `resolve_text_intro(style_key, policy)` 解析：先从 `montage-style-policy.v2.json` 读取该 style 的 `enum_name`，再要求它同时存在于 policy 的 `verified_text_intro_enum_names` 和 `TextIntro.__members__`，最后用 `TextIntro[enum_name]` 取得枚举对象；任一条件不满足就返回 `unverified_style_resource`。禁止对任意字符串直接 `getattr`，也禁止猜枚举。然后调用 `TextSegment.add_animation(resolved_intro, duration)`。所有时间都相对片段并落在片段内部；源视频音量仍为 0。v1 分支保持现状。

- [ ] **Step 5: 扩充明文 QC**

验证器读取生成的 `project_profile.json` 中 `plan_version`，对 v2 检查：图片关键帧存在且首尾完整；标题不超过 5 秒；caption off 时无 caption track；highlights-only 的区间并集在 15%–25% 或带明确不足 warning；每条字幕对应 plan item；同一时刻最多一条；明显效果密度合规；BGM/SFX/verified 叠化和红线仍按现有规则校验。

- [ ] **Step 6: 运行全部 skill 测试并重新扫描 snapshot**

Run:

```powershell
Set-Location -LiteralPath $skillRoot
python -m pytest -q
Invoke-RestMethod -Method Post "$consoleBase/api/skills/scan"
```

Expected: v1/v2 新增测试 PASS。若既有注册测试仍失败，输出准确测试名和差异，另开修复任务；不得放宽断言。随后从控制台的 skill 管理页/API 执行一次扫描，验证新 `SkillSnapshot.Files` 包含 `scripts/production_plan_v2.py` 且 SHA-256 与磁盘一致。skill 根不是独立 Git 仓库，因此这里不写虚假 commit；控制台代码仍在后续任务正常提交。

### Task 5：真机提取并锁定剪映表现白名单

**Files:**

- Create: `skill-root/assets/montage-style-policy.v2.json`
- Create: `skill-root/scripts/build_montage_style_reference.py`
- Create: `skill-root/tests/test_montage_style_policy.py`
- Modify: `skill-root/scripts/run_montage_job.py`
- Modify: `skill-root/scripts/validate_montage_draft.py`
- Modify: `skill-root/assets/user-verified-draft-structure-v05.json`

- [ ] **Step 1: 写拒绝未知资源的测试**

新增测试断言：未知 `style_key`、文字动画、滤镜、蒙版、场景特效或资源 ID 均返回 `unverified_style_resource`；policy 中缺 cache 映射时返回 PLAN/`awaiting_input`；明显效果在 20 秒内重复或任意 30 秒超过一个时失败。

- [ ] **Step 2: 创建人工真机验证样稿**

用户在剪映里新建一个不含隐私素材的最小草稿，各放一次准备允许的表现：`opening_title_v1` 淡入/上移、`chapter_v1`、`highlight_v1`、图片 Ken Burns、`finance_neutral` 轻调色，以及至多一个“数字强调”、一个“风险震动”。不增加未经需求确认的花字或转场。

前置条件：把该草稿的用户授权明文 QC 副本保存为 `$skillRoot/fixtures/montage-style-v2-qc`。目录不存在、草稿仍加密或结构版本不匹配时，脚本必须返回 `awaiting_input/style_reference_missing`，Task 5 停止且控制台继续只产 v1；不得用空 policy 或猜测值越过此门。

- [ ] **Step 3: 从授权的解密 QC 副本生成脱敏 policy**

Run:

```powershell
$qcDraft = (Resolve-Path '.\fixtures\montage-style-v2-qc').Path
python scripts/build_montage_style_reference.py --draft $qcDraft --output assets/montage-style-policy.v2.json
```

Expected: `fixtures/montage-style-v2-qc` 是 Step 2 保存的用户授权明文 QC 副本；输出只含逻辑 style key、类型、剪映资源/effect/resource ID、参数范围、结构指纹和来源草稿版本；不含原素材路径、文字、工程 ID、设备 ID。未在样稿中真实出现并通过校验的资源不写入 policy。

- [ ] **Step 4: 将 policy 接入执行器和验证器**

执行器只接受 policy 中的逻辑 key；运行时通过 machine profile 解析需要缓存文件的条目。`finance_neutral` 的饱和、对比、亮度强度上限取真机样稿中的已验证范围；如果只验证了文字动画和 Ken Burns，则滤镜/蒙版/明显特效保持禁用，不猜 enum 或资源 ID。

- [ ] **Step 5: 运行测试和一次明文真机 round-trip**

Run: `python -m pytest -q tests/test_montage_style_policy.py tests/test_run_montage_job.py tests/test_validate_montage_draft.py`

Expected: PASS。再用固定短计划执行 `validate-plan -> execute -> validate_montage_draft.py`，人工打开剪映仅做本任务要求的真机验收，确认每种白名单表现可见且可编辑；记录剪映版本、policy SHA-256、草稿路径与截图。失败资源从白名单删除，不能用“可能兼容”放行。

### Task 6：建立独立 catalog.db、设置与安全边界

**Files:**

- Create: `internal/mediacatalog/models.go`
- Create: `internal/mediacatalog/repository.go`
- Create: `internal/mediacatalog/repository_test.go`
- Create: `internal/mediacatalog/indexer.go`
- Create: `internal/mediacatalog/indexer_test.go`
- Modify: `internal/domain/settings.go`
- Modify: `internal/store/settings.go`
- Modify: `internal/settings/service.go`
- Modify: `internal/settings/service_test.go`
- Modify: `schemas/settings.schema.json`
- Modify: `schemas/settings_schema_test.go`
- Modify: `internal/codex/manifest.go`
- Modify: `internal/codex/manifest_test.go`
- Modify: `internal/httpapi/task_manifest.go`
- Modify: `internal/httpapi/task_manifest_test.go`

- [ ] **Step 1: 写 schema、路径与密钥泄漏失败测试**

测试覆盖：第 4.2 节六张表及唯一键/外键；重复 SHA-256 幂等；shot 时间范围越界失败；`relative_path` 的绝对路径、`..`、符号链接逃逸失败；中断后重跑只处理未完成 phase；manifest JSON 可包含 `catalog_path`、`ffmpeg_path`、`ffprobe_path`、vision/embedding endpoint/model，但绝不包含 API key、Authorization 或 secret value。

- [ ] **Step 2: 运行定向测试确认失败**

Run: `go test ./internal/mediacatalog ./internal/settings ./internal/codex ./internal/httpapi -run 'Catalog|MediaCatalog|Manifest.*Secret|FFmpeg|Embedding|Vision' -count=1`

Expected: FAIL，当前不存在 `internal/mediacatalog`，且 `PublicSettings`/manifest 没有这些字段。

- [ ] **Step 3: 实现 catalog domain 和事务仓储**

`models.go` 定义 `Source`、`Shot`、`Keyframe`、`Tag`、`Rights`、`Job` 及状态枚举；`Source.Kind` 使用顶层 `movie|broll|image`，`Source.Subtype` 使用 `video|photo|chart|illustration|generated`，`Source.Origin` 使用 `local|generated|pexels|pixabay`。`repository.go` 在 `media_root/catalog.db` 建表，所有写入使用事务。数据库仅存相对路径；每次读写文件前将路径与 canonical media root 拼接并再次验证 contained regular file。向量以 little-endian float32 blob 保存，同时记录 model、dimension、analysis_version。

- [ ] **Step 4: 增加公开配置和加密密钥**

在 `domain.PublicSettings`、`store.publicSettingKeys`、boot/`validatePublic` 和 settings schema 中新增：`media_catalog_path`、`ffmpeg_path`、`ffprobe_path`、`vision_base_url`、`vision_model`、`embedding_base_url`、`embedding_model`、`pexels_api_base_url`、`pixabay_api_base_url`、`max_external_results_per_query`。base URL 只允许 HTTPS，host 必须分别等于固定白名单 `api.pexels.com`、`pixabay.com`；测试可通过显式注入 fake transport，不开放任意生产 host。`media_catalog_path` 必须位于 `media_root`；两个二进制必须是绝对、存在、普通文件。新增 `vision_api_key`、`embedding_api_key`、`pixabay_api_key` 作为加密 secret；复用既有 `pexels_api_key`。全部密钥进入 `settings.Runtime` 的 `json:"-"` 字段，沿用现有 protector/repository 流程。

- [ ] **Step 5: 冻结 manifest 的非密钥字段**

扩展 `codex.ManifestSettings` 明确列出 `media_catalog_path` 和可复现的 endpoint/model/binary path；禁止使用 `map[string]any` 透传 Runtime。`task_manifest.go` 只从 typed public fields 复制。增加序列化扫描测试，递归检查 key/value 均不含实际测试密钥。

- [ ] **Step 6: 运行测试并提交**

Run:

```powershell
gofmt -w internal/mediacatalog internal/domain/settings.go internal/store/settings.go internal/settings internal/codex internal/httpapi/task_manifest.go internal/httpapi/task_manifest_test.go
go test ./internal/mediacatalog ./internal/settings ./internal/codex ./internal/httpapi -count=1
```

Expected: PASS。

```powershell
git add internal/mediacatalog internal/domain/settings.go internal/store/settings.go internal/settings schemas/settings.schema.json schemas/settings_schema_test.go internal/codex internal/httpapi/task_manifest.go internal/httpapi/task_manifest_test.go
git commit -m "feat: add secure montage media catalog"
```

### Task 7：用 FFmpeg 探测电影、切镜并抽取低清关键帧

**Files:**

- Create: `internal/mediacatalog/ffmpeg.go`
- Create: `internal/mediacatalog/ffmpeg_test.go`
- Create: `internal/mediacatalog/pipeline.go`
- Create: `internal/mediacatalog/pipeline_test.go`
- Modify: `internal/agentruntime/montageplan/plan.go:33-38,356-366`

- [ ] **Step 1: 写命令契约与恢复测试**

用 fake `CommandRunner` 断言：`ffprobe` 以 JSON 返回 duration/width/height/fps/audio；切镜使用 `select='gt(scene,0.32)'` 或等价可配置阈值并解析时间戳；每个 shot 在 20%、50%、80% 位置抽 2–3 张最长边不超过 512px、去音频、JPEG quality 固定的关键帧；所有参数用 argv 传递，不经 shell；进程超时会杀掉子进程并将 job 标记为可重试失败。

- [ ] **Step 2: 运行测试确认实现缺失**

Run: `go test ./internal/mediacatalog -run 'FFmpeg|FFprobe|Scene|Keyframe|PipelineResume' -count=1`

Expected: FAIL。

- [ ] **Step 3: 实现受控 runner 和场景边界规范化**

定义：

```go
type CommandRunner interface { Run(context.Context, string, ...string) ([]byte, []byte, error) }
type Probe struct { DurationMS int64; Width, Height int; FPS float64; HasAudio bool }
type SceneBoundary struct { InMS, OutMS int64 }
```

最短 shot 1500ms、最长 12000ms；过短边界并入相邻镜头，过长镜头等距拆分；输出按 InMS/OutMS 稳定排序。`ProbeDuration` 改为注入已验证的 `ffprobe_path`，不再硬编码 PATH 中的 `ffprobe`。

切镜质量升级路径：FFmpeg `select='gt(scene,阈值)'` 对渐变转场（叠化、淡入淡出）易漏切。若真机验收发现切镜质量不足，可在既有受控 Python 环境改用 PySceneDetect（`ContentDetector`/`AdaptiveDetector`，或 TransNetV2 ONNX 检测器，CPU 可跑）产出同一 `SceneBoundary` 列表。catalog 只存边界时间与检测器名/版本，切换检测器不改表结构、不重建已分析镜头。

- [ ] **Step 4: 实现只上传关键帧的本地派生流水线**

原电影和音轨永不交给 analyzer；pipeline 只将生成的 2–3 张低清 JPEG 路径交给下一阶段。关键帧先写 job-specific `.part`，校验尺寸/SHA-256 后原子 rename；失败时不留下 catalog completed 状态。电影 P1 默认不物理导出 shot MP4，只记录源文件和 in/out。

- [ ] **Step 5: 用合成 fixture 做真实 FFmpeg 集成测试**

Run:

```powershell
go test ./internal/mediacatalog -count=1
go test ./internal/agentruntime/montageplan -count=1
```

Expected: 配置的 FFmpeg 环境下 PASS；未配置时测试显式 Skip 并验证 preflight 返回 `ffmpeg_not_configured`，不允许网络下载二进制或静默退化。

- [ ] **Step 6: Commit**

```powershell
git add internal/mediacatalog internal/agentruntime/montageplan
git commit -m "feat: index movie shots with ffmpeg"
```

### Task 8：分析关键帧并生成可复现的视觉向量

**Files:**

- Create: `internal/mediacatalog/analyzer.go`
- Create: `internal/mediacatalog/analyzer_test.go`
- Create: `internal/mediacatalog/prompts.go`
- Create: `internal/mediacatalog/prompts_test.go`
- Modify: `internal/mediacatalog/pipeline.go`
- Modify: `internal/mediacatalog/repository.go`

- [ ] **Step 1: 写严格 JSON、隐私与幂等测试**

fake server 必须断言请求只含该 shot 的 2–3 张 data URL/上传引用且每张最长边 ≤512px，不含电影路径、视频 bytes、音频或相邻 shot；响应必须严格包含 `summary,mood,setting,people_count,motion_level,has_text,tags`。额外测试 429 指数退避、超时、畸形 JSON、embedding dimension 变化、同 source hash + analysis version 重跑不重复请求。

- [ ] **Step 2: 运行失败测试**

Run: `go test ./internal/mediacatalog -run 'Analyzer|Vision|Embedding|Analysis' -count=1`

Expected: FAIL。

- [ ] **Step 3: 实现窄接口和固定 prompt version**

定义：

```go
type ShotAnalysis struct {
    Summary string `json:"summary"`
    Mood string `json:"mood"`
    Setting string `json:"setting"`
    PeopleCount int `json:"people_count"`
    MotionLevel string `json:"motion_level"`
    HasText bool `json:"has_text"`
    Tags []TagScore `json:"tags"`
}
type VisionAnalyzer interface { Analyze(context.Context, []KeyframeInput) (ShotAnalysis, error) }
type Embedder interface { Embed(context.Context, string) ([]float32, error) }
```

prompt 要求描述可见事实，不识别人名、不推断敏感属性、不复述画面中文字；embedding 输入由 summary + tags + mood + setting 的固定模板生成，保存 `analysis_version="vision-v1"` 与具体模型名/维度。

- [ ] **Step 4: 接入限流、重试与状态机**

并发取公开设置上限；429/5xx 最多 3 次，使用可注入 clock 的 1s/2s/4s backoff；4xx contract error 不重试。每个 shot 独立提交结果，失败 shot 可单独 retry，不回滚已成功 shot。任何 API key 仅从 `settings.Runtime` 注入 analyzer，日志和 DB 禁止持久化。

- [ ] **Step 5: 运行测试并提交**

Run: `gofmt -w internal/mediacatalog && go test ./internal/mediacatalog -count=1`

Expected: PASS；重复运行同一 fixture 的网络调用数为 0。

```powershell
git add internal/mediacatalog
git commit -m "feat: analyze montage shot keyframes"
```

### Task 9：实现文案意图、四级召回和确定性重排

**Files:**

- Create: `internal/agentruntime/montageplan/match.go`
- Create: `internal/agentruntime/montageplan/match_test.go`
- Modify: `internal/agentruntime/montageplan/intent.go`
- Modify: `internal/agentruntime/montageplan/intent_test.go`
- Modify: `internal/agentruntime/montageplan/quota.go`
- Modify: `internal/agentruntime/montageplan/plan_v2.go`
- Modify: `internal/agentruntime/montagescript/run.go`
- Test: `internal/agentruntime/montagescript/run_test.go`

- [ ] **Step 1: 写四级降级、打分和稳定性测试**

fixture 至少包含银行柜台、关门、水位下降、深夜独坐、城市交通五类 shot；分别验证 direct、metaphor、emotion、neutral 命中；阈值使用第 5.3 节的 0.72/0.62/0.54；同分按 `shot_id` 和 stable hash 排序；AI 候选顺序随机打乱不改变最终 timeline；可见文字风险、近邻同源、重复 shot、配额压力正确扣分。

- [ ] **Step 2: 运行测试确认当前假语义说明失败**

Run: `go test ./internal/agentruntime/montageplan ./internal/agentruntime/montagescript -run 'Intent|Match|Recall|Rank|MontagePlanV2' -count=1`

Expected: FAIL；当前 `selection_reason` 是硬编码风景轮询说明。

- [ ] **Step 3: 实现 NarrativeIntent 的模型接口和本地 fallback**

定义 `IntentAnalyzer.Analyze(ctx, []TimedSentence) ([]NarrativeIntent,error)`。严格 JSON 包含 `entities/topics/mood/visual_concepts/metaphors/importance/caption_kind`；失败时本地规则提取数字、实体样式词、`但是/真正/因此/结论` 等转折/结论词，并生成可工作的 fallback，不阻塞任务。

- [ ] **Step 4: 实现四级召回和第 5.3 节固定评分**

每级先从 catalog 查询最多 50 个候选，再按固定公式生成 `rankedCandidate`。`MatchEvidence` 必须写入 level、分数、intent ID、命中的 tag/concept 和短 reason；reason 由模板生成，不让模型自由改写。确定性 planner 应用 Task 2 的配额与去重；模型永不直接决定最终时间线。

- [ ] **Step 5: 在 montagescript 中注入 catalog/analyzer，而非全局读取**

扩展 `montagescript.Options` 注入 planner/capability 所需接口，测试使用 fake catalog/analyzer。失败策略：模型不可用走本地 fallback；catalog 不可读返回 `media_catalog_unavailable`；候选不足走 neutral 并写 QC warning。

- [ ] **Step 6: 运行测试并提交**

Run:

```powershell
gofmt -w internal/agentruntime/montageplan internal/agentruntime/montagescript
go test ./internal/agentruntime/montageplan ./internal/agentruntime/montagescript -count=1
```

Expected: PASS。

```powershell
git add internal/agentruntime/montageplan internal/agentruntime/montagescript
git commit -m "feat: match narration intents to montage shots"
```

### Task 10：增加建库状态 API、设置入口和任务 QC 摘要

**Files:**

- Create: `internal/httpapi/media_catalog.go`
- Create: `internal/httpapi/media_catalog_test.go`
- Modify: `internal/app/app.go`
- Modify: `internal/app/app_test.go`
- Create: `web/src/api/mediaCatalog.ts`
- Create: `web/src/media-library/MediaLibraryPanel.tsx`
- Create: `web/src/media-library/MediaLibraryPanel.test.tsx`
- Modify: `web/src/settings/SettingsPanel.tsx`
- Modify: `web/src/settings/SettingsPanel.test.tsx`
- Modify: `web/src/project-workbench/ProjectWorkbench.tsx`
- Modify: `web/src/project-workbench/ProjectWorkbench.test.tsx`
- Modify: `web/src/project-workbench/types.ts`
- Modify: `web/src/App.tsx`
- Test: `web/src/App.test.tsx`
- Create: `web/e2e/media-catalog.spec.ts`

- [ ] **Step 1: 写 API 合同失败测试**

路由固定为：`GET /api/media-catalog/status`、`GET /api/media-catalog/sources?kind=&status=&cursor=`、`POST /api/media-catalog/index`、`POST /api/media-catalog/sources/{id}/retry`。响应 DTO：

```json
{
  "state":"idle|scanning|extracting|analyzing|ready|degraded|failed",
  "counts":{"sources":12,"shots":328,"ready_shots":310,"failed_shots":18},
  "active_job":{"id":"...","phase":"analyzing","completed_units":61,"total_units":100},
  "warnings":[{"code":"rights_unknown","count":2}]
}
```

测试 GET 无 CSRF、POST 必须通过现有 CSRF middleware；同一时间只能有一个全局 index job，重复请求返回 409 `catalog_job_active`；错误中不回显绝对电影路径或 API key。

- [ ] **Step 2: 写 UI 失败测试**

MediaLibraryPanel 展示目录配置、总源/shot/ready/failed、当前 phase、进度、错误码、按 kind/status 过滤和单项重试。SettingsPanel 只配置全局库；ProjectWorkbench 只显示本次 plan 的 B-roll/movie/image 比例、字幕模式/覆盖率、效果数量和 warnings，不能把 catalog 冒充项目 assets。

- [ ] **Step 3: 运行后端和前端失败测试**

Run:

```powershell
go test ./internal/httpapi ./internal/app -run 'MediaCatalog|CatalogRoute' -count=1
npm --prefix web run test -- MediaLibraryPanel SettingsPanel ProjectWorkbench App
```

Expected: FAIL，新路由和组件尚不存在。

- [ ] **Step 4: 实现 handler、挂载和前端轮询**

handler 仅依赖窄 `CatalogService` 接口；在 `app.go` 与 settings 路由相邻挂载 `/api/media-catalog/`。前端轮询仅在 active job 存在时每 2 秒执行，组件卸载或状态进入 ready/degraded/failed 时取消；错误提供可操作文案 `ffmpeg_not_configured`、`vision_not_configured`、`catalog_path_invalid`。

- [ ] **Step 5: 完成 unit/E2E 并提交**

Run:

```powershell
gofmt -w internal/httpapi/media_catalog.go internal/httpapi/media_catalog_test.go internal/app/app.go internal/app/app_test.go
go test ./internal/httpapi ./internal/app -count=1
npm --prefix web run typecheck
npm --prefix web run test
npm --prefix web run test:e2e -- media-catalog.spec.ts
```

Expected: PASS。

```powershell
git add internal/httpapi/media_catalog.go internal/httpapi/media_catalog_test.go internal/app web/src/api/mediaCatalog.ts web/src/media-library web/src/settings web/src/project-workbench web/src/App.tsx web/src/App.test.tsx web/e2e/media-catalog.spec.ts
git commit -m "feat: expose montage media catalog status"
```

### Task 11：接入生成图片与有权利元数据的外部素材导入

**Files:**

- Create: `internal/mediacatalog/importer.go`
- Create: `internal/mediacatalog/importer_test.go`
- Create: `internal/mediacatalog/providers.go`
- Create: `internal/mediacatalog/providers_test.go`
- Modify: `internal/imageproject/planner.go`
- Modify: `internal/imageproject/planner_test.go`
- Modify: `internal/imageproject/client.go`
- Test: `internal/imageproject/client_test.go`
- Modify: `internal/httpapi/media_catalog.go`
- Modify: `internal/httpapi/media_catalog_test.go`

- [ ] **Step 1: 写统一导入和权利失败测试**

测试本地文件、`imageproject.GenerateResult`、Pexels/Pixabay fake HTTP 响应都进入同一 importer；按 bytes SHA-256 去重；目标先写唯一 `.part` 再原子 rename；落库前验证 MIME/尺寸；外部条目必须保存 `source_url,creator,license_code,license_url,attribution,retrieved_at,rights_notes`；未知许可写 `publishability="local_draft_only"`，绝不写 `publishable`。

- [ ] **Step 2: 运行失败测试**

Run: `go test ./internal/mediacatalog ./internal/imageproject ./internal/httpapi -run 'Import|Rights|GeneratedImage|Provider' -count=1`

Expected: FAIL，当前没有统一 importer/provider。

- [ ] **Step 3: 定义 importer/provider 窄接口**

```go
type ImportRequest struct {
    Kind SourceKind
    Bytes []byte
    SuggestedName string
    Prompt string
    Rights Rights
}
type Provider interface {
    Search(context.Context, string, int) ([]RemoteAsset, error)
    Fetch(context.Context, RemoteAsset) (io.ReadCloser, error)
}
```

实现 `PexelsProvider` 和 `PixabayProvider`；认证只从 `settings.Runtime.PexelsAPIKey/PixabayAPIKey` 注入，请求 host 必须匹配 Task 6 的固定白名单，单次 limit 不超过 `max_external_results_per_query`（默认 20、最大 50）。对应 key 未配置时 provider 状态为 disabled，并返回 `provider_not_configured`，不阻塞纯本地建库/生成图。下载只允许 HTTPS、公网 DNS/IP、最多 3 次同白名单域重定向、单文件最大 200 MiB、受支持视频/图片 MIME；文件名由服务端生成，不能使用 URL 路径直接落盘。写 catalog 必须持有现有 `media-index-write` 锁或升级为同一共享素材写锁，禁止与注册锁嵌套。

- [ ] **Step 4: 复用图文核心而不复用项目表/ZIP**

复用图文模式的**生产路径**：`imageproject.SuggestSegments`/`SuggestPrompts`（AI 分段与提示词，输入改为带时间的叙事意图段，段数不受图文模式 18 张上限约束）与 `Generator.Generate`/`GenerateBatch`；适配器接收 `GenerateResult.Bytes/MIMEType/Width/Height` 后调用 importer。提示词必须携带系列一致性锚（风格预设、统一色板、固定视觉母题）。**`SplitScript`/`BuildPrompt` 是已退役的规则拆卡路径（仅测试引用），不得接回生产。**不得调用 `internal/httpapi/imageprojects.go`，不得写 `image_projects/image_project_items`，不得生成 ZIP。`rights_notes` 记录 provider/model/prompt hash/生成时间，API key 不记录。

- [ ] **Step 5: 添加可选的缺口补图策略**

只有 deterministic planner 报告 image quota 缺口且设置允许生成时，才为最多 3 个高 importance intent 生成图片；每个 intent 只请求一张，失败则继续使用本地 image/neutral B-roll 并写 warning，不阻塞草稿。外部搜索结果先展示来源和许可摘要，导入后才可被 planner 使用。

- [ ] **Step 6: 运行测试并提交**

Run:

```powershell
gofmt -w internal/mediacatalog internal/imageproject internal/httpapi/media_catalog.go internal/httpapi/media_catalog_test.go
go test ./internal/mediacatalog ./internal/imageproject ./internal/httpapi -count=1
```

Expected: PASS；fake provider 能证明没有请求私网、没有密钥落库、重复内容不生成第二份文件。

```powershell
git add internal/mediacatalog internal/imageproject internal/httpapi/media_catalog.go internal/httpapi/media_catalog_test.go
git commit -m "feat: import generated and licensed montage media"
```

### Task 12：能力门控、全链路验收与文档同步

**Files:**

- Create: `internal/skillregistry/capabilities.go`
- Create: `internal/skillregistry/capabilities_test.go`
- Modify: `internal/domain/settings.go`
- Modify: `internal/httpapi/task_manifest.go`
- Modify: `internal/httpapi/task_manifest_test.go`
- Modify: `internal/agentruntime/montagescript/run.go`
- Modify: `internal/agentruntime/montagescript/run_test.go`
- Modify: `README.md`
- Modify: `docs/AI-HANDOFF.md`
- Modify: `docs/ARCHITECTURE.md`
- Modify: `docs/USER-GUIDE.md`
- Modify: `docs/operations/acceptance-checklist.md`
- Modify: `docs/operations/troubleshooting.md`

- [ ] **Step 1: 写 capability 与历史 snapshot 兼容测试**

capability 不能靠版本字符串或文件存在猜测。新增 `assets/capabilities.json` 到 skill，内容至少为：

```json
{"contract_version":"1.0","production_plan_versions":["1.0","2.0"],"features":["highlight_captions","image_keyframes","style_policy_v2"]}
```

`skillregistry` 从 `SkillSnapshot.Files` 找到并按该 snapshot 的已冻结内容读取/校验 capability；测试：有 v2 capability 才在 manifest 冻结 `montage_plan_version="2.0"`，没有/畸形 capability 继续 v1，未知 capability 不启动 v2；已经持久化的 v1 task 始终使用自己的 `skill_snapshot_id` 和 manifest，不因 Latest 更新而改变。

- [ ] **Step 2: 运行失败测试**

Run: `go test ./internal/skillregistry ./internal/httpapi ./internal/agentruntime/montagescript -run 'Capability|PlanVersion|HistoricalSnapshot' -count=1`

Expected: FAIL，当前 snapshot 没有 capability parser，manifest 也没有 plan version。

- [ ] **Step 3: 按安全顺序启用 v2**

严格执行：① Task 4/5 完成 skill 双读和真机验证；② 重新扫描并冻结含 capability 的新 snapshot；③ `taskManifestPreparer` 在创建新任务时读取该 snapshot capability；④ 只有明确含 `2.0` 才写 v2 manifest 并让 `montagescript` 调 `BuildV2`；⑤ v1 task/旧 snapshot 保持 `Build`。capability 解析失败按 v1 安全降级并记录 warning，不把任务置于半生成状态。

- [ ] **Step 4: 跑全量自动化基线**

Run:

```powershell
go test ./...
go vet ./...
npm --prefix web run typecheck
npm --prefix web run lint
npm --prefix web run test
npm --prefix web run test:e2e
npm --prefix web run build:verify
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify-baseline.ps1 -StrictEmbeddedDist
```

Expected: 全部 exit 0。当前环境已知 `go test ./...` 曾在 124 秒超时、`npm`/FFmpeg 当前不可用；实施环境必须先按 Task 0 补齐工具链并给测试足够超时。已知 skill 注册测试若仍失败，必须在发布 v2 前修复，不接受基线豁免。

- [ ] **Step 5: 用三种真实场景做明文和剪映真机验收**

分别运行：电影+B-roll+图片齐全；电影不足触发降级；字幕 mode=off。每个场景验证 plan JSON、plaintext draft、QC report、注册 receipt；人工打开新注册草稿确认图片 Ken Burns、标题只在开场、重点字幕可编辑/关闭、电影/B-roll/图片比例大致合规、音效稀疏、明显效果不密集。不得覆盖旧草稿；注册仍由受信控制台通过同进程 managed `register` 完成。

- [ ] **Step 6: 同步用户文档和故障排查**

文档必须说明素材目录布局、推荐渠道与许可记录、FFmpeg/视觉/embedding 设置、完整电影不上云、caption off/highlights-only、建库状态码、配额降级 warning、真机白名单以及“可生成草稿不等于拥有公开发布权”。`AI-HANDOFF` 记录 v1/v2 兼容和 snapshot 绑定。

- [ ] **Step 7: 最终提交**

```powershell
git add internal/skillregistry internal/domain/settings.go internal/httpapi/task_manifest.go internal/httpapi/task_manifest_test.go internal/agentruntime/montagescript README.md docs
git commit -m "feat: enable verified montage media intelligence v2"
```

## 11. 端到端验收标准

1. 300 秒、三类素材充足时，B-roll/movie/image 的时间占比分别落在 35%–45% / 25%–35% / 20%–30%；单 shot 不重复，同 source 最多两次且相隔至少五段。
2. `captions.mode=off` 的草稿没有字幕轨；`highlights_only` 只创建重点句文字片段，区间并集为旁白 15%–25%，不导入全量 SRT，不创建 ASR/文稿匹配轨。
3. 开场标题 3–5 秒后消失；章节标签短暂出现；图片有可编辑 Ken Burns 关键帧；电影和 B-roll 不再统一 1.4 缩放/0.5 透明度。
4. 每个 shot 都可追溯到 source、in/out、match level/score/reason 和权利元数据；未知权利素材明确标记 `local_draft_only`。
5. 云端请求只含每 shot 的 2–3 张低清关键帧及必要文本，绝不含完整电影、连续视频帧或电影音轨。
6. 转场、BGM、SFX、文字动画、滤镜、蒙版和明显效果只使用真机验证白名单；任意 30 秒最多一个明显效果，SFX 至少间隔 12 秒。
7. 新 v2 任务必须绑定明确声明 v2 capability 的 skill snapshot；历史 v1 task 在升级后仍可重试并得到原有行为。
8. 草稿中的视频、图片、文字、关键帧、BGM、SFX 和转场均可在剪映继续编辑；自动测试、明文 QC、注册 receipt 和一次人工真机验收均有证据。
