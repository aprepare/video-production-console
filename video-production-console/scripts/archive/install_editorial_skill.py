"""Install the versioned editorial contract; back up every changed Skill file."""
from pathlib import Path
import argparse,datetime,json,shutil


def main():
 ap=argparse.ArgumentParser();ap.add_argument('--skill-root',type=Path,required=True);a=ap.parse_args();sk=a.skill_root
 pending={}
 def change(rel,old,new):
  p=sk/rel;t=pending.get(p,p.read_text('utf-8'))
  if new in t:return
  if t.count(old)!=1:raise RuntimeError(f'Unexpected Skill version: {rel}, anchor count={t.count(old)}')
  pending[p]=t.replace(old,new)
 change('scripts/production_plan_v2.py','ALLOWED_TOP_LEVEL_KEYS = frozenset({','ALLOWED_TOP_LEVEL_KEYS = frozenset({\n    "editorial",')
 change('scripts/production_plan_v2.py','    _reject_unknown_keys(findings, plan, ALLOWED_TOP_LEVEL_KEYS, "production plan v2")','    _reject_unknown_keys(findings, plan, ALLOWED_TOP_LEVEL_KEYS, "production plan v2")\n    from editorial_layers import validate_editorial\n    validate_editorial(plan, findings)')
 change('scripts/run_montage_job.py','        self._patch_verified_fields(context, policy, plan)','        self._patch_verified_fields(context, policy, plan)')
 change('scripts/run_montage_job.py','''                sfx_track,
            )
        script.save()
        meta_path = context.workspace / "draft_meta_info.json"
        meta = read_json(meta_path)
        meta["draft_name"] = context.draft_display_name
        write_json(meta_path, meta)
        self._patch_verified_fields(context, policy, plan)''','''                sfx_track,
            )
        from editorial_layers import build_editorial_layers
        build_editorial_layers(script, draft, context, plan, total_us)
        script.save()
        meta_path = context.workspace / "draft_meta_info.json"
        meta = read_json(meta_path)
        meta["draft_name"] = context.draft_display_name
        write_json(meta_path, meta)
        self._patch_verified_fields(context, policy, plan)''')
 change('scripts/run_montage_job.py','''        for index, segment in enumerate(segments[1:], 1):
            if plan is not None and planned[index].get("transition") is None:
                continue  # null means a real hard cut, not a missing default''','''        outgoing = ((plan or {}).get("editorial") or {}).get("transition_attachment") == "outgoing"
        for index in range(1, len(segments)):
            if plan is not None and planned[index].get("transition") is None:
                continue  # null means a real hard cut, not a missing default
            # Jianying transitions belong to the segment BEFORE the boundary.
            # Keep legacy snapshots stable; new editorial plans declare outgoing.
            segment = segments[index - 1] if outgoing else segments[index]''')
 change('scripts/run_montage_job.py','''                "mix_preset": (plan.get("media_mix_policy") or {}).get("preset"),''','''                "mix_preset": (plan.get("media_mix_policy") or {}).get("preset"),
                "editorial": plan.get("editorial"),''')
 change('scripts/run_montage_job.py','''        return validate_draft(read_json(content_path), profile, str(content_path))''','''        content = read_json(content_path)
        report = validate_draft(content, profile, str(content_path))
        from editorial_layers import validate_editorial_draft
        report["findings"].extend(validate_editorial_draft(content, plan, context))
        report["editorial"] = {"callouts": len((plan.get("editorial") or {}).get("callouts") or []), "sounds": len((plan.get("editorial") or {}).get("sounds") or []), "transition_attachment": (plan.get("editorial") or {}).get("transition_attachment", "legacy")}
        return report''')
 change('scripts/validate_montage_draft.py','''    allowed_text_tracks = {chapter_name, "标题", "副标题"}''','''    allowed_text_tracks = {chapter_name, "标题", "副标题"}
    if v2.get("editorial"):
        allowed_text_tracks.add("重点标识")''')
 change('scripts/validate_montage_draft.py','''            if "transition" in shot and shot["transition"] is None:''','''            outgoing = (v2.get("editorial") or {}).get("transition_attachment") == "outgoing"
            transition_shot = (plan_shots[number] if number < len(plan_shots) else {"transition": None}) if outgoing else shot
            if "transition" in transition_shot and transition_shot["transition"] is None:''')
 change('scripts/validate_montage_draft.py','''            if number > 1 and ("transition" not in shot or shot["transition"] is not None):''','''            if (outgoing or number > 1) and ("transition" not in transition_shot or transition_shot["transition"] is not None):''')
 change('scripts/run_montage_job.py','''        if project_duration >= 240 and not 3 <= len(sfx) <= 12:
            findings.append({"level": "error", "message": "audio.sfx requires 3-12 placements for a five-minute-scale video"})

    forbidden_actions = (
        "tts", "asr", "caption", "subtitle", "synthetic speech", "voiceover",
        "rewrite script", "generate script", "自动字幕", "改写文案", "重写文案", "合成配音",''','''        editorial = plan.get("editorial")
        if isinstance(editorial, dict) and editorial.get("version") == 1:
            # New editorial cues are separately typed, gain-bounded and verified.
            # Keep one legacy opening hit, not three loud hits merely to satisfy
            # a legacy count. Do not relax the original contract for old plans.
            sounds = editorial.get("sounds") if isinstance(editorial.get("sounds"), list) else []
            if len(sfx) != 1 or (project_duration >= 240 and len(sounds) < 2):
                findings.append({"level": "error", "message": "editorial audio requires one opening hit and at least two verified soft cues for long videos"})
        elif project_duration >= 240 and not 3 <= len(sfx) <= 12:
            findings.append({"level": "error", "message": "audio.sfx requires 3-12 placements for a five-minute-scale video"})

    forbidden_actions = (
        "tts", "asr", "caption", "subtitle", "synthetic speech", "voiceover",
        "rewrite script", "generate script", "自动字幕", "改写文案", "重写文案", "合成配音",''')
 source=Path(__file__).resolve().parent.parent/'tools/jianying_editorial/editorial_layers.py';pending[sk/'scripts/editorial_layers.py']=source.read_text('utf-8')
 backup=Path.home()/'.agentdock/workspaces'/('editorial-skill-backup-'+datetime.datetime.now().strftime('%Y%m%d-%H%M%S'));backup.mkdir(parents=True)
 for p,text in pending.items():
  if p.exists():
   target=backup/p.relative_to(sk);target.parent.mkdir(parents=True,exist_ok=True);shutil.copy2(p,target)
  p.write_text(text,encoding='utf-8')
 print(json.dumps({'installed_files':len(pending),'backup':str(backup)},ensure_ascii=False))
if __name__=='__main__':main()
