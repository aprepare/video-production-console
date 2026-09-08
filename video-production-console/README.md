# 视频生产控制台

本地视频生产控制台：当前开放风景混剪、图文制作（ZIP）。电影混剪、图文视频已屏蔽，后续再开。

**说明只这一份：[docs/项目说明.md](docs/项目说明.md)**（怎么用、代码地图、红线、未提交统计）。界面 token 见 [DESIGN.md](DESIGN.md)。

```powershell
# 仅库里还没有管理员时：
$env:VIDEO_CONSOLE_INITIAL_PASSWORD = "你的初始口令"

.\dist\video-production-console.exe
# 开发：go run .\cmd\console
```

浏览器打开 `http://127.0.0.1:2030`。必须从仓库根目录启动，以便 `scripts/image-video-draft` 能解析。改前端后要进 exe：`npm --prefix web run build:embed`，再 `go build`。

本目录是开发线。发给合作伙伴的桌面 0.1.15 在另一条分支 `feat/partner-exe-rollout`，不要混。

二创规则、成稿和参考库在 `二创工作区/`，改错文件可从 GitHub 历史还原，不要覆盖整个目录。
