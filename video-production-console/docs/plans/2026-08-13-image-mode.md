# 图文模式 Implementation Plan

> **For Hermes:** 严格按 TDD 逐项实现；混剪线和图文线不共享视觉参数。

**Goal:** 在现有视频生产控制台中增加可切换的图文模式，接收用户最终文案、按语义生成有序图片、网页预览/单张重生成，并打包 ZIP 下载。

**Architecture:** 新增独立 `imageprojects` 领域，不进入现有项目/资产/混剪状态机。SQLite 持久化图文批次和图片卡片；服务端持有加密 API Key，调用 OpenAI 兼容 `/images/generations`。前端首页增加“混剪模式 / 图文模式”切换，图文工作台独立展示参数、风格、卡片与下载。

**Tech Stack:** Go `net/http`、SQLite、`archive/zip`、React/TypeScript、Vitest、Playwright。

---

## API 契约

- `GET /api/image-projects`：列出图文批次。
- `POST /api/image-projects`：创建批次并按段落/句末语义拆成指定数量卡片，不改写原文。
- `GET /api/image-projects/{id}`：读取批次与卡片。
- `PATCH /api/image-projects/{id}`：修改批次名称、比例、风格、自定义风格、并发数。
- `PATCH /api/image-projects/{id}/items/{itemID}`：修改单张提示词或对应文案。
- `POST /api/image-projects/{id}/generate`：生成缺失/失败图片。
- `POST /api/image-projects/{id}/items/{itemID}/generate`：单张重生成。
- `GET /api/image-projects/{id}/items/{itemID}/image`：显示图片。
- `GET /api/image-projects/{id}/download`：按 `001_标题.png` 顺序打包 ZIP，并附 `manifest.json`。
- `DELETE /api/image-projects/{id}`：删除批次前必须由 UI 二次确认。

## 设置

新增 public 设置：
- `image_base_url`（默认空，允许 HTTP/HTTPS；用户已明确接受当前 HTTP 风险）
- `image_model`（默认 `gpt-image-2`）
- `max_image_concurrency`（1–5，默认 3，可热更）

新增 secret：`image_api_key`。只加密落库、不回显、不写任务文件或日志。

## 风格预设

- `finance_documentary` 财经纪实插画（默认）
- `red_ink` 赤墨风
- `old_newspaper` 旧报档案风
- `ledger_investigation` 账本调查风
- `dark_crisis` 暗黑危机风
- `city_era` 城市时代感
- `blackboard` 黑板讲解风
- `custom` 自定义

所有预设默认：面向中国中老年财经认知、主体明确、中高信息密度、无日期/收益/比例/伪文字、无品牌水印。

## 任务顺序

1. RED：设置字段与 secret 契约测试；GREEN：设置持久化、加密运行时与 UI。
2. RED：SQLite migration/repository 创建、列表、更新、删除测试；GREEN：图文批次表和卡片表。
3. RED：文案拆分保持全文顺序、不改写、不丢字测试；GREEN：语义分段器。
4. RED：OpenAI 图片客户端并发、URL/base64 响应、尺寸校验、限流测试；GREEN：安全 HTTP 客户端与文件存储。
5. RED：图文 REST API 创建/生成/重生成/图片读取/ZIP 测试；GREEN：handler 与路由。
6. RED：前端模式切换、创建表单、风格和比例、画廊、重生成、打包测试；GREEN：图文工作台。
7. 更新 `ARCHITECTURE.md`、`USER-GUIDE.md`、`AI-HANDOFF.md`、验收清单和排障手册。
8. 全量 Go/TS/lint/Vitest/Playwright/build:embed，独立审查，commit；重建并重启 2030 前说明会中断当前实例并再次确认。

## 验收

- 混剪模式原功能和测试不变。
- 图文模式不调用二创任务。
- 页面可见每张图片的序号、原文、提示词、状态和实际尺寸。
- 并发值严格限制 1–5；失败单张可独立重试。
- ZIP 文件按顺序命名，manifest 可把图片追溯到原文和提示词。
- 浏览器及日志中无 API Key。
