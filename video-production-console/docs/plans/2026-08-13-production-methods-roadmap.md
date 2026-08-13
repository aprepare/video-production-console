# 制作方式路线图（Production Methods Roadmap）

> 本文是四种视频制作方式的定义、现状、依赖与实施顺序的权威说明，供接手的 AI 建立产品方向判断，避免把"未实施的计划"误读为现状。技术实施细节见 [素材智能混剪 v2 计划](2026-08-13-montage-media-intelligence.md)；系统现状描述以 [项目全景说明](../ARCHITECTURE.md) 为准，本文不重复。
>
> 更新时间 2026-08-13。方式一、二已上线；方式三、四未实施。

## 1. 四种制作方式

| # | 方式 | 画面来源 | 文案/音频 | 状态 | 实现载体 |
|---|---|---|---|---|---|
| 1 | 风景混剪 | 本地素材库确定性打散轮询 | 二创文案 + 火山 TTS | 已上线 | `internal/agentruntime/montageplan`（plan v1） |
| 2 | 图文批次 | AI 分段建议 → 确认 → 按段生图 | 定稿文案；音频由用户在发布侧自理 | 已上线（`c7382e5`，2026-08-13） | `internal/imageproject` + 图文工作台 |
| 3 | 电影混剪 | 本地电影切镜 + 语义检索匹配文案 | 二创文案 + 火山 TTS | 未实施 | v2 计划 P0–P3 全量 |
| 4 | 图片视频 | 按词级 SRT 分段逐段生图 + 动效 | 二创文案 + 火山 TTS + 词级 SRT | 未实施 | v2 计划 P0/P1 子集 + 图文组件复用（P1.5 里程碑） |

说明：方式二交付有序图片 ZIP，控制台不托管音频与成片合成；其音频用法的平台合规性由用户自行确认（用户已确认视频号合规）。方式四的成片是可编辑剪映草稿，不是渲染死的 MP4。

## 2. 核心结构判断（实施前必读）

1. **方式三与方式四共享同一底座**：SRT→叙事意图分段、时长配额与去重、图片 Ken Burns、重点字幕、真机特效白名单、`production_plan` v2 契约与 QC。二者区别只在画面获取方式——方式三是"从既有素材库**检索**"（必须先建库：切镜、视觉打标、向量），方式四是"按段**现场生成**"（无需预先建库）。
2. **检索的基建比生成重一个数量级**，所以先做方式四、后做方式三：方式四只需要 v2 契约层 + 图文组件复用，不需要 FFmpeg 切镜、视觉分析模型和 catalog 检索。
3. **方式四的建库是"生成即入库"**：图片按每条视频现场生成，顺手登记进素材目录。生成素材的元数据是免费的（提示词、源文案段、风格、模型即天然标签），不需要视觉打标。积累到规模后二期开启"先检索后生成"降本；复用必须配 `use_count`/`last_used_at` 防同账号视觉重复。
4. **终局是一个库、两类来源**：方式三/四产物进入同一 catalog（`origin` 区分 `local`/`generated`），规划器统一四级召回。方式三上线后不是独立管线，而是"方式四底座 + 电影素材来源"。

## 3. 方式四：图片视频管线

| 步骤 | 内容 | 现状 |
|---|---|---|
| 1 | 二创文案 → 火山 TTS 配音 + 词级 SRT | 已有（`POST /api/projects/{id}/narration`） |
| 2 | 词级 SRT 聚合为 6–10 秒"视觉段"（按句子边界；数字/转折/结论句单独成段），5 分钟约 30–45 段 | 待建（v2 计划 `intent.go`） |
| 3 | 按段生成提示词：复用 `imageproject.SuggestSegments`/`SuggestPrompts` 思路，输入改为带时间的段落；必须加系列一致性锚（风格预设 + 统一色板 + 固定视觉母题），段落上限放开（不受图文模式 18 张限制） | 待改造（`internal/imageproject/planner.go`） |
| 4 | 批量生图（并发 1–18） | 已有（`imageproject.Generator`/`GenerateBatch`） |
| 5 | 图片落盘 `media_root/derived/generated/{provider}/` 并登记 catalog（prompt、源段落、tags、模型、时间） | 待建（薄适配层，v2 计划 Task 11 的生成部分） |
| 6 | `production_plan` v2（`image_video` mix 预设）→ skill 生成图片 Ken Burns + 重点字幕草稿 → 剪映登记 | 待建（v2 计划 Task 3/4/5） |

开场 30–60 秒 vox 风格（图生视频）为**二期增强**：先用图片 + Ken Burns + 白名单特效上线跑数据，验证后再接图生视频；产物作为 `origin=generated` 视频素材入库复用。

## 4. 方式三：电影建库操作与方法

用户操作只有三步：电影文件放入 `media_root/originals/movies/` → 设置里配好 FFmpeg 与视觉模型 → 控制台点建库、看进度、失败单条重试。

系统内部流程：哈希去重 → `ffprobe` 探测 → 场景检测切镜（只记录每镜 in/out 时间点，不物理切割文件）→ 每镜在 20%/50%/80% 抽 2–3 张 512px 低清关键帧 → 关键帧交多模态模型打标（一句话摘要、情绪、场景、人数、运动强度、标签；严格 JSON）→ 文本 embedding → 存 `media_root/catalog.db`。**电影本体与音轨永不上传。**

规模预期：2 小时电影约 800–1200 个镜头，打标是两三千次小图请求；建库必须支持断点续跑与单条重试（v2 计划 `media_jobs` 状态机已设计）。切镜首选 FFmpeg 场景滤镜，质量不足时升级 PySceneDetect/TransNetV2（见 v2 计划 Task 7）。

### 4.1 分布式建库（多机协作，二期规划）

用户可用多台电脑分摊建库，结束后把结果并回主机。设计约定（catalog 的内容寻址已为此留好接口，实施为一个独立合并命令，不改表结构）：

- 传回物：远程机的 `catalog.db` + `derived/keyframes/`（小截图，通常几百 MB），电影本体不必随建库包往返；
- 合并规则：按 `media_sources.sha256` 逐条并入——本地已有则跳过，没有则收编其 shots/tags/rights/embeddings 与截图文件，远程 ID 重映射为本地 ID；重复合并幂等；
- 硬前提一：**电影原件最终必须在主机** `media_root/originals/movies/` 的相同相对路径下，否则台账条目无法用于剪映成片；
- 硬前提二：所有机器统一视觉模型、embedding 模型/维度、`analysis_version`，否则向量互不可比；
- 硬前提三：目录结构一致（同一套 `originals/` 约定）；
- 形态建议：`console-maintenance.exe` 风格子命令（`catalog merge <远程包路径>`），或控制台维护页入口；
- 收益边界：多机省的是切镜/抽帧 CPU 与打标墙钟时间（各机各用 API key 并行），打标 API 费用不因多机而减少。

配套的**独立建库 CLI**（`catalog-builder`，二期）：把 T6–T8 的建库管线套一个免安装命令行外壳，参数化素材目录、ffmpeg/ffprobe 路径、vision/embedding 端点与密钥、并发数；断点续跑复用 `media_jobs` 状态机；密钥走参数/环境变量，不依赖 DPAPI。用于云机分布式建库。

GPU 选型指引：默认设计（云 API 打标）不吃 GPU——切镜抽帧吃 CPU、打标吃网络，租多核 CPU 云机即可；只有改用本地视觉模型（vLLM/Ollama 起 Qwen-VL 类模型，暴露 OpenAI 兼容端点、`vision_base_url` 指向本机）才需要 4090 级 GPU，此时零代码改动即可切换。决策顺序：先用云 API 单机试跑一部电影核实成本，再决定 CPU 云机堆并行还是 GPU + 本地模型。

## 5. 可借鉴的开源项目（2026-08-13 检索）

| 项目 | 借鉴点 | 对应环节 |
|---|---|---|
| [NarratoAI](https://github.com/linyqh/NarratoAI) | 影视解说全链路参照：抽帧/缓存/视觉并发链路、FFmpeg 引擎检测、剪映草稿导出；视觉打标成本参考（宣传硅基流动模型剪 10 分钟视频约 0.1 元） | 方式三整体 |
| [PySceneDetect](https://github.com/Breakthrough/PySceneDetect) | 切镜标准库：`ContentDetector`/`AdaptiveDetector` 与 TransNetV2 ONNX 检测器（渐变转场更准，CPU 可跑） | 方式三切镜 |
| [videoseek-cli](https://github.com/dakshjain-1616/videoseek-cli)、[SentrySearch](https://github.com/ssrajadh/sentrysearch)、[SceneTrace_AI](https://github.com/NYN-05/SceneTrace_AI)、[sift-video](https://github.com/sourav4243/sift-video) | 验证两种检索架构：视觉 LLM 描述→文本检索（可解释、中文友好，v2 计划采用）vs CLIP 类直接向量（免费本地，无法解释，可作二期补充召回） | 方式三检索 |
| [story-flicks](https://github.com/alecm20/story-flicks) | "一段一图 + TTS + 字幕成片"结构验证；其短板（先分段后配音时间不准、无风格一致性、渲染死 MP4、无复用）即本项目差异点 | 方式四 |
| [MoneyPrinterTurbo](https://github.com/harry0703/MoneyPrinterTurbo) | 管线骨架、字幕双模式（TTS 词级时间戳 / faster-whisper 对齐兜底）、Pexels/Pixabay 接入实现参考 | 方式四 / 外部素材 |

共同边界：上述项目全部直接渲染成品 MP4。本项目输出可编辑剪映草稿 + 确定性规划 + 登记校验，不采用 MoviePy 渲染路线，不要被参照项目带偏。

## 6. 实施顺序

1. 已完成：图文模式收尾并提交（`c7382e5` 等，2026-08-13）。
2. v1 真机回归（[AI 接手说明 §1.2](../AI-HANDOFF.md) 第 1–2 条），确认基线绿，再动代码。
3. v2 计划 Task 0–2：typed media index + 三类时长配额，修掉"只选风景"（方式一即刻受益）。
4. v2 计划 Task 3/4/5 + intent 分段 + 图片生成适配 → **方式四上线**（P1.5 里程碑）。
5. v2 计划 P2 电影建库（FFmpeg + 视觉打标 + 四级召回）→ **方式三上线**。
6. 可选：图生视频开场、外部图库导入、库内检索复用降本。

## 7. 修订记录

2026-08-13 依据本路线图对 [v2 计划](2026-08-13-montage-media-intelligence.md) 的修订（内容已同步进该文档）：

- Task 11 复用目标由 `SplitScript`/`BuildPrompt`（已退役，仅测试引用）改为 `SuggestSegments`/`SuggestPrompts`。
- `media_mix_policy` 增加任务级预设 `movie_mix` / `image_video`，方式四以 image 主导配额交付。
- §8 分阶段表新增 P1.5"图片视频"里程碑。
- Task 7 切镜增加 PySceneDetect/TransNetV2 升级路径。
- §4.2 明确图片（无 shot 概念）的 embedding 落 source 级或整图退化 shot，实施时二选一。
- Task 0 的图文基线前置已满足；`docs/plans/2026-08-13-image-mode.md` 已按 docs 惯例删除（实现早已超出该计划，现状见 [全景说明](../ARCHITECTURE.md) 与 [AI 接手说明 §5](../AI-HANDOFF.md)，考古走 git 历史）。
