from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path

import pyJianYingDraft as draft


MOTION = {
    "subtle_zoom_in": (1.02, 1.08, 0.0, 0.0),
    "subtle_zoom_out": (1.08, 1.02, 0.0, 0.0),
    "subtle_pan_left": (1.04, 1.04, 0.012, -0.012),
    "subtle_pan_right": (1.04, 1.04, -0.012, 0.012),
}


def read_plan(path: Path) -> dict:
    data = json.loads(path.read_text(encoding="utf-8"))
    if data.get("output_mode") not in {"image_slideshow", "image_to_video"}:
        raise ValueError("invalid output mode")
    scenes = data.get("scenes") or []
    if not scenes:
        raise ValueError("draft plan has no scenes")
    previous = 0
    for scene in scenes:
        start = int(scene["start_us"])
        duration = int(scene["duration_us"])
        media = Path(scene["media_path"]).resolve()
        if start != previous or duration <= 0 or not media.is_file():
            raise ValueError("draft scene timeline or media is invalid")
        if data["output_mode"] == "image_slideshow" and scene.get("motion") not in MOTION:
            raise ValueError("slideshow motion is not in the fixed allowlist")
        if data["output_mode"] == "image_to_video" and scene.get("motion") not in {None, "", "none"}:
            raise ValueError("image-to-video scenes cannot contain slideshow motion")
        previous = start + duration
    if previous != int(data["duration_us"]):
        raise ValueError("scene timeline does not cover narration")
    narration = Path(data["narration_path"]).resolve()
    if not narration.is_file():
        raise ValueError("narration file is missing")
    return data


def add_motion(segment, motion: str, duration: int) -> None:
    scale_from, scale_to, x_from, x_to = MOTION[motion]
    segment.add_keyframe(draft.KeyframeProperty.uniform_scale, 0, scale_from)
    segment.add_keyframe(draft.KeyframeProperty.uniform_scale, duration, scale_to)
    segment.add_keyframe(draft.KeyframeProperty.position_x, 0, x_from)
    segment.add_keyframe(draft.KeyframeProperty.position_x, duration, x_to)


def build(plan_path: Path, output_root: Path) -> dict:
    plan = read_plan(plan_path)
    job_id = str(plan["job_id"])
    workspace_parent = output_root / "workspace"
    workspace_parent.mkdir(parents=True, exist_ok=True)
    script = draft.DraftFolder(str(workspace_parent)).create_draft(job_id, 1080, 1920, 24, allow_replace=False)
    total_us = int(plan["duration_us"])
    visual_track = script.append_track(draft.TrackSpec(draft.TrackType.video, "主画面"))
    for scene in plan["scenes"]:
        start = int(scene["start_us"])
        duration = int(scene["duration_us"])
        media_path = str(Path(scene["media_path"]).resolve())
        material = draft.VideoMaterial(media_path)
        segment = draft.VideoSegment(
            material,
            draft.Timerange(start, duration),
            source_timerange=draft.Timerange(0, duration),
            volume=0,
        )
        motion = scene.get("motion")
        if motion in MOTION:
            add_motion(segment, motion, duration)
        script.add_segment(segment, visual_track)

    narration_track = script.append_track(draft.TrackSpec(draft.TrackType.audio, "旁白"))
    narration_material = draft.AudioMaterial(str(Path(plan["narration_path"]).resolve()), "旁白")
    script.add_segment(
        draft.AudioSegment(
            narration_material,
            draft.Timerange(0, total_us),
            source_timerange=draft.Timerange(0, total_us),
            volume=1.0,
        ),
        narration_track,
    )
    script.save()

    workspace = workspace_parent / job_id
    content_path = workspace / "draft_content.json"
    meta_path = workspace / "draft_meta_info.json"
    content = json.loads(content_path.read_text(encoding="utf-8"))
    meta = json.loads(meta_path.read_text(encoding="utf-8"))
    track_names = {track.get("name") for track in content.get("tracks", [])}
    if "字幕" in track_names or "字幕轨" in track_names or "标题" in track_names:
        raise ValueError("draft contains a forbidden caption or title track")
    content["duration"] = total_us
    content["spoken_captions"] = False
    content["editable_titles"] = False
    content_path.write_text(json.dumps(content, ensure_ascii=False, indent=2), encoding="utf-8")
    meta["draft_name"] = str(plan["display_name"])
    meta["tm_duration"] = total_us
    meta_path.write_text(json.dumps(meta, ensure_ascii=False, indent=2), encoding="utf-8")

    digest = hashlib.sha256()
    for path in (content_path, meta_path):
        digest.update(path.read_bytes())
    summary = {
        "status": "built",
        "job_id": job_id,
        "display_name": plan["display_name"],
        "workspace": str(workspace),
        "duration_us": total_us,
        "scenes": len(plan["scenes"]),
        "captions": 0,
        "editable_titles": 0,
        "draft_fingerprint": digest.hexdigest(),
    }
    (output_root / "build-summary.json").write_text(json.dumps(summary, ensure_ascii=False, indent=2), encoding="utf-8")
    print(json.dumps(summary, ensure_ascii=False))


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--plan", required=True)
    parser.add_argument("--output-root", required=True)
    args = parser.parse_args()
    build(Path(args.plan).resolve(), Path(args.output_root).resolve())


if __name__ == "__main__":
    main()
