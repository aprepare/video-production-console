package mcpserver

// APICatalog 是 console_api_catalog 返回的接口清单（方法 路径 | 说明 | 标记）。
// 标记：$ 花钱或调外部模型/服务；! 删除或不可逆；↑ 需要上传文件（用专用工具或不支持）。
// 新增接口时在这里补一行；mcpserver 的测试会核对清单与实际注册的路由一致。
const APICatalog = `# 账号
GET /api/accounts | 列出账号
POST /api/accounts | 新建账号（multipart：name + background 图片；用 create_account） | ↑
PATCH /api/accounts/{id} | 账号改名 {name}
PUT /api/accounts/{id}/overrides | 账号级音色与混剪样式覆盖（AccountOverrides；{} 清除）
POST /api/accounts/{id}/background | 更换账号背景图（multipart background；用 replace_account_background） | ↑
DELETE /api/accounts/{id} | 停用账号（软删除） | !
# 商户、订单、素材
GET /api/business/overview | 经营总览
GET /api/merchants | 商户列表 ?status&q
POST /api/merchants | 新建商户 {name, category, contact, phone, address, selling_points, notes, account_id, status}
GET /api/merchants/{id} | 商户详情
PATCH /api/merchants/{id} | 修改商户
DELETE /api/merchants/{id} | 删除商户及其全部素材文件 | !
GET /api/merchants/{id}/materials | 商户素材列表
POST /api/merchants/{id}/materials | 上传素材（multipart file + 授权字段；用 upload_merchant_material） | ↑
PATCH /api/materials/{id} | 修改素材说明与授权 {kind, description, license_source, license_scope, license_expires_at, license_proof}
DELETE /api/materials/{id} | 删除素材及文件 | !
GET /api/materials/{id}/content | 素材文件内容
GET /api/orders | 订单列表 ?merchant_id&status
POST /api/orders | 新建订单 {merchant_id, title, package, price_cents, paid_cents, video_quota, max_duration_s, revision_rounds, due_date, scope_notes, status, renewal_of}
GET /api/orders/{id} | 订单详情（成本、项目、汇总）
PATCH /api/orders/{id} | 修改订单
DELETE /api/orders/{id} | 删除订单 | !
POST /api/orders/{id}/costs | 记成本 {project_id, kind, minutes, amount_cents, note, occurred_at}
DELETE /api/order-costs/{id} | 删除成本记录 | !
POST /api/orders/{id}/projects | 项目挂到订单 {project_id}
DELETE /api/orders/{id}/projects/{projectID} | 解除项目与订单关联 | !
POST /api/orders/{id}/projects/{projectID}/deliver | 标记项目已交付
# 对标采集与拆解
GET /api/references/status | 平台接口连通状态
GET /api/references/search | 抖音/小红书关键词搜索 ?platform&keyword&cursor&search_id&session_id | $
GET /api/references/preview | 搜索结果封面或原视频在线预览 ?platform&kind&url
GET /api/references | 对标库 ?q&platform&merchant_id&status&limit
POST /api/references | 采集对标 {link | post, keyword, merchant_id, transcribe}（transcribe 时付费转写） | $
GET /api/references/{id} | 对标详情（transcript 口播全文）
PATCH /api/references/{id} | 改口播稿/备注/商户 {transcript, notes, merchant_id}
DELETE /api/references/{id} | 删除对标 | !
POST /api/references/{id}/transcribe | 口播转写（通义听悟，异步） | $
GET /api/references/{id}/comments | 拉平台评论 ?limit | $
GET /api/references/{id}/media | 原视频 mp4 ?download=1
POST /api/references/{id}/media | 下载原视频到本机（异步）
GET /api/references/{id}/cover | 对标封面图
# 关键词监控与评论洞察
GET /api/monitors | 监控任务列表
POST /api/monitors | 新建关键词监控 {platform, keyword, name, interval_minutes, pages, …}
GET /api/monitors/{id} | 监控任务详情
PATCH /api/monitors/{id} | 修改监控配置（仅更新已提供字段）
DELETE /api/monitors/{id} | 删除监控任务及历史 | !
POST /api/monitors/{id}/run | 立即排队执行一轮监控 | $
GET /api/monitors/{id}/runs | 监控执行历史 ?limit
GET /api/monitors/feed | 监控信息流 ?task_id&filter&limit
PATCH /api/monitors/items/{item_id} | 标记条目 {read, dismissed}
POST /api/monitors/items/read-all | 全部已读 {task_id}
POST /api/monitors/items/{item_id}/collect | 采进素材库 {transcribe}（转写时付费） | $
POST /api/monitors/items/{item_id}/score | 立即重新打分 | $
POST /api/comment-insights | 评论洞察 {reference_id, model} | $
GET /api/comment-insights | 评论洞察历史 ?reference_id
GET /api/comment-insights/{id} | 评论洞察报告
DELETE /api/comment-insights/{id} | 删除评论洞察 | !
POST /api/reference-analyses | 拆解分析 {reference_id, model, focus, product}（视觉模型，异步；生成逐镜头 AI 复刻提示词） | $
GET /api/reference-analyses | 拆解列表 ?reference_id
GET /api/reference-analyses/{id} | 拆解详情
GET /api/reference-analyses/{id}/frames/{n} | 第 n 张关键帧
DELETE /api/reference-analyses/{id} | 删除拆解 | !
# AI 视频审核
POST /api/video-qc | 创建视频审核 {source, source_id, product, model}（视觉模型，异步；上传来源使用 multipart） | $
GET /api/video-qc | 审核记录列表 ?limit
GET /api/video-qc/{id} | 审核详情（报告与帧列表）
GET /api/video-qc/{id}/frames/{n} | 第 n 张审核帧 JPEG
GET /api/video-qc/{id}/video | 上传来源的原视频（支持 Range）
DELETE /api/video-qc/{id} | 删除审核记录及文件 | !
# 二创：实验与运行
GET /api/remix-lab/defaults | 创作台默认配置与模型预设
PUT /api/remix-lab/presets | 保存模型槽位预设 {slots[]}
GET /api/remix-lab/experiments | 二创实验列表
POST /api/remix-lab/experiments | 旧版多槽位实验 {source, slots[], prompt_ids[]} | $
GET /api/remix-lab/experiments/{id} | 实验详情（runs），用于轮询
DELETE /api/remix-lab/experiments/{id} | 删除实验 | !
PATCH /api/remix-lab/runs/{id} | 给稿写备注 {comment}
POST /api/remix-lab/runs/{id}/adopt | 定稿采纳到项目 {project_id}
POST /api/remix-lab/runs/{id}/rework | 带批注改稿 {annotations} | $
POST /api/remix-lab/runs/{id}/retry | 重试/指定节点重跑 {node_id, model} | $
GET /api/remix-lab/runs/{id}/rerun-options | 可换模型重跑的选项
POST /api/remix-lab/runs/{id}/rerun | 换模型重跑 {writer, reviewer, planner, references} | $
GET /api/remix-lab/runs/{id}/stages | 各节点输入输出与耗时
POST /api/remix-lab/workflow/run | 开始二创 {source, account_id, run_count, model, models, model_configs:[{model,reasoning_effort}], reviewer, auto}；models 兼容旧调用，model_configs 可逐模型指定强度 | $
POST /api/remix-lab/workflow/import-draft | 导入已有成稿 {continuous_script, short_titles, …, account_id}
# 二创：生产
POST /api/remix-lab/runs/{id}/produce | 确认生产（口播→字幕→配音→混剪） {account_id} | $
POST /api/remix-lab/runs/{id}/produce/redo | 单步重做 {step: spoken|captions|narration|montage} | $
GET /api/remix-lab/productions/by-project/{projectID} | 按项目查生产记录
# 二创：发布包与封面
GET /api/remix-lab/runs/{id}/package | 读发布包
PUT /api/remix-lab/runs/{id}/package | 保存定稿与发布包 {continuous_script, short_titles, titles, cover_titles, descriptions, topics, cta, cover_path}
POST /api/remix-lab/runs/{id}/cover | 本地出封面 {line1, line2}
GET /api/remix-lab/runs/{id}/cover | 封面 png
POST /api/remix-lab/runs/{id}/ai-cover | AI 出封面（RunningHub，异步） {line1, line2} | $
GET /api/remix-lab/runs/{id}/ai-cover | AI 封面进度
# 二创：提示词、风格、工作流
GET /api/remix-lab/prompts | 提示词库
POST /api/remix-lab/prompts | 新增/修改提示词 {id, name, description, style, stamp, system, user}
PUT /api/remix-lab/prompts/{id} | 修改提示词
DELETE /api/remix-lab/prompts/{id} | 删除提示词 | !
GET /api/remix-lab/active-prompt | 全局默认写手提示词
PUT /api/remix-lab/active-prompt | 设定全局默认提示词 {id, system, user, clear}
GET /api/remix-lab/agent-prompts | 多 agent 系统提示词
PUT /api/remix-lab/agent-prompts | 保存多 agent 提示词 {hook_system, facts_search_system, facts_offline_system, ammo_system, reviewer_system}
GET /api/remix-lab/styles | 风格模板列表
POST /api/remix-lab/styles | 新增/修改风格 {id, base_id, name, category, description, best_for, planner, writer, writer_user, reviewer, rules, finance, publish}
DELETE /api/remix-lab/styles/{id} | 删除风格（内置不可删） | !
GET /api/remix-lab/workflow | 工作流定义 ?account_id
PUT /api/remix-lab/workflow | 保存工作流定义（Workflow 对象） ?account_id
GET /api/remix-lab/workflow-prompts | 工作流及各节点提示词 ?account_id
PUT /api/remix-lab/workflow-prompts | 保存工作流提示词 {workflow} ?account_id
PUT /api/remix-lab/workflow/style | 把风格套到账号 {style_id, brief} ?account_id
PUT /api/remix-lab/workflow/brief | 更新账号内容简报 {industry, audience, offer, selling_points, conversion, persona, platform, duration_s, banned, notes} ?account_id
# 二创：已发布与 AI 助手
GET /api/remix-lab/published | 已发布文案库
PUT /api/remix-lab/published/{id} | 回填成绩 {views, likes, orders, notes, updated_at}
GET /api/remix-lab/agent-settings | 网页 AI 助手的模型设置
PUT /api/remix-lab/agent-settings | 保存 AI 助手设置 {model, base_url, reasoning_effort, api_key, clear_api_key}
POST /api/remix-lab/agent/chat | 和网页 AI 助手对话 {message, experiment_id} | $
GET /api/remix-lab/agent/last | AI 助手上一轮结果
GET /api/remix-lab/agent/history | AI 助手对话历史
DELETE /api/remix-lab/agent/history | 清空 AI 助手历史 | !
POST /api/remix-lab/agent/confirm | 执行 AI 助手的提案 {proposal_id}
# 项目与资产
POST /api/projects | 新建项目 {account_id, title}
GET /api/projects | 项目列表 ?account_id&stage&q
POST /api/projects/batch-delete | 批量删除项目 {ids[]} | !
GET /api/projects/{id} | 项目详情与资产
DELETE /api/projects/{id} | 删除项目及文件 | !
POST /api/projects/{id}/assets/{type} | 上传项目资产（multipart file） | ↑
POST /api/projects/{id}/topic-card/versions | 上传选题卡新版本（multipart） | ↑
POST /api/projects/{id}/move | 切换阶段 {stage}
POST /api/projects/{id}/remix | 旧流程 Codex 二创 {model, reasoning_effort} | $
POST /api/projects/{id}/publish | 标记已发布
GET /api/projects/{id}/notes/{step} | 读步骤笔记
PUT /api/projects/{id}/notes/{step} | 写步骤笔记 {notes}
POST /api/projects/{id}/narration | 用连续稿合成配音和字幕 {voice_id, speaker_id, model, emotion, speed, volume, pitch, preserve_chinese_years} | $
GET /api/assets/{id}/content | 资产文件内容
GET /api/assets/{id}/directory-manifest | 目录资产文件清单
POST /api/assets/{id}/open-directory | 在本机打开资产目录
POST /api/assets/{id}/export-video | 把剪映草稿导出视频（本地渲染）
GET /api/assets/{id}/export-video | 导出进度
# 任务
POST /api/projects/{id}/tasks | 发起 Codex 任务 {type, action, prompt, …} | $
POST /api/projects/{id}/topic-card | 生成正式选题卡 | $
GET /api/tasks | 任务列表 ?center=1&project_id&status
POST /api/tasks/dismiss | 从任务中心移除已结束任务 {ids[]}
GET /api/tasks/{id} | 任务详情
GET /api/tasks/{id}/timing/summary | 任务用时汇总
GET /api/tasks/{id}/timing/runs | 任务各阶段用时
POST /api/tasks/{id}/answer | 回答任务提问 {answer}
POST /api/tasks/{id}/cancel | 取消任务 | !
GET /api/tasks/{id}/artifacts | 任务产物
GET /api/tasks/{id}/result | 任务结果
GET /api/tasks/{id}/diagnostics | 诊断日志 ?after&limit
GET /api/tasks/{id}/semantic-events | 语义事件 ?after&limit
POST /api/tasks/{id}/retry-completion | 重新处理任务产出
POST /api/tasks/{id}/retry-registration | 重试剪映草稿注册
# 混剪与剪映
GET /api/jianying-drafts | 剪映草稿列表
POST /api/jianying-drafts/extract-style | 从剪映草稿提取混剪样式 {name}
POST /api/jianying-drafts/import-bgm | 从草稿导入 BGM {name}
GET /api/jianying-drafts/health | 剪映兼容性与备份
POST /api/jianying-drafts/backups/prune | 清理草稿备份 {keep_latest, older_than_days, dry_run} | !
GET /api/jianying-drafts/storyboard | 混剪分镜表 ?task_id
GET /api/jianying-drafts/storyboard/frame | 分镜画面 ?task_id&shot
GET /api/jianying-drafts/detect | 探测剪映草稿目录
POST /api/jianying-drafts/detect/apply | 应用剪映草稿目录 {draft_root}
GET /api/bgm-library | BGM 库
POST /api/bgm-library/rescan | 重新扫描 BGM
# 图文、图生视频、AIGC
GET /api/image-studio/models | 可用图片模型
POST /api/image-studio/enhance | 同步扩写生图提示词 | $
POST /api/image-studio/generations | 创建生图任务 | $
GET /api/image-studio/generations | 出图历史 ?limit&before&favorite&q
GET /api/image-studio/generations/{id} | 出图任务详情
PATCH /api/image-studio/generations/{id} | 收藏或取消收藏 {favorite}
DELETE /api/image-studio/generations/{id} | 删除生图记录及图片 | !
POST /api/image-studio/generations/{id}/retry | 重试失败图片 | $
GET /api/image-studio/outputs/{id}/image | 获取或下载单张图片 ?download=1
GET /api/image-studio/generations/{id}/download | 打包下载本次图片
GET /api/image-projects | 图文项目列表
POST /api/image-projects | 新建图文项目 {title, script, ratio, style, concurrency, …} | $
POST /api/image-projects/quick-generate | 一键图文（规划+提示词+出图） | $
POST /api/image-projects/segment-preview | 预览分段与发布候选 | $
GET /api/image-projects/{id} | 图文项目详情
POST /api/image-projects/{id}/resume | 续跑一键图文 | $
PATCH /api/image-projects/{id} | 改标题 {title}
DELETE /api/image-projects/{id} | 删除图文项目 | !
PATCH /api/image-projects/{id}/items/{item} | 改单张 {source_text, title, prompt}
POST /api/image-projects/{id}/generate | 全部出图 | $
POST /api/image-projects/{id}/items/{item}/generate | 单张出图 | $
GET /api/image-projects/{id}/items/{item}/image | 单张图片
GET /api/image-projects/{id}/download | 打包下载
PATCH /api/image-projects/{id}/publishing-candidates/{position} | 改发布候选 {title, description}
POST /api/image-projects/{id}/publishing-candidates/{position}/select | 选定发布候选
POST /api/image-projects/{id}/publishing-candidates/select | 选定发布候选 {position}
POST /api/image-projects/{id}/publishing-candidates/generate | 重新生成发布候选 | $
PATCH /api/image-projects/{id}/output-mode | 切换输出模式 {output_mode}
POST /api/image-projects/{id}/image-video-jobs | 图转视频任务 {output_mode, account_id, idempotency_key} | $
GET /api/image-video-jobs/{id} | 图转视频任务详情
POST /api/image-video-jobs/{id}/retry-failed | 重试失败条目 | $
POST /api/image-video-jobs/{id}/cancel | 取消图转视频 | !
POST /api/image-video-jobs/{id}/retry-registration | 重试剪映注册
GET /api/image-videos | 图文视频列表
POST /api/image-videos | 一键图文视频 {script, title, account_id, …} | $
GET /api/image-videos/{id} | 图文视频详情
POST /api/image-videos/{id}/resume | 续跑 | $
POST /api/aigc/extract-frames | 商户视频抽帧 {merchant_id, material_id, count}
POST /api/aigc/restyle | 图片重绘 {merchant_id, material_ids, prompt, preserve, order_id, unit_cost_cents} | $
POST /api/aigc/image-to-video | 准备图生视频（不扣费） {merchant_id, material_ids, prompt, duration_s, aspect, order_id}
POST /api/aigc/jobs/{id}/confirm | 确认图生视频（RunningHub H3） | $
GET /api/aigc/jobs | AIGC 任务列表 ?merchant_id
GET /api/aigc/jobs/{id} | AIGC 任务详情
GET /api/aigc/lineage/{materialID} | 素材派生关系
# 设置与配置
GET /api/settings | 读设置
PUT /api/settings | 改设置 {public: {…}, secrets: {…}}
POST /api/settings/test/{dependency} | 检测依赖
POST /api/settings/repair/baokuan-mcp | 修复爆款 MCP 配置
GET /api/setup/status | 配置体检
GET /api/setup/models | 可用模型
POST /api/setup/activate | 导入配置码 {code}
POST /api/setup/export-code | 导出配置码 {issued_to, expires_days, include_secrets, keys}
GET /api/skills | 技能列表
POST /api/skills/scan | 重新扫描技能
GET /api/skills/{name} | 技能详情
GET /api/dependencies | 依赖状态
POST /api/dependencies/baokuan-mcp/configure | 配置爆款 MCP
GET /api/library/materials/search | 爆款素材搜索 | $
POST /api/library/materials/bundle | 爆款素材打包 {feed_ids, observation_ids, snippet_ids} | $
# 选题与素材目录
GET /api/ideas | 选题会话列表
POST /api/ideas | 新建选题会话 {account_id, title}
GET /api/ideas/{id} | 选题会话详情
DELETE /api/ideas/{id} | 删除选题会话 | !
POST /api/ideas/{id}/messages | 发消息（触发选题脑暴） {content, account_id, source_feed_ids} | $
POST /api/ideas/{id}/select | 选定候选建项目 {candidate_id, title, account_id} | $
GET /api/media-catalog/status | 素材目录状态
GET /api/media-catalog/sources | 素材源 ?kind&status&cursor
GET /api/media-catalog/providers | 外部图库配置
GET /api/media-catalog/search | 搜 Pexels/Pixabay ?provider&q&limit
POST /api/media-catalog/index | 建素材索引 | $
POST /api/media-catalog/import | 导入素材（JSON 或 multipart）
POST /api/media-catalog/sources/{id}/retry | 重试素材源 | $
GET /api/obsidian | Obsidian 知识库连接状态
POST /api/system/restart | 重启控制台（运行中的任务会中断后续跑） | !
GET /api/health | 健康检查
GET /api/runtime | 运行时信息`
