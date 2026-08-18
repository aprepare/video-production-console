# 素材智能混剪 v2（历史计划，不要按本文施工）

> **2026-08-15：** 本文原是 Task 0–12 实施计划，勾选框和「重点字幕 15%–25% / 3–5 秒白字片头」已经**不是**当前产品。
>
> 现状请读：
>
> - [项目全景说明](../ARCHITECTURE.md) §5.4、§11
> - [AI 接手说明](../AI-HANDOFF.md)
> - [制作方式路线图](2026-08-13-production-methods-roadmap.md)
>
> 当前已落地：`catalog.db`、`RunHostedBuild`、`BuildV2` 四级召回、风景过滤、电影 `movie_catalog` 预设、控制台与 catalog-builder 共用建库。
>
> 当前产品锁：风景/电影混剪 `captions.mode=off`，无窗内白字标题，缩放约 1.20。方式四（图片视频）停放。
>
> 完整旧稿在 git 历史（本文件被替换前的版本）。需要考古时用 `git log -- docs/plans/2026-08-13-montage-media-intelligence.md`。
