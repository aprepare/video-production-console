# 图文制作双输出模式实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在现有 `/image-projects` 图文制作中增加可持久恢复的“图片视频”和“图生视频”两种输出模式，全部镜头成功后生成并可信登记剪映草稿；图生视频失败自动重试且绝不静默降级。

**Architecture:** 保留现有图文图片项目与 ZIP 流程，在其旁新增独立 `internal/imagevideo` 作业域和 `internal/grokvideo` 供应商客户端。作业以 SQLite 租约、版本号、attempt 历史和幂等键驱动；配音复用 `internal/narration`，媒体质检复用设置中的 FFmpeg/FFprobe，草稿生成使用固定内置模板 `jianying-image-video-v1`，可信登记复用现有 montage `Registrar`，并在 ImageVideo 域建立等强度的 job-bound manifest/workspace 校验。前端只读取公开作业状态，不接触接口地址或密钥。

**Tech Stack:** Go 1.25、SQLite/modernc、React 19 + TypeScript + react-query、Vitest、Playwright、FFmpeg/FFprobe、pyJianYingDraft/剪映受管登记、OpenAI-compatible Grok 视频接口。

---

## 文件结构与边界

新增文件：

- `internal/imagevideo/model.go`：输出模式、作业、镜头、attempt、状态与公开错误模型。
- `internal/imagevideo/template.go`：固定模板版本、嵌入配置和 SHA-256 指纹。
- `internal/imagevideo/templates/jianying-image-video-v1.json`：无字幕、无可编辑标题的竖屏草稿合同。
- `internal/imagevideo/timing.go`：配音时序到 4.3–4.6 秒、至少 6 秒、6/10/15 秒镜头的确定性转换。
- `internal/imagevideo/narration.go`：把 ImageProject 适配到 `internal/narration`，保存配音与 timing document。
- `internal/imagevideo/media.go`：真实探针、完整解码、480×848/24fps 标准化。
- `internal/imagevideo/worker.go`：租约领取、公平调度、自动重试和手动 retry round。
- `internal/imagevideo/draft.go`：写入专用草稿计划并调用受控 Python 构建器。
- `internal/imagevideo/register.go`：把成功工作区交给可信登记抽象。
- `internal/grokvideo/client.go`：提交、轮询和下载 Grok 图生视频。
- `internal/store/imagevideojobs.go`：作业、镜头、attempt、租约与幂等仓储。
- `internal/httpapi/imagevideo_jobs.go`：开始、详情、失败镜头重试、取消、登记重试 API。
- `scripts/image-video-draft/build_image_video_draft.py`：只根据已校验计划创建剪映明文工作区。
- `web/src/image-mode/ImageVideoJobPanel.tsx`：模式/账号选择、锁定状态、进度、失败与重试。

修改文件：

- `internal/store/migrations.go`、`internal/domain/imageprojects.go`、`internal/store/imageprojects.go`
- `internal/httpapi/imageprojects.go`、`internal/httpapi/imageproject_quick.go`、`internal/app/app.go`
- `web/src/types.ts`、`web/src/image-mode/QuickGenerateForm.tsx`、`web/src/image-mode/ImageModeWorkbench.tsx`、`web/src/image-mode/image-mode.css`
- 相应 Go、Vitest、Playwright 测试与 `internal/webui/dist` 嵌入产物。

不修改 `/projects?mode=image-video`，不复用 `RunMode` 表示输出模式，不使用 montage v1 忽略 `MixPreset` 的降级路径。

### Task 1：冻结领域合同、模板和 SQLite 迁移

**Files:**
- Create: `internal/imagevideo/model.go`
- Create: `internal/imagevideo/template.go`
- Create: `internal/imagevideo/templates/jianying-image-video-v1.json`
- Create: `internal/imagevideo/template_test.go`
- Modify: `internal/domain/imageprojects.go`
- Modify: `internal/store/migrations.go`
- Modify: `internal/store/migrations_test.go`

- [ ] **Step 1：先写迁移失败测试**

在 `internal/store/migrations_test.go` 增加 `TestMigration24AddsImageVideoOutputModesAndJobs`：建立只迁移到 v23 的数据库，插入一个历史 `image_projects` 记录，再执行最新迁移并断言：

```go
if got := columnText(t, db, "image_projects", projectID, "output_mode"); got != "image_slideshow" {
    t.Fatalf("legacy output_mode = %q", got)
}
requireColumns(t, db, "image_projects", "output_mode", "output_mode_locked_at", "account_id", "template_version", "template_fingerprint")
requireTable(t, db, "image_video_jobs")
requireTable(t, db, "image_video_job_items")
requireTable(t, db, "image_video_job_attempts")
```

再测试非法 `output_mode`、非法 item duration、同一项目两个活动 job、重复 attempt 幂等键均被数据库拒绝。

- [ ] **Step 2：运行测试，确认因迁移缺失而失败**

Run: `go test ./internal/store -run 'TestMigration24AddsImageVideo' -count=1`

Expected: FAIL，错误应是缺少字段或表，而不是测试语法错误。

- [ ] **Step 3：实现第 24 个迁移和领域常量**

`internal/imagevideo/model.go` 固定：

```go
type OutputMode string

const (
    ModeSlideshow    OutputMode = "image_slideshow"
    ModeImageToVideo OutputMode = "image_to_video"
)

type JobStatus string
const (
    JobPending JobStatus = "pending"
    JobRunning JobStatus = "running"
    JobSucceeded JobStatus = "succeeded"
    JobFailed JobStatus = "failed"
    JobCanceled JobStatus = "canceled"
)

const (
    VideoModel = "grok-imagine-video-1.5"
    VideoResolution = "480p"
    VideoWidth = 480
    VideoHeight = 848
    VideoFPS = 24
    MaxVideoConcurrency = 6
    MaxAttemptsPerRound = 3
)
```

迁移使用仓库现有 SQLite 类型：`TEXT`、`INTEGER`、`DATETIME`。第 24 个 migration 只由现有 schema version 表执行一次；测试连续两次打开同一数据库，第二次必须跳过 v24 且不重复执行 `ALTER TABLE`。`image_project_items.id` 作为稳定镜头来源，不假设不存在的资产表 ID；作业 item 保存输入路径的 SHA-256 快照和输出相对路径。关键约束：

```sql
ALTER TABLE image_projects ADD COLUMN output_mode TEXT NOT NULL DEFAULT 'image_slideshow'
  CHECK (output_mode IN ('image_slideshow','image_to_video'));
ALTER TABLE image_projects ADD COLUMN output_mode_locked_at DATETIME;
ALTER TABLE image_projects ADD COLUMN account_id TEXT REFERENCES accounts(id) ON DELETE SET NULL;
ALTER TABLE image_projects ADD COLUMN template_version TEXT;
ALTER TABLE image_projects ADD COLUMN template_fingerprint TEXT;

CREATE UNIQUE INDEX image_video_one_active_job
ON image_video_jobs(project_id)
WHERE status IN ('pending','running');
```

`image_video_jobs` 还必须包含 `idempotency_key UNIQUE`、`phase`、`draft_status`、`registration_status`、`lease_owner`、`lease_expires_at`、`version`、配音/草稿/登记错误、manifest 相对路径和指纹字段；phase 固定为 `preparing|narration|media|draft|registration|completed`。`image_video_job_items` 使用 `image_project_item_id` 外键、`retry_round`、`attempt`、`timeline_duration_us`、可空 `requested_duration_seconds`、可空 `actual_duration_us`、受管相对输入/输出路径和 SHA-256；`image_video_job_attempts` 对 `(job_item_id,retry_round,attempt)` 和 `idempotency_key` 唯一。所有路径在写入前必须规范化并验证位于 `<DataRoot>/image-projects/<projectID>/`，不得进入 API 响应。

- [ ] **Step 4：实现固定模板及指纹测试**

模板 JSON 固定 `canvas=1080x1920`、`spoken_captions=false`、`editable_titles=false`、图片前 30 秒时长范围、后续最小时长、轻推拉/平移/淡入淡出白名单、图生视频目标 480×848/24fps。`template.go` 用 `//go:embed` 加载并计算 SHA-256；测试断言版本为 `jianying-image-video-v1`，重复调用指纹稳定且模板中字幕/标题均关闭。

- [ ] **Step 5：运行迁移与模板测试**

Run: `go test ./internal/store ./internal/imagevideo -run 'TestMigration24|TestBuiltInTemplate' -count=1`

Expected: PASS。

- [ ] **Step 6：提交**

```powershell
git add internal/store/migrations.go internal/store/migrations_test.go internal/domain/imageprojects.go internal/imagevideo/model.go internal/imagevideo/template.go internal/imagevideo/template_test.go internal/imagevideo/templates/jianying-image-video-v1.json
git commit -m "feat: add image video job contracts"
```

### Task 2：实现持久作业仓储、模式锁定和崩溃恢复

**Files:**
- Create: `internal/store/imagevideojobs.go`
- Create: `internal/store/imagevideojobs_test.go`
- Modify: `internal/store/imageprojects.go`
- Modify: `internal/store/imageprojects_test.go`

- [ ] **Step 1：写仓储失败测试**

覆盖以下独立行为：

```go
func TestCreateImageVideoJobLocksModeAccountAndTemplateAtomically(t *testing.T)
func TestCreateImageVideoJobReturnsExistingJobForSameIdempotencyKey(t *testing.T)
func TestCreateImageVideoJobRejectsDifferentModeAfterLock(t *testing.T)
func TestClaimImageVideoItemUsesLeaseAndVersion(t *testing.T)
func TestExpiredLeaseRequeuesOnlyUnfinishedItems(t *testing.T)
func TestStartRetryRoundKeepsSucceededItemsAndAttemptHistory(t *testing.T)
func TestFinalFailureNeverChangesOutputMode(t *testing.T)
```

锁定测试必须同时检查 `output_mode_locked_at`、`account_id`、`template_version`、`template_fingerprint`；任一校验失败时不允许残留半个 job。

- [ ] **Step 2：运行测试确认 RED**

Run: `go test ./internal/store -run 'Test(Create|Claim|Expired|StartRetry|FinalFailure).*ImageVideo' -count=1`

Expected: FAIL，提示 repository 或方法不存在。

- [ ] **Step 3：实现窄仓储接口**

```go
type ImageVideoJobRepository interface {
    CreateAndLock(ctx context.Context, input imagevideo.CreateJob) (imagevideo.Job, bool, error)
    GetDetail(ctx context.Context, jobID string) (imagevideo.JobDetail, error)
    ClaimNextItem(ctx context.Context, owner string, now time.Time, lease time.Duration) (imagevideo.ClaimedItem, error)
    BeginAttempt(ctx context.Context, claim imagevideo.ClaimedItem, requestFingerprint string) (imagevideo.Attempt, error)
    CompleteAttempt(ctx context.Context, input imagevideo.AttemptSuccess) error
    FailAttempt(ctx context.Context, input imagevideo.AttemptFailure) (retry bool, err error)
    StartRetryRound(ctx context.Context, jobID string, now time.Time) ([]string, error)
    Cancel(ctx context.Context, jobID string, now time.Time) error
    RequeueExpired(ctx context.Context, now time.Time) (int64, error)
}
```

所有 claim/complete/fail 操作用 `runImmediate` 和 `WHERE version=?`；成功 item 的输出 SHA 和路径不可被后续 retry round 覆盖。错误文本进入数据库前调用统一脱敏/截断函数。

- [ ] **Step 4：扩展 ImageProject scan/create/update**

更新 `imageProjectSelectCols`、`scanImageProject`、`Create` 和默认值。增加条件更新：仅 `output_mode_locked_at IS NULL` 时允许修改模式。历史 quick/manual 的 `RunMode`、恢复逻辑保持不变。

- [ ] **Step 5：运行仓储全包测试**

Run: `go test ./internal/store -count=1`

Expected: PASS，旧 image project 测试也必须通过。

- [ ] **Step 6：提交**

```powershell
git add internal/store/imagevideojobs.go internal/store/imagevideojobs_test.go internal/store/imageprojects.go internal/store/imageprojects_test.go
git commit -m "feat: persist image video jobs"
```

### Task 3：按配音时序生成确定性镜头计划

**Files:**
- Create: `internal/imagevideo/timing.go`
- Create: `internal/imagevideo/timing_test.go`
- Create: `internal/imagevideo/narration.go`
- Create: `internal/imagevideo/narration_test.go`

- [ ] **Step 1：写时长选择和语义拆分失败测试**

```go
func TestChooseVideoTierUsesSmallestCoveringDuration(t *testing.T) {
    cases := []struct{ us int64; want int }{
        {4_300_000, 6}, {6_000_000, 6}, {6_000_001, 10},
        {10_000_001, 15},
    }
}

func TestSplitTimedSegmentOver15SecondsAtPunctuation(t *testing.T)
func TestSlideshowScenesKeepFirst30SecondsBetween43And46(t *testing.T)
func TestSlideshowScenesAfter30SecondsAreAtLeast6Seconds(t *testing.T)
func TestScenePlanningNeverDropsNarrationTextOrTime(t *testing.T)
```

当单段超过 15 秒时，在 timing document 中最近的句号、问号、感叹号、分号或逗号边界拆成子镜头；若没有标点，以词级时间戳的最后安全边界拆分。子镜头暂时复用同一图片，但使用不同动效方向，不能截断音频或丢字。

- [ ] **Step 2：运行测试确认 RED**

Run: `go test ./internal/imagevideo -run 'TestChooseVideoTier|TestSplitTimed|TestSlideshow|TestScenePlanning' -count=1`

Expected: FAIL。

- [ ] **Step 3：实现纯函数镜头计划**

核心 API：

```go
func BuildTimedScenes(mode OutputMode, items []domain.ImageProjectItem, timing narration.TimingDocument) ([]Scene, error)
func ChooseVideoTier(durationUS int64) (int, error)
```

输出必须保留 `source_text` 的顺序和逐字覆盖，保存 `start_us/end_us`、`timeline_duration_us`、图片 item ID、图片 SHA、标题和动效种类。只有 `ModeImageToVideo` 调用 `ChooseVideoTier` 并保存 6/10/15 秒档位；`ModeSlideshow` 的 requested/actual video duration 必须为空。图片视频首 30 秒优先在 4.3–4.6 秒语义边界切换，后续优先至少 6 秒；边界不足时宁可延长，不得硬切词语。

- [ ] **Step 4：实现 ImageProject 配音适配器**

复用 `narration.Produce` 的 `Synthesizer`、`Delivery` 和 timing document，但不调用绑定普通 Project 的 HTTP handler。voice profile 从服务端 Runtime 的 `TTSProvider`、`VolcSpeechSpeakerID`、`VolcSpeechResourceID` 及合成器版本生成；密钥仍只取 `VolcSpeechAPIKey`，不序列化。输入指纹：

```go
sha256(project.Script + "\x00" + voiceProfile + "\x00" + synthesizerVersion)
```

输出保存在 `<DataRoot>/image-projects/<projectID>/narration/`；job 保存相对路径、SHA、时长和 timing document SHA。重复作业命中同一指纹时复用；镜头重试不重复配音。字幕/SRT 可以作为内部对齐产物保存，但不得进入草稿轨道。

- [ ] **Step 5：测试复用和不截断**

Run: `go test ./internal/imagevideo ./internal/narration -count=1`

Expected: PASS。

- [ ] **Step 6：提交**

```powershell
git add internal/imagevideo/timing.go internal/imagevideo/timing_test.go internal/imagevideo/narration.go internal/imagevideo/narration_test.go
git commit -m "feat: plan image video scenes from narration"
```

### Task 4：实现 Grok 图生视频客户端

**Files:**
- Create: `internal/grokvideo/client.go`
- Create: `internal/grokvideo/client_test.go`
- Create: `internal/grokvideo/prompt.go`
- Create: `internal/grokvideo/prompt_test.go`

- [ ] **Step 1：写 httptest 失败测试**

服务端断言提交路径为 `/v1/videos/generations`，payload 只发送实测兼容字段。合同证据来自本地已完成样片的 `scratch/vox-editorial-test-20260819-01/video-requests.json`、`video-poll-responses.json` 和 `video-request-error-evidence.json`：

```json
{
  "model": "grok-imagine-video-1.5",
  "prompt": "...",
  "seconds": 6,
  "resolution": "480p",
  "input_reference": {"image_url": "data:image/png;base64,..."}
}
```

不得发送已被真实接口 422 拒绝的 `size=480x848` 或重复 `duration` 字段。测试还要覆盖 submit 返回 `request_id`/`id` 两种形状、`GET /v1/videos/{id}` 的 `pending/done/failed`、done 中 `video.url`、响应过大、取消、超时、下载非视频、错误脱敏和禁止把 API key 写进错误。

- [ ] **Step 2：运行测试确认 RED**

Run: `go test ./internal/grokvideo -count=1`

Expected: FAIL，package 不存在。

- [ ] **Step 3：实现客户端和安全下载**

```go
type Client interface {
    Submit(context.Context, SubmitRequest) (Request, error)
    Poll(context.Context, string) (Status, error)
    Download(context.Context, string, io.Writer) error
}
```

Base URL 使用现有 `settings.Runtime.GrokBaseURL`，凭据使用现有 `GrokAPIKey`，模型固定为视频模型而不复用 `GrokModel` 文本模型名；不新增公共 endpoint/key 字段。轮询间隔和最长等待由 worker 注入；下载沿用 `internal/imageproject/client.go` 的公共地址验证、响应体大小限制和私网地址防护。客户端接收 `*security.Redactor`，启动时注册 Grok key；持久化错误、日志、provider body 和 FFmpeg stderr 统一先 redactor 处理并限制长度。

- [ ] **Step 4：实现快速动效提示词**

提示词固定包含：0.5 秒内开始运动、3 秒内明显变化、标题位置/字形/拼写保持稳定、不新增文字/数字/logo、无慢动作、人物不说话；再按段落语义生成动作。测试断言提示词包含原始段落和图片标题，但不泄露路径、项目 ID 或接口信息。

- [ ] **Step 5：运行测试**

Run: `go test ./internal/grokvideo -count=1`

Expected: PASS。

- [ ] **Step 6：提交**

```powershell
git add internal/grokvideo
git commit -m "feat: add grok image to video client"
```

### Task 5：真实媒体探针、完整解码和 480p 标准化

**Files:**
- Create: `internal/imagevideo/media.go`
- Create: `internal/imagevideo/media_test.go`

- [ ] **Step 1：写命令参数和失败分类测试**

使用 fake `CommandRunner` 断言流程顺序：下载临时文件 → ffprobe JSON → ffmpeg 完整解码到 null → 标准化 → 再次 probe/完整解码 → 原子改名。标准化滤镜固定：

```text
scale=480:848:force_original_aspect_ratio=decrease,
pad=480:848:(ow-iw)/2:(oh-ih)/2:color=black,
fps=24
```

输出参数固定 `-c:v libx264 -pix_fmt yuv420p -movflags +faststart -an`。测试 `400x736` 输入被等比缩放/补边，不能使用直接 `scale=480:848` 拉伸；探针失败、完整解码失败、零帧、无视频轨、时长不足均返回可重试媒体错误。

- [ ] **Step 2：运行测试确认 RED**

Run: `go test ./internal/imagevideo -run 'TestMedia|TestNormalize|TestDecode' -count=1`

Expected: FAIL。

- [ ] **Step 3：实现 MediaProcessor**

```go
type MediaProcessor interface {
    NormalizeAndVerify(ctx context.Context, sourcePath, destinationPath string, requestedSeconds int) (MediaProbe, error)
}
```

从 `settings.Runtime.FFmpegPath/FFprobePath` 构造，路径为空返回稳定错误 `image_video_ffmpeg_not_configured`。成功结果必须为 480×848、24fps、H.264/yuv420p，可完整解码，实际时长允许一个帧间隔误差；记录真实 probe，不信供应商的 `duration` 元数据。

- [ ] **Step 4：运行测试**

Run: `go test ./internal/imagevideo -run 'TestMedia|TestNormalize|TestDecode' -count=1`

Expected: PASS。

- [ ] **Step 5：提交**

```powershell
git add internal/imagevideo/media.go internal/imagevideo/media_test.go
git commit -m "feat: validate image video media"
```

### Task 6：实现持久 worker、自动重试与不降级门

**Files:**
- Create: `internal/imagevideo/worker.go`
- Create: `internal/imagevideo/worker_test.go`
- Modify: `internal/imagevideo/model.go`

- [ ] **Step 1：写状态机失败测试**

```go
func TestWorkerRetriesRetryableFailureThreeTimesThenFailsJob(t *testing.T)
func TestWorkerDoesNotRetryCanceledOrPermanentValidationFailure(t *testing.T)
func TestWorkerNeverFallsBackFromImageToVideoToSlideshow(t *testing.T)
func TestWorkerLimitsProviderCallsToSixGlobally(t *testing.T)
func TestWorkerOnlyBuildsDraftAfterEveryItemSucceeds(t *testing.T)
func TestManualRetryStartsNewRoundForFailedItemsOnly(t *testing.T)
func TestWorkerResumesExpiredLeaseWithoutOverwritingSuccess(t *testing.T)
func TestWorkerResumesProviderDoneBeforeDownloadWithoutResubmitting(t *testing.T)
func TestWorkerCommitsExistingVerifiedOutputAfterCrashBeforeDatabaseCommit(t *testing.T)
func TestWorkerConvergesRunningJobWithNoRunnableItems(t *testing.T)
```

用 fake clock/sleeper/provider/media/draft，避免测试真实等待。自动 backoff 固定 2 秒、5 秒；取消和配置错误不盲目重试。供应商超时、429、5xx、失败状态、下载失败、媒体探针失败进入本轮下一 attempt。

- [ ] **Step 2：运行测试确认 RED**

Run: `go test ./internal/imagevideo -run 'TestWorker' -count=1`

Expected: FAIL。

- [ ] **Step 3：实现严格模式分派**

```go
switch job.OutputMode {
case ModeSlideshow:
    return w.buildSlideshowItem(ctx, claim)
case ModeImageToVideo:
    return w.generateVideoItem(ctx, claim)
default:
    return imagevideo.ErrInvalidOutputMode
}
```

禁止在任何 error 分支调用 slideshow。全局 semaphore 容量 6；claim 按 `created_at, ordinal` 排序，在多个 project 间轮转。attempt 开始前持久化，完成后先验证输出 SHA 再标 success。

- [ ] **Step 4：实现启动恢复和关闭**

worker 启动先 `RequeueExpired(now)`，然后消费 pending；关闭时停止领取新 item，等待当前调用在 context 超时内退出。`running` 但租约未过期的项不得被第二 worker 抢走。attempt 已保存 provider request ID 时，恢复后只继续 Poll/Download，不能再次 Submit；标准化输出已存在但数据库尚未提交时，先按预期 SHA、probe 和完整解码验证，匹配则补提交，不匹配才删除临时文件并重做。扫描结束后必须把“所有 items succeeded”的 job 推进到 draft，把“存在最终 failed 且无可重试 item”的 job 收敛到 failed，禁止 job 永久停在 running。

- [ ] **Step 5：运行 worker 与 race 测试**

Run: `go test -race ./internal/imagevideo ./internal/store -count=1`

Expected: PASS。

- [ ] **Step 6：提交**

```powershell
git add internal/imagevideo/worker.go internal/imagevideo/worker_test.go internal/imagevideo/model.go
git commit -m "feat: run durable image video jobs"
```

### Task 7：生成图片视频/图生视频剪映明文工作区

**Files:**
- Create: `internal/imagevideo/draft.go`
- Create: `internal/imagevideo/draft_test.go`
- Create: `scripts/image-video-draft/build_image_video_draft.py`
- Create: `scripts/image-video-draft/test_build_image_video_draft.py`

- [ ] **Step 1：写草稿计划和 Python 构建失败测试**

Go 测试断言计划只接受全部 success 的 ordered items；包含画布 1080×1920、旁白、图片/视频源、start/end、动效、转场、模板版本/指纹；`captions=[]`、`editable_titles=[]`。任一 failed/pending item 必须返回 `image_video_items_incomplete`。

Python 测试使用临时目录和小型假媒体，断言：

- 图片模式按时长写图片片段并只使用模板白名单动效。
- 图生视频模式写视频片段，只在时间轴上按旁白边界截取视频时长，不做画面空间裁切，也不改变语速。
- 时间线无缝、总时长覆盖旁白。
- 不创建字幕轨或文本轨。
- 输出 `draft_content.json`、`draft_meta_info.json`、`draft_cover.png` 和摘要。

- [ ] **Step 2：运行测试确认 RED**

Run: `go test ./internal/imagevideo -run 'TestDraft' -count=1`

Run: `python -m unittest discover -s scripts/image-video-draft -p "test_*.py"`

Expected: FAIL，构建器不存在。

- [ ] **Step 3：实现受控计划和构建命令**

`DraftBuilder.Build` 只允许输入/输出位于 `<DataRoot>/image-projects/<projectID>/jobs/<jobID>/`；使用 machine profile 中的 Python，参数通过 JSON 文件传递，不拼接 shell。Python 端复用已安装 pyJianYingDraft，生成新草稿 ID，不读取用户任意路径。

图片动效在前 30 秒轮换轻微放大、轻微缩小、左右平移，后续降低变化频率；转场只用淡入淡出或轻微左右切换。图生视频不再叠加大幅推拉。BGM/音效仅在内置模板明确配置并且资源存在时加入，缺失时返回明确模板资源错误，不静默换资源。

- [ ] **Step 4：运行 Go/Python 测试**

Run: `go test ./internal/imagevideo -run 'TestDraft' -count=1`

Run: `python -m unittest discover -s scripts/image-video-draft -p "test_*.py"`

Expected: PASS。

- [ ] **Step 5：提交**

```powershell
git add internal/imagevideo/draft.go internal/imagevideo/draft_test.go scripts/image-video-draft
git commit -m "feat: build image video jianying drafts"
```

### Task 8：复用 Registrar 并增加 ImageVideo 自有绑定校验

**Files:**
- Create: `internal/imagevideo/register.go`
- Create: `internal/imagevideo/register_test.go`
- Modify: `internal/imagevideo/draft.go`
- Modify: `internal/imagevideo/draft_test.go`
- Test: `internal/montage/registrar_test.go`
- Test: `internal/montage/coordinator_test.go`

- [ ] **Step 1：写 ImageVideo 受管绑定失败测试**

测试拒绝：manifest 不是 job 表绑定路径、manifest 的 `task_id/job_id` 不等于 job UUID、`output_dir` 不等于 job 根下 `output`、workspace 不等于 `output/workspace/<jobID>`、DataRoot 外路径、符号链接、缺少摘要、machine profile 或模板指纹不匹配、重复 job 不同草稿指纹。使用现有 `montage.Registrar` 的 fake runner 验证同一 job/同一指纹重复调用返回同一 receipt，且 Registrar 仍检查 `draft_content.json` 三方 SHA、目录哈希、root_meta 和无符号链接。

- [ ] **Step 2：运行测试确认 RED**

Run: `go test ./internal/montage ./internal/imagevideo -run 'TestRegistrar|TestImageVideoRegistration' -count=1`

Expected: FAIL。

- [ ] **Step 3：生成合法 manifest 并复用现有 Registrar**

```go
type RegistrationBinding struct {
    JobID string
    ProjectID string
    ManifestPath string
    OutputDir string
    WorkspaceDir string
    TemplateVersion string
    TemplateFingerprint string
    DraftFingerprint string
}
```

Task 7 的 draft builder 同时写入合法 `task_manifest.json`：`task_id == job_id`、二者均为 job UUID，`output_dir` 为 job 根下 `output`，`non_secret_settings.machine_profile_path` 和 `draft_display_name` 来自服务端受信配置；不含秘密。`register.go` 先从 `image_video_jobs` 读取并验证上述持久绑定，再用 `montage.ResolveTrustedRuntime` 得到 Python、剪映根、skill root 和 `run_montage_job.py`，构造现有 `montage.RegisterRequest` 调用 `Registrar.Register`。不要修改或绕过 `Coordinator.validateRetainedPaths`；普通 montage 仍走原 Coordinator，imagevideo 使用等强度的 job-bound validator。登记 attempt 和 receipt 指纹写入 imagevideo 自有表。

- [ ] **Step 4：实现独立登记重试**

媒体 job succeeded 后若登记失败，job 保持媒体成功并记录 `registration_status=failed`；重试只重新构建/登记草稿，不重新配音、生图或生成视频。

- [ ] **Step 5：运行 montage 与 imagevideo 测试**

Run: `go test ./internal/montage ./internal/imagevideo ./internal/httpapi -count=1`

Expected: PASS，既有 montage 登记测试无回归。

- [ ] **Step 6：提交**

```powershell
git add internal/imagevideo/register.go internal/imagevideo/register_test.go internal/imagevideo/draft.go internal/imagevideo/draft_test.go
git commit -m "feat: register image video drafts safely"
```

### Task 9：提供作业 API 并接入应用生命周期

**Files:**
- Create: `internal/httpapi/imagevideo_jobs.go`
- Create: `internal/httpapi/imagevideo_jobs_test.go`
- Modify: `internal/httpapi/imageprojects.go`
- Modify: `internal/httpapi/imageproject_quick.go`
- Modify: `internal/app/app.go`
- Modify: `internal/app/app_test.go`

- [ ] **Step 1：写 HTTP 失败测试**

覆盖：创建项目默认模式；创建时保存显式模式；锁定前 PATCH；锁定后 409；开始 job 校验图片全 ready、账号存在、配音/FFmpeg/`GrokBaseURL+GrokAPIKey` 配置；相同 idempotency key 返回同一 job；详情不返回 URL/key/路径；取消；只重试失败镜头；登记重试。`internal/app/app_test.go` 增加 `TestImageVideoRoutesRequireAuthentication` 和 `TestImageVideoMutationsRejectMissingCSRF`，逐个覆盖六条新路由。

失败响应统一：

```json
{"code":"image_video_items_failed","message":"3 个镜头生成失败，可只重试失败镜头","job_id":"...","item_ids":["..."]}
```

- [ ] **Step 2：运行测试确认 RED**

Run: `go test ./internal/httpapi ./internal/app -run 'TestImageVideo|TestImageProjectOutputMode' -count=1`

Expected: FAIL。

- [ ] **Step 3：实现路由**

```text
PATCH /api/image-projects/{id}/output-mode
POST  /api/image-projects/{id}/image-video-jobs
GET   /api/image-video-jobs/{id}
POST  /api/image-video-jobs/{id}/retry-failed
POST  /api/image-video-jobs/{id}/cancel
POST  /api/image-video-jobs/{id}/retry-registration
```

前端公开模型信息由 server 常量生成，只返回 `grok-imagine-video-1.5`、480×848、24fps、最大并发 6。Runtime 的 `GrokBaseURL/GrokAPIKey`、供应商 URL、本地文件路径均不序列化。handler/worker/provider 共用注入的 `security.Redactor`；HTTP 上游 body、worker 错误、FFprobe/FFmpeg stderr 在日志、数据库和 API 三层都经过脱敏与长度限制。

- [ ] **Step 4：接入 worker 生命周期**

`app.New` 构造一个 worker，服务启动时执行租约恢复并开始消费，关闭 context 时停止。测试用依赖注入 fake provider/media/draft/registration，不调用外部服务。

- [ ] **Step 5：运行后端测试**

Run: `go test ./internal/httpapi ./internal/app ./internal/imagevideo ./internal/grokvideo ./internal/store -count=1`

Expected: PASS。

- [ ] **Step 6：提交**

```powershell
git add internal/httpapi/imagevideo_jobs.go internal/httpapi/imagevideo_jobs_test.go internal/httpapi/imageprojects.go internal/httpapi/imageproject_quick.go internal/app/app.go internal/app/app_test.go
git commit -m "feat: expose image video job APIs"
```

### Task 10：增加前端模式选择、锁定、进度和失败重试

**Files:**
- Modify: `web/src/types.ts`
- Modify: `web/src/image-mode/QuickGenerateForm.tsx`
- Modify: `web/src/image-mode/QuickGenerateForm.test.tsx`
- Create: `web/src/image-mode/ImageVideoJobPanel.tsx`
- Create: `web/src/image-mode/ImageVideoJobPanel.test.tsx`
- Modify: `web/src/image-mode/ImageModeWorkbench.tsx`
- Modify: `web/src/image-mode/ImageModeWorkbench.test.tsx`
- Modify: `web/src/image-mode/image-mode.css`

- [ ] **Step 1：写 UI 失败测试**

断言：默认选中“图片视频”；可选择“图生视频”；quick payload 带 `output_mode`；图片全部 ready 后显示账号选择和只读模板 `jianying-image-video-v1`；开始后模式/账号禁用；刷新按服务端 job 恢复；自动重试显示“第 n/3 次”；最终失败显示镜头与错误及“只重试失败镜头”；点击只提交失败 job API；UI 和序列化对象中不出现 BaseURL、API key、供应商 URL。

- [ ] **Step 2：运行测试确认 RED**

Run: `npm --prefix web run test -- src/image-mode/QuickGenerateForm.test.tsx src/image-mode/ImageVideoJobPanel.test.tsx src/image-mode/ImageModeWorkbench.test.tsx`

Expected: FAIL。

- [ ] **Step 3：增加类型和独立 Panel**

```ts
export type ImageOutputMode = "image_slideshow" | "image_to_video";
export type ImageVideoJobStatus = "pending" | "running" | "succeeded" | "failed" | "canceled";

export interface ImageVideoJobItem {
  id: string;
  ordinal: number;
  status: ImageVideoJobStatus;
  retry_round: number;
  attempt: number;
  timeline_duration_seconds: number;
  requested_duration_seconds?: 6 | 10 | 15;
  actual_duration_seconds?: number;
  error_message?: string;
}
```

`ImageVideoJobPanel` 自己负责 job query 和 1500ms 轮询；只有 `pending/running` 轮询。账号列表调用现有 `/api/accounts`，不复用含“全部账号”的 `AccountSwitcher`。模板只读展示，不新增模板选择器。

- [ ] **Step 4：接入 quick 和详情页**

Quick 表单在风格/并发之前显示两张单选卡；项目详情在所有图片 ready 后显示 Panel。图片仍可预览/下载 ZIP；视频 job 不影响旧项目的这些操作。失败不显示“已完成”，成功后展示草稿路径的公开名称和登记状态。

- [ ] **Step 5：运行前端测试和类型检查**

Run: `npm --prefix web run typecheck`

Run: `npm --prefix web run test`

Expected: PASS。

- [ ] **Step 6：提交**

```powershell
git add web/src/types.ts web/src/image-mode/QuickGenerateForm.tsx web/src/image-mode/QuickGenerateForm.test.tsx web/src/image-mode/ImageVideoJobPanel.tsx web/src/image-mode/ImageVideoJobPanel.test.tsx web/src/image-mode/ImageModeWorkbench.tsx web/src/image-mode/ImageModeWorkbench.test.tsx web/src/image-mode/image-mode.css
git commit -m "feat: add image video mode controls"
```

### Task 11：端到端契约、嵌入构建和真实验收门

**Files:**
- Modify: `web/e2e/image-mode.spec.ts`
- Modify: `docs/ARCHITECTURE.md`
- Modify: `docs/AI-HANDOFF.md`
- Modify: `docs/USER-GUIDE.md`
- Modify: `internal/webui/dist/**`（由 `build:embed` 生成）
- Create: `docs/operations/image-video-jobs.md`
- Create: `internal/app/imagevideo_e2e_test.go`
- Create: `internal/imagevideo/grok_integration_test.go`

- [ ] **Step 1：写 E2E 失败用例**

Mock API 完成两条路径：

1. 图片视频：选择模式 → 选账号 → 开始 → 锁定 → succeeded → 显示已登记草稿。
2. 图生视频：两个 item，第一项成功、第二项自动三次后失败 → 不出现草稿 → 点击只重试失败镜头 → 新 round 成功 → 才显示草稿。

额外断言页面源码和响应 fixture 不含接口地址、key、供应商下载 URL。

- [ ] **Step 2：运行 E2E 确认 RED，再实现 fixture/交互直到 GREEN**

Run: `npm --prefix web run test:e2e -- image-mode.spec.ts`

Expected before fixture implementation: FAIL；完成后 PASS。

- [ ] **Step 3：更新文档**

文档明确旧 `/projects?mode=image-video` 与新 `/image-projects` 输出模式隔离；说明前端规则、FFmpeg 前置、失败重试、不降级、登记重试、无字幕/无可编辑标题，以及真实供应商可能返回 400×736 但系统会探针后标准化。

- [ ] **Step 4：运行完整后端验证**

Run: `go test ./... -count=1`

Run: `go vet ./...`

Expected: PASS，无 data race、无新增 vet 警告。

- [ ] **Step 5：运行完整前端验证**

Run: `npm --prefix web run typecheck`

Run: `npm --prefix web run test`

Run: `npm --prefix web run build:verify`

Expected: PASS。

- [ ] **Step 6：更新 exe 嵌入资源并校验**

Run: `npm --prefix web run build:embed`

Run: `.\scripts\check-embedded-dist.ps1 -Strict`

Run: `go build -o dist\video-production-console.next.exe .\cmd\console`

Expected: dist 同步、严格检查通过、旁路 exe 构建成功；不要覆盖可能正在运行的 `video-production-console.exe`。完成验收并停止旧进程后，才按现有发布流程替换正式 exe。

- [ ] **Step 7：使用 fake provider 做本地端到端验收**

在 `internal/app/imagevideo_e2e_test.go` 建立临时数据库、fake 配音、fake Grok、fake FFmpeg runner 和临时剪映根，创建一个 ImageProject，走完整配音、两个模式、失败重试、草稿构建和可信登记。检查数据库 attempt 历史、没有模式变化、没有部分草稿、登记 receipt 与三方 hash 一致。

Run: `go test ./internal/app -run TestImageVideoEndToEndWithFakeProvider -count=1`

Expected: PASS。

- [ ] **Step 8：使用真实接口做一条受控验收**

在带 `//go:build integration` 的 `internal/imagevideo/grok_integration_test.go` 中只从环境变量读取测试服务地址和密钥，缺失时跳过；测试不得打印或落盘它们。仅上传本次测试项目图片；固定 `grok-imagine-video-1.5`、480p、最大 6 并发。至少让 fake 前置 transport 制造一次可重试失败后再放行真实请求，确认自动 attempt 记录；若三次失败，状态必须停在 failed，不得生成图片视频草稿。真实成功片段用 FFprobe 和完整解码验证 480×848、24fps，再检查剪映草稿登记产物。

Precondition: 当前终端已通过本机密钥管理流程设置 `VIDEO_CONSOLE_TEST_VIDEO_BASE_URL` 和 `VIDEO_CONSOLE_TEST_VIDEO_API_KEY`，测试代码只读取、不打印这两个变量。

Run: `go test -tags=integration ./internal/imagevideo -run TestGrokImageToVideoIntegration -count=1`

Expected: PASS；测试输出和生成记录中不含 key、Authorization、base64 或供应商私有响应。命令中的值只在本机临时环境设置，不写入仓库、文档实例或日志。

- [ ] **Step 9：提交最终集成**

```powershell
git add web/e2e/image-mode.spec.ts docs/ARCHITECTURE.md docs/AI-HANDOFF.md docs/USER-GUIDE.md docs/operations/image-video-jobs.md internal/webui/dist
git commit -m "docs: document image video production"
```

## 最终验收清单

- [ ] `/image-projects` 默认图片视频，可选择图生视频；刷新后模式不漂移。
- [ ] 作业开始后模式、账号、内置模板版本与指纹锁定。
- [ ] 图片视频前 30 秒单图目标 4.3–4.6 秒，后续至少 6 秒；不截断口播。
- [ ] 图生视频只用 6/10/15 秒、480p、最多 6 并发；真实媒体统一为 480×848/24fps。
- [ ] 每轮最多 3 次 attempt；手动重试只开新 round 处理失败镜头。
- [ ] 任何失败都不会切换到图片视频，也不会生成部分失败草稿。
- [ ] 无字幕轨、无可编辑标题；图片内中文标题不被标准化过程裁掉或拉伸。
- [ ] 全部成功才构建草稿；可信登记失败可独立重试，不重新生成媒体。
- [ ] 前端、日志、数据库公开错误和产物清单不含 API key、Authorization 或 base64。
- [ ] Go、race、vet、Vitest、Playwright、embed strict、exe build 和真机草稿发现全部通过。
