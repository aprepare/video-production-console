---
name: Video Production Console
version: 2
colors:
  navigation: "#14243b"
  canvas: "#f4f7fb"
  surface: "#ffffff"
  surfaceMuted: "#edf2f7"
  text: "#203148"
  textMuted: "#61738a"
  border: "#dfe7f1"
  accent: "#087fa4"
  warning: "#b47418"
  danger: "#b44747"
  info: "#4d7896"
typography:
  family: '"Segoe UI", "PingFang SC", "Microsoft YaHei", system-ui, sans-serif'
  display: '"Segoe UI Variable Display", "Microsoft YaHei UI", sans-serif'
  mono: '"Cascadia Code", Consolas, monospace'
  bodySize: 14px
  bodyLineHeight: 1.6
spacing: [4, 8, 12, 16, 24, 32, 48]
radius:
  small: 10px
  medium: 12px
  large: 16px
breakpoints:
  mobile: 640px
  tablet: 900px
  desktop: 1200px
layers:
  workbenchAction: 20
  notice: 30
  modalBackdrop: 40
  modalDialog: 41
  toast: 60
---

# 视频生产控制台设计系统

## 定位

专业、本地优先的视频生产工具。深蓝灰固定导航、明亮工作区、青蓝主操作与当前阶段。全局导航统一为文案与混剪、图文制作、AI短片；设置、主题与退出只在导航中出现。页面呈现当前状态、已有产物和下一步，不将生成草稿表述为已发布。

## 规则

- 颜色、间距、圆角、阴影和层级必须来自 token，不在组件内新增无语义硬编码。
- 每个视图只有一个明确主动作，状态不能只依赖颜色表达。
- 正文与交互文字满足 WCAG AA；键盘焦点始终可见。
- 表单必须有持久 `<label>`；placeholder 仅作示例。
- 普通通知使用 `role="status"`，阻断性错误使用 `role="alert"`。
- 触控目标最小 44×44px；动效短且支持 `prefers-reduced-motion`。
- 工作台通过 `--workbench-*` 语义别名消费全局 token，不维护第二套独立色板。

## 响应式

- 全局导航：桌面208px，761–1180px收至80px，760px以下改为112px高的顶部导航；内容按照剩余可用空间布局。
- 动效150–220ms，用于交互反馈与状态切换，遵循减弱动态效果偏好。

- `< 640px`：移动单栏，固定主动作需预留 safe-area 和正文底部空间。
- `640–899px`：单主栏，辅助信息折叠或进入抽屉。
- `900–1199px`：两栏工作区，不强制三栏压缩。
- `>= 1200px`：完整桌面布局，可使用三栏工作台。
- 关键页面在 320px 宽度和 200% 缩放下不得水平溢出。
