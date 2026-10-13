# Frontend Studio Implementation Plan

> 在当前已授权工作树直接执行，以逐项验证完成本次优化；不增加审批或提交其他人的改动。

**Goal:** 统一全站视觉并提高文案与图文操作效率。

**Architecture:** 共用设计色与控件规范在 App.css 维护；页面布局在各自 CSS 中维护。图文项目库拆成独立组件，保留 ImageModeWorkbench 的数据和生产行为。

**Tech Stack:** React 19、TypeScript、CSS、Lucide、Vitest、现有 Playwright 测试。

**Spec:** `docs/superpowers/specs/2026-09-30-frontend-studio-design.md`

## 约束

保留已有文案、账号、制作工程及后端接口；不恢复已删除功能。保留用户未提交改动。无新增包、网络字体或生图资源。

- [x] 保存待改源码快照。整理 App.css 全局颜色、文字、控件、弹窗及登录样式；更新 shell/console-navigation.css 导航。
- [x] 在 remix-lab/remix-lab.css、fixed-flow-steps.css、project-workbench/project-workbench.css 整理标题、历史、步骤与详情布局；改善历史空状态和展开语义。
- [x] 添加 ImageProjectLibrary.tsx 和 UI 测试，验证搜索、状态筛选、清除与打开项目。更新 ImageModeWorkbench、QuickGenerateForm 和 image-mode.css，移除重复标题并增加字符数。
- [x] 统一 settings-panel.css 与账号弹窗的控件和深色颜色；检查键盘和手机布局。
- [x] 执行相关前端测试、类型检查、构建和独立浏览器验收；记录截图与必要修复。
- [x] 编译最终嵌入资源的 Go EXE，更新并重启服务，核对 HTTP 健康与前端资源，记录交付。

