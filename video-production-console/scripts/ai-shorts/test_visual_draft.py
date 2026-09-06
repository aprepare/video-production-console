"""Offline fixture tests: build real Jianying JSON, never touch the user's draft root."""
import json
import tempfile
import unittest
import wave
import subprocess
from pathlib import Path
from unittest.mock import patch

from PIL import Image
import build_ai_short_draft as exporter


class VisualDraftTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.image = self.root / "test.png"
        Image.new("RGB", (576, 1024), (70, 90, 100)).save(self.image)
        self.voice = self.root / "voice.wav"
        with wave.open(str(self.voice), "wb") as wav:
            wav.setnchannels(1)
            wav.setsampwidth(2)
            wav.setframerate(8000)
            wav.writeframes(b"\0\0" * (8*8000))
        self.job = {
            "draft_name": "visual-test", "workspace_parent": str(self.root / "workspace"),
            "duration_s": 8, "layout": "portrait_full", "narration": str(self.voice),
            "shots": [
                {"image": str(self.image), "start_s": 0, "end_s": 4, "camera_move": "zoom_in", "annotation": "先看清条件"},
                {"image": str(self.image), "start_s": 4, "end_s": 8, "camera_move": "still"},
            ],
            "captions": [
                {"text": "利率0.95%，先看清风险", "start_s": 0, "end_s": 4, "keywords": [{"text": "0.95%", "kind": "number"}, {"text": "风险", "kind": "risk"}]},
                {"text": "学习《财富觉醒方法论》", "start_s": 4, "end_s": 8},
            ],
            "visual_settings": {"caption_enabled": True, "caption_position": "lower", "caption_size": 12, "keywords_enabled": True, "annotation_enabled": True, "motion_strength": "gentle", "transition": "fade"},
        }

    def build(self):
        result = exporter.build(self.job)
        content = json.loads((Path(result["workspace"]) / "draft_content.json").read_text(encoding="utf-8"))
        return result, content

    def test_full_draft_editable_tracks_keyword_ranges_and_timing(self):
        result, data = self.build()
        self.assertEqual(data["canvas_config"]["width"], 1080)
        self.assertEqual(data["canvas_config"]["height"], 1920)
        tracks = {t["name"]: t for t in data["tracks"]}
        self.assertIn("字幕", tracks)
        self.assertIn("重点标注", tracks)
        self.assertNotIn("背景框架", tracks)
        scenes = tracks["画面"]["segments"]
        self.assertEqual([s["target_timerange"] for s in scenes], [{"start": 0, "duration": 4000000}, {"start": 4000000, "duration": 4000000}])
        self.assertTrue(scenes[0]["common_keyframes"])
        self.assertEqual(tracks["字幕"]["segments"][0]["clip"]["transform"]["y"], -0.56)
        contents = [json.loads(m["content"]) for m in data["materials"]["texts"]]
        cap = next(c for c in contents if "0.95%" in c["text"])
        ranges = [(cap["text"][s["range"][0]:s["range"][1]], s["fill"]["content"]["solid"]["color"]) for s in cap["styles"]]
        self.assertIn(("0.95%", list(exporter.hex_rgb("#FFD166"))), ranges)
        self.assertIn(("风险", list(exporter.hex_rgb("#FF9A8B"))), ranges)
        self.assertEqual("".join(t for t,_ in ranges), cap["text"])
        self.assertTrue((Path(result["workspace"])/"visual_manifest.json").exists())

    def test_legacy_inset_still_has_frame(self):
        self.job["layout"] = "portrait_inset"
        self.job.pop("visual_settings")
        _, data = self.build()
        tracks={t["name"]:t for t in data["tracks"]}
        self.assertIn("背景框架", tracks)
        self.assertNotIn("重点标注", tracks)
        self.assertEqual(tracks["字幕"]["segments"][0]["clip"]["transform"]["y"], -0.30)

    def test_native_dissolve_single_track_without_caption_shift(self):
        _, data = self.build()
        tracks = {t["name"]: t for t in data["tracks"]}
        self.assertNotIn("画面叠化", tracks)
        first = tracks["画面"]["segments"][0]
        second = tracks["画面"]["segments"][1]
        self.assertEqual(first["target_timerange"], {"start": 0, "duration": 4000000})
        self.assertEqual(second["target_timerange"], {"start": 4000000, "duration": 4000000})
        self.assertFalse(any(k["property_type"] == "KFTypeAlpha" for k in first["common_keyframes"]))
        self.assertEqual(tracks["字幕"]["segments"][1]["target_timerange"]["start"], 4000000)
        transitions=data["materials"]["transitions"]
        self.assertEqual(len(transitions),1)
        self.assertEqual(transitions[0]["duration"],300000)
        self.assertTrue(transitions[0]["is_overlap"])
        self.assertEqual(transitions[0]["effect_id"],"322577")
        self.assertIn(transitions[0]["id"],first["extra_material_refs"])
        self.assertNotIn(transitions[0]["id"],second["extra_material_refs"])

    def test_video_and_image_dissolve_keep_total_duration_and_mute_video(self):
        import imageio_ffmpeg
        video=self.root/"offline.mp4"
        subprocess.run([imageio_ffmpeg.get_ffmpeg_exe(),"-v","error","-f","lavfi","-i","color=c=blue:s=180x320:d=8","-an","-c:v","libx264","-y",str(video)],check=True)
        self.job["shots"][0].pop("image")
        self.job["shots"][0]["video"]=str(video)
        _,data=self.build()
        tracks={t["name"]:t for t in data["tracks"]}
        first=tracks["画面"]["segments"][0]
        second=tracks["画面"]["segments"][1]
        self.assertEqual(first["volume"],0)
        self.assertEqual(first["target_timerange"]["duration"],4000000)
        self.assertEqual(second["target_timerange"]["start"]+second["target_timerange"]["duration"],8000000)
        self.assertEqual(second["render_index"],first["render_index"])
        self.assertEqual(len([t for t in data["tracks"] if t["type"]=="video"]),1)

    def test_disable_captions_annotations_and_movement(self):
        self.job["visual_settings"].update(caption_enabled=False, annotation_enabled=False, motion_strength="none", transition="cut")
        _, data = self.build()
        tracks={t["name"]:t for t in data["tracks"]}
        self.assertFalse(tracks["字幕"]["segments"])
        self.assertNotIn("重点标注",tracks)
        for group in tracks["画面"]["segments"][0]["common_keyframes"]:
            values=[k["values"] for k in group["keyframe_list"]]
            self.assertTrue(all(v==values[0] for v in values))

    def test_keyword_overlap_is_not_duplicated(self):
        seg=exporter.KeywordTextSegment("本金10万元与10万", exporter.draft.Timerange(0,1000000), keywords=[{"text":"10万元","kind":"number"},{"text":"10万","kind":"concept"}])
        content=json.loads(seg.export_material()["content"])
        self.assertEqual("".join(content["text"][s["range"][0]:s["range"][1]] for s in content["styles"]),content["text"])

    def test_export_keeps_previous_draft_and_assets(self):
        root=self.root/"jianying-fixture"
        old=root/"old-version"
        old.mkdir(parents=True)
        (old/"sentinel.txt").write_text("keep",encoding="utf-8")
        exporter.write_json(root/"root_meta_info.json",{"all_draft_store":[{"draft_fold_path":old.as_posix()}]})
        self.job.update(jianying_root=str(root),replace_draft="")
        first,_=self.build()
        second,_=self.build()
        with patch.object(exporter,"stop_jianying"):
            first_path=Path(exporter.register(self.job,first))
            second_path=Path(exporter.register(self.job,second))
        self.assertNotEqual(first_path,second_path)
        self.assertTrue((old/"sentinel.txt").exists())
        self.assertTrue(first_path.exists() and second_path.exists() and self.image.exists())
        self.assertEqual(len(exporter.read_json(root/"root_meta_info.json")["all_draft_store"]),3)


if __name__ == "__main__":
    unittest.main()
