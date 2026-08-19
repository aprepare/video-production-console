# 合作伙伴单文件便携版与 VPS 网关设计

日期：2026-08-19
状态：已确认

## 1. 文档关系

本设计取代 `2026-08-19-partner-portable-draft-delivery-design.md` 及其配套的网站、Cloudflare、下载中心和 `.vpcdraft` 实施计划，作为当前合作伙伴分发目标的唯一生效设计。旧文档仅保留为历史记录，不按旧路线继续实施。

## 2. 目标

面向四位固定 Windows 合作伙伴提供一个可直接发送的自解压 EXE。合作伙伴电脑已安装剪映，收到完整风景素材包后，在本机完成风景混剪、图文制作、配音、素材落盘和剪映草稿登记。

合作伙伴不需要自行安装 Codex CLI、Python 或 FFmpeg，不需要管理员权限，也不接触上游文本、生图接口地址或密钥。文本与生图请求经部署在 `cpa` VPS 上的鉴权网关转发；每位伙伴使用独立密钥且只绑定一台电脑。软件每次启动必须联网验证通过才能进入。

## 3. 本期范围

本期包含：

- 单文件自解压 Windows EXE；
- 内置 Go 主程序、Web UI、Python 运行时、`pyJianYingDraft` 依赖、FFmpeg/FFprobe、schema、BGM、音效和转场资源；
- 首次激活、单设备绑定和每次启动验证；
- 风景混剪；
- 图文制作；
- `gpt-5.6-sol`、`grok-4.6` 和对应思考强度；
- `gpt-image-2` 生图；
- Aura Studio 现有配音模型与克隆音色；
- 本机路径检测、machine profile 自动生成、本地素材索引和剪映草稿登记；
- 四位伙伴的密钥创建、禁用、轮换、解绑和基础用量统计；
- 手工发送新版 EXE 的升级方式。

本期不包含：

- 合作伙伴网站、下载中心、Cloudflare Tunnel/Access 和 `.vpcdraft`；
- 自动更新；
- 管理员任务审核；
- 复杂网页管理后台；
- `catalog.db` 跨电脑迁移；
- 电影混剪；
- 图生视频；
- 默认向剪映草稿加入字幕；
- 对现有自用版功能做无关重构。

## 4. 总体架构

```text
partner-portable.exe
        |
        | extract/verify
        v
%LOCALAPPDATA%\VideoProductionConsole\app\<version>
        |
        +--> local Go console + embedded Web UI
        +--> bundled Python/pyJianYingDraft
        +--> bundled FFmpeg/FFprobe
        +--> bundled BGM/SFX/transitions
        |
        | HTTPS + pinned private CA
        v
https://23.138.12.112:2443
partner-gateway (Docker, SQLite)
        |
        +--> authentication/device binding/session
        +--> model allowlist/rate limit/usage
        +--> cli-proxy-api:2001/v1 (Docker internal path)

Partner PC --HTTPS directly--> Aura Studio TTS
Partner PC --local paths-----> scenery materials + Jianying drafts
```

现有 `cli-proxy-api` 及公网 `2001` 端口在第一版保持不变，避免影响现有调用。合作伙伴软件只访问 `2443` 网关，不直接访问 `2001`。

## 5. 发布形态与本地目录

对外发布物只有：

1. 一个自解压 EXE；
2. 一份独立分发的完整风景素材包。

自解压 EXE 内含版本化 payload 和 SHA-256 manifest。启动器先把 payload 解压到 staging 目录，逐项验证 hash 后原子切换为正式版本目录，再启动实际控制台。校验失败不得运行半成品。

固定目录为：

```text
%LOCALAPPDATA%\VideoProductionConsole\
├─ app\<version>\
├─ app\previous\
├─ data\
│  ├─ console.db
│  ├─ config\machine-profile.json
│  ├─ projects\
│  ├─ image-projects\
│  └─ logs\
└─ cache\
```

程序文件与用户数据分离。新版 EXE 解压新版本后继续使用原 `data`，成功启动后最多保留当前版和上一版运行目录。清理只能针对上述固定 `app` 子目录内已验证的旧版本，不能删除素材、项目或用户选择的目录。

第一版不做商业代码签名。发布时同时提供 EXE 的 SHA-256；Windows SmartScreen 可能显示未知发布者提示，这是四位固定伙伴首版试用的已知限制。

## 6. 伙伴模式

同一代码库保留自用模式和伙伴模式。发布构建写入不可由前端设置切换的 edition 标记；该标记是产品功能边界，不被当作安全边界。

伙伴模式：

- 启动不再把外部 Codex CLI 作为硬依赖；
- 文案任务使用 OpenAI-compatible 内置运行链并经 VPS 网关调用；
- 只显示风景混剪和图文制作；
- 隐藏电影混剪、图生视频及无关工程功能；
- 后端同步拒绝伙伴模式下被隐藏功能的创建请求，避免只隐藏页面而保留误操作入口；
- `/api/settings` 不返回上游 Base URL 或密钥，且拒绝伙伴端修改这些字段；
- 模型选择必须经过本地后端和 VPS 网关双重白名单校验。

自用模式保持现有行为，现有配置、项目、任务和剪映登记路径不得因伙伴模式而改变。

## 7. 激活、设备绑定与会话

### 7.1 伙伴密钥

每位伙伴创建一个随机的高熵激活密钥。后台只显示一次明文，数据库保存带盐的慢哈希和非敏感前缀，日志不记录完整密钥。

伙伴记录至少包含：

- partner id；
- 显示名称；
- key hash 与 key prefix；
- `active`/`disabled` 状态；
- 绑定设备 hash；
- session version；
- 创建、最近验证和最近调用时间；
- 文本、生图、验证失败和限流计数。

### 7.2 首次激活

首次启动只显示激活页。客户端向 `/auth/activate` 提交伙伴密钥、设备指纹 hash、应用版本和 edition。设备指纹由 Windows MachineGuid 经单向 hash 计算，不上传原始 MachineGuid、磁盘序列号或其他硬件原文。

验证成功且尚未绑定时，后台原子绑定当前设备并返回 partner id、随机 device secret、当前能力清单、模型清单和 Aura 配音运行配置。客户端不长期保存原始伙伴密钥；device secret 和 Aura API key 使用 Windows DPAPI 绑定当前 Windows 用户加密保存。

### 7.3 每次启动验证

以后每次启动，客户端用 partner id、device secret、设备 hash、应用版本调用 `/auth/verify`。后台同时检查伙伴状态、device secret hash、设备绑定、session version 和允许的最低客户端版本。任何一项失败都停留在激活/错误页，不能加载业务界面。

成功后返回随机 opaque session token，有效期 12 小时，只保存在进程内存。网关每次请求都检查 session 和伙伴状态，因此禁用伙伴后立即阻断新调用。软件长时间运行且 session 到期时，使用 device secret 静默重新验证；重新验证失败则退出业务界面。

不提供离线宽限期。服务器不可用时软件不能进入，这是明确产品规则。

### 7.4 单设备与运维动作

同一伙伴只允许一个设备 hash。第二台电脑激活必须拒绝。管理员通过服务器命令执行：创建、列出、启用、禁用、轮换激活密钥、解绑设备和查看摘要用量。解绑后原 device secret 和所有 session 立即失效，新电脑必须重新激活。

第一版不开发网页管理后台，也不存在任务审批状态。

## 8. VPS 网关

新增独立 Go 命令和 Docker 镜像，部署到 `cpa` 的独立目录与 Compose service。网关使用独立 SQLite 数据卷，不复用主控制台数据库。SQLite 启用 WAL、busy timeout 和定期一致性备份；四位用户不引入新的 Postgres 依赖。

对伙伴开放：

- `POST /auth/activate`；
- `POST /auth/verify`；
- `POST /v1/chat/completions`；
- `POST /v1/images/generations`；
- `GET /v1/models`。

管理动作只通过 VPS 本机命令执行，不开放公网管理路由。

网关在转发前执行：

- session 与伙伴状态校验；
- 请求体大小和并发限制；
- 每伙伴独立速率限制；
- 模型、思考强度、图片数量和尺寸白名单；
- 删除客户端携带的上游 Authorization，改用服务器保存的上游密钥；
- 超时、取消和响应大小限制；
- 稳定错误码与脱敏日志。

文本模型白名单为 `gpt-5.6-sol`、`grok-4.6`；思考强度白名单为 `low`、`medium`、`high`、`xhigh`、`max`、`ultra`。生图模型固定为 `gpt-image-2`，每次现有业务请求固定 `n=1`，尺寸限于当前客户端支持的比例映射。

上游地址和密钥只存在 VPS 环境文件或 Docker secret，不进入 Git、镜像层、伙伴 EXE、HTTP 响应、任务 manifest 或日志。部署完成后轮换用户在设计阶段提供过的上游共享密钥，旧密钥不作为伙伴凭据。

## 9. IP TLS

伙伴固定访问 `https://23.138.12.112:2443`，不使用域名。

使用离线私有 CA 为带 IP SAN `23.138.12.112` 的服务器证书签名。CA 私钥不放在 VPS；VPS 只保存服务器证书和叶子私钥。客户端内置 CA 公钥并由 Go HTTP transport 建立专用信任池，不修改 Windows 系统证书库，也不关闭 TLS 校验。

客户端校验证书链、IP SAN 和有效期。证书不匹配、过期或握手降级必须拒绝连接。证书续签沿用同一私有 CA，因此不要求重发 EXE；更换私有 CA 才需要发布新版 EXE。

## 10. Aura Studio 配音

Aura Studio 不经 VPS 代理。激活/验证成功后，合作伙伴电脑直接调用当前 Aura Studio HTTPS API，继续使用现有 `speech-2.8-hd` 模型和克隆音色，保留音频、SRT 与词级时间轴产物。

Aura Base URL、API key、模型和音色不在伙伴前端设置中展示。Aura API key 由激活响应通过已校验 TLS 下发，并使用 DPAPI 加密落盘；调用时只在本地 Go 进程内解密。该方案防止普通用户从界面和配置文件直接读取，但不能阻止有本机调试能力的高级用户从进程内存提取，用户已接受这一例外风险。

字幕默认不加入剪映草稿。SRT 与词级时间轴仍作为项目产物保留，便于未来重新启用字幕能力。

## 11. 首次启动与 machine profile

首次激活成功后执行本机配置向导：

1. 自动检测剪映及常见草稿根目录；
2. 检测失败时允许用户手动选择草稿根目录；
3. 要求用户选择单独收到的风景素材根目录；
4. 验证路径存在、可读写且不指向危险的系统根目录；
5. 使用内置 Python 路径、剪映草稿根目录、素材根目录和本机索引路径生成 `data\config\machine-profile.json`；
6. 检查 FFmpeg/FFprobe、Python、`pyJianYingDraft`、草稿写权限和素材可读性；
7. 通过后进入主界面。

machine profile 是本机运行配置，不是账号或授权文件。合作伙伴之间不得复制。至少包含 `python_binary`、`jianying_root`、`media_root` 和本机生成的 `media_index_path`，还可引用随包分发的 BGM、音效和转场资源配置。

本期不迁移原电脑的 `catalog.db`。选择素材根目录后，在合作伙伴电脑按现有素材扫描规则生成本机索引。路径包含中文、空格、非系统盘或不同盘符时必须正常工作。

## 12. 前端与业务流程

伙伴前端只显示业务所需信息：

- 伙伴名称与授权状态；
- `gpt-5.6-sol`、`grok-4.6`；
- 文本思考强度；
- 固定生图模型 `gpt-image-2`；
- Aura 配音模型和音色；
- 素材、输出、草稿路径及业务参数。

不显示或允许修改：

- VPS 网关地址；
- 上游中转地址；
- 任意 API key；
- 生图服务地址；
- Aura 服务地址；
- 自定义模型字符串。

风景混剪数据流：

```text
原稿 -> 网关文案处理 -> Aura 配音 -> 本地风景匹配
     -> 本地 BGM/SFX/转场 -> 生成草稿 -> 登记到本机剪映
```

图文制作数据流：

```text
原稿 -> 网关文本规划/提示词 -> 网关 gpt-image-2
     -> 图片本地落盘 -> 图文项目/剪映草稿
```

所有项目、音频、SRT、词级时间轴、图片、草稿和回执保存在合作伙伴本机。VPS 网关不持久化完整文案、生成图片或配音文件。

## 13. 错误处理

- 密钥错误、禁用、设备不匹配：显示稳定的授权错误，不进入主界面；
- VPS 不可达：显示服务暂不可用，不显示 IP、端口或内部错误；
- TLS 校验失败：拒绝连接，不允许用户点击跳过；
- 客户端版本过低：提示获取新版 EXE；
- 素材或草稿路径丢失：进入路径修复向导，不删除原项目；
- Python、FFmpeg 或资源损坏：启动器按 manifest 重新释放并校验；
- 模型失败：显示模型名称、request id 和简化错误，不显示上游地址、响应头或密钥；
- Aura 请求或下载失败：保留项目与阶段状态，允许重试配音；
- 草稿登记失败：保留已生成草稿和媒体，修复路径后重试登记；
- 新版启动失败：保留上一版目录和全部用户数据，允许重新运行旧版 EXE。

## 14. 安全与隐私

- 伙伴激活密钥、device secret、session token 和所有上游密钥不得进入日志；
- 本地长期秘密使用 DPAPI，session token 只存内存；
- 网关不信任前端传来的模型、Authorization、上游地址或伙伴身份字段；
- 网关日志只记录 request id、partner id、模型、耗时、状态、字节数和限流结果，不记录完整 prompt 或图片正文；
- 服务器错误响应统一脱敏；
- 管理命令只能通过 `cpa` SSH 的 root 运维路径执行；
- SQLite、环境密钥、TLS 叶子私钥和备份文件权限限制为部署账户可读；
- 自解压器拒绝 payload 路径逃逸、绝对路径、符号链接和 hash 不匹配；
- 所有递归清理只允许在解析并验证后的固定 `%LOCALAPPDATA%\VideoProductionConsole\app` 下执行。

网关地址可以从程序二进制或网络行为中被发现，因此地址不被当作秘密；真正的安全边界是 TLS、伙伴身份、设备绑定、session 校验、模型白名单和服务器端上游密钥。

## 15. 测试策略

### 15.1 单元测试

- 密钥生成、慢哈希、错误密钥与禁用状态；
- 首次原子绑定、第二设备拒绝、解绑和重绑；
- device secret、session 过期、session version 撤销；
- 模型、思考强度、图片数量和尺寸白名单；
- 客户端 Authorization 与上游字段剥离；
- DPAPI 保存/读取失败路径；
- machine profile 生成与路径验证；
- 自解压 manifest、hash、路径逃逸和版本切换；
- 伙伴模式设置脱敏及隐藏功能的后端拒绝。

### 15.2 集成测试

- 使用 mock 上游验证 Chat Completions 与 Images Generations 转发；
- 验证上游密钥只由网关注入且不出现在响应/日志；
- 验证每次启动、静默续期、禁用立即失效和解绑；
- 验证 TLS 正确证书、错误 CA、错误 IP SAN、过期证书；
- 验证 Aura 配置下发、DPAPI 落盘、直接配音和产物保存；
- 验证素材重扫、中文/空格/跨盘路径和剪映登记；
- 验证自用模式原有设置、任务和登记行为不回归。

### 15.3 干净 Windows 验收

在只安装剪映、没有 Codex CLI/Python/FFmpeg 的 Windows 账户上：

1. 无管理员权限双击单个 EXE；
2. 使用天中观局测试密钥激活并绑定；
3. 选择独立素材包和剪映草稿目录；
4. 完成一次真实风景混剪；
5. 完成一次真实图文制作；
6. 验证 Aura 音频、SRT、词级时间轴和默认无字幕草稿；
7. 在剪映中找到、打开并播放草稿；
8. 在第二台电脑使用同一密钥并确认被拒绝；
9. 禁用该伙伴并确认当前/新请求立即失败；
10. 运行新版 EXE 并确认激活、设置、素材路径和历史项目保留。

## 16. 发布与回滚

发布顺序固定为：

1. 备份 `cpa` 现有 Compose/容器信息，不修改 `cli-proxy-api` 现有行为；
2. 部署独立网关、SQLite 数据卷、IP TLS 和健康检查；
3. 使用 mock 上游完成安全与协议测试；
4. 接入现有 `cli-proxy-api:2001/v1`，轮换上游共享密钥；
5. 创建四位伙伴密钥；
6. 构建 partner edition 单文件 EXE并记录 SHA-256；
7. 先交付天中观局并完成干净 Windows 全链路验收；
8. 验收通过后交付其余三位伙伴。

网关回滚通过停止新 Compose service 完成，不改变现有 `2001` 服务。客户端回滚通过重新运行上一版 EXE 完成，版本化程序目录与用户数据分离，回滚不得降级或删除 `data`。

## 17. 最终验收标准

满足以下全部条件才算完成：

- 用户只需接收一个 EXE 和独立素材包；
- 在干净 Windows 用户环境下无需管理员权限启动；
- 每位伙伴独立密钥且只绑定一台电脑；
- 每次启动必须经 VPS 验证；
- 伙伴前端、日志、任务文件和导出物不出现上游接口或上游密钥；
- 文本模型、全部允许的思考强度和 `gpt-image-2` 可用；
- Aura 直连配音可用，字幕产物保留但默认不加入草稿；
- 风景混剪与图文制作均在伙伴本机完成；
- 草稿自动登记并能在伙伴剪映中打开；
- 自用模式行为不回归；
- 天中观局试点通过后才向其余伙伴发布。
