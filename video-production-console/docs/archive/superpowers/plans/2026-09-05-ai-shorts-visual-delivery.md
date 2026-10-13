# AI短片财经分镜与草稿升级

**Goal:** 在现有财经解说入口中完成语义分镜、原生竖图、可审阅关键词和可编辑剪映交付。

**Architecture:** 扩展现有 Short/Shot JSON；新项目默认全幅9:16，旧项目保留横图内嵌。复用整篇TTS和逐字时间轴，画面、字幕和短标注分别入轨。图片使用本地关键帧运动，不新增付费图生视频步骤。

**Tech Stack:** Go、React/TypeScript、Python、pyJianYingDraft。

## 范围与验收

- 文案按完整含义拆镜，保留对象、变化/动作与画面意图；去掉固定意象词典和每镜固定秒数说明。
- Shot可保存原句、意图、主体类型、关键词分类、镜头运动、短标注，预览实际生图提示词。
- 图片尺寸与布局同时变更：全幅9:16 / 原有横图底板。新图独立版本文件，旧草稿引用素材不覆盖。
- 字幕保留小数、金额、年份及课程名，关键词必须来自旁白；字幕内分色，不重复盖一层错位文字。
- 用户可选字幕位置、字号、关键词开关、运动强度；修改包装不用重新生图。
- 图片按实际TTS区间铺满，完整字幕与短标注独立可编辑；重组装保留旧剪映草稿。
- 保留已有未提交改动；不自动运行用户的付费模型、配音或生产项目。

## 实施顺序

- [x] Go字段、设置、分镜提示词、单镜编辑与请求尺寸；单测覆盖新旧JSON、提示词、关键词与小数。
- [x] Python全幅、静止/推拉/横移、局部淡入、字幕分色、标注轨；用临时素材真实构建草稿，注册测试仅使用临时模拟根目录。
- [x] 前端设置、分镜编辑、标注预览、提示词/关键词说明；组件测试、类型与构建检查。
- [ ] 集成复核、构建本机程序，检查运行任务后安全更新；只读验收页面与健康状态。

## 验证记录

- `go test ./internal/aishorts -count=1` 通过；覆盖数值完整性、关键词归属、横竖画幅请求、新旧项目迁移、编辑互斥、图片/配音版本与并发缓存复用。
- `go test ./internal/httpapi -count=1` 通过。
- `npm --prefix web run test -- src/ai-shorts/AiShortsPage.test.tsx`：9项通过。
- `python -m unittest discover -s scripts/ai-shorts -p test_visual_draft.py`：5项通过，使用实际安装的pyJianYingDraft导出JSON并核对可编辑轨道、关键帧、富文本范围、时间轴及旧草稿保留。
- `npm --prefix web run build:embed` 和 `go build -o dist/video-production-console.next.exe ./cmd/console` 成功。
- Playwright隔离预览验证：单镜运动/标注/关键词保存请求正确；390px视口无横向溢出。截图位于`artifacts/ai-shorts-visual-desktop.png`和`artifacts/ai-shorts-visual-mobile.png`。
- 服务切换前检查：AI短片0项；数据库任务、生图和二创运行表没有运行中任务。自动审批拦截了停止旧服务、替换程序并重启的命令，返回`blocked by policy`，未执行服务切换；已向用户请求本次重启许可。

## 交付

2026-09-05补全生图模型入口：AI短片新建与编辑表单支持项目级`image_model`，留空显示并使用真实后台默认；创建/PATCH/存储及角色图、分镜图请求完整透传，模型更改保留已有素材，不要求重拆分镜。AI短片Go测试、11项前端组件测试、定向HTTP测试、前端类型/嵌入构建及Go程序编译通过；独立复核未发现缺陷。HTTP全套曾因无关`TestRemixLabDeleteExperimentHTTP`临时目录清理竞态失败，该项定向重跑通过；不计为本次全套通过。最新可执行文件仍在`dist/video-production-console.next.exe`，需手动重启入口启用。

使用与参数说明：`docs/ai-short-finance-visual-guide.md`。旧源码、嵌入前端和可执行文件备份：`artifacts/ai-shorts-before-visual-upgrade-20260905/`。

2026-09-05用户明确要求重新编译重启后，前端与Go程序再次构建成功。自动执行重启仍被环境以`blocked by policy`拒绝，旧进程31032继续提供健康服务；不再重复请求授权。已提供手动入口`scripts/restart-compiled-console.cmd`，对应PowerShell脚本通过语法检查，包含目标程序核验、备份、切换、健康检查及失败回滚；未自动执行该脚本。新版程序SHA256：`14B144B19D026D4261148FB4158779D54652F72D2FA176B2C4A229EB661C8359`。

随后用户手动运行重启入口，在替换EXE时遇到文件占用，脚本回滚到旧版。已修复等待进程退出、文件占用限时重试、复制后哈希校验、回滚错误提示及目标文件缺失时恢复；6项离线检查通过，未停止或启动真实服务。复核端口2030由PID63684提供服务，健康接口返回200/ok；运行文件哈希仍与新版不同，新版启用继续待手动重试验证。
