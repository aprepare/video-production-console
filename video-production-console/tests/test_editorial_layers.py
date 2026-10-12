import json
import sys
from pathlib import Path
from types import SimpleNamespace
import wave

sys.path.insert(0,str(Path.home()/'.codex/skills/jianying-montage-draft/scripts'))
import editorial_layers as e
import run_montage_job as job


def sample_plan():
 return {'plan_version':'2.0','project_duration_s':70,'media_mix_policy':{'preset':'scenic_semantic'},'graphics':{'captions':{'items':[{'text':'先把家里的账算清楚','start_s':10,'end_s':14}]}},'editorial':{'version':1,'transition_attachment':'outgoing','highlights_status':'unit','callouts':[{'cue_index':0,'text':'先把家里的账算清楚','kind':'keypoint','start_s':10,'end_s':15.2}],'sounds':[{'start_s':10,'duration_s':.5,'cache_key':'sfx_water_drop','volume':.12}]}}

def test_rejects_hallucinated_quote():
 p=sample_plan();p['editorial']['callouts'][0]['text']='保证赚钱一百万';findings=[];e.validate_editorial(p,findings);assert any('exact substring' in f['message'] for f in findings)

def test_rejects_arbitrary_sound_path():
 p=sample_plan();p['editorial']['sounds'][0]['cache_key']='../../evil';findings=[];e.validate_editorial(p,findings);assert any('whitelisted' in f['message'] for f in findings)

def test_real_layers_are_editable_and_masked(tmp_path):
 import pyJianYingDraft as d
 sound=tmp_path/'cue.wav'
 with wave.open(str(sound),'wb') as w:w.setnchannels(1);w.setsampwidth(2);w.setframerate(48000);w.writeframes(b'\0\0'*48000)
 script=d.DraftFolder(str(tmp_path)).create_draft('layers',1080,1920,30)
 ctx=SimpleNamespace(workspace=tmp_path/'layers',machine_profile=SimpleNamespace(cache_paths={'sfx_water_drop':sound}))
 p=sample_plan();e.build_editorial_layers(script,d,ctx,p,70000000);script.save()
 content=json.loads((ctx.workspace/'draft_content.json').read_text('utf-8'))
 assert e.validate_editorial_draft(content,p,ctx)==[]
 assert len(content['materials'].get('masks',[]))==1
 assert any(t['name']==e.TEXT_TRACK and t['type']=='text' for t in content['tracks'])
 # A missing mask must not pass just because its track is present.
 content['materials']['masks']=[]
 assert any('mask missing' in f['message'] for f in e.validate_editorial_draft(content,p,ctx))

def test_native_transition_attaches_to_outgoing_segment(tmp_path):
 ctx=SimpleNamespace(workspace=tmp_path,machine_profile=SimpleNamespace(cache_paths={'bgm':'bgm.mp3','tr':'transition'}))
 policy={'bgm':{'cache_key':'bgm'},'sfx':[],'transition':{'name':'叠化','type':'transition','effect_id':'322577','resource_id':'6724845717472416269','cache_key':'tr','duration_us':466666,'is_overlap':True}}
 content={'materials':{'audios':[]},'tracks':[{'name':'画面','segments':[{'id':'a'},{'id':'b'},{'id':'c'}]}]}
 (tmp_path/'draft_content.json').write_text(json.dumps(content),encoding='utf-8')
 plan={'editorial':{'transition_attachment':'outgoing'},'timeline':[{'transition':None},{'transition':{'preset':'cross_dissolve'}},{'transition':None}]}
 job.DeterministicDraftBackend()._patch_verified_fields(ctx,policy,plan)
 changed=json.loads((tmp_path/'draft_content.json').read_text('utf-8'));segs=changed['tracks'][0]['segments'];transition_id=changed['materials']['transitions'][0]['id']
 assert transition_id in segs[0]['extra_material_refs']
 assert not segs[1].get('extra_material_refs') and not segs[2].get('extra_material_refs')
