# 视频生产控制台

本地统一视频项目看板，默认监听 `0.0.0.0:2030`，已启用管理员会话、CSRF 防护和登录限速，可供同一受信任局域网内的手机访问。开发入口首次启动会初始化管理口令 `123321`；接入局域网前必须通过 `/api/auth/password` 更换口令，且不要暴露到公共 Wi-Fi 或公网。控制台共享现有爆款库 `127.0.0.1:2022`，不会启动、停止或修改爆款库主程序。

## 开发

```powershell
go test ./...
go vet ./...
Set-Location web
npm run build
Set-Location ..
go build -o dist/video-production-console.exe ./cmd/console
```

启动后访问 `http://127.0.0.1:2030`。账号只有名称和固定背景图；项目资产与 Codex 任务按项目隔离。Codex 并发数在“设置”页调整，范围为 1—4。

混剪草稿从明文产物到剪映正式资产的状态、失败恢复和最终验收步骤，见 [混剪草稿登记与验收](docs/operations/montage-registration.md)。
完整的手工验收路径见 [控制台人工验收清单](docs/operations/acceptance-checklist.md)。

## 主要接口

- `/api/accounts`、`/api/projects`：账号和项目看板数据
- `/api/projects/{id}/tasks`、`/api/tasks/{id}/answer`、`/api/tasks/{id}/cancel`：创建、恢复和取消 Codex 任务
- `/api/tasks/{id}/events`：WebSocket 实时事件，支持 `after` 序号断线重放
- `/api/tasks/{id}/result`、`/api/tasks/{id}/semantic-events`：任务结果、正式资产和面向用户的中文进度
- `/api/tasks/{id}/retry-registration`：只重试已有明文草稿的剪映登记
- `/api/assets/{id}/directory-manifest`：读取已登记剪映资产的规范目录路径和相对文件清单
- `/api/assets/{id}/open-directory`：仅允许控制台所在电脑上的浏览器直接通过 `localhost`/回环地址请求打开该资产目录；请求只传资产 ID，不接受客户端路径。此桌面动作不支持反向代理或隧道访问，代理到本机也会被拒绝
- `/api/chat/sessions`、`/api/chat/sessions/{id}`：新建、列出、读取或删除控制台对话
- `/api/chat/sessions/{id}/messages`、`/api/chat/sessions/{id}/fork`：实时补充消息或从当前对话复制一份
- `/api/codex/history`：读取本机桌面版、CLI 和任务历史；支持 `source` 来源筛选与 `limit` 条数限制
- `/api/codex/history/{id}`、`/api/codex/history/{id}/resume`、`/api/codex/history/{id}/fork`：查看、继续原会话或复制到控制台
- `/api/library/materials/search`、`/api/library/materials/bundle`：爆款库只读代理
- `/api/dependencies`：Codex、爆款库、MCP 与 Obsidian 状态
- `/api/settings`：读取或修改本机配置，包括 1—4 的 Codex 并发限制、默认模型与推理强度、App Server 开关、历史显示条数、工作目录白名单，以及爆款库、Obsidian、媒体、剪映和机器配置路径；密钥只接受更新，不会明文回显。响应中的 `public`/`configured_public` 是已保存配置，`active_public` 是当前进程正在使用的配置；`restart_required=true` 表示路径、App Server 或密钥等启动配置需重启后生效，并发和历史数量等热更新项不受影响

生产验证使用 fake Codex 和 httptest，不打开微信视频号。

## Workflow V2 prerequisites

- Codex CLI must resolve from `codex --version`.
- Installed Skills live under `C:\Users\prepare\.codex\skills`.
- `baokuan` MCP must appear enabled in `codex mcp list` and its HTTP service must answer at `http://127.0.0.1:2022`.
- The Obsidian vault and topic-card directory are configured in the console Settings page.
- The console listens on `0.0.0.0:2030` only with administrator authentication enabled.
- Automated verification does not open WeChat Channels or Jianying.
