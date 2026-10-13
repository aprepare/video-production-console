# Console UI Implementation Plan

**Goal:** 统一整套控制台视觉与导航，修复影响内容生产的操作状态和数据保留问题。
**Architecture:** 保留现有路由、API、业务状态机与弹层层级，增加统一导航和设计基础；各业务模块在独立文件边界内重构。
**Tech Stack:** React 19、TypeScript、现有 Lucide、CSS、React Flow、Vitest。
**Spec:** `docs/superpowers/specs/2026-09-05-console-ui-design.md`

- [x] 主代理：App 展示外壳与 ConsoleNavigation、全局 token/样式、任务提交状态、导航测试；不修改工作流API。
- [x] 文案工作区：remix-lab 全模块及测试。页头收纳、工作流/详情清晰布局、草稿按账号恢复、先保存再生产、提交锁、导入重试和文案库状态。
- [x] 制作工作区：image-mode、ai-shorts 页面及样式测试。统一内容层级、表单进度、空错态、操作反馈，移除重复全局导航，保留业务行为。
- [x] 辅助工作区：project-workbench、settings、accounts、assets、tasks 模块及各自样式测试。项目下一步、设置分组、弹层可访问性与响应式，任务pending props由主代理接线。
- [x] 集成：对照逐文件差异，typecheck/lint，业务回归与前端全测，修复真实失败。
- [x] 视觉验证：在浏览器查看主要页面、1440/768/390尺寸、暗色、减弱动效；空错态和防重复用假API测试。
- [x] 交付：build:embed，嵌入资源验证，Go构建；保留可回滚二进制，安全更新实际入口，记录验证与截图。

提交状态、未保存草稿、生产顺序等行为变更先写失败用例再修复。纯布局与配色不写镜像CSS的单测。所有工作者不得回退别人的改动；通用设计token只由主代理维护。

验收记录：`docs/audits/2026-09-05-console-ui.md`。24个测试文件，261项通过，4项既有跳过。已更新原2030端口实际运行程序并保留旧exe备份。
