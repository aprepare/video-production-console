# 进化台接入补丁（剩余接线）

已入库：`internal/remixlab/*`、`httpapi/remix_lab.go`、`openaicompat/export.go`、前端 `RemixLabPage.tsx`。

## 仍需改动

### 1. openaicompat/run.go

Options 增加 `SystemPrompt` / `UserPrompt`。
manifestLite.NonSecretSettings 增加 `remix_system_prompt` / `remix_user_prompt`。
Run 中优先用覆盖，否则 `buildWriter*`。
自定义 user 若含 `{{SOURCE}}`/`{{NOTES}}` 则替换。

### 2. codex ManifestSettings

增加 `RemixSystemPrompt` / `RemixUserPrompt` 并写入 JSON。

### 3. task_manifest.go

remix 任务准备时：
```go
active, ok, _ := remixlab.Store{DataRoot: runtime.DataRoot}.GetActive()
if ok {
  settings.RemixSystemPrompt = active.System
  settings.RemixUserPrompt = active.User
  // style 可同步 active.Style
}
```

### 4. app.go

```go
mux.Handle("/api/remix-lab/", httpapi.NewRemixLabHandler(options.Config.DataRoot))
```

### 5. 前端挂载

`App.tsx` / `ConsoleHome` 增加「进化台」入口，渲染 `RemixLabPage`。

CSS：`.remix-lab-layout { display:grid; grid-template-columns: 280px 1fr; gap: 1rem; }` 等。
