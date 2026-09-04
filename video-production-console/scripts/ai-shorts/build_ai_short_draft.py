# -*- coding: utf-8 -*-
"""AI 短片 → 剪映草稿。

输入一个 job.json：
{
  "draft_name": "云中观局_拿幻想当出路_0902-1530",
  "jianying_root": "C:/Users/.../com.lveditor.draft",
  "workspace_parent": "<assetDir>/draft",        # 草稿先在这里生成，再搬进剪映目录
  "headline": "拿幻想当出路，最终沦为他人的养料",
  "duration_s": 42.5,
  "shots": [{"video": ".../shot_01.mp4", "start_s": 0.0, "end_s": 4.2,
             "audio": ".../voice_01.mp3", "video_volume": 0.15, "speaker": "旁白"},   # 旁白镜：TTS + 压低的环境音
            {"video": ".../shot_02.mp4", "start_s": 4.2, "end_s": 10.2,
             "video_volume": 1.0, "speaker": "狐厨"}, ...],                          # 角色镜：视频自带台词声轨
  "captions": [{"text": "族谱上写着", "start_s": 0.0, "end_s": 1.3}, ...],
  "style": {"caption_font": "新青年体", "caption_size": 15, "caption_color": [r,g,b],
            "headline_font": "新青年体", "headline_size": 11, "headline_color": [r,g,b]}
}
输出：把草稿注册进剪映（复制目录 + 更新 root_meta_info.json），打印 JSON 结果。
"""
from __future__ import annotations

import json
import os
import shutil
import subprocess
import sys
import time
import uuid
from pathlib import Path

import pyJianYingDraft as draft


def us(seconds: float) -> int:
    return int(round(float(seconds) * 1_000_000))


def read_json(path: Path):
    return json.loads(path.read_text(encoding="utf-8"))


def write_json(path: Path, value) -> None:
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2), encoding="utf-8")


def stop_jianying() -> None:
    """注册前停掉剪映，否则它会把 root_meta_info 写回旧值。"""
    if os.name == "nt":
        subprocess.run(["taskkill", "/IM", "JianyingPro.exe", "/F"], capture_output=True)
        time.sleep(1.0)


def font_of(name: str):
    name = (name or "").strip()
    if name and name in draft.FontType.__members__:
        return draft.FontType[name]
    return None


def hex_rgb(value: str):
    """'#RRGGBB' → (r, g, b) 0～1；不合法就白色。"""
    s = (value or "").strip().lstrip("#")
    if len(s) != 6:
        return (1.0, 1.0, 1.0)
    try:
        return tuple(int(s[i:i + 2], 16) / 255 for i in (0, 2, 4))
    except ValueError:
        return (1.0, 1.0, 1.0)


# 图片镜的推拉平移：(起始缩放, 结束缩放, 起始 x 偏移, 结束 x 偏移)。幅度小，中老年看着不晕。
CAMERA_MOVES = {
    "zoom_in": (1.03, 1.10, 0.0, 0.0),
    "zoom_out": (1.10, 1.03, 0.0, 0.0),
    "pan_left": (1.06, 1.06, 0.015, -0.015),
    "pan_right": (1.06, 1.06, -0.015, 0.015),
}


def add_camera_move(segment, move: str, duration: int) -> None:
    scale_from, scale_to, x_from, x_to = CAMERA_MOVES.get(move, CAMERA_MOVES["zoom_in"])
    segment.add_keyframe(draft.KeyframeProperty.uniform_scale, 0, scale_from)
    segment.add_keyframe(draft.KeyframeProperty.uniform_scale, duration, scale_to)
    segment.add_keyframe(draft.KeyframeProperty.position_x, 0, x_from)
    segment.add_keyframe(draft.KeyframeProperty.position_x, duration, x_to)


# ===== 竖版内嵌布局（借用混剪验证过的数值） =====
# 9:16 画布，16:9 画面塞进中间 1080×730 的窗口：素材"贴宽"后 607 高，×1.2 = 729 正好填满窗口。
PORTRAIT_W, PORTRAIT_H = 1080, 1920
WINDOW_H = 730
INSET_BASE = 1.20
# 推拉：在 1.20～1.26 之间，横移 ±0.02（混剪 image_motion 边界：scale 1.18～1.30，pan ≤0.03）。
INSET_MOVES = {
    "zoom_in": (1.20, 1.26, 0.0, 0.02),
    "zoom_out": (1.26, 1.20, 0.0, -0.02),
    "pan_left": (1.23, 1.23, 0.02, -0.02),
    "pan_right": (1.23, 1.23, -0.02, 0.02),
}
NARRATION_VOLUME = 1.7783  # +5 dB，混剪同款


def add_inset_move(segment, move: str, duration: int) -> None:
    scale_from, scale_to, x_from, x_to = INSET_MOVES.get(move, INSET_MOVES["zoom_in"])
    segment.add_keyframe(draft.KeyframeProperty.uniform_scale, 0, scale_from)
    segment.add_keyframe(draft.KeyframeProperty.uniform_scale, duration, scale_to)
    if x_from != x_to:
        segment.add_keyframe(draft.KeyframeProperty.position_x, 0, x_from)
        segment.add_keyframe(draft.KeyframeProperty.position_x, duration, x_to)


def make_frame(background: str, out_dir: Path) -> str:
    """把账号 9:16 背景图做成中间挖空 1080×730 透明窗的框，盖在画面上面；没有背景图就用深色底。"""
    from PIL import Image

    out_dir.mkdir(parents=True, exist_ok=True)
    if background and Path(background).is_file():
        img = Image.open(background).convert("RGBA").resize((PORTRAIT_W, PORTRAIT_H), Image.LANCZOS)
    else:
        img = Image.new("RGBA", (PORTRAIT_W, PORTRAIT_H), (18, 20, 28, 255))
    top = (PORTRAIT_H - WINDOW_H) // 2
    hole = Image.new("RGBA", (PORTRAIT_W, WINDOW_H), (0, 0, 0, 0))
    img.paste(hole, (0, top))
    path = out_dir / "frame.png"
    img.save(path)
    return str(path)


def bgm_windows(total_us: int, bgm: dict) -> list:
    """BGM 循环：先放开头一遍（到 usable_head），再反复副歌段，直到铺满。返回 (target_start, src_start, length)。"""
    head = us(bgm.get("usable_head_s") or 0)
    climax_start = us(bgm.get("climax_start_s") or 0)
    climax_len = us(bgm.get("climax_duration_s") or 0)
    duration = us(bgm.get("duration_s") or 0)
    if head <= 0:
        head = duration or total_us
    if climax_len <= 0 or climax_start + climax_len > (duration or head):
        climax_start, climax_len = 0, head
    windows = []
    cursor = 0
    first = min(head, total_us)
    windows.append((0, 0, first))
    cursor = first
    guard = 0
    while cursor < total_us and guard < 200:
        length = min(climax_len, total_us - cursor)
        if length <= 0:
            break
        windows.append((cursor, climax_start, length))
        cursor += length
        guard += 1
    return windows


def build(job: dict) -> dict:
    parent = Path(job["workspace_parent"])
    parent.mkdir(parents=True, exist_ok=True)
    job_id = uuid.uuid4().hex
    workspace = parent / job_id
    if workspace.exists():
        shutil.rmtree(workspace)
    portrait = (job.get("layout") or "") == "portrait_inset"
    if portrait:
        script = draft.DraftFolder(str(parent)).create_draft(job_id, PORTRAIT_W, PORTRAIT_H, 30, allow_replace=False)
    else:
        script = draft.DraftFolder(str(parent)).create_draft(job_id, 1920, 1080, 30, allow_replace=False)

    shots = job["shots"]
    total_us = us(job.get("duration_s") or (shots[-1]["end_s"] if shots else 0))
    brand = job.get("brand") or {}

    # 画面轨：每镜一段。视频镜：角色镜声轨全开（角色在里面说话），旁白镜片段比配音长就截、
    # 比配音短就放慢速度铺满，声轨压低只留环境音。图片镜：一张图铺满这镜，用关键帧做推拉平移。
    video_track = script.append_track(draft.TrackSpec(draft.TrackType.video, "画面"))
    first_visual = True
    for shot in shots:
        start = us(shot["start_s"])
        duration = us(shot["end_s"]) - start
        if duration <= 0:
            continue  # 零长片段不上轨，否则会和下一段重叠
        image_path = (shot.get("image") or "").strip()
        if image_path:
            material = draft.VideoMaterial(image_path)
            kwargs = {"source_timerange": draft.Timerange(0, duration), "volume": 0}
            if portrait:
                kwargs["clip_settings"] = draft.ClipSettings(scale_x=INSET_BASE, scale_y=INSET_BASE)
            segment = draft.VideoSegment(material, draft.Timerange(start, duration), **kwargs)
            if portrait:
                # 不加转场：pyJianYingDraft 内置的叠化会让剪映重新整理 Cache\effect，
                # 把混剪机器模板里登记的转场缓存路径顶掉（实测过一次）。硬切 + 推拉足够。
                add_inset_move(segment, shot.get("camera_move") or "", duration)
            else:
                add_camera_move(segment, shot.get("camera_move") or "", duration)
            script.add_segment(segment, video_track)
            first_visual = False
            continue
        material = draft.VideoMaterial(str(shot["video"]))
        src_dur = int(getattr(material, "duration", 0) or 0)
        kwargs = {"volume": float(shot.get("video_volume", 0) or 0)}
        if src_dur and src_dur >= duration:
            kwargs["source_timerange"] = draft.Timerange(0, duration)
        elif src_dur:
            kwargs["source_timerange"] = draft.Timerange(0, src_dur)
            kwargs["speed"] = round(src_dur / duration, 4)
        if portrait:
            kwargs["clip_settings"] = draft.ClipSettings(scale_x=INSET_BASE, scale_y=INSET_BASE)
        script.add_segment(draft.VideoSegment(material, draft.Timerange(start, duration), **kwargs), video_track)
        first_visual = False

    # 背景框（竖版）：账号背景图挖窗后盖在画面上，铺满全片。
    if portrait:
        frame_path = make_frame((brand.get("background") or "").strip(), workspace / "generated_visuals")
        frame_track = script.append_track(draft.TrackSpec(draft.TrackType.video, "背景框架"))
        frame_material = draft.VideoMaterial(frame_path)
        script.add_segment(
            draft.VideoSegment(frame_material, draft.Timerange(0, total_us), source_timerange=draft.Timerange(0, total_us), volume=0),
            frame_track,
        )

    # 旁白轨：只有旁白镜有 TTS 片段。老的单文件 narration 也兼容。
    audio_track = script.append_track(draft.TrackSpec(draft.TrackType.audio, "旁白"))
    narration_volume = NARRATION_VOLUME if portrait else 1.0
    if job.get("narration"):
        narration = draft.AudioMaterial(str(job["narration"]))
        narration_us = int(getattr(narration, "duration", 0) or 0)
        script.add_segment(draft.AudioSegment(narration, draft.Timerange(0, narration_us or total_us), volume=narration_volume), audio_track)
        total_us = max(total_us, narration_us)
    else:
        for shot in shots:
            audio_path = (shot.get("audio") or "").strip()
            if not audio_path:
                continue
            clip = draft.AudioMaterial(audio_path)
            clip_us = int(getattr(clip, "duration", 0) or 0)
            start = us(shot["start_s"])
            slot = us(shot["end_s"]) - start
            length = min(clip_us, slot) if clip_us else slot
            if length <= 0:
                continue
            script.add_segment(draft.AudioSegment(clip, draft.Timerange(start, length), volume=1.0), audio_track)

    # BGM：先放头一遍再循环副歌段，开头 1.5 秒淡入、结尾 2 秒淡出，音量走混剪验证过的 -12 dB。
    bgm = brand.get("bgm") or {}
    bgm_path = (bgm.get("path") or "").strip()
    if bgm_path and Path(bgm_path).is_file():
        bgm_track = script.append_track(draft.TrackSpec(draft.TrackType.audio, "BGM"))
        bgm_material = draft.AudioMaterial(bgm_path)
        bgm_dur = int(getattr(bgm_material, "duration", 0) or 0)
        if bgm_dur and not bgm.get("duration_s"):
            bgm["duration_s"] = bgm_dur / 1_000_000
        windows = bgm_windows(total_us, bgm)
        volume = float(bgm.get("volume") or 0.2512)
        for idx, (target, src, length) in enumerate(windows):
            if length <= 0:
                continue
            seg = draft.AudioSegment(bgm_material, draft.Timerange(target, length), source_timerange=draft.Timerange(src, length), volume=volume)
            fade_in = 1_500_000 if idx == 0 else 0
            fade_out = 2_000_000 if idx == len(windows) - 1 else 0
            if fade_in or fade_out:
                seg.add_fade(min(fade_in, length // 2), min(fade_out, length // 2))
            script.add_segment(seg, bgm_track)

    # 音效：Go 侧已按开头/转折/结尾算好落点，这里只负责放上去（-8 dB）。
    sfx_list = [s for s in (brand.get("sfx") or []) if (s.get("path") or "").strip() and Path(s["path"]).is_file()]
    if sfx_list:
        sfx_track = script.append_track(draft.TrackSpec(draft.TrackType.audio, "音效"))
        prev_end = 0
        for s in sorted(sfx_list, key=lambda x: x["at_s"]):
            at = max(prev_end, us(s["at_s"]))
            mat = draft.AudioMaterial(s["path"])
            length = int(getattr(mat, "duration", 0) or 0) or 2_000_000
            length = min(length, total_us - at)
            if length <= 200_000:
                continue
            script.add_segment(draft.AudioSegment(mat, draft.Timerange(at, length), source_timerange=draft.Timerange(0, length), volume=float(s.get("volume") or 0.3981)), sfx_track)
            prev_end = at + length

    style = job.get("style") or {}
    # 顶部金句（全片）
    headline = (job.get("headline") or "").strip()
    if headline:
        head_track = script.append_track(draft.TrackSpec(draft.TrackType.text, "标题"))
        kwargs = {
            "style": draft.TextStyle(size=float(style.get("headline_size", 11)), color=tuple(style.get("headline_color", [1, 1, 1])), bold=True),
            "clip_settings": draft.ClipSettings(transform_y=0.78),
            "border": draft.TextBorder(color=(0, 0, 0), width=60.0),
        }
        if f := font_of(style.get("headline_font", "新青年体")):
            kwargs["font"] = f
        script.add_segment(draft.TextSegment(headline, draft.Timerange(0, total_us), **kwargs), head_track)

    # 字幕（跟旁白计时）。竖版：压在画面窗口底边，白字 + 底色块（截图那种），不描边；横版沿用旧样式。
    cap_track = script.append_track(draft.TrackSpec(draft.TrackType.text, "字幕"))
    prev_end = 0
    caption = brand.get("caption") or {}
    for cap in job.get("captions") or []:
        start = max(prev_end, us(cap["start_s"]))
        end = us(cap["end_s"])
        if end <= start:
            continue
        if portrait:
            # 一条字幕 = 一个完整分句，小字号、自动折行，最多两行；行宽留 0.86 给底色块边距。
            kwargs = {
                "style": draft.TextStyle(
                    size=float(caption.get("size") or 9), color=hex_rgb(caption.get("color") or "#FFFFFF"),
                    bold=True, align=1, auto_wrapping=True, max_line_width=0.86,
                ),
                # 窗口底边在 y=-0.38；字幕压在窗口内侧下沿。
                "clip_settings": draft.ClipSettings(transform_y=-0.30),
            }
            if caption.get("bg_color"):
                kwargs["background"] = draft.TextBackground(
                    color=str(caption["bg_color"]), alpha=float(caption.get("bg_alpha") or 0.92), style=1, round_radius=0.12,
                )
            else:
                kwargs["border"] = draft.TextBorder(color=(0, 0, 0), width=40.0)
            if f := font_of(caption.get("font") or "新青年体"):
                kwargs["font"] = f
        else:
            kwargs = {
                "style": draft.TextStyle(size=float(style.get("caption_size", 15)), color=tuple(style.get("caption_color", [1, 1, 1]))),
                "clip_settings": draft.ClipSettings(transform_y=-0.72),
                "border": draft.TextBorder(color=(0, 0, 0), width=50.0),
            }
            if f := font_of(style.get("caption_font", "新青年体")):
                kwargs["font"] = f
        script.add_segment(draft.TextSegment(cap["text"], draft.Timerange(start, end - start), **kwargs), cap_track)
        prev_end = end

    script.save()
    meta_path = workspace / "draft_meta_info.json"
    meta = read_json(meta_path)
    meta["draft_name"] = job["draft_name"]
    write_json(meta_path, meta)
    return {"workspace": str(workspace), "job_id": job_id, "duration_us": total_us}


def register(job: dict, built: dict) -> str:
    root = Path(job["jianying_root"])
    root_meta_path = root / "root_meta_info.json"
    if not root_meta_path.is_file():
        raise FileNotFoundError(f"root_meta_info.json not found under {root}")
    workspace = Path(built["workspace"])
    target = root / built["job_id"]
    stop_jianying()
    root_meta = read_json(root_meta_path)
    entries = root_meta.get("all_draft_store")
    if not isinstance(entries, list):
        raise ValueError("root_meta_info.all_draft_store must be a list")
    # 重新组装：撤掉上一版（只动本工具登记过、且仍在剪映目录下的那一条）。
    previous = (job.get("replace_draft") or "").strip()
    if previous:
        prev_path = Path(previous)
        if prev_path.parent.resolve() == root.resolve() and prev_path.is_dir():
            entries = [e for e in entries if str(e.get("draft_fold_path") or "").casefold() != prev_path.as_posix().casefold()]
            shutil.rmtree(prev_path, ignore_errors=True)
    shutil.copytree(workspace, target)
    meta_path = target / "draft_meta_info.json"
    meta = read_json(meta_path)
    meta["draft_fold_path"] = target.as_posix()
    meta["draft_root_path"] = root.as_posix()
    meta["draft_name"] = job["draft_name"]
    write_json(meta_path, meta)
    now_us = int(time.time() * 1_000_000)
    entry = {
        "cloud_draft_cover": False, "cloud_draft_sync": False, "draft_cloud_last_action_download": False,
        "draft_cloud_purchase_info": "", "draft_cloud_template_id": "", "draft_cloud_tutorial_info": "",
        "draft_cloud_videocut_purchase_info": "", "draft_cover": (target / "draft_cover.jpg").as_posix(),
        "draft_fold_path": target.as_posix(), "draft_id": str(meta.get("draft_id") or built["job_id"]),
        "draft_is_ai_shorts": False, "draft_is_ai_translate": False, "draft_is_invisible": False,
        "draft_is_pippit_draft": False, "draft_is_web_article_video": False,
        "draft_json_file": (target / "draft_content.json").as_posix(), "draft_name": job["draft_name"],
        "draft_new_version": str(meta.get("draft_new_version") or ""), "draft_root_path": root.as_posix(),
        "draft_timeline_materials_size": (target / "draft_content.json").stat().st_size, "draft_type": "",
        "draft_web_article_video_enter_from": "", "pippit_avatar_url": "", "pippit_extra_info": "",
        "pippit_id": "", "pippit_user_name": "", "streaming_edit_draft_ready": True,
        "tm_draft_cloud_completed": "", "tm_draft_cloud_entry_id": -1, "tm_draft_cloud_modified": 0,
        "tm_draft_cloud_parent_entry_id": -1, "tm_draft_cloud_space_id": -1, "tm_draft_cloud_user_id": -1,
        "tm_draft_create": now_us, "tm_draft_modified": now_us, "tm_draft_removed": 0,
        "tm_duration": int(built["duration_us"]),
    }
    backup = root_meta_path.with_suffix(f".ai-short-{built['job_id']}.bak.json")
    shutil.copy2(root_meta_path, backup)
    entries.insert(0, entry)
    root_meta["all_draft_store"] = entries
    root_meta["draft_ids"] = len(entries)
    write_json(root_meta_path, root_meta)
    return str(target)


def main() -> int:
    if len(sys.argv) < 2:
        print(json.dumps({"ok": False, "error": "usage: build_ai_short_draft.py job.json"}))
        return 2
    job = read_json(Path(sys.argv[1]))
    try:
        built = build(job)
        target = register(job, built)
    except Exception as exc:  # noqa: BLE001
        print(json.dumps({"ok": False, "error": f"{type(exc).__name__}: {exc}"}, ensure_ascii=False))
        return 1
    print(json.dumps({"ok": True, "draft_path": target, "duration_us": built["duration_us"]}, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    sys.exit(main())
