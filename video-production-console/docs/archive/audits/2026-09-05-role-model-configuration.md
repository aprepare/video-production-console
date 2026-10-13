# 三角色模型配置修正

用户指出原“模型配置”显示模型槽，实际需求为二创策划、写手、审稿三个角色。本次将主入口接到当前账号工作流，保留旧写手预设的次级入口。

- 新增 RoleModelsDialog 和共享 RoleModelFields，三个角色各自选择模型、推理强度、Fast。
- 通过原有工作流保存接口合并对应节点配置，保留提示词、节点连接、生产设置；不修改历史实验快照。
- 原 Base URL/API Key/多写手预设移到“连接与旧版预设”，数据不迁移、不删除。
- 重跑接口添加可选 planner，兼容旧请求；重跑配置读取当前账号保存的角色模型。临时修改仅进入新运行快照，旧稿保持。
- 写手 Fast 不传播到未设置 Fast 的策划或审稿。默认模型按钮恢复该角色打开弹窗时的配置。

## 验证

- 前端四个相关测试文件：43 项通过，4 项既有跳过。
- 默认模型按钮修正后，再次运行 RoleModels.test.tsx 与 Rerun.test.tsx：3 项通过。
- go test ./internal/remixlab -count=1：通过。
- go test ./internal/httpapi -count=1：首次触发既有 TestRemixLabDeleteExperimentHTTP 异步写入与临时目录清理竞争；独立重跑整个包通过，未修改该测试或相关生产逻辑。
- 实际运行层模拟客户端回归 TestPlannerRuntimeDelivers：通过，分别核对策划、写手、审稿请求的 model、reasoning_effort、service_tier。没有真实模型调用。
- go vet ./internal/remixlab ./internal/httpapi、TypeScript 检查、前端嵌入构建、Go 构建通过。
- 独立代码审查无必须返工项；本轮相关 git diff --check 通过，仅既有换行格式提示。
- 浏览器确认模型配置有三张角色卡；当前账号标签正确，旧项目仍可打开；重跑弹窗也已显示二创策划、写手、审稿三个角色。
- HTTP 根页及当前 JS/CSS 资源均返回 200。

## 部署与数据

- 当前本地服务 PID：55900；端口 2030。
- dist/video-production-console.exe 与 dist/video-production-console.role-models.exe SHA-256：4D87979456B704DC351E0E342A72F763ABFA5AF6CDD0B1450415A51A56D13117。
- JS：index-BEmnDXty.js；CSS：index-BHrE9pee.css。
- 原程序已备份为 dist/video-production-console.before-role-models-20260905-122125.exe。
- 部署前基线：2026-09-05-role-models-history-before.json。27 条运行、26 条实验、0 条独立运行快照，部署后内容哈希一致。新增的第 27 条运行在本次部署前已存在，本次没有创建或修改业务稿件。
- 配置保存与重跑生成通过隔离测试验证；浏览器验收只读取配置，没有替用户选定新模型或发起付费生成。

刷新页面后，在“模型配置”分别设置三个角色并保存；同项目“重新生成文案”也可临时改三个角色。
