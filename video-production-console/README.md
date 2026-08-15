# 视频生产控制台

本地视频生产控制台：Go 管鉴权、项目/资产、任务调度、图文批次和素材库；React 提供制作方式首页、五阶段混剪工作台和图文工作台。混剪交付剪映可编辑草稿，图文交付有序图片 ZIP。二创走 OpenAI 兼容接口，不走 Codex CLI。

新安装默认监听 `http://127.0.0.1:2030`。首次启动前须设置 `VIDEO_CONSOLE_INITIAL_PASSWORD`；已有管理员后不会再读取该变量。

## 文档导航

| 文档 | 用途 |
|---|---|
| [项目全景说明](docs/ARCHITECTURE.md) | **先读这份**：模块、目录、机制、契约；§11 是给其他 AI 的模块→代码对照 |
| [可交互架构图](docs/archify/runtime.architecture.html) | Archify 运行时地图：搜索节点、追踪路径、四条导览 |
| [使用说明](docs/USER-GUIDE.md) | 登录、图文、同行原文二创、本机建库与自动检索、混剪限制 |
| [AI 接手说明](docs/AI-HANDOFF.md) | 2026-08-15 进度、能用什么、不要做什么、验证命令、工作区红线 |
| [优化工单](docs/OPTIMIZATION-BACKLOG.md) | 历史优化项；P0–P3 代码项已完成，不要当未做清单 |
| [制作方式路线图](docs/plans/2026-08-13-production-methods-roadmap.md) | 四种方式的**当前**状态（方式四已停放） |
| [混剪登记](docs/operations/montage-registration.md) | 明文草稿、登记、重试与本机限制 |
| [验收清单](docs/operations/acceptance-checklist.md) | 人工验收路径 |
| [常见问题与排障](docs/operations/troubleshooting.md) | 已验证故障、错误原文、根因、最小处置与禁止操作 |

## 最短启动

```powershell
# 仅首次安装（尚无管理员）需要：
$env:VIDEO_CONSOLE_INITIAL_PASSWORD = "你的初始口令"

go run .\cmd\console
# 或运行已构建的：
# .\dist\video-production-console.exe
```

浏览器打开 `http://127.0.0.1:2030`，首次登录后立即改密。

图文生图需在「设置」填写 OpenAI 兼容的生图 Base URL、模型（默认 `gpt-image-2`）、API Key，并可设置默认并发、比例与风格。API Key 只由后端加密保存，页面只显示是否已配置，文档和配置示例都不要写真实密钥。HTTPS 更安全；系统允许在用户明确接受风险时保存 HTTP 地址，但 HTTP 会明文传输请求头中的密钥。

混剪素材库：电影/B-roll/图片放进 `media_root/originals/{movies,broll,images}/`，设置里填写素材库目录、FFmpeg/FFprobe、视觉与 embedding。首页「素材库 → 开始建库」会扫描、切镜、打标、写向量。建好且有就绪镜头后，点「开始风景混剪」或「开始电影混剪」会**自动从 `catalog.db` 检索**，不必手工选片。风景线只留风景/景观；电影线按口播概念检索。云机建库用独立程序 `cmd/catalog-builder`（`:2031`）打包，回本机合并。完整电影和原音轨不上云。skill snapshot 声明 `2.0` 的新任务走 v2 计划；旧任务继续用自己的 snapshot。未知许可只标 `local_draft_only`。

二创硬切 OpenAI 兼容接口（设置「二创*」）。混剪默认本机 `script` runtime。设置页不做 runtime 下拉。详见 [AI 接手说明](docs/AI-HANDOFF.md)。

## 日常验证（不覆盖嵌入前端）

```powershell
.\scripts\verify-baseline.ps1
```

该脚本使用 `build:verify`，输出到 `.tmp/web-dist`，不会改写 `internal/webui/dist`。

## 正式发布

```powershell
.\scripts\release.ps1 -Version 1.0.0
```

或手动：

```powershell
npm --prefix web run build:embed
.\scripts\check-embedded-dist.ps1 -Strict
go build -o .\dist\video-production-console.exe .\cmd\console
```

仅在明确要同步生产页面时执行 `build:embed`。

## 数据与垃圾清理约定

- 运行时数据根：`./video-console-data`（权威库 `console.db`）
- 根目录遗留的 `video-console.db` 不是权威库
- `.tmp/`、`dist/` 备份 exe、日志、Playwright 缓存、未跟踪构建产物均可清理重建；不要删除 `video-console-data/` 与 `internal/webui/dist/`（后者是嵌入式发布输入）

## 安全提示

- 不要把控制台暴露到公网或公共 Wi-Fi
- 生图服务优先使用 HTTPS；只有明确接受明文传输风险时才继续使用 HTTP
- 局域网访问须在设置中显式配置监听地址并重启
- 「打开剪映目录」仅允许本机回环访问触发
