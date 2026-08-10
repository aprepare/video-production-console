---
name: Video Production Console
version: 1
colors:
  canvas: "#f3f6f8"
  surface: "#ffffff"
  surfaceMuted: "#eaf0f4"
  text: "#17283a"
  textMuted: "#647587"
  border: "#d7e0e7"
  accent: "#147d69"
  warning: "#b47418"
  danger: "#b44747"
  info: "#4d7896"
typography:
  family: 'Inter, "SF Pro Text", "Segoe UI", "Microsoft YaHei", system-ui, sans-serif'
  bodySize: 14px
  bodyLineHeight: 1.6
spacing: [4, 8, 12, 16, 24, 32, 48]
radius:
  small: 10px
  medium: 14px
  large: 20px
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

专业、本地优先的视频生产工具。使用冷灰画布、白色内容面和深蓝灰文字，青绿色只用于主动作与完成状态；界面应克制、清晰，避免大面积深色压迫、装饰性渐变抢占内容层级和卡片无限嵌套。

## 规则

- 颜色、间距、圆角、阴影和层级必须来自 token，不在组件内新增无语义硬编码。
- 每个视图只有一个明确主动作，状态不能只依赖颜色表达。
- 正文与交互文字满足 WCAG AA；键盘焦点始终可见。
- 表单必须有持久 `<label>`；placeholder 仅作示例。
- 普通通知使用 `role="status"`，阻断性错误使用 `role="alert"`。
- 触控目标最小 44×44px；动效短且支持 `prefers-reduced-motion`。
- 工作台通过 `--workbench-*` 语义别名消费全局 token，不维护第二套独立色板。

## 响应式

- `< 640px`：移动单栏，固定主动作需预留 safe-area 和正文底部空间。
- `640–899px`：单主栏，辅助信息折叠或进入抽屉。
- `900–1199px`：两栏工作区，不强制三栏压缩。
- `>= 1200px`：完整桌面布局，可使用三栏工作台。
- 关键页面在 320px 宽度和 200% 缩放下不得水平溢出。
