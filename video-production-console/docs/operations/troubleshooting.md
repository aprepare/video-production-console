# 常见问题与排障手册

本文记录真实运行中已经遇到、复现并定位过的问题。目标是先根据症状判断故障层级，再采取最小处置，避免盲目重启、重复派任务或破坏已经生成的草稿。

## 1. 通用排障顺序

1. 先检查 `GET http://127.0.0.1:2030/api/health`。
2. `200 {"status":"ok"}` 表示控制台服务存活；其他受保护 API 返回 `401` 通常只是未登录，不代表服务故障。
3. 任务失败时先读 `/api/tasks/{id}`、`/result`、`/diagnostics`，按相同 `error_message` 归类，不要逐条盲目重试。
4. 区分混剪阶段：
   - 尚未产生明文工作区：修复生成环境后重新派混剪；
   - `completion_phase=plaintext_ready` 且 `can_retry_registration=true`：明文草稿已保留，只修登记环境并调用“只重试登记”；
   - `completion_phase=registered`：草稿已经登记成功，不要重复生成。
5. 改后端、嵌入前端或启动配置后，必须重建 exe 并重启 2030，再做真实 API/任务验证。

## 2. 控制台连接、登录与页面

### 2.1 `2030` 返回 `401`

**含义：** 会话未登录或已过期，不是服务宕机。

**检查：**

- `/api/health` 是否仍返回 200；
- 浏览器是否需要重新登录；
- 写请求是否同时带 `video_console_session`、`video_console_csrf` Cookie 和 `X-CSRF-Token`。

**注意：** 同一来源 10 分钟内 5 次密码失败会锁定 15 分钟。密码只试一次，不要自动循环重试。

### 2.2 连接被拒绝、HTTP 000、`WinError 10061`

**含义：** 2030 没有监听，监控层只能标记 `monitor_error`；这不等于库内任务从 `completed` 变成 `failed`。

**处理：**

1. 查端口而不是猜进程名；
2. 确认没有第二个实例占用 SQLite；
3. 从仓库根目录启动已重建的 `dist/video-production-console.exe`；
4. 恢复后再回读任务和项目资产，禁止因为监控读取失败而重复派任务。

### 2.3 项目页面刷新后 404

**原因：** 运行的是未包含 SPA 回退的旧构建，或者只改了 Vite 开发前端、没有同步嵌入资源。

**处理：**

```powershell
npm --prefix web run build:embed
.\scripts\check-embedded-dist.ps1 -Strict
go build -o .\dist\video-production-console.exe .\cmd\console
```

重启后再强制刷新浏览器。

### 2.4 页面提示“检查网络连接”，但服务健康

这类提示是前端对 `fetch` 异常的兜底文案，不足以证明真实网络故障。应结合请求是否到达服务端、HTTP 状态码和后端路由判断。

## 3. 配音与素材上传

### 3.1 手动上传配音总提示网络失败

**真实原因（已修复）：**

- 自动配音：`POST /api/projects/{id}/narration`
- 手动上传：`POST /api/projects/{id}/assets/narration`

旧路由只按路径是否以 `/narration` 结尾分流，导致手动上传也被送进自动配音 mux，返回裸 `404 page not found`，浏览器再显示成网络失败。

**当前约束：**

- `/narration` 只负责读取连续文案并调用语音服务生成配音和 SRT；
- `/assets/narration` 只负责上传 `.mp3`、`.wav`、`.m4a`，上限 200MB，不自动生成字幕；
- 两条路由有独立回归测试，后续不得合并为模糊后缀判断。

### 3.2 配音文件被拒绝

依次检查：

1. 扩展名是否为 MP3/WAV/M4A；
2. 文件真实内容与扩展名是否一致；
3. 是否超过 200MB；
4. MP3/WAV 文件头是否有效；
5. 会话和 CSRF 是否仍有效。

不要仅凭文件能播放就判断一定能通过严格资产校验。

### 3.3 自动配音失败

自动配音依赖设置中的火山语音 API Key、音色 ID 和资源 ID。该入口与手动上传独立：自动配音未配置不会阻止手动上传已有音频。

## 4. 混剪生成阶段

### 4.1 `No module named 'pyJianYingDraft'`

**真实原因（已修复）：** machine profile 虽然指定了正确 Python，但旧代码构造 `montage-script-run` 时没有把 `python_binary` 传给自调用子进程，生成阶段仍回退到进程 PATH 里的 Hermes Python。

**当前链路：**

```text
machine profile.python_binary
→ 解析为绝对路径
→ --python-binary
→ montage-script-run
→ montagescript.Run
```

machine profile 应使用明确绝对路径，例如：

```json
"python_binary": "D:\\ProgramData\\anaconda3\\python.exe"
```

禁止依赖模糊的 `"python"` 和启动终端 PATH。

### 4.2 `cannot import name '_imaging' from 'PIL'`

**真实原因（已修复）：** 登记阶段调用的是 Anaconda Python，但继承了 Hermes 的 `PYTHONPATH`，优先加载 Hermes venv 中不匹配的 Pillow 二进制扩展。

**当前处理：** 登记和重命名子进程启动前清除父进程的 `PYTHONPATH`、`PYTHONHOME`，并显式设置 UTF-8 输出环境。

如果未来再次出现，先打印目标 Python 的 `sys.executable`、`PIL.__file__` 和 `sys.path`，确认没有跨环境加载包。

### 4.3 `ffprobe` 不在 PATH

典型错误：

```text
build plan: measure narration duration: exec: "ffprobe": executable file not found in %PATH%
```

本机已知 ffprobe 位于：

```text
D:\ProgramData\anaconda3\envs\index-tts\Library\bin\ffprobe.exe
```

当前混剪优先用 `pyJianYingDraft.local_materials.AudioMaterial` 测量音频时长，失败时才回退 ffprobe。若仍报此错，先检查目标 Python 环境和 ffprobe 路径，不要改时间线数据。

### 4.4 素材时间范围超出真实时长

典型错误：

```text
截取素材时间范围 [start=0, end=8800000] 超出素材时长(8140000)
```

8 秒时间线按 1.1× 播放需要约 8.8 秒源素材。当前系统会在入队前排除 `<10s` 素材，并由 `fitShotToClip`/skill 执行层再次钳制。若再次出现，属于素材时长过滤或计划钳制回归，应修代码，不能靠手改计划 JSON。

### 4.5 `validate-plan: exit status 2`

先读任务目录中的：

```text
output/production_plan.validation.json
```

历史原因包括长片 SFX 数量不符合规则、镜头 source range 超界、资源配置缺失。不要只看 exit status；以 validation 文件中的具体规则为准。

### 4.6 长片只有一个 SFX 导致计划失败

旁白 `<240s` 可只有一个开场 SFX；`>=240s` 必须稳定铺 3—5 个且间隔至少 12 秒。系统已由 Go 确定性生成，不应让调用方手写 SFX 时间点。

## 5. Manifest、结果与数据库契约

### 5.1 `task_id must be a UUID`

manifest schema 2.0 要求 `task_id == job_id == 控制台任务 UUID`。不能使用自造短 ID 或外部工单编号。

### 5.2 `artifact path is outside output_dir`

任务产物只能位于 manifest 声明的 `output_dir` 内。不要把任意本机路径塞入结果信封，也不要绕过受控任务目录。

### 5.3 `UNIQUE constraint failed: idea_candidates.id`

属于候选 ID 落库冲突。修复方向是候选 ID 生成/幂等和事务逻辑，不是删除数据库或重复提交同一批候选。

### 5.4 Codex 上下文耗尽

典型错误：

```text
Codex ran out of room in the model's context window
```

应缩小输入：使用控制台准备的来源快照、按需读取素材索引，禁止把整份媒体索引、整库转录或无关日志灌入任务上下文。

## 6. PowerShell与本机脚本坑

### 6.1 外层Shell吞掉 `$`

不要在 Git Bash 外层直接写包含 `$var`、`foreach` 的 `powershell -Command "..."`。外层可能先展开变量，产生 `Missing variable name after foreach` 等假错误。复杂逻辑写成 `.ps1` 后用 `-File` 执行。

### 6.2 中文 `.ps1` 被 PowerShell 5.1 按错误编码读取

无 BOM 的 UTF-8 中文脚本可能被按 GBK 解析，表现为字符串终止符缺失、中文乱码或语法错误。安全做法：

- 尽量不用中文脚本文字；或
- 保存为 UTF-8 BOM；
- 开头设置：

```powershell
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
```

### 6.3 PowerShell脚本嵌套调用时Body丢失

传递 Hashtable 等复杂对象时，优先在同一个 PowerShell 进程中直接调用脚本：

```powershell
& $api -Path $path -Method POST -Body $body
```

不要再启动一层 `powershell -File ... -Body $body`，否则对象可能被字符串化或丢失，服务端表现为 `Task type and prompt are required.`。

## 7. 不要采取的错误处置

- 不要把 `401` 当服务宕机；
- 不要因监控连接失败而重派已完成任务；
- 不要在已有 `plaintext_ready` 时重新生成混剪，优先“只重试登记”；
- 不要手写 production plan、时间轴 segments 或非契约 JSON 工单；
- 不要启动第二个控制台实例争抢 SQLite；
- 不要删除 `video-console-data/`、剪映正式草稿或保留的明文工作区；
- 不要用 `git reset/checkout/restore/stash/clean` 处理运行问题。

## 8. 修复后的验证门槛

涉及上述链路的修复，至少执行：

```powershell
go test ./...
npm --prefix web run typecheck
npm --prefix web run lint
npm --prefix web test -- --run
npm --prefix web run build:embed
.\scripts\check-embedded-dist.ps1 -Strict
go build -o .\dist\video-production-console.exe .\cmd\console
```

然后重启 2030，并用真实任务验证：生成成功、登记成功、`completion_phase=registered`、`registered_asset` 非空且 `error_message` 为空。自动测试不能替代真实剪映草稿登记验证。
