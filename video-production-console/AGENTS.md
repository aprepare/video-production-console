# 视频生产项目记录

## 控制台功能范围

- 2026-09-30 用户要求移除文档柜和 AI短片，已删除专属前后端模块、脚本、画风预览与 AI短片独立数据配置。仅保留旧页面回首页及旧 API 返回 HTTP 410 的兼容处理，不恢复入口。
- 用户明确保留 `二创工作区/` 中的原稿、成稿、规则和参考资料。文案与混剪、图文制作、共享账号数据库及下述独立视频生产工程不属于本次删除范围。
- 2026-09-30 用户选择前端「视觉与操作一起优化」：全站使用 App.css 语义颜色，导航和共用控件适配深色主题；制作步骤为紧凑列表，提示词库位于工作流工具栏；图文项目库支持本地标题搜索、状态筛选及刷新。设计记录在 `docs/superpowers/specs/2026-09-30-frontend-studio-design.md`，验收在 `artifacts/frontend-studio-20260930/`。
- 2026-10-01 用户提供完整新版系统二创提示词，原文存于 `artifacts/remix-prompt-update-20261001/requested-prompt.txt`。已采用到提示词库 `bone_flesh`、全局当前提示词、全局工作流及 7 个已有账号工作流。仅重构中段，保留原文开头和末段（仅允许等值数字中文化），受众为 25—40 岁普通打工人，篇幅为原文的 90%—110%；旧公共课程与发布字段规则设为空，避免覆盖用户要求。保留模型、工作流结构和制作参数。控制台既有 JSON 存储契约保留，正文不含分析或制作说明。实际配置和无网络模拟请求验收记录在同目录；这些平台检测参数是用户提供的模板内容，未作事实核验或通过检测的保证。

## 本机运行配置

- 2026-10-01 修复混剪 `registration_coordinator_unavailable`：剪映 `C:/Users/prepare/AppData/Local/JianyingPro/User Data` 为指向 D 盘的目录联接，注册组件要求真实路径。系统设置及 `video-console-data/machine-profiles/default.json` 的 `jianying_root` 统一为 `D:/JianyingPro/AppData/User Data/Projects/com.lveditor.draft`；勿改回 C 盘联接入口。重启后运行时、22 个 Python 文件指纹及注册接口均已验证，记录与配置备份在 `artifacts/montage-runtime-repair-20261001/`。该次失败任务在执行前停止，未自动重新制作或写入剪映草稿。

## 账号风格与当前工程

- 账号视觉选择以 `docs/account-visual-styles.md` 为准。2026-09-29用户已确认：认知漫步=A暖纸手账，居中观=B黑白报刊拼贴，财富漫游=C浅绿微缩沙盘，财经漫游=第一支深蓝金色图文动效。不同账号不自动套用相同模板。候选样片中的品牌名须在其他账号正式制作时替换。
- 当前认知漫步完整工程：`artifacts/finance-second-20260929/production/journal-full/`；制作状态以该目录的BRIEF、production-plan、REVIEW、DELIVERY及registered-draft记录为准。
- 观局思考已确认 G1 朱砂水墨，参考 `artifacts/guanju-sikao-style-study-20260929/G1_朱砂水墨_分镜预览.png`。独立工程 `artifacts/guanju-sikao-20260929/`：用户已批准 `approved-script.txt`（Claude revision-02，1456字），原网页Aura新配音 `aurastd/观局思考_原网页音色.wav`。完整47分镜、168条字幕、约4分51秒草稿已注册为 `观局思考_朱砂水墨_完整视频_0929`，状态以 `production/registered-draft.json`、`verification.json` 和 `REVIEW.md` 为准。勿将其他账号文案、配音、素材替换进来。
- 认知漫步文案是 `artifacts/finance-second-20260929/approved-script.txt`，配音是同目录 `aurastd/第二篇_原网页音色.wav`，已获用户批准。沿用原网页Aura配音，勿改成Fish或重新写稿。
- 用户要求图文/代码动效与少量新生成视频混合，不做全AI视频。新素材每片仅用一次，不复用其他账号或弃用版本的图片视频；已批准样片作为同片开头可以原样保留。
- 口播显示字幕不加逗号句号，必要数字小数点保留。按原声时间对齐。用户明确本次不需要财经数据核查。
- 第一支已发布视频由用户自行修改，禁止覆盖 `artifacts/remix-comparison-20260929/visual-preview/` 成品及其已注册草稿。当前新片始终用独立草稿名。
- 旧版暖色全视频及revision-02深蓝样片已被用户否定，不得误认作当前认知漫步已批准方案。
- 生成后实看素材，检查结构、运动、黑帧、字幕和分镜接缝；通过明文验证后使用剪映注册锁新建草稿，不默认打开剪映UI。
- 最新交付偏好：用户明确后续只需要剪映草稿。最终只提供草稿名称及位置，不额外交付或展示MP4。制作与验片所需的内部渲染文件可保留。
- 观局思考第一分镜已按用户反馈重做：当前最新草稿为 `观局思考_朱砂水墨_首镜重做版_0929`，记录在 `artifacts/guanju-sikao-20260929/production/opening-v2/`。基于用户剪映保存版本保留其167条字幕及后续剪辑，仅替换前176帧；勿用最初168条字幕工程覆盖当前修改。
- 观局思考第二支数据资产视频：`artifacts/data-assets-remix-20260929/`。用户已批准2573字Claude稿，原网页Aura配音；G1朱砂水墨，80分镜、298条字幕、535.83秒。新草稿 `观局思考_数据资产_朱砂水墨_0929`，含独立BGM。封面与发布文案在 `publish/`；状态以 `production/registered-draft.json`、`render-verification.json`、`REVIEW.md` 和 `DELIVERY.md` 为准。未覆盖上一支首镜重做版，未发布。
