# 前端开发说明

前端使用 React、TypeScript、Vite、Vitest 和 Oxlint，源码位于 `web/src`。开发服务器仅用于 HMR；生产页面由 Go 从 `internal/webui/dist` 嵌入并提供。

## 安装与开发

```powershell
npm install
npm run dev
```

## 日常验证

```powershell
npm run typecheck
npm run lint
npm run test
npm run build:verify
```

`build:verify` 输出到仓库根目录 `.tmp/web-dist`，不会修改生产嵌入资源。

## 正式嵌入构建

```powershell
npm run build:embed
```

`build:embed` 会清空并重建 `internal/webui/dist`。仅在明确同步生产前端时运行；完成后必须检查 `index.html` 引用的哈希资源是否存在并纳入版本控制：

```powershell
Set-Location ..
.\scripts\check-embedded-dist.ps1 -Strict
```

`npm run build` 为兼容入口，等同于 `npm run build:embed`，不应用作普通只读验证。
