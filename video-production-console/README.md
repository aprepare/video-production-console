# 视频生产控制台

本地统一视频项目看板，默认监听 `127.0.0.1:2030`，共享现有爆款库 `127.0.0.1:2022`。控制台不会启动、停止或修改爆款库主程序。

## 开发

```powershell
go test ./...
go vet ./...
Set-Location web
npm run build
Set-Location ..
go build -o dist/video-production-console.exe ./cmd/console
```

启动后访问 `http://127.0.0.1:2030`。账号只有名称和固定背景图；项目资产与 Codex 任务按项目隔离。网页顶部可调整 Codex 并发数，范围为 1—4。

## 主要接口

- `/api/accounts`、`/api/projects`：账号和项目看板数据
- `/api/projects/{id}/tasks`、`/api/tasks/{id}/answer`、`/api/tasks/{id}/cancel`：创建、恢复和取消 Codex 任务
- `/api/tasks/{id}/events`：WebSocket 实时事件，支持 `after` 序号断线重放
- `/api/library/materials/search`、`/api/library/materials/bundle`：爆款库只读代理
- `/api/dependencies`：Codex、爆款库、MCP 与 Obsidian 状态
- `/api/settings`：读取或修改并发限制

生产验证使用 fake Codex 和 httptest，不打开微信视频号。
