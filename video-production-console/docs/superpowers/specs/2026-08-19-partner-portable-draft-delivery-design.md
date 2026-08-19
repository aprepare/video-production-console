# 合作伙伴 Portable Draft 交付设计

日期：2026-08-19

## 1. 目标

本设计让多个合作伙伴通过独立网站提交原稿，并在 Windows 主机上全自动完成文案、配音、剪映草稿、素材截取与 `.vpcdraft` 交付。合作伙伴网站经 Cloudflare Tunnel/Access 对外提供，管理后台仍仅监听 `127.0.0.1:2030`；合作伙伴服务监听 `127.0.0.1:2032`，由 Cloudflare Access 提供邮箱 OTP 和身份断言，应用验证 Access JWT 的 audience/email 后建立自己的短期授权会话。下载包可在另一台只有 Windows、剪映和导入器的电脑上导入，不依赖 Codex、Python、FFmpeg 或完整素材库。

## 2. 非目标

- 不公开管理后台，不把现有发布 `accounts` 作为合作伙伴登录用户。
- 不发送完整素材库，不把 portable 任务登记到主机剪映。
- 不支持管理员审核作为自动流程闸门；管理员只做运维、查看和故障处置。
- 不让导入器执行任意脚本、依赖服务器运行时或直接复用现有 Python 注册逻辑。
- 不在本期重写现有 `local_jianying` 管线。

## 3. 已确定约束与决策

1. `projects`/`codex_tasks`/`assets`/`task_artifacts` 继续复用；新增 partner/package 存储与迁移。
2. 合作伙伴身份使用 `partner_users`，项目授权使用 `partner_account_grants` 和 `partner_project_owners`；每个后端查询必须带 owner scope，不能以 account 登录态代替 owner。
3. partner 项目固定 `delivery_mode=portable`；管理员自用项目固定 `delivery_mode=local_jianying` 并保持现状。
4. 自动阶段为 `queued → rewriting → narrating → montaging → validating → clipping_media → packaging → signing → ready`。失败仅从失败阶段重试，已完成阶段不重复执行；阶段和重试信息持久化。
5. `.vpcdraft` 是可验证、可移植的交付物；包内路径只允许 `vpcasset://<asset-id>`，服务器绝对路径残留即失败。
6. 现有 Python 注册逻辑仅作为参考行为；Go importer 必须独立实现锁、备份、草稿 ID 冲突、原子索引更新和回执验证。

## 4. 架构

```text
Partner browser --Cloudflare Tunnel/Access--> 127.0.0.1:2032 partner HTTP API
                                                    |
                                      partnerauth + owner-scoped stores
                                                    |
                         projects/codex_tasks/assets/task_artifacts + package store
                                                    |
                         worker: partnerworkflow -> portablepackage -> signer

Admin browser -------------------------------> 127.0.0.1:2030 admin API/UI

Other Windows PC: jianying-draft-importer.exe + 剪映 + 用户选择的长期素材目录
```

组件边界如下：

- `internal/partnerauth`：Access JWT/audience/email 验证、短期应用会话、Access 断言绑定、partner owner scope；不发送或验证应用 OTP，不读取发布账户密码。
- `internal/partnerworkflow`：持久化状态机、阶段幂等、失败重试、交付门。
- `internal/portablepackage`：明文 workspace 验证、资产收集、稳定命名、manifest、Zip、Ed25519 签名。
- `internal/httpapi/partner_*`：新建项目、我的项目、进度、下载中心；响应只含友好状态和下载元数据。
- `store` partner/package：owner、阶段、包、下载事件及有效期的事务读写。
- `web/src/partner`：独立合作伙伴页面。
- `cmd/draft-importer`：独立 Windows 可执行文件；不依赖服务器代码运行时。
- `migrations`：新增表、索引、约束。
- `app/main/release/admin monitoring`：启动 partner 服务、构建发布物、监控阶段/磁盘/失败和下载。

## 5. 数据模型

- `partner_users`：`id`, `email`（规范化且唯一）, `status`, `access_subject`（可选）, `created_at`, `last_login_at`, `session_version`。Access 身份断言通过后，应用仅建立短期授权会话。
- `partner_account_grants`：`partner_user_id`, `account_id`, `scope`, `active`, 审计时间；用于将合作伙伴映射到可用业务账户，但不授予管理后台权限。
- `partner_project_owners`：`project_id`, `partner_user_id`, `account_id`, `created_at`, 唯一约束 `(project_id, partner_user_id)`；所有项目、任务、资产、artifact 和下载查询通过该表过滤。
- `portable_packages`：`id`, `project_id`, `owner_id`, `package_path`, `package_sha256`, `signature_key_id`, `manifest_version`, `size_bytes`, `status`, `expires_at`, `marked_expired_at`, `created_at`。包状态至少为 `ready`, `expired`, `purged`。
- `partner_download_events`：`package_id`, `owner_id`, `event_type`（issued/range/complete/expired/renewed/failed）, `request_id`, `bytes_sent`, `created_at`, 客户端摘要；不记录敏感路径。
- `projects` 增加 `delivery_mode`；`codex_tasks`/阶段记录增加 `stage`, `attempt`, `started_at`, `finished_at`, `last_error_code`, `last_error_message`, `next_retry_at`, `worker_id`、幂等键。迁移默认既有项目为 `local_jianying`。

数据库事务必须同时检查 owner；越权查询返回不存在（而非泄露存在性）。状态更新使用版本号/条件更新，避免两个 worker 重复阶段。

## 6. 状态机与交付门

每阶段入口先读取持久化输出指纹；指纹相同则复用并跳过执行。阶段成功以 artifact/hash 写入事务为准，随后才推进状态。失败记录稳定错误码和重试时间；可重试错误采用退避，永久错误停留在该阶段并显示可理解原因。

最终 `ready` 前必须全部通过：原稿存在且可读、文案完成、配音可解码、明文 draft 可验证、截取时长与 `source_timerange` 一致、所有引用资产均已收集、服务器路径扫描为零、包 SHA-256 已计算、Ed25519 签名可验证、磁盘空间余量满足阈值。未通过不得创建可下载链接。

合作伙伴看到的状态仅为“排队中、处理中、准备下载、下载已过期、需要重试”等友好文本；不得返回 API key、绝对路径、worker 日志或内部错误堆栈。

## 7. Portable 包格式

`.vpcdraft` 是 ZIP 容器，根目录包含 `manifest.json`、`draft/` 明文草稿、`assets/` 实际引用媒体、`SIGNATURE.ed25519`，可包含 `backgrounds/`、`generated/`、`audio/` 等按 role 分类的文件。只收集任务实际引用的风景视频片段、配音、允许分发的 BGM/SFX、背景/生成图层；不复制完整素材库。

`manifest.json` 必须记录 `schema_version`, `package_version`, `importer_min_version`, `draft_version`, `project_id`, 包级 hash/signature 元数据，以及每个资产的 `asset_id`, `role`, 包内 `path`, 原始 `sha256`, `size`, `mime`, `source_timerange`、持续时间和必要的编码信息。资产以 SHA-256 稳定命名，避免同名覆盖。

视频按实际使用范围前后各保留 3 秒；不足则取可用范围。必要时重编码，重编码后重新计算 hash，并同步更新 manifest 与 draft 的 `source_timerange`。生成包前将 draft 中所有服务器绝对路径替换为 `vpcasset://<asset-id>`；扫描 Windows 驱动器路径、UNC、工作区绝对路径和临时路径，任何残留直接使 packaging 失败。

签名覆盖 canonical manifest（固定 UTF-8、排序规则和换行）及包内文件 hash 列表。签名私钥只在服务器受保护存储中使用，包中仅携带 key id 与公钥验证所需信息；轮换保留旧 key 的验证能力。

## 8. 导入器算法

`jianying-draft-importer.exe` 首次运行让用户选择长期素材目录，默认 `%USERPROFILE%\Videos`；记录本机配置，不上传目录。用户双击关联 `.vpcdraft` 后：

1. 打开包并限制总展开大小、单文件大小、文件数量和路径深度；拒绝绝对路径、`..`、符号链接和 Zip Slip。
2. 验证 schema/importer 版本、Ed25519 签名、manifest 全部 SHA-256、文件大小、MIME/媒体可读性及磁盘空间。
3. 解压到带随机名的 staging 目录，按 `asset_id` 建立 `vpcasset://` 到本地长期素材目录的映射；缺失或冲突不覆盖用户文件。
4. 解析明文 draft，替换全部虚拟路径，生成新的草稿 ID；若 ID 已存在则生成确定性再建副本 ID，并向用户说明。
5. 提示用户关闭剪映；获得目标草稿目录锁后备份 `root_meta_info` 及受影响索引，写入临时文件并 flush，原子 rename 更新索引。
6. 重新读取并完整验证新草稿、路径、资产 hash、时长和索引回执；成功后提交 staging，写入本地导入回执（包 hash、草稿 ID、时间、版本）。
7. 任一步失败，释放锁、删除未提交 staging、恢复备份并原子恢复索引；保留错误码和可诊断摘要，不泄露服务器路径。

导入同一包重复执行时，回执使其幂等：若目标草稿和 hash 已匹配则报告已导入；用户可选择再建副本。导入器不能调用 Python 注册模块，也不能假定本机有 FFmpeg；媒体校验使用 Go/Windows 可用库，若必须转换则包在服务器端已完成。

## 9. 下载中心

包下载链接默认 7 天有效，可由授权合作伙伴续期；续期只更新 `expires_at` 并记录事件。服务支持 HTTP Range 和断点续传。过期先原子标记 `expired`，不立即删除项目；清理作业只删除过期包文件及 staging，保留项目、manifest 摘要和下载审计。下载响应不得暴露文件系统路径。

## 10. 安全与错误恢复

Cloudflare Access 负责邮箱 OTP 和身份断言；应用验证 Access JWT 的 audience、email（以及可选的 `access_subject`）后建立短期授权会话，并绑定会话版本。应用不发送或验证第二套 OTP，不构成双 OTP。Cookie 使用 Secure/HttpOnly/SameSite，CSRF、CORS、请求体、并发和下载速率均受限；会话撤销通过递增 `session_version`，并拒绝过期、重放或 audience/email 不匹配的 Access 断言。后台 2030 不经 Tunnel 暴露；2032 只开放 partner 路由。日志采用 request id 和稳定错误码，脱敏邮箱、token、绝对路径、API key。

包制作采用临时目录、fsync、原子 rename；磁盘空间不足、源文件变化、权限不足、签名/路径/哈希错误均可恢复或明确终止。worker 崩溃后租约超时可安全重试；阶段输出按幂等键和 hash 复用。数据库迁移可前滚，新增字段兼容旧项目。

## 11. 兼容性

既有管理员项目默认 `local_jianying`，原流程和剪映登记行为不变。新 importer 只接受明确支持的 schema 和 `importer_min_version`，未知字段可忽略但未知必需字段拒绝。Windows 支持文档规定的版本、NTFS、UTF-8 路径和剪映草稿布局；剪映布局变更由 `draft_version`/导入器版本门阻止静默损坏。

## 12. 测试与验收

- 单元测试：owner scope 越权、Access 断言验证（JWT/audience/email/过期/重放）、会话撤销与版本并发、每阶段幂等/重试、状态并发、路径扫描、3 秒余量、hash/manifest canonicalization、签名、Range、过期续期。
- 集成测试：从原稿到 ready 的全链路；失败注入后只重跑失败阶段；旧 `local_jianying` 回归；迁移升级/回滚。
- 安全测试：Zip Slip、绝对路径、符号链接、伪造 manifest/签名、哈希替换、越权 IDOR、CSRF、Range 滥用、日志泄密。
- importer Windows 测试：锁竞争、剪映未关闭、权限不足、空间不足、ID 冲突、索引中断、回滚、重复导入和再建副本。
- 跨机人工验收：M1 使用现有天中观局草稿，在另一台仅有 Windows+剪映+导入器且没有素材库的电脑导入并打开；M1 通过后 M2 验证正式包/导入器，M3 验证网站，M4 验证 Tunnel、Access、升级和回滚。

验收必须保存包 hash、签名验证结果、导入回执、草稿打开截图/日志摘要和阶段审计；不得把原始绝对路径带入共享证据。

## 13. 里程碑

- M1：最小 portable 包、Go importer、天中观局草稿跨机导入/回滚验证。
- M2：正式 manifest、资产截取/重编码、签名、交付门、下载中心。
- M3：partnerauth、2032 API、四个页面、持久化 workflow 与可观测性。
- M4：Cloudflare Tunnel/Access、Access 断言验证与会话撤销/重放防护、升级迁移、运维演练和正式发布。

## 14. 发布与运维

发布物包含服务器版本、签名 key id、公钥、`jianying-draft-importer.exe` 及版本校验信息。上线前执行迁移备份、M1–M4 验收和回滚演练；发布阶段只允许一个 schema/Importer 兼容矩阵内的组合。监控 partner 2032 可用性、Access 断言验证失败率、会话撤销/重放拒绝、各阶段耗时/重试、交付门失败、磁盘余量、包生成失败、Range 错误、过期清理和导入回执失败。告警不得包含密钥或绝对路径。定期轮换签名密钥、审计 grant、清理包和 staging，并保留项目级审计记录。

## 15. 明确决策清单

- partner 与发布 account 分离；owner scope 是后端查询硬约束。
- partner 全自动，无管理员审核；失败按阶段重试。
- portable 不登记主机剪映；local_jianying 完全保留。
- 包只含实际引用且留 3 秒的媒体，SHA-256 稳定命名，Ed25519 签名，绝对路径零容忍。
- importer 独立、Go 实现锁/备份/ID 冲突/原子索引/回执，不依赖 Python、Codex、FFmpeg 或素材库。
- 下载 7 天、可续期、支持 Range；过期清包但不删项目。
- M1 跨机验证通过后才进入正式包、网站和 Tunnel 发布。
