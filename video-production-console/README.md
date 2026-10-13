# 极影智造 · 视频生产控制台

给本地商户做短视频内容的工作台：**找爆款 → 写文案 → 配音 → 生成剪映草稿 → 记账交付**，一台 Windows 电脑上跑完。

第一次打开不知道从哪下手，点左侧导航底部的 **「新手引导」**，按步骤走一遍就会。

## 五个工作区

| 工作区 | 做什么 |
|---|---|
| 商户与订单 | 登记商户、上传授权素材、建订单、记成本、标记交付；「AIGC 重塑」把实拍图换环境再生成视频 |
| 对标采集 | 粘贴抖音/小红书/视频号链接或搜关键词：原视频存到本机能播能下，口播自动转写，可做「拆解分析」 |
| 文案与混剪 | 把口播二创成新文案（可选写法 + 填商户简报），确认后自动配音、生成剪映草稿 |
| 图文制作 | 做图文笔记与配图 |
| 配置体检 | 看哪些配置没好、怎么修；粘贴配置码、自动检测剪映 |

## 启动

```powershell
.\dist\video-production-console.exe     # 浏览器打开 http://127.0.0.1:2030
# 开发：go run .\cmd\console
```

首次启动会建管理员，口令取环境变量 `VIDEO_CONSOLE_INITIAL_PASSWORD`，没设就是 `123456`，登录后到设置里改掉。必须从本目录启动，`scripts/image-video-draft` 才能解析。

改了前端要进 exe：`npm --prefix web run build:embed`，再 `go build -o dist\video-production-console.exe .\cmd\console`。重启用 PowerShell 7 运行 `scripts\restart-compiled-console.ps1`。

## 目录

| 路径 | 是什么 | 发给别人时 |
|---|---|---|
| `cmd/`、`internal/`、`web/`、`schemas/`、`tools/`、`scripts/` | 代码 | 发 |
| `docs/项目说明.md` | 唯一的详细说明：怎么用、代码地图、排障、红线 | 发 |
| `docs/archive/`、`scripts/archive/` | 历史方案、验收记录、已完成的一次性脚本 | 可不发 |
| `二创工作区/` | 财经账号的二创规则、成稿与参考资料 | 按需 |
| `video-console-data/` | 运行数据：账号、项目、密钥、内置 Python 与 ffmpeg | **不发**（含密钥） |
| `artifacts/` | 成片工程与备份；部分目录被已注册的剪映草稿引用，别删 | 不发 |
| `dist/`、`release/` | 编译出的程序与旧安装包 | 不发 |

发给别人看代码，用 `git archive` 打包即可，只会带上受版本管理的文件，不会带数据和密钥：

```powershell
git archive --format=zip -o 极影智造-源码.zip HEAD
# 不带财经样片（压缩后约 5MB，带上约 76MB）：
git archive --format=zip -o 极影智造-源码.zip HEAD -- . ":(exclude)二创工作区/参考库/画面测试"
```

界面配色与字体见 [DESIGN.md](DESIGN.md)。
