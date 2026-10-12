"""Account-aware card placement, tested against the repository implementation."""
import importlib.util
import json
from pathlib import Path
from types import SimpleNamespace
from PIL import Image

spec = importlib.util.spec_from_file_location('editorial_layout_candidate', Path(__file__).resolve().parents[1]/'tools/jianying_editorial/editorial_layers.py')
e = importlib.util.module_from_spec(spec)
spec.loader.exec_module(e)


def plan(y=0):
 return {'plan_version':'2.0','project_duration_s':70,'media_mix_policy':{'preset':'scenic_semantic'},'style_overrides':{'spoken_v1':{'y':y}},'graphics':{'captions':{'items':[{'text':'先把家里的账算清楚','start_s':10,'end_s':14}]}},'editorial':{'version':1,'transition_attachment':'outgoing','highlights_status':'unit','callouts':[{'cue_index':0,'text':'先把家里的账算清楚','kind':'keypoint','start_s':10,'end_s':15.2}],'sounds':[]}}


def test_default_account_layout_is_unchanged():
 assert e.editorial_card_layout(plan()) == ((76,1384,1004,1578),-.54)


def test_bottom_caption_has_separate_card_region():
 bounds,y=e.editorial_card_layout(plan(-.636))
 assert 595 <= bounds[1] < bounds[3] <= 1325
 assert bounds[3]+100 < (1+.636)*960
 assert 0 < y < .38


def test_actual_card_and_text_follow_account_layout(tmp_path):
 import pyJianYingDraft as d
 p=plan(-.636)
 script=d.DraftFolder(str(tmp_path)).create_draft('account-layout',1080,1920,30)
 ctx=SimpleNamespace(workspace=tmp_path/'account-layout',machine_profile=SimpleNamespace(cache_paths={}))
 e.build_editorial_layers(script,d,ctx,p,70000000);script.save()
 content=json.loads((ctx.workspace/'draft_content.json').read_text('utf-8'))
 track=next(t for t in content['tracks'] if t['name']==e.TEXT_TRACK)
 assert abs(track['segments'][0]['clip']['transform']['y']-.247)<1e-6
 with Image.open(ctx.workspace/'generated_visuals/editorial-card-keypoint.png') as im:
  bbox=im.getchannel('A').getbbox()
 assert bbox[1]==626 and bbox[3]<=822
 assert p['style_overrides']['spoken_v1']['y']==-.636
 assert e.validate_editorial_draft(content,p,ctx)==[]
