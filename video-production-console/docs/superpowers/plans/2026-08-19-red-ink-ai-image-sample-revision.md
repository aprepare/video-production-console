# Red Ink AI Image Sample Revision Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Generate 13 finance-oriented Chinese red-ink images and register a new 58.321406-second editable Jianying sample with slower 4.3–4.7-second shots, smaller baked-in Chinese titles, no captions, and the existing narration/audio package.

**Architecture:** Keep this as an isolated one-off sample under `scratch/ai-image-sample-20260819/red-ink-sample-a919584`. Use the approved batch-image client for 13 independent `gpt-image-2` requests, adapt the already verified V2 draft builder to 13 full-screen images and gentler keyframes, validate the plaintext draft without OCR or aesthetic inspection, then register one new draft through the existing managed Jianying registration lock.

**Tech Stack:** Python 3, Pillow, `pyJianYingDraft`, `chatgpt2api-batch-image/scripts/batch_generate.py`, Jianying montage registration helpers, PowerShell, JSON.

---

## File map

- Create `scratch/ai-image-sample-20260819/red-ink-prompts.json`: 13 complete image requests with exact allowed Chinese text.
- Create `scratch/ai-image-sample-20260819/build_red_ink_draft.py`: one-off 13-shot draft builder derived from the verified V2 sample builder.
- Create `scratch/ai-image-sample-20260819/validate_red_ink_draft.py`: engineering-only validator for timing, media, tracks, motion, audio and transitions.
- Create `scratch/ai-image-sample-20260819/red-ink-sample-a919584/`: generated images, prepared media, workspace and build summary.
- Create `video-console-data/projects/289e99a2-c847-471b-ac33-27c62ef657ee/ai-image-video-sample-red-ink-20260819/`: validation and registration reports.
- Reuse `scratch/ai-image-sample-20260819/v2-profile.json`, `scratch/ai-image-sample-20260819/v2-policy.json` and `scratch/ai-image-sample-20260819/register_sample_draft.py` without changing shared product code.

### Task 1: Prepare and preflight the 13 image prompts

**Files:**
- Create: `scratch/ai-image-sample-20260819/red-ink-prompts.json`
- Read: `docs/superpowers/specs/2026-08-19-red-ink-ai-image-sample-revision-design.md`

- [ ] **Step 1: Create the prompt input with 13 independent requests**

Use this exact top-level structure and preserve the listed titles and allowed Chinese. Every `prompt` must be complete because the batch tool sends requests independently.

```json
{
  "prompts": [
    {"id":"red-ink-01","title":"九月财富浪潮","size":"1024x1536","quality":"auto","prompt":"9:16竖屏，当代中国财经赤墨插画，暖白宣纸肌理，墨黑城市与山水，朱砂红日和财富浪潮，少量低饱和暗金光线，一位抬头望向浪潮的中国中老年人物，主体清楚、元素丰富、全屏构图、底部不预留字幕区。准确生成两处中文：小字‘告诉你一个好消息’，主标题‘9月财富浪潮来了’。主标题最多两行，宽度约占画面50%，整体高度不超过画面14%，放在中上部留白并与朱砂笔触融合，禁止居中满屏大字。除这两处指定中文外，不得出现任何其他文字、字母、数字、水印、二维码或Logo。"},
    {"id":"red-ink-02","title":"多数人未知","size":"1024x1536","quality":"auto","prompt":"9:16竖屏，当代中国财经赤墨插画，暖白宣纸肌理，墨黑人群向前行走，只有一位中国中老年人物被朱砂红光照亮，背景有墨色城市和暗金信息流，焦点明确、画面丰富、全屏铺满、底部不留字幕区。准确生成中文主标题‘99%的人还不知道’，最多两行，宽度约占画面50%，高度不超过画面11%，置于中上部留白，不使用满屏大字。除这句指定中文外，不得出现任何其他文字、字母、数字、水印、二维码或Logo。"},
    {"id":"red-ink-03","title":"十二条忠告","size":"1024x1536","quality":"auto","prompt":"9:16竖屏，当代中国财经赤墨插画，暖白宣纸卷轴展开，十二道朱砂印记沿墨线向远方延伸，墨黑山势、人物剪影和少量暗金节点组成财经寓意，层次丰富、全屏构图、底部不留字幕区。准确生成中文主标题‘12条黄金忠告’，最多两行，宽度约占画面48%，高度不超过画面10%，放在中上部宣纸留白。除这句指定中文外，不得出现任何其他文字、字母、数字、水印、二维码或Logo。"},
    {"id":"red-ink-04","title":"资产翻十倍","size":"1024x1536","quality":"auto","prompt":"9:16竖屏，当代中国财经赤墨插画，普通中国中老年人走上墨色山阶，朱砂红色上升光路贯穿画面，暗金节点向高处聚拢，赤日、城市和云海形成纵深，强烈但不俗艳，全屏构图、底部不留字幕区。准确生成中文主标题‘一年资产翻十倍’，最多两行，宽度约占画面52%，高度不超过画面11%，置于中上部且不遮挡人物。除这句指定中文外，不得出现任何其他文字、字母、数字、水印、二维码或Logo。"},
    {"id":"red-ink-05","title":"一台奔驰的价值","size":"1024x1536","quality":"auto","prompt":"9:16竖屏，当代中国财经赤墨插画，无品牌豪华轿车剪影停在宣纸道路尽头，墨黑城市、朱砂价值刻度和少量暗金光芒围绕，画面面向中老年受众，直观、有冲击力但不浮夸，全屏铺满、底部不留字幕区。准确生成中文主标题‘价值一台奔驰’，最多两行，宽度约占画面48%，高度不超过画面10%，放在中上部留白。除这句指定中文外，不得出现任何其他文字、字母、数字、水印、二维码、品牌车标或Logo。"},
    {"id":"red-ink-06","title":"赚钱像喝水","size":"1024x1536","quality":"auto","prompt":"9:16竖屏，当代中国财经赤墨插画，墨色水流注入透明杯盏，朱砂涟漪与暗金折光向外扩散，旁边有普通中国人物的手和简洁财富流动意象，宣纸肌理清晰、全屏构图、底部不留字幕区。准确生成中文主标题‘赚钱像喝水一样’，最多两行，宽度约占画面50%，高度不超过画面10%，置于中上部留白。除这句指定中文外，不得出现任何其他文字、字母、数字、水印、二维码或Logo。"},
    {"id":"red-ink-07","title":"大数据精准","size":"1024x1536","quality":"auto","prompt":"9:16竖屏，当代中国财经赤墨插画，无数墨点和暗金数据流汇向一部无界面文字的手机轮廓，朱砂红光圈锁定一位中国中老年人物，背景是墨色城市与网络线条，元素丰富、焦点清楚、全屏构图、底部不留字幕区。准确生成中文主标题‘大数据非常精准’，最多两行，宽度约占画面50%，高度不超过画面10%，放在中上部。除这句指定中文外，不得出现任何其他文字、字母、数字、水印、二维码、应用界面或Logo。"},
    {"id":"red-ink-08","title":"八方来财","size":"1024x1536","quality":"auto","prompt":"9:16竖屏，当代中国财经赤墨插画，八方朱砂红色光路从山河和城市汇入中央赤日，普通中国家庭站在汇聚点，少量暗金财富节点环绕，构图宏大但清晰、全屏铺满、底部不留字幕区。准确生成中文主标题‘2026八方来财’，最多两行，宽度约占画面50%，高度不超过画面11%，置于中上部留白。除这句指定中文外，不得出现任何其他文字、字母、数字、水印、二维码或Logo。"},
    {"id":"red-ink-09","title":"健康富有幸福","size":"1024x1536","quality":"auto","prompt":"9:16竖屏，当代中国财经赤墨插画，中国三代家庭在暖白宣纸场景中相聚，墨竹、墨色远山、朱砂红日和暗金灯火环绕，人物亲切真实，面向中老年受众，温暖而有层次、全屏构图、底部不留字幕区。准确生成中文主标题‘健康·富有·幸福’，宽度约占画面52%，高度不超过画面10%，置于中上部留白。除这句指定中文外，不得出现任何其他文字、字母、数字、水印、二维码或Logo。"},
    {"id":"red-ink-10","title":"点亮好运","size":"1024x1536","quality":"auto","prompt":"9:16竖屏，当代中国财经赤墨插画，一只手轻点由朱砂墨迹形成的小爱心，祝福墨纹、上升光点、红日和温暖家庭灯火向外扩散，画面简洁而有冲击力、全屏铺满、底部不留字幕区。准确生成中文主标题‘点亮好运’，单行或两行，宽度约占画面42%，高度不超过画面9%，放在中上部且不遮挡手部。除这句指定中文外，不得出现任何其他文字、字母、数字、水印、二维码、应用界面或Logo。"},
    {"id":"red-ink-11","title":"内容随时消失","size":"1024x1536","quality":"auto","prompt":"9:16竖屏，当代中国财经赤墨插画，一页宣纸内容和无文字手机轮廓正在化为墨粒与朱砂碎片，暗金光线逐渐熄灭，墨黑山形与红色裂隙营造紧迫感，元素丰富、全屏构图、底部不留字幕区。准确生成中文主标题‘内容可能随时消失’，最多两行，宽度约占画面55%，高度不超过画面11%，置于中上部留白。除这句指定中文外，不得出现任何其他文字、字母、数字、水印、二维码、应用界面或Logo。"},
    {"id":"red-ink-12","title":"转给重要的人","size":"1024x1536","quality":"auto","prompt":"9:16竖屏，当代中国财经赤墨插画，两位中国亲友并肩查看一部无界面文字的手机，朱砂红线连接两人和远处家庭灯火，墨色山水与暗金暖光形成温情层次，全屏铺满、底部不留字幕区。准确生成中文主标题‘转给最重要的人’，最多两行，宽度约占画面50%，高度不超过画面10%，放在中上部留白。除这句指定中文外，不得出现任何其他文字、字母、数字、水印、二维码、应用界面或Logo。"},
    {"id":"red-ink-13","title":"普通人的机会","size":"1024x1536","quality":"auto","prompt":"9:16竖屏，当代中国财经赤墨插画，普通中国中老年人物登上墨色高峰，云层打开朱砂红与暗金交织的道路，远处赤日、山河和城市形成宏大收束，画面充满希望与行动感、全屏构图、底部不留字幕区。准确生成中文主标题‘普通人的翻身机会’，最多两行，宽度约占画面55%，高度不超过画面11%，置于中上部且不遮挡人物。除这句指定中文外，不得出现任何其他文字、字母、数字、水印、二维码或Logo。"}
  ]
}
```

- [ ] **Step 2: Validate prompt count and required prompt fields without reviewing image content**

Run:

```powershell
$p = Get-Content -LiteralPath 'scratch/ai-image-sample-20260819/red-ink-prompts.json' -Raw | ConvertFrom-Json
if ($p.prompts.Count -ne 13) { throw "Expected 13 prompts" }
if (@($p.prompts | Where-Object { -not $_.title -or -not $_.prompt }).Count -ne 0) { throw "Missing title or prompt" }
```

Expected: exit code `0`, no output.

- [ ] **Step 3: Run the required batch dry-run**

Run:

```powershell
python 'C:/Users/prepare/.codex/skills/chatgpt2api-batch-image/scripts/batch_generate.py' --prompts 'scratch/ai-image-sample-20260819/red-ink-prompts.json' --output 'scratch/ai-image-sample-20260819/red-ink-sample-a919584/images' --dry-run
```

Expected: `13 requests` and wave plan `12 -> 1`; no output directory or network request.

### Task 2: Generate the 13 red-ink images

**Files:**
- Create: `scratch/ai-image-sample-20260819/red-ink-sample-a919584/images/*`
- Create: `scratch/ai-image-sample-20260819/red-ink-sample-a919584/images.state/prompts.json`
- Create: `scratch/ai-image-sample-20260819/red-ink-sample-a919584/images.state/manifest.json`

- [ ] **Step 1: Run the formal batch once**

Run:

```powershell
python 'C:/Users/prepare/.codex/skills/chatgpt2api-batch-image/scripts/batch_generate.py' --prompts 'scratch/ai-image-sample-20260819/red-ink-prompts.json' --output 'scratch/ai-image-sample-20260819/red-ink-sample-a919584/images'
```

Expected: exit code `0` and `13/13` success. Do not open, OCR, score or visually inspect the returned images.

- [ ] **Step 2: Resume only if the first batch reports failures**

Run only when the previous command exits `2`:

```powershell
python 'C:/Users/prepare/.codex/skills/chatgpt2api-batch-image/scripts/batch_generate.py' --prompts 'scratch/ai-image-sample-20260819/red-ink-prompts.json' --output 'scratch/ai-image-sample-20260819/red-ink-sample-a919584/images' --resume
```

Expected: all failed requests recovered without regenerating successful files.

- [ ] **Step 3: Verify only engineering properties**

Run:

```powershell
$files = Get-ChildItem -LiteralPath 'scratch/ai-image-sample-20260819/red-ink-sample-a919584/images' -File
if ($files.Count -ne 13) { throw "Expected 13 generated image files" }
```

Expected: exit code `0`. This is not permission to inspect text accuracy or aesthetics.

### Task 3: Build the isolated 13-shot plaintext draft

**Files:**
- Create: `scratch/ai-image-sample-20260819/build_red_ink_draft.py`
- Reuse: `scratch/ai-image-sample-20260819/build_sample_draft_v2.py`
- Reuse: `scratch/ai-image-sample-20260819/v2-profile.json`
- Reuse: `scratch/ai-image-sample-20260819/v2-policy.json`

- [ ] **Step 1: Copy the verified one-off builder to a red-ink-specific file**

Run:

```powershell
Copy-Item -LiteralPath 'scratch/ai-image-sample-20260819/build_sample_draft_v2.py' -Destination 'scratch/ai-image-sample-20260819/build_red_ink_draft.py'
```

Expected: the destination exists and the existing V2 builder remains unchanged.

- [ ] **Step 2: Replace the shot contract and motion table**

In `build_red_ink_draft.py`, use these exact constants:

```python
SCENE_BOUNDARIES = [
    0.0, 4.3, 8.7, 13.2, 17.6, 22.1, 26.6,
    31.0, 35.5, 40.0, 44.6, 49.2, 53.8, 58.321406,
]
SCENE_STATES = ["B"] * 13
```

Replace the image-count guard with:

```python
if len(files) != 13:
    raise ValueError(f"expected 13 source images, got {len(files)}")
```

Replace the motion list with exactly 13 subtle moves:

```python
motion = [
    (1.00, 1.035, 0.000, 0.006),
    (1.025, 1.000, -0.006, 0.006),
    (1.00, 1.030, 0.000, 0.000),
    (1.01, 1.040, 0.000, 0.006),
    (1.025, 1.000, 0.006, -0.006),
    (1.00, 1.030, -0.006, 0.006),
    (1.03, 1.005, 0.000, -0.006),
    (1.00, 1.035, 0.000, 0.006),
    (1.025, 1.000, 0.006, -0.006),
    (1.00, 1.030, 0.000, 0.000),
    (1.025, 1.000, -0.006, 0.006),
    (1.00, 1.035, 0.000, 0.006),
    (1.01, 1.040, 0.000, 0.000),
]
```

In `patch_verified_resources`, require `13` main visual segments and attach transitions to segments 2–13. In the build summary, write `scenes: 13`, `captions: 0`, `sfx: 4`, and `transitions: 12`. Keep `prepare_b` returning the fitted RGB frame directly; do not call `dark_bottom`, draw text or reserve a lower region.

- [ ] **Step 3: Build with the fixed job and display identity**

Run:

```powershell
python 'scratch/ai-image-sample-20260819/build_red_ink_draft.py' `
  --images 'scratch/ai-image-sample-20260819/red-ink-sample-a919584/images' `
  --narration 'video-console-data/projects/289e99a2-c847-471b-ac33-27c62ef657ee/ai-image-video-sample-v1-4075b4d3/media/narration.mp3' `
  --captions 'scratch/ai-image-sample-20260819/captions/captions.json' `
  --srt 'scratch/ai-image-sample-20260819/captions/captions.srt' `
  --script 'scratch/ai-image-sample-20260819/continuous-script.txt' `
  --profile 'scratch/ai-image-sample-20260819/v2-profile.json' `
  --policy 'scratch/ai-image-sample-20260819/v2-policy.json' `
  --output 'scratch/ai-image-sample-20260819/red-ink-sample-a919584/build' `
  --job-id 'red-ink-sample-a919584' `
  --display-name 'AI配图样片_财富浪潮_赤墨版'
```

Expected: `status=built`, `duration_us=58321406`, `scenes=13`, `captions=0`, `sfx=4`, `transitions=12`.

### Task 4: Validate the plaintext draft without visual image review

**Files:**
- Create: `scratch/ai-image-sample-20260819/validate_red_ink_draft.py`
- Create: `video-console-data/projects/289e99a2-c847-471b-ac33-27c62ef657ee/ai-image-video-sample-red-ink-20260819/draft.validation.json`

- [ ] **Step 1: Copy the existing engineering validator**

Run:

```powershell
Copy-Item -LiteralPath 'scratch/ai-image-sample-20260819/validate_sample_draft.py' -Destination 'scratch/ai-image-sample-20260819/validate_red_ink_draft.py'
```

- [ ] **Step 2: Replace its shot-specific checks**

The red-ink validator must implement these exact predicates:

```python
checks["scene_count_13"] = len(scenes) == 13
checks["shots_between_4_3_and_4_7s"] = all(
    4_300_000 <= int(s["target_timerange"]["duration"]) <= 4_700_000
    for s in scenes
)
checks["video_materials_exist"] = (
    len(videos) == 13
    and all(Path(item.get("path") or "").is_file() for item in videos)
)
checks["verified_transitions_12"] = (
    len(transitions) == 12
    and all(
        item.get("effect_id") == "322577"
        and item.get("resource_id") == "6724845717472416269"
        and int(item.get("duration") or 0) == 466666
        and item.get("is_overlap") is True
        for item in transitions
    )
    and all(
        any(ref in transition_ids for ref in segment.get("extra_material_refs", []))
        for segment in scenes[1:]
    )
)
checks["no_text_tracks"] = not any(
    track.get("type") == "text" or track.get("name") in ("字幕", "章节标题", "品牌条", "自动字幕", "文稿匹配", "ASR")
    for track in content.get("tracks", [])
)
```

Retain the exact total-duration check `58_321_406`, canvas/version checks, contiguous coverage check, 1080×1920 prepared-image dimension check, Ken Burns keyframe check, one narration segment, one BGM segment, four SFX segments, and existence checks for all referenced audio paths. Do not add OCR, title-text parsing, screenshots, aesthetic scoring or image rejection.

- [ ] **Step 3: Run the validator**

Run:

```powershell
New-Item -ItemType Directory -Force -Path 'video-console-data/projects/289e99a2-c847-471b-ac33-27c62ef657ee/ai-image-video-sample-red-ink-20260819' | Out-Null
python 'scratch/ai-image-sample-20260819/validate_red_ink_draft.py' `
  'scratch/ai-image-sample-20260819/red-ink-sample-a919584/build/workspace/red-ink-sample-a919584' `
  --script 'scratch/ai-image-sample-20260819/continuous-script.txt' `
  --report 'video-console-data/projects/289e99a2-c847-471b-ac33-27c62ef657ee/ai-image-video-sample-red-ink-20260819/draft.validation.json'
Copy-Item -LiteralPath 'scratch/ai-image-sample-20260819/red-ink-sample-a919584/build/build-summary.json' -Destination 'video-console-data/projects/289e99a2-c847-471b-ac33-27c62ef657ee/ai-image-video-sample-red-ink-20260819/build-summary.json'
```

Expected: `status=pass`, 13 scenes, 12 transitions, zero captions/text tracks and no errors.

### Task 5: Register and independently verify the new Jianying draft

**Files:**
- Reuse: `scratch/ai-image-sample-20260819/register_sample_draft.py`
- Create: `video-console-data/projects/289e99a2-c847-471b-ac33-27c62ef657ee/ai-image-video-sample-red-ink-20260819/registration-verification.json`
- Create: `C:/Users/prepare/AppData/Local/JianyingPro/User Data/Projects/com.lveditor.draft/red-ink-sample-a919584/`

- [ ] **Step 1: Preflight identity and registration safety**

Run:

```powershell
$root = 'C:/Users/prepare/AppData/Local/JianyingPro/User Data/Projects/com.lveditor.draft'
if (Test-Path -LiteralPath (Join-Path $root 'red-ink-sample-a919584')) { throw 'Target directory already exists' }
$meta = Get-Content -LiteralPath (Join-Path $root 'root_meta_info.json') -Raw | ConvertFrom-Json
if (@($meta.all_draft_store | Where-Object { $_.draft_name -eq 'AI配图样片_财富浪潮_赤墨版' }).Count -ne 0) { throw 'Display name already exists' }
```

Expected: exit code `0`. Never delete or overwrite a matching target.

- [ ] **Step 2: Register under the managed registration lock**

Run:

```powershell
python 'scratch/ai-image-sample-20260819/register_sample_draft.py' `
  --skill-scripts 'C:/Users/prepare/.codex/skills/jianying-montage-draft/scripts' `
  --job-id 'red-ink-sample-a919584' `
  --workspace 'scratch/ai-image-sample-20260819/red-ink-sample-a919584/build/workspace/red-ink-sample-a919584' `
  --output-dir 'video-console-data/projects/289e99a2-c847-471b-ac33-27c62ef657ee/ai-image-video-sample-red-ink-20260819' `
  --jianying-root 'C:/Users/prepare/AppData/Local/JianyingPro/User Data/Projects/com.lveditor.draft' `
  --display-name 'AI配图样片_财富浪潮_赤墨版' `
  --cover 'scratch/ai-image-sample-20260819/red-ink-sample-a919584/build/media/scene-01-B.png'
```

Expected: the existing `ManagedRegistrationRunner` owns acquire, renewal and release in the same foreground process and returns `status=completed`. Do not launch Jianying.

- [ ] **Step 3: Write the registration verification report**

Read the post-registration `root_meta_info.json`, require exactly one matching display name, require the indexed directory and `draft_content.json` to exist, require all 13 image paths referenced by the registered draft to exist, and compute SHA-256 for source and registered `draft_content.json`. Write:

```json
{
  "status": "completed",
  "job_id": "red-ink-sample-a919584",
  "display_name": "AI配图样片_财富浪潮_赤墨版",
  "root_meta_unique_matches": 1,
  "registered_dir_exists": true,
  "source_registered_sha256_equal": true,
  "media_paths_referenced": 13,
  "media_paths_exist": 13,
  "ui_smoke_test": "not_performed",
  "image_qc": "not_performed_by_user_request"
}
```

Expected: all booleans `true`; report saved as `registration-verification.json` in the red-ink report directory.

### Task 6: Final verification and handoff

**Files:**
- Read: `draft.validation.json`
- Read: `build-summary.json`
- Read: `registration-verification.json`

- [ ] **Step 1: Re-run the plaintext validator after registration**

Run the Task 4 validation command again against the source workspace.

Expected: `status=pass` with unchanged checks.

- [ ] **Step 2: Confirm scope isolation**

Run:

```powershell
git status --short -- 'scratch/ai-image-sample-20260819' 'video-console-data/projects/289e99a2-c847-471b-ac33-27c62ef657ee'
git diff --name-only -- ':!scratch/**' ':!video-console-data/**'
```

Expected: red-ink work is confined to one-off sample/report paths; unrelated existing worktree changes remain untouched.

- [ ] **Step 3: Report the user-facing result**

Report the visible Jianying name `AI配图样片_财富浪潮_赤墨版`, 13-shot count, 4.3–4.7-second duration range, 58.321406-second total, zero caption/text tracks, narration/BGM/four SFX/twelve transitions, registered path and validation report path. Explicitly state that no OCR, Chinese-accuracy or aesthetic image QC was performed and Jianying UI was not launched.
