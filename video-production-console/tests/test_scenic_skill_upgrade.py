"""Regression checks for the installed montage executor companion contract."""
from __future__ import annotations
import importlib.util
import json
import os
from pathlib import Path
import sys
from types import SimpleNamespace
import pytest

SKILL = Path(os.environ.get("JIAN_YING_SKILL_ROOT", str(Path.home() / ".codex/skills/jianying-montage-draft")))
sys.path.insert(0, str(SKILL / "scripts"))
import run_montage_job as executor
import production_plan_v2 as parser


def test_cache_resolution_does_not_modify_profile(tmp_path):
    cache = tmp_path / "Cache"
    new = cache / "effect/322577/new-download"
    new.mkdir(parents=True)
    (new / "effect.json").write_text("{}", encoding="utf-8")
    stale = cache / "effect/322577/old-download"
    values = {"transition_cross_dissolve": stale, "bgm_yawaraka_hikari": cache / "music/track.mp3"}
    profile = tmp_path / "profile.json"
    profile.write_text(json.dumps({"cache_paths": {k: str(v) for k, v in values.items()}}), encoding="utf-8")
    before = profile.read_bytes()
    resolved = executor.resolve_transition_cache(values)
    assert resolved["transition_cross_dissolve"] == new
    assert values["transition_cross_dissolve"] == stale
    assert profile.read_bytes() == before


def test_hard_cuts_are_not_replaced_by_default_dissolves(tmp_path):
    draft_path = tmp_path / "draft_content.json"
    draft = {"materials": {"audios": []}, "tracks": [{"name": "画面", "segments": [{}, {}, {}]}]}
    draft_path.write_text(json.dumps(draft), encoding="utf-8")
    context = SimpleNamespace(workspace=tmp_path, machine_profile=SimpleNamespace(cache_paths={"bgm": tmp_path / "bgm.mp3", "transition": tmp_path / "effect"}))
    policy = {"bgm": {"cache_key": "bgm"}, "sfx": [], "transition": {"name": "叠化", "type": "transition", "effect_id": "322577", "resource_id": "6724845717472416269", "cache_key": "transition", "duration_us": 466666, "is_overlap": True}}
    plan = {"timeline": [{"transition": None}, {"transition": None}, {"transition": {"preset": "cross_dissolve", "duration_s": 0.466666}}]}
    backend = executor.DeterministicDraftBackend()
    backend._patch_verified_fields(context, policy, plan)
    result = json.loads(draft_path.read_text("utf-8"))
    segments = result["tracks"][0]["segments"]
    assert not segments[0].get("extra_material_refs")
    assert not segments[1].get("extra_material_refs")
    assert len(segments[2]["extra_material_refs"]) == 1
    assert len(result["materials"]["transitions"]) == 1
    with pytest.raises(ValueError, match="segment count"):
        backend._patch_verified_fields(context, policy, {"timeline": []})


def test_scenic_contract_preserves_strict_unknown_preset_rejection():
    assert "scenic_semantic" in parser.KNOWN_MIX_PRESETS
    findings = []
    parser._parse_media_mix_policy(findings, {"preset": "unsafe_unknown", "basis": "timeline_duration", "targets": {k: {"min": 0, "max": 1} for k in parser.MEDIA_KINDS}, "max_source_uses": 1, "min_segments_between_reuse": 5})
    assert any(f["level"] == "error" and "unknown" in f["message"] for f in findings)


def test_parser_accepts_hard_cut_only_when_scenic_policy_allows_it():
    shot = {"shot_no": 2, "start_s": 5, "end_s": 10, "media_kind": "broll", "source_id": "china-1", "source_path": "china.mp4", "source_in_s": 0, "source_out_s": 5, "match": {"level": "metaphor", "score": 0.6, "intent_id": "segment-2", "reason": "China imagery"}, "motion": {"preset": "steady", "scale_from": 1.2, "scale_to": 1.2, "x_from": 0, "x_to": 0, "y_from": 0, "y_to": 0}, "opacity": 1, "source_audio_muted": True, "look": "none", "transition": None, "emphasis_effect": None}
    findings = []
    parser._parse_timeline_shot(findings, shot, 1, 10, allow_hard_cuts=True)
    assert not [f for f in findings if f["level"] == "error"], findings
    legacy = []
    parser._parse_timeline_shot(legacy, shot, 1, 10)
    assert any("transition is required" in f["message"] for f in legacy)


def test_source_identity_accepts_alias_but_rejects_replacement(tmp_path):
    import validate_montage_draft as validator
    first=tmp_path / "original.mp4"
    second=tmp_path / "other.mp4"
    first.write_bytes(b"fixture");second.write_bytes(b"fixture")
    assert validator.same_source_file(first, first)
    assert not validator.same_source_file(first, second)
    assert not validator.same_source_file(first, tmp_path / "missing.mp4")
    if os.name == "nt":
        alias=chr(92)*2+"?"+chr(92)+str(first)
        assert validator.same_source_file(first, alias)
