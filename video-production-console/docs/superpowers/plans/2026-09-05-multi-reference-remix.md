# 多模型参考稿与生产提示词更新 Implementation Plan

**Goal:** 用户自行选择参考模型数量（包括2、3个），各模型独立产生完整二创稿、标题、视频描述与开头/中段/课尾说明；写手可借鉴语句，所有参考版本在改稿区可查。

**Architecture:** 复用 workflow 的并行 agent，在 config.role 标记 reference；每次生成先持久化独立版本，再将完整候选汇入写手。角色模型窗口和重跑窗口管理可增删列表，阶段 API 返回参考版本供定稿和流程页展示。生产写手、参考写手、审稿共享同一份文字规则，发布协议分别定义。

**Tech Stack:** Go、JSON 文件、SQLite 现有运行快照、React/TypeScript/Vitest。

**Spec:** 本对话用户批准的可变数量多模型参考流程及 finance-viral-remix v3.8。

## Constraints

- 现有工作区改动保留，先备份涉及文件；模型/推理强度/Fast/凭证不在提示词同步中重置。
- 原文直接提供给所有参考模型及写手；参考模型相互独立。参考意见可舍弃，语句可借鉴，事实不按多数表决。
- 新稿、重试、定稿修改保留已生成参考版本。历史运行快照不迁移。
- 用户选择的模型沿用现有写手API连接；0个参考模型代表直接写作，最多6个agent。
- 不自动发起真实付费生成，不启动配音混剪；本次交付功能与更新配置。

## Tasks

- [x] 1. 保存基线，新增并行参考稿保留/失败隔离、角色列表与展示行为测试，运行观察失败。
  - `internal/agentruntime/openaicompat/reference_drafts_test.go`: 两路屏障确认并发及独立输入，长文末尾与标题描述进入写手，重试增加不可变版本；坏响应不注入但可查。
  - `web/src/remix-lab/ReferenceModels.test.tsx`: 添加到3路再移除到2路，保存重开仍正确，主写手与审稿配置保留。
- [x] 2. 更新 `editorial_policy.go` 与残留 `prompts.go` 课尾规则；新增参考稿提示词，保留生产发布包兼容格式。
- [x] 3. 新增 `reference_drafts.go` 解析/存档/读取，`flowrun.go` 参考角色落盘后汇总，`stages.go` 返回完整版本；`workflow.go` 与 `rerun.go` 验证配置和数量。
- [x] 4. 新增可复用参考模型列表和参考稿查看组件；接入角色配置、重跑、定稿编辑、运行流程；文案/标题/描述可复制，失败与版本清晰可见。
- [x] 5. 覆盖存取、历史保留、并发/独立输入、失败/非法结构、空列表直写、模型设置与重跑，执行相关 Go 包和前端测试、类型检查。
- [x] 6. 扩展政策导出与同步，dry-run核对后备份应用到当前配置。构建前后端，确认没有进行中任务后更新本地服务，检查健康及浏览器实际界面。

## Verification

`go test ./internal/agentruntime/openaicompat ./internal/remixlab ./internal/httpapi`

`python -m unittest scripts/test_sync_editorial_policy.py`

`npm --prefix web test -- ReferenceModels ReferenceDrafts RoleModels Rerun FixedRunFlow`

`npm --prefix web run build:embed`，`go build -o dist/video-production-console-next.exe ./cmd/console`

最终记录真实执行结果；模拟接口测试证明编排与保存，不声称已经验证真实模型文案质量。

完成记录：`docs/audits/2026-09-05-multi-reference-delivery.md`。
