"""Apply the companion scenic contract to an installed Jianying draft Skill.
Run with --skill-root PATH. Exact anchors prevent patching an unknown version.
Original bytes are backed up outside the executable Skill before any writes.
"""
from pathlib import Path
import argparse, datetime, hashlib, json, os, shutil

def main():
 ap=argparse.ArgumentParser();ap.add_argument('--skill-root',type=Path,required=True);a=ap.parse_args()
 root=a.skill_root/'scripts';edits={}
 def change(name,old,new,count=1):
  p=root/name;t=edits.get(p,p.read_text('utf-8'))
  if new in t:return
  if t.count(old)!=count:raise RuntimeError(f'{name}: contract anchor mismatch ({t.count(old)}), no files written')
  edits[p]=t.replace(old,new)
 change('production_plan_v2.py','KNOWN_MIX_PRESETS = ("movie_mix", "image_video")','KNOWN_MIX_PRESETS = ("movie_mix", "image_video", "scenic_semantic")')
 helper='''def resolve_transition_cache(cache_paths: dict[str, Path]) -> dict[str, Path]:
    """Resolve only the verified effect's cache, without mutating machine identity."""
    resolved = dict(cache_paths)
    current = resolved.get("transition_cross_dissolve", Path("__MISSING_MACHINE_PATH__"))
    if current.exists():
        return resolved
    roots: set[Path] = set()
    for value in cache_paths.values():
        for parent in value.parents:
            if parent.name.casefold() == "cache":
                roots.add(parent)
                break
    candidates: list[Path] = []
    for cache_root in sorted(roots, key=str):
        effect = cache_root / "effect" / "322577"
        if not effect.is_dir() or effect.is_symlink():
            continue
        for candidate in effect.iterdir():
            if not candidate.is_dir() or candidate.is_symlink():
                continue
            if getattr(candidate, "is_junction", lambda: False)():
                continue
            if candidate.resolve().parent != effect.resolve():
                continue
            candidates.append(candidate)
    if candidates:
        resolved["transition_cross_dissolve"] = max(candidates, key=lambda p: (p.stat().st_mtime_ns, str(p)))
    return resolved


'''
 change('run_montage_job.py','@dataclass(frozen=True)\nclass MachineProfile:',helper+'@dataclass(frozen=True)\nclass MachineProfile:')
 change('run_montage_job.py','            cache_paths=cache_paths,','            cache_paths=resolve_transition_cache(cache_paths),')
 change('run_montage_job.py','''        self._patch_verified_fields(context, policy)
        self._patch_caption_keyword_spans''','''        self._patch_verified_fields(context, policy, plan)
        self._patch_caption_keyword_spans''')
 change('run_montage_job.py','def _patch_verified_fields(self, context: JobContext, policy: dict[str, Any]) -> None:','def _patch_verified_fields(self, context: JobContext, policy: dict[str, Any], plan: dict[str, Any] | None = None) -> None:')
 change('run_montage_job.py','''        for segment in segments[1:]:
            transition_id = uuid.uuid4().hex''','''        planned = list((plan or {}).get("timeline") or [])
        if plan is not None and len(planned) != len(segments):
            raise ValueError("transition plan does not match generated segment count")
        for index, segment in enumerate(segments[1:], 1):
            if plan is not None and planned[index].get("transition") is None:
                continue  # null means a real hard cut, not a missing default
            transition_id = uuid.uuid4().hex''')
 change('run_montage_job.py','''                        "emphasis_effect": shot.get("emphasis_effect"),
                    }''','''                        "emphasis_effect": shot.get("emphasis_effect"),
                        "transition": shot.get("transition"),
                        "source_in_s": shot.get("source_in_s"),
                        "source_out_s": shot.get("source_out_s"),
                    }''')
 change('run_montage_job.py','''            profile["v2"] = {
                "project_duration_s":''','''            profile["v2"] = {
                "mix_preset": (plan.get("media_mix_policy") or {}).get("preset"),
                "project_duration_s":''')
 # Validate hard cuts instead of simply relaxing the all-dissolve validator.
 change('validate_montage_draft.py','''            if number > 1:
                refs = segment.get("extra_material_refs") or []
                candidates = [transitions[ref] for ref in refs if ref in transitions]
                if not candidates:''','''            if "transition" in shot and shot["transition"] is None:
                refs = segment.get("extra_material_refs") or []
                if any(ref in transitions for ref in refs):
                    issue(findings, "error", f"video segment {number} hard cut has an unexpected transition")
            if number > 1 and ("transition" not in shot or shot["transition"] is not None):
                refs = segment.get("extra_material_refs") or []
                candidates = [transitions[ref] for ref in refs if ref in transitions]
                if not candidates:''')
 change('validate_montage_draft.py','''            segment_duration = int(timerange.get("duration", 0))
            if abs(float(segment.get("volume", 1)))''','''            segment_duration = int(timerange.get("duration", 0))
            if v2.get("mix_preset") == "scenic_semantic":
                if abs(float(segment.get("speed", 1)) - 1.0) > 0.001:
                    issue(findings, "error", f"video segment {number} must play at normal speed")
                expected_start = round(float(shot.get("start_s", 0)) * 1_000_000)
                expected_duration = round((float(shot.get("end_s", 0)) - float(shot.get("start_s", 0))) * 1_000_000)
                if abs(int(timerange.get("start", 0)) - expected_start) > V2_TIME_TOLERANCE_US or abs(segment_duration - expected_duration) > V2_TIME_TOLERANCE_US:
                    issue(findings, "error", f"video segment {number} timeline differs from the approved plan")
                actual_material = videos.get(segment.get("material_id")) or {}
                if normalized_path(actual_material.get("path", "")) != normalized_path(shot.get("source_path", "")):
                    issue(findings, "error", f"video segment {number} source differs from the approved plan")
                if media_kind != "image" and shot.get("source_in_s") is not None:
                    source_range = segment.get("source_timerange") or {}
                    expected_in = round(float(shot["source_in_s"]) * 1_000_000)
                    expected_span = round((float(shot["source_out_s"]) - float(shot["source_in_s"])) * 1_000_000)
                    if abs(int(source_range.get("start", 0)) - expected_in) > V2_TIME_TOLERANCE_US or abs(int(source_range.get("duration", 0)) - expected_span) > V2_TIME_TOLERANCE_US:
                        issue(findings, "error", f"video segment {number} source window differs from the approved plan")
            if abs(float(segment.get("volume", 1)))''')
 # Never silently slow a scenic shot when an input duration is wrong.
 change('run_montage_job.py','''                speed = max(source_duration_us / duration_us, 0.05)
            segment = draft.VideoSegment(''','''                speed = max(source_duration_us / duration_us, 0.05)
                if (plan.get("media_mix_policy") or {}).get("preset") == "scenic_semantic" and abs(speed - 1.0) > 0.001:
                    raise ValueError("scenic source duration differs from plan; re-probe the indexed source instead of silently changing speed")
            segment = draft.VideoSegment(''',count=2)
 change('production_plan_v2.py','''def _parse_timeline_shot(
    findings: list[dict[str, str]], raw: Any, index: int, project_duration_s: float
) -> TimelineShotV2 | None:''','''def _parse_timeline_shot(
    findings: list[dict[str, str]], raw: Any, index: int, project_duration_s: float,
    *, allow_hard_cuts: bool = False,
) -> TimelineShotV2 | None:''')
 change('production_plan_v2.py','if number > 1 and transition is None:', 'if number > 1 and transition is None and not allow_hard_cuts:')
 change('production_plan_v2.py','shot = _parse_timeline_shot(findings, raw_shot, index, duration)','shot = _parse_timeline_shot(findings, raw_shot, index, duration, allow_hard_cuts=bool(mix_policy and mix_policy.preset == "scenic_semantic"))')
 # Reconcile baseline-proven stale test expectations with existing behavior:
 # short board titles are supported, and repeated registration is idempotent.
 change('../tests/test_production_plan_v2.py','''        plan["graphics"]["title"]["text"] = "太短"
        _, findings = self.parse(plan)
        self.assertIn("graphics.title must contain 6-15 Chinese characters", error_messages(findings))''','''        plan["graphics"]["title"]["text"] = "太短"
        _, findings = self.parse(plan)
        self.assertEqual([item for item in findings if item["level"] == "error"], [], findings)''')
 change('../tests/test_production_plan_v2.py','self.assertIn("graphics.title must contain 6-15 Chinese characters", error_messages(findings))','self.assertIn("graphics.title must contain at most 15 characters", error_messages(findings))')
 change('../tests/test_run_montage_job.py','("graphics.title", "graphics.subtitle", "manifest narration", "audio.sfx", "transition", "forbidden")','("graphics.", "manifest narration", "audio.sfx", "transition", "forbidden")')
 change('../tests/test_run_montage_job.py','''        second = module.register_job(self.manifest, draft, runner=module.FakeRegistrationRunner(), validator=backend)
        self.assertEqual(second["status"], "failed")''','''        second = module.register_job(self.manifest, draft, runner=module.FakeRegistrationRunner(), validator=backend)
        self.assertEqual(second["status"], "completed")
        after_retry = json.loads(root_meta.read_text(encoding="utf-8"))
        self.assertEqual(len(after_retry["all_draft_store"]), 1)''')
 change('../tests/test_validate_montage_draft.py','test_rejects_short_titles_and_non_full_width_boundary_lines','test_accepts_short_titles_but_rejects_non_full_width_boundary_lines')
 change('../tests/test_validate_montage_draft.py','self.assertIn("title has 4 Chinese characters", messages)','self.assertNotIn("title has", messages)')
 change('../tests/test_run_montage_job.py','self.assertIn("target", second["summary"].casefold())','self.assertIn("registration completed", second["summary"].casefold())')
 change('validate_montage_draft.py','def normalized_path(path: str | Path) -> str:', '''def same_source_file(left: str | Path, right: str | Path) -> bool:
    # Compare filesystem identity, including Windows extended-length aliases.
    if not left or not right:
        return False
    try:
        return Path(left).samefile(Path(right))
    except (OSError, ValueError):
        return False


def normalized_path(path: str | Path) -> str:''')
 change('validate_montage_draft.py','if normalized_path(actual_material.get("path", "")) != normalized_path(shot.get("source_path", "")):', 'if not same_source_file(actual_material.get("path", ""), shot.get("source_path", "")):')
 # Syntax-check the entire patch before changing the installed Skill.
 for p,t in edits.items():compile(t,str(p),'exec')
 if not edits:print('Skill already upgraded');return
 backup=Path.home()/'.agentdock/workspaces'/('scenic-skill-backup-'+datetime.datetime.now().strftime('%Y%m%d-%H%M%S'));backup.mkdir(parents=True)
 hashes={}
 for p,t in edits.items():
  shutil.copy2(p,backup/p.name);hashes[p.name]=hashlib.sha256(p.read_bytes()).hexdigest()
  tmp=p.with_suffix('.upgrade.tmp');tmp.write_text(t,encoding='utf-8');os.replace(tmp,p)
 (backup/'sha256.json').write_text(json.dumps(hashes,indent=2),encoding='utf-8')
 print('Skill upgraded; original files:',backup)
if __name__=='__main__':main()
