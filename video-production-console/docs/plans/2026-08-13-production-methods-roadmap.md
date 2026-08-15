# 制作方式路线图（当前状态）

> 四种视频制作方式的**现状**。技术实现以 [项目全景说明](../ARCHITECTURE.md) 和 [AI 接手说明](../AI-HANDOFF.md) 为准。
>
> 更新时间 2026-08-15。旧版「方式三/四代码已落地待验收」已过时：方式三代码已接通、待真机出片；方式四只有 planner 预设，**不要做**。

## 1. 四种制作方式

| # | 方式 | 画面来源 | 文案/音频 | 状态 | 实现载体 |
|---|---|---|---|---|---|
| 1 | 风景混剪 | 本机 catalog 召回后再滤风景/景观；无就绪风景时回退 `media_index.json` 打散 | 二创 + 火山 TTS | **日产能用** | `BuildV2` + `filterLandscapeCandidates`；旧 skill 走 v1 `Build` |
| 2 | 图文批次 | 文本模型分段 + 生图 | 定稿文案；音频用户自理 | **日产能用** | `/image-projects` 一键；`/advanced` 手动 |
| 3 | 电影混剪 | 本机电影切镜库按口播四级召回，不过滤风景 | 二创 + 火山 TTS | **代码接通，待真机出片** | `jianying-movie-montage` + `SelectModeMovieCatalog` |
| 4 | 图片视频 | 按词级 SRT 现场生图 + Ken Burns | 二创 + TTS + SRT | **停放**：仅 `image_video` 预设，无用户入口 | 不要实施 |

方式二交付有序图片 ZIP。方式三/一交付剪映可编辑草稿。

## 2. 建库与自动检索（已接通）

用户本机操作：

1. 电影/B-roll/图片放入 `media_root/originals/{movies,broll,images}/`。
2. 设置填写素材库目录、FFmpeg/FFprobe、视觉与 embedding。
3. 控制台「素材库 → 开始建库」。现在跑完整三步：扫描 → 切镜抽帧 → 打标向量（`mediacatalog.RunHostedBuild`）。
4. 看「就绪镜头」。之后点混剪，系统自动从 `catalog.db` 检索，不必手工选片。

云机：独立程序 `cmd/catalog-builder`（`:2031`）建库 → 下载结果包 → 本机同一程序「合并到本机」。原片相对路径必须一致。控制台没有合并 UI。

检索规则见 [ARCHITECTURE §5.4 / §11.5](../ARCHITECTURE.md)。风景线滤风景；电影线跟口播。识别全失败不得标 ready。

## 3. 共享底座（不要误读）

方式三与方式四曾计划共享 v2 契约。当前产品锁：

- 风景/电影日产：`captions.mode=off`，无窗内白色片头，缩放约 1.20。
- 方式四不要开工。
- 二创硬切 OpenAI 兼容接口，不走 Codex。

## 4. 下一步（只验收，不扩线）

1. 风景混剪真机再跑 2–3 条。
2. 本机建一部短片或合并已有 catalog-pack，跑一条电影混剪。
3. 图文一键：刷新恢复、「继续生成」、ZIP。
4. 不要同时开图片视频。

## 5. 历史计划

`docs/plans/2026-08-13-montage-media-intelligence.md` 已收成历史指针。完整旧稿在 git 历史。不要按其中的勾选框或「15%–25% 重点字幕」施工。
