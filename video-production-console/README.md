# 视频生产控制台

本地视频生产看板：Go 提供鉴权、项目/资产、Codex 任务调度与实时事件；React 提供五阶段工作台；混剪草稿可登记为剪映可继续编辑的正式资产。

新安装默认监听 `http://127.0.0.1:2030`。首次启动前须设置 `VIDEO_CONSOLE_INITIAL_PASSWORD`；已有管理员后不会再读取该变量。

## 文档导航

| 文档 | 用途 |
|---|---|
| [项目全景说明](docs/ARCHITECTURE.md) | **先读这份**：项目做什么、技术怎么实现、端到端流程、每个目录的含义、关键机制与契约现状 |
| [使用说明](docs/USER-GUIDE.md) | 登录、五阶段、同行原文二创、混剪限制 |
| [AI 接手说明](docs/AI-HANDOFF.md) | 接手备忘：近期改动、下一步规划、验证命令、工作区红线 |
| [优化工单](docs/OPTIMIZATION-BACKLOG.md) | 待办优化项，按 P0→P3 排序，每条含证据/改法/验收/边界 |
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

可选 AgentRuntime（默认不变：混剪 `script`，remix/topic `codex`）：见 [AI 接手说明 §3](docs/AI-HANDOFF.md)。例如 `VIDEO_CONSOLE_LLM_RUNTIME=openai_compat` 并配置 `VIDEO_CONSOLE_OPENAI_*`。设置页不做 runtime UI。

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
- 局域网访问须在设置中显式配置监听地址并重启
- 「打开剪映目录」仅允许本机回环访问触发
