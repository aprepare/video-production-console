"""Typed, resource-free editorial graphics plus verified local cue sounds.
The quote text remains editable. Cards/mattes are deterministic local geometry,
not downloaded stickers with unknown rights or fabricated effect resource IDs.
"""
from __future__ import annotations
import json
import math
from pathlib import Path

TEXT_TRACK = "重点标识"
CARD_TRACK = "强调卡片"
FOCUS_TRACK = "语义聚焦"
SOUND_TRACK = "剪辑提示音"


def clean(text):
    return "".join(str(text).split())


def validate_editorial(plan, findings):
    ed = plan.get("editorial")
    if ed is None:
        return
    def error(message):
        findings.append({"level": "error", "message": "editorial: " + message})
    if not isinstance(ed, dict):
        error("must be an object"); return
    if set(ed) - {"version", "transition_attachment", "highlights_status", "callouts", "sounds"}:
        error("unknown fields")
    if ed.get("version") != 1 or ed.get("transition_attachment") != "outgoing":
        error("unsupported version or transition attachment")
    if (plan.get("media_mix_policy") or {}).get("preset") != "scenic_semantic":
        error("editorial layers require scenic_semantic")
    duration = plan.get("project_duration_s", 0)
    captions = ((plan.get("graphics") or {}).get("captions") or {}).get("items") or []
    calls = ed.get("callouts")
    sounds = ed.get("sounds")
    if not isinstance(calls, list) or len(calls) > 12:
        error("callouts must be a list of at most twelve"); return
    previous_end = -100
    for item in calls:
        if not isinstance(item, dict) or set(item) - {"cue_index", "text", "kind", "start_s", "end_s"}:
            error("invalid callout fields"); continue
        idx, text = item.get("cue_index"), item.get("text")
        if type(idx) is not int or not 0 <= idx < len(captions) or not isinstance(text, str) or not 4 <= len(text) <= 18:
            error("invalid quote/cue"); continue
        source = clean("".join(str(c.get("text") or "") for c in captions[idx:idx+3]))
        if clean(text) not in source:
            error("quote must be an exact substring of its source cues")
        if item.get("kind") not in {"keypoint", "warning", "number", "conclusion"}:
            error("unknown callout kind")
        try:
            start, end = float(item["start_s"]), float(item["end_s"])
            if not all(math.isfinite(x) for x in [start, end]) or not 0 <= start < end <= duration+.001 or not 2 <= end-start <= 6 or start-previous_end < 21.9:
                error("callout time or spacing outside bounds")
            if not float(captions[idx]["start_s"])-.02 <= start <= float(captions[min(idx+2,len(captions)-1)]["end_s"])+.02:
                error("callout does not align with its source cues")
            previous_end = end
        except (KeyError, TypeError, ValueError):
            error("invalid callout time")
    if not isinstance(sounds, list) or len(sounds) > max(4, duration/6):
        error("sound count exceeds density limit"); return
    previous_start = -100
    for item in sounds:
        if not isinstance(item, dict) or set(item) - {"start_s", "duration_s", "cache_key", "volume"}:
            error("invalid sound fields"); continue
        if item.get("cache_key") not in {"sfx_whoosh", "sfx_water_drop", "sfx_conclusion_hit"}:
            error("sound resource is not whitelisted")
        try:
            start, length, volume = float(item["start_s"]), float(item["duration_s"]), float(item["volume"])
            if not all(math.isfinite(x) for x in [start,length,volume]) or not 0 <= start < duration or not .05 <= length <= 1 or start+length > duration+.001 or not .04 <= volume <= .25 or start-previous_start < 9:
                error("sound range, gain or spacing outside bounds")
            previous_start = start
        except (KeyError, TypeError, ValueError):
            error("invalid sound values")


def _envelope(segment, draft, duration, maximum=1.0):
    ramp = min(180000, duration//4)
    for at, value in [(0,0.0),(ramp,maximum),(max(ramp,duration-260000),maximum),(duration,0.0)]:
        segment.add_keyframe(draft.KeyframeProperty.alpha, at, value)


def editorial_card_layout(plan):
    """Keep emphasis cards clear of account-specific bottom caption tracks.

    The normal lower card occupies y=1384..1578 on the 1920px canvas.
    Accounts with spoken captions below y=-0.50 use an upper-window card
    instead; the original account caption position and styling stay untouched.
    """
    overrides = plan.get("style_overrides") or {}
    caption_y = []
    for key, style in overrides.items():
        if key.startswith("spoken") and isinstance(style, dict):
            value = style.get("y")
            if isinstance(value, (int, float)) and not isinstance(value, bool) and math.isfinite(value):
                caption_y.append(value)
    if caption_y and min(caption_y) <= -.50:
        return (76, 626, 1004, 820), .247
    return (76, 1384, 1004, 1578), -.54


def build_editorial_layers(script, draft, context, plan, total_us):
    ed = plan.get("editorial")
    if not ed:
        return
    findings = []
    validate_editorial(plan,findings)
    if findings:
        raise ValueError(findings[0]["message"])
    from PIL import Image, ImageDraw
    generated = context.workspace / "generated_visuals"
    generated.mkdir(parents=True,exist_ok=True)
    cards = {}
    card_bounds, text_y = editorial_card_layout(plan)
    left, top, right, bottom = card_bounds
    for kind, accent in [("keypoint",(235,194,91,255)),("warning",(224,114,81,255)),("number",(235,194,91,255)),("conclusion",(235,194,91,255))]:
        image = Image.new("RGBA",(1080,1920),(0,0,0,0));draw=ImageDraw.Draw(image)
        draw.rounded_rectangle(card_bounds,radius=24,fill=(12,17,24,228),outline=accent,width=3)
        draw.rounded_rectangle((left+24,top+40,left+33,bottom-40),radius=4,fill=accent)
        # An editable-card background contains no burned-in quote text.
        path=generated/("editorial-card-"+kind+".png");image.save(path);cards[kind]=path
    matte=Image.new("RGBA",(1080,1920),(0,0,0,0));ImageDraw.Draw(matte).rectangle((0,595,1080,1325),fill=(0,0,0,255))
    matte_path=generated/"editorial-focus.png";matte.save(matte_path)
    card_track=script.append_track(draft.TrackSpec(draft.TrackType.video,CARD_TRACK))
    focus_track=script.append_track(draft.TrackSpec(draft.TrackType.video,FOCUS_TRACK))
    text_track=script.append_track(draft.TrackSpec(draft.TrackType.text,TEXT_TRACK))
    font=draft.FontType.__members__.get("新青年体")
    for i,item in enumerate(ed.get("callouts") or []):
        start=round(item["start_s"]*1e6);length=min(round((item["end_s"]-item["start_s"])*1e6),total_us-start)
        tr=draft.Timerange(start,length)
        seg=draft.VideoSegment(str(cards[item["kind"]]),tr,clip_settings=draft.ClipSettings(alpha=1))
        _envelope(seg,draft,length);script.add_segment(seg,card_track)
        value=item["text"]
        if len(value)>11:
            split=(len(value)+1)//2;value=value[:split]+"\n"+value[split:]
        color=(1,.85,.46) if item["kind"]!="warning" else (1,.66,.48)
        txt=draft.TextSegment(value,tr,font=font,style=draft.TextStyle(size=11.4,bold=True,color=color,align=1,line_spacing=4),clip_settings=draft.ClipSettings(transform_x=.025,transform_y=text_y))
        _envelope(txt,draft,length);txt.add_keyframe(draft.KeyframeProperty.position_y,0,text_y-.025);txt.add_keyframe(draft.KeyframeProperty.position_y,200000,text_y)
        script.add_segment(txt,text_track)
        if i%2==0:
            focus=draft.VideoSegment(str(matte_path),tr,clip_settings=draft.ClipSettings(alpha=.20))
            focus.add_mask(draft.MaskType.圆形,size=.46,invert=True,feather=30)
            _envelope(focus,draft,length,.20);script.add_segment(focus,focus_track)
    sound_track=script.append_track(draft.TrackSpec(draft.TrackType.audio,SOUND_TRACK))
    for item in ed.get("sounds") or []:
        path=context.machine_profile.cache_paths[item["cache_key"]]
        if not path.is_file():raise ValueError("editorial sound cache missing: "+item["cache_key"])
        material=draft.AudioMaterial(str(path))
        start=round(item["start_s"]*1e6);length=min(round(item["duration_s"]*1e6),int(material.duration),total_us-start)
        if length<50000:raise ValueError("editorial sound too short")
        seg=draft.AudioSegment(material,draft.Timerange(start,length),source_timerange=draft.Timerange(0,length),volume=item["volume"])
        seg.add_fade(min(20000,length//4),min(150000,length//3));script.add_segment(seg,sound_track)


def validate_editorial_draft(content, plan, context):
    ed=plan.get("editorial")
    if not ed:return []
    findings=[]
    def error(message):findings.append({"level":"error","message":"editorial draft: "+message})
    tracks={t.get("name"):t for t in content.get("tracks") or []};materials=content.get("materials") or {}
    texts={t.get("id"):t for t in materials.get("texts") or []};audios={a.get("id"):a for a in materials.get("audios") or []}
    text_segments=(tracks.get(TEXT_TRACK) or {}).get("segments") or [];cards=(tracks.get(CARD_TRACK) or {}).get("segments") or []
    callouts=ed.get("callouts") or []
    if len(text_segments)!=len(callouts) or len(cards)!=len(callouts):error("callout/card counts differ from plan")
    for segment,item in zip(text_segments,callouts):
        payload=json.loads((texts.get(segment.get("material_id")) or {}).get("content") or "{}")
        if clean(payload.get("text",""))!=clean(item["text"]):error("quote differs from approved text")
        timerange=segment.get("target_timerange") or {}
        if abs(timerange.get("start",0)/1e6-item["start_s"])>.03 or abs(timerange.get("duration",0)/1e6-(item["end_s"]-item["start_s"]))>.03:error("callout timing mismatch")
        if len(segment.get("common_keyframes") or [])<2:error("callout missing fade/position animation")
    focus=(tracks.get(FOCUS_TRACK) or {}).get("segments") or [];masks={m.get("id") for m in materials.get("masks") or []}
    if len(focus)!=(len(callouts)+1)//2:error("focus layer count differs")
    for s in focus:
        if not masks.intersection(s.get("extra_material_refs") or []):error("focus mask missing")
    segments=(tracks.get(SOUND_TRACK) or {}).get("segments") or [];sounds=ed.get("sounds") or []
    if len(segments)!=len(sounds):error("cue sound count differs")
    def canonical(path):return str(path).replace("\\\\?\\","").replace("\\","/").casefold()
    for s,item in zip(segments,sounds):
        material=audios.get(s.get("material_id")) or {}
        if canonical(material.get("path",""))!=canonical(context.machine_profile.cache_paths[item["cache_key"]]):error("cue sound source mismatch")
        if abs(float(s.get("volume",1))-item["volume"])>.001:error("cue sound gain mismatch")
        if abs(((s.get("target_timerange") or {}).get("start",0))/1e6-item["start_s"])>.02:error("cue sound timing mismatch")
    return findings
